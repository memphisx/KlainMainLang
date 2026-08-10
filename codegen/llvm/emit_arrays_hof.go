package llvm

import (
	"KlainMainLang/ast"
	"fmt"
)

func (e *Emitter) emitArrayMap(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: map takes exactly 1 argument", pos.Line, pos.Col)
	}
	ptrReg, lenReg, elemTy, err := e.resolveArrayForHOF(mem.Object, pos)
	if err != nil {
		return Value{}, err
	}
	cb, err := e.resolveCallbackWithHints(args[0], []Type{elemTy, TypeI64})
	if err != nil {
		return Value{}, err
	}
	retElemTy := cb.retType()
	if retElemTy.IR == "void" || retElemTy.IR == "" {
		return Value{}, fmt.Errorf("%d:%d: map callback must return a value", pos.Line, pos.Col)
	}
	// A TypedArray's .map() always returns the same TypedArray kind (real
	// JS semantics: Uint8Array.prototype.map() always returns a new
	// Uint8Array), coercing each result into it — not whatever type the
	// callback expression itself would otherwise naturally produce. An
	// unannotated arrow callback's own inferred return type can silently
	// differ from the receiver's narrower element width (e.g. defaulting
	// to i64 while the receiver is Uint8Array's i8), which would corrupt
	// every index but the first once the result is read back at the
	// receiver's own (narrower) element stride — found by direct testing,
	// not by inspection, while verifying "everything already reused for
	// free" during TDD-00018's implementation.
	isTypedArray := e.inferExprType(mem.Object).IsTypedArray
	if isTypedArray {
		retElemTy = elemTy
	}

	e.ensureMalloc()
	outBytes := e.freshReg()
	outPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %d", outBytes, lenReg, retElemTy.Align()))
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", outPtr, outBytes))

	idxAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idxAlloca))

	condL := e.freshLabel("map.cond")
	bodyL := e.freshLabel("map.body")
	doneL := e.freshLabel("map.done")

	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	idxVal := e.freshReg()
	done := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idxVal, idxAlloca))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", done, idxVal, lenReg))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", done, doneL, bodyL))

	e.emitLabel(bodyL)
	inGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", inGep, elemTy.IR, ptrReg, idxVal))
	inVal := e.loadArrayElem(inGep, elemTy)

	cbArgs := []Value{inVal}
	if cb.arity() >= 2 {
		cbArgs = append(cbArgs, Value{Ref: idxVal, Ty: TypeI64})
	}
	resultVal, err := e.emitCBCall(cb, cbArgs)
	if err != nil {
		return Value{}, err
	}
	if isTypedArray {
		resultVal = e.coerce(resultVal, retElemTy)
	}

	outGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", outGep, retElemTy.IR, outPtr, idxVal))
	// A callback that returns an array per element (the shape .flatMap()
	// builds .map() on top of) needs retElemTy's own slot boxed exactly
	// like any other nested-array-element write — see storeArrayElem's own
	// doc comment / TDD-00029. Input elemTy can independently be array-typed
	// too now (loadArrayElem above already transparently unboxes it) —
	// ADR-00151/TDD-00059.
	e.storeArrayElem(outGep, retElemTy, resultVal)

	idxNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", idxNext, idxVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", idxNext, idxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(doneL)
	r0 := e.freshReg()
	r1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", r0, outPtr))
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %s, 1", r1, r0, lenReg))
	resultTy := ArrayOf(retElemTy)
	resultTy.IsTypedArray = isTypedArray
	return Value{Ref: r1, Ty: resultTy}, nil
}

// emitArrayForEach implements arr.forEach(fn): calls fn(elem, index?) for each element, no return value.
func (e *Emitter) emitArrayForEach(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: forEach takes exactly 1 argument", pos.Line, pos.Col)
	}
	ptrReg, lenReg, elemTy, err := e.resolveArrayForHOF(mem.Object, pos)
	if err != nil {
		return Value{}, err
	}
	cb, err := e.resolveCallbackWithHints(args[0], []Type{elemTy, TypeI64})
	if err != nil {
		return Value{}, err
	}

	idxAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idxAlloca))

	condL := e.freshLabel("foreach.cond")
	bodyL := e.freshLabel("foreach.body")
	doneL := e.freshLabel("foreach.done")

	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	idxVal := e.freshReg()
	done := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idxVal, idxAlloca))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", done, idxVal, lenReg))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", done, doneL, bodyL))

	e.emitLabel(bodyL)
	inGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", inGep, elemTy.IR, ptrReg, idxVal))
	inVal := e.loadArrayElem(inGep, elemTy)

	cbArgs := []Value{inVal}
	if cb.arity() >= 2 {
		cbArgs = append(cbArgs, Value{Ref: idxVal, Ty: TypeI64})
	}
	if _, err := e.emitCBCall(cb, cbArgs); err != nil {
		return Value{}, err
	}

	idxNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", idxNext, idxVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", idxNext, idxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(doneL)
	return Value{Ty: TypeVoid}, nil
}

// emitArrayFilter implements arr.filter(pred): returns a new array containing
// only elements for which pred(elem[, index]) returns true.
func (e *Emitter) emitArrayFilter(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: filter takes exactly 1 argument", pos.Line, pos.Col)
	}
	ptrReg, lenReg, elemTy, err := e.resolveArrayForHOF(mem.Object, pos)
	if err != nil {
		return Value{}, err
	}
	cb, err := e.resolveCallbackWithHints(args[0], []Type{elemTy, TypeI64})
	if err != nil {
		return Value{}, err
	}

	e.ensureMalloc()
	outBytes := e.freshReg()
	outPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %d", outBytes, lenReg, elemTy.Align()))
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", outPtr, outBytes))

	idxAlloca := e.freshReg()
	cntAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxAlloca))
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", cntAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idxAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", cntAlloca))

	condL := e.freshLabel("filt.cond")
	bodyL := e.freshLabel("filt.body")
	storeL := e.freshLabel("filt.store")
	incL := e.freshLabel("filt.inc")
	doneL := e.freshLabel("filt.done")

	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	idxVal := e.freshReg()
	done := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idxVal, idxAlloca))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", done, idxVal, lenReg))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", done, doneL, bodyL))

	e.emitLabel(bodyL)
	inGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", inGep, elemTy.IR, ptrReg, idxVal))
	inVal := e.loadArrayElem(inGep, elemTy)

	cbArgs := []Value{inVal}
	if cb.arity() >= 2 {
		cbArgs = append(cbArgs, Value{Ref: idxVal, Ty: TypeI64})
	}
	predVal, err := e.emitCBCall(cb, cbArgs)
	if err != nil {
		return Value{}, err
	}
	boolVal := e.emitToBool(predVal)
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", boolVal.Ref, storeL, incL))

	e.emitLabel(storeL)
	cntVal := e.freshReg()
	outGep := e.freshReg()
	cntNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", cntVal, cntAlloca))
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", outGep, elemTy.IR, outPtr, cntVal))
	e.storeArrayElem(outGep, elemTy, inVal)
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", cntNext, cntVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", cntNext, cntAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", incL))

	e.emitLabel(incL)
	idxNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", idxNext, idxVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", idxNext, idxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(doneL)
	finalCnt := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", finalCnt, cntAlloca))
	r0 := e.freshReg()
	r1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", r0, outPtr))
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %s, 1", r1, r0, finalCnt))
	return Value{Ref: r1, Ty: ArrayOf(elemTy)}, nil
}

// emitArrayReduce implements arr.reduce(cb, initial): folds elements left-to-right.
// The callback signature is (acc, elem) => newAcc. Returns the final accumulator.
func (e *Emitter) emitArrayReduce(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 2 {
		return Value{}, fmt.Errorf("%d:%d: reduce takes exactly 2 arguments (callback, initial)", pos.Line, pos.Col)
	}
	ptrReg, lenReg, elemTy, err := e.resolveArrayForHOF(mem.Object, pos)
	if err != nil {
		return Value{}, err
	}
	// accTy's hint comes from a pure static inference of the initial-value
	// expression (not evaluating it early), so the callback's accumulator
	// parameter gets the right type while still evaluating args in their
	// original left-to-right order (callback resolved first, matching
	// real JS's argument evaluation order, exactly as before).
	accTyHint := e.inferExprType(args[1])
	cb, err := e.resolveCallbackWithHints(args[0], []Type{accTyHint, elemTy})
	if err != nil {
		return Value{}, err
	}
	initVal, err := e.emitExpr(args[1])
	if err != nil {
		return Value{}, err
	}
	accTy := initVal.Ty

	accAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", accAlloca, accTy.IR, accTy.Align()))
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", accTy.IR, initVal.Ref, accAlloca, accTy.Align()))

	idxAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idxAlloca))

	condL := e.freshLabel("red.cond")
	bodyL := e.freshLabel("red.body")
	doneL := e.freshLabel("red.done")

	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	idxVal := e.freshReg()
	done := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idxVal, idxAlloca))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", done, idxVal, lenReg))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", done, doneL, bodyL))

	e.emitLabel(bodyL)
	accCur := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", accCur, accTy.IR, accAlloca, accTy.Align()))
	inGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", inGep, elemTy.IR, ptrReg, idxVal))
	inVal := e.loadArrayElem(inGep, elemTy)

	newAcc, err := e.emitCBCall(cb, []Value{{Ref: accCur, Ty: accTy}, inVal})
	if err != nil {
		return Value{}, err
	}
	newAccCoerced := e.coerce(newAcc, accTy)
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", accTy.IR, newAccCoerced.Ref, accAlloca, accTy.Align()))

	idxNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", idxNext, idxVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", idxNext, idxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(doneL)
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", result, accTy.IR, accAlloca, accTy.Align()))
	return Value{Ref: result, Ty: accTy}, nil
}

// emitArrayFind implements arr.find(pred): returns the first element satisfying
// pred, or the zero value of the element type if none is found.
func (e *Emitter) emitArrayFind(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: find takes exactly 1 argument", pos.Line, pos.Col)
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
	// Zero-initialise: 0 for numbers, null for pointers.
	zeroVal := "0"
	if elemTy.IR == "ptr" {
		zeroVal = "null"
	}
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", elemTy.IR, zeroVal, foundAlloca, elemTy.Align()))

	idxAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idxAlloca))

	condL := e.freshLabel("find.cond")
	bodyL := e.freshLabel("find.body")
	matchL := e.freshLabel("find.match")
	incL := e.freshLabel("find.inc")
	doneL := e.freshLabel("find.done")

	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	idxVal := e.freshReg()
	done := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idxVal, idxAlloca))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", done, idxVal, lenReg))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", done, doneL, bodyL))

	e.emitLabel(bodyL)
	inGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", inGep, elemTy.IR, ptrReg, idxVal))
	inVal := e.loadArrayElem(inGep, elemTy)
	predVal, err := e.emitCBCall(cb, []Value{inVal})
	if err != nil {
		return Value{}, err
	}
	boolVal := e.emitToBool(predVal)
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", boolVal.Ref, matchL, incL))

	e.emitLabel(matchL)
	e.storeArrayElem(foundAlloca, elemTy, inVal)
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))

	e.emitLabel(incL)
	idxNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", idxNext, idxVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", idxNext, idxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(doneL)
	if elemTy.IsArray {
		return e.loadArrayElemMaybeNull(foundAlloca, elemTy), nil
	}
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", result, elemTy.IR, foundAlloca, elemTy.Align()))
	return Value{Ref: result, Ty: elemTy}, nil
}

// emitArraySome implements arr.some(pred): returns true if any element satisfies pred.
func (e *Emitter) emitArraySome(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: some takes exactly 1 argument", pos.Line, pos.Col)
	}
	ptrReg, lenReg, elemTy, err := e.resolveArrayForHOF(mem.Object, pos)
	if err != nil {
		return Value{}, err
	}
	cb, err := e.resolveCallbackWithHints(args[0], []Type{elemTy, TypeI64})
	if err != nil {
		return Value{}, err
	}

	resAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i1, align 1", resAlloca))
	e.emitInstr(fmt.Sprintf("store i1 0, ptr %s, align 1", resAlloca))

	idxAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idxAlloca))

	condL := e.freshLabel("some.cond")
	bodyL := e.freshLabel("some.body")
	trueL := e.freshLabel("some.true")
	incL := e.freshLabel("some.inc")
	doneL := e.freshLabel("some.done")

	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	idxVal := e.freshReg()
	done := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idxVal, idxAlloca))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", done, idxVal, lenReg))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", done, doneL, bodyL))

	e.emitLabel(bodyL)
	inGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", inGep, elemTy.IR, ptrReg, idxVal))
	inVal := e.loadArrayElem(inGep, elemTy)
	predVal, err := e.emitCBCall(cb, []Value{inVal})
	if err != nil {
		return Value{}, err
	}
	boolVal := e.emitToBool(predVal)
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", boolVal.Ref, trueL, incL))

	e.emitLabel(trueL)
	e.emitInstr(fmt.Sprintf("store i1 1, ptr %s, align 1", resAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))

	e.emitLabel(incL)
	idxNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", idxNext, idxVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", idxNext, idxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(doneL)
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i1, ptr %s, align 1", result, resAlloca))
	return Value{Ref: result, Ty: TypeBool}, nil
}

// emitArrayEvery implements arr.every(pred): returns true if all elements satisfy pred.
func (e *Emitter) emitArrayEvery(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: every takes exactly 1 argument", pos.Line, pos.Col)
	}
	ptrReg, lenReg, elemTy, err := e.resolveArrayForHOF(mem.Object, pos)
	if err != nil {
		return Value{}, err
	}
	cb, err := e.resolveCallbackWithHints(args[0], []Type{elemTy, TypeI64})
	if err != nil {
		return Value{}, err
	}

	resAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i1, align 1", resAlloca))
	e.emitInstr(fmt.Sprintf("store i1 1, ptr %s, align 1", resAlloca)) // assume true

	idxAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idxAlloca))

	condL := e.freshLabel("evry.cond")
	bodyL := e.freshLabel("evry.body")
	falseL := e.freshLabel("evry.false")
	incL := e.freshLabel("evry.inc")
	doneL := e.freshLabel("evry.done")

	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	idxVal := e.freshReg()
	done := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idxVal, idxAlloca))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", done, idxVal, lenReg))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", done, doneL, bodyL))

	e.emitLabel(bodyL)
	inGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", inGep, elemTy.IR, ptrReg, idxVal))
	inVal := e.loadArrayElem(inGep, elemTy)
	predVal, err := e.emitCBCall(cb, []Value{inVal})
	if err != nil {
		return Value{}, err
	}
	boolVal := e.emitToBool(predVal)
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", boolVal.Ref, incL, falseL))

	e.emitLabel(falseL)
	e.emitInstr(fmt.Sprintf("store i1 0, ptr %s, align 1", resAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))

	e.emitLabel(incL)
	idxNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", idxNext, idxVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", idxNext, idxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(doneL)
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i1, ptr %s, align 1", result, resAlloca))
	return Value{Ref: result, Ty: TypeBool}, nil
}

// emitArrayJoin implements arr.join(sep?): concatenates elements into a string,
// separated by sep (default ","). Non-string elements are converted via sprintf.
