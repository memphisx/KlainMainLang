package llvm

import (
	"fmt"
	"KlainMainLang/ast"
)

// Array variable declarations, mutations, destructuring, and higher-order functions.

func (e *Emitter) emitArrayVarDecl(v *ast.VarDeclaration, ty Type) error {
	elemTy := *ty.ElemType
	ptrName := e.freshReg()
	lenName := e.freshReg()

	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", ptrName))
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", lenName))
	e.define(v.Name, Symbol{Ptr: ptrName, LenPtr: lenName, Ty: ty})

	if v.Init == nil {
		e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", ptrName))
		e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", lenName))
		return nil
	}

	// Dynamic-size array: new Array<T>(runtimeSize)
	if na, ok := v.Init.(*ast.NewArrayExpression); ok {
		sizeVal, err := e.emitExpr(na.Size)
		if err != nil {
			return err
		}
		sizeVal = e.coerce(sizeVal, TypeI64)
		e.ensureCalloc()
		dataReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @calloc(i64 %s, i64 %d)", dataReg, sizeVal.Ref, elemTy.Align()))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", dataReg, ptrName))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", sizeVal.Ref, lenName))
		return nil
	}

	// Array variable initialised by a function that returns an array.
	if call, ok := v.Init.(*ast.CallExpression); ok {
		val, err := e.emitExpr(call)
		if err != nil {
			return err
		}
		// val.Ref holds the {ptr, i64} aggregate returned by emitCall.
		ptrReg := e.freshReg()
		lenReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", ptrReg, val.Ref))
		e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 1", lenReg, val.Ref))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", ptrReg, ptrName))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", lenReg, lenName))
		return nil
	}

	// For index expressions (e.g. groupMap["key"]) or any other expression that
	// produces a {ptr, i64} array aggregate, evaluate it and extract the parts.
	if _, ok := v.Init.(*ast.ArrayLiteral); !ok {
		val, err := e.emitExpr(v.Init)
		if err != nil {
			return err
		}
		if !val.Ty.IsArray {
			return fmt.Errorf("%d:%d: array variable must be initialized with an array expression", v.GetPos().Line, v.GetPos().Col)
		}
		ptrReg := e.freshReg()
		lenReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", ptrReg, val.Ref))
		e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 1", lenReg, val.Ref))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", ptrReg, ptrName))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", lenReg, lenName))
		return nil
	}

	lit, ok := v.Init.(*ast.ArrayLiteral)
	if !ok {
		return fmt.Errorf("%d:%d: array variable must be initialized with an array literal or a function returning an array", v.GetPos().Line, v.GetPos().Col)
	}

	// Check for spread elements — requires runtime length computation.
	hasSpread := false
	for _, elem := range lit.Elements {
		if _, ok := elem.(*ast.SpreadElement); ok {
			hasSpread = true
			break
		}
	}
	if hasSpread {
		return e.emitSpreadArrayLit(lit, ptrName, lenName, elemTy)
	}

	n := int64(len(lit.Elements))
	e.ensureMalloc()
	dataReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", dataReg, n*int64(elemTy.Align())))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", dataReg, ptrName))
	e.emitInstr(fmt.Sprintf("store i64 %d, ptr %s, align 8", n, lenName))

	for i, elem := range lit.Elements {
		val, err := e.emitExpr(elem)
		if err != nil {
			return err
		}
		val = e.coerce(val, elemTy)
		gepReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %d", gepReg, elemTy.IR, dataReg, i))
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", elemTy.IR, val.Ref, gepReg, elemTy.Align()))
	}
	return nil
}

// emitSpreadArrayLit handles array literals that contain one or more spread elements.
// It computes total length at runtime, allocates one contiguous buffer, and fills it
// using a write cursor: memcpy per spread, store per static element.
func (e *Emitter) emitSpreadArrayLit(lit *ast.ArrayLiteral, ptrName, lenName string, elemTy Type) error {
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
			return fmt.Errorf("%d:%d: spread element must be an array variable", sp.GetPos().Line, sp.GetPos().Col)
		}
		sym, found := e.lookup(spId.Name)
		if !found || !sym.Ty.IsArray {
			return fmt.Errorf("%d:%d: '%s' is not an array", sp.GetPos().Line, sp.GetPos().Col, spId.Name)
		}
		lenReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", lenReg, sym.LenPtr))
		newTotal := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = add i64 %s, %s", newTotal, totalReg, lenReg))
		totalReg = newTotal
	}

	// Allocate the buffer.
	e.ensureMalloc()
	bytesReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %d", bytesReg, totalReg, elemTy.Align()))
	dataReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", dataReg, bytesReg))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", dataReg, ptrName))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", totalReg, lenName))

	// Write cursor.
	cursorPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", cursorPtr))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", cursorPtr))

	for _, elem := range lit.Elements {
		if sp, ok := elem.(*ast.SpreadElement); ok {
			spId := sp.Arg.(*ast.Identifier) // already validated above
			sym, _ := e.lookup(spId.Name)
			// Load source ptr and length.
			srcPtr := e.freshReg()
			srcLen := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", srcPtr, sym.Ptr))
			e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", srcLen, sym.LenPtr))
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
			val, err := e.emitExpr(elem)
			if err != nil {
				return err
			}
			val = e.coerce(val, elemTy)
			cVal := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", cVal, cursorPtr))
			gepReg := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", gepReg, elemTy.IR, dataReg, cVal))
			e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", elemTy.IR, val.Ref, gepReg, elemTy.Align()))
			newC := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", newC, cVal))
			e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", newC, cursorPtr))
		}
	}
	return nil
}


func (e *Emitter) emitArrayDestructuring(s *ast.ArrayDestructuring) error {
	dataPtr, elemTy, err := e.resolveArrayDataPtr(s.Init, s.GetPos())
	if err != nil {
		return err
	}
	for i, name := range s.Names {
		if name == "" {
			continue
		}
		gepReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %d", gepReg, elemTy.IR, dataPtr, i))
		valReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", valReg, elemTy.IR, gepReg, elemTy.Align()))
		localPtr := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", localPtr, elemTy.IR, elemTy.Align()))
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", elemTy.IR, valReg, localPtr, elemTy.Align()))
		e.define(name, Symbol{Ptr: localPtr, Ty: elemTy})
	}
	return nil
}

// resolveArrayDataPtr emits code to obtain the raw heap pointer for an array
// expression. Handles identifiers, function calls, and array literals.
func (e *Emitter) resolveArrayDataPtr(init ast.Expression, pos ast.Pos) (string, Type, error) {
	switch src := init.(type) {
	case *ast.Identifier:
		sym, found := e.lookup(src.Name)
		if !found || !sym.Ty.IsArray {
			return "", Type{}, fmt.Errorf("%d:%d: '%s' is not an array", pos.Line, pos.Col, src.Name)
		}
		dataPtr := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", dataPtr, sym.Ptr))
		return dataPtr, *sym.Ty.ElemType, nil

	case *ast.CallExpression:
		val, err := e.emitExpr(src)
		if err != nil {
			return "", Type{}, err
		}
		if !val.Ty.IsArray {
			return "", Type{}, fmt.Errorf("%d:%d: function call does not return an array", pos.Line, pos.Col)
		}
		ptrReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", ptrReg, val.Ref))
		return ptrReg, *val.Ty.ElemType, nil

	case *ast.ArrayLiteral:
		ty := e.inferArrayType(src)
		elemTy := *ty.ElemType
		n := int64(len(src.Elements))
		e.ensureMalloc()
		dataReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", dataReg, n*int64(elemTy.Align())))
		for i, elem := range src.Elements {
			val, err := e.emitExpr(elem)
			if err != nil {
				return "", Type{}, err
			}
			val = e.coerce(val, elemTy)
			gepReg := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %d", gepReg, elemTy.IR, dataReg, i))
			e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", elemTy.IR, val.Ref, gepReg, elemTy.Align()))
		}
		return dataReg, elemTy, nil
	}
	return "", Type{}, fmt.Errorf("%d:%d: array destructuring requires an array variable, function call, or array literal", pos.Line, pos.Col)
}


func (e *Emitter) emitPop(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 0 {
		return Value{}, fmt.Errorf("%d:%d: pop takes no arguments", pos.Line, pos.Col)
	}
	id, ok := mem.Object.(*ast.Identifier)
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: pop requires an array variable", pos.Line, pos.Col)
	}
	sym, ok := e.lookup(id.Name)
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: undefined variable '%s'", pos.Line, pos.Col, id.Name)
	}
	if !sym.Ty.IsArray {
		return Value{}, fmt.Errorf("%d:%d: '%s' is not an array", pos.Line, pos.Col, id.Name)
	}
	elemTy := *sym.Ty.ElemType

	curPtr := e.freshReg()
	curLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", curPtr, sym.Ptr))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", curLen, sym.LenPtr))

	newLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, 1", newLen, curLen))

	slot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", slot, elemTy.IR, curPtr, newLen))
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", result, elemTy.IR, slot, elemTy.Align()))

	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", newLen, sym.LenPtr))

	return Value{Ref: result, Ty: elemTy}, nil
}

// emitSplice implements arr.splice(start, deleteCount): removes deleteCount
// elements at start, returns them as a new array, shifts the tail left.
func (e *Emitter) emitSplice(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 2 {
		return Value{}, fmt.Errorf("%d:%d: splice takes exactly two arguments (start, deleteCount)", pos.Line, pos.Col)
	}
	id, ok := mem.Object.(*ast.Identifier)
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: splice requires an array variable", pos.Line, pos.Col)
	}
	sym, ok := e.lookup(id.Name)
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: undefined variable '%s'", pos.Line, pos.Col, id.Name)
	}
	if !sym.Ty.IsArray {
		return Value{}, fmt.Errorf("%d:%d: '%s' is not an array", pos.Line, pos.Col, id.Name)
	}
	elemTy := *sym.Ty.ElemType

	startVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	startVal = e.coerce(startVal, TypeI64)

	delCount, err := e.emitExpr(args[1])
	if err != nil {
		return Value{}, err
	}
	delCount = e.coerce(delCount, TypeI64)

	curPtr := e.freshReg()
	curLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", curPtr, sym.Ptr))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", curLen, sym.LenPtr))

	// Allocate result array and copy the removed slice into it.
	e.ensureCalloc()
	resultPtr := e.freshReg()
	copyBytes := e.freshReg()
	srcPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @calloc(i64 %s, i64 %d)", resultPtr, delCount.Ref, elemTy.Align()))
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", srcPtr, elemTy.IR, curPtr, startVal.Ref))
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %d", copyBytes, delCount.Ref, elemTy.Align()))
	e.ensureMemmove()
	e.emitInstr(fmt.Sprintf("call ptr @memmove(ptr %s, ptr %s, i64 %s)", resultPtr, srcPtr, copyBytes))

	// Shift the tail left: memmove(ptr+start, ptr+start+deleteCount, remaining*elemSize).
	startPlusDel := e.freshReg()
	tailSrc := e.freshReg()
	tailDst := e.freshReg()
	remaining := e.freshReg()
	shiftBytes := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, %s", startPlusDel, startVal.Ref, delCount.Ref))
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", tailSrc, elemTy.IR, curPtr, startPlusDel))
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", tailDst, elemTy.IR, curPtr, startVal.Ref))
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", remaining, curLen, startPlusDel))
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %d", shiftBytes, remaining, elemTy.Align()))
	e.emitInstr(fmt.Sprintf("call ptr @memmove(ptr %s, ptr %s, i64 %s)", tailDst, tailSrc, shiftBytes))

	// Update length.
	newLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", newLen, curLen, delCount.Ref))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", newLen, sym.LenPtr))

	// Pack result into {ptr, i64} aggregate.
	r0 := e.freshReg()
	r1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", r0, resultPtr))
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %s, 1", r1, r0, delCount.Ref))

	return Value{Ref: r1, Ty: sym.Ty}, nil
}

// emitShift implements arr.shift(): save ptr[0], memmove left, decrement len.
func (e *Emitter) emitShift(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 0 {
		return Value{}, fmt.Errorf("%d:%d: shift takes no arguments", pos.Line, pos.Col)
	}
	id, ok := mem.Object.(*ast.Identifier)
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: shift requires an array variable", pos.Line, pos.Col)
	}
	sym, ok := e.lookup(id.Name)
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: undefined variable '%s'", pos.Line, pos.Col, id.Name)
	}
	if !sym.Ty.IsArray {
		return Value{}, fmt.Errorf("%d:%d: '%s' is not an array", pos.Line, pos.Col, id.Name)
	}
	elemTy := *sym.Ty.ElemType

	curPtr := e.freshReg()
	curLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", curPtr, sym.Ptr))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", curLen, sym.LenPtr))

	// save first element before moving
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", result, elemTy.IR, curPtr, elemTy.Align()))

	newLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, 1", newLen, curLen))

	// src = ptr + 1 element; move (len-1) elements left
	src := e.freshReg()
	moveBytes := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 1", src, elemTy.IR, curPtr))
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %d", moveBytes, newLen, elemTy.Align()))
	e.ensureMemmove()
	e.emitInstr(fmt.Sprintf("call ptr @memmove(ptr %s, ptr %s, i64 %s)", curPtr, src, moveBytes))

	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", newLen, sym.LenPtr))

	return Value{Ref: result, Ty: elemTy}, nil
}

// emitUnshift implements arr.unshift(val): realloc, memmove right, write at [0], increment len.
// Returns the new length (matching JS semantics).
func (e *Emitter) emitUnshift(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: unshift takes exactly one argument", pos.Line, pos.Col)
	}
	id, ok := mem.Object.(*ast.Identifier)
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: unshift requires an array variable", pos.Line, pos.Col)
	}
	sym, ok := e.lookup(id.Name)
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: undefined variable '%s'", pos.Line, pos.Col, id.Name)
	}
	if !sym.Ty.IsArray {
		return Value{}, fmt.Errorf("%d:%d: '%s' is not an array", pos.Line, pos.Col, id.Name)
	}
	elemTy := *sym.Ty.ElemType

	val, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	val = e.coerce(val, elemTy)

	curPtr := e.freshReg()
	curLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", curPtr, sym.Ptr))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", curLen, sym.LenPtr))

	newLen := e.freshReg()
	newBytes := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", newLen, curLen))
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %d", newBytes, newLen, elemTy.Align()))

	e.ensureRealloc()
	newPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @realloc(ptr %s, i64 %s)", newPtr, curPtr, newBytes))

	// dst = newPtr + 1 element; move existing elements right
	dst := e.freshReg()
	moveBytes := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 1", dst, elemTy.IR, newPtr))
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %d", moveBytes, curLen, elemTy.Align()))
	e.ensureMemmove()
	e.emitInstr(fmt.Sprintf("call ptr @memmove(ptr %s, ptr %s, i64 %s)", dst, newPtr, moveBytes))

	// write new element at index 0
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", elemTy.IR, val.Ref, newPtr, elemTy.Align()))

	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", newPtr, sym.Ptr))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", newLen, sym.LenPtr))

	return Value{Ref: newLen, Ty: TypeI64}, nil
}

// emitPush implements arr.push(val): realloc, store at [len], update ptr+len.
// Returns the new length (i64), matching JS semantics.
func (e *Emitter) emitPush(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: push takes exactly one argument", pos.Line, pos.Col)
	}
	id, ok := mem.Object.(*ast.Identifier)
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: push requires an array variable", pos.Line, pos.Col)
	}
	sym, ok := e.lookup(id.Name)
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: undefined variable '%s'", pos.Line, pos.Col, id.Name)
	}
	if !sym.Ty.IsArray {
		return Value{}, fmt.Errorf("%d:%d: '%s' is not an array", pos.Line, pos.Col, id.Name)
	}
	elemTy := *sym.Ty.ElemType

	val, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	val = e.coerce(val, elemTy)

	curPtr := e.freshReg()
	curLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", curPtr, sym.Ptr))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", curLen, sym.LenPtr))

	newLen := e.freshReg()
	newBytes := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", newLen, curLen))
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %d", newBytes, newLen, elemTy.Align()))

	e.ensureRealloc()
	newPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @realloc(ptr %s, i64 %s)", newPtr, curPtr, newBytes))

	slot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", slot, elemTy.IR, newPtr, curLen))
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", elemTy.IR, val.Ref, slot, elemTy.Align()))

	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", newPtr, sym.Ptr))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", newLen, sym.LenPtr))

	return Value{Ref: newLen, Ty: TypeI64}, nil
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
		ptrReg = e.freshReg()
		lenReg = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", ptrReg, sym.Ptr))
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", lenReg, sym.LenPtr))
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
func (e *Emitter) emitArrayMap(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: map takes exactly 1 argument", pos.Line, pos.Col)
	}
	ptrReg, lenReg, elemTy, err := e.resolveArrayForHOF(mem.Object, pos)
	if err != nil {
		return Value{}, err
	}
	cb, err := e.resolveCallback(args[0])
	if err != nil {
		return Value{}, err
	}
	retElemTy := cb.retType()
	if retElemTy.IR == "void" || retElemTy.IR == "" {
		return Value{}, fmt.Errorf("%d:%d: map callback must return a value", pos.Line, pos.Col)
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
	inElem := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", inGep, elemTy.IR, ptrReg, idxVal))
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", inElem, elemTy.IR, inGep, elemTy.Align()))

	cbArgs := []Value{{Ref: inElem, Ty: elemTy}}
	if cb.arity() >= 2 {
		cbArgs = append(cbArgs, Value{Ref: idxVal, Ty: TypeI64})
	}
	resultVal, err := e.emitCBCall(cb, cbArgs)
	if err != nil {
		return Value{}, err
	}

	outGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", outGep, retElemTy.IR, outPtr, idxVal))
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", retElemTy.IR, resultVal.Ref, outGep, retElemTy.Align()))

	idxNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", idxNext, idxVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", idxNext, idxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(doneL)
	r0 := e.freshReg()
	r1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", r0, outPtr))
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %s, 1", r1, r0, lenReg))
	return Value{Ref: r1, Ty: ArrayOf(retElemTy)}, nil
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
	cb, err := e.resolveCallback(args[0])
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
	inElem := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", inGep, elemTy.IR, ptrReg, idxVal))
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", inElem, elemTy.IR, inGep, elemTy.Align()))

	cbArgs := []Value{{Ref: inElem, Ty: elemTy}}
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
	cb, err := e.resolveCallback(args[0])
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
	inElem := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", inGep, elemTy.IR, ptrReg, idxVal))
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", inElem, elemTy.IR, inGep, elemTy.Align()))

	cbArgs := []Value{{Ref: inElem, Ty: elemTy}}
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
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", elemTy.IR, inElem, outGep, elemTy.Align()))
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
	cb, err := e.resolveCallback(args[0])
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
	inElem := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", inGep, elemTy.IR, ptrReg, idxVal))
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", inElem, elemTy.IR, inGep, elemTy.Align()))

	newAcc, err := e.emitCBCall(cb, []Value{{Ref: accCur, Ty: accTy}, {Ref: inElem, Ty: elemTy}})
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
	cb, err := e.resolveCallback(args[0])
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
	inElem := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", inGep, elemTy.IR, ptrReg, idxVal))
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", inElem, elemTy.IR, inGep, elemTy.Align()))
	predVal, err := e.emitCBCall(cb, []Value{{Ref: inElem, Ty: elemTy}})
	if err != nil {
		return Value{}, err
	}
	boolVal := e.emitToBool(predVal)
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", boolVal.Ref, matchL, incL))

	e.emitLabel(matchL)
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", elemTy.IR, inElem, foundAlloca, elemTy.Align()))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))

	e.emitLabel(incL)
	idxNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", idxNext, idxVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", idxNext, idxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(doneL)
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
	cb, err := e.resolveCallback(args[0])
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
	inElem := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", inGep, elemTy.IR, ptrReg, idxVal))
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", inElem, elemTy.IR, inGep, elemTy.Align()))
	predVal, err := e.emitCBCall(cb, []Value{{Ref: inElem, Ty: elemTy}})
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
	cb, err := e.resolveCallback(args[0])
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
	inElem := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", inGep, elemTy.IR, ptrReg, idxVal))
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", inElem, elemTy.IR, inGep, elemTy.Align()))
	predVal, err := e.emitCBCall(cb, []Value{{Ref: inElem, Ty: elemTy}})
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
func (e *Emitter) emitArrayJoin(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) > 1 {
		return Value{}, fmt.Errorf("%d:%d: join takes 0 or 1 arguments", pos.Line, pos.Col)
	}
	ptrReg, lenReg, elemTy, err := e.resolveArrayForHOF(mem.Object, pos)
	if err != nil {
		return Value{}, err
	}

	var sepVal Value
	if len(args) == 0 {
		sepVal = Value{Ref: e.internString(","), Ty: TypePtr}
	} else {
		sepVal, err = e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
	}

	emptyStr := e.internString("")
	accAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", accAlloca))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", emptyStr, accAlloca))

	idxAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idxAlloca))

	condL := e.freshLabel("join.cond")
	bodyL := e.freshLabel("join.body")
	firstL := e.freshLabel("join.first")
	restL := e.freshLabel("join.rest")
	incL := e.freshLabel("join.inc")
	doneL := e.freshLabel("join.done")

	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	idxVal := e.freshReg()
	done := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idxVal, idxAlloca))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", done, idxVal, lenReg))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", done, doneL, bodyL))

	e.emitLabel(bodyL)
	inGep := e.freshReg()
	inElem := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", inGep, elemTy.IR, ptrReg, idxVal))
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", inElem, elemTy.IR, inGep, elemTy.Align()))
	elemStrVal, err := e.emitValueToString(Value{Ref: inElem, Ty: elemTy})
	if err != nil {
		return Value{}, err
	}
	isFirst := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", isFirst, idxVal))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isFirst, firstL, restL))

	e.emitLabel(firstL)
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", elemStrVal.Ref, accAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", incL))

	e.emitLabel(restL)
	accCur := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", accCur, accAlloca))
	withSep, err := e.emitStringConcat(Value{Ref: accCur, Ty: TypePtr}, sepVal)
	if err != nil {
		return Value{}, err
	}
	newAcc, err := e.emitStringConcat(withSep, elemStrVal)
	if err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", newAcc.Ref, accAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", incL))

	e.emitLabel(incL)
	idxNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", idxNext, idxVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", idxNext, idxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(doneL)
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", result, accAlloca))
	return Value{Ref: result, Ty: TypePtr}, nil
}

// emitArraySort implements arr.sort() and arr.sort(compareFn).
// Sorts in-place using qsort and returns the same array (ptr+len aggregate).
func (e *Emitter) emitArraySort(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) > 1 {
		return Value{}, fmt.Errorf("%d:%d: sort takes 0 or 1 arguments", pos.Line, pos.Col)
	}

	ptrReg, lenReg, elemTy, err := e.resolveArrayForHOF(mem.Object, pos)
	if err != nil {
		return Value{}, err
	}

	e.ensureQsort()

	var cmpFnRef string

	if len(args) == 0 {
		// Default comparators based on element type
		switch {
		case elemTy.Float:
			e.ensureSortCmpF64()
			cmpFnRef = "@__kml_cmp_f64"
		case isStringTy(elemTy):
			e.ensureSortCmpStr()
			cmpFnRef = "@__kml_cmp_str"
		default:
			e.ensureSortCmpI64()
			cmpFnRef = "@__kml_cmp_i64"
		}
	} else {
		// Custom comparator: resolve closure and store in global, use trampoline
		cb, err2 := e.resolveCallbackWithHints(args[0], []Type{elemTy, elemTy})
		if err2 != nil {
			return Value{}, err2
		}
		if cb.kind != cbClosure {
			return Value{}, fmt.Errorf("%d:%d: sort comparator must be an arrow function or closure", pos.Line, pos.Col)
		}

		e.ensureSortClosGlobal()
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr @__kml_sort_clos, align 8", cb.hdrPtr))

		switch {
		case elemTy.Float:
			e.ensureSortTrampolineF64()
			cmpFnRef = "@__kml_sort_tramp_f64"
		case isStringTy(elemTy):
			e.ensureSortTrampolineStr()
			cmpFnRef = "@__kml_sort_tramp_str"
		default:
			e.ensureSortTrampolineI64()
			cmpFnRef = "@__kml_sort_tramp_i64"
		}
	}

	elemSize := int64(8)
	if elemTy.IR == "i1" {
		elemSize = 1
	} else if elemTy.IR == "i8" {
		elemSize = 1
	} else if elemTy.IR == "i16" {
		elemSize = 2
	} else if elemTy.IR == "i32" {
		elemSize = 4
	}

	e.emitInstr(fmt.Sprintf("call void @qsort(ptr %s, i64 %s, i64 %d, ptr %s)", ptrReg, lenReg, elemSize, cmpFnRef))

	// Return the array as an aggregate (same ptr+len as input)
	r0 := e.freshReg()
	r1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", r0, ptrReg))
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %s, 1", r1, r0, lenReg))
	retTy := ArrayOf(elemTy)
	return Value{Ref: r1, Ty: retTy}, nil
}

// emitArraySlice implements arr.slice(start[, end]): returns a new array
// containing elements from start up to (but not including) end.
// Negative indices count from the end; both are clamped to [0, len].
func (e *Emitter) emitArraySlice(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 1 || len(args) > 2 {
		return Value{}, fmt.Errorf("%d:%d: slice takes 1 or 2 arguments", pos.Line, pos.Col)
	}
	ptrReg, lenReg, elemTy, err := e.resolveArrayForHOF(mem.Object, pos)
	if err != nil {
		return Value{}, err
	}
	e.ensureMalloc()
	e.ensureMemcpy()

	startRaw, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	startN := e.emitNormalizeSliceIdx(e.coerce(startRaw, TypeI64).Ref, lenReg)

	var endN string
	if len(args) == 2 {
		endRaw, err := e.emitExpr(args[1])
		if err != nil {
			return Value{}, err
		}
		endN = e.emitNormalizeSliceIdx(e.coerce(endRaw, TypeI64).Ref, lenReg)
	} else {
		endN = lenReg
	}

	rawLen := e.freshReg()
	isNegLen := e.freshReg()
	sliceLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", rawLen, endN, startN))
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 0", isNegLen, rawLen))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 0, i64 %s", sliceLen, isNegLen, rawLen))

	byteCount := e.freshReg()
	newPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %d", byteCount, sliceLen, elemTy.Align()))
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", newPtr, byteCount))

	srcGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", srcGep, elemTy.IR, ptrReg, startN))
	e.emitInstr(fmt.Sprintf("call ptr @memcpy(ptr %s, ptr %s, i64 %s)", newPtr, srcGep, byteCount))

	r0 := e.freshReg()
	r1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", r0, newPtr))
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %s, 1", r1, r0, sliceLen))
	return Value{Ref: r1, Ty: ArrayOf(elemTy)}, nil
}

// emitElemEq emits an i1 register for (a == b) where a and b have type elemTy.
// Strings use strcmp; floats use fcmp oeq; all other types use icmp eq.
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

// emitArrayIndexOf implements arr.indexOf(val): returns the index of the first
// element equal to val, or -1 if not found.
func (e *Emitter) emitArrayIndexOf(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: indexOf takes exactly 1 argument", pos.Line, pos.Col)
	}
	ptrReg, lenReg, elemTy, err := e.resolveArrayForHOF(mem.Object, pos)
	if err != nil {
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
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idxAlloca))

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
	return Value{Ref: result, Ty: TypeI64}, nil
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
	cb, err := e.resolveCallback(args[0])
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
	elem := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", gep, elemTy.IR, ptrReg, idxVal))
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", elem, elemTy.IR, gep, elemTy.Align()))
	cbArgs := []Value{{Ref: elem, Ty: elemTy}}
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
	return Value{Ref: result, Ty: TypeI64}, nil
}

// emitArrayConcat implements arr.concat(other): returns a new array containing
// all elements of arr followed by all elements of other.
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
	r0 := e.freshReg()
	r1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", r0, ptrReg))
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
