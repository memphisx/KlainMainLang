package llvm

import (
	"KlainMainLang/ast"
	"fmt"
	"strconv"
	"strings"
)

func (e *Emitter) emitArrayVarDecl(v *ast.VarDeclaration, ty Type) error {
	elemTy := *ty.ElemType
	// Object-reference array model (TDD-00127): a named array is a *stable slot*
	// (Symbol.Ptr) holding a pointer to a shared heap {data, len} header. The
	// slot is a fresh `alloca ptr` for a local, or the pre-registered module
	// global for a promoted top-level binding (TDD-00093). Allocate the header
	// now and point the slot at it; every branch below then fills the header's
	// data/len fields through ptrName/lenName exactly as it used to fill the two
	// separate allocas — the field addresses are all that changed, so those
	// branches are untouched.
	var slot string
	if e.promotedGlobalDecls[v] {
		if err := e.promotedStorageAgrees(v, ty); err != nil {
			return err
		}
		slot = e.moduleGlobals[v.Name].Ptr
	} else if fsym, ok := e.forwardBoxes[v]; ok {
		// A closure built before this declaration already captured its cell.
		slot = fsym.Ptr
		e.define(v.Name, fsym)
	} else if e.hoistedCaptures[v.Name] {
		// Captured by a nested closure: the slot is a heap cell made here,
		// which dominates every use — not at the capturing closure, which
		// may sit in one branch (promoteCaptureToCell's work, done early).
		slot = e.boxHoistedCapture(v.Name, TypePtr, "null", v.Kind == "const", v.Kind == "var")
		sym, _ := e.lookup(v.Name)
		sym.Ty = ty
		e.define(v.Name, sym)
	} else {
		slot = e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
		if v.Kind == "var" {
			// A skippable `var` (hoistedVarMaySkip widened it to `T[] |
			// undefined`) reads the null header — absent — on the path where this
			// declaration never ran (ADR-01057).
			e.emitAlloca(fmt.Sprintf("store ptr null, ptr %s, align 8", slot))
		}
		e.define(v.Name, Symbol{Ptr: slot, Ty: ty, IsConst: v.Kind == "const"})
	}
	// `let a: T[] | null = null` (or no initializer at all): the binding holds no
	// array — a null header, which every read of a nullable binding guards.
	if _, isNullLit := v.Init.(*ast.NullLiteral); ty.Nullable() && (isNullLit || v.Init == nil) {
		e.heapStore("ptr", "null", slot, 8)
		return nil
	}
	ptrName := e.newArrayHeader("null", "0")
	e.heapStore("ptr", ptrName, slot, 8)
	lenName := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 1", lenName, arrayHeaderTy, ptrName))

	if v.Init == nil {
		e.heapStore("ptr", "null", ptrName, 8)
		e.heapStore("i64", "0", lenName, 8)
		return nil
	}

	// Dynamic-size array: new Array<T>(runtimeSize) — built via
	// emitNewArraySizedAggregate (TDD-00028's general-expression producer)
	// and its header taken here, the same "build the array value once, every
	// consumer reads it" shape every branch below now shares.
	if na, ok := v.Init.(*ast.NewArrayExpression); ok {
		val, err := e.emitNewArraySizedAggregate(na, elemTy)
		if err != nil {
			return err
		}
		e.heapStore("ptr", val.Ref, slot, 8)
		return nil
	}

	// TypedArray construction: new Int8Array(...)/.../new Float64Array(...)
	// — see docs/tdd/TDD-00018.md. Checked before the generic expression
	// case below (TypedArray construction is its own AST node, not a call),
	// mirroring exactly how NewArrayExpression is handled just above.
	if nta, ok := v.Init.(*ast.NewTypedArrayExpression); ok {
		return e.emitNewTypedArrayVarDecl(nta, ptrName, lenName, elemTy)
	}

	// Array literal: built via emitArrayLiteralAggregate (TDD-00028), hinted
	// against this var-decl's own resolved element type, and extracted here
	// — same shape as every other branch. Note this also correctly handles
	// nested array-literal elements (an ArrayLiteral element of lit is
	// itself resolved through emitExprWithObjectHint inside
	// emitArrayLiteralData/emitSpreadArrayLitData, which TDD-00028 also
	// makes work instead of erroring).
	if lit, ok := v.Init.(*ast.ArrayLiteral); ok {
		val, err := e.emitArrayLiteralAggregate(lit, &elemTy)
		if err != nil {
			return err
		}
		e.heapStore("ptr", val.Ref, slot, 8)
		return nil
	}

	// JSON.parse / Response.json() into an array type (`const xs: T[] =
	// JSON.parse(...)`, or the same awaited): pass the array type through so
	// projection builds a T[] array value (TDD-00077 P3), mirroring the scalar/
	// object var-decl's own type-context branch. The generic emitExpr path below
	// would call it with no type context.
	if val, ok, err := e.emitDeclJSONProjection(v.Init, ty); ok {
		if err != nil {
			return err
		}
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", val.Ref, slot))
		return nil
	}

	// Any other expression that produces an array value — a
	// function call, an index expression (e.g. groupMap["key"]), a Map/Set
	// method result, etc.
	val, err := e.emitExpr(v.Init)
	if err != nil {
		return err
	}
	if val.Ty.IsDynamic() {
		// `const xs: T[] = anyValue` (an assertion in TS), or the array member
		// of a union the checker narrowed to it: the box's array.
		val = e.emitUnboxBoxToType(val.Ref, ty)
	}
	if val.Ty.IsClass() && !val.Ty.IsArray() || val.Ty.IsTuple() && !val.Ty.IsArray() {
		// An instance of a class extending Array: the array its body is; a
		// tuple: its elements.
		val = e.convert(val, ty)
	}
	if !val.Ty.IsArray() {
		return fmt.Errorf("%d:%d: array variable must be initialized with an array expression", v.GetPos().Line, v.GetPos().Col)
	}
	// A concrete array into an `any[]` binding (or the reverse): its elements
	// converted, in a copy.
	if conv, ok := e.convertArrayElems(val, ty); ok {
		val = conv
	}
	// Reference semantics (TDD-00213 Stage 1): if the initializer is an existing
	// header-backed array (`let a = b`, or any expression carrying a live header),
	// point this binding's slot at the SAME header cell so the two bindings alias
	// — a `push` through either is visible through the other, and they are `===`.
	// The fresh header minted above is discarded; a genuinely new array
	// expression (literal/new/slice/HOF) keeps it and fills it.
	// A nullable-array initializer (a `Map<K,T[]>` get that may miss) binds a
	// `T[] | undefined`: remember the nullability on the symbol so a later
	// identifier load rebuilds it null-safely and its truthiness tests the
	// header (see emitExpr's array-identifier path). Only meaningful for a named
	// local (a promoted global keeps its declared type). A non-nullable
	// annotation (`const a: T[] = c ? x : y`) is the binding's type whatever
	// the initializer's branches look like.
	if val.Ty.Nullable() && !e.promotedGlobalDecls[v] && (v.TypeAnnot == nil || v.TypeAnnot.Nullable) {
		if sym, ok := e.lookup(v.Name); ok {
			sym.Ty.setNullable(true)
			e.define(v.Name, sym)
		}
	}
	e.heapStore("ptr", val.Ref, slot, 8)
	return nil
}

// loadArrayFieldValue reads an array-typed struct field: the field slot holds
// the array's shared header (TDD-00213 Stage 2), which is the value, so a
// binding of the field aliases it. A null header (a calloc'd field) is absent
// for a nullable type, else the empty array. fieldSlot is the field's GEP
// address.
func (e *Emitter) loadArrayFieldValue(fieldSlot string, fieldTy Type) Value {
	header := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", header, fieldSlot))
	if fieldTy.Nullable() {
		return Value{Ref: header, Ty: fieldTy}
	}
	return Value{Ref: e.nonNullArrayHeader(header), Ty: fieldTy}
}

// nonNullArrayHeader is header, or a fresh empty array's header when it is
// null.
func (e *Emitter) nonNullArrayHeader(header string) string {
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", header, slot))
	nullL := e.freshLabel("arrhdr.null")
	doneL := e.freshLabel("arrhdr.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", e.ptrIsNull(header), nullL, doneL))
	e.emitLabel(nullL)
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", e.newArrayHeader("null", "0"), slot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	out := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", out, slot))
	return out
}

// loadArraySlotAggregate reads an array from a slot that is always written
// before it is read (a generator/promise/iterator state-struct slot): the
// header it holds, with no null guard and no basic blocks, so it is safe
// inside a state machine's linear control flow.
func (e *Emitter) loadArraySlotAggregate(slot string, ty Type) Value {
	header := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", header, slot))
	return Value{Ref: header, Ty: ty}
}

// arrayReturnHeader is the header pointer an array value crosses the `ret ptr`
// array ABI (TDD-00213 Stage 3) and every header-pointer slot with: the value
// itself, since an array value is its shared header (TDD-00230 P3.3).
func (e *Emitter) arrayReturnHeader(val Value) string {
	return val.Ref
}

// arrayValueFromHeaderReg is the array value a header pointer is (an array
// `ret ptr` call result, TDD-00213 Stage 3).
func (e *Emitter) arrayValueFromHeaderReg(headerReg string, ty Type) Value {
	return Value{Ref: headerReg, Ty: ty}
}

// storeArrayFieldHeader stores an array value into a header-pointer field slot
// (TDD-00213 Stage 2): the value is its header, so `obj.arr = xs` aliases xs,
// and an absent value stores the null header.
func (e *Emitter) storeArrayFieldHeader(fieldSlot string, val Value) {
	e.heapStore("ptr", val.Ref, fieldSlot, 8)
}

// boxArrayValue is the header pointer an array value stores into an 8-byte
// slot (a nested-array element, TDD-00029): the value itself.
func (e *Emitter) boxArrayValue(val Value) string {
	return val.Ref
}

// mkArray is the array value of type ty over a data pointer and length: a
// fresh header holding them.
func (e *Emitter) mkArray(dataReg, lenReg string, ty Type) Value {
	return Value{Ref: e.newArrayHeader(dataReg, lenReg), Ty: ty}
}

// arrayFromAgg is the array value of type ty over a runtime routine's
// {data, len} aggregate result: a fresh header holding them.
func (e *Emitter) arrayFromAgg(aggReg string, ty Type) Value {
	d, n := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", d, aggReg))
	e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 1", n, aggReg))
	return e.mkArray(d, n, ty)
}

// arrayAggOf is an array value's current {data, len} as an aggregate, for a
// runtime routine that takes one by value.
func (e *Emitter) arrayAggOf(v Value) string {
	d, n := e.arrayParts(v)
	a0, a1 := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", a0, d))
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %s, 1", a1, a0, n))
	return a1
}

// storeArrayPartsInto writes an array value's current data pointer and length
// into an inline header at hdr (a class-extends-Array instance's body).
func (e *Emitter) storeArrayPartsInto(v Value, hdr string) {
	d, n := e.arrayParts(v)
	lp := e.freshReg()
	e.heapStore("ptr", d, hdr, 8)
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 1", lp, arrayHeaderTy, hdr))
	e.heapStore("i64", n, lp, 8)
	e.storeArrayHolesNull(hdr)
}

// arrayHolesOff is the offset of an array header's holes word (TDD-00226):
// null for a packed array, else the bitmap of its holes.
const arrayHolesOff = 16

// arrayHeaderSize is an array header's size past its header word:
// {data, len, holes}.
const arrayHeaderSize = 24

// storeArrayHolesNull marks the header at hdr packed.
func (e *Emitter) storeArrayHolesNull(hdr string) {
	hp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %d", hp, hdr, arrayHolesOff))
	e.heapStore("ptr", "null", hp, 8)
}

// arrayParts loads an array value's current data pointer and length from its
// header. A null header (an absent array) reads as the empty array.
func (e *Emitter) arrayParts(v Value) (data, n string) {
	h := e.absentSafeArrayHeader(v.Ref)
	data, n = e.freshReg(), e.freshReg()
	lp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", data, h))
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 1", lp, arrayHeaderTy, h))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", n, lp))
	return data, n
}

// arrayLen loads an array value's current length.
func (e *Emitter) arrayLen(v Value) string {
	_, n := e.arrayParts(v)
	return n
}

// arrayData loads an array value's current data pointer.
func (e *Emitter) arrayData(v Value) string {
	d, _ := e.arrayParts(v)
	return d
}

// absentSafeArrayHeader is header, or the shared read-only empty cell when it
// is null — branchless, so it is safe inside a state machine's linear flow.
func (e *Emitter) absentSafeArrayHeader(header string) string {
	if header == "null" || header == "zeroinitializer" {
		header = "null"
	}
	if !e.usedAbsentArrayCell {
		e.usedAbsentArrayCell = true
		e.emitGlobal(fmt.Sprintf("@__kml_absent_array_cell = internal constant {i64, ptr, i64, ptr} {i64 %d, ptr null, i64 0, ptr null}, align 8", e.arrayHeaderWord()|kmlHdrImmortal))
	}
	src := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr getelementptr (i8, ptr @__kml_absent_array_cell, i64 8), ptr %s", src, e.ptrIsNull(header), header))
	return src
}

// arrayHeaderTy is the LLVM type of a named array's shared header cell — a
// heap {data, len} record (TDD-00127). Under the object-reference array model,
// a named array variable's Symbol.Ptr is a *stable slot* (an `alloca ptr`, or a
// module global) holding a pointer to one of these headers. Reads/mutators
// re-derive the data/len field addresses from the current header
// (arrayDataLenSlots); a reassignment swaps the whole header pointer in the
// slot (aliasing, faithful JS reference semantics); an in-place mutator
// (push/splice/...) writes a new data pointer/length *through* the header, so it
// propagates to every alias — including a callee the array was passed to, since
// the header pointer is what crosses the call boundary. (No capacity field yet:
// amortized-growth perf work is deliberately deferred — see the TDD.)
const arrayHeaderTy = "{ ptr, i64 }"

// newArrayHeader mallocs a fresh {ptr, i64} header initialised from a data
// pointer and length, returning the header pointer (== the address of its data
// field, since field 0 is at offset 0).
func (e *Emitter) newArrayHeader(dataReg, lenReg string) string {
	h := e.allocArrayHeader()
	lp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 1", lp, arrayHeaderTy, h))
	e.heapStore("ptr", dataReg, h, 8)
	e.heapStore("i64", lenReg, lp, 8)
	e.storeArrayHolesNull(h)
	return h
}

// arrayHeaderWord is the header word before every array header: the Array
// host's type id (TDD-00247 stage 5), at header - 8 as for Date and RegExp.
func (e *Emitter) arrayHeaderWord() int64 {
	return e.stableTypeID("HArray") | hostTypeIDFlag
}

// argumentsHeaderWord is an Arguments object's header word: an array
// header (its elements, its length) that is no Array (Array.isArray,
// constructor, inspection, JSON).
func (e *Emitter) argumentsHeaderWord() int64 {
	return e.stableTypeID("HArguments") | hostTypeIDFlag
}

// emitArrayIsArguments is whether the array header hdr (an any[] value) is
// an Arguments object's, by its word; a null header is not.
func (e *Emitter) emitArrayIsArguments(hdr string) string {
	isNull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, hdr))
	r := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i1, align 1", r))
	e.emitInstr(fmt.Sprintf("store i1 false, ptr %s, align 1", r))
	readL, doneL := e.freshLabel("isargs.read"), e.freshLabel("isargs.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNull, doneL, readL))
	e.emitLabel(readL)
	wp, w, is := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 -8", wp, hdr))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", w, wp))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", is, e.headerIdentity(w), e.argumentsHeaderWord()))
	e.emitInstr(fmt.Sprintf("store i1 %s, ptr %s, align 1", is, r))
	e.emitTerminator("br label %" + doneL)
	e.emitLabel(doneL)
	out := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i1, ptr %s, align 1", out, r))
	return out
}

// emitArgumentsBranch is a ptr-valued result for the array value v: args()
// when v holds an Arguments object, else plain(). Without an any[] type,
// plain() alone.
func (e *Emitter) emitArgumentsBranch(v Value, args, plain func() (Value, error)) (Value, error) {
	if !mayBeArguments(v.Ty) || v.Ref == "zeroinitializer" {
		return plain()
	}
	out := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", out))
	argsL, plainL, doneL := e.freshLabel("args.yes"), e.freshLabel("args.no"), e.freshLabel("args.join")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", e.emitArrayIsArguments(v.Ref), argsL, plainL))
	for i, fn := range []func() (Value, error){args, plain} {
		if i == 0 {
			e.emitLabel(argsL)
		} else {
			e.emitLabel(plainL)
		}
		r, err := fn()
		if err != nil {
			return Value{}, err
		}
		if !e.blockDone {
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", r.Ref, out))
			e.emitTerminator("br label %" + doneL)
		}
	}
	e.emitLabel(doneL)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", r, out))
	return Value{Ref: r, Ty: TypePtr}, nil
}

// mayBeArguments reports whether t, an array type, may hold an Arguments
// object: an any[] (the type `arguments` has).
func mayBeArguments(t Type) bool {
	return t.IsArray() && !t.IsTypedArray() && !t.IsBuffer() && t.ElemType != nil && t.ElemType.IsDynamic() && !t.IsTuple()
}

// allocArrayHeader mallocs an array header with its header word, returning
// the header (past the word); its {data, len} fields are left to the caller.
func (e *Emitter) allocArrayHeader() string {
	return e.allocArrayHeaderSized(arrayHeaderSize, false)
}

// allocArrayHeaderSized is allocArrayHeader for a header of size bytes (an
// exec() result's carries its index, input and groups after {data, len}),
// zeroed when zero is set.
func (e *Emitter) allocArrayHeaderSized(size int, zero bool) string {
	base, h := e.freshReg(), e.freshReg()
	if zero {
		e.ensureCalloc()
		e.heapCallocInto(base, "1", fmt.Sprint(size+8))
	} else {
		e.ensureMalloc()
		e.heapMallocInto(base, fmt.Sprint(size+8))
	}
	e.heapStore("i64", fmt.Sprint(e.arrayHeaderWord()), base, 8)
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 8", h, base))
	return h
}

// arrayDataLenSlots derives the addresses of the data and length fields of an
// array symbol's *current* header. dataSlot is the header pointer itself (field
// 0 at offset 0); lenSlot is field 1. Every named-array read and in-place
// mutator goes through here, so it always observes the header the slot points
// at *now* (post any reassignment), and writes through it propagate to aliases.
func (e *Emitter) arrayDataLenSlots(sym Symbol) (dataSlot, lenSlot string) {
	h := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", h, sym.Ptr))
	// undefined in a binding typed as an array (through `any`) is a null
	// header: reads see the shared empty one. Writes guard first
	// (resolveArrayMutLoc), so it is never written.
	h = e.nullSafeArrayHeader(h)
	lenSlot = e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 1", lenSlot, arrayHeaderTy, h))
	return h, lenSlot
}

// nullSafeArrayHeader is header, or a shared empty header when it is null.
func (e *Emitter) nullSafeArrayHeader(header string) string {
	if !e.fnDecls["__kml_empty_arr_hdr"] {
		e.fnDecls["__kml_empty_arr_hdr"] = true
		e.emitGlobal(fmt.Sprintf("@__kml_empty_arr_cell = internal global {i64, ptr, i64, ptr} {i64 %d, ptr null, i64 0, ptr null}, align 8", e.arrayHeaderWord()|kmlHdrImmortal))
	}
	isNull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, header))
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr getelementptr (i8, ptr @__kml_empty_arr_cell, i64 8), ptr %s", r, isNull, header))
	return r
}

// newArrayHeaderSlot allocates a stable stack slot (`alloca ptr`) holding a
// pointer to a fresh header built from (dataReg, lenReg), returning the slot —
// the storage of a named array variable (object-reference model, TDD-00127).
func (e *Emitter) newArrayHeaderSlot(dataReg, lenReg string) string {
	header := e.newArrayHeader(dataReg, lenReg)
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", header, slot))
	return slot
}

// newArrayHeaderSlotFromAggregate returns a stable slot (`alloca ptr`) holding
// an array value's header — the storage of a named array variable (TDD-00127).
func (e *Emitter) newArrayHeaderSlotFromAggregate(val Value) string {
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", val.Ref, slot))
	return slot
}

// arrayArgFromAggregate produces the (header, len) pair that passes an array
// value as a (ptr, i64) call argument (TDD-00127): its own header, so a
// mutation inside the callee reaches the caller. A null header (an absent
// array) passes as is, and the callee sees undefined.
func (e *Emitter) arrayArgFromAggregate(val Value) (header, lenReg string) {
	return val.Ref, e.arrayLen(val)
}

// packArrayArg produces the (headerReg, lenReg) pair to pass an array argument
// under the object-reference ABI (TDD-00127): the value's header, so an
// in-place mutation inside the callee propagates back to the caller. An absent
// array crosses the call as a null header: the callee reads it as undefined,
// with length 0.
func (e *Emitter) packArrayArg(arg ast.Expression, val Value, paramTy Type) (header, lenReg string) {
	if val.Ref == "" {
		// A named array: its binding's header.
		if v, err := e.emitExpr(arg); err == nil {
			val = v
		}
	}
	return e.arrayArgFromAggregate(val)
}

// emptyArrayArgHeader returns a fresh {null, 0} header for an empty array/rest
// argument, so the callee reads length 0 rather than dereferencing a null
// header (TDD-00127).
func (e *Emitter) emptyArrayArgHeader() string {
	return e.newArrayHeader("null", "0")
}

// omittedArrayArgHeader is the header an omitted `xs?: T[]` argument passes: a
// null header — the absent array, `undefined` in the callee — when the
// parameter's type can say so, else the empty array it has always been.
func (e *Emitter) omittedArrayArgHeader(paramTy Type) string {
	if paramTy.Nullable() {
		return "null"
	}
	return e.emptyArrayArgHeader()
}

// emitAbsentArrayValue is the `null`/`undefined` value of a nullable array
// type: the null header.
func (e *Emitter) emitAbsentArrayValue(ty Type) Value {
	return Value{Ref: "null", Ty: ty}
}

// emitArrayIsAbsent yields the i1 "this nullable array value is
// null/undefined": its header is null. A present array, even an empty one,
// has a header.
func (e *Emitter) emitArrayIsAbsent(v Value) string {
	if v.Ref == "zeroinitializer" {
		return "true"
	}
	// The null header, or nullRef (null unboxed from `any`).
	isRef, r := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, %s", isRef, v.Ref, e.nullRef()))
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", r, e.ptrIsNull(v.Ref), isRef))
	return r
}

// iterableDisplayName renders an iterated expression the way Node names it in
// "… is not iterable": `xs`, `o.tags`, `this.items`, `f(...)`.
func (e *Emitter) iterableDisplayName(expr ast.Expression) string {
	switch ex := expr.(type) {
	case *ast.Identifier:
		return e.displayName(ex.Name)
	case *ast.ThisExpression:
		return "this"
	case *ast.MemberExpression:
		return e.iterableDisplayName(ex.Object) + "." + ex.Property
	case *ast.NonNullExpression:
		return e.iterableDisplayName(ex.Arg)
	case *ast.CallExpression:
		return e.iterableDisplayName(ex.Callee) + " is not a function or its return value"
	}
	return "object"
}

// displayName is a binding's name as a message shows it: a binding the
// compiler synthesized (bindValue, a host body) shows the expression it
// stands for, or "object".
func (e *Emitter) displayName(name string) string {
	if d, ok := e.synthDisplay[name]; ok {
		return d
	}
	if strings.HasPrefix(name, "__kml_bound_") || strings.HasPrefix(name, "__kml_hostbody_") || strings.HasPrefix(name, "__kml_dynarr_") {
		return "object"
	}
	return demangleModuleName(name)
}

// emitNotIterableValueGuard is emitNotIterableGuard for an array *value* (a
// field read, a call result) of a nullable type.
func (e *Emitter) emitNotIterableValueGuard(expr ast.Expression, v Value) {
	if !v.Ty.IsArray() || !v.Ty.Nullable() || e.blockDone {
		return
	}
	e.ensureNullDerefThrow()
	throwL := e.freshLabel("notiter.throw")
	okL := e.freshLabel("notiter.ok")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", e.emitArrayIsAbsent(v), throwL, okL))
	e.emitLabel(throwL)
	e.emitInstr(fmt.Sprintf("call void @__kml_throw_nullderef(ptr %s)", e.internString(e.iterableDisplayName(expr)+" is not iterable")))
	e.emitTerminator("unreachable")
	e.emitLabel(okL)
}

// emitNotIterableGuard throws Node's `TypeError: xs is not iterable` when the
// nullable array binding sym (named name) holds no array — the check `for…of`
// and spread make before touching the header.
func (e *Emitter) emitNotIterableGuard(name string, sym Symbol) {
	e.emitArrayBindingGuard(sym, e.displayName(name)+" is not iterable")
}

// emitArrayBindingGuard throws a TypeError with msg when the array binding
// sym holds no array: a nullable one's absence, or undefined that reached a
// binding typed as an array through `any` (its header is null either way).
func (e *Emitter) emitArrayBindingGuard(sym Symbol, msg string) {
	if e.blockDone || sym.Ptr == "" {
		return
	}
	h := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", h, sym.Ptr))
	e.ensureNullDerefThrow()
	throwL := e.freshLabel("notiter.throw")
	okL := e.freshLabel("notiter.ok")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", e.ptrIsNull(h), throwL, okL))
	e.emitLabel(throwL)
	e.emitInstr(fmt.Sprintf("call void @__kml_throw_nullderef(ptr %s)", e.internString(msg)))
	e.emitTerminator("unreachable")
	e.emitLabel(okL)
}

// bindArrayParamPattern binds an array parameter, then unpacks a rest
// object pattern (`...{ length }`) over the collected array.
func (e *Emitter) bindArrayParamPattern(p ast.Param, pty Type) error {
	e.bindArrayParam(p.Name, pty)
	if p.ObjectPattern == nil {
		return nil
	}
	return e.emitStmt(ast.NewObjectDestructuring("let", p.ObjectPattern, ast.NewIdentifier(p.Name, p.Pos), p.Pos))
}

// bindArrayParam binds an array-typed function/method/closure parameter under
// the object-reference model (TDD-00127). The incoming `%p_<name>_ptr` argument
// is the caller's {data, len} header pointer; a stable slot (`%v_<name>_ptr`)
// holds it, so in-place mutations (push/splice) propagate back to the caller
// while a whole-variable reassignment rebinds only this frame. The companion
// `%p_<name>_len` argument is redundant (length lives in the header) and left
// unused — kept only so the two-word (ptr, i64) array ABI is unchanged.
func (e *Emitter) bindArrayParam(name string, pty Type) {
	if e.hoistedCaptures[name] {
		// Captured by a nested closure: a heap cell at entry, which dominates
		// the whole body, not one made at the capture site.
		e.boxHoistedCapture(name, TypePtr, "%p_"+name+"_ptr", false, true)
		sym, _ := e.lookup(name)
		sym.Ty = pty
		e.define(name, sym)
		return
	}
	slot := "%v_" + name + "_ptr"
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
	e.emitInstr(fmt.Sprintf("store ptr %%p_%s_ptr, ptr %s, align 8", name, slot))
	e.define(name, Symbol{Ptr: slot, Ty: pty})
}

// unboxArrayValue reads a nested-array element's header (TDD-00029).
func (e *Emitter) unboxArrayValue(boxPtr string, elemTy Type) Value {
	return Value{Ref: boxPtr, Ty: elemTy}
}

// loadArrayHeaderOrAbsent is the array value a possibly-null header is.
func (e *Emitter) loadArrayHeaderOrAbsent(header string, ty Type) Value {
	return Value{Ref: header, Ty: ty}
}

// loadArrayElem loads the value at a GEP'd array-backing-buffer slot
// (gepReg, of type elemTy). For a nested-array element (elemTy.IsArray) the
// slot holds a box pointer (see boxArrayValue) that's transparently unboxed
// here; every other element type loads directly, unchanged from before
// TDD-00029. Use this instead of a raw `load elemTy.IR, ptr gepReg` at any
// array-element-read call site so nested arrays work for free.
func (e *Emitter) loadArrayElem(gepReg string, elemTy Type) Value {
	// Flat-array element (TDD-00134 Stage 2): the slot IS the struct — the
	// interior pointer is the object value (a view into the buffer).
	if elemTy.Inline {
		elemTy.Inline = false
		return Value{Ref: gepReg, Ty: elemTy}
	}
	if elemTy.IsArray() {
		boxPtr := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", boxPtr, gepReg))
		return e.unboxArrayValue(boxPtr, elemTy)
	}
	reg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", reg, elemTy.IR, gepReg, elemTy.Align()))
	return Value{Ref: reg, Ty: elemTy}
}

// loadArrayElemMaybeNull is loadArrayElem for a slot that may itself hold a
// null box pointer — a "no match" sentinel (.find()'s pre-loop zero-init):
// the null header, the absent array.
func (e *Emitter) loadArrayElemMaybeNull(slotPtr string, elemTy Type) Value {
	boxPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", boxPtr, slotPtr))
	return Value{Ref: boxPtr, Ty: elemTy}
}

// storeArrayElem stores val (of type elemTy) into a GEP'd array-backing-
// buffer slot. For a nested-array element, val's header pointer is
// stored in the slot (see boxArrayValue); every other element type stores directly, unchanged
// from before TDD-00029. Use this instead of a raw
// `store elemTy.IR val.Ref, ptr gepReg` at any array-element-write call site.
func (e *Emitter) storeArrayElem(gepReg string, elemTy Type, val Value) {
	// Flat-array element (TDD-00134 Stage 2): copy the value's fields into
	// the inline slot — value semantics, the @value opt-in's whole point.
	if elemTy.Inline {
		e.ensureMemcpy()
		e.emitInstr(fmt.Sprintf("call void @memcpy(ptr %s, ptr %s, i64 %d)", gepReg, val.Ref, elemTy.StructSize()))
		return
	}
	if elemTy.IsArray() && isSelfDescribingBox(val.Ty) {
		// An `any` element into an array-of-arrays (or Buffer[]): the box's
		// array.
		val = e.emitUnboxBoxToType(val.Ref, elemTy)
	}
	if elemTy.IsArray() {
		// Share the value's live header when it has one (reference semantics:
		// `m[i] = existingArray` aliases, matching storeArrayFieldHeader); a
		// new array expression mints its own box. A null carried header (an
		// absent field read) falls back to the fresh box, since element reads
		// unbox unguarded.
		box := val.Ref
		if box == "" {
			box = e.arrayReturnHeader(val) // a null box for an absent value
		} else if val.Ty.Nullable() {
			// keep a carried null header: the element is absent
		} else {
			fresh := e.boxArrayValue(val)
			isNull := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, box))
			sel := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", sel, isNull, fresh, box))
			box = sel
		}
		e.heapStore("ptr", box, gepReg, 8)
		return
	}
	e.heapStore(elemTy.IR, val.Ref, gepReg, elemTy.Align())
}

// emitBoxCopyLoop copies srcLen elements from srcPtr (a buffer of srcElemTy
// scalars) into dst (a boxed-element `any[]` i64 buffer), boxing each source
// value to `any` on the way. Used where a concrete-element array's contents
// flow into a boxed-element array (concat into `any[]`, TDD-00200) and a raw
// byte-copy would leave the source scalars un-tagged in the box slots.
func (e *Emitter) emitBoxCopyLoop(dst, srcPtr, srcLen string, srcElemTy Type) {
	idxAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idxAlloca))
	condL := e.freshLabel("boxcopy.cond")
	bodyL := e.freshLabel("boxcopy.body")
	doneL := e.freshLabel("boxcopy.done")
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	idxVal := e.freshReg()
	at := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idxVal, idxAlloca))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", at, idxVal, srcLen))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", at, doneL, bodyL))
	e.emitLabel(bodyL)
	srcGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", srcGep, srcElemTy.IR, srcPtr, idxVal))
	srcElem := e.loadArrayElem(srcGep, srcElemTy)
	boxed := e.convert(srcElem, TypeAny)
	dstGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %s, i64 %s", dstGep, dst, idxVal))
	e.heapStore("i64", boxed.Ref, dstGep, 8)
	idxNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", idxNext, idxVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", idxNext, idxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(doneL)
}

// emitScalarZero emits a zero constant of the given scalar type as a Value.
// For an array type the caller should use mkArray / the null header instead —
// this helper is for scalar-typed zeros only.
func (e *Emitter) emitScalarZero(t Type) Value {
	zero := t.zeroLiteral()
	reg := e.freshReg()
	switch {
	case t.Float:
		e.emitInstr(fmt.Sprintf("%s = fadd %s %s, %s", reg, t.IR, zero, zero))
	case t.IR == "ptr":
		e.emitInstr(fmt.Sprintf("%s = inttoptr i64 0 to ptr", reg))
	default:
		e.emitInstr(fmt.Sprintf("%s = or %s %s, %s", reg, t.IR, zero, zero))
	}
	return Value{Ref: reg, Ty: t}
}

// emitSpreadArrayLitData handles array literals that contain one or more
// spread elements: computes total length at runtime, allocates one
// contiguous buffer, and fills it using a write cursor (memcpy per spread,
// store per static element), returning the data pointer and length operands
// rather than storing into caller-supplied allocas — shared by
// emitArrayVarDecl (which stores the result into its own header's fields) and
// emitArrayLiteralAggregate (TDD-00028, which builds a fresh array header
// from it instead), so there's exactly one spread-array-literal
// implementation rather than one per caller shape.
func (e *Emitter) emitSpreadArrayLitData(lit *ast.ArrayLiteral, elemTy Type) (dataReg, lenReg string, err error) {
	// Count static (non-spread) elements.
	staticCount := int64(0)
	for _, elem := range lit.Elements {
		if _, ok := elem.(*ast.SpreadElement); !ok {
			staticCount++
		}
	}

	// Pre-evaluate each spread's source array ONCE — resolveArrayForHOF handles
	// both an array variable and any array-valued *expression* (`[...m.keys()]`,
	// `[...arr.slice(1)]`, `[...[1,2]]`), returning its data ptr + length. The
	// resulting SSA regs dominate both loops below, so each spread is evaluated
	// exactly once.
	type spreadSrc struct {
		ptr, length string
		elem        Type
	}
	spreadOf := map[*ast.SpreadElement]spreadSrc{}
	for _, elem := range lit.Elements {
		sp, ok := elem.(*ast.SpreadElement)
		if !ok {
			continue
		}
		arg := e.collectionSpreadSource(sp.Arg)
		if id, ok := sp.Arg.(*ast.Identifier); ok {
			if sym, found := e.lookup(id.Name); found && sym.Ty.IsFlatArray() {
				return "", "", fmt.Errorf("%d:%d: a @value array supports index read/write, .length, for...of, and .push — spreading '%s' needs a regular (pointer-element) array", sp.GetPos().Line, sp.GetPos().Col, id.Name)
			}
		}
		// Spreading a string (`[..."abc"]`) yields its characters — materialize
		// the char array once and hand its ptr+len to the copy loops.
		if at := e.inferExprType(arg); isStringTy(at) && !at.IsArray() && !at.IsClass() && !at.IsObject() {
			sv, verr := e.emitExpr(arg)
			if verr != nil {
				return "", "", verr
			}
			chars := e.emitStringToCharArray(sv)
			sp0 := e.freshReg()
			sl0 := e.freshReg()
			sp0 = e.arrayData(chars)
			sl0 = e.arrayLen(chars)
			spreadOf[sp] = spreadSrc{ptr: sp0, length: sl0, elem: TypePtr}
			continue
		}
		srcPtr, srcLen, srcElem, rerr := e.resolveArrayForHOF(arg, sp.GetPos())
		if rerr != nil {
			return "", "", rerr
		}
		spreadOf[sp] = spreadSrc{ptr: srcPtr, length: srcLen, elem: srcElem}
	}

	// Compute runtime total = staticCount + sum(spread.length).
	totalReg := fmt.Sprintf("%d", staticCount)
	for _, elem := range lit.Elements {
		sp, ok := elem.(*ast.SpreadElement)
		if !ok {
			continue
		}
		newTotal := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = add i64 %s, %s", newTotal, totalReg, spreadOf[sp].length))
		totalReg = newTotal
	}

	// Allocate the buffer.
	e.ensureMalloc()
	bytesReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %d", bytesReg, totalReg, elemTy.Align()))
	dataReg = e.freshReg()
	e.heapMallocInto(dataReg, bytesReg)

	// Write cursor.
	cursorPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", cursorPtr))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", cursorPtr))

	for _, elem := range lit.Elements {
		if sp, ok := elem.(*ast.SpreadElement); ok {
			src := spreadOf[sp] // pre-evaluated above
			srcPtr, srcLen := src.ptr, src.length
			// GEP to cursor position in dest.
			cVal := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", cVal, cursorPtr))
			dstReg := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", dstReg, elemTy.IR, dataReg, cVal))
			// Concrete elements spread into box elements (`[...nums]` as
			// any[]) are boxed one by one; a copy would reinterpret them.
			if elemTy.IsDynamic() && !elemTy.IsArray() && src.elem.IR != "" && !src.elem.IsDynamic() {
				if err := e.emitSpreadBoxLoop(srcPtr, srcLen, src.elem, dstReg); err != nil {
					return "", "", err
				}
				newC := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = add i64 %s, %s", newC, cVal, srcLen))
				e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", newC, cursorPtr))
				continue
			}
			// Box elements (a protocol iterable's values) into concrete
			// elements are unboxed one by one.
			if src.elem.IsDynamic() && !src.elem.IsArray() && !elemTy.IsDynamic() && elemTy.IR != "" {
				if err := e.emitSpreadUnboxLoop(srcPtr, srcLen, elemTy, dstReg); err != nil {
					return "", "", err
				}
				newC := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = add i64 %s, %s", newC, cVal, srcLen))
				e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", newC, cursorPtr))
				continue
			}
			// bytes = len * elemSize
			copyBytes := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %d", copyBytes, srcLen, elemTy.Align()))
			e.ensureMemcpy()
			e.emitInstr(fmt.Sprintf("call void @memcpy(ptr %s, ptr %s, i64 %s)", dstReg, srcPtr, copyBytes))
			// Advance cursor.
			newC := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = add i64 %s, %s", newC, cVal, srcLen))
			e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", newC, cursorPtr))
		} else {
			if nl, ok := elem.(*ast.NullLiteral); ok && nl.Elision {
				// An elision after a spread: a hole at the cursor's index.
				at := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", at, cursorPtr))
				e.spreadElisions = append(e.spreadElisions, at)
			}
			// Static element.
			val, verr := e.emitExprWithObjectHint(elem, elemTy)
			if verr != nil {
				return "", "", verr
			}
			if elemTy.UnionMembers != nil {
				if val, verr = e.convertChecked(val, elemTy, elem.GetPos(), "array element"); verr != nil {
					return "", "", verr
				}
			}
			val = e.convert(val, elemTy)
			cVal := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", cVal, cursorPtr))
			gepReg := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", gepReg, elemTy.IR, dataReg, cVal))
			e.storeArrayElem(gepReg, elemTy, val)
			newC := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", newC, cVal))
			e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", newC, cursorPtr))
		}
	}
	return dataReg, totalReg, nil
}

// emitArrayLiteralData builds a non-spread array literal's malloc'd,
// populated backing buffer, returning the data pointer and static element
// count — shared by every non-spread array-literal producer (var-decl
// allocas, emitArrayLiteralAggregate's general-expression path, array
// destructuring) so there's exactly one implementation of "malloc N *
// elemSize, store each element" rather than one per caller.
func (e *Emitter) emitArrayLiteralData(lit *ast.ArrayLiteral, elemTy Type) (dataReg string, n int64, err error) {
	// A nullable-scalar or union element type (`(number | null)[]`, `(A | B)[]`)
	// isn't a supported array element shape (its `{ i1, T }` / boxed storage has
	// never been wired into the array-element path) — reject cleanly rather than
	// emitting an invalid store of the aggregate into a bare-scalar slot.
	if isNullableScalar(elemTy) {
		return "", 0, fmt.Errorf("%d:%d: a nullable or union array element type (e.g. `(number | null)[]`) is not yet supported — a union is usable as an object field, but not yet as an array element", lit.GetPos().Line, lit.GetPos().Col)
	}
	n = int64(len(lit.Elements))
	// A literal-only literal (a constant table) is built from a static image
	// rather than element by element (ADR-01064).
	if d, ok := e.tryEmitStaticArrayLiteral(lit, elemTy); ok {
		return d, n, nil
	}
	e.ensureMalloc()
	dataReg = e.freshReg()
	e.heapMallocInto(dataReg, fmt.Sprint(n*int64(elemTy.Align())))
	for i, elem := range lit.Elements {
		var val Value
		var verr error
		if elemTy.IsDynamic() && e.inferExprType(elem).NullAndUndef() {
			// Keep a three-state local's absence (a plain read unwraps it).
			val, verr = e.emitExpr(elem)
			val.Ty.setNullAndUndef(val.Ty.NullAndUndef() || isNullableScalar(val.Ty) || val.Ty.IR == "ptr")
		} else {
			val, verr = e.emitExprWithObjectHint(elem, elemTy)
		}
		if verr != nil {
			return "", 0, verr
		}
		if elemTy.UnionMembers != nil {
			if val, verr = e.convertChecked(val, elemTy, elem.GetPos(), "array element"); verr != nil {
				return "", 0, verr
			}
		}
		// A boolean among numbers (or a number among booleans) is as
		// heterogeneous as a string among them — both are plain scalars, so
		// coerce would silently turn `[true, 5]` into `[true, true]`.
		if !elemTy.IsDynamic() && !val.Ty.IsDynamic() && elemTy.IR != "ptr" && val.Ty.IR != "ptr" &&
			!isNullableScalar(elemTy) && !isNullableScalar(val.Ty) && !elemTy.IsDate() && !val.Ty.IsDate() &&
			(elemTy.IR == "i1") != (val.Ty.IR == "i1") && val.Ty.IR != "" && val.Ty.IR != "void" {
			return "", 0, fmt.Errorf("%d:%d: array elements must share one type — element %d is a %s, not a %s (a heterogeneous array is not supported)", elem.GetPos().Line, elem.GetPos().Col, i, typeofString(val.Ty), typeofString(elemTy))
		}
		val = e.convert(val, elemTy)
		// A heterogeneous array literal (`[obj, 0, "s"]`) reaches here with an
		// element whose type coerce couldn't convert to the array's element type
		// (e.g. a number into a ptr/string element) — reject cleanly instead of
		// emitting an invalid `store <elemTy> <mismatchedRef>`. Composite element
		// types (nested array, object, Map/Set, any, nullable scalar) legitimately
		// carry a different storage IR than their Type.IR, so they're exempt —
		// storeArrayElem handles those. This compiler's arrays are homogeneous.
		if val.Ty.IR != elemTy.IR && !elemTy.IsArray() && !elemTy.IsDynamic() && !isNullableScalar(elemTy) {
			return "", 0, fmt.Errorf("%d:%d: array elements must share one type — element %d does not match the array's element type (a heterogeneous array is not supported)", elem.GetPos().Line, elem.GetPos().Col, i)
		}
		// The IR=="ptr" trap: an array-shaped element reports Ty.IR=="ptr" just
		// like an object/string slot, so the IR-equality check above cannot see
		// a nested-array element landing in a non-array slot (e.g. `[obj, [x]]`,
		// whose element type infers to the first element's object type). Storing
		// the array's header pointer into a bare ptr slot would be invalid
		// IR — reject cleanly. A dynamic/any elemTy boxes arrays via storeArrayElem
		// and is exempt; an array-of-arrays elemTy (both sides IsArray) matches.
		if val.Ty.IsArray() != elemTy.IsArray() && !elemTy.IsDynamic() {
			return "", 0, fmt.Errorf("%d:%d: array elements must share one type — element %d does not match the array's element type (a heterogeneous array is not supported)", elem.GetPos().Line, elem.GetPos().Col, i)
		}
		// Both sides arrays, but with different storage (`[[7], new
		// Int32Array([8])]`: a number[] slot handed an i32 buffer) — storing the
		// header would silently reinterpret the element bits (ADR-01059). Under
		// -compat=js inferArrayType already boxed such a literal to `any[]`; a
		// strict literal, or an explicit `number[][]` annotation, is rejected.
		if val.Ty.IsArray() && elemTy.IsArray() && !elemTy.IsDynamic() && !arrayStorageCompatible(val.Ty, elemTy) {
			return "", 0, fmt.Errorf("%d:%d: array elements must share one type — element %d is a %s, not a %s (a heterogeneous array is not supported; under -compat=js it becomes any[])", elem.GetPos().Line, elem.GetPos().Col, i, arrayTypeName(val.Ty), arrayTypeName(elemTy))
		}
		gepReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %d", gepReg, elemTy.IR, dataReg, i))
		e.storeArrayElem(gepReg, elemTy, val)
	}
	return dataReg, n, nil
}

// emitArrayLiteralAggregate builds an array literal as an array
// Value (TDD-00028) — the general "array as an expression"
// representation this compiler already produces for a function's own
// array-typed return value, and already consumes in resolveArrayForHOF/
// resolveArrayDataPtr/emitArrayVarDecl's own *ast.CallExpression branch.
// This is what makes an array literal usable anywhere an expression is
// expected (a call argument, a return value, an object-literal field, a
// nested nested array-literal element, a plain reassignment), not just as a
// var-decl initializer.
//
// hintElemTy, when non-nil, is the declared/expected element type already
// known from context (a var-decl annotation, a function parameter's
// declared type, an object-literal field's declared type — threaded through
// via emitExprWithObjectHint) and every element is coerced against it,
// mirroring TDD-00007's own object-literal hint-vs-self-inferred fix.
// hintElemTy nil falls back to inferArrayType's established first-element
// inference (the literal's pre-existing, unchanged convention for a
// genuinely unannotated context).
func (e *Emitter) emitArrayLiteralAggregate(lit *ast.ArrayLiteral, hintElemTy *Type) (Value, error) {
	var elemTy Type
	if hintElemTy != nil {
		elemTy = *hintElemTy
	} else {
		elemTy = *e.inferArrayType(lit).ElemType
	}
	// Array-of-arrays (elemTy itself an array type — number[][], a nested
	// literal, etc.): each element is boxed (see boxArrayValue/
	// storeArrayElem, TDD-00029) so the backing buffer below stays a
	// uniform 8-byte-per-slot layout regardless of nesting — no special
	// casing needed past storeArrayElem itself.
	hasSpread := false
	for _, elem := range lit.Elements {
		if _, ok := elem.(*ast.SpreadElement); ok {
			hasSpread = true
			break
		}
	}

	var dataReg, lenVal string
	var elisions []string
	if hasSpread {
		var err error
		saved := e.spreadElisions
		e.spreadElisions = nil
		dataReg, lenVal, err = e.emitSpreadArrayLitData(lit, elemTy)
		elisions, e.spreadElisions = e.spreadElisions, saved
		if err != nil {
			return Value{}, err
		}
	} else {
		d, n, err := e.emitArrayLiteralData(lit, elemTy)
		if err != nil {
			return Value{}, err
		}
		dataReg = d
		lenVal = fmt.Sprintf("%d", n)
	}

	r1 := e.newArrayHeader(dataReg, lenVal)
	e.emitElisionHoles(r1, lit)
	for _, at := range elisions {
		next := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", next, at))
		e.emitHolesMark(r1, at, next)
	}
	return Value{Ref: r1, Ty: ArrayOf(elemTy)}, nil
}

// emitNewArraySizedAggregate builds `new Array<T>(size)` (dynamic length,
// zero-initialized) as an array Value — the general-expression
// sibling of emitArrayVarDecl's own *ast.NewArrayExpression branch, for the
// same TDD-00028 reasons emitArrayLiteralAggregate exists.
func (e *Emitter) emitNewArraySizedAggregate(na *ast.NewArrayExpression, elemTy Type) (Value, error) {
	// A dynamic-size array is calloc'd (zero-initialized) below. For a
	// scalar/pointer elemTy that's a well-defined zero value; for a nested
	// array element it would zero-init every slot's box pointer to null,
	// and reading an unwritten element (loadArrayElem's unbox) would
	// dereference that null box. Rather than special-case a null-box read
	// path for a construction form real code is unlikely to combine with
	// nested arrays anyway, this is deliberately out of scope for now — use
	// an array literal (`[[1,2],[3,4]]`) instead, which never has this gap.
	if elemTy.IsArray() {
		return Value{}, fmt.Errorf("%d:%d: new Array<T>(n) does not yet support an array-typed element (nested arrays) — use an array literal instead.", na.GetPos().Line, na.GetPos().Col)
	}
	sizeVal, err := e.emitExpr(na.Size)
	if err != nil {
		return Value{}, err
	}
	sizeVal, err = e.convertChecked(sizeVal, TypeI64, na.Size.GetPos(), "new Array size")
	if err != nil {
		return Value{}, err
	}
	e.ensureCalloc()
	dataReg := e.freshReg()
	e.heapCallocInto(dataReg, sizeVal.Ref, fmt.Sprint(elemTy.Align()))
	if isUnconstrainedDynamic(elemTy) {
		// An any[]'s unwritten slots read undefined.
		idx := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idx))
		e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idx))
		condL, bodyL, doneL := e.freshLabel("newarr.cond"), e.freshLabel("newarr.body"), e.freshLabel("newarr.done")
		e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
		e.emitLabel(condL)
		i, more := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", i, idx))
		e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, %s", more, i, sizeVal.Ref))
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", more, bodyL, doneL))
		e.emitLabel(bodyL)
		slot, next := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %s, i64 %s", slot, dataReg, i))
		e.heapStore("i64", fmt.Sprint(nbUndefined), slot, 8)
		e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", next, i))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", next, idx))
		e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
		e.emitLabel(doneL)
	}
	r1 := e.newArrayHeader(dataReg, sizeVal.Ref)
	// Every element of `new Array(n)` is a hole until written.
	if elemTy.Float && elemTy.IR == "double" {
		e.emitFillHoles(dataReg, "0", sizeVal.Ref, elemTy)
	}
	e.emitHolesMark(r1, "0", sizeVal.Ref)
	return Value{Ref: r1, Ty: ArrayOf(elemTy)}, nil
}

func (e *Emitter) emitArrayDestructuring(s *ast.ArrayDestructuring) error {
	// `const [v, err] = f()` on a by-value-tuple function (TDD-00134 Stage 3):
	// keep the returned aggregate and bind with extractvalue — the
	// allocation-free path this ABI exists for.
	if call, ok := s.Init.(*ast.CallExpression); ok {
		if id, ok := call.Callee.(*ast.Identifier); ok && !e.isShadowedByLocal(id.Name) {
			if sig, found := e.funcs[id.Name]; found && sig.RetType.TupleByVal {
				e.wantTupleAggregate = true
				val, err := e.emitCallToFuncSig(id.Name, sig, call.Args, s.GetPos())
				e.wantTupleAggregate = false
				if err != nil {
					return err
				}
				return e.unpackTupleAggregate(val.Ref, sig.RetType, s.Elems, s.GetPos())
			}
		}
	}
	// A tuple source (`const [a, b] = someTuple`, TDD-00066) binds positionally
	// to the tuple's fields rather than indexing an array backing buffer.
	// (Unless the program may replace Array.prototype[Symbol.iterator]: a
	// tuple is an array, which iterates through it.)
	if srcTy := e.inferExprType(s.Init); srcTy.IsTuple() && !e.arrayIterPatchable() {
		objVal, err := e.emitExpr(s.Init)
		if err != nil {
			return err
		}
		return e.unpackTuplePatternInto(objVal.Ref, srcTy, s.Elems, s.GetPos())
	}
	e.iterTake = patternTake(s.Elems) + 1
	e.iterUse = true
	dataPtr, lenVal, elemTy, err := e.resolveArrayDataPtr(s.Init, s.GetPos())
	e.iterTake = 0
	if err != nil {
		return err
	}
	return e.unpackArrayPatternInto(dataPtr, lenVal, elemTy, s.Elems)
}

// unpackArrayPatternInto is emitArrayDestructuring's core, factored out so
// a destructured function parameter (whose data pointer and length are
// already known — no Init expression to resolve, see emit_func.go's
// emitFunctionDeclAs) can share the exact same per-element unpack logic
// instead of duplicating it. lenVal is any valid i64 IR operand (an SSA
// register or an integer literal — see resolveArrayDataPtr).
//
// A pattern position past the source array's actual length is ordinary,
// valid JS (`let [a, b] = [1]`), not a bug — unlike plain `arr[i]` indexing
// (emitIndexPtr, emit_exprs_member.go), which throws on an explicit,
// arbitrary runtime-computed index. Before this bounds check existed, an
// out-of-range position read directly past the source array's malloc'd
// buffer — a real out-of-bounds heap read, not just an uninitialized-value
// bug (found investigating destructuring defaults, see ADR-00157). It's
// also the one reliable "was this position actually provided" signal
// array destructuring has, and ADR-00158 builds `[a = expr]` default
// values directly on top of it: an out-of-bounds position evaluates and
// stores elem.Default (only when actually needed — lazily, inside the
// out-of-bounds branch, matching real JS's own lazy default evaluation)
// instead of the fallback zero literal.
func (e *Emitter) unpackArrayPatternInto(dataPtr, lenVal string, elemTy Type, elems []ast.ArrayPatternElem) error {
	for i, elem := range elems {
		// Rest element (`[a, ...rest]`, ADR-00161) — parser-enforced last
		// element, always defined regardless of the source array's length
		// (an empty array when this position is already past it, the same
		// clamp-to-zero `.slice()` already uses for an out-of-range start
		// index — emitArraySlice, codegen/llvm/emit_arrays_sort.go).
		// Genuinely a new, independent array (malloc + memcpy), not an
		// aliasing view into the source's own backing buffer, matching
		// real JS's own copy semantics.
		if elem.Rest {
			e.ensureMalloc()
			e.ensureMemcpy()
			rawLen := e.freshReg()
			isNegLen := e.freshReg()
			restLen := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %d", rawLen, lenVal, i))
			e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 0", isNegLen, rawLen))
			e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 0, i64 %s", restLen, isNegLen, rawLen))

			byteCount := e.freshReg()
			newPtr := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %d", byteCount, restLen, elemTy.Align()))
			e.heapMallocInto(newPtr, byteCount)

			srcGep := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %d", srcGep, elemTy.IR, dataPtr, i))
			e.emitInstr(fmt.Sprintf("call ptr @memcpy(ptr %s, ptr %s, i64 %s)", newPtr, srcGep, byteCount))

			slot := e.newArrayHeaderSlot(newPtr, restLen)
			if err := e.bindRestArray(slot, newPtr, restLen, elemTy, elem); err != nil {
				return err
			}
			continue
		}
		// Nested sub-pattern at this position (`[[a, b], c]` /
		// `[{ x }, { y }]`, TDD-00065 Stage 2) — Name is "" but the element
		// is not a hole; destructure the element at index i with the
		// sub-pattern. Checked before the hole test below, which keys on the
		// same empty Name.
		if elem.SubArray != nil || elem.SubObject != nil {
			if err := e.unpackNestedArrayElem(dataPtr, lenVal, elemTy, i, elem); err != nil {
				return err
			}
			continue
		}
		if elem.Name == "" {
			continue
		}

		// A statically-`undefined` element (`[undefined]`, or an elision hole
		// `[,]`) with a default: the element is always `undefined`, so the
		// default always applies — JS applies a destructuring default whenever
		// the matched value is `undefined`, not only when the position is out
		// of bounds. Bind directly to the default. This is both the correct
		// value (loading the element would yield `undefined`, `const [x = 23] =
		// [undefined]` must be `23`) and avoids invalid IR — a differently-
		// represented default (a number's double bits) stored into the
		// `undefined`-typed ptr slot emitted `store ptr <fpconst>`.
		if elem.Default != nil && elemTy.IsUndefined() && elem.SubArray == nil && elem.SubObject == nil {
			defVal, derr := e.emitExpr(elem.Default)
			if derr != nil {
				return derr
			}
			if defVal.Ty.IsArray() {
				hdr := e.boxArrayValue(defVal)
				slot := e.freshReg()
				e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
				e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", hdr, slot))
				e.definePatternLocal(elem.Name, Symbol{Ptr: slot, Ty: defVal.Ty})
			} else {
				slot := e.freshReg()
				e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", slot, defVal.Ty.IR, defVal.Ty.Align()))
				e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", defVal.Ty.IR, defVal.Ref, slot, defVal.Ty.Align()))
				e.definePatternLocal(elem.Name, Symbol{Ptr: slot, Ty: defVal.Ty})
			}
			continue
		}

		inBoundsReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ult i64 %d, %s", inBoundsReg, i, lenVal))
		okL := e.freshLabel("destr.ok")
		oobL := e.freshLabel("destr.oob")
		afterL := e.freshLabel("destr.after")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", inBoundsReg, okL, oobL))

		// A default of another kind than the elements (`[h = [1]] = []`,
		// whose elements type as numbers): the binding holds either, as an
		// any.
		if elem.Default != nil && !defaultFitsElem(elemTy, e.inferExprType(elem.Default)) {
			slot := e.freshReg()
			e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", slot))
			e.emitLabel(okL)
			gepReg := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %d", gepReg, elemTy.IR, dataPtr, i))
			bv, err := e.emitBoxValue(e.loadArrayElem(gepReg, elemTy))
			if err != nil {
				return err
			}
			e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", bv.Ref, slot))
			e.emitTerminator(fmt.Sprintf("br label %%%s", afterL))
			e.emitLabel(oobL)
			dv, err := e.emitExpr(elem.Default)
			if err != nil {
				return err
			}
			if dv, err = e.emitBoxValue(dv); err != nil {
				return err
			}
			e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", dv.Ref, slot))
			e.emitTerminator(fmt.Sprintf("br label %%%s", afterL))
			e.emitLabel(afterL)
			e.definePatternLocal(elem.Name, Symbol{Ptr: slot, Ty: TypeAny})
			continue
		}

		// A destructured element that's itself an array is stored under the
		// object-reference model (TDD-00127): a stable slot holding a pointer to
		// a heap {data, len} header. Each branch builds its own header from the
		// element it produces and points the shared slot at it. See
		// docs/tdd/TDD-00029.md.
		if elemTy.IsArray() {
			slot := e.freshReg()
			e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))

			e.emitLabel(okL)
			gepReg := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %d", gepReg, elemTy.IR, dataPtr, i))
			val := e.loadArrayElem(gepReg, elemTy)
			okHeader := e.boxArrayValue(val)
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", okHeader, slot))
			e.emitTerminator(fmt.Sprintf("br label %%%s", afterL))

			e.emitLabel(oobL)
			if elem.Default != nil {
				defVal, err := e.emitExpr(elem.Default)
				if err != nil {
					return err
				}
				if defVal.Ty.IsDynamic() {
					defVal = e.convert(defVal, elemTy)
				}
				switch {
				case defVal.Ty.IsArray():
					defHeader := e.boxArrayValue(defVal)
					e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", defHeader, slot))
				case defVal.Ty.IR == "void" || defVal.Ty.Kind == kindUndefined:
					// An undefined default binds undefined, as past the
					// end does.
					e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", slot))
				default:
					return fmt.Errorf("%d:%d: destructuring default must be an array to match '%s'", elem.Default.GetPos().Line, elem.Default.GetPos().Col, elem.Name)
				}
			} else {
				// Past the end is undefined: no header, read as an
				// element read's absent array is.
				e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", slot))
			}
			e.emitTerminator(fmt.Sprintf("br label %%%s", afterL))

			e.emitLabel(afterL)
			bindTy := elemTy
			if knownLen, err := strconv.Atoi(lenVal); elem.Default == nil && !(err == nil && i < knownLen) {
				bindTy = indexReadType(elemTy)
			}
			e.definePatternLocal(elem.Name, Symbol{Ptr: slot, Ty: bindTy})
			continue
		}

		// A position that may be past the source's length binds as
		// `T | undefined` (TDD-00187 Stage 2): a scalar element gets a
		// nullable-scalar { i1, T } local (absent on the out-of-bounds
		// branch), a pointer element keeps its null with the static type
		// flagged. A provably-in-bounds position (compile-time-known source
		// length, e.g. a literal init) stays a bare T — matching tsc, which
		// types tuple destructuring bare. A `= default` also stays bare: the
		// binding is always defined, exactly as TS narrows it.
		knownLen, convErr := strconv.Atoi(lenVal)
		lenIsConst := convErr == nil
		mayBeAbsent := elem.Default == nil && !(lenIsConst && i < knownLen)

		if mayBeAbsent && isNullableScalar(undefinedableElem(elemTy)) {
			nty := undefinedableElem(elemTy)
			nsPtr := e.freshReg()
			e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", nsPtr, nullableScalarStorageIR(nty), storageAlign(nty)))

			e.emitLabel(okL)
			gepReg := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %d", gepReg, elemTy.IR, dataPtr, i))
			val := e.loadArrayElem(gepReg, elemTy)
			e.storeNullableScalarPresent(nsPtr, nty, val.Ref)
			e.emitTerminator(fmt.Sprintf("br label %%%s", afterL))

			e.emitLabel(oobL)
			e.storeNullableScalarAbsent(nsPtr, nty)
			e.emitTerminator(fmt.Sprintf("br label %%%s", afterL))

			e.emitLabel(afterL)
			e.define(elem.Name, Symbol{Ptr: nsPtr, Ty: nty})
			continue
		}

		localPtr := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", localPtr, elemTy.IR, elemTy.Align()))

		e.emitLabel(okL)
		gepReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %d", gepReg, elemTy.IR, dataPtr, i))
		val := e.loadArrayElem(gepReg, elemTy)
		if elem.Default != nil && elemTy.IsDynamic() {
			// An undefined element takes the default as a missing one does.
			isU, haveL := e.freshReg(), e.freshLabel("destr.have")
			e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", isU, val.Ref, nbUndefined))
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isU, oobL, haveL))
			e.emitLabel(haveL)
		}
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", elemTy.IR, val.Ref, localPtr, elemTy.Align()))
		e.emitTerminator(fmt.Sprintf("br label %%%s", afterL))

		e.emitLabel(oobL)
		if elem.Default != nil {
			defVal, err := e.emitExpr(elem.Default)
			if err != nil {
				return err
			}
			defVal = e.convert(defVal, elemTy)
			e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", elemTy.IR, defVal.Ref, localPtr, elemTy.Align()))
		} else {
			// Miss default: the undefined box for a dynamic element, the
			// zero/null otherwise (a pointer element's null renders as
			// `undefined` via the flagged binding type below).
			e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", elemTy.IR, missRef(elemTy), localPtr, elemTy.Align()))
		}
		e.emitTerminator(fmt.Sprintf("br label %%%s", afterL))

		e.emitLabel(afterL)
		bindTy := elemTy
		if mayBeAbsent {
			bindTy = undefinedableElem(elemTy)
		}
		e.definePatternLocal(elem.Name, Symbol{Ptr: localPtr, Ty: bindTy})
	}
	return nil
}

// unpackNestedArrayElem destructures the element at index i of an array
// pattern with a nested sub-pattern (`[[a, b], c]` / `[{ x }, { y }]`,
// TDD-00065 Stage 2). It resolves the element to a safe source — the real
// element when index i is in bounds, a deterministic empty array / zeroed
// object when it is past the source's length (real JS throws there; this
// compiler keeps the same memory-safe "deterministic zero" convention
// ADR-00157 established for a leaf position) — then re-enters the matching
// unpack with the sub-pattern. A `= default` on a nested position isn't
// supported yet (a clean rejection, not silently ignored).
func (e *Emitter) unpackNestedArrayElem(dataPtr, lenVal string, elemTy Type, i int, elem ast.ArrayPatternElem) error {
	// An object pattern over an element that is not a static object (null,
	// a primitive, a union): the element under a hidden name, destructured
	// as a value — null and undefined throw.
	if elem.Default != nil || (elem.SubObject != nil && (!elemTy.IsObject() || elemTy.IsDynamic())) {
		// `[[a, b] = d]`: the element (or d) under a hidden name, then that
		// name destructured.
		hidden := e.nestedDefaultName()
		elems := make([]ast.ArrayPatternElem, i+1)
		elems[i] = ast.ArrayPatternElem{Name: hidden, Pos: elem.Pos, Default: elem.Default}
		if err := e.unpackArrayPatternInto(dataPtr, lenVal, elemTy, elems); err != nil {
			return err
		}
		return e.emitNestedPatternDecl(elem.SubArray, elem.SubObject, hidden, elem.Pos)
	}
	inBounds := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ult i64 %d, %s", inBounds, i, lenVal))
	okL := e.freshLabel("destr.nok")
	oobL := e.freshLabel("destr.noob")
	afterL := e.freshLabel("destr.nafter")

	if elem.SubArray != nil && isUnconstrainedDynamic(elemTy) {
		// An `any` element: whatever it iterates to at run time.
		subPtrA, subLenA := e.freshReg(), e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", subPtrA))
		e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", subLenA))
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", inBounds, okL, oobL))
		e.emitLabel(okL)
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %s, i64 %d", gep, dataPtr, i))
		w := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", w, gep))
		p, l := e.emitAnyArrayElemsTake(Value{Ref: w, Ty: TypeAny}, nil, patternTake(elem.SubArray))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", p, subPtrA))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", l, subLenA))
		e.emitTerminator(fmt.Sprintf("br label %%%s", afterL))
		e.emitLabel(oobL)
		e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", subPtrA))
		e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", subLenA))
		e.emitTerminator(fmt.Sprintf("br label %%%s", afterL))
		e.emitLabel(afterL)
		subPtr, subLen := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", subPtr, subPtrA))
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", subLen, subLenA))
		return e.unpackArrayPatternInto(subPtr, subLen, TypeAny, elem.SubArray)
	}
	if elem.SubArray != nil && elemTy.IsTuple() && !elemTy.IsArray() && !elemTy.IsDynamic() {
		// A tuple element (`[[k, v]] = [...map]`): its fields; past the
		// source's end it is undefined, which is not iterable.
		okL, oobL := e.freshLabel("destr.tok"), e.freshLabel("destr.toob")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", inBounds, okL, oobL))
		e.emitLabel(oobL)
		e.emitThrowTypeError("undefined is not iterable (cannot read property Symbol(Symbol.iterator))")
		e.emitLabel(okL)
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %d", gep, elemTy.IR, dataPtr, i))
		val := e.loadArrayElem(gep, elemTy)
		return e.unpackTuplePatternInto(val.Ref, elemTy, elem.SubArray, ast.Pos{})
	}
	if elem.SubArray != nil {
		if !elemTy.IsArray() || elemTy.ElemType == nil {
			return fmt.Errorf("cannot array-destructure a non-array element")
		}
		subPtrA := e.freshReg()
		subLenA := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", subPtrA))
		e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", subLenA))
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", inBounds, okL, oobL))

		e.emitLabel(okL)
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %d", gep, elemTy.IR, dataPtr, i))
		val := e.loadArrayElem(gep, elemTy)
		p := e.freshReg()
		l := e.freshReg()
		p = e.arrayData(val)
		l = e.arrayLen(val)
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", p, subPtrA))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", l, subLenA))
		e.emitTerminator(fmt.Sprintf("br label %%%s", afterL))

		e.emitLabel(oobL)
		e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", subPtrA))
		e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", subLenA))
		e.emitTerminator(fmt.Sprintf("br label %%%s", afterL))

		e.emitLabel(afterL)
		subPtr := e.freshReg()
		subLen := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", subPtr, subPtrA))
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", subLen, subLenA))
		return e.unpackArrayPatternInto(subPtr, subLen, *elemTy.ElemType, elem.SubArray)
	}

	// Nested object sub-pattern — the element must be an object/class type.
	if !elemTy.IsObject() {
		return fmt.Errorf("cannot object-destructure a non-object element")
	}
	subObjA := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", subObjA))
	// A zeroed stand-in object for the out-of-bounds case, so the recursive
	// unpack never dereferences null (memory-safe, reads deterministic zeros).
	zeroObj := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca %s, align 8", zeroObj, elemTy.StructIR()))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", inBounds, okL, oobL))

	e.emitLabel(okL)
	gep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %d", gep, elemTy.IR, dataPtr, i))
	objp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", objp, gep))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", objp, subObjA))
	e.emitTerminator(fmt.Sprintf("br label %%%s", afterL))

	e.emitLabel(oobL)
	e.emitInstr(fmt.Sprintf("store %s zeroinitializer, ptr %s, align 8", elemTy.StructIR(), zeroObj))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", zeroObj, subObjA))
	e.emitTerminator(fmt.Sprintf("br label %%%s", afterL))

	e.emitLabel(afterL)
	subObj := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", subObj, subObjA))
	return e.unpackObjectPatternInto(subObj, elemTy, elem.SubObject, ast.Pos{})
}

// resolveArrayDataPtr emits code to obtain the raw heap pointer and length
// for an array expression — lenVal is either an SSA register or (for an
// array-literal source, whose element count is already known at compile
// time) a plain integer literal string; both are valid i64 operands in the
// `icmp` unpackArrayPatternInto's own out-of-bounds check needs. Handles
// identifiers, function calls, and array literals.
func (e *Emitter) resolveArrayDataPtr(init ast.Expression, pos ast.Pos) (dataPtr, lenVal string, elemTy Type, err error) {
	if body, ok, herr := e.hostArrayReceiver(init); herr != nil || ok {
		if herr != nil {
			return "", "", Type{}, herr
		}
		init = body // an instance of a class extending Array: its array
	}
	// A string source destructures its code points (`const [a, b] = "xy"` →
	// a="x", b="y"), as for-of and Array.from do (ADR-00536, ADR-01188). Checked before
	// the node-kind switch so a string identifier or literal is handled here
	// rather than falling through to the "not an array" errors below.
	if isForOfStringTy(e.inferExprType(init)) {
		sv, sErr := e.emitExpr(init)
		if sErr != nil {
			return "", "", Type{}, sErr
		}
		charArr := e.emitStringToCharArray(sv)
		ptrReg := e.freshReg()
		ptrReg = e.arrayData(charArr)
		lenReg := e.freshReg()
		lenReg = e.arrayLen(charArr)
		return ptrReg, lenReg, TypePtr, nil
	}
	// A Map or Set: its iteration's elements (`[...s]`), entries for a Map.
	if srcTy := e.inferExprType(init); (srcTy.IsMap() || srcTy.IsSet()) && !srcTy.IsDynamic() {
		val, err := e.emitExpr(ast.NewArrayLiteral([]ast.Expression{&ast.SpreadElement{Arg: init}}, pos))
		if err != nil {
			return "", "", Type{}, err
		}
		if !val.Ty.IsArray() || val.Ty.ElemType == nil {
			return "", "", Type{}, fmt.Errorf("%d:%d: array destructuring needs an array, a string or an iterable", pos.Line, pos.Col)
		}
		return e.arrayData(val), e.arrayLen(val), *val.Ty.ElemType, nil
	}
	// A value held in `any`, or a static iterable that is not an array:
	// its elements are decided at run time (the iteration protocol).
	if srcTy := e.inferExprType(init); !srcTy.IsArray() && (isUnconstrainedDynamic(srcTy) || e.hasIteratorMember(srcTy)) {
		return e.resolveArrayForHOF(init, pos)
	}
	// A js-lane value of no iterable type throws at run time.
	if e.jsNonIterable(init) {
		return e.resolveArrayForHOF(init, pos)
	}
	// So may an array's, once the program may replace
	// Array.prototype[Symbol.iterator] (resolveArrayHOF checks it).
	if srcTy := e.inferExprType(init); e.iterUse && (srcTy.IsArray() || srcTy.IsTuple()) && !srcTy.IsFlatArray() && !srcTy.IsTypedArray() && e.arrayIterPatchable() {
		return e.resolveArrayForHOF(init, pos)
	}
	if id, ok := init.(*ast.Identifier); ok && e.isDynamicBinding(id.Name) {
		// A union binding the checker narrows to its array member.
		val, verr := e.emitExpr(id)
		if verr != nil {
			return "", "", Type{}, verr
		}
		if !val.Ty.IsArray() || val.Ty.ElemType == nil {
			return "", "", Type{}, fmt.Errorf("%d:%d: '%s' is not an array", pos.Line, pos.Col, id.Name)
		}
		ptrReg := e.freshReg()
		ptrReg = e.arrayData(val)
		lenReg := e.freshReg()
		lenReg = e.arrayLen(val)
		return ptrReg, lenReg, *val.Ty.ElemType, nil
	}
	switch src := init.(type) {
	case *ast.Identifier:
		sym, found := e.lookup(src.Name)
		if found && sym.Ty.IsFlatArray() {
			return "", "", Type{}, fmt.Errorf("%d:%d: a @value array supports index read/write, .length, for...of, and .push — destructuring '%s' needs a regular (pointer-element) array", pos.Line, pos.Col, src.Name)
		}
		if !found || !sym.Ty.IsArray() {
			return "", "", Type{}, fmt.Errorf("%d:%d: '%s' is not an array", pos.Line, pos.Col, src.Name)
		}
		dataSlot, lenSlot := e.arrayDataLenSlots(sym)
		dataPtr = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", dataPtr, dataSlot))
		lenReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", lenReg, lenSlot))
		return dataPtr, lenReg, *sym.Ty.ElemType, nil

	case *ast.ArrayLiteral:
		elemTy = *e.inferArrayType(src).ElemType
		dataReg, n, litErr := e.emitArrayLiteralData(src, elemTy)
		if litErr != nil {
			return "", "", Type{}, litErr
		}
		return dataReg, fmt.Sprintf("%d", n), elemTy, nil
	}
	// Any other expression holding an array (a call, `xs as T[]`, `o.items`,
	// `c ? xs : ys`): its array value.
	val, valErr := e.emitExpr(init)
	if valErr != nil {
		return "", "", Type{}, valErr
	}
	if !val.Ty.IsArray() || val.Ty.ElemType == nil {
		return "", "", Type{}, fmt.Errorf("%d:%d: array destructuring needs an array, a string or an iterable", pos.Line, pos.Col)
	}
	ptrReg := e.freshReg()
	ptrReg = e.arrayData(val)
	lenReg := e.freshReg()
	lenReg = e.arrayLen(val)
	return ptrReg, lenReg, *val.Ty.ElemType, nil
}

// collectionSpreadSource is the iterator a spread of a Map, Set, Headers
// or URLSearchParams walks (its default iterator: `entries()`, or a Set's
// `values()`), which the array paths handle; any other source as itself.
func (e *Emitter) collectionSpreadSource(arg ast.Expression) ast.Expression {
	if hv := e.hostView(arg); hv != nil {
		// A class extending Map or Set spreads as its host (TDD-00247).
		return arrayFromCall(arg.GetPos(), []ast.Expression{hv})
	}
	t := e.inferExprType(arg)
	if t.IsArray() || t.IsDynamic() {
		return arg
	}
	// A generator or a collection iterator is spread by running it to
	// completion, as Array.from does.
	if t.IsGenerator() && !t.GeneratorIsAsync || t.IsCollIter() {
		pos := arg.GetPos()
		return arrayFromCall(pos, []ast.Expression{arg})
	}
	if mapIterable(t) {
		pos := arg.GetPos()
		return arrayFromCall(pos, []ast.Expression{arg})
	}
	method := ""
	switch {
	case t.IsMap() && !t.IsSet():
		method = "entries"
	case t.IsSet():
		method = "values"
	default:
		return arg
	}
	return ast.NewCallExpression(ast.NewMemberExpression(arg, method, arg.GetPos()), nil, arg.GetPos())
}

// emitAnyArrayElems is the data pointer and length of the array an `any`
// holds, its elements boxed; a value that is not an array throws the
// TypeError Node's spread and for-of throw.
// emitAnyArrayElemsTake is emitAnyArrayElems reading at most n values from
// an iterator that is not an array (n < 0: every value), as array
// destructuring does.
func (e *Emitter) emitAnyArrayElemsTake(val Value, src ast.Expression, n int) (ptrReg, lenReg string) {
	if n >= 0 {
		e.iterTakeNow = n + 1
	}
	p, l := e.emitAnyArrayElems(val, src)
	e.iterTakeNow = 0
	return p, l
}

func (e *Emitter) emitAnyArrayElems(val Value, src ast.Expression) (ptrReg, lenReg string) {
	e.ensureDynJSONC()
	if !e.declaredAnyArrView {
		e.declaredAnyArrView = true
		e.emitGlobal("declare ptr @__kml_anyarr_view(i64)")
		e.emitGlobal("declare void @__kml_anyarr_sync(i64, ptr)")
	}
	// A string walks its code points, as does a String wrapper.
	val = e.anyStringAsCodePoints(e.emitUnwrapPrim(val))
	viewSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", viewSlot))
	direct := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_anyarr_view(i64 %s)", direct, e.emitHostIterValue(val.Ref)))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", direct, viewSlot))
	isArr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", isArr, direct))
	okL, protoL := e.freshLabel("anyarr.ok"), e.freshLabel("anyarr.proto")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", e.emitArrayFastPathOK(isArr), okL, protoL))
	// Not an array: the iteration protocol (a string's code points, a
	// generator, a [Symbol.iterator]() method), collected; a value with
	// none throws the loop's "is not iterable".
	e.emitLabel(protoL)
	iterable := val
	method, _ := e.emitAnyMemberOrUndefined(iterable, "@@iterator")
	method = e.emitIterMethodFix(iterable, method)
	mtag, _ := e.emitUnboxTagPayload(method)
	isFn := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isFn, mtag, kmlTagDynFunc))
	collectL, badL := e.freshLabel("anyarr.collect"), e.freshLabel("anyarr.bad")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isFn, collectL, badL))
	e.emitLabel(badL)
	if msg := e.notIterableMessage(src); msg != "" {
		e.emitThrowTypeError(msg)
	} else {
		take := e.iterTakeNow
		e.iterTakeNow = 0
		if err := e.emitThrowNotIterableValue(val); err != nil {
			e.emitThrowTypeError("object is not iterable (cannot read property Symbol(Symbol.iterator))")
		}
		e.iterTakeNow = take
	}
	e.emitLabel(collectL)
	collected := e.freshReg()
	if take := e.iterTakeNow; take > 0 {
		// A destructuring pattern's own count: only that many values,
		// the iterator closed when it is left unfinished.
		e.iterTakeNow = 0
		e.ensureAnyIterTake()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_any_iter_take(i64 %s, i64 %d)", collected, iterable.Ref, take-1))
	} else {
		e.ensureAnyIterCollect()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_any_iter_collect(i64 %s)", collected, iterable.Ref))
	}
	cview := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_anyarr_view(i64 %s)", cview, collected))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", cview, viewSlot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", okL))
	e.emitLabel(okL)
	view := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", view, viewSlot))
	ptrReg, lenReg = e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", ptrReg, view))
	lp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 1", lp, arrayHeaderTy, view))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", lenReg, lp))
	return ptrReg, lenReg
}

// identWalksAtRunTime reports whether iterating a binding is decided at run
// time: it holds `any`, or a static iterable that is not an array.
func (e *Emitter) identWalksAtRunTime(id *ast.Identifier) bool {
	sym, found := e.lookup(id.Name)
	if !found || sym.Ty.IsArray() {
		return false
	}
	return isUnconstrainedDynamic(sym.Ty) || e.hasIteratorMember(sym.Ty)
}

func (e *Emitter) resolveArrayForHOF(objExpr ast.Expression, pos ast.Pos) (ptrReg, lenReg string, elemTy Type, err error) {
	_, ptrReg, lenReg, elemTy, err = e.resolveArrayHOF(objExpr, pos)
	return
}

// receiverArrayHeader is the value a method that returns its receiver
// (sort, reverse, fill, copyWithin) produces: the receiver's own header, or a
// header over its elements when it has none (a view of an `any`'s elements).
func (e *Emitter) receiverArrayHeader(hdr, ptrReg, lenReg string) string {
	if hdr != "" {
		return hdr
	}
	return e.newArrayHeader(ptrReg, lenReg)
}

// resolveArrayHOF is resolveArrayForHOF that also returns the array's header
// (empty for the elements of an `any` or an iterable object, which are a
// view): a method returning its receiver returns that header.
func (e *Emitter) resolveArrayHOF(objExpr ast.Expression, pos ast.Pos) (hdr, ptrReg, lenReg string, elemTy Type, err error) {
	// A destructuring pattern's count, taken before anything nested runs.
	take := e.iterTake - 1
	e.iterTake = 0
	iterating := e.iterUse || (e.arrayIterPatchable() && e.spreadArgs[objExpr])
	e.iterUse = false
	objExpr = e.iterableArraySource(objExpr)
	if t := e.inferExprType(objExpr); t.IsTuple() && !t.IsDynamic() {
		// A tuple walks as an array of its elements (tupleAsArray).
		objExpr = ast.NewAsExpression(objExpr, ast.AnyArrayAnnotation(""), pos)
	}
	if body, ok, herr := e.hostArrayReceiver(objExpr); herr != nil || ok {
		if herr != nil {
			return "", "", "", Type{}, herr
		}
		objExpr = body // an instance of a class extending Array: its array
	}
	// `xs!` walks xs (the assertion is erased at run time).
	for {
		nn, ok := objExpr.(*ast.NonNullExpression)
		if !ok || !e.inferExprType(nn.Arg).IsArray() {
			break
		}
		objExpr = nn.Arg
	}
	if id, ok := objExpr.(*ast.Identifier); ok && !e.isDynamicBinding(id.Name) && !e.identWalksAtRunTime(id) && !e.jsNonIterable(objExpr) {
		sym, found := e.lookup(id.Name)
		if !found || !sym.Ty.IsArray() {
			err = fmt.Errorf("%d:%d: '%s' is not an array", pos.Line, pos.Col, id.Name)
			return
		}
		elemTy = TypeI64
		if sym.Ty.ElemType != nil {
			elemTy = *sym.Ty.ElemType
		}
		// Walking a binding that holds no array (`for…of`, spread, destructuring
		// of an omitted `xs?: T[]`) is `TypeError: xs is not iterable`; a method
		// call already threw its own TypeError at the member access.
		e.emitNotIterableGuard(id.Name, sym)
		hdr = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", hdr, sym.Ptr))
		dataSlot, lenSlot := e.arrayDataLenSlots(sym)
		ptrReg = e.freshReg()
		lenReg = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", ptrReg, dataSlot))
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", lenReg, lenSlot))
		if iterating {
			ptrReg, lenReg, err = e.emitPatchedArrayElems(hdr, ptrReg, lenReg, sym.Ty, elemTy, objExpr, take)
		}
		return
	}
	// Non-identifier: evaluate and read data/len from the array's header.
	var val Value
	val, err = e.emitExpr(objExpr)
	if err != nil {
		return
	}
	if isUnconstrainedDynamic(val.Ty) || (val.Ty.IsDynamic() && !val.Ty.IsDynamicObject() && unionHasArrayMember(val.Ty)) {
		// An `any` holding an array at run time (a dynamic function's rest
		// parameter, a JSON.parse result), or a union box narrowed to its
		// array member (`Array.isArray(v) ? v.join() : …`): its elements as
		// `any[]` (__kml_anyarr_view); anything else is not iterable.
		ptrReg, lenReg = e.emitAnyArrayElemsTake(val, objExpr, take)
		elemTy = TypeAny
		return
	}
	if !val.Ty.IsArray() && e.hasIteratorMember(val.Ty) {
		// A static object or class instance with a [Symbol.iterator]
		// member: the protocol's values, boxed.
		var boxed Value
		if boxed, err = e.emitBoxValue(val); err != nil {
			return
		}
		ptrReg, lenReg = e.emitAnyArrayElemsTake(boxed, objExpr, take)
		elemTy = TypeAny
		return
	}
	if !val.Ty.IsArray() && e.compatJS() {
		// JavaScript: iterating a value that is not iterable throws at run
		// time (the protocol's TypeError).
		var boxed Value
		if boxed, err = e.emitBoxValue(val); err != nil {
			return
		}
		ptrReg, lenReg = e.emitAnyArrayElemsTake(boxed, objExpr, take)
		elemTy = TypeAny
		return
	}
	if !val.Ty.IsArray() {
		err = fmt.Errorf("%d:%d: value is not an array", pos.Line, pos.Col)
		return
	}
	e.emitNotIterableValueGuard(objExpr, val)
	elemTy = TypeI64
	if val.Ty.ElemType != nil {
		elemTy = *val.Ty.ElemType
	}
	hdr = val.Ref
	ptrReg = e.arrayData(val)
	lenReg = e.arrayLen(val)
	if iterating {
		ptrReg, lenReg, err = e.emitPatchedArrayElems(hdr, ptrReg, lenReg, val.Ty, elemTy, objExpr, take)
	}
	return
}

// emitArrayMap implements arr.map(cb): returns a new array where each element
// is the result of calling cb(elem[, index]).

func (e *Emitter) emitArrayCopy(ptrReg, lenReg string, elemTy Type) string {
	e.ensureMalloc()
	e.ensureMemcpy()
	byteCount := e.freshReg()
	newPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %d", byteCount, lenReg, elemTy.Align()))
	e.heapMallocInto(newPtr, byteCount)
	e.emitInstr(fmt.Sprintf("call ptr @memcpy(ptr %s, ptr %s, i64 %s)", newPtr, ptrReg, byteCount))
	return newPtr
}

// emitArraySlice implements arr.slice(start[, end]): returns a new array
// containing elements from start up to (but not including) end.
// Negative indices count from the end; both are clamped to [0, len].

func (e *Emitter) emitElemEq(elemTy Type, aReg, bReg string) string {
	// A boxed-element array (`any[]`, TDD-00200): both operands are NaN-boxed
	// words, so element equality is __kml_any_eq (=== / SameValueZero over
	// dynamic values) — content equality for strings, double equality for
	// numbers — not the raw i64 identity the default arm would give (which
	// would fail for two equal-but-distinct string boxes). Used by indexOf /
	// lastIndexOf / includes.
	if elemTy.IsDynamic() {
		e.ensureAnyEq()
		eq := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_any_eq(i64 %s, i64 %s)", eq, aReg, bReg))
		return eq
	}
	if elemTy.IR == "ptr" && !elemTy.IsArray() && !elemTy.IsObject() && !elemTy.IsFunc() && !hasHandleFlag(elemTy) {
		// A string slot can hold null (`[null]` typed string[]): the same
		// pointer is equal, a null against anything else is not, and only
		// two non-null strings reach strcmp.
		e.ensureStrcmp()
		slot := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i1, align 1", slot))
		same, an, bn, either := e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, %s", same, aReg, bReg))
		e.emitInstr(fmt.Sprintf("store i1 %s, ptr %s, align 1", same, slot))
		e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", an, aReg))
		e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", bn, bReg))
		e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", either, an, bn))
		skip := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", skip, same, either))
		cmpL, doneL := e.freshLabel("streq.cmp"), e.freshLabel("streq.done")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", skip, doneL, cmpL))
		e.emitLabel(cmpL)
		cmp, z := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @strcmp(ptr %s, ptr %s)", cmp, aReg, bReg))
		e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 0", z, cmp))
		e.emitInstr(fmt.Sprintf("store i1 %s, ptr %s, align 1", z, slot))
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
		e.emitLabel(doneL)
		eq := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i1, ptr %s, align 1", eq, slot))
		return eq
	}
	if elemTy.IR == "double" {
		eq := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = fcmp oeq double %s, %s", eq, aReg, bReg))
		return eq
	}
	eq := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq %s %s, %s", eq, elemTy.IR, aReg, bReg))
	return eq
}

// arrayIndexToI64 normalizes an evaluated bracket-index value to the plain i64
// the slot GEP and bounds check consume. A numeric or dynamic (NaN-boxed) index
// rides the ordinary numeric coercion (fptosi / ToNumber). A *string*-typed
// index — legal in JS, where `x["2"]` addresses the same slot as `x[2]` — must
// be parsed to its integer value at runtime rather than reinterpreted: leaving
// the string data pointer where an i64 index is required emitted invalid IR
// (`icmp uge i64 <ptr>` / a GEP indexed by a pointer constant). strtoll(base 10)
// consumes the leading integer run (matching the canonical decimal array-index
// spelling); a non-index string such as "1.1" or "4294967296" resolves to an
// out-of-range slot and is caught by the same bounds check, never a crash.
func (e *Emitter) arrayIndexToI64(idxVal Value, pos ast.Pos) (Value, error) {
	// undefined and null are 0 (ToIntegerOrInfinity of NaN and of +0).
	if idxVal.Ty.Kind == kindUndefined || idxVal.Ty.IsNull() {
		return Value{Ref: "0", Ty: TypeI64}, nil
	}
	if isStringTy(idxVal.Ty) && !idxVal.Ty.IsClass() && !idxVal.Ty.IsObject() {
		e.ensureStrtoll()
		r := e.freshReg()
		if idxVal.Ty.Nullable() {
			// An absent string is undefined or null: 0, not strtoll's read.
			safe, isNull := e.freshReg(), e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, idxVal.Ref))
			e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", safe, isNull, e.internString("0"), idxVal.Ref))
			idxVal.Ref = safe
		}
		e.emitInstr(fmt.Sprintf("%s = call i64 @strtoll(ptr %s, ptr null, i32 10)", r, idxVal.Ref))
		return Value{Ref: r, Ty: TypeI64}, nil
	}
	// A non-numeric, non-string index (e.g. an object or Symbol) has no sound
	// conversion to an integer slot — convertChecked rejects it cleanly rather
	// than letting the bare coerce fall through and leave a `ptr` where an i64
	// is required (the A1 invalid-IR cluster; matches tsc, which rejects a
	// non-number index type). ADR-00882/00883 did the same for DataView/String.
	return e.convertChecked(idxVal, TypeI64, pos, "array index")
}

// emitArrayIndexOf implements arr.indexOf(val): returns the index of the first
// element equal to val, or -1 if not found.

// isDynamicBinding reports whether name is bound to a box (any, a union):
// an array read of it goes through its value, which the checker's
// narrowing unboxes, not through an array binding's header slot.
func (e *Emitter) isDynamicBinding(name string) bool {
	sym, ok := e.lookup(name)
	return ok && sym.Ty.IsDynamic() && !sym.Ty.IsArray()
}

// emitSpreadBoxLoop boxes n concrete elements of type srcElem at src into the
// i64 box slots at dst.
// emitSpreadUnboxLoop stores n box words from src into dst as elements of
// dstElem, each unboxed.
func (e *Emitter) emitSpreadUnboxLoop(src, n string, dstElem Type, dst string) error {
	idx := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idx))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idx))
	condL, bodyL, endL := e.freshLabel("spreadunbox.cond"), e.freshLabel("spreadunbox.body"), e.freshLabel("spreadunbox.end")
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	i := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", i, idx))
	more := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, %s", more, i, n))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", more, bodyL, endL))
	e.emitLabel(bodyL)
	sg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %s, i64 %s", sg, src, i))
	w := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", w, sg))
	v := e.emitUnboxBoxToType(w, dstElem)
	dg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", dg, dstElem.IR, dst, i))
	e.storeArrayElem(dg, dstElem, v)
	next := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", next, i))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", next, idx))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(endL)
	return nil
}

func (e *Emitter) emitSpreadBoxLoop(src, n string, srcElem Type, dst string) error {
	idx := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idx))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idx))
	condL, bodyL, endL := e.freshLabel("spreadbox.cond"), e.freshLabel("spreadbox.body"), e.freshLabel("spreadbox.end")
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	i := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", i, idx))
	more := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, %s", more, i, n))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", more, bodyL, endL))
	e.emitLabel(bodyL)
	slotIR := srcElem.IR
	if srcElem.IsArray() {
		slotIR = "ptr" // an element array is its header pointer
	}
	sg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", sg, slotIR, src, i))
	v := e.loadArrayElem(sg, srcElem)
	boxed, err := e.emitBoxValue(v)
	if err != nil {
		return err
	}
	dg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %s, i64 %s", dg, dst, i))
	e.heapStore("i64", boxed.Ref, dg, 8)
	next := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", next, i))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", next, idx))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(endL)
	return nil
}

// unionHasArrayMember reports whether union type t has an array member.
func unionHasArrayMember(t Type) bool {
	for _, m := range t.UnionMembers {
		if m.IsArray() {
			return true
		}
	}
	return false
}

// iterableArraySource is expr, or Array.from(expr) when expr is an iterable
// that is not an array — a Map, Set, Array/Map/Set iterator or sync
// generator — so a position that walks an array (a Map or Set seed,
// Array.prototype methods' sources) takes any of them, as its Iterable
// parameter does.
func (e *Emitter) iterableArraySource(expr ast.Expression) ast.Expression {
	if expr == nil {
		return nil
	}
	if hv := e.hostView(expr); hv != nil {
		return arrayFromCall(expr.GetPos(), []ast.Expression{hv})
	}
	t := e.inferExprType(expr)
	if mapIterable(t) || t.IsCollIter() || t.IsGenerator() && !t.GeneratorIsAsync {
		pos := expr.GetPos()
		return arrayFromCall(pos, []ast.Expression{expr})
	}
	return expr
}

// bindRestArray binds a rest element's new array (its header in slot): to
// its name, or destructured by its sub-pattern (`[a, ...[b, c]]`,
// `[...{ length }]`).
func (e *Emitter) bindRestArray(slot, dataPtr, lenVal string, elemTy Type, elem ast.ArrayPatternElem) error {
	switch {
	case elem.SubArray != nil:
		return e.unpackArrayPatternInto(dataPtr, lenVal, elemTy, elem.SubArray)
	case elem.SubObject != nil:
		tmp := "__kml_rest_" + slot[1:]
		e.define(tmp, Symbol{Ptr: slot, Ty: ArrayOf(elemTy)})
		return e.emitObjectDestructuring(ast.NewObjectDestructuring("const", elem.SubObject, ast.NewIdentifier(tmp, elem.Pos), elem.Pos))
	case elem.Name != "":
		e.define(elem.Name, Symbol{Ptr: slot, Ty: ArrayOf(elemTy)})
	}
	return nil
}

// nestedDefaultName is a fresh hidden binding for a nested pattern with a
// default (`[[a] = d]` / `{ k: { a } = d }`).
func (e *Emitter) nestedDefaultName() string {
	return "__kml_nd" + strings.TrimPrefix(e.freshReg(), "%")
}

// emitNestedPatternDecl destructures the hidden binding name with a nested
// pattern: `let <pattern> = name`. A value with no static shape for the
// pattern (an element of an empty literal) is destructured as an any.
func (e *Emitter) emitNestedPatternDecl(sub []ast.ArrayPatternElem, obj []ast.DestructProp, name string, pos ast.Pos) error {
	var src ast.Expression = ast.NewIdentifier(name, pos)
	if sym, ok := e.lookup(name); ok {
		shaped := sym.Ty.IsDynamic() || (sub != nil && (sym.Ty.IsArray() || sym.Ty.IsTuple())) || (obj != nil && sym.Ty.IsObject())
		if !shaped {
			v, err := e.emitExpr(src)
			if err != nil {
				return err
			}
			b, err := e.emitBoxValue(v)
			if err != nil {
				return err
			}
			src = e.bindValue(b, pos)
		}
	}
	if sub != nil {
		return e.emitStmt(ast.NewArrayDestructuring("let", sub, src, pos))
	}
	return e.emitStmt(ast.NewObjectDestructuring("let", obj, src, pos))
}

// defaultFitsElem reports a destructuring default of type def that the
// element type elem can hold as it is: the same kind of value (numbers
// with numbers, an array with arrays, an object with objects). An unknown
// or dynamic side fits (the element's own conversion applies).
func defaultFitsElem(elem, def Type) bool {
	if def.IR == "" || def.IR == "void" || elem.IsDynamic() || elem.IsUndefined() {
		return true
	}
	if def.IsDynamic() {
		return false // a dynamic default in a static element's slot loses its value
	}
	if elem.IsArray() || def.IsArray() {
		return elem.IsArray() && def.IsArray()
	}
	num := func(t Type) bool { return t.Float || t.IsInteger() }
	if num(elem) || num(def) {
		return num(elem) && num(def)
	}
	return elem.IR == def.IR && elem.IsObject() == def.IsObject()
}
