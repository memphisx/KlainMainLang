package llvm

import (
	"KlainMainLang/ast"
	"fmt"
	"strconv"
)

func (e *Emitter) emitArrayConcat(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	// JS concat takes any number of arguments — each an array (flattened
	// one level) or a plain value (appended) — and zero arguments copy the
	// receiver (ADR-00463; previously exactly one array argument).
	hdr1, ptrReg1, lenReg1, elemTy, err := e.resolveArrayHOF(mem.Object, pos)
	if err != nil {
		return Value{}, err
	}
	hdr1 = e.hofHolesHeader(mem.Object, hdr1)
	type concatPart struct {
		ptr, length string // array part
		hdr         string // array part's header, when it may hold holes
		srcElemTy   Type   // array part's own element type
		scalar      *Value // scalar part (length 1)
	}
	// An `any` argument may hold an array to spread or any other value, so
	// the result is an any[] (concatWidensToAny); the receiver's own
	// elements are boxed into it.
	srcElemTy := elemTy
	elemTy = e.concatElemType(mem.Object, elemTy, args)
	var parts []concatPart
	for _, arg := range args {
		if argTy := e.inferExprType(arg); argTy.IsDynamic() && elemTy.IsDynamic() {
			// An `any` holding an Array is spread (IsConcatSpreadable);
			// any other value is appended — decided on the box at runtime.
			p, l, err := e.emitConcatAnyPart(arg)
			if err != nil {
				return Value{}, err
			}
			parts = append(parts, concatPart{ptr: p, length: l, srcElemTy: TypeAny})
			continue
		}
		if e.inferExprType(arg).IsArray() {
			h, p, l, aet, err := e.resolveArrayHOF(arg, pos)
			if err != nil {
				return Value{}, err
			}
			parts = append(parts, concatPart{ptr: p, length: l, hdr: e.hofHolesHeader(arg, h), srcElemTy: aet})
			continue
		}
		v, err := e.emitExpr(arg)
		if err != nil {
			return Value{}, err
		}
		if v, err = e.convertChecked(v, elemTy, arg.GetPos(), "concat argument"); err != nil {
			return Value{}, err
		}
		parts = append(parts, concatPart{scalar: &v})
	}
	e.ensureMalloc()
	e.ensureMemcpy()

	newLen := lenReg1
	for _, p := range parts {
		next := e.freshReg()
		if p.scalar != nil {
			e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", next, newLen))
		} else {
			e.emitInstr(fmt.Sprintf("%s = add i64 %s, %s", next, newLen, p.length))
		}
		newLen = next
	}
	totalBytes := e.freshReg()
	newPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %d", totalBytes, newLen, elemTy.Align()))
	e.heapMallocInto(newPtr, totalBytes)

	if elemTy.IsDynamic() && !srcElemTy.IsDynamic() {
		e.emitBoxCopyLoop(newPtr, ptrReg1, lenReg1, srcElemTy)
	} else {
		bytes1 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %d", bytes1, lenReg1, elemTy.Align()))
		e.emitInstr(fmt.Sprintf("call ptr @memcpy(ptr %s, ptr %s, i64 %s)", newPtr, ptrReg1, bytes1))
	}

	off := lenReg1
	for _, p := range parts {
		dst := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", dst, elemTy.IR, newPtr, off))
		next := e.freshReg()
		if p.scalar != nil {
			e.heapStore(elemTy.IR, p.scalar.Ref, dst, elemTy.Align())
			e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", next, off))
		} else if elemTy.IsDynamic() && !p.srcElemTy.IsDynamic() {
			// Concatenating a concrete-element array into a boxed-element array
			// (`any[]`, TDD-00200): the source slots hold raw scalars, not NaN
			// boxes, so a byte-copy would mis-tag them. Copy element-wise,
			// boxing each source value to `any` on the way in.
			e.emitBoxCopyLoop(dst, p.ptr, p.length, p.srcElemTy)
			e.emitInstr(fmt.Sprintf("%s = add i64 %s, %s", next, off, p.length))
		} else {
			nb := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %d", nb, p.length, elemTy.Align()))
			e.emitInstr(fmt.Sprintf("call ptr @memcpy(ptr %s, ptr %s, i64 %s)", dst, p.ptr, nb))
			e.emitInstr(fmt.Sprintf("%s = add i64 %s, %s", next, off, p.length))
		}
		off = next
	}

	r1 := e.newArrayHeader(newPtr, newLen)
	// Each array part keeps its holes.
	e.emitHolesCopy(r1, "0", hdr1, "0", lenReg1)
	at := lenReg1
	for _, p := range parts {
		next := e.freshReg()
		if p.scalar != nil {
			e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", next, at))
		} else {
			e.emitHolesCopy(r1, at, p.hdr, "0", p.length)
			e.emitInstr(fmt.Sprintf("%s = add i64 %s, %s", next, at, p.length))
		}
		at = next
	}
	return Value{Ref: r1, Ty: ArrayOf(elemTy)}, nil
}

// concatElemType is the element type of recv.concat(args) over recv's
// elemTy: an `any` argument makes an any[] (concatWidensToAny); an empty
// literal receiver takes its arguments' element type, the one they share or
// any, under -compat=js only: to tsc it is never[], and the strict lane
// rejects what tsc does.
func (e *Emitter) concatElemType(recv ast.Expression, elemTy Type, args []ast.Expression) Type {
	if e.concatWidensToAny(elemTy, args) {
		return TypeAny
	}
	lit, ok := recv.(*ast.ArrayLiteral)
	if !ok || len(lit.Elements) != 0 || len(args) == 0 || !e.compatJS() {
		return elemTy
	}
	var shared *Type
	for _, arg := range args {
		t := e.inferExprType(arg)
		if t.IsArray() && t.ElemType != nil {
			t = *t.ElemType
		}
		if shared == nil {
			shared = &t
		} else if arrayStorageKey(ArrayOf(*shared)) != arrayStorageKey(ArrayOf(t)) {
			return TypeAny
		}
	}
	return *shared
}

// concatWidensToAny reports whether Array.prototype.concat over elements of
// elemTy with args makes an any[]: an `any` argument is spread when it holds
// an array (IsConcatSpreadable) and appended otherwise, whatever it holds,
// which only boxed elements can carry.
func (e *Emitter) concatWidensToAny(elemTy Type, args []ast.Expression) bool {
	if elemTy.IsDynamic() {
		return false
	}
	for _, arg := range args {
		t := e.inferExprType(arg)
		if t.IsDynamic() {
			return true
		}
		// An undefined or null appended to scalar elements: only a box
		// holds it.
		if (t.IsUndefined() || t.IsNull() || t.IR == "void") && !elemTy.Nullable() && elemTy.IR != "ptr" {
			return true
		}
	}
	return false
}

// emitConcatAnyPart is concat's view of an `any` argument: an Array's
// elements (boxed), or a one-slot run holding the value itself.
func (e *Emitter) emitConcatAnyPart(arg ast.Expression) (ptrReg, lenReg string, err error) {
	v, err := e.emitExpr(arg)
	if err != nil {
		return "", "", err
	}
	if v, err = e.emitBoxValue(v); err != nil {
		return "", "", err
	}
	e.ensureDynJSONC()
	if !e.declaredAnyArrView {
		e.declaredAnyArrView = true
		e.emitGlobal("declare ptr @__kml_anyarr_view(i64)")
		e.emitGlobal("declare void @__kml_anyarr_sync(i64, ptr)")
	}
	one, ptrSlot, lenSlot := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", one))
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", ptrSlot))
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", lenSlot))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", v.Ref, one))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", one, ptrSlot))
	e.emitInstr(fmt.Sprintf("store i64 1, ptr %s, align 8", lenSlot))
	isArr := e.emitDynIsArray(v)
	arrL, doneL := e.freshLabel("concat.spread"), e.freshLabel("concat.part")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isArr.Ref, arrL, doneL))
	e.emitLabel(arrL)
	view := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_anyarr_view(i64 %s)", view, v.Ref))
	data, lp, n := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", data, view))
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 1", lp, arrayHeaderTy, view))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", n, lp))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", data, ptrSlot))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", n, lenSlot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	ptrReg, lenReg = e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", ptrReg, ptrSlot))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", lenReg, lenSlot))
	return ptrReg, lenReg, nil
}

// emitArrayReverse implements arr.reverse(): reverses elements in place and
// returns the same array (mutates the original).
func (e *Emitter) emitArrayReverse(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 0 {
		return Value{}, fmt.Errorf("%d:%d: reverse takes no arguments", pos.Line, pos.Col)
	}
	hdr, ptrReg, lenReg, elemTy, err := e.resolveArrayHOF(mem.Object, pos)
	if err != nil {
		return Value{}, err
	}
	e.emitArrayGuardFor(mem.Object, arrOpReorder, nil)
	e.emitReverseInPlace(ptrReg, lenReg, elemTy)
	e.emitHolesCall("__kml_holes_reverse", e.hofHolesHeader(mem.Object, hdr), lenReg)
	r1 := e.receiverArrayHeader(hdr, ptrReg, lenReg)
	return Value{Ref: r1, Ty: ArrayOf(elemTy)}, nil
}

// emitReverseInPlace reverses ptrReg[0:lenReg] via a swap loop over the first
// half — shared by emitArrayReverse (reverses the caller's own array) and
// emitArrayToReversed (reverses a fresh copy, leaving the original untouched).
func (e *Emitter) emitReverseInPlace(ptrReg, lenReg string, elemTy Type) {
	halfLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = udiv i64 %s, 2", halfLen, lenReg))
	idxAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idxAlloca))

	condL := e.freshLabel("rev.cond")
	bodyL := e.freshLabel("rev.body")
	doneL := e.freshLabel("rev.done")

	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	idxVal := e.freshReg()
	atHalf := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idxVal, idxAlloca))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", atHalf, idxVal, halfLen))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", atHalf, doneL, bodyL))

	e.emitLabel(bodyL)
	lenM1 := e.freshReg()
	jIdx := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, 1", lenM1, lenReg))
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", jIdx, lenM1, idxVal))
	gepI := e.freshReg()
	gepJ := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", gepI, elemTy.IR, ptrReg, idxVal))
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", gepJ, elemTy.IR, ptrReg, jIdx))
	valI := e.freshReg()
	valJ := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", valI, elemTy.IR, gepI, elemTy.Align()))
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", valJ, elemTy.IR, gepJ, elemTy.Align()))
	e.heapStore(elemTy.IR, valJ, gepI, elemTy.Align())
	e.heapStore(elemTy.IR, valI, gepJ, elemTy.Align())
	idxNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", idxNext, idxVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", idxNext, idxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(doneL)
}

// emitArrayToReversed implements arr.toReversed(): reverses a fresh copy,
// leaving arr untouched.
func (e *Emitter) emitArrayToReversed(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 0 {
		return Value{}, fmt.Errorf("%d:%d: toReversed takes no arguments", pos.Line, pos.Col)
	}
	ptrReg, lenReg, elemTy, err := e.resolveArrayForHOF(mem.Object, pos)
	if err != nil {
		return Value{}, err
	}
	newPtr := e.emitArrayCopy(ptrReg, lenReg, elemTy)
	e.emitReverseInPlace(newPtr, lenReg, elemTy)
	r1 := e.newArrayHeader(newPtr, lenReg)
	return Value{Ref: r1, Ty: e.copyResultType(mem.Object, elemTy)}, nil
}

// emitArrayFill implements arr.fill(val[, start[, end]]): fills elements in
// [start, end) with val in place and returns the same array.
func (e *Emitter) emitArrayFill(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 1 || len(args) > 3 {
		return Value{}, fmt.Errorf("%d:%d: fill takes 1–3 arguments", pos.Line, pos.Col)
	}
	hdr, ptrReg, lenReg, elemTy, err := e.resolveArrayHOF(mem.Object, pos)
	if err != nil {
		return Value{}, err
	}
	e.emitArrayGuardFor(mem.Object, arrOpReorder, nil)
	fillVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	// A numeric element filled with an array: a Buffer repeats a typed
	// array's bytes, and any other fill takes the value's ToNumber
	// (`[5]` → 5, `[1, 2]` → NaN → 0), as Node does.
	var pattern *Value
	if !elemTy.IsArray() && elemTy.IR != "ptr" && fillVal.Ty.IsArray() && !fillVal.Ty.IsDynamic() {
		if taTy := e.inferExprType(mem.Object); taTy.IsBuffer() && (fillVal.Ty.IsTypedArray() || fillVal.Ty.IsBuffer()) {
			pattern = &fillVal
		} else if n, err := e.emitUnaryPlus(fillVal, args[0].GetPos()); err == nil {
			fillVal = n
		}
	}
	// TDD-00101: bigint-element / clamped TypedArrays convert the fill value
	// through the store conversion layer instead of the plain coerce.
	if pattern != nil {
		// Stored below as bytes, not as one element.
	} else if taTy := e.inferExprType(mem.Object); taTy.BigIntElem || taTy.Clamped {
		fillVal, err = e.toTypedArrayElem(fillVal, taTy, pos)
		if err != nil {
			return Value{}, err
		}
	} else {
		fillVal, err = e.convertChecked(fillVal, elemTy, args[0].GetPos(), "array element")
		if err != nil {
			return Value{}, err
		}
	}
	// Box once, outside the loop below, and store the same box pointer into
	// every filled slot — real JS's own .fill() semantics for a reference
	// type share one reference across every slot (`[{}].fill(obj)` doesn't
	// clone obj per slot), and a nested array is no different. Boxing fresh
	// per slot inside the loop would give every slot its own equal-by-value
	// but not reference-equal copy instead.
	fillSlotRef := fillVal.Ref
	if elemTy.IsArray() && pattern == nil {
		fillSlotRef = e.boxArrayValue(fillVal)
	}

	var startN string
	if len(args) >= 2 {
		sr, err := e.emitExpr(args[1])
		if err != nil {
			return Value{}, err
		}
		sIdx, err := e.arrayIndexToI64(sr, args[1].GetPos())
		if err != nil {
			return Value{}, err
		}
		startN = e.emitNormalizeSliceIdx(sIdx.Ref, lenReg)
	} else {
		startN = "0"
	}
	var endN string
	if len(args) >= 3 {
		if endN, err = e.emitEndIndex(args[2], lenReg); err != nil {
			return Value{}, err
		}
	} else {
		endN = lenReg
	}

	if pattern != nil {
		e.emitBufferFillPattern(*pattern, ptrReg, startN, endN)
		r1 := e.receiverArrayHeader(hdr, ptrReg, lenReg)
		return Value{Ref: r1, Ty: ArrayOf(elemTy)}, nil
	}

	idxAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxAlloca))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", startN, idxAlloca))

	condL := e.freshLabel("fill.cond")
	bodyL := e.freshLabel("fill.body")
	doneL := e.freshLabel("fill.done")

	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	idxVal := e.freshReg()
	done := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idxVal, idxAlloca))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", done, idxVal, endN))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", done, doneL, bodyL))

	e.emitLabel(bodyL)
	gep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", gep, elemTy.IR, ptrReg, idxVal))
	e.heapStore(elemTy.IR, fillSlotRef, gep, elemTy.Align())
	idxNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", idxNext, idxVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", idxNext, idxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(doneL)
	e.emitHolesCall("__kml_holes_clear", e.hofHolesHeader(mem.Object, hdr), startN, endN)
	r1 := e.receiverArrayHeader(hdr, ptrReg, lenReg)
	return Value{Ref: r1, Ty: ArrayOf(elemTy)}, nil
}

// emitBufferFillPattern is buf.fill(view, start, end) for a typed array view:
// the view's bytes repeated over [start, end) of the Buffer's bytes at dst.
// An empty view is Node's ERR_INVALID_ARG_VALUE.
func (e *Emitter) emitBufferFillPattern(view Value, dst, start, end string) {
	data, n := e.arrayParts(view)
	width := int64(1)
	if view.Ty.ElemType != nil {
		width = int64(view.Ty.ElemType.Align()) // a scalar element's size
	}
	bytes := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %d", bytes, n, width))
	empty := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", empty, bytes))
	throwL, okL := e.freshLabel("buffill.empty"), e.freshLabel("buffill.ok")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", empty, throwL, okL))
	e.emitLabel(throwL)
	shown := "<Buffer >"
	if !view.Ty.IsBuffer() {
		shown = typedArrayConstructorName(view.Ty) + "(0) []"
	}
	e.emitThrowCoded("TypeError", "ERR_INVALID_ARG_VALUE", e.internString("The argument 'value' is invalid. Received "+shown))
	e.emitLabel(okL)
	e.ensureBufferCodecs()
	e.emitInstr(fmt.Sprintf("call void @__kml_buf_fill_bytes(ptr %s, i64 %s, i64 %s, ptr %s, i64 %s)", dst, start, end, data, bytes))
}

// emitArrayAt implements arr.at(index): returns the element at the given index
// with negative-index support, as `T | undefined` — an out-of-range index
// yields a real absent value (TDD-00187; a nested-array element still
// zero-fills, ADR-00246).
func (e *Emitter) emitArrayAt(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: at takes exactly 1 argument", pos.Line, pos.Col)
	}
	ptrReg, lenReg, elemTy, err := e.resolveArrayForHOF(mem.Object, pos)
	if err != nil {
		return Value{}, err
	}
	idxRaw, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	// Real JS .at() index handling: a negative index counts from the end and,
	// unlike .slice()'s clamp, one still negative after `+ len` is simply out
	// of range (`[10,20,30].at(-5)` is `undefined`, not the first element).
	rawIVal, err := e.arrayIndexToI64(idxRaw, args[0].GetPos())
	if err != nil {
		return Value{}, err
	}
	rawI := rawIVal.Ref
	isNeg := e.freshReg()
	plusLen := e.freshReg()
	normIdx := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 0", isNeg, rawI))
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, %s", plusLen, rawI, lenReg))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", normIdx, isNeg, plusLen, rawI))

	// An array-typed result (nested array, TDD-00029) is its header; the OOB
	// branch stores the null header, undefined.
	resIR := StructFieldIR(elemTy)
	resultAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", resultAlloca, resIR, elemTy.Align()))
	if elemTy.IsArray() {
		e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", resultAlloca))
	} else {
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", elemTy.IR, missRef(elemTy), resultAlloca, elemTy.Align()))
	}

	nonNeg := e.freshReg()
	below := e.freshReg()
	inBounds := e.freshReg()
	loadL := e.freshLabel("at.load")
	doneL := e.freshLabel("at.done")
	e.emitInstr(fmt.Sprintf("%s = icmp sge i64 %s, 0", nonNeg, normIdx))
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, %s", below, normIdx, lenReg))
	e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", inBounds, nonNeg, below))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", inBounds, loadL, doneL))

	e.emitLabel(loadL)
	gep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", gep, elemTy.IR, ptrReg, normIdx))
	elem := e.loadArrayElem(gep, elemTy)
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", resIR, elem.Ref, resultAlloca, elemTy.Align()))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))

	e.emitLabel(doneL)
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", result, resIR, resultAlloca, elemTy.Align()))
	// TDD-00101: a bigint-element TypedArray's .at() surfaces a bigint
	// handle. Out of range that handle is `undefined` (a flagged null,
	// TDD-00187): the wrap-to-handle call only makes sense for a real
	// element, so select null on the miss path.
	if taTy := e.inferExprType(mem.Object); taTy.BigIntElem {
		handle := e.wrapTypedArrayLoad(Value{Ref: result, Ty: elemTy}, taTy)
		sel := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr null", sel, inBounds, handle.Ref))
		hty := undefinedableElem(handle.Ty)
		return Value{Ref: sel, Ty: hty}, nil
	}
	// `T | undefined`: present exactly when the index was in range. inBounds
	// was computed in the pre-branch block, which dominates the merge.
	return e.wrapUndefinedable(Value{Ref: result, Ty: elemTy}, inBounds), nil
}

// emitArrayWith implements arr.with(index, val): returns a fresh copy of arr
// with the element at index replaced by val — arr itself is untouched.
// Negative indices count from the end (same normalization as .at()); an
// index still out of range after normalization throws a catchable Error,
// matching real JS's RangeError (the same "index out of range" failure this
// compiler already treats as a real, catchable exception, e.g. array
// index-out-of-bounds reads — see docs/adr/00/ADR-00044.md).
func (e *Emitter) emitArrayWith(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 2 {
		return Value{}, fmt.Errorf("%d:%d: with takes exactly 2 arguments (index, value)", pos.Line, pos.Col)
	}
	ptrReg, lenReg, elemTy, err := e.resolveArrayForHOF(mem.Object, pos)
	if err != nil {
		return Value{}, err
	}
	idxRaw, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	idxI, err := e.arrayIndexToI64(idxRaw, args[0].GetPos())
	if err != nil {
		return Value{}, err
	}
	// The relative index, from the end when negative; out of [0, len) is a
	// RangeError (no clamping, unlike slice).
	neg, fromEnd, normIdx := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 0", neg, idxI.Ref))
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, %s", fromEnd, lenReg, idxI.Ref))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", normIdx, neg, fromEnd, idxI.Ref))

	valRaw, err := e.emitExpr(args[1])
	if err != nil {
		return Value{}, err
	}
	val := e.convert(valRaw, elemTy)

	inBounds := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ult i64 %s, %s", inBounds, normIdx, lenReg))
	oobL := e.freshLabel("with.oob")
	okL := e.freshLabel("with.ok")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", inBounds, okL, oobL))

	e.emitLabel(oobL)
	if e.inferExprType(mem.Object).IsTypedArray() {
		e.emitThrowRangeError("Invalid typed array index")
	} else if err := e.emitThrowRangeErrorNum("Invalid index : ", idxI.Ref, ""); err != nil {
		return Value{}, err
	}

	e.emitLabel(okL)
	newPtr := e.emitArrayCopy(ptrReg, lenReg, elemTy)
	slot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", slot, elemTy.IR, newPtr, normIdx))
	e.storeArrayElem(slot, elemTy, val)

	r1 := e.newArrayHeader(newPtr, lenReg)
	return Value{Ref: r1, Ty: e.copyResultType(mem.Object, elemTy)}, nil
}

// --- .flat(depth?) / .flatMap(fn) ---
//
// This compiler's arrays are statically typed with a fixed nesting depth
// (e.g. number[][] is exactly two levels, always) — unlike real JS, where
// .flat(depth)'s depth is an arbitrary runtime value and the result is
// whatever shape falls out of it. Since a KlainMainLang array's element
// type has to be known at compile time, depth has to be too: each level of
// flattening unwraps exactly one level of the receiver's own static array
// nesting, so the *type* of the result depends on depth the same way it
// depends on anything else static. resolveFlatDepth below requires depth
// to be a compile-time constant (a literal, or the bare identifier
// `Infinity` this compiler already recognizes elsewhere — emitIdent in
// emit_exprs.go) and rejects anything else with a clear compile error
// rather than silently guessing — the same "clean rejection over silently
// wrong behavior" precedent TDD-00029's own nested-array HOF guards use.

// maxFlatDepth stands in for `Infinity`: emitArrayFlat's own loop condition
// (curElemTy.IsArray) always stops once the array is fully flat regardless
// of how large depth is, so this only needs to be "larger than any real
// array nesting depth ever reaches," not a genuine unbounded value.
const maxFlatDepth = 1 << 30

// resolveFlatDepth evaluates arr.flat(depth?)'s depth argument as a
// compile-time constant. Omitted defaults to 1, matching real JS.
func (e *Emitter) resolveFlatDepth(args []ast.Expression, pos ast.Pos) (int, error) {
	if len(args) == 0 {
		return 1, nil
	}
	if len(args) > 1 {
		return 0, fmt.Errorf("%d:%d: flat takes 0 or 1 arguments", pos.Line, pos.Col)
	}
	arg := args[0]
	if neg, ok := arg.(*ast.UnaryExpression); ok && neg.Op == "-" {
		if _, ok := neg.Arg.(*ast.NumberLiteral); ok {
			return 0, fmt.Errorf("%d:%d: flat's depth must be a non-negative integer constant", pos.Line, pos.Col)
		}
	}
	if id, ok := arg.(*ast.Identifier); ok && id.Name == "Infinity" {
		return maxFlatDepth, nil
	}
	if lit, ok := arg.(*ast.NumberLiteral); ok {
		n, err := strconv.ParseInt(lit.Value, 0, 64)
		if err != nil || n < 0 {
			return 0, fmt.Errorf("%d:%d: flat's depth must be a non-negative integer constant", pos.Line, pos.Col)
		}
		return int(n), nil
	}
	return 0, fmt.Errorf("%d:%d: flat's depth argument must be a compile-time constant integer (or Infinity) — this compiler's arrays have a fixed nesting depth at the type level, so the result type must be knowable at compile time, not just its value", pos.Line, pos.Col)
}

// emitArrayFlattenOnce concatenates the contents of every inner-array
// element of ptrReg[0:lenReg] into one fresh buffer — exactly one level of
// .flat()'s flattening. elemTy must itself be array-typed (checked by
// every caller before calling this); innerElemTy is elemTy's own ElemType,
// unwrapped one level. Two passes over the outer array, the same
// sum-then-copy shape emitSpreadArrayLitData uses for a statically-known
// list of sources, generalized to a runtime loop over however many inner
// arrays the outer array actually holds: pass 1 sums every inner array's
// length (each outer element unboxed via loadArrayElem, which already
// transparently unboxes a nested-array element regardless of how it's
// nested — TDD-00029); pass 2 walks again, memcpy-ing each inner array's
// own bytes into the accumulating output at a running cursor offset.
func (e *Emitter) emitArrayFlattenOnce(ptrReg, lenReg string, elemTy Type) (newPtr, newLen string, innerElemTy Type, err error) {
	innerElemTy = *elemTy.ElemType

	totalAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", totalAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", totalAlloca))
	idxAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idxAlloca))

	sumCondL := e.freshLabel("flat.sumcond")
	sumBodyL := e.freshLabel("flat.sumbody")
	sumDoneL := e.freshLabel("flat.sumdone")
	e.emitTerminator(fmt.Sprintf("br label %%%s", sumCondL))
	e.emitLabel(sumCondL)
	idxVal := e.freshReg()
	sumDone := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idxVal, idxAlloca))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", sumDone, idxVal, lenReg))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", sumDone, sumDoneL, sumBodyL))

	e.emitLabel(sumBodyL)
	sumGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", sumGep, elemTy.IR, ptrReg, idxVal))
	sumInner := e.loadArrayElem(sumGep, elemTy)
	innerLenForSum := e.freshReg()
	innerLenForSum = e.arrayLen(sumInner)
	curTotal := e.freshReg()
	newTotal := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", curTotal, totalAlloca))
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, %s", newTotal, curTotal, innerLenForSum))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", newTotal, totalAlloca))
	sumIdxNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", sumIdxNext, idxVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", sumIdxNext, idxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", sumCondL))

	e.emitLabel(sumDoneL)
	totalFinal := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", totalFinal, totalAlloca))

	e.ensureMalloc()
	e.ensureMemcpy()
	outBytes := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %d", outBytes, totalFinal, innerElemTy.Align()))
	outPtr := e.freshReg()
	e.heapMallocInto(outPtr, outBytes)

	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idxAlloca))
	cursorAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", cursorAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", cursorAlloca))

	copyCondL := e.freshLabel("flat.copycond")
	copyBodyL := e.freshLabel("flat.copybody")
	copyDoneL := e.freshLabel("flat.copydone")
	e.emitTerminator(fmt.Sprintf("br label %%%s", copyCondL))
	e.emitLabel(copyCondL)
	idxVal2 := e.freshReg()
	copyDone := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idxVal2, idxAlloca))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", copyDone, idxVal2, lenReg))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", copyDone, copyDoneL, copyBodyL))

	e.emitLabel(copyBodyL)
	copyGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", copyGep, elemTy.IR, ptrReg, idxVal2))
	copyInner := e.loadArrayElem(copyGep, elemTy)
	innerPtr := e.freshReg()
	innerLen := e.freshReg()
	innerPtr = e.arrayData(copyInner)
	innerLen = e.arrayLen(copyInner)
	cursorVal := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", cursorVal, cursorAlloca))
	dst := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", dst, innerElemTy.IR, outPtr, cursorVal))
	copyBytes := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %d", copyBytes, innerLen, innerElemTy.Align()))
	e.emitInstr(fmt.Sprintf("call ptr @memcpy(ptr %s, ptr %s, i64 %s)", dst, innerPtr, copyBytes))
	newCursor := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, %s", newCursor, cursorVal, innerLen))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", newCursor, cursorAlloca))
	copyIdxNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", copyIdxNext, idxVal2))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", copyIdxNext, idxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", copyCondL))

	e.emitLabel(copyDoneL)
	return outPtr, totalFinal, innerElemTy, nil
}

// emitArrayFlat implements arr.flat(depth?): flattens depth levels of
// nested-array elements (default 1, matching real JS) into a new array,
// leaving arr itself untouched. depth is a compile-time constant
// (resolveFlatDepth) — see this section's own doc comment for why. Always
// returns a fresh array, even when nothing actually gets flattened (depth
// 0, or a receiver that isn't nested at all), matching real JS's own
// `.flat()` always allocating a new array.
func (e *Emitter) emitArrayFlat(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	depth, err := e.resolveFlatDepth(args, pos)
	if err != nil {
		return Value{}, err
	}
	ptrReg, lenReg, elemTy, err := e.resolveArrayForHOF(mem.Object, pos)
	if err != nil {
		return Value{}, err
	}

	if flatHasAbsentRow(elemTy, depth) {
		// A row that may be undefined stays as an element (JS keeps it):
		// the rows go boxed through the dynamic flatten.
		return e.emitFlatBoxedRows(ptrReg, lenReg, elemTy, depth), nil
	}
	if elemTy.IsDynamic() && !elemTy.IsDynamicObject() && elemTy.IR == TypeAny.IR {
		// Boxed elements (`any`, a union): an element that is an Array at run time is
		// spliced in (dynjson.c).
		e.ensureDynJSONC()
		hdr := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_anyarr_flat(ptr %s, i64 %s, i64 %d)", hdr, ptrReg, lenReg, depth))
		return e.arrayValueFromHeaderReg(hdr, ArrayOf(elemTy)), nil
	}
	curPtr, curLen, curElemTy := ptrReg, lenReg, elemTy
	flattened := false
	for i := 0; i < depth && curElemTy.IsArray(); i++ {
		curPtr, curLen, curElemTy, err = e.emitArrayFlattenOnce(curPtr, curLen, curElemTy)
		if err != nil {
			return Value{}, err
		}
		flattened = true
	}
	if !flattened {
		curPtr = e.emitArrayCopy(ptrReg, lenReg, elemTy)
		curLen = lenReg
	}

	r1 := e.newArrayHeader(curPtr, curLen)
	return Value{Ref: r1, Ty: ArrayOf(curElemTy)}, nil
}

// emitArrayFlatMap implements arr.flatMap(fn): arr.map(fn) followed by
// exactly one level of flattening — real JS's flatMap has no depth
// parameter at all, always fixed at 1, so none of emitArrayFlat's
// compile-time-constant-depth machinery is needed here. When fn doesn't
// return an array-typed value per element, flattening by 1 is a no-op (a
// scalar has nothing to flatten), matching real JS exactly — the mapped
// result is returned as-is rather than routing it through
// emitArrayFlattenOnce at all.
func (e *Emitter) emitArrayFlatMap(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: flatMap takes exactly 1 argument", pos.Line, pos.Col)
	}
	mapped, err := e.emitArrayMap(mem, args, pos)
	if err != nil {
		return Value{}, err
	}
	// A callback returning `any`: an array result is flattened, anything
	// else kept, decided per element at run time.
	if mapped.Ty.ElemType != nil && mapped.Ty.ElemType.IsDynamic() {
		return e.emitFlatBoxedRows(e.arrayData(mapped), e.arrayLen(mapped), *mapped.Ty.ElemType, 1), nil
	}
	if mapped.Ty.ElemType == nil || !mapped.Ty.ElemType.IsArray() {
		return mapped, nil
	}
	// A callback that can fall off the end maps to `U[] | undefined` (ADR-01065):
	// JS appends the `undefined` itself as an element, so the rows go boxed
	// through the dynamic flatten.
	if flatHasAbsentRow(*mapped.Ty.ElemType, 1) {
		return e.emitFlatBoxedRows(e.arrayData(mapped), e.arrayLen(mapped), *mapped.Ty.ElemType, 1), nil
	}

	ptrReg := e.freshReg()
	lenReg := e.freshReg()
	ptrReg = e.arrayData(mapped)
	lenReg = e.arrayLen(mapped)
	newPtr, newLen, innerElemTy, err := e.emitArrayFlattenOnce(ptrReg, lenReg, *mapped.Ty.ElemType)
	if err != nil {
		return Value{}, err
	}
	r1 := e.newArrayHeader(newPtr, newLen)
	return Value{Ref: r1, Ty: ArrayOf(innerElemTy)}, nil
}

// emitArrayKeys implements arr.keys(): returns a materialized number[] of
// indices [0, len) — the same "return a materialized array, not a lazy
// iterator" convention Map.keys()/Set.values() already use, since this
// compiler has no general iterator protocol.

// flatHasAbsentRow reports whether flattening an array of elem to depth
// meets a row that may be undefined: JS keeps such a row as an element.
func flatHasAbsentRow(elem Type, depth int) bool {
	for i := 0; i < depth && elem.IsArray(); i++ {
		if elem.Nullable() {
			return true
		}
		if elem.ElemType == nil {
			return false
		}
		elem = *elem.ElemType
	}
	return false
}

// emitFlatBoxedRows flattens rows of elemTy to depth through the dynamic
// flatten (__kml_anyarr_flat): each row boxed, an absent one as undefined.
func (e *Emitter) emitFlatBoxedRows(ptrReg, lenReg string, elemTy Type, depth int) Value {
	e.ensureMalloc()
	bytes := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, 8", bytes, lenReg))
	buf := e.freshReg()
	e.heapMallocInto(buf, bytes)
	idxSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxSlot))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idxSlot))
	condL, bodyL, doneL := e.freshLabel("flatbox.cond"), e.freshLabel("flatbox.body"), e.freshLabel("flatbox.done")
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	idx, more := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idx, idxSlot))
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, %s", more, idx, lenReg))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", more, bodyL, doneL))
	e.emitLabel(bodyL)
	src := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", src, elemTy.IR, ptrReg, idx))
	row := e.genLoadElemAt(src, elemTy)
	boxed, err := e.emitBoxValue(Value{Ref: row, Ty: elemTy})
	if err != nil {
		boxed = Value{Ref: fmt.Sprint(nbUndefined), Ty: TypeAny}
	}
	dst := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %s, i64 %s", dst, buf, idx))
	e.heapStore("i64", boxed.Ref, dst, 8)
	next := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", next, idx))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", next, idxSlot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(doneL)
	e.ensureDynJSONC()
	hdr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_anyarr_flat(ptr %s, i64 %s, i64 %d)", hdr, buf, lenReg, depth))
	return e.arrayValueFromHeaderReg(hdr, ArrayOf(TypeAny))
}
