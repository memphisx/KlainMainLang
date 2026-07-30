package llvm

import (
	"KlainMainLang/ast"
	"fmt"
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
	fillVal = e.coerce(fillVal, elemTy)

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
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", elemTy.IR, fillVal.Ref, gep, elemTy.Align()))
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

	zeroVal := "0"
	if elemTy.IR == "ptr" {
		zeroVal = "null"
	}
	resultAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", resultAlloca, elemTy.IR, elemTy.Align()))
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", elemTy.IR, zeroVal, resultAlloca, elemTy.Align()))

	inBounds := e.freshReg()
	loadL := e.freshLabel("at.load")
	doneL := e.freshLabel("at.done")
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, %s", inBounds, normIdx, lenReg))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", inBounds, loadL, doneL))

	e.emitLabel(loadL)
	gep := e.freshReg()
	elem := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", gep, elemTy.IR, ptrReg, normIdx))
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", elem, elemTy.IR, gep, elemTy.Align()))
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", elemTy.IR, elem, resultAlloca, elemTy.Align()))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))

	e.emitLabel(doneL)
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", result, elemTy.IR, resultAlloca, elemTy.Align()))
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
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", elemTy.IR, val.Ref, slot, elemTy.Align()))

	r0 := e.freshReg()
	r1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", r0, newPtr))
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %s, 1", r1, r0, lenReg))
	return Value{Ref: r1, Ty: ArrayOf(elemTy)}, nil
}

// emitArrayKeys implements arr.keys(): returns a materialized number[] of
// indices [0, len) — the same "return a materialized array, not a lazy
// iterator" convention Map.keys()/Set.values() already use, since this
// compiler has no general iterator protocol.
