package llvm

import (
	"KlainMainLang/ast"
	"fmt"
)

func (e *Emitter) emitArrayVarDecl(v *ast.VarDeclaration, ty Type) error {
	elemTy := *ty.ElemType
	ptrName := e.freshReg()
	lenName := e.freshReg()

	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", ptrName))
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", lenName))
	e.define(v.Name, Symbol{Ptr: ptrName, LenPtr: lenName, Ty: ty, IsConst: v.Kind == "const"})

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

// emitArrayIndexOf implements arr.indexOf(val): returns the index of the first
// element equal to val, or -1 if not found.
