package llvm

import (
	"KlainMainLang/ast"
	"fmt"
	"strconv"
)

func (e *Emitter) emitArrayConcat(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: concat takes exactly 1 argument", pos.Line, pos.Col)
	}
	ptrReg1, lenReg1, elemTy, err := e.resolveArrayForHOF(mem.Object, pos)
	if err != nil {
		return Value{}, err
	}
	ptrReg2, lenReg2, _, err2 := e.resolveArrayForHOF(args[0], pos)
	if err2 != nil {
		return Value{}, err2
	}
	e.ensureMalloc()
	e.ensureMemcpy()

	newLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, %s", newLen, lenReg1, lenReg2))
	totalBytes := e.freshReg()
	newPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %d", totalBytes, newLen, elemTy.Align()))
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", newPtr, totalBytes))

	bytes1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %d", bytes1, lenReg1, elemTy.Align()))
	e.emitInstr(fmt.Sprintf("call ptr @memcpy(ptr %s, ptr %s, i64 %s)", newPtr, ptrReg1, bytes1))

	dstOff := e.freshReg()
	bytes2 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", dstOff, elemTy.IR, newPtr, lenReg1))
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %d", bytes2, lenReg2, elemTy.Align()))
	e.emitInstr(fmt.Sprintf("call ptr @memcpy(ptr %s, ptr %s, i64 %s)", dstOff, ptrReg2, bytes2))

	r0 := e.freshReg()
	r1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", r0, newPtr))
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %s, 1", r1, r0, newLen))
	return Value{Ref: r1, Ty: ArrayOf(elemTy)}, nil
}

// emitArrayReverse implements arr.reverse(): reverses elements in place and
// returns the same array (mutates the original).
func (e *Emitter) emitArrayReverse(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 0 {
		return Value{}, fmt.Errorf("%d:%d: reverse takes no arguments", pos.Line, pos.Col)
	}
	ptrReg, lenReg, elemTy, err := e.resolveArrayForHOF(mem.Object, pos)
	if err != nil {
		return Value{}, err
	}
	e.emitReverseInPlace(ptrReg, lenReg, elemTy)
	r0 := e.freshReg()
	r1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", r0, ptrReg))
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %s, 1", r1, r0, lenReg))
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
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", elemTy.IR, valJ, gepI, elemTy.Align()))
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", elemTy.IR, valI, gepJ, elemTy.Align()))
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
	r0 := e.freshReg()
	r1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", r0, newPtr))
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %s, 1", r1, r0, lenReg))
	return Value{Ref: r1, Ty: ArrayOf(elemTy)}, nil
}

// emitArrayFill implements arr.fill(val[, start[, end]]): fills elements in
// [start, end) with val in place and returns the same array.
func (e *Emitter) emitArrayFill(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 1 || len(args) > 3 {
		return Value{}, fmt.Errorf("%d:%d: fill takes 1–3 arguments", pos.Line, pos.Col)
	}
	ptrReg, lenReg, elemTy, err := e.resolveArrayForHOF(mem.Object, pos)
	if err != nil {
		return Value{}, err
	}
	fillVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	// TDD-00101: bigint-element / clamped TypedArrays convert the fill value
	// through the store conversion layer instead of the plain coerce.
	if taTy := e.inferExprType(mem.Object); taTy.BigIntElem || taTy.Clamped {
		fillVal, err = e.coerceTypedArrayStore(fillVal, taTy, pos)
		if err != nil {
			return Value{}, err
		}
	} else {
		fillVal = e.coerce(fillVal, elemTy)
	}
	// Box once, outside the loop below, and store the same box pointer into
	// every filled slot — real JS's own .fill() semantics for a reference
	// type share one reference across every slot (`[{}].fill(obj)` doesn't
	// clone obj per slot), and a nested array is no different. Boxing fresh
	// per slot inside the loop would give every slot its own equal-by-value
	// but not reference-equal copy instead.
	fillSlotRef := fillVal.Ref
	if elemTy.IsArray {
		fillSlotRef = e.boxArrayValue(fillVal)
	}

	var startN string
	if len(args) >= 2 {
		sr, err := e.emitExpr(args[1])
		if err != nil {
			return Value{}, err
		}
		startN = e.emitNormalizeSliceIdx(e.coerce(sr, TypeI64).Ref, lenReg)
	} else {
		startN = "0"
	}
	var endN string
	if len(args) >= 3 {
		er, err := e.emitExpr(args[2])
		if err != nil {
			return Value{}, err
		}
		endN = e.emitNormalizeSliceIdx(e.coerce(er, TypeI64).Ref, lenReg)
	} else {
		endN = lenReg
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
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", elemTy.IR, fillSlotRef, gep, elemTy.Align()))
	idxNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", idxNext, idxVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", idxNext, idxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(doneL)
	r0 := e.freshReg()
	r1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", r0, ptrReg))
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %s, 1", r1, r0, lenReg))
	return Value{Ref: r1, Ty: ArrayOf(elemTy)}, nil
}

// emitArrayAt implements arr.at(index): returns the element at the given index
// with negative-index support. Returns zero/null for out-of-range indices.
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
	normIdx := e.emitNormalizeSliceIdx(e.coerce(idxRaw, TypeI64).Ref, lenReg)

	// An array-typed result (nested array, TDD-00029) needs a {ptr,i64}
	// slot, not elemTy.IR's plain "ptr" — the same StructFieldIR convention
	// emitOptionalMember already established for an array-typed field read,
	// including its zero-value shape ({null,0}) for the out-of-range case.
	resIR := StructFieldIR(elemTy)
	resultAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", resultAlloca, resIR, elemTy.Align()))
	if elemTy.IsArray {
		z0 := e.freshReg()
		z1 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr null, 0", z0))
		e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 0, 1", z1, z0))
		e.emitInstr(fmt.Sprintf("store {ptr, i64} %s, ptr %s, align %d", z1, resultAlloca, elemTy.Align()))
	} else {
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", elemTy.IR, zeroRef(elemTy), resultAlloca, elemTy.Align()))
	}

	inBounds := e.freshReg()
	loadL := e.freshLabel("at.load")
	doneL := e.freshLabel("at.done")
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, %s", inBounds, normIdx, lenReg))
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
	// handle (out-of-range gives 0n rather than undefined — same
	// zero-value convention plain arrays already use here).
	if taTy := e.inferExprType(mem.Object); taTy.BigIntElem {
		return e.wrapTypedArrayLoad(Value{Ref: result, Ty: elemTy}, taTy), nil
	}
	return Value{Ref: result, Ty: elemTy}, nil
}

// emitArrayWith implements arr.with(index, val): returns a fresh copy of arr
// with the element at index replaced by val — arr itself is untouched.
// Negative indices count from the end (same normalization as .at()); an
// index still out of range after normalization throws a catchable Error,
// matching real JS's RangeError (the same "index out of range" failure this
// compiler already treats as a real, catchable exception, e.g. array
// index-out-of-bounds reads — see docs/adr/ADR-00044.md).
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
	normIdx := e.emitNormalizeSliceIdx(e.coerce(idxRaw, TypeI64).Ref, lenReg)

	valRaw, err := e.emitExpr(args[1])
	if err != nil {
		return Value{}, err
	}
	val := e.coerce(valRaw, elemTy)

	inBounds := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, %s", inBounds, normIdx, lenReg))
	oobL := e.freshLabel("with.oob")
	okL := e.freshLabel("with.ok")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", inBounds, okL, oobL))

	e.emitLabel(oobL)
	e.emitInternalThrow(e.internString("Array index out of range"))

	e.emitLabel(okL)
	newPtr := e.emitArrayCopy(ptrReg, lenReg, elemTy)
	slot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", slot, elemTy.IR, newPtr, normIdx))
	e.storeArrayElem(slot, elemTy, val)

	r0 := e.freshReg()
	r1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", r0, newPtr))
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %s, 1", r1, r0, lenReg))
	return Value{Ref: r1, Ty: ArrayOf(elemTy)}, nil
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
	e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 1", innerLenForSum, sumInner.Ref))
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
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", outPtr, outBytes))

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
	e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", innerPtr, copyInner.Ref))
	e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 1", innerLen, copyInner.Ref))
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

	curPtr, curLen, curElemTy := ptrReg, lenReg, elemTy
	flattened := false
	for i := 0; i < depth && curElemTy.IsArray; i++ {
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

	r0 := e.freshReg()
	r1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", r0, curPtr))
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %s, 1", r1, r0, curLen))
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
	if mapped.Ty.ElemType == nil || !mapped.Ty.ElemType.IsArray {
		return mapped, nil
	}

	ptrReg := e.freshReg()
	lenReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", ptrReg, mapped.Ref))
	e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 1", lenReg, mapped.Ref))
	newPtr, newLen, innerElemTy, err := e.emitArrayFlattenOnce(ptrReg, lenReg, *mapped.Ty.ElemType)
	if err != nil {
		return Value{}, err
	}
	r0 := e.freshReg()
	r1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", r0, newPtr))
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %s, 1", r1, r0, newLen))
	return Value{Ref: r1, Ty: ArrayOf(innerElemTy)}, nil
}

// emitArrayKeys implements arr.keys(): returns a materialized number[] of
// indices [0, len) — the same "return a materialized array, not a lazy
// iterator" convention Map.keys()/Set.values() already use, since this
// compiler has no general iterator protocol.
