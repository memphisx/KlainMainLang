package llvm

import (
	"KlainMainLang/ast"
	"fmt"
)

func (e *Emitter) emitArrayMap(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, internalAt(pos, "map arity %d", len(args))
	}
	hdr, ptrReg, lenReg, elemTy, err := e.resolveArrayHOF(mem.Object, pos)
	if err != nil {
		return Value{}, err
	}
	self := e.hofSelf(mem.Object, hdr)
	live := e.hofLiveHeader(mem.Object, hdr)
	hdr = e.hofHolesHeader(mem.Object, hdr)
	cb, err := e.resolveCallbackWithHints(args[0], []Type{e.walkElemHint(live, elemTy), TypeF64, e.hofSelfHint(self, elemTy)})
	if err != nil {
		return Value{}, err
	}
	retElemTy := cb.retType()
	// A callback returning nothing maps every element to undefined.
	voidRet := retElemTy.IR == "void" || retElemTy.IR == ""
	if voidRet {
		retElemTy = TypeAny
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
	recvTy := e.typeOfExpr(mem.Object)
	isTypedArray := recvTy.IsTypedArray()
	if isTypedArray {
		retElemTy = elemTy
	}
	// A callback returning `T | undefined` (`k => m.get(k)`) fills a boxed-element
	// array: the { i1, T } optional has no element storage, and storing it into a
	// bare-T slot was invalid IR.
	boxedRet := isNullableScalar(retElemTy)
	retElemTy = arrayElemType(retElemTy)

	outBytes := e.freshReg()
	outPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %d", outBytes, lenReg, retElemTy.Align()))
	e.heapMallocInto(outPtr, elemAllocKind(retElemTy), outBytes)
	if hdr != "" {
		// A hole is skipped: its slot of the result stays a zeroed hole.
		e.emitInstr(fmt.Sprintf("call "+irPtr+" @memset("+irPtr+" %s, i32 0, i64 %s)", outPtr, outBytes))
	}

	idxAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, "+irPtr+" %s, align 8", idxAlloca))

	condL := e.freshLabel("map.cond")
	bodyL := e.freshLabel("map.body")
	doneL := e.freshLabel("map.done")

	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	idxVal := e.freshReg()
	done := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", idxVal, idxAlloca))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", done, idxVal, lenReg))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", done, doneL, bodyL))

	e.emitLabel(bodyL)
	e.emitHoleSkip(hdr, idxVal, idxAlloca, 1, condL)
	inGep := e.hofLiveGep(live, ptrReg, idxVal, elemTy, func() { e.hofSkip(idxVal, idxAlloca, 1, condL) })
	inVal := e.loadArrayElem(inGep, elemTy)

	cbArgs := e.hofCBArgs(cb, []Value{e.walkElemArg(live, idxVal, elemTy, inVal)}, idxVal, live, self, ptrReg, lenReg, elemTy)
	resultVal, err := e.emitCBCall(cb, cbArgs)
	if err != nil {
		return Value{}, err
	}
	if voidRet {
		resultVal = Value{Ref: fmt.Sprint(nbUndefined), Ty: TypeAny}
	}
	if isTypedArray {
		// A Uint8ClampedArray's .map() clamp-stores each result (TDD-00101).
		if recvTy.Clamped {
			resultVal, err = e.toTypedArrayElem(resultVal, recvTy, pos)
			if err != nil {
				return Value{}, err
			}
		} else {
			resultVal = e.convert(resultVal, retElemTy)
		}
	}
	if boxedRet {
		resultVal = e.convert(resultVal, retElemTy)
	}

	outGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i64 %s", outGep, retElemTy.IR, outPtr, idxVal))
	// A callback that returns an array per element (the shape .flatMap()
	// builds .map() on top of) needs retElemTy's own slot boxed exactly
	// like any other nested-array-element write — see storeArrayElem's own
	// doc comment / TDD-00029. Input elemTy can independently be array-typed
	// too now (loadArrayElem above already transparently unboxes it) —
	// ADR-00151/TDD-00059.
	e.storeArrayElem(outGep, retElemTy, resultVal)

	idxNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", idxNext, idxVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", idxNext, idxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(doneL)
	r1 := e.newArrayHeader(outPtr, lenReg)
	e.emitHolesCopy(r1, "0", hdr, "0", lenReg)
	resultTy := ArrayOf(retElemTy)
	if isTypedArray {
		resultTy = typedArrayResultType(recvTy) // the receiver's kind: a Buffer maps to a Buffer
	}
	return Value{Ref: r1, Ty: resultTy}, nil
}

// emitArrayForEach implements arr.forEach(fn): calls fn(elem, index?, array?)
// for each element, no return value. The 3rd `array` argument (ADR-00573) is
// the source array itself — passed as a fresh header over the same
// backing, so reads through it see live elements (a length-snapshot copy, the
// same HOF-callback array narrowing the reference-semantics caveat documents).
func (e *Emitter) emitArrayForEach(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, internalAt(pos, "forEach arity %d", len(args))
	}
	hdr, ptrReg, lenReg, elemTy, err := e.resolveArrayHOF(mem.Object, pos)
	if err != nil {
		return Value{}, err
	}
	self := e.hofSelf(mem.Object, hdr)
	live := e.hofLiveHeader(mem.Object, hdr)
	hdr = e.hofHolesHeader(mem.Object, hdr)
	cb, err := e.resolveCallbackWithHints(args[0], []Type{e.walkElemHint(live, elemTy), TypeF64, e.hofSelfHint(self, elemTy)})
	if err != nil {
		return Value{}, err
	}

	idxAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, "+irPtr+" %s, align 8", idxAlloca))

	condL := e.freshLabel("foreach.cond")
	bodyL := e.freshLabel("foreach.body")
	doneL := e.freshLabel("foreach.done")

	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	idxVal := e.freshReg()
	done := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", idxVal, idxAlloca))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", done, idxVal, lenReg))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", done, doneL, bodyL))

	e.emitLabel(bodyL)
	e.emitHoleSkip(hdr, idxVal, idxAlloca, 1, condL)
	inGep := e.hofLiveGep(live, ptrReg, idxVal, elemTy, func() { e.hofSkip(idxVal, idxAlloca, 1, condL) })
	inVal := e.loadArrayElem(inGep, elemTy)

	cbArgs := e.hofCBArgs(cb, []Value{e.walkElemArg(live, idxVal, elemTy, inVal)}, idxVal, live, self, ptrReg, lenReg, elemTy)
	if _, err := e.emitCBCall(cb, cbArgs); err != nil {
		return Value{}, err
	}

	idxNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", idxNext, idxVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", idxNext, idxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(doneL)
	return Value{Ty: TypeVoid}, nil
}

// emitArrayFilter implements arr.filter(pred): returns a new array containing
// only elements for which pred(elem[, index]) returns true.
func (e *Emitter) emitArrayFilter(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, internalAt(pos, "filter arity %d", len(args))
	}
	hdr, ptrReg, lenReg, elemTy, err := e.resolveArrayHOF(mem.Object, pos)
	if err != nil {
		return Value{}, err
	}
	self := e.hofSelf(mem.Object, hdr)
	live := e.hofLiveHeader(mem.Object, hdr)
	hdr = e.hofHolesHeader(mem.Object, hdr)
	cb, err := e.resolveCallbackWithHints(args[0], []Type{e.walkElemHint(live, elemTy), TypeF64, e.hofSelfHint(self, elemTy)})
	if err != nil {
		return Value{}, err
	}

	outBytes := e.freshReg()
	outPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %d", outBytes, lenReg, elemTy.Align()))
	e.heapMallocInto(outPtr, elemAllocKind(elemTy), outBytes)

	idxAlloca := e.freshReg()
	cntAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxAlloca))
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", cntAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, "+irPtr+" %s, align 8", idxAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, "+irPtr+" %s, align 8", cntAlloca))

	condL := e.freshLabel("filt.cond")
	bodyL := e.freshLabel("filt.body")
	storeL := e.freshLabel("filt.store")
	incL := e.freshLabel("filt.inc")
	doneL := e.freshLabel("filt.done")

	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	idxVal := e.freshReg()
	done := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", idxVal, idxAlloca))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", done, idxVal, lenReg))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", done, doneL, bodyL))

	e.emitLabel(bodyL)
	e.emitHoleSkip(hdr, idxVal, idxAlloca, 1, condL)
	inGep := e.hofLiveGep(live, ptrReg, idxVal, elemTy, func() { e.hofSkip(idxVal, idxAlloca, 1, condL) })
	inVal := e.loadArrayElem(inGep, elemTy)

	cbArgs := e.hofCBArgs(cb, []Value{e.walkElemArg(live, idxVal, elemTy, inVal)}, idxVal, live, self, ptrReg, lenReg, elemTy)
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
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", cntVal, cntAlloca))
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i64 %s", outGep, elemTy.IR, outPtr, cntVal))
	e.storeArrayElem(outGep, elemTy, inVal)
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", cntNext, cntVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", cntNext, cntAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", incL))

	e.emitLabel(incL)
	idxNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", idxNext, idxVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", idxNext, idxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(doneL)
	finalCnt := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", finalCnt, cntAlloca))
	r1 := e.newArrayHeader(outPtr, finalCnt)
	if recvTy := e.typeOfExpr(mem.Object); recvTy.IsTypedArray() {
		return Value{Ref: r1, Ty: typedArrayResultType(recvTy)}, nil // a typed array filters to its own kind
	}
	return Value{Ref: r1, Ty: ArrayOf(elemTy)}, nil
}

// emitArrayReduce implements arr.reduce(cb[, initial]): folds elements
// left-to-right. The callback signature is (acc, elem) => newAcc. Returns
// the final accumulator.
//
// The initial value is optional (ADR-00163), matching real JS: when
// omitted, the array's own first element seeds the accumulator and the
// fold starts from index 1 instead of 0 — an empty array with no initial
// value throws (real JS: `TypeError: Reduce of empty array with no
// initial value`; this compiler's own internal-throw convention for a
// runtime-detected error like this is always a plain Error, not a
// TypeError, matching e.g. the array-index-out-of-bounds throw elsewhere).
// emitArrayReduce implements both arr.reduce (fromRight=false) and
// arr.reduceRight (fromRight=true). The two differ only in traversal order:
// reduceRight seeds from the last element and walks the index down to 0, so the
// index setup, loop-termination test, and step are the sole branch points; the
// accumulator/callback/coercion machinery is shared verbatim. The callback
// argument order (accumulator, element, ...) is identical for both — only the
// visitation order reverses, matching real JS.
func (e *Emitter) emitArrayReduce(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos, fromRight bool) (Value, error) {
	verb := "reduce"
	if fromRight {
		verb = "reduceRight"
	}
	if len(args) != 1 && len(args) != 2 {
		return Value{}, internalAt(pos, "%s arity %d", verb, len(args))
	}
	hasInitial := len(args) == 2
	hdr, ptrReg, lenReg, elemTy, err := e.resolveArrayHOF(mem.Object, pos)
	if err != nil {
		return Value{}, err
	}
	self := e.hofSelf(mem.Object, hdr)
	live := e.hofLiveHeader(mem.Object, hdr)
	hdr = e.hofHolesHeader(mem.Object, hdr)
	// accTy's hint comes from a pure static inference of the initial-value
	// expression (not evaluating it early), so the callback's accumulator
	// parameter gets the right type while still evaluating args in their
	// original left-to-right order (callback resolved first, matching
	// real JS's argument evaluation order, exactly as before). With no
	// initial value, the accumulator's type is just the element type — it
	// starts as (and only ever holds) an actual array element.
	accTyHint := elemTy
	if hasInitial {
		accTyHint = e.reduceSeedType(args[1])
	}
	cb, err := e.resolveCallbackWithHints(args[0], []Type{accTyHint, e.walkElemHint(live, elemTy), TypeF64, e.hofSelfHint(self, elemTy)})
	if err != nil {
		return Value{}, err
	}
	// A callback that can fall off the end returns `T | undefined`
	// (ADR-01065; the checker takes reduce's generic overload for it), and
	// the accumulator then *is* undefined on the next step — `[1,2,3].reduce((a, x) => { if (x > 1) return a + x }, 0)` is NaN
	// in Node. Widen the accumulator to the same nullable scalar and re-resolve
	// the callback with that parameter hint, so `a + x` inside it sees a
	// presence-aware operand (nullableScalarOperand) rather than a payload zero.
	accWidened := false
	if rt := cb.retType(); hasInitial && rt.Nullable() && rt.IsUndefined() && reduceAccWidenable(accTyHint) {
		accTyHint = undefinedableElem(accTyHint)
		if cb, err = e.resolveCallbackWithHints(args[0], []Type{accTyHint, e.walkElemHint(live, elemTy), TypeF64, e.hofSelfHint(self, elemTy)}); err != nil {
			return Value{}, err
		}
		accWidened = true
	}
	// A callback with no result (JavaScript's): each step's accumulator is
	// undefined, held as a box from the initial value on.
	voidCB := false
	widenSeed := false
	if rt := cb.retType(); rt.IR == "void" || rt.IR == "" {
		accTyHint = TypeAny
		if cb, err = e.resolveCallbackWithHints(args[0], []Type{TypeAny, e.walkElemHint(live, elemTy), TypeF64, e.hofSelfHint(self, elemTy)}); err != nil {
			return Value{}, err
		}
		accWidened, voidCB = true, true
		widenSeed = !hasInitial // the first element, boxed
	}

	// JavaScript's accumulator is whatever the last step returned: a callback
	// whose result the seed's type cannot hold (a string from a number seed,
	// a possibly-undefined result, a box of either kind) widens it to `any`.
	if rt := cb.retType(); e.compatJS() && !accWidened && rt.IR != "void" && rt.IR != "" {
		seedTy := elemTy
		if hasInitial {
			seedTy = e.typeOfExpr(args[1])
			if lit, ok := args[1].(*ast.ArrayLiteral); ok && len(lit.Elements) == 0 {
				seedTy = accTyHint
			}
		}
		if !convertiblePure(rt, seedTy) || isNullableScalar(rt) && !isNullableScalar(seedTy) || rt.IsDynamic() && !seedTy.IsDynamic() {
			accTyHint = TypeAny
			if cb, err = e.resolveCallbackWithHints(args[0], []Type{TypeAny, e.walkElemHint(live, elemTy), TypeF64, e.hofSelfHint(self, elemTy)}); err != nil {
				return Value{}, err
			}
			accWidened = true
			if !hasInitial {
				widenSeed = true
			}
		}
	}
	// A reduce callback must return the accumulator value; a void callback would
	// leave nothing to store back into the accumulator (invalid IR otherwise).
	if rt := cb.retType(); !voidCB && (rt.IR == "void" || rt.IR == "") {
		return Value{}, internalAt(pos, "%s callback without a result past its widening", verb)
	}

	var accTy Type
	accAlloca := e.freshReg()
	idxAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxAlloca))

	// lastIdx = len - 1, reused by the fromRight seed/start-index math.
	lastIdx := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, 1", lastIdx, lenReg))

	if accWidened && hasInitial {
		initVal, err := e.emitExpr(args[1])
		if err != nil {
			return Value{}, err
		}
		accTy = accTyHint
		e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", accAlloca, accSlotIR(accTy), storageAlign(accTy)))
		seed := e.convert(initVal, accTy)
		e.emitInstr(fmt.Sprintf("store %s %s, "+irPtr+" %s, align %d", accSlotIR(accTy), seed.Ref, accAlloca, storageAlign(accTy)))
		if fromRight {
			e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", lastIdx, idxAlloca))
		} else {
			e.emitInstr(fmt.Sprintf("store i64 0, "+irPtr+" %s, align 8", idxAlloca))
		}
	} else if hasInitial {
		initVal, err := e.emitExpr(args[1])
		if err != nil {
			return Value{}, err
		}
		accTy = initVal.Ty
		if lit, ok := args[1].(*ast.ArrayLiteral); ok && len(lit.Elements) == 0 && e.compatJS() {
			accTy = accTyHint // reduceSeedType: what the callback puts in it
			initVal = e.convert(initVal, accTy)
		}
		e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", accAlloca, accSlotIR(accTy), accTy.Align()))
		e.emitInstr(fmt.Sprintf("store %s %s, "+irPtr+" %s, align %d", accSlotIR(accTy), initVal.Ref, accAlloca, accTy.Align()))
		// reduce starts at the first index (0); reduceRight at the last (len-1),
		// which is -1 for an empty array so the descending loop exits at once.
		if fromRight {
			e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", lastIdx, idxAlloca))
		} else {
			e.emitInstr(fmt.Sprintf("store i64 0, "+irPtr+" %s, align 8", idxAlloca))
		}
	} else {
		accTy = elemTy
		if widenSeed {
			accTy = TypeAny
		}
		e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", accAlloca, accSlotIR(accTy), accTy.Align()))

		// The seed is the first element that is no hole (the last, reduceRight).
		seedIdx := "0"
		if fromRight {
			seedIdx = lastIdx
		}
		emptyReg := e.freshReg()
		if hdr != "" {
			seedIdx = e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_holes_seed("+irPtr+" %s, i64 %s, i32 %d)", seedIdx, hdr, lenReg, map[bool]int{false: 0, true: 1}[fromRight]))
			e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 0", emptyReg, seedIdx))
		} else {
			e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", emptyReg, lenReg))
		}
		emptyL := e.freshLabel("red.empty")
		seedL := e.freshLabel("red.seed")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", emptyReg, emptyL, seedL))

		e.emitLabel(emptyL)
		e.emitInternalThrow(e.internString("Reduce of empty array with no initial value"))

		e.emitLabel(seedL)
		// Seed from the last element (reduceRight) or the first (reduce), then
		// start the loop one step inward.
		firstGep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i64 %s", firstGep, elemTy.IR, ptrReg, seedIdx))
		firstVal := e.loadArrayElem(firstGep, elemTy)
		if widenSeed {
			if firstVal, err = e.emitBoxValue(firstVal); err != nil {
				return Value{}, err
			}
		}
		e.emitInstr(fmt.Sprintf("store %s %s, "+irPtr+" %s, align %d", accSlotIR(accTy), firstVal.Ref, accAlloca, accTy.Align()))
		startIdx := e.freshReg()
		if fromRight {
			e.emitInstr(fmt.Sprintf("%s = sub i64 %s, 1", startIdx, seedIdx))
		} else {
			e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", startIdx, seedIdx))
		}
		e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", startIdx, idxAlloca))
	}

	condL := e.freshLabel("red.cond")
	bodyL := e.freshLabel("red.body")
	doneL := e.freshLabel("red.done")

	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	idxVal := e.freshReg()
	done := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", idxVal, idxAlloca))
	// reduceRight walks down and finishes once the index passes 0 (negative);
	// reduce walks up and finishes when it reaches len.
	if fromRight {
		e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 0", done, idxVal))
	} else {
		e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", done, idxVal, lenReg))
	}
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", done, doneL, bodyL))

	e.emitLabel(bodyL)
	redStep := 1
	if fromRight {
		redStep = -1
	}
	e.emitHoleSkip(hdr, idxVal, idxAlloca, redStep, condL)
	accCur := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, "+irPtr+" %s, align %d", accCur, accSlotIR(accTy), accAlloca, storageAlign(accTy)))
	inGep := e.hofLiveGep(live, ptrReg, idxVal, elemTy, func() { e.hofSkip(idxVal, idxAlloca, redStep, condL) })
	inVal := e.loadArrayElem(inGep, elemTy)

	newAcc, err := e.emitCBCall(cb, e.hofCBArgs(cb, []Value{{Ref: accCur, Ty: accTy}, e.walkElemArg(live, idxVal, elemTy, inVal)}, idxVal, live, self, ptrReg, lenReg, elemTy))
	if err != nil {
		return Value{}, err
	}
	// A callback that can fall off the end returns `T | undefined` (ADR-01065);
	// storing that into a bare-`T` accumulator is tsc's "Type 'T | undefined' is
	// not assignable to type 'T'" — reject as tsc does rather than let coerce
	// collapse the absent value to zero. `-compat=js` keeps the lenient collapse
	// (the accumulator would be `undefined` there — BACKLOG §0).
	if voidCB {
		newAcc = Value{Ref: fmt.Sprint(nbUndefined), Ty: TypeAny}
	}
	if isNullableScalar(newAcc.Ty) && !isNullableScalar(accTy) && !e.compatJS() && !accWidened {
		return Value{}, internalAt(pos, "%s callback result %s | undefined past the accumulator's widening", verb, tsTypeName(newAcc.Ty.withoutNullable()))
	}
	if accWidened {
		// The widened accumulator stores the callback's nullable result as is
		// (the { i1, T } aggregate, or a possibly-null pointer) — absence travels
		// with it into the next step and out as the result.
		stepped := e.convert(newAcc, accTy)
		e.emitInstr(fmt.Sprintf("store %s %s, "+irPtr+" %s, align %d", accSlotIR(accTy), stepped.Ref, accAlloca, storageAlign(accTy)))
	} else {
		newAccCoerced := e.convert(newAcc, accTy)
		// The accumulator has one type for the whole fold: the initial value's, or
		// the element type without one. A callback whose result has no conversion
		// to it (`[1, 2].reduce((x, y) => `${x}+${y}`, 0)` — a string into a number)
		// is tsc's "no overload matches this call"; storing it anyway was invalid IR.
		if newAccCoerced.Ty.IR != accTy.IR {
			return Value{}, internalAt(pos, "%s callback result %s past the accumulator %s (checker-rejected)", verb, tsTypeName(newAcc.Ty), tsTypeName(accTy))
		}
		e.emitInstr(fmt.Sprintf("store %s %s, "+irPtr+" %s, align %d", accSlotIR(accTy), newAccCoerced.Ref, accAlloca, accTy.Align()))
	}

	idxNext := e.freshReg()
	if fromRight {
		e.emitInstr(fmt.Sprintf("%s = sub i64 %s, 1", idxNext, idxVal))
	} else {
		e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", idxNext, idxVal))
	}
	e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", idxNext, idxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(doneL)
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, "+irPtr+" %s, align %d", result, accSlotIR(accTy), accAlloca, storageAlign(accTy)))
	return Value{Ref: result, Ty: accTy}, nil
}

// emitArrayFind implements arr.find(pred): returns the first element satisfying
// pred, or `undefined` (`T | undefined`, TDD-00187) if none is found.
func (e *Emitter) emitArrayFind(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, internalAt(pos, "find arity %d", len(args))
	}
	live, ptrReg, lenReg, elemTy, err := e.resolveArrayHOF(mem.Object, pos)
	if err != nil {
		return Value{}, err
	}
	self := e.hofSelf(mem.Object, live)
	live = e.hofLiveHeader(mem.Object, live)
	cb, err := e.resolveCallbackWithHints(args[0], []Type{findElemHint(live, elemTy), TypeF64, e.hofSelfHint(self, elemTy)})
	if err != nil {
		return Value{}, err
	}

	foundAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", foundAlloca, elemTy.IR, elemTy.Align()))
	// Miss default: undefined box for a dynamic element, zero/null otherwise
	// (a scalar's zero is then marked absent via the flag below — TDD-00187).
	e.emitInstr(fmt.Sprintf("store %s %s, "+irPtr+" %s, align %d", elemTy.IR, missRef(elemTy), foundAlloca, elemTy.Align()))
	flagAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i1, align 1", flagAlloca))
	e.emitInstr(fmt.Sprintf("store i1 false, "+irPtr+" %s, align 1", flagAlloca))

	idxAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, "+irPtr+" %s, align 8", idxAlloca))

	condL := e.freshLabel("find.cond")
	bodyL := e.freshLabel("find.body")
	matchL := e.freshLabel("find.match")
	incL := e.freshLabel("find.inc")
	doneL := e.freshLabel("find.done")

	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	idxVal := e.freshReg()
	done := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", idxVal, idxAlloca))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", done, idxVal, lenReg))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", done, doneL, bodyL))

	e.emitLabel(bodyL)
	inGep := e.hofLiveGep(live, ptrReg, idxVal, elemTy, nil)
	inVal := e.loadArrayElem(inGep, elemTy)
	predVal, err := e.emitCBCall(cb, e.hofCBArgs(cb, []Value{e.findElemArg(live, idxVal, elemTy, inVal)}, idxVal, live, self, ptrReg, lenReg, elemTy))
	if err != nil {
		return Value{}, err
	}
	boolVal := e.emitToBool(predVal)
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", boolVal.Ref, matchL, incL))

	e.emitLabel(matchL)
	e.storeArrayElem(foundAlloca, elemTy, inVal)
	e.emitInstr(fmt.Sprintf("store i1 true, "+irPtr+" %s, align 1", flagAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))

	e.emitLabel(incL)
	idxNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", idxNext, idxVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", idxNext, idxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(doneL)
	if elemTy.IsArray() {
		return e.loadArrayElemMaybeNull(foundAlloca, elemTy), nil
	}
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, "+irPtr+" %s, align %d", result, elemTy.IR, foundAlloca, elemTy.Align()))
	found := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i1, "+irPtr+" %s, align 1", found, flagAlloca))
	return e.wrapUndefinedable(Value{Ref: result, Ty: elemTy}, found), nil
}

// emitArraySome implements arr.some(pred): returns true if any element satisfies pred.
func (e *Emitter) emitArraySome(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, internalAt(pos, "some arity %d", len(args))
	}
	hdr, ptrReg, lenReg, elemTy, err := e.resolveArrayHOF(mem.Object, pos)
	if err != nil {
		return Value{}, err
	}
	self := e.hofSelf(mem.Object, hdr)
	live := e.hofLiveHeader(mem.Object, hdr)
	hdr = e.hofHolesHeader(mem.Object, hdr)
	cb, err := e.resolveCallbackWithHints(args[0], []Type{e.walkElemHint(live, elemTy), TypeF64, e.hofSelfHint(self, elemTy)})
	if err != nil {
		return Value{}, err
	}

	resAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i1, align 1", resAlloca))
	e.emitInstr(fmt.Sprintf("store i1 0, "+irPtr+" %s, align 1", resAlloca))

	idxAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, "+irPtr+" %s, align 8", idxAlloca))

	condL := e.freshLabel("some.cond")
	bodyL := e.freshLabel("some.body")
	trueL := e.freshLabel("some.true")
	incL := e.freshLabel("some.inc")
	doneL := e.freshLabel("some.done")

	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	idxVal := e.freshReg()
	done := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", idxVal, idxAlloca))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", done, idxVal, lenReg))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", done, doneL, bodyL))

	e.emitLabel(bodyL)
	e.emitHoleSkip(hdr, idxVal, idxAlloca, 1, condL)
	inGep := e.hofLiveGep(live, ptrReg, idxVal, elemTy, func() { e.hofSkip(idxVal, idxAlloca, 1, condL) })
	inVal := e.loadArrayElem(inGep, elemTy)
	predVal, err := e.emitCBCall(cb, e.hofCBArgs(cb, []Value{e.walkElemArg(live, idxVal, elemTy, inVal)}, idxVal, live, self, ptrReg, lenReg, elemTy))
	if err != nil {
		return Value{}, err
	}
	boolVal := e.emitToBool(predVal)
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", boolVal.Ref, trueL, incL))

	e.emitLabel(trueL)
	e.emitInstr(fmt.Sprintf("store i1 1, "+irPtr+" %s, align 1", resAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))

	e.emitLabel(incL)
	idxNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", idxNext, idxVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", idxNext, idxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(doneL)
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i1, "+irPtr+" %s, align 1", result, resAlloca))
	return Value{Ref: result, Ty: TypeBool}, nil
}

// emitArrayEvery implements arr.every(pred): returns true if all elements satisfy pred.
func (e *Emitter) emitArrayEvery(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, internalAt(pos, "every arity %d", len(args))
	}
	hdr, ptrReg, lenReg, elemTy, err := e.resolveArrayHOF(mem.Object, pos)
	if err != nil {
		return Value{}, err
	}
	self := e.hofSelf(mem.Object, hdr)
	live := e.hofLiveHeader(mem.Object, hdr)
	hdr = e.hofHolesHeader(mem.Object, hdr)
	cb, err := e.resolveCallbackWithHints(args[0], []Type{e.walkElemHint(live, elemTy), TypeF64, e.hofSelfHint(self, elemTy)})
	if err != nil {
		return Value{}, err
	}

	resAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i1, align 1", resAlloca))
	e.emitInstr(fmt.Sprintf("store i1 1, "+irPtr+" %s, align 1", resAlloca)) // assume true

	idxAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, "+irPtr+" %s, align 8", idxAlloca))

	condL := e.freshLabel("evry.cond")
	bodyL := e.freshLabel("evry.body")
	falseL := e.freshLabel("evry.false")
	incL := e.freshLabel("evry.inc")
	doneL := e.freshLabel("evry.done")

	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	idxVal := e.freshReg()
	done := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", idxVal, idxAlloca))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", done, idxVal, lenReg))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", done, doneL, bodyL))

	e.emitLabel(bodyL)
	e.emitHoleSkip(hdr, idxVal, idxAlloca, 1, condL)
	inGep := e.hofLiveGep(live, ptrReg, idxVal, elemTy, func() { e.hofSkip(idxVal, idxAlloca, 1, condL) })
	inVal := e.loadArrayElem(inGep, elemTy)
	predVal, err := e.emitCBCall(cb, e.hofCBArgs(cb, []Value{e.walkElemArg(live, idxVal, elemTy, inVal)}, idxVal, live, self, ptrReg, lenReg, elemTy))
	if err != nil {
		return Value{}, err
	}
	boolVal := e.emitToBool(predVal)
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", boolVal.Ref, incL, falseL))

	e.emitLabel(falseL)
	e.emitInstr(fmt.Sprintf("store i1 0, "+irPtr+" %s, align 1", resAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))

	e.emitLabel(incL)
	idxNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", idxNext, idxVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", idxNext, idxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(doneL)
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i1, "+irPtr+" %s, align 1", result, resAlloca))
	return Value{Ref: result, Ty: TypeBool}, nil
}

// emitArrayJoin implements arr.join(sep?): concatenates elements into a string,
// separated by sep (default ","). Non-string elements are converted via sprintf.

// hofCBArgs is an array method's callback arguments: the leading ones (the
// element, or the accumulator and element), then the index and the array
// itself when the callback takes them. The array is a fresh header
// over the same data (the array value shape every HOF returns): element
// reads through it are live, the length a snapshot (ADR-00573).
func (e *Emitter) hofCBArgs(cb Callback, lead []Value, idx, live string, self Value, ptrReg, lenReg string, elemTy Type) []Value {
	args := lead
	if cb.acceptsArgAt(len(lead)) {
		args = append(args, Value{Ref: idx, Ty: TypeI64})
	}
	if cb.acceptsArgAt(len(lead)+1) && self.Ref != "" {
		return append(args, self) // the typed array itself
	}
	if cb.acceptsArgAt(len(lead)+1) && live != "" {
		// The array itself (hofLiveHeader).
		return append(args, Value{Ref: live, Ty: ArrayOf(elemTy)})
	}
	if cb.acceptsArgAt(len(lead) + 1) {
		agg1 := e.newArrayHeader(ptrReg, lenReg)
		args = append(args, Value{Ref: agg1, Ty: ArrayOf(elemTy)})
	}
	return args
}

// hofSelf is the receiver an array method passes its callback as the
// third argument when that is a typed array, whose header hdr is: the typed
// array itself (`a === t`). Empty for any other receiver, which passes its
// live header or a header over its data (hofCBArgs).
func (e *Emitter) hofSelf(objExpr ast.Expression, hdr string) Value {
	if hdr == "" || hdr == "zeroinitializer" {
		return Value{}
	}
	t := e.typeOfExpr(objExpr)
	if !t.IsTypedArray() || t.IsDynamic() || t.Nullable() {
		return Value{}
	}
	return Value{Ref: hdr, Ty: t}
}

// hofSelfHint is the type a callback's third parameter takes: the typed
// array's own type, else an array of elemTy.
func (e *Emitter) hofSelfHint(self Value, elemTy Type) Type {
	if self.Ref != "" {
		return self.Ty
	}
	return ArrayOf(elemTy)
}

// typedArrayResultType is the type of a new typed array a method of recv
// returns: the receiver's own kind (%TypedArray%'s species), not nullable.
func typedArrayResultType(recv Type) Type {
	recv.setNullable(false)
	recv.setUndefined(false)
	return recv
}

// accSlotIR is the IR of reduce's accumulator slot: the value's own
// register shape.
func accSlotIR(t Type) string {
	return storageIR(t)
}

// reduceSeedType is the accumulator type a reduce's initial value gives:
// its own, except that JavaScript's empty `[]` seed (-compat=js) has no
// element type and holds whatever the callback puts in it.
func (e *Emitter) reduceSeedType(seed ast.Expression) Type {
	if lit, ok := seed.(*ast.ArrayLiteral); ok && len(lit.Elements) == 0 && e.compatJS() {
		return ArrayOf(TypeAny)
	}
	return e.typeOfExpr(seed)
}

// hofLiveHeader is the header of the array an array method's callback walk
// runs over, hdr, when the callback may change that array (a plain array,
// not a typed or flat one); "" otherwise. Each step reads the element
// through it (hofLiveGep): a callback that grows the array moves its data.
func (e *Emitter) hofLiveHeader(objExpr ast.Expression, hdr string) string {
	if hdr == "" || hdr == "zeroinitializer" {
		return ""
	}
	t := e.typeOfExpr(objExpr)
	if t.IsTypedArray() && !t.IsDynamic() && !t.Nullable() && e.programResizesBuffers() {
		// A view over a resizable buffer the callback may resize: each
		// step reads the length its header has then, an index past it the
		// element undefined (a typed array's [[Get]]), not skipped.
		if e.liveTyped == nil {
			e.liveTyped = map[string]bool{}
		}
		e.liveTyped[hdr] = true
		return hdr
	}
	// (A possibly-absent array's walk got here with its header: the
	// receiver was there.)
	if !t.IsArray() || t.IsTypedArray() || t.IsFlatArray() || t.ExecArray || t.IsDynamic() {
		return ""
	}
	return hdr
}

// programResizesBuffers reports a program that may resize an ArrayBuffer
// (it names `resize` or `grow`, or makes one with `maxByteLength`).
func (e *Emitter) programResizesBuffers() bool {
	if e.resizesKnown {
		return e.resizes
	}
	e.resizesKnown = true
	if e.prog != nil {
		ast.Inspect(e.prog, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.MemberExpression:
				e.resizes = e.resizes || x.Property == "resize" || x.Property == "grow"
			case *ast.ObjectLiteral:
				for _, p := range x.Properties {
					e.resizes = e.resizes || p.Key == "maxByteLength"
				}
			}
			return !e.resizes
		})
	}
	return e.resizes
}

// walkElemHint is the element parameter type a walk's callback gets: under
// a live typed array, possibly undefined (findElemHint).
func (e *Emitter) walkElemHint(live string, elemTy Type) Type {
	if e.liveTyped[live] {
		return findElemHint(live, elemTy)
	}
	return elemTy
}

// walkElemArg is the element a walk's callback receives at idx (findElemArg
// under a live typed array).
func (e *Emitter) walkElemArg(live, idx string, elemTy Type, raw Value) Value {
	if e.liveTyped[live] {
		return e.findElemArg(live, idx, elemTy, raw)
	}
	return raw
}

// hofLiveGep is the address of element idx of the array a callback walk
// runs over: through the live header, re-read each step. An index the
// callback shrank the array below runs skip (forEach, map, filter, reduce,
// some, every: spec HasProperty is false) or, with skip nil, reads the
// type's miss value (find and its kin: spec Get is undefined). Without a
// live header, the element of the data loaded before the walk.
func (e *Emitter) hofLiveGep(live, ptrReg, idx string, elemTy Type, skip func()) string {
	gep := e.freshReg()
	if live == "" {
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i64 %s", gep, elemTy.IR, ptrReg, idx))
		return gep
	}
	if e.liveTyped[live] {
		skip = nil // (a typed array's element past its length is undefined)
	}
	v := Value{Ref: live, Ty: ArrayOf(elemTy)}
	data, n := e.arrayData(v), e.arrayLen(v)
	in := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ult i64 %s, %s", in, idx, n))
	if skip != nil {
		okL, outL := e.freshLabel("hof.live"), e.freshLabel("hof.gone")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", in, okL, outL))
		e.emitLabel(outL)
		skip()
		e.emitLabel(okL)
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i64 %s", gep, elemTy.IR, data, idx))
		return gep
	}
	miss := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", miss, elemTy.IR, elemTy.Align()))
	e.emitInstr(fmt.Sprintf("store %s %s, "+irPtr+" %s, align %d", elemTy.IR, missRef(elemTy), miss, elemTy.Align()))
	at, r := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i64 %s", at, elemTy.IR, data, idx))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, "+irPtr+" %s, "+irPtr+" %s", r, in, at, miss))
	return r
}

// findElemHint is the element parameter type a find-kin callback gets: a
// walk over a live header can pass an index the callback shrank the array
// below, where the element is undefined, so a scalar element arrives as its
// `T | undefined` form.
func findElemHint(live string, elemTy Type) Type {
	if live == "" {
		return elemTy
	}
	if t := undefinedableElem(elemTy); isNullableScalar(t) {
		return t
	}
	return elemTy
}

// findElemArg is the element a find-kin callback receives at idx: raw, or
// for a scalar under a live header its `T | undefined` form, absent past
// the current length.
func (e *Emitter) findElemArg(live, idx string, elemTy Type, raw Value) Value {
	if !isNullableScalar(findElemHint(live, elemTy)) {
		return raw
	}
	in := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ult i64 %s, %s", in, idx, e.arrayLen(Value{Ref: live, Ty: ArrayOf(elemTy)})))
	return e.wrapUndefinedable(raw, in)
}

// hofSkip steps a walk's index (idxSlot) by step past idx and resumes it
// at condL.
func (e *Emitter) hofSkip(idx, idxSlot string, step int, condL string) {
	next := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, %d", next, idx, step))
	e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", next, idxSlot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
}
