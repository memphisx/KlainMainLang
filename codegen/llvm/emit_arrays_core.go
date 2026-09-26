package llvm

import (
	"KlainMainLang/ast"
	"fmt"
	"strconv"
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
	if _, isNullLit := v.Init.(*ast.NullLiteral); ty.Nullable && (isNullLit || v.Init == nil) {
		e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", slot))
		return nil
	}
	ptrName := e.newArrayHeader("null", "0")
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", ptrName, slot))
	lenName := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 1", lenName, arrayHeaderTy, ptrName))

	if v.Init == nil {
		e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", ptrName))
		e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", lenName))
		return nil
	}

	// Dynamic-size array: new Array<T>(runtimeSize) — built via
	// emitNewArraySizedAggregate (TDD-00028's general-expression producer)
	// and extracted here, the same "build the aggregate once, every
	// consumer extracts from it" shape every branch below now shares.
	if na, ok := v.Init.(*ast.NewArrayExpression); ok {
		val, err := e.emitNewArraySizedAggregate(na, elemTy)
		if err != nil {
			return err
		}
		return e.storeArrayAggregateInto(val, ptrName, lenName)
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
		return e.storeArrayAggregateInto(val, ptrName, lenName)
	}

	// JSON.parse / Response.json() into an array type (`const xs: T[] =
	// JSON.parse(...)`, or the same awaited): pass the array type through so
	// projection builds a T[] aggregate (TDD-00077 P3), mirroring the scalar/
	// object var-decl's own type-context branch. The generic emitExpr path below
	// would call it with no type context.
	if val, ok, err := e.emitDeclJSONProjection(v.Init, ty); ok {
		if err != nil {
			return err
		}
		return e.storeArrayAggregateInto(val, ptrName, lenName)
	}

	// Any other expression that produces a {ptr, i64} array aggregate — a
	// function call, an index expression (e.g. groupMap["key"]), a Map/Set
	// method result, etc.
	val, err := e.emitExpr(v.Init)
	if err != nil {
		return err
	}
	if val.Ty.IsDynamic {
		// `const xs: T[] = anyValue` (an assertion in TS), or the array member
		// of a union the checker narrowed to it: the box's array.
		val = e.emitUnboxBoxToType(val.Ref, ty)
	}
	if !val.Ty.IsArray {
		return fmt.Errorf("%d:%d: array variable must be initialized with an array expression", v.GetPos().Line, v.GetPos().Col)
	}
	// Reference semantics (TDD-00213 Stage 1): if the initializer is an existing
	// header-backed array (`let a = b`, or any expression carrying a live header),
	// point this binding's slot at the SAME header cell so the two bindings alias
	// — a `push` through either is visible through the other, and they are `===`.
	// The fresh header minted above is discarded; a genuinely new array
	// expression (literal/new/slice/HOF — no ArrayHeader) keeps it and fills it.
	// A nullable-array initializer (a `Map<K,T[]>` get that may miss) binds a
	// `T[] | undefined`: remember the nullability on the symbol so a later
	// identifier load rebuilds it null-safely and its truthiness tests the
	// header (see emitExpr's array-identifier path). Only meaningful for a named
	// local (a promoted global keeps its declared type).
	if val.Ty.Nullable && !e.promotedGlobalDecls[v] {
		if sym, ok := e.lookup(v.Name); ok {
			sym.Ty.Nullable = true
			e.define(v.Name, sym)
		}
	}
	if val.ArrayHeader != "" {
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", val.ArrayHeader, slot))
		return nil
	}
	if val.Ty.Nullable && (ty.Nullable || !e.promotedGlobalDecls[v]) {
		// A header-less possibly-absent value (a RegExp miss): absent binds the
		// null header, so the binding's absence test is the header alone.
		sel := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr null, ptr %s", sel, e.emitArrayIsAbsent(val), ptrName))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", sel, slot))
	}
	return e.storeArrayAggregateInto(val, ptrName, lenName)
}

// storeArrayAggregateInto extracts val's {ptr, i64} aggregate into a
// var-decl's own two allocas (the "Named Symbol" array representation —
// see the project's own Array value duality note) — the common tail every
// emitArrayVarDecl branch now shares.
func (e *Emitter) storeArrayAggregateInto(val Value, ptrName, lenName string) error {
	ptrReg := e.freshReg()
	lenReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", ptrReg, val.Ref))
	e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 1", lenReg, val.Ref))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", ptrReg, ptrName))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", lenReg, lenName))
	return nil
}

// loadArrayFieldValue reads an array-typed struct field into a plain {ptr,i64}
// array Value. Under the header-pointer field model (TDD-00213 Stage 2) a struct
// slot for an array holds a pointer to a shared {data,len} header, so this loads
// the header, derefs its data/len into the aggregate, and carries the header in
// ArrayHeader — making a binding of the field value alias the same array
// (`let x = obj.arr`) and a mutation through the field visible. A null header (a
// calloc'd/absent field) reads as the {null,0} empty array, matching the
// pre-migration inline behavior. fieldSlot is the field's GEP address.
func (e *Emitter) loadArrayFieldValue(fieldSlot string, fieldTy Type) Value {
	header := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", header, fieldSlot))
	isNull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, header))
	nullL := e.freshLabel("arrfld.null")
	loadL := e.freshLabel("arrfld.load")
	doneL := e.freshLabel("arrfld.done")
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca {ptr, i64}, align 8", slot))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNull, nullL, loadL))
	e.emitLabel(nullL)
	e.emitInstr(fmt.Sprintf("store {ptr, i64} {ptr null, i64 0}, ptr %s, align 8", slot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(loadL)
	agg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load {ptr, i64}, ptr %s, align 8", agg, header))
	e.emitInstr(fmt.Sprintf("store {ptr, i64} %s, ptr %s, align 8", agg, slot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	out := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load {ptr, i64}, ptr %s, align 8", out, slot))
	return Value{Ref: out, Ty: fieldTy, ArrayHeader: header}
}

// loadArraySlotAggregate is the branchless sibling of loadArrayFieldValue for a
// slot that is ALWAYS written before it is read (a generator/promise/iterator
// state-struct slot, never a calloc'd-and-untouched object field) — it derefs the
// header without the null guard, so it introduces no basic blocks and is safe to
// call inside a state machine's linear control flow. The slot holds a header
// pointer (TDD-00213 Stage 2); the returned Value carries it in ArrayHeader.
func (e *Emitter) loadArraySlotAggregate(slot string, ty Type) Value {
	header := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", header, slot))
	agg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load {ptr, i64}, ptr %s, align 8", agg, header))
	return Value{Ref: agg, Ty: ty, ArrayHeader: header}
}

// arrayReturnHeader produces the header pointer to return an array value across
// the `ret ptr` array ABI (TDD-00213 Stage 3): it shares the value's live header
// when it has one (a returned field/global/captured/named array keeps its
// identity so the caller aliases the same array), else mints a fresh header from
// the {data,len} aggregate (a transient/new array expression). Mirrors
// storeArrayFieldHeader, the field-storage analogue.
//
// A possibly-absent header-less value (a RegExp miss — absent by its null data
// pointer) yields the null header, so absence has one meaning across the ABI.
func (e *Emitter) arrayReturnHeader(val Value) string {
	if val.ArrayHeader != "" {
		return val.ArrayHeader
	}
	if val.Ty.Nullable {
		sel := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr null, ptr %s", sel, e.emitArrayIsAbsent(val), e.boxArrayValue(val)))
		return sel
	}
	return e.boxArrayValue(val)
}

// arrayValueFromHeaderReg wraps an array `ret ptr` call result (a header pointer,
// TDD-00213 Stage 3) into an ordinary {ptr,i64} array Value: it derefs the header
// for the aggregate and carries the header in ArrayHeader, so binding the call
// result aliases the returned array (`let x = f(); x === f()`'s source shares one
// header). headerReg is the raw call-instruction result.
func (e *Emitter) arrayValueFromHeaderReg(headerReg string, ty Type) Value {
	if ty.Nullable {
		// A `T[] | null` result may be the null header (arrayReturnHeader): read
		// the shared all-zero cell instead. Branchless — this runs inside state
		// machines' linear control flow too.
		return e.loadArrayHeaderOrAbsent(headerReg, ty)
	}
	agg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load {ptr, i64}, ptr %s, align 8", agg, headerReg))
	return Value{Ref: agg, Ty: ty, ArrayHeader: headerReg}
}

// arrayValueFromHeaderSlotGuarded is the null-guarded sibling of
// arrayValueFromHeaderReg: `header` is a header pointer that may be null (e.g. a
// Map<K,T[]> get() miss returns 0 → inttoptr null). A null header reads as the
// {null,0} empty array instead of dereferencing null, mirroring
// loadArrayFieldValue's absent-field guard.
func (e *Emitter) arrayValueFromHeaderSlotGuarded(header string, ty Type) Value {
	isNull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, header))
	nullL := e.freshLabel("arrhdr.null")
	loadL := e.freshLabel("arrhdr.load")
	doneL := e.freshLabel("arrhdr.done")
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca {ptr, i64}, align 8", slot))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNull, nullL, loadL))
	e.emitLabel(nullL)
	e.emitInstr(fmt.Sprintf("store {ptr, i64} {ptr null, i64 0}, ptr %s, align 8", slot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(loadL)
	agg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load {ptr, i64}, ptr %s, align 8", agg, header))
	e.emitInstr(fmt.Sprintf("store {ptr, i64} %s, ptr %s, align 8", agg, slot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	out := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load {ptr, i64}, ptr %s, align 8", out, slot))
	return Value{Ref: out, Ty: ty, ArrayHeader: header}
}

// storeArrayFieldHeader stores an array Value into a header-pointer field slot
// (TDD-00213 Stage 2): it shares the value's live header when it has one
// (reference semantics — `obj.arr = existingArray` aliases), else mints a fresh
// header from the {data,len} aggregate (a new array expression). fieldSlot is the
// field's GEP address.
func (e *Emitter) storeArrayFieldHeader(fieldSlot string, val Value) {
	// arrayReturnHeader: a header-less absent value of a nullable type (a SQL
	// NULL blob, a RegExp miss) stores the null header — the field's absence.
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", e.arrayReturnHeader(val), fieldSlot))
}

// boxArrayValue heap-allocates a 16-byte {ptr, i64} box and stores val's
// aggregate into it, returning the box pointer — the storage TDD-00029 uses
// for a nested-array element so an outer array's backing buffer can stay a
// uniform 8-byte-per-slot layout (elemTy.IR/Align() already report "ptr"/8
// for an array-typed elemTy) instead of needing 16-byte slots only when the
// element happens to itself be an array. One extra malloc + one extra
// indirection per nested-array element access is the accepted cost — see
// docs/tdd/TDD-00029.md's Design for the (a)-vs-(b) tradeoff this resolves.
func (e *Emitter) boxArrayValue(val Value) string {
	e.ensureMalloc()
	box := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", box))
	e.emitInstr(fmt.Sprintf("store {ptr, i64} %s, ptr %s, align 8", val.Ref, box))
	return box
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
	e.ensureMalloc()
	h := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", h))
	lp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 1", lp, arrayHeaderTy, h))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", dataReg, h))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", lenReg, lp))
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
	lenSlot = e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 1", lenSlot, arrayHeaderTy, h))
	return h, lenSlot
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

// newArrayHeaderSlotFromAggregate boxes an array aggregate Value into a fresh
// header and returns a stable slot (`alloca ptr`) pointing at it — the storage
// of a named array variable initialised from an expression result (TDD-00127).
func (e *Emitter) newArrayHeaderSlotFromAggregate(val Value) string {
	header := e.boxArrayValue(val)
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", header, slot))
	return slot
}

// arrayArgFromAggregate produces the {data, len} header to pass an array Value
// as a (ptr, i64) call argument under the object-reference ABI (TDD-00127).
// A Value carrying a live header (a field read, nested element, returned or
// captured array) passes that SAME header so mutations inside the callee
// (push/splice) propagate — JS reference semantics across every argument
// shape, not just named variables. Only a genuinely transient expression
// (literal, slice/map result — no ArrayHeader) gets a fresh header: the callee
// may mutate it, but it has no caller-visible identity. A carried header can
// be null (an absent calloc'd field, a Map get miss): the callee derefs its
// header unguarded, so a fresh header wrapping the (safe {null,0}) aggregate
// is selected in that case.
func (e *Emitter) arrayArgFromAggregate(val Value) (header, lenReg string) {
	lenReg = e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 1", lenReg, val.Ref))
	fresh := e.boxArrayValue(val)
	if val.ArrayHeader == "" {
		return fresh, lenReg
	}
	isNull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, val.ArrayHeader))
	sel := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", sel, isNull, fresh, val.ArrayHeader))
	return sel, lenReg
}

// packArrayArg produces the (headerReg, lenReg) pair to pass an array argument
// under the object-reference ABI (TDD-00127). When the argument is a named array
// variable, its own header pointer is passed, so an in-place mutation inside the
// callee (push/splice) propagates back to the caller — JS reference semantics.
// Any other array expression goes through arrayArgFromAggregate, which shares
// the value's own live header when it carries one (a member/index/field read)
// and mints a fresh header only for a true transient.
//
// An absent array crosses the call as a null header, but only into a parameter
// whose type can be absent (`T[] | null`, `xs?: T[]`) — that callee guards its
// header. Any other callee derefs it unguarded, so it gets an empty array.
// The length word is redundant (bindArrayParam), so an absent array passes 0.
func (e *Emitter) packArrayArg(arg ast.Expression, val Value, paramTy Type) (header, lenReg string) {
	if id, ok := arg.(*ast.Identifier); ok {
		if sym, found := e.lookup(id.Name); found && sym.Ty.IsArray {
			if sym.Ty.Nullable {
				h := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", h, sym.Ptr))
				if paramTy.Nullable {
					return h, "0"
				}
				sel := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", sel, e.ptrIsNull(h), e.emptyArrayArgHeader(), h))
				return sel, "0"
			}
			dataSlot, lenSlot := e.arrayDataLenSlots(sym)
			lr := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", lr, lenSlot))
			return dataSlot, lr
		}
	}
	if paramTy.Nullable && val.Ty.Nullable {
		// A possibly-absent value into a parameter that can be absent: keep a
		// carried header as is; a header-less transient is absent exactly when
		// its data pointer is null (a `null` literal, a RegExp miss).
		if val.ArrayHeader != "" {
			return val.ArrayHeader, "0"
		}
		lenReg = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 1", lenReg, val.Ref))
		sel := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr null, ptr %s", sel, e.emitArrayIsAbsent(val), e.boxArrayValue(val)))
		return sel, lenReg
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
	if paramTy.Nullable {
		return "null"
	}
	return e.emptyArrayArgHeader()
}

// emitAbsentArrayValue is the `null`/`undefined` value of a nullable array type:
// the {null,0} aggregate carrying a null header, which is what every absence
// test consults (emitArrayIsAbsent).
func (e *Emitter) emitAbsentArrayValue(ty Type) Value {
	return Value{Ref: "zeroinitializer", Ty: ty, ArrayHeader: "null"}
}

// emitArrayIsAbsent yields the i1 "this nullable array value is null/undefined".
// The header decides when the value carries one — a present array, even an
// empty one, has a header — and the data pointer otherwise (a header-less
// transient: a RegExp miss, a `find` miss).
func (e *Emitter) emitArrayIsAbsent(v Value) string {
	if v.ArrayHeader != "" {
		return e.ptrIsNull(v.ArrayHeader)
	}
	d := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", d, v.Ref))
	return e.ptrIsNull(d)
}

// iterableDisplayName renders an iterated expression the way Node names it in
// "… is not iterable": `xs`, `o.tags`, `this.items`, `f(...)`.
func iterableDisplayName(expr ast.Expression) string {
	switch ex := expr.(type) {
	case *ast.Identifier:
		return demangleModuleName(ex.Name)
	case *ast.ThisExpression:
		return "this"
	case *ast.MemberExpression:
		return iterableDisplayName(ex.Object) + "." + ex.Property
	case *ast.NonNullExpression:
		return iterableDisplayName(ex.Arg)
	case *ast.CallExpression:
		return iterableDisplayName(ex.Callee) + " is not a function or its return value"
	}
	return "object"
}

// emitNotIterableValueGuard is emitNotIterableGuard for an array *value* (a
// field read, a call result) of a nullable type.
func (e *Emitter) emitNotIterableValueGuard(expr ast.Expression, v Value) {
	if !v.Ty.IsArray || !v.Ty.Nullable || e.blockDone {
		return
	}
	e.ensureNullDerefThrow()
	throwL := e.freshLabel("notiter.throw")
	okL := e.freshLabel("notiter.ok")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", e.emitArrayIsAbsent(v), throwL, okL))
	e.emitLabel(throwL)
	e.emitInstr(fmt.Sprintf("call void @__kml_throw_nullderef(ptr %s)", e.internString(iterableDisplayName(expr)+" is not iterable")))
	e.emitTerminator("unreachable")
	e.emitLabel(okL)
}

// emitNotIterableGuard throws Node's `TypeError: xs is not iterable` when the
// nullable array binding sym (named name) holds no array — the check `for…of`
// and spread make before touching the header.
func (e *Emitter) emitNotIterableGuard(name string, sym Symbol) {
	if !sym.Ty.Nullable || e.blockDone {
		return
	}
	h := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", h, sym.Ptr))
	e.ensureNullDerefThrow()
	throwL := e.freshLabel("notiter.throw")
	okL := e.freshLabel("notiter.ok")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", e.ptrIsNull(h), throwL, okL))
	e.emitLabel(throwL)
	e.emitInstr(fmt.Sprintf("call void @__kml_throw_nullderef(ptr %s)", e.internString(demangleModuleName(name)+" is not iterable")))
	e.emitTerminator("unreachable")
	e.emitLabel(okL)
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

// unboxArrayValue loads the {ptr, i64} aggregate out of a box pointer
// produced by boxArrayValue, returning it as an ordinary array Value —
// exactly the representation every other array-producing expression already
// returns (function return, literal, slice, ...), so nothing downstream of a
// nested-array element read needs to know boxing happened at all. The box IS
// the element's live {data,len} header (one shared cell per nested array), so
// the Value carries it in ArrayHeader — a nested element passed onward (call
// argument, binding, HOF callback element) then aliases the same array
// instead of snapshotting (TDD-00127 residual).
//
// A null box is an absent element (`[xs.at(9)]`, storeArrayElem): it reads as
// the {null,0} aggregate carrying the null header, never through the pointer.
func (e *Emitter) unboxArrayValue(boxPtr string, elemTy Type) Value {
	return e.loadArrayHeaderOrAbsent(boxPtr, elemTy)
}

// loadArrayHeaderOrAbsent reads the {data,len} aggregate behind a header that
// may be null, selecting one shared all-zero cell for the null case —
// branchless, so it is safe inside a state machine's linear control flow.
func (e *Emitter) loadArrayHeaderOrAbsent(header string, ty Type) Value {
	if !e.usedAbsentArrayCell {
		e.usedAbsentArrayCell = true
		e.emitGlobal("@__kml_absent_array = internal constant { ptr, i64 } zeroinitializer, align 8")
	}
	src := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr @__kml_absent_array, ptr %s", src, e.ptrIsNull(header), header))
	agg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load {ptr, i64}, ptr %s, align 8", agg, src))
	return Value{Ref: agg, Ty: ty, ArrayHeader: header}
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
	if elemTy.IsArray {
		boxPtr := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", boxPtr, gepReg))
		return e.unboxArrayValue(boxPtr, elemTy)
	}
	reg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", reg, elemTy.IR, gepReg, elemTy.Align()))
	return Value{Ref: reg, Ty: elemTy}
}

// loadArrayElemMaybeNull is loadArrayElem for a slot that may itself hold a
// null box pointer — a "no match" sentinel (e.g. .find()'s own pre-loop
// zero-init, reusing the same "0/null doubles as absent" convention
// .find() already established for every other element type). Unboxing a
// null box would dereference null; this branches around the unbox instead
// and produces the {ptr:null,i64:0} "null array" sentinel shape
// RegExp.exec()'s own T[] | null result already uses, rather than crashing.
// Only meaningful for elemTy.IsArray — added alongside ADR-00151/TDD-00059
// lifting rejectNestedArrayElem for the callback-invoking HOF methods.
func (e *Emitter) loadArrayElemMaybeNull(slotPtr string, elemTy Type) Value {
	boxPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", boxPtr, slotPtr))
	isNull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, boxPtr))
	nullL := e.freshLabel("arrelem.null")
	foundL := e.freshLabel("arrelem.found")
	doneL := e.freshLabel("arrelem.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNull, nullL, foundL))

	e.emitLabel(nullL)
	r0 := e.freshReg()
	nullAgg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr null, 0", r0))
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 0, 1", nullAgg, r0))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))

	e.emitLabel(foundL)
	foundAgg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load {ptr, i64}, ptr %s, align 8", foundAgg, boxPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))

	e.emitLabel(doneL)
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = phi {ptr, i64} [ %s, %%%s ], [ %s, %%%s ]", result, nullAgg, nullL, foundAgg, foundL))
	// The box IS the element's live header; carry it (possibly null — the
	// "no match" sentinel, which downstream consumers null-guard) so a found
	// element aliases the source array (TDD-00127 residual).
	return Value{Ref: result, Ty: elemTy, ArrayHeader: boxPtr}
}

// storeArrayElem stores val (of type elemTy) into a GEP'd array-backing-
// buffer slot. For a nested-array element, val's {ptr,i64} aggregate is
// boxed first (see boxArrayValue) and the box pointer is what's actually
// stored in the slot; every other element type stores directly, unchanged
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
	if elemTy.IsArray && isSelfDescribingBox(val.Ty) {
		// An `any` element into an array-of-arrays (or Buffer[]): the box's
		// array.
		val = e.emitUnboxBoxToType(val.Ref, elemTy)
	}
	if elemTy.IsArray {
		// Share the value's live header when it has one (reference semantics:
		// `m[i] = existingArray` aliases, matching storeArrayFieldHeader); a
		// new array expression mints its own box. A null carried header (an
		// absent field read) falls back to the fresh box, since element reads
		// unbox unguarded.
		box := val.ArrayHeader
		if box == "" {
			box = e.arrayReturnHeader(val) // a null box for an absent value
		} else if val.Ty.Nullable {
			// keep a carried null header: the element is absent
		} else {
			fresh := e.boxArrayValue(val)
			isNull := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, box))
			sel := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", sel, isNull, fresh, box))
			box = sel
		}
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", box, gepReg))
		return
	}
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", elemTy.IR, val.Ref, gepReg, elemTy.Align()))
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
	boxed := e.coerce(srcElem, TypeAny)
	dstGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %s, i64 %s", dstGep, dst, idxVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", boxed.Ref, dstGep))
	idxNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", idxNext, idxVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", idxNext, idxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(doneL)
}

// emitScalarZero emits a zero constant of the given scalar type as a Value.
// For an aggregate type (array {ptr,i64}) the caller should emit the two-part
// insertvalue {ptr,i64} undef, ptr null, 0 / insertvalue {ptr,i64} ..., i64 0, 1
// pattern directly instead — this helper is for scalar-typed zeros only.
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

// rejectNestedArrayElem returns a clear compile-time error when elemTy is
// itself an array — used by array operations that either invoke a callback
// with the loaded element (map/filter/forEach/reduce/find*/some/every/sort's
// comparator) or compare/consume elements as bare, elemTy.IR-typed scalar
// registers (indexOf/includes/join) — neither is safe yet for a boxed
// nested-array element: closures don't decompose an array-typed parameter
// into (ptr, i64) the way a named top-level function call's own ABI already
// does (emitCallToFuncSig), and a raw register can't be compared/stringified
// without knowing it's actually a box pointer needing an unbox first.
// Indexing, destructuring, for...of, and the copy/insert-based methods
// (concat/reverse/slice/splice/fill/at/with/...) don't have this problem —
// they route through loadArrayElem/storeArrayElem instead and remain fully
// supported for nested arrays. See docs/tdd/TDD-00029.md.
func (e *Emitter) rejectNestedArrayElem(elemTy Type, opName string, pos ast.Pos) error {
	if elemTy.IsArray {
		return fmt.Errorf("%d:%d: .%s() does not yet support an array-of-arrays element type", pos.Line, pos.Col, opName)
	}
	return nil
}

// emitSpreadArrayLitData handles array literals that contain one or more
// spread elements: computes total length at runtime, allocates one
// contiguous buffer, and fills it using a write cursor (memcpy per spread,
// store per static element), returning the data pointer and length operands
// rather than storing into caller-supplied allocas — shared by
// emitArrayVarDecl (which stores the result into its own two allocas) and
// emitArrayLiteralAggregate (TDD-00028, which builds a {ptr,i64} aggregate
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
		if id, ok := sp.Arg.(*ast.Identifier); ok {
			if sym, found := e.lookup(id.Name); found && sym.Ty.IsFlatArray {
				return "", "", fmt.Errorf("%d:%d: a @value array supports index read/write, .length, for...of, and .push — spreading '%s' needs a regular (pointer-element) array", sp.GetPos().Line, sp.GetPos().Col, id.Name)
			}
		}
		// Spreading a string (`[..."abc"]`) yields its characters — materialize
		// the char array once and hand its ptr+len to the copy loops.
		if at := e.inferExprType(sp.Arg); isStringTy(at) && !at.IsArray && !at.IsClass && !at.IsObject {
			sv, verr := e.emitExpr(sp.Arg)
			if verr != nil {
				return "", "", verr
			}
			chars := e.emitStringToCharArray(sv)
			sp0 := e.freshReg()
			sl0 := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", sp0, chars.Ref))
			e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 1", sl0, chars.Ref))
			spreadOf[sp] = spreadSrc{ptr: sp0, length: sl0, elem: TypePtr}
			continue
		}
		srcPtr, srcLen, srcElem, rerr := e.resolveArrayForHOF(sp.Arg, sp.GetPos())
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
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", dataReg, bytesReg))

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
			if elemTy.IsDynamic && !elemTy.IsArray && src.elem.IR != "" && !src.elem.IsDynamic {
				if err := e.emitSpreadBoxLoop(srcPtr, srcLen, src.elem, dstReg); err != nil {
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
			// Static element.
			val, verr := e.emitExprWithObjectHint(elem, elemTy)
			if verr != nil {
				return "", "", verr
			}
			if elemTy.UnionMembers != nil {
				if val, verr = e.coerceChecked(val, elemTy, elem.GetPos(), "array element"); verr != nil {
					return "", "", verr
				}
			}
			val = e.coerce(val, elemTy)
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
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", dataReg, n*int64(elemTy.Align())))
	for i, elem := range lit.Elements {
		val, verr := e.emitExprWithObjectHint(elem, elemTy)
		if verr != nil {
			return "", 0, verr
		}
		if elemTy.UnionMembers != nil {
			if val, verr = e.coerceChecked(val, elemTy, elem.GetPos(), "array element"); verr != nil {
				return "", 0, verr
			}
		}
		// A boolean among numbers (or a number among booleans) is as
		// heterogeneous as a string among them — both are plain scalars, so
		// coerce would silently turn `[true, 5]` into `[true, true]`.
		if !elemTy.IsDynamic && !val.Ty.IsDynamic && elemTy.IR != "ptr" && val.Ty.IR != "ptr" &&
			!isNullableScalar(elemTy) && !isNullableScalar(val.Ty) && !elemTy.IsDate && !val.Ty.IsDate &&
			(elemTy.IR == "i1") != (val.Ty.IR == "i1") && val.Ty.IR != "" && val.Ty.IR != "void" {
			return "", 0, fmt.Errorf("%d:%d: array elements must share one type — element %d is a %s, not a %s (a heterogeneous array is not supported)", elem.GetPos().Line, elem.GetPos().Col, i, typeofString(val.Ty), typeofString(elemTy))
		}
		val = e.coerce(val, elemTy)
		// A heterogeneous array literal (`[obj, 0, "s"]`) reaches here with an
		// element whose type coerce couldn't convert to the array's element type
		// (e.g. a number into a ptr/string element) — reject cleanly instead of
		// emitting an invalid `store <elemTy> <mismatchedRef>`. Composite element
		// types (nested array, object, Map/Set, any, nullable scalar) legitimately
		// carry a different storage IR than their Type.IR, so they're exempt —
		// storeArrayElem handles those. This compiler's arrays are homogeneous.
		if val.Ty.IR != elemTy.IR && !elemTy.IsArray && !elemTy.IsDynamic && !isNullableScalar(elemTy) {
			return "", 0, fmt.Errorf("%d:%d: array elements must share one type — element %d does not match the array's element type (a heterogeneous array is not supported)", elem.GetPos().Line, elem.GetPos().Col, i)
		}
		// The IR=="ptr" trap: an array-shaped element reports Ty.IR=="ptr" just
		// like an object/string slot, so the IR-equality check above cannot see
		// a nested-array element landing in a non-array slot (e.g. `[obj, [x]]`,
		// whose element type infers to the first element's object type). Storing
		// the array's `{ptr,i64}` aggregate into a bare ptr slot would be invalid
		// IR — reject cleanly. A dynamic/any elemTy boxes arrays via storeArrayElem
		// and is exempt; an array-of-arrays elemTy (both sides IsArray) matches.
		if val.Ty.IsArray != elemTy.IsArray && !elemTy.IsDynamic {
			return "", 0, fmt.Errorf("%d:%d: array elements must share one type — element %d does not match the array's element type (a heterogeneous array is not supported)", elem.GetPos().Line, elem.GetPos().Col, i)
		}
		// Both sides arrays, but with different storage (`[[7], new
		// Int32Array([8])]`: a number[] slot handed an i32 buffer) — storing the
		// header would silently reinterpret the element bits (ADR-01059). Under
		// -compat=js inferArrayType already boxed such a literal to `any[]`; a
		// strict literal, or an explicit `number[][]` annotation, is rejected.
		if val.Ty.IsArray && elemTy.IsArray && !elemTy.IsDynamic && !arrayStorageCompatible(val.Ty, elemTy) {
			return "", 0, fmt.Errorf("%d:%d: array elements must share one type — element %d is a %s, not a %s (a heterogeneous array is not supported; under -compat=js it becomes any[])", elem.GetPos().Line, elem.GetPos().Col, i, arrayTypeName(val.Ty), arrayTypeName(elemTy))
		}
		gepReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %d", gepReg, elemTy.IR, dataReg, i))
		e.storeArrayElem(gepReg, elemTy, val)
	}
	return dataReg, n, nil
}

// emitArrayLiteralAggregate builds an array literal as a {ptr, i64}
// aggregate Value (TDD-00028) — the general "array as an expression"
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
	if hasSpread {
		var err error
		dataReg, lenVal, err = e.emitSpreadArrayLitData(lit, elemTy)
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

	r0 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", r0, dataReg))
	r1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %s, 1", r1, r0, lenVal))
	return Value{Ref: r1, Ty: ArrayOf(elemTy)}, nil
}

// emitNewArraySizedAggregate builds `new Array<T>(size)` (dynamic length,
// zero-initialized) as a {ptr, i64} aggregate — the general-expression
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
	if elemTy.IsArray {
		return Value{}, fmt.Errorf("%d:%d: new Array<T>(n) does not yet support an array-typed element (nested arrays) — use an array literal instead.", na.GetPos().Line, na.GetPos().Col)
	}
	sizeVal, err := e.emitExpr(na.Size)
	if err != nil {
		return Value{}, err
	}
	sizeVal, err = e.coerceChecked(sizeVal, TypeI64, na.Size.GetPos(), "new Array size")
	if err != nil {
		return Value{}, err
	}
	e.ensureCalloc()
	dataReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @calloc(i64 %s, i64 %d)", dataReg, sizeVal.Ref, elemTy.Align()))
	r0 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", r0, dataReg))
	r1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %s, 1", r1, r0, sizeVal.Ref))
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
	if srcTy := e.inferExprType(s.Init); srcTy.IsTuple {
		objVal, err := e.emitExpr(s.Init)
		if err != nil {
			return err
		}
		return e.unpackTuplePatternInto(objVal.Ref, srcTy, s.Elems, s.GetPos())
	}
	dataPtr, lenVal, elemTy, err := e.resolveArrayDataPtr(s.Init, s.GetPos())
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
			e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", newPtr, byteCount))

			srcGep := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %d", srcGep, elemTy.IR, dataPtr, i))
			e.emitInstr(fmt.Sprintf("call ptr @memcpy(ptr %s, ptr %s, i64 %s)", newPtr, srcGep, byteCount))

			slot := e.newArrayHeaderSlot(newPtr, restLen)
			e.define(elem.Name, Symbol{Ptr: slot, Ty: ArrayOf(elemTy)})
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
		if elem.Default != nil && elemTy.IsUndefined && elem.SubArray == nil && elem.SubObject == nil {
			defVal, derr := e.emitExpr(elem.Default)
			if derr != nil {
				return derr
			}
			if defVal.Ty.IsArray {
				hdr := e.boxArrayValue(defVal)
				slot := e.freshReg()
				e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
				e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", hdr, slot))
				e.define(elem.Name, Symbol{Ptr: slot, Ty: defVal.Ty})
			} else {
				slot := e.freshReg()
				e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", slot, defVal.Ty.IR, defVal.Ty.Align()))
				e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", defVal.Ty.IR, defVal.Ref, slot, defVal.Ty.Align()))
				e.define(elem.Name, Symbol{Ptr: slot, Ty: defVal.Ty})
			}
			continue
		}

		inBoundsReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ult i64 %d, %s", inBoundsReg, i, lenVal))
		okL := e.freshLabel("destr.ok")
		oobL := e.freshLabel("destr.oob")
		afterL := e.freshLabel("destr.after")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", inBoundsReg, okL, oobL))

		// A destructured element that's itself an array is stored under the
		// object-reference model (TDD-00127): a stable slot holding a pointer to
		// a heap {data, len} header. Each branch builds its own header from the
		// aggregate it produces and points the shared slot at it. See
		// docs/tdd/TDD-00029.md.
		if elemTy.IsArray {
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
				if !defVal.Ty.IsArray {
					return fmt.Errorf("%d:%d: destructuring default must be an array to match '%s'", elem.Default.GetPos().Line, elem.Default.GetPos().Col, elem.Name)
				}
				defHeader := e.boxArrayValue(defVal)
				e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", defHeader, slot))
			} else {
				emptyHeader := e.newArrayHeader("null", "0")
				e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", emptyHeader, slot))
			}
			e.emitTerminator(fmt.Sprintf("br label %%%s", afterL))

			e.emitLabel(afterL)
			e.define(elem.Name, Symbol{Ptr: slot, Ty: elemTy})
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
			e.define(elem.Name, Symbol{Ptr: nsPtr, Ty: nty, NullableBoxed: true})
			continue
		}

		localPtr := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", localPtr, elemTy.IR, elemTy.Align()))

		e.emitLabel(okL)
		gepReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %d", gepReg, elemTy.IR, dataPtr, i))
		val := e.loadArrayElem(gepReg, elemTy)
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", elemTy.IR, val.Ref, localPtr, elemTy.Align()))
		e.emitTerminator(fmt.Sprintf("br label %%%s", afterL))

		e.emitLabel(oobL)
		if elem.Default != nil {
			defVal, err := e.emitExpr(elem.Default)
			if err != nil {
				return err
			}
			defVal = e.coerce(defVal, elemTy)
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
		e.define(elem.Name, Symbol{Ptr: localPtr, Ty: bindTy})
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
	if elem.Default != nil {
		return fmt.Errorf("a default value on a nested destructuring pattern is not yet supported")
	}
	inBounds := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ult i64 %d, %s", inBounds, i, lenVal))
	okL := e.freshLabel("destr.nok")
	oobL := e.freshLabel("destr.noob")
	afterL := e.freshLabel("destr.nafter")

	if elem.SubArray != nil {
		if !elemTy.IsArray || elemTy.ElemType == nil {
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
		e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", p, val.Ref))
		e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 1", l, val.Ref))
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
	if !elemTy.IsObject {
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
	// A string source destructures its characters (`const [a, b] = "xy"` →
	// a="x", b="y"), one 1-byte character string per element — the same
	// byte-string model for-of and Array.from use (ADR-00536). Checked before
	// the node-kind switch so a string identifier or literal is handled here
	// rather than falling through to the "not an array" errors below.
	if isForOfStringTy(e.inferExprType(init)) {
		sv, sErr := e.emitExpr(init)
		if sErr != nil {
			return "", "", Type{}, sErr
		}
		charArr := e.emitStringToCharArray(sv)
		ptrReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", ptrReg, charArr.Ref))
		lenReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 1", lenReg, charArr.Ref))
		return ptrReg, lenReg, TypePtr, nil
	}
	if id, ok := init.(*ast.Identifier); ok && e.isDynamicBinding(id.Name) {
		// A union binding the checker narrows to its array member.
		val, verr := e.emitExpr(id)
		if verr != nil {
			return "", "", Type{}, verr
		}
		if !val.Ty.IsArray || val.Ty.ElemType == nil {
			return "", "", Type{}, fmt.Errorf("%d:%d: '%s' is not an array", pos.Line, pos.Col, id.Name)
		}
		ptrReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", ptrReg, val.Ref))
		lenReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 1", lenReg, val.Ref))
		return ptrReg, lenReg, *val.Ty.ElemType, nil
	}
	switch src := init.(type) {
	case *ast.Identifier:
		sym, found := e.lookup(src.Name)
		if found && sym.Ty.IsFlatArray {
			return "", "", Type{}, fmt.Errorf("%d:%d: a @value array supports index read/write, .length, for...of, and .push — destructuring '%s' needs a regular (pointer-element) array", pos.Line, pos.Col, src.Name)
		}
		if !found || !sym.Ty.IsArray {
			return "", "", Type{}, fmt.Errorf("%d:%d: '%s' is not an array", pos.Line, pos.Col, src.Name)
		}
		dataSlot, lenSlot := e.arrayDataLenSlots(sym)
		dataPtr = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", dataPtr, dataSlot))
		lenReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", lenReg, lenSlot))
		return dataPtr, lenReg, *sym.Ty.ElemType, nil

	case *ast.CallExpression:
		val, callErr := e.emitExpr(src)
		if callErr != nil {
			return "", "", Type{}, callErr
		}
		if !val.Ty.IsArray {
			return "", "", Type{}, fmt.Errorf("%d:%d: function call does not return an array", pos.Line, pos.Col)
		}
		ptrReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", ptrReg, val.Ref))
		lenReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 1", lenReg, val.Ref))
		return ptrReg, lenReg, *val.Ty.ElemType, nil

	case *ast.ArrayLiteral:
		elemTy = *e.inferArrayType(src).ElemType
		dataReg, n, litErr := e.emitArrayLiteralData(src, elemTy)
		if litErr != nil {
			return "", "", Type{}, litErr
		}
		return dataReg, fmt.Sprintf("%d", n), elemTy, nil
	}
	return "", "", Type{}, fmt.Errorf("%d:%d: array destructuring requires an array variable, function call, or array literal", pos.Line, pos.Col)
}

func (e *Emitter) resolveArrayForHOF(objExpr ast.Expression, pos ast.Pos) (ptrReg, lenReg string, elemTy Type, err error) {
	if id, ok := objExpr.(*ast.Identifier); ok && !e.isDynamicBinding(id.Name) {
		sym, found := e.lookup(id.Name)
		if !found || !sym.Ty.IsArray {
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
		dataSlot, lenSlot := e.arrayDataLenSlots(sym)
		ptrReg = e.freshReg()
		lenReg = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", ptrReg, dataSlot))
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", lenReg, lenSlot))
		return
	}
	// Non-identifier: evaluate and extract from {ptr, i64} aggregate.
	var val Value
	val, err = e.emitExpr(objExpr)
	if err != nil {
		return
	}
	if !val.Ty.IsArray {
		err = fmt.Errorf("%d:%d: value is not an array", pos.Line, pos.Col)
		return
	}
	e.emitNotIterableValueGuard(objExpr, val)
	elemTy = TypeI64
	if val.Ty.ElemType != nil {
		elemTy = *val.Ty.ElemType
	}
	ptrReg = e.freshReg()
	lenReg = e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", ptrReg, val.Ref))
	e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 1", lenReg, val.Ref))
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
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", newPtr, byteCount))
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
	if elemTy.IsDynamic {
		e.ensureAnyEq()
		eq := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_any_eq(i64 %s, i64 %s)", eq, aReg, bReg))
		return eq
	}
	if elemTy.IR == "ptr" && !elemTy.IsArray && !elemTy.IsObject {
		e.ensureStrcmp()
		cmp := e.freshReg()
		eq := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @strcmp(ptr %s, ptr %s)", cmp, aReg, bReg))
		e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 0", eq, cmp))
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
	if isStringTy(idxVal.Ty) && !idxVal.Ty.IsClass && !idxVal.Ty.IsObject {
		e.ensureStrtoll()
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @strtoll(ptr %s, ptr null, i32 10)", r, idxVal.Ref))
		return Value{Ref: r, Ty: TypeI64}, nil
	}
	// A non-numeric, non-string index (e.g. an object or Symbol) has no sound
	// conversion to an integer slot — coerceChecked rejects it cleanly rather
	// than letting the bare coerce fall through and leave a `ptr` where an i64
	// is required (the A1 invalid-IR cluster; matches tsc, which rejects a
	// non-number index type). ADR-00882/00883 did the same for DataView/String.
	return e.coerceChecked(idxVal, TypeI64, pos, "array index")
}

// emitArrayIndexOf implements arr.indexOf(val): returns the index of the first
// element equal to val, or -1 if not found.

// isDynamicBinding reports whether name is bound to a box (any, a union):
// an array read of it goes through its value, which the checker's
// narrowing unboxes, not through an array binding's header slot.
func (e *Emitter) isDynamicBinding(name string) bool {
	sym, ok := e.lookup(name)
	return ok && sym.Ty.IsDynamic && !sym.Ty.IsArray
}

// emitSpreadBoxLoop boxes n concrete elements of type srcElem at src into the
// i64 box slots at dst.
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
	if srcElem.IsArray {
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
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", boxed.Ref, dg))
	next := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", next, i))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", next, idx))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(endL)
	return nil
}
