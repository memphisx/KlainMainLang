package llvm

import (
	"KlainMainLang/ast"
	"fmt"
)

func (e *Emitter) emitArrayIndexOf(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 1 || len(args) > 2 {
		return Value{}, fmt.Errorf("%d:%d: indexOf takes 1 or 2 arguments (item[, fromIndex])", pos.Line, pos.Col)
	}
	ptrReg, lenReg, elemTy, err := e.resolveArrayForHOF(mem.Object, pos)
	if err != nil {
		return Value{}, err
	}
	if err := e.rejectNestedArrayElem(elemTy, "indexOf", pos); err != nil {
		return Value{}, err
	}
	needleVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	needleVal = e.coerce(needleVal, elemTy)

	resultAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", resultAlloca))
	e.emitInstr(fmt.Sprintf("store i64 -1, ptr %s, align 8", resultAlloca))
	idxAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxAlloca))
	// Optional fromIndex: start the scan there (a negative index counts from the
	// end, clamped to 0); default 0.
	startVal := "0"
	if len(args) == 2 {
		fromVal, ferr := e.emitExpr(args[1])
		if ferr != nil {
			return Value{}, ferr
		}
		fromIdx, ferr := e.coerceChecked(fromVal, TypeI64, args[1].GetPos(), "indexOf fromIndex")
		if ferr != nil {
			return Value{}, ferr
		}
		from := fromIdx.Ref
		neg := e.freshReg()
		plusLen := e.freshReg()
		adj := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 0", neg, from))
		e.emitInstr(fmt.Sprintf("%s = add i64 %s, %s", plusLen, from, lenReg))
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", adj, neg, plusLen, from))
		stillNeg := e.freshReg()
		clamped := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 0", stillNeg, adj))
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 0, i64 %s", clamped, stillNeg, adj))
		startVal = clamped
	}
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", startVal, idxAlloca))

	condL := e.freshLabel("idxof.cond")
	bodyL := e.freshLabel("idxof.body")
	matchL := e.freshLabel("idxof.match")
	incL := e.freshLabel("idxof.inc")
	doneL := e.freshLabel("idxof.done")

	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	idxVal := e.freshReg()
	done := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idxVal, idxAlloca))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", done, idxVal, lenReg))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", done, doneL, bodyL))

	e.emitLabel(bodyL)
	gep := e.freshReg()
	elem := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", gep, elemTy.IR, ptrReg, idxVal))
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", elem, elemTy.IR, gep, elemTy.Align()))
	eqReg := e.emitElemEq(elemTy, elem, needleVal.Ref)
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", eqReg, matchL, incL))

	e.emitLabel(matchL)
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", idxVal, resultAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))

	e.emitLabel(incL)
	idxNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", idxNext, idxVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", idxNext, idxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(doneL)
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", result, resultAlloca))
	return e.countToNumber(Value{Ref: result, Ty: TypeI64}), nil
}

// emitArrayLastIndexOf implements arr.lastIndexOf(val): the index of the LAST
// occurrence of val (scanning from the end), or -1. Mirror of emitArrayIndexOf
// with a descending scan; returns on the first match found from the back.
func (e *Emitter) emitArrayLastIndexOf(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: lastIndexOf takes exactly 1 argument", pos.Line, pos.Col)
	}
	ptrReg, lenReg, elemTy, err := e.resolveArrayForHOF(mem.Object, pos)
	if err != nil {
		return Value{}, err
	}
	if err := e.rejectNestedArrayElem(elemTy, "lastIndexOf", pos); err != nil {
		return Value{}, err
	}
	needleVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	needleVal = e.coerce(needleVal, elemTy)

	resultAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", resultAlloca))
	e.emitInstr(fmt.Sprintf("store i64 -1, ptr %s, align 8", resultAlloca))
	idxAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxAlloca))
	// start at len-1
	startIdx := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, 1", startIdx, lenReg))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", startIdx, idxAlloca))

	condL := e.freshLabel("lidxof.cond")
	bodyL := e.freshLabel("lidxof.body")
	matchL := e.freshLabel("lidxof.match")
	decL := e.freshLabel("lidxof.dec")
	doneL := e.freshLabel("lidxof.done")

	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	idxVal := e.freshReg()
	done := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idxVal, idxAlloca))
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 0", done, idxVal))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", done, doneL, bodyL))

	e.emitLabel(bodyL)
	gep := e.freshReg()
	elem := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", gep, elemTy.IR, ptrReg, idxVal))
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", elem, elemTy.IR, gep, elemTy.Align()))
	eqReg := e.emitElemEq(elemTy, elem, needleVal.Ref)
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", eqReg, matchL, decL))

	e.emitLabel(matchL)
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", idxVal, resultAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))

	e.emitLabel(decL)
	idxPrev := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, 1", idxPrev, idxVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", idxPrev, idxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(doneL)
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", result, resultAlloca))
	return e.countToNumber(Value{Ref: result, Ty: TypeI64}), nil
}

// emitArrayIncludes implements arr.includes(val): returns true if val is present.
func (e *Emitter) emitArrayIncludes(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: includes takes exactly 1 argument", pos.Line, pos.Col)
	}
	ptrReg, lenReg, elemTy, err := e.resolveArrayForHOF(mem.Object, pos)
	if err != nil {
		return Value{}, err
	}
	if err := e.rejectNestedArrayElem(elemTy, "includes", pos); err != nil {
		return Value{}, err
	}
	needleVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	needleVal = e.coerce(needleVal, elemTy)

	foundAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i1, align 1", foundAlloca))
	e.emitInstr(fmt.Sprintf("store i1 false, ptr %s, align 1", foundAlloca))
	idxAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idxAlloca))

	condL := e.freshLabel("inc.cond")
	bodyL := e.freshLabel("inc.body")
	matchL := e.freshLabel("inc.match")
	incL := e.freshLabel("inc.inc")
	doneL := e.freshLabel("inc.done")

	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	idxVal := e.freshReg()
	done := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idxVal, idxAlloca))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", done, idxVal, lenReg))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", done, doneL, bodyL))

	e.emitLabel(bodyL)
	gep := e.freshReg()
	elem := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", gep, elemTy.IR, ptrReg, idxVal))
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", elem, elemTy.IR, gep, elemTy.Align()))
	eqReg := e.emitElemEq(elemTy, elem, needleVal.Ref)
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", eqReg, matchL, incL))

	e.emitLabel(matchL)
	e.emitInstr(fmt.Sprintf("store i1 true, ptr %s, align 1", foundAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))

	e.emitLabel(incL)
	idxNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", idxNext, idxVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", idxNext, idxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(doneL)
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i1, ptr %s, align 1", result, foundAlloca))
	return Value{Ref: result, Ty: TypeBool}, nil
}

// emitArrayFindIndex implements arr.findIndex(pred): returns the index of the
// first element for which pred returns true, or -1 if none do.
func (e *Emitter) emitArrayFindIndex(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: findIndex takes exactly 1 argument", pos.Line, pos.Col)
	}
	ptrReg, lenReg, elemTy, err := e.resolveArrayForHOF(mem.Object, pos)
	if err != nil {
		return Value{}, err
	}
	cb, err := e.resolveCallbackWithHints(args[0], []Type{elemTy, TypeI64})
	if err != nil {
		return Value{}, err
	}

	resultAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", resultAlloca))
	e.emitInstr(fmt.Sprintf("store i64 -1, ptr %s, align 8", resultAlloca))
	idxAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idxAlloca))

	condL := e.freshLabel("fidx.cond")
	bodyL := e.freshLabel("fidx.body")
	matchL := e.freshLabel("fidx.match")
	incL := e.freshLabel("fidx.inc")
	doneL := e.freshLabel("fidx.done")

	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	idxVal := e.freshReg()
	done := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idxVal, idxAlloca))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", done, idxVal, lenReg))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", done, doneL, bodyL))

	e.emitLabel(bodyL)
	gep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", gep, elemTy.IR, ptrReg, idxVal))
	elemVal := e.loadArrayElem(gep, elemTy)
	cbArgs := []Value{elemVal}
	if cb.arity() >= 2 {
		cbArgs = append(cbArgs, Value{Ref: idxVal, Ty: TypeI64})
	}
	predVal, err := e.emitCBCall(cb, cbArgs)
	if err != nil {
		return Value{}, err
	}
	boolVal := e.emitToBool(predVal)
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", boolVal.Ref, matchL, incL))

	e.emitLabel(matchL)
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", idxVal, resultAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))

	e.emitLabel(incL)
	idxNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", idxNext, idxVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", idxNext, idxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(doneL)
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", result, resultAlloca))
	return e.countToNumber(Value{Ref: result, Ty: TypeI64}), nil
}

// emitArrayFindLast implements arr.findLast(pred): same as .find(), but
// scans from the last element backward — a genuine reverse loop, not a
// forward scan keeping the last match, since real JS invokes pred in reverse
// order too (observable if pred has side effects, e.g. a console.log).
func (e *Emitter) emitArrayFindLast(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: findLast takes exactly 1 argument", pos.Line, pos.Col)
	}
	ptrReg, lenReg, elemTy, err := e.resolveArrayForHOF(mem.Object, pos)
	if err != nil {
		return Value{}, err
	}
	cb, err := e.resolveCallbackWithHints(args[0], []Type{elemTy, TypeI64})
	if err != nil {
		return Value{}, err
	}

	foundAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", foundAlloca, elemTy.IR, elemTy.Align()))
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", elemTy.IR, missRef(elemTy), foundAlloca, elemTy.Align()))
	flagAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i1, align 1", flagAlloca))
	e.emitInstr(fmt.Sprintf("store i1 false, ptr %s, align 1", flagAlloca))

	idxAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxAlloca))
	startIdx := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, 1", startIdx, lenReg))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", startIdx, idxAlloca))

	condL := e.freshLabel("findlast.cond")
	bodyL := e.freshLabel("findlast.body")
	matchL := e.freshLabel("findlast.match")
	decL := e.freshLabel("findlast.dec")
	doneL := e.freshLabel("findlast.done")

	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	idxVal := e.freshReg()
	loopDone := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idxVal, idxAlloca))
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 0", loopDone, idxVal))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", loopDone, doneL, bodyL))

	e.emitLabel(bodyL)
	gep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", gep, elemTy.IR, ptrReg, idxVal))
	elemVal := e.loadArrayElem(gep, elemTy)
	cbArgs := []Value{elemVal}
	if cb.arity() >= 2 {
		cbArgs = append(cbArgs, Value{Ref: idxVal, Ty: TypeI64})
	}
	predVal, err := e.emitCBCall(cb, cbArgs)
	if err != nil {
		return Value{}, err
	}
	boolVal := e.emitToBool(predVal)
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", boolVal.Ref, matchL, decL))

	e.emitLabel(matchL)
	e.storeArrayElem(foundAlloca, elemTy, elemVal)
	e.emitInstr(fmt.Sprintf("store i1 true, ptr %s, align 1", flagAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))

	e.emitLabel(decL)
	idxNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, 1", idxNext, idxVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", idxNext, idxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(doneL)
	if elemTy.IsArray {
		return e.loadArrayElemMaybeNull(foundAlloca, elemTy), nil
	}
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", result, elemTy.IR, foundAlloca, elemTy.Align()))
	found := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i1, ptr %s, align 1", found, flagAlloca))
	return e.wrapUndefinedable(Value{Ref: result, Ty: elemTy}, found), nil
}

// emitArrayFindLastIndex implements arr.findLastIndex(pred): same reverse
// scan as findLast, returning the matched index (or -1) instead of the value.
func (e *Emitter) emitArrayFindLastIndex(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: findLastIndex takes exactly 1 argument", pos.Line, pos.Col)
	}
	ptrReg, lenReg, elemTy, err := e.resolveArrayForHOF(mem.Object, pos)
	if err != nil {
		return Value{}, err
	}
	cb, err := e.resolveCallbackWithHints(args[0], []Type{elemTy, TypeI64})
	if err != nil {
		return Value{}, err
	}

	resultAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", resultAlloca))
	e.emitInstr(fmt.Sprintf("store i64 -1, ptr %s, align 8", resultAlloca))
	idxAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxAlloca))
	startIdx := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, 1", startIdx, lenReg))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", startIdx, idxAlloca))

	condL := e.freshLabel("fidxlast.cond")
	bodyL := e.freshLabel("fidxlast.body")
	matchL := e.freshLabel("fidxlast.match")
	decL := e.freshLabel("fidxlast.dec")
	doneL := e.freshLabel("fidxlast.done")

	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	idxVal := e.freshReg()
	loopDone := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idxVal, idxAlloca))
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 0", loopDone, idxVal))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", loopDone, doneL, bodyL))

	e.emitLabel(bodyL)
	gep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", gep, elemTy.IR, ptrReg, idxVal))
	elemVal := e.loadArrayElem(gep, elemTy)
	cbArgs := []Value{elemVal}
	if cb.arity() >= 2 {
		cbArgs = append(cbArgs, Value{Ref: idxVal, Ty: TypeI64})
	}
	predVal, err := e.emitCBCall(cb, cbArgs)
	if err != nil {
		return Value{}, err
	}
	boolVal := e.emitToBool(predVal)
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", boolVal.Ref, matchL, decL))

	e.emitLabel(matchL)
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", idxVal, resultAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))

	e.emitLabel(decL)
	idxNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, 1", idxNext, idxVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", idxNext, idxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(doneL)
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", result, resultAlloca))
	return e.countToNumber(Value{Ref: result, Ty: TypeI64}), nil
}

// emitArrayConcat implements arr.concat(other): returns a new array containing
// all elements of arr followed by all elements of other.
