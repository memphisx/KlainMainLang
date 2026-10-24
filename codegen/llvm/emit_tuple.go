// emit_tuple.go — tuple types `[T0, T1, ...]` (TDD-00066). A tuple is stored as
// a fixed-shape struct with synthetic positional field names "0","1",...
// (TupleType, types.go), so construction, element access, destructuring, and
// serialization all lean on the existing object/struct machinery; only the
// tuple-specific surface (a constant-index read, positional destructuring, and
// array-shaped rendering) lives here or is flagged via Type.IsTuple.
package llvm

import (
	"KlainMainLang/ast"
	"fmt"
	"strconv"
	"strings"
)

// emitTupleLiteral builds a tuple value from an array literal's element
// expressions against the declared tuple type — element i is stored into field
// "i". A nullable-scalar element is boxed and a null-valued source lvalue keeps
// its null-ness, exactly like an object-literal field (storeScalarOrNullableFieldExpr).
// lit is the source ArrayLiteral node (used only to match a stack-allocation
// plan under -optimize-memory, TDD-00134 Stage 1); nil is always safe.
func (e *Emitter) emitTupleLiteral(lit ast.Expression, elems []ast.Expression, ty Type) (Value, error) {
	if len(elems) != len(ty.Fields) {
		return Value{}, fmt.Errorf("a %d-tuple literal must have exactly %d elements, got %d", len(ty.Fields), len(ty.Fields), len(elems))
	}
	// Zeroing allocation (calloc, or alloca + zero store when stack-planned):
	// an object/tuple struct must read back deterministic zeros for any slot
	// a store doesn't fully cover — same reasoning as emitObjectLiteral
	// (ADR-00157).
	dataReg := e.structAlloc(lit, ty)
	structIR := ty.StructIR()
	for i, elemExpr := range elems {
		if _, isSpread := elemExpr.(*ast.SpreadElement); isSpread {
			return Value{}, fmt.Errorf("a spread element is not supported in a tuple literal")
		}
		fieldTy := ty.Fields[i].Ty
		gepReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gepReg, structIR, dataReg, i))
		if err := e.storeScalarOrNullableFieldExpr(gepReg, fieldTy, elemExpr); err != nil {
			return Value{}, err
		}
	}
	return Value{Ref: dataReg, Ty: ty}, nil
}

// unpackTuplePatternInto binds an array-destructuring pattern positionally
// against a tuple value at objPtr: pattern position i binds field "i". Unlike
// the array-source path (unpackArrayPatternInto), a tuple's arity is known at
// compile time and each position has its own type, so there is no runtime
// bounds check and no per-position default fallback — a pattern longer than the
// tuple is a compile-time error. Supports plain identifier targets, holes, and
// a nested sub-pattern on a tuple/object/array element; a rest element is a
// clean rejection in V1 (TDD-00066).
func (e *Emitter) unpackTuplePatternInto(objPtr string, tupleTy Type, elems []ast.ArrayPatternElem, pos ast.Pos) error {
	if len(elems) > len(tupleTy.Fields) {
		return fmt.Errorf("%d:%d: destructuring pattern has %d elements but the tuple has only %d", pos.Line, pos.Col, len(elems), len(tupleTy.Fields))
	}
	structIR := tupleTy.StructIR()
	for i, elem := range elems {
		if elem.Rest {
			return e.bindTupleRest(objPtr, tupleTy, i, elem, pos)
		}
		if elem.Name == "" && elem.SubArray == nil && elem.SubObject == nil {
			continue // hole
		}
		fieldTy := tupleTy.Fields[i].Ty
		gepReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gepReg, structIR, objPtr, i))

		if elem.SubArray != nil || elem.SubObject != nil {
			if err := e.unpackNestedTupleElem(gepReg, fieldTy, elem, pos); err != nil {
				return err
			}
			continue
		}

		if fieldTy.IsArray() {
			// An array element binds under the object-reference model (TDD-00127):
			// a stable slot holding the element's header, so its
			// .length/indexing/mutators work.
			val := e.loadScalarOrNullableField(gepReg, fieldTy) // {ptr,i64} aggregate
			slot := e.newArrayHeaderSlotFromAggregate(val)
			e.define(elem.Name, Symbol{Ptr: slot, Ty: fieldTy})
			continue
		}

		val := e.loadScalarOrNullableField(gepReg, fieldTy)
		slot := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", slot, StructFieldIR(fieldTy), fieldTy.Align()))
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", StructFieldIR(fieldTy), val.Ref, slot, fieldTy.Align()))
		e.define(elem.Name, Symbol{Ptr: slot, Ty: fieldTy})
	}
	return nil
}

// bindTupleRest binds `...rest` at pattern position from: a new array of
// the tuple's elements from there on, typed by their one element type when
// they share it, else as any[].
func (e *Emitter) bindTupleRest(objPtr string, tupleTy Type, from int, elem ast.ArrayPatternElem, pos ast.Pos) error {
	tmpSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", tmpSlot))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", objPtr, tmpSlot))
	tmp := "__kml_tuple_rest_" + strings.TrimPrefix(tmpSlot, "%")
	e.define(tmp, Symbol{Ptr: tmpSlot, Ty: tupleTy})
	var items []ast.Expression
	for j := from; j < len(tupleTy.Fields); j++ {
		items = append(items, ast.NewIndexExpression(ast.NewIdentifier(tmp, pos), ast.NewNumberLiteral(strconv.Itoa(j), pos), pos))
	}
	elemTy := tupleCommonElem(tupleTy, from)
	arrTy := ArrayOf(elemTy)
	val, err := e.emitExprWithObjectHint(ast.NewArrayLiteral(items, pos), arrTy)
	if err != nil {
		return err
	}
	val = e.convert(val, arrTy)
	return e.bindRestArray(e.newArrayHeaderSlotFromAggregate(val), e.arrayData(val), e.arrayLen(val), elemTy, elem)
}

// unpackNestedTupleElem destructures a nested sub-pattern against a tuple
// element that is itself a tuple/object (loaded by pointer) or an array.
func (e *Emitter) unpackNestedTupleElem(gepReg string, fieldTy Type, elem ast.ArrayPatternElem, pos ast.Pos) error {
	switch {
	case elem.SubArray != nil && fieldTy.IsTuple():
		sub := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", sub, gepReg))
		return e.unpackTuplePatternInto(sub, fieldTy, elem.SubArray, pos)
	case elem.SubArray != nil && fieldTy.IsArray():
		val := e.loadScalarOrNullableField(gepReg, fieldTy) // {ptr,i64}
		ptrReg := e.freshReg()
		lenReg := e.freshReg()
		ptrReg = e.arrayData(val)
		lenReg = e.arrayLen(val)
		elemTy := TypeI64
		if fieldTy.ElemType != nil {
			elemTy = *fieldTy.ElemType
		}
		return e.unpackArrayPatternInto(ptrReg, lenReg, elemTy, elem.SubArray)
	case elem.SubObject != nil && fieldTy.IsObject():
		sub := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", sub, gepReg))
		return e.unpackObjectPatternInto(sub, fieldTy, elem.SubObject, pos)
	default:
		return fmt.Errorf("%d:%d: nested destructuring pattern does not match the tuple element's type", pos.Line, pos.Col)
	}
}

// tupleConstIndex extracts a non-negative compile-time-constant integer index
// from a tuple index expression, or ok=false when the index isn't a plain
// integer literal (a tuple has no runtime array to index dynamically).
func tupleConstIndex(expr ast.Expression) (int64, bool) {
	nl, ok := expr.(*ast.NumberLiteral)
	if !ok || strings.ContainsRune(nl.Value, '.') {
		return 0, false
	}
	n, err := strconv.ParseInt(nl.Value, 0, 64)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

// emitTupleToString renders a tuple as its elements joined by commas —
// real JS's `String([a, b])` (and thus `${tuple}` interpolation and, here,
// console.log). An element is rendered by the ordinary value-to-string path
// (a nested string is not quoted, matching JS's own comma join).
func (e *Emitter) emitTupleToString(v Value) (Value, error) {
	acc := Value{Ref: e.internString(""), Ty: TypePtr}
	structIR := v.Ty.StructIR()
	for i, field := range v.Ty.Fields {
		gepReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gepReg, structIR, v.Ref, i))
		fieldVal := e.loadScalarOrNullableField(gepReg, field.Ty)
		if i > 0 {
			var err error
			if acc, err = e.emitStringConcat(acc, Value{Ref: e.internString(","), Ty: TypePtr}); err != nil {
				return Value{}, err
			}
		}
		elemStr, err := e.emitValueToString(fieldVal)
		if err != nil {
			return Value{}, err
		}
		if acc, err = e.emitStringConcat(acc, elemStr); err != nil {
			return Value{}, err
		}
	}
	return acc, nil
}

// emitTupleIndex emits `t[i]` for a constant index into a tuple — a field
// access on the underlying struct, since a tuple has no array backing buffer.
func (e *Emitter) emitTupleIndex(ex *ast.IndexExpression, tupleTy Type) (Value, error) {
	idx, ok := tupleConstIndex(ex.Index)
	if !ok {
		return e.emitTupleIndexDynamic(ex, tupleTy)
	}
	if idx >= int64(len(tupleTy.Fields)) {
		return Value{}, fmt.Errorf("%d:%d: tuple index %d is out of range (the tuple has %d element(s))", ex.GetPos().Line, ex.GetPos().Col, idx, len(tupleTy.Fields))
	}
	objVal, err := e.emitExpr(ex.Object)
	if err != nil {
		return Value{}, err
	}
	fieldTy := tupleTy.Fields[idx].Ty
	gepReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gepReg, tupleTy.StructIR(), objVal.Ref, idx))
	if fieldTy.IsArray() {
		// An array element's slot holds a header pointer (TDD-00213 Stage 2), the
		// array value itself.
		return e.loadArrayFieldValue(gepReg, fieldTy), nil
	}
	return e.loadScalarOrNullableField(gepReg, fieldTy), nil
}

// emitTupleIndexDynamic is `t[i]` for an index known only at run time: the
// element at that position, boxed (undefined past the end), as the union of
// the element types TypeScript gives it.
func (e *Emitter) emitTupleIndexDynamic(ex *ast.IndexExpression, tupleTy Type) (Value, error) {
	objVal, err := e.emitExpr(ex.Object)
	if err != nil {
		return Value{}, err
	}
	iv, err := e.emitExpr(ex.Index)
	if err != nil {
		return Value{}, err
	}
	if iv.Ty.IsDynamic() {
		iv = e.emitUnboxBoxToType(iv.Ref, TypeF64)
	}
	idx := e.convert(iv, TypeI64)
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", slot))
	e.emitInstr(fmt.Sprintf("store i64 %d, ptr %s, align 8", nbUndefined, slot))
	doneL := e.freshLabel("tupidx.done")
	var cases strings.Builder
	labels := make([]string, len(tupleTy.Fields))
	for i := range tupleTy.Fields {
		labels[i] = e.freshLabel("tupidx.at")
		fmt.Fprintf(&cases, " i64 %d, label %%%s", i, labels[i])
	}
	e.emitTerminator(fmt.Sprintf("switch i64 %s, label %%%s [%s ]", idx.Ref, doneL, cases.String()))
	for i, f := range tupleTy.Fields {
		e.emitLabel(labels[i])
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, tupleTy.StructIR(), objVal.Ref, i))
		var v Value
		if f.Ty.IsArray() {
			v = e.loadArrayFieldValue(gep, f.Ty)
		} else {
			v = e.loadScalarOrNullableField(gep, f.Ty)
		}
		b, err := e.emitBoxValue(v)
		if err != nil {
			return Value{}, err
		}
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", b.Ref, slot))
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	}
	e.emitLabel(doneL)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", r, slot))
	return Value{Ref: r, Ty: TypeAny}, nil
}

// tupleArrayMethods are the Array.prototype methods that read their
// receiver without changing it: on a tuple they run over its elements.
var tupleArrayMethods = map[string]bool{
	"map": true, "filter": true, "forEach": true, "join": true, "includes": true,
	"indexOf": true, "lastIndexOf": true, "slice": true, "concat": true, "every": true,
	"some": true, "find": true, "findIndex": true, "findLast": true, "findLastIndex": true,
	"reduce": true, "reduceRight": true, "entries": true, "keys": true, "values": true,
	"at": true, "flat": true, "flatMap": true, "toString": true, "toReversed": true,
	"toSorted": true, "toSpliced": true, "with": true,
}

// tupleArrayCall is `t.m(…)` on a tuple, for a non-mutating array method
// m, as the call on the tuple's elements as an array (`(t as any[]).m(…)`).
// A mutating method (`push`, `sort`) would change the copy, not the tuple,
// so it is not rewritten.
func (e *Emitter) tupleArrayCall(ex *ast.CallExpression) (*ast.CallExpression, bool) {
	mem, ok := ex.Callee.(*ast.MemberExpression)
	if !ok || !tupleArrayMethods[mem.Property] {
		return nil, false
	}
	if c, ok := e.tupleArrayCalls[ex]; ok {
		if c == nil { // ex is itself a rewrite
			return nil, false
		}
		return c, true
	}
	if ot := e.inferExprType(mem.Object); !ot.IsTuple() || ot.IsDynamic() {
		return nil, false
	}
	recv := ast.NewAsExpression(mem.Object, ast.AnyArrayAnnotation(""), mem.GetPos())
	callee := ast.NewMemberExpression(recv, mem.Property, mem.GetPos())
	callee.Optional = mem.Optional
	c := *ex
	c.Callee = callee
	if e.tupleArrayCalls == nil {
		e.tupleArrayCalls = map[*ast.CallExpression]*ast.CallExpression{}
	}
	e.tupleArrayCalls[ex] = &c
	e.tupleArrayCalls[&c] = nil
	return &c, true
}

// tupleArrayMethodRule emits a tuple's array-method call (tupleArrayCall).
func (e *Emitter) tupleArrayMethodRule(ex *ast.CallExpression) (Value, bool, error) {
	if c, ok := e.tupleArrayCall(ex); ok {
		return handled(e.emitCall(c))
	}
	return Value{}, false, nil
}

// tupleAsArray reports the array type a tuple asserted to one
// (`t as any[]`) converts to: a tuple's struct is not an array's storage.
func (e *Emitter) tupleAsArray(ex *ast.AsExpression) (Type, bool) {
	if ex.TypeAnnot == nil || ex.TypeAnnot.ElemType == nil {
		return Type{}, false
	}
	t := e.resolveType(ex.TypeAnnot)
	if !t.IsArray() || t.IsTuple() || t.IsDynamic() {
		return Type{}, false
	}
	if it := e.inferExprType(ex.Expr); !it.IsTuple() || it.IsDynamic() {
		return Type{}, false
	}
	return t, true
}

// tupleCommonElem is the element type of an array of a tuple's elements from
// position from on: their one type when all are the same plain scalar or
// string, else any.
func tupleCommonElem(tupleTy Type, from int) Type {
	elemTy := TypeAny
	for j := from; j < len(tupleTy.Fields); j++ {
		ft := tupleTy.Fields[j].Ty
		plain := isPlainStringType(ft) || (ft.IR == "i64" || ft.IR == "double" || ft.IR == "i1") && !ft.IsDynamic() && !ft.IsArray()
		switch {
		case !plain:
			return TypeAny
		case j == from:
			elemTy = ft
		case elemTy.IR != ft.IR || isPlainStringType(elemTy) != isPlainStringType(ft):
			return TypeAny
		}
	}
	return elemTy
}

// tupleSpreadRule is a call spreading a tuple (`f(...t)`): the tuple's
// arity is static, so its elements become positional arguments. The
// receiver and the arguments are evaluated once, in order, first.
func (e *Emitter) tupleSpreadRule(ex *ast.CallExpression) (Value, bool, error) {
	spreads := false
	for _, a := range ex.Args {
		if sp, ok := a.(*ast.SpreadElement); ok {
			if t := e.inferExprType(sp.Arg); t.IsTuple() && !t.IsDynamic() {
				spreads = true
				break
			}
		}
	}
	if !spreads {
		return Value{}, false, nil
	}
	c := *ex
	if mem, ok := ex.Callee.(*ast.MemberExpression); ok {
		obj, err := e.receiverTemp(mem.Object)
		if err != nil {
			return handled(Value{}, err)
		}
		m := *mem
		m.Object = obj
		c.Callee = &m
	}
	c.Args = nil
	for _, a := range ex.Args {
		sp, ok := a.(*ast.SpreadElement)
		if !ok {
			t, err := e.receiverTemp(a)
			if err != nil {
				return handled(Value{}, err)
			}
			c.Args = append(c.Args, t)
			continue
		}
		tt := e.inferExprType(sp.Arg)
		if !tt.IsTuple() || tt.IsDynamic() {
			t, err := e.receiverTemp(sp.Arg)
			if err != nil {
				return handled(Value{}, err)
			}
			c.Args = append(c.Args, ast.NewSpreadElement(t, sp.GetPos()))
			continue
		}
		t, err := e.receiverTemp(sp.Arg)
		if err != nil {
			return handled(Value{}, err)
		}
		for j := range tt.Fields {
			c.Args = append(c.Args, ast.NewIndexExpression(t, ast.NewNumberLiteral(strconv.Itoa(j), sp.GetPos()), sp.GetPos()))
		}
	}
	return handled(e.emitCall(&c))
}
