package llvm

import (
	"KlainMainLang/ast"
	"fmt"
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
		slot = e.moduleGlobals[v.Name].Ptr
	} else {
		slot = e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
		e.define(v.Name, Symbol{Ptr: slot, Ty: ty, IsConst: v.Kind == "const"})
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
	if !val.Ty.IsArray {
		return fmt.Errorf("%d:%d: array variable must be initialized with an array expression", v.GetPos().Line, v.GetPos().Col)
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

// arrayArgFromAggregate materializes an array aggregate Value into a fresh
// {data, len} header and returns (headerReg, lenReg) for packing as a (ptr, i64)
// call argument under the object-reference ABI (TDD-00127). A transient
// expression argument gets a fresh header (the callee may mutate it, but it has
// no caller-visible identity); a named-variable argument should instead pass its
// own header pointer so mutations propagate — see arrayDataLenSlots.
func (e *Emitter) arrayArgFromAggregate(val Value) (header, lenReg string) {
	header = e.boxArrayValue(val)
	lenReg = e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 1", lenReg, val.Ref))
	return
}

// packArrayArg produces the (headerReg, lenReg) pair to pass an array argument
// under the object-reference ABI (TDD-00127). When the argument is a named array
// variable, its own header pointer is passed, so an in-place mutation inside the
// callee (push/splice) propagates back to the caller — JS reference semantics.
// Any other array expression is a transient with no caller-visible identity, so
// a fresh header wrapping its aggregate is passed instead. (A member/index
// array — e.g. `obj.field` — currently takes the transient path, so passing an
// object's array field to a mutating function does not propagate; that is a
// known limitation, not new behaviour.)
func (e *Emitter) packArrayArg(arg ast.Expression, val Value) (header, lenReg string) {
	if id, ok := arg.(*ast.Identifier); ok {
		if sym, found := e.lookup(id.Name); found && sym.Ty.IsArray {
			dataSlot, lenSlot := e.arrayDataLenSlots(sym)
			lr := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", lr, lenSlot))
			return dataSlot, lr
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

// bindArrayParam binds an array-typed function/method/closure parameter under
// the object-reference model (TDD-00127). The incoming `%p_<name>_ptr` argument
// is the caller's {data, len} header pointer; a stable slot (`%v_<name>_ptr`)
// holds it, so in-place mutations (push/splice) propagate back to the caller
// while a whole-variable reassignment rebinds only this frame. The companion
// `%p_<name>_len` argument is redundant (length lives in the header) and left
// unused — kept only so the two-word (ptr, i64) array ABI is unchanged.
func (e *Emitter) bindArrayParam(name string, pty Type) {
	slot := "%v_" + name + "_ptr"
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
	e.emitInstr(fmt.Sprintf("store ptr %%p_%s_ptr, ptr %s, align 8", name, slot))
	e.define(name, Symbol{Ptr: slot, Ty: pty})
}

// unboxArrayValue loads the {ptr, i64} aggregate out of a box pointer
// produced by boxArrayValue, returning it as an ordinary array Value —
// exactly the representation every other array-producing expression already
// returns (function return, literal, slice, ...), so nothing downstream of a
// nested-array element read needs to know boxing happened at all.
func (e *Emitter) unboxArrayValue(boxPtr string, elemTy Type) Value {
	agg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load {ptr, i64}, ptr %s, align 8", agg, boxPtr))
	return Value{Ref: agg, Ty: elemTy}
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
	return Value{Ref: result, Ty: elemTy}
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
	if elemTy.IsArray {
		box := e.boxArrayValue(val)
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", box, gepReg))
		return
	}
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", elemTy.IR, val.Ref, gepReg, elemTy.Align()))
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

	// Compute runtime total = staticCount + sum(spread.length).
	totalReg := fmt.Sprintf("%d", staticCount)
	for _, elem := range lit.Elements {
		sp, ok := elem.(*ast.SpreadElement)
		if !ok {
			continue
		}
		spId, ok := sp.Arg.(*ast.Identifier)
		if !ok {
			return "", "", fmt.Errorf("%d:%d: spread element must be an array variable", sp.GetPos().Line, sp.GetPos().Col)
		}
		sym, found := e.lookup(spId.Name)
		if found && sym.Ty.IsFlatArray {
			return "", "", fmt.Errorf("%d:%d: a @value array supports index read/write, .length, for...of, and .push — spreading '%s' needs a regular (pointer-element) array", sp.GetPos().Line, sp.GetPos().Col, spId.Name)
		}
		if !found || !sym.Ty.IsArray {
			return "", "", fmt.Errorf("%d:%d: '%s' is not an array", sp.GetPos().Line, sp.GetPos().Col, spId.Name)
		}
		_, spLenSlot := e.arrayDataLenSlots(sym)
		spLenReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", spLenReg, spLenSlot))
		newTotal := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = add i64 %s, %s", newTotal, totalReg, spLenReg))
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
			spId := sp.Arg.(*ast.Identifier) // already validated above
			sym, _ := e.lookup(spId.Name)
			// Load source ptr and length from its current header.
			srcDataSlot, srcLenSlot := e.arrayDataLenSlots(sym)
			srcPtr := e.freshReg()
			srcLen := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", srcPtr, srcDataSlot))
			e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", srcLen, srcLenSlot))
			// GEP to cursor position in dest.
			cVal := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", cVal, cursorPtr))
			dstReg := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", dstReg, elemTy.IR, dataReg, cVal))
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
	if isNullableScalar(elemTy) || elemTy.UnionMembers != nil {
		return "", 0, fmt.Errorf("%d:%d: a nullable or union array element type (e.g. `(number | null)[]`) is not yet supported — a union is usable as an object field, but not yet as an array element", lit.GetPos().Line, lit.GetPos().Col)
	}
	n = int64(len(lit.Elements))
	e.ensureMalloc()
	dataReg = e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", dataReg, n*int64(elemTy.Align())))
	for i, elem := range lit.Elements {
		val, verr := e.emitExprWithObjectHint(elem, elemTy)
		if verr != nil {
			return "", 0, verr
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
	sizeVal = e.coerce(sizeVal, TypeI64)
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
			e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", elemTy.IR, elemTy.zeroLiteral(), localPtr, elemTy.Align()))
		}
		e.emitTerminator(fmt.Sprintf("br label %%%s", afterL))

		e.emitLabel(afterL)
		e.define(elem.Name, Symbol{Ptr: localPtr, Ty: elemTy})
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
	if id, ok := objExpr.(*ast.Identifier); ok {
		sym, found := e.lookup(id.Name)
		if !found || !sym.Ty.IsArray {
			err = fmt.Errorf("%d:%d: '%s' is not an array", pos.Line, pos.Col, id.Name)
			return
		}
		elemTy = TypeI64
		if sym.Ty.ElemType != nil {
			elemTy = *sym.Ty.ElemType
		}
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
func (e *Emitter) arrayIndexToI64(idxVal Value) Value {
	if isStringTy(idxVal.Ty) && !idxVal.Ty.IsClass && !idxVal.Ty.IsObject {
		e.ensureStrtoll()
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @strtoll(ptr %s, ptr null, i32 10)", r, idxVal.Ref))
		return Value{Ref: r, Ty: TypeI64}
	}
	return e.coerce(idxVal, TypeI64)
}

// emitArrayIndexOf implements arr.indexOf(val): returns the index of the first
// element equal to val, or -1 if not found.
