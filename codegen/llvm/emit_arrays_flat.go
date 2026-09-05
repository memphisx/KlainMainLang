// emit_arrays_flat.go — `/** @value */` flat value-type arrays (TDD-00134
// Stage 2). A flat array packs its fixed-shape object elements INLINE in one
// contiguous buffer (AoS, stride = StructSize) instead of the default
// array-of-pointers layout — cache-friendly, vectorizable, one allocation.
//
// This is an explicit aliasing-semantics opt-in, never inferred: writing a
// value into a slot COPIES its fields (later mutation of the source is not
// seen through the array), where a plain object array shares the pointer.
// Reading `arr[i]` yields an interior pointer (a view), so `arr[i].x = 1`
// and `const p = arr[i]; p.x` behave like the reference model — but a view
// is invalidated by a growth realloc (`push`), the documented Vec/C++-vector
// contract.
//
// The type deliberately is NOT IsArray (Type.IsFlatArray): every ptr-slot
// array operation (HOFs, sort, slice, spread, destructuring, …) rejects a
// flat array instead of silently corrupting the buffer. The supported V1
// surface has explicit branches: construction from an array literal, index
// read/write, `.length`, `for...of`, and `.push`. Storage mirrors a named
// array — a stable slot pointing at a shared {data,len} header — so
// `arrayDataLenSlots` works unchanged.
package llvm

import (
	"fmt"

	"KlainMainLang/ast"
)

// flatElemView returns elemTy marked Inline — the element type emitIndexPtr
// hands back for a flat array, telling loadArrayElem/storeArrayElem the slot
// holds the struct itself, not a pointer to it.
func flatElemView(elemTy Type) Type {
	elemTy.Inline = true
	return elemTy
}

// flatArrayElemOK reports whether elemTy may be a flat-array element: a
// fixed-shape plain object (interface/object-literal shape). Classes (hidden
// tag/vtable/emitter fields and constructor identity), tuples, nested
// arrays, and dynamic values stay on the default pointer layout.
func flatArrayElemOK(elemTy Type) bool {
	return elemTy.IsObject && !elemTy.IsClass && !elemTy.IsTuple &&
		!elemTy.IsDynamic && !elemTy.IsDynamicObject && !elemTy.IsArray
}

// emitFlatArrayVarDecl builds a `/** @value */` array binding from its
// literal initializer: one malloc of n*StructSize, each element written in
// place via memcpy from the emitted element value.
func (e *Emitter) emitFlatArrayVarDecl(v *ast.VarDeclaration, declaredTy Type) error {
	pos := v.GetPos()
	if !declaredTy.IsArray || declaredTy.ElemType == nil {
		return fmt.Errorf("%d:%d: @value applies to an array declaration (e.g. `/** @value */ const ps: Point[] = [...]`)", pos.Line, pos.Col)
	}
	elemTy := *declaredTy.ElemType
	if !flatArrayElemOK(elemTy) {
		return fmt.Errorf("%d:%d: @value requires a fixed-shape object element type (an interface/object shape) — class instances, tuples, nested arrays, and dynamic values keep the default layout", pos.Line, pos.Col)
	}
	if e.promotedGlobalDecls[v] {
		return fmt.Errorf("%d:%d: a @value array read by other functions is not supported yet — declare it inside the function that uses it", pos.Line, pos.Col)
	}
	lit, ok := v.Init.(*ast.ArrayLiteral)
	if !ok {
		return fmt.Errorf("%d:%d: a @value array must be initialized with an array literal", pos.Line, pos.Col)
	}
	for _, el := range lit.Elements {
		if _, isSpread := el.(*ast.SpreadElement); isSpread {
			return fmt.Errorf("%d:%d: a spread element is not supported in a @value array literal", pos.Line, pos.Col)
		}
	}

	size := elemTy.StructSize()
	n := int64(len(lit.Elements))
	e.ensureMalloc()
	e.ensureMemcpy()
	dataReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", dataReg, maxInt64(n*size, 1)))
	structIR := elemTy.StructIR()
	for i, el := range lit.Elements {
		val, err := e.emitExprWithObjectHint(el, elemTy)
		if err != nil {
			return err
		}
		if !val.Ty.IsObject {
			return fmt.Errorf("%d:%d: @value array element %d is not an object value", pos.Line, pos.Col, i)
		}
		slot := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %d", slot, structIR, dataReg, i))
		e.emitInstr(fmt.Sprintf("call void @memcpy(ptr %s, ptr %s, i64 %d)", slot, val.Ref, size))
	}

	flatTy := FlatArrayType(elemTy)
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
	hdr := e.newArrayHeader(dataReg, fmt.Sprintf("%d", n))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", hdr, slot))
	e.define(v.Name, Symbol{Ptr: slot, Ty: flatTy, IsConst: v.Kind == "const"})
	return nil
}

// emitForOfFlatArray iterates a flat array, binding each element's interior
// pointer (a view — mutating the binding's fields mutates the array).
func (e *Emitter) emitForOfFlatArray(s *ast.ForOfStatement, arrTy Type, condL, bodyL, incL, endL string) error {
	if s.VarName == "" {
		return fmt.Errorf("%d:%d: destructuring a @value array element in for...of is not supported — bind a name and read its fields", s.GetPos().Line, s.GetPos().Col)
	}
	elemTy := *arrTy.ElemType
	sym, found := e.lookup(mustIdentName(s.Iterable))
	if !found {
		return fmt.Errorf("%d:%d: undefined variable", s.GetPos().Line, s.GetPos().Col)
	}
	dataSlot, lenSlot := e.arrayDataLenSlots(sym)

	idxPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxPtr))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idxPtr))
	elemSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", elemSlot))
	e.define(s.VarName, Symbol{Ptr: elemSlot, Ty: elemTy, IsConst: s.Kind == "const"})

	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	e.emitSafepoint()
	idxV, lenV, cond := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idxV, idxPtr))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", lenV, lenSlot))
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, %s", cond, idxV, lenV))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", cond, bodyL, endL))

	e.emitLabel(bodyL)
	idx2, data, gep := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idx2, idxPtr))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", data, dataSlot))
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", gep, elemTy.StructIR(), data, idx2))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", gep, elemSlot))
	if err := e.emitStmt(s.Body); err != nil {
		return err
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", incL))

	e.emitLabel(incL)
	idx3, idx4 := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idx3, idxPtr))
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", idx4, idx3))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", idx4, idxPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(endL)
	return nil
}

// emitFlatArrayPush implements push on a flat array: realloc to the new
// element count (stride = StructSize) and memcpy each pushed value's fields
// into its slot. Growth may move the buffer, so any previously-taken element
// view (an `arr[i]` binding) is invalidated — the documented contract.
func (e *Emitter) emitFlatArrayPush(arrTy Type, mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	elemTy := *arrTy.ElemType
	id, ok := mem.Object.(*ast.Identifier)
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: push on a @value array requires a named array variable", pos.Line, pos.Col)
	}
	sym, found := e.lookup(id.Name)
	if !found {
		return Value{}, fmt.Errorf("%d:%d: undefined variable '%s'", pos.Line, pos.Col, id.Name)
	}
	dataSlot, lenSlot := e.arrayDataLenSlots(sym)
	size := elemTy.StructSize()
	structIR := elemTy.StructIR()

	vals := make([]Value, 0, len(args))
	for _, arg := range args {
		v, err := e.emitExprWithObjectHint(arg, elemTy)
		if err != nil {
			return Value{}, err
		}
		if !v.Ty.IsObject {
			return Value{}, fmt.Errorf("%d:%d: push on a @value array takes object values of its element type", pos.Line, pos.Col)
		}
		vals = append(vals, v)
	}

	curPtr, curLen := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", curPtr, dataSlot))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", curLen, lenSlot))
	if len(vals) == 0 {
		return e.countToNumber(Value{Ref: curLen, Ty: TypeI64}), nil
	}

	e.ensureRealloc()
	e.ensureMemcpy()
	newLen, newBytes, newPtr := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, %d", newLen, curLen, len(vals)))
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %d", newBytes, newLen, size))
	e.emitInstr(fmt.Sprintf("%s = call ptr @realloc(ptr %s, i64 %s)", newPtr, curPtr, newBytes))
	for i, v := range vals {
		slotIdx, slot := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = add i64 %s, %d", slotIdx, curLen, i))
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", slot, structIR, newPtr, slotIdx))
		e.emitInstr(fmt.Sprintf("call void @memcpy(ptr %s, ptr %s, i64 %d)", slot, v.Ref, size))
	}
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", newPtr, dataSlot))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", newLen, lenSlot))
	return e.countToNumber(Value{Ref: newLen, Ty: TypeI64}), nil
}

func mustIdentName(ex ast.Expression) string {
	if id, ok := ex.(*ast.Identifier); ok {
		return id.Name
	}
	return ""
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
