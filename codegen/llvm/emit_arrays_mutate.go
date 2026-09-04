package llvm

import (
	"KlainMainLang/ast"
	"fmt"
)

// resolveArrayMutLoc resolves the *storage location* of a mutable array —
// the address its data pointer lives at and the address its length lives at —
// so the in-place mutators (push/pop/shift/unshift/splice) can write a new
// ptr/len back. Three receiver shapes exist:
//   - a named variable: the two allocas of its Symbol (Ptr/LenPtr);
//   - an object/class array field (incl. `this.field`): the field's inline
//     {ptr, i64} struct slot, split into its two sub-slots;
//   - a nested-array element (`matrix[i]`): the element slot holds a box
//     pointer to a heap {ptr, i64}, so mutating through the box updates every
//     alias of that inner array.
//
// Anything else (a slice result, a call result, ...) has no writable home for
// a resized length, which is exactly why these methods can't accept an
// arbitrary array-valued expression.
func (e *Emitter) resolveArrayMutLoc(objExpr ast.Expression, verb string, pos ast.Pos) (ptrPtr, lenPtr string, elemTy Type, err error) {
	switch obj := objExpr.(type) {
	case *ast.Identifier:
		sym, ok := e.lookup(obj.Name)
		if !ok {
			return "", "", Type{}, fmt.Errorf("%d:%d: undefined variable '%s'", pos.Line, pos.Col, obj.Name)
		}
		if !sym.Ty.IsArray || sym.Ty.ElemType == nil {
			return "", "", Type{}, fmt.Errorf("%d:%d: '%s' is not an array", pos.Line, pos.Col, obj.Name)
		}
		// Object-reference model (TDD-00127): the data/len field addresses of the
		// array's *current* shared header. A mutator writing a new data ptr/len
		// through these addresses updates the one header every alias — including a
		// callee this array was passed to — observes, giving JS reference
		// semantics (push/splice inside a callee grow the caller's array).
		dataSlot, lenSlot := e.arrayDataLenSlots(sym)
		return dataSlot, lenSlot, *sym.Ty.ElemType, nil

	case *ast.MemberExpression:
		objVal, evalErr := e.emitExpr(obj.Object)
		if evalErr != nil {
			return "", "", Type{}, evalErr
		}
		if !objVal.Ty.IsObject {
			return "", "", Type{}, fmt.Errorf("%d:%d: %s requires an array variable, array field, or nested-array element", pos.Line, pos.Col, verb)
		}
		idx, fieldTy, ok := objVal.Ty.FieldIndex(obj.Property)
		if !ok {
			return "", "", Type{}, fmt.Errorf("%d:%d: no field '%s'", pos.Line, pos.Col, obj.Property)
		}
		fieldTy = e.canonicalizeClassTy(fieldTy)
		if !fieldTy.IsArray || fieldTy.ElemType == nil {
			return "", "", Type{}, fmt.Errorf("%d:%d: field '%s' is not an array", pos.Line, pos.Col, obj.Property)
		}
		if objVal.Ty.IsClass {
			if err := e.checkFieldVisibility(objVal.Ty.ClassName, obj.Property, pos); err != nil {
				return "", "", Type{}, err
			}
		}
		slot := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", slot, objVal.Ty.StructIR(), objVal.Ref, idx))
		ptrPtr = e.freshReg()
		lenPtr = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr {ptr, i64}, ptr %s, i32 0, i32 0", ptrPtr, slot))
		e.emitInstr(fmt.Sprintf("%s = getelementptr {ptr, i64}, ptr %s, i32 0, i32 1", lenPtr, slot))
		return ptrPtr, lenPtr, *fieldTy.ElemType, nil

	case *ast.IndexExpression:
		slot, eTy, idxErr := e.emitIndexPtr(obj)
		if idxErr != nil {
			return "", "", Type{}, idxErr
		}
		if !eTy.IsArray || eTy.ElemType == nil {
			return "", "", Type{}, fmt.Errorf("%d:%d: %s requires an array-typed element", pos.Line, pos.Col, verb)
		}
		boxPtr := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", boxPtr, slot))
		ptrPtr = e.freshReg()
		lenPtr = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr {ptr, i64}, ptr %s, i32 0, i32 0", ptrPtr, boxPtr))
		e.emitInstr(fmt.Sprintf("%s = getelementptr {ptr, i64}, ptr %s, i32 0, i32 1", lenPtr, boxPtr))
		return ptrPtr, lenPtr, *eTy.ElemType, nil
	}
	return "", "", Type{}, fmt.Errorf("%d:%d: %s requires an array variable, array field, or nested-array element", pos.Line, pos.Col, verb)
}

func (e *Emitter) emitPop(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 0 {
		return Value{}, fmt.Errorf("%d:%d: pop takes no arguments", pos.Line, pos.Col)
	}
	ptrPtr, lenPtr, elemTy, err := e.resolveArrayMutLoc(mem.Object, "pop", pos)
	if err != nil {
		return Value{}, err
	}

	curPtr := e.freshReg()
	curLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", curPtr, ptrPtr))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", curLen, lenPtr))

	// Guard: empty array — return the element type's zero value and leave
	// length unchanged (0). Real JS returns `undefined`; this compiler has
	// no general sentinel for that on a concrete scalar type, so we return
	// the type's own zero value — the same simplification already used by
	// optional parameters, under-assigned class fields, and destructuring
	// past the source's length (ADR-00157/ADR-00158/ADR-00164).
	isEmpty := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", isEmpty, curLen))

	emptyL := e.freshLabel("pop.empty")
	popL := e.freshLabel("pop.pop")
	doneL := e.freshLabel("pop.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isEmpty, emptyL, popL))

	e.emitLabel(emptyL)
	var zeroVal Value
	if elemTy.IsArray {
		// Nested-array element: zero is a {null, 0} aggregate.
		r0 := e.freshReg()
		r1 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr null, 0", r0))
		e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 0, 1", r1, r0))
		zeroVal = Value{Ref: r1, Ty: elemTy}
	} else {
		zeroVal = e.emitScalarZero(elemTy)
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))

	e.emitLabel(popL)
	newLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, 1", newLen, curLen))

	slot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", slot, elemTy.IR, curPtr, newLen))
	result := e.loadArrayElem(slot, elemTy)

	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", newLen, lenPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))

	e.emitLabel(doneL)
	phiReg := e.freshReg()
	if elemTy.IsArray {
		// Nested array: {ptr, i64} aggregate.
		e.emitInstr(fmt.Sprintf("%s = phi {ptr, i64} [ %s, %%%s ], [ %s, %%%s ]", phiReg, zeroVal.Ref, emptyL, result.Ref, popL))
	} else {
		e.emitInstr(fmt.Sprintf("%s = phi %s [ %s, %%%s ], [ %s, %%%s ]", phiReg, elemTy.IR, zeroVal.Ref, emptyL, result.Ref, popL))
	}

	return Value{Ref: phiReg, Ty: elemTy}, nil
}

// emitSplice implements arr.splice(start, deleteCount?, ...items): removes
// deleteCount elements at start, inserts items in their place, and returns
// the removed elements as a new array. start is normalized the same way
// .at()/.slice() already do (negative counts from the end, clamped to
// [0, len]); deleteCount is clamped to [0, len - start] — real JS clamps
// deleteCount the same way, and this compiler used to skip that clamp
// entirely (see ADR-00056: a deleteCount larger than the remaining tail
// read past the backing allocation and corrupted the array's own length to
// negative, a real memory-safety bug, not just a wrong-answer one).
func (e *Emitter) emitSplice(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 1 {
		return Value{}, fmt.Errorf("%d:%d: splice takes at least 1 argument (start)", pos.Line, pos.Col)
	}
	ptrPtr, lenPtr, elemTy, err := e.resolveArrayMutLoc(mem.Object, "splice", pos)
	if err != nil {
		return Value{}, err
	}

	curPtr := e.freshReg()
	curLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", curPtr, ptrPtr))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", curLen, lenPtr))

	startRaw, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	startN := e.emitNormalizeSliceIdx(e.arrayIndexToI64(startRaw).Ref, curLen)

	// deleteCount defaults to "everything from start to the end", matching
	// real JS when the argument is omitted; when given, clamp to what's
	// actually left from start so a too-large value can never read/shift
	// past the buffer.
	avail := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", avail, curLen, startN))
	delCount := avail
	if len(args) >= 2 {
		delRaw, err := e.emitExpr(args[1])
		if err != nil {
			return Value{}, err
		}
		delRaw = e.coerce(delRaw, TypeI64)
		negClamped := e.freshReg()
		isNeg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 0", isNeg, delRaw.Ref))
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 0, i64 %s", negClamped, isNeg, delRaw.Ref))
		tooBig := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp sgt i64 %s, %s", tooBig, negClamped, avail))
		clamped := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", clamped, tooBig, avail, negClamped))
		delCount = clamped
	}

	var items []ast.Expression
	if len(args) > 2 {
		items = args[2:]
	}
	numInserted := int64(len(items))

	// Copy the removed slice into the result array before anything shifts —
	// once the tail shift below happens, this region no longer holds the
	// original values.
	e.ensureMalloc()
	e.ensureMemmove()
	resultPtr := e.freshReg()
	copyBytes := e.freshReg()
	removedSrc := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %d", copyBytes, delCount, elemTy.Align()))
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", resultPtr, copyBytes))
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", removedSrc, elemTy.IR, curPtr, startN))
	e.emitInstr(fmt.Sprintf("call ptr @memmove(ptr %s, ptr %s, i64 %s)", resultPtr, removedSrc, copyBytes))

	// newLen = curLen - deleteCount + numInserted. Grow the backing buffer
	// first if needed — the tail shift below must land in valid memory.
	// Both phi predecessors below are labels this function just created and
	// branched from directly (checkL, growL) — never assume the name of
	// whatever block was active before this point, since emitSplice can run
	// mid-function from an arbitrarily-named caller block.
	newLen := e.freshReg()
	tmpLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", tmpLen, curLen, delCount))
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, %d", newLen, tmpLen, numInserted))

	checkL := e.freshLabel("splice.checkgrow")
	growL := e.freshLabel("splice.grow")
	afterGrowL := e.freshLabel("splice.aftergrow")
	e.emitTerminator(fmt.Sprintf("br label %%%s", checkL))

	e.emitLabel(checkL)
	growing := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp sgt i64 %s, %s", growing, newLen, curLen))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", growing, growL, afterGrowL))

	e.emitLabel(growL)
	e.ensureRealloc()
	newBytes := e.freshReg()
	reallocedPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %d", newBytes, newLen, elemTy.Align()))
	e.emitInstr(fmt.Sprintf("%s = call ptr @realloc(ptr %s, i64 %s)", reallocedPtr, curPtr, newBytes))
	e.emitTerminator(fmt.Sprintf("br label %%%s", afterGrowL))

	e.emitLabel(afterGrowL)
	workPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = phi ptr [ %s, %%%s ], [ %s, %%%s ]", workPtr, reallocedPtr, growL, curPtr, checkL))

	// Shift the tail (arr[start+deleteCount : curLen], using the ORIGINAL
	// length) to its new home at start+numInserted. memmove handles both
	// directions correctly regardless of whether items are being inserted
	// (tail moves right) or net-removed (tail moves left).
	startPlusDel := e.freshReg()
	startPlusIns := e.freshReg()
	tailSrc := e.freshReg()
	tailDst := e.freshReg()
	tailCount := e.freshReg()
	shiftBytes := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, %s", startPlusDel, startN, delCount))
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, %d", startPlusIns, startN, numInserted))
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", tailSrc, elemTy.IR, workPtr, startPlusDel))
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", tailDst, elemTy.IR, workPtr, startPlusIns))
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", tailCount, curLen, startPlusDel))
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %d", shiftBytes, tailCount, elemTy.Align()))
	e.emitInstr(fmt.Sprintf("call ptr @memmove(ptr %s, ptr %s, i64 %s)", tailDst, tailSrc, shiftBytes))

	// Write the inserted items into [start, start+numInserted).
	for i, itemExpr := range items {
		itemVal, err := e.emitExpr(itemExpr)
		if err != nil {
			return Value{}, err
		}
		itemVal = e.coerce(itemVal, elemTy)
		slotIdx := e.freshReg()
		slot := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = add i64 %s, %d", slotIdx, startN, i))
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", slot, elemTy.IR, workPtr, slotIdx))
		e.storeArrayElem(slot, elemTy, itemVal)
	}

	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", workPtr, ptrPtr))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", newLen, lenPtr))

	// Pack the removed elements into a {ptr, i64} aggregate.
	r0 := e.freshReg()
	r1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", r0, resultPtr))
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %s, 1", r1, r0, delCount))

	return Value{Ref: r1, Ty: ArrayOf(elemTy)}, nil
}

// emitArrayToSpliced implements arr.toSpliced(start, deleteCount?, ...items):
// the same start/deleteCount normalization and clamping as the fixed
// emitSplice, but builds and returns a brand-new array — [0, start) + items
// + [start+deleteCount, len) — instead of mutating arr in place. Since
// there's no existing buffer to preserve or shift, this is a straight
// three-segment concatenation into one freshly sized allocation, simpler
// than splice's in-place grow/shift logic.
func (e *Emitter) emitArrayToSpliced(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 1 {
		return Value{}, fmt.Errorf("%d:%d: toSpliced takes at least 1 argument (start)", pos.Line, pos.Col)
	}
	ptrReg, lenReg, elemTy, err := e.resolveArrayForHOF(mem.Object, pos)
	if err != nil {
		return Value{}, err
	}

	startRaw, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	startN := e.emitNormalizeSliceIdx(e.arrayIndexToI64(startRaw).Ref, lenReg)
	avail := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", avail, lenReg, startN))
	delCount := avail
	if len(args) >= 2 {
		delRaw0, err := e.emitExpr(args[1])
		if err != nil {
			return Value{}, err
		}
		delRaw := e.coerce(delRaw0, TypeI64)
		negClamped := e.freshReg()
		isNeg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 0", isNeg, delRaw.Ref))
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 0, i64 %s", negClamped, isNeg, delRaw.Ref))
		tooBig := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp sgt i64 %s, %s", tooBig, negClamped, avail))
		clamped := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", clamped, tooBig, avail, negClamped))
		delCount = clamped
	}

	var items []ast.Expression
	if len(args) > 2 {
		items = args[2:]
	}
	numInserted := int64(len(items))

	newLen := e.freshReg()
	tmpLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", tmpLen, lenReg, delCount))
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, %d", newLen, tmpLen, numInserted))

	e.ensureMalloc()
	e.ensureMemmove()
	newBytes := e.freshReg()
	newPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %d", newBytes, newLen, elemTy.Align()))
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", newPtr, newBytes))

	// Segment 1: [0, start) copied as-is.
	headBytes := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %d", headBytes, startN, elemTy.Align()))
	e.emitInstr(fmt.Sprintf("call ptr @memmove(ptr %s, ptr %s, i64 %s)", newPtr, ptrReg, headBytes))

	// Segment 2: the inserted items, written at [start, start+numInserted).
	for i, itemExpr := range items {
		itemVal, err := e.emitExpr(itemExpr)
		if err != nil {
			return Value{}, err
		}
		itemVal = e.coerce(itemVal, elemTy)
		slotIdx := e.freshReg()
		slot := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = add i64 %s, %d", slotIdx, startN, i))
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", slot, elemTy.IR, newPtr, slotIdx))
		e.storeArrayElem(slot, elemTy, itemVal)
	}

	// Segment 3: [start+deleteCount, len) copied to [start+numInserted, newLen).
	tailStart := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, %s", tailStart, startN, delCount))
	tailCount := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", tailCount, lenReg, tailStart))
	tailBytes := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %d", tailBytes, tailCount, elemTy.Align()))
	tailSrc := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", tailSrc, elemTy.IR, ptrReg, tailStart))
	startPlusIns := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, %d", startPlusIns, startN, numInserted))
	tailDst := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", tailDst, elemTy.IR, newPtr, startPlusIns))
	e.emitInstr(fmt.Sprintf("call ptr @memmove(ptr %s, ptr %s, i64 %s)", tailDst, tailSrc, tailBytes))

	r0 := e.freshReg()
	r1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", r0, newPtr))
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %s, 1", r1, r0, newLen))
	return Value{Ref: r1, Ty: ArrayOf(elemTy)}, nil
}

// emitShift implements arr.shift(): save ptr[0], memmove left, decrement len.
// On an empty array, returns the element type's zero value and leaves length
// unchanged (0) — real JS returns `undefined`, but this compiler has no general
// sentinel for that on a concrete scalar type (the same simplification used by
// optional params, class-field zero-initialization, and destructuring past the
// source length — ADR-00157/ADR-00158/ADR-00164).
func (e *Emitter) emitShift(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 0 {
		return Value{}, fmt.Errorf("%d:%d: shift takes no arguments", pos.Line, pos.Col)
	}
	ptrPtr, lenPtr, elemTy, err := e.resolveArrayMutLoc(mem.Object, "shift", pos)
	if err != nil {
		return Value{}, err
	}

	curPtr := e.freshReg()
	curLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", curPtr, ptrPtr))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", curLen, lenPtr))

	// Guard: empty array — return the element type's zero value.
	isEmpty := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", isEmpty, curLen))

	emptyL := e.freshLabel("shift.empty")
	shiftL := e.freshLabel("shift.shift")
	doneL := e.freshLabel("shift.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isEmpty, emptyL, shiftL))

	e.emitLabel(emptyL)
	var zeroVal Value
	if elemTy.IsArray {
		r0 := e.freshReg()
		r1 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr null, 0", r0))
		e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 0, 1", r1, r0))
		zeroVal = Value{Ref: r1, Ty: elemTy}
	} else {
		zeroVal = e.emitScalarZero(elemTy)
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))

	e.emitLabel(shiftL)
	// save first element before moving
	result := e.loadArrayElem(curPtr, elemTy)

	newLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, 1", newLen, curLen))

	// src = ptr + 1 element; move (len-1) elements left
	src := e.freshReg()
	moveBytes := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 1", src, elemTy.IR, curPtr))
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %d", moveBytes, newLen, elemTy.Align()))
	e.ensureMemmove()
	e.emitInstr(fmt.Sprintf("call ptr @memmove(ptr %s, ptr %s, i64 %s)", curPtr, src, moveBytes))

	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", newLen, lenPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))

	e.emitLabel(doneL)
	phiReg := e.freshReg()
	if elemTy.IsArray {
		e.emitInstr(fmt.Sprintf("%s = phi {ptr, i64} [ %s, %%%s ], [ %s, %%%s ]", phiReg, zeroVal.Ref, emptyL, result.Ref, shiftL))
	} else {
		e.emitInstr(fmt.Sprintf("%s = phi %s [ %s, %%%s ], [ %s, %%%s ]", phiReg, elemTy.IR, zeroVal.Ref, emptyL, result.Ref, shiftL))
	}

	return Value{Ref: phiReg, Ty: elemTy}, nil
}

// emitUnshift implements arr.unshift(...items): realloc, memmove right by the
// item count, write the items at [0..N), increment len by N. Returns the new
// length (matching JS semantics; the zero-argument call returns the current
// length unchanged).
func (e *Emitter) emitUnshift(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	ptrPtr, lenPtr, elemTy, err := e.resolveArrayMutLoc(mem.Object, "unshift", pos)
	if err != nil {
		return Value{}, err
	}

	vals := make([]Value, 0, len(args))
	for _, arg := range args {
		// A function-typed element contextually types an untyped arrow /
		// function-expression argument's parameters from the element
		// signature (ADR-00632) — otherwise its params self-infer to the
		// numeric default and mis-decode when later called.
		var v Value
		var err error
		if elemTy.IsFunc {
			v, err = e.emitExprWithObjectHint(arg, elemTy)
		} else {
			v, err = e.emitExpr(arg)
		}
		if err != nil {
			return Value{}, err
		}
		vals = append(vals, e.coerce(v, elemTy))
	}

	curPtr := e.freshReg()
	curLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", curPtr, ptrPtr))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", curLen, lenPtr))

	if len(vals) == 0 {
		return e.countToNumber(Value{Ref: curLen, Ty: TypeI64}), nil
	}

	newLen := e.freshReg()
	newBytes := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, %d", newLen, curLen, len(vals)))
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %d", newBytes, newLen, elemTy.Align()))

	e.ensureRealloc()
	newPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @realloc(ptr %s, i64 %s)", newPtr, curPtr, newBytes))

	// dst = newPtr + N elements; move existing elements right
	dst := e.freshReg()
	moveBytes := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %d", dst, elemTy.IR, newPtr, len(vals)))
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %d", moveBytes, curLen, elemTy.Align()))
	e.ensureMemmove()
	e.emitInstr(fmt.Sprintf("call ptr @memmove(ptr %s, ptr %s, i64 %s)", dst, newPtr, moveBytes))

	// write the new elements at [0..N)
	for i, val := range vals {
		slot := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %d", slot, elemTy.IR, newPtr, i))
		e.storeArrayElem(slot, elemTy, val)
	}

	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", newPtr, ptrPtr))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", newLen, lenPtr))

	return e.countToNumber(Value{Ref: newLen, Ty: TypeI64}), nil
}

// emitPush implements arr.push(val): realloc, store at [len], update ptr+len.
// Returns the new length (i64), matching JS semantics.
func (e *Emitter) emitPush(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	ptrPtr, lenPtr, elemTy, err := e.resolveArrayMutLoc(mem.Object, "push", pos)
	if err != nil {
		return Value{}, err
	}

	// Real .push(...items) is variadic (including the zero-argument call,
	// which just returns the current length): evaluate every item first
	// (left-to-right, before any resize is observable), then grow once by
	// the whole count and store each at its slot.
	vals := make([]Value, 0, len(args))
	for _, arg := range args {
		// A function-typed element contextually types an untyped arrow /
		// function-expression argument's parameters from the element
		// signature (ADR-00632) — otherwise its params self-infer to the
		// numeric default and mis-decode when later called.
		var v Value
		var err error
		if elemTy.IsFunc {
			v, err = e.emitExprWithObjectHint(arg, elemTy)
		} else {
			v, err = e.emitExpr(arg)
		}
		if err != nil {
			return Value{}, err
		}
		vals = append(vals, e.coerce(v, elemTy))
	}

	curPtr := e.freshReg()
	curLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", curPtr, ptrPtr))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", curLen, lenPtr))

	if len(vals) == 0 {
		return e.countToNumber(Value{Ref: curLen, Ty: TypeI64}), nil
	}

	newLen := e.freshReg()
	newBytes := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, %d", newLen, curLen, len(vals)))
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %d", newBytes, newLen, elemTy.Align()))

	e.ensureRealloc()
	newPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @realloc(ptr %s, i64 %s)", newPtr, curPtr, newBytes))

	for i, val := range vals {
		slotIdx := e.freshReg()
		slot := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = add i64 %s, %d", slotIdx, curLen, i))
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", slot, elemTy.IR, newPtr, slotIdx))
		e.storeArrayElem(slot, elemTy, val)
	}

	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", newPtr, ptrPtr))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", newLen, lenPtr))

	return e.countToNumber(Value{Ref: newLen, Ty: TypeI64}), nil
}
