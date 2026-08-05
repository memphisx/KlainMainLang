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
		if !found || !sym.Ty.IsArray {
			return "", "", fmt.Errorf("%d:%d: '%s' is not an array", sp.GetPos().Line, sp.GetPos().Col, spId.Name)
		}
		spLenReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", spLenReg, sym.LenPtr))
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
			val, verr := e.emitExprWithObjectHint(elem, elemTy)
			if verr != nil {
				return "", "", verr
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
	return dataReg, totalReg, nil
}

// emitArrayLiteralData builds a non-spread array literal's malloc'd,
// populated backing buffer, returning the data pointer and static element
// count — shared by every non-spread array-literal producer (var-decl
// allocas, emitArrayLiteralAggregate's general-expression path, array
// destructuring) so there's exactly one implementation of "malloc N *
// elemSize, store each element" rather than one per caller.
func (e *Emitter) emitArrayLiteralData(lit *ast.ArrayLiteral, elemTy Type) (dataReg string, n int64, err error) {
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
		gepReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %d", gepReg, elemTy.IR, dataReg, i))
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", elemTy.IR, val.Ref, gepReg, elemTy.Align()))
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
	// literal, etc.) is a real, separate gap, not something TDD-00028's own
	// fix happens to unblock: an array's backing buffer is a flat sequence
	// of fixed-width, elemTy-sized slots (see emitArrayLiteralData), sized
	// for a scalar/pointer element — but a nested array's own value is a
	// {ptr, i64} *pair*, which doesn't fit in one such slot without a
	// boxing/indirection layer this compiler doesn't have (every array
	// read/write/HOF/.length call site throughout emit_arrays_*.go would
	// need to know about it). Rejected here with a clear, deliberate error
	// instead of silently reaching a confusing clang-stage type-mismatch a
	// few instructions later (found exactly that way while implementing
	// this fix: `store i64 %aggregateReg, ptr %slot` — a 16-byte {ptr,i64}
	// value forced into an 8-byte slot). Real array-of-arrays support is
	// its own follow-up design question, not scoped here.
	if elemTy.IsArray {
		return Value{}, fmt.Errorf("%d:%d: nested arrays (array-of-arrays) are not yet supported as a value — see docs/tdd/TDD-00028.md", lit.GetPos().Line, lit.GetPos().Col)
	}

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
		elemTy := *e.inferArrayType(src).ElemType
		dataReg, _, err := e.emitArrayLiteralData(src, elemTy)
		if err != nil {
			return "", Type{}, err
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
