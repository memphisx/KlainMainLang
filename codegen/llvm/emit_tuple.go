// emit_tuple.go — tuple types `[T0, T1, ...]` (TDD-00066). A tuple is an
// array (TupleType, types.go): its elements are held in the array's one
// storage type, and each read converts to the element's own type (Fields).
// The tuple-specific surface (a constant-index read, positional
// destructuring, rendering) lives here or is flagged via Type.IsTuple.
package llvm

import (
	"KlainMainLang/ast"
	"KlainMainLang/checker"
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
	elems, err := e.expandTupleSpreads(elems)
	if err != nil {
		return Value{}, err
	}
	if len(elems) != len(ty.Fields) {
		return Value{}, fmt.Errorf("a %d-tuple literal must have exactly %d elements, got %d", len(ty.Fields), len(ty.Fields), len(elems))
	}
	vals := make([]Value, len(elems))
	for i, elemExpr := range elems {
		if _, isSpread := elemExpr.(*ast.SpreadElement); isSpread {
			return Value{}, fmt.Errorf("a spread element is not supported in a tuple literal")
		}
		v, err := e.emitExprWithObjectHint(elemExpr, ty.Fields[i].Ty)
		if err != nil {
			return Value{}, err
		}
		vals[i] = e.convert(v, ty.Fields[i].Ty)
	}
	return e.tupleOf(vals, ty), nil
}

// tupleOf is a new tuple of type ty holding vals, each of its element's type.
func (e *Emitter) tupleOf(vals []Value, ty Type) Value {
	store := *ty.ElemType
	dataReg := e.freshReg()
	e.heapMallocInto(dataReg, elemAllocKind(store), fmt.Sprint(max(1, len(vals))*store.Align()))
	for i, v := range vals {
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i64 %d", gep, store.IR, dataReg, i))
		e.storeArrayElem(gep, store, e.convert(v, store))
	}
	return e.mkArray(dataReg, fmt.Sprint(len(vals)), ty)
}

// tupleElem reads element i of the tuple t, as the element's own type.
func (e *Emitter) tupleElem(t Value, i int) Value {
	store := *t.Ty.ElemType
	gep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i64 %d", gep, store.IR, e.arrayData(t), i))
	return e.convert(e.loadArrayElem(gep, store), t.Ty.Fields[i].Ty)
}

// expandTupleSpreads replaces each spread of a tuple value in a tuple
// literal's elements (`[1, ...pair]`) by reads of its elements, the tuple
// evaluated once, in order with the elements around it.
func (e *Emitter) expandTupleSpreads(elems []ast.Expression) ([]ast.Expression, error) {
	spread := false
	for _, x := range elems {
		if _, ok := x.(*ast.SpreadElement); ok {
			spread = true
		}
	}
	if !spread {
		return elems, nil
	}
	var out []ast.Expression
	for i, x := range elems {
		sp, ok := x.(*ast.SpreadElement)
		if !ok {
			if hasSpreadAfter(elems, i) {
				// An element before a later spread is evaluated in its
				// place, before that spread's tuple.
				v, err := e.emitExpr(x)
				if err != nil {
					return nil, err
				}
				out = append(out, e.tupleSpreadTemp(v, x.GetPos()))
				continue
			}
			out = append(out, x)
			continue
		}
		var v Value
		var err error
		hint := e.typeOfExpr(sp.Arg)
		if as, ok := sp.Arg.(*ast.AsExpression); ok && as.TypeAnnot != nil {
			if _, lit := as.Expr.(*ast.ArrayLiteral); lit {
				hint = e.annotationType(as.TypeAnnot) // `...(["x"] as [string])`
				sp = ast.NewSpreadElement(as.Expr, sp.GetPos())
			}
		}
		if hint.IsTuple() {
			v, err = e.emitExprWithObjectHint(sp.Arg, hint)
		} else {
			v, err = e.emitExpr(sp.Arg)
		}
		if err != nil {
			return nil, err
		}
		if !v.Ty.IsTuple() {
			return nil, fmt.Errorf("a spread of a value that is not a tuple is not supported in a tuple literal")
		}
		tmp := e.tupleSpreadTemp(v, sp.GetPos())
		for j := range v.Ty.Fields {
			out = append(out, ast.NewIndexExpression(tmp, ast.NewNumberLiteral(strconv.Itoa(j), sp.GetPos()), sp.GetPos()))
		}
	}
	return out, nil
}

func hasSpreadAfter(elems []ast.Expression, i int) bool {
	for _, x := range elems[i+1:] {
		if _, ok := x.(*ast.SpreadElement); ok {
			return true
		}
	}
	return false
}

// tupleSpreadTemp holds an evaluated value in a fresh slot, named by an
// identifier only this expansion reads.
func (e *Emitter) tupleSpreadTemp(v Value, pos ast.Pos) *ast.Identifier {
	slot := e.freshReg()
	ir := StructFieldIR(v.Ty)
	e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", slot, ir, v.Ty.Align()))
	e.emitInstr(fmt.Sprintf("store %s %s, "+irPtr+" %s, align %d", ir, v.Ref, slot, v.Ty.Align()))
	name := "__kml_tuple_spread_" + strings.TrimPrefix(slot, "%")
	e.define(name, Symbol{Ptr: slot, Ty: v.Ty})
	return ast.NewIdentifier(name, pos)
}

// tuplePatternFits reports a pattern binding nothing past a tuple of n
// elements: beyond it only holes and a rest (an empty array) may follow.
func tuplePatternFits(elems []ast.ArrayPatternElem, n int) bool {
	for i := n; i < len(elems); i++ {
		el := elems[i]
		if !el.Rest && (el.Name != "" || el.SubArray != nil || el.SubObject != nil) {
			return false
		}
	}
	return true
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
	if !tuplePatternFits(elems, len(tupleTy.Fields)) {
		return internalAt(pos, "destructuring pattern of %d elements over a %d-tuple", len(elems), len(tupleTy.Fields))
	}
	tv := Value{Ref: objPtr, Ty: tupleTy}
	for i, elem := range elems {
		if elem.Rest {
			return e.bindTupleRest(objPtr, tupleTy, i, elem, pos)
		}
		if elem.Name == "" && elem.SubArray == nil && elem.SubObject == nil {
			continue // hole
		}
		if i >= len(tupleTy.Fields) {
			continue
		}
		fieldTy := tupleTy.Fields[i].Ty
		val := e.tupleElem(tv, i)
		if elem.SubArray != nil || elem.SubObject != nil {
			if err := e.unpackNestedTupleElem(val, fieldTy, elem, pos); err != nil {
				return err
			}
			continue
		}
		if fieldTy.IsArray() {
			// An array element binds under the object-reference model (TDD-00127):
			// a stable slot holding the element's header.
			e.define(elem.Name, Symbol{Ptr: e.newArrayHeaderSlotFromAggregate(val), Ty: fieldTy})
			continue
		}
		slot := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", slot, StructFieldIR(fieldTy), fieldTy.Align()))
		e.emitInstr(fmt.Sprintf("store %s %s, "+irPtr+" %s, align %d", StructFieldIR(fieldTy), val.Ref, slot, fieldTy.Align()))
		e.define(elem.Name, Symbol{Ptr: slot, Ty: fieldTy})
	}
	return nil
}

// bindTupleRest binds `...rest` at pattern position from: a new array of
// the tuple's elements from there on, typed by their one element type when
// they share it, else as any[].
func (e *Emitter) bindTupleRest(objPtr string, tupleTy Type, from int, elem ast.ArrayPatternElem, pos ast.Pos) error {
	tmpSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca "+irPtr+", align 8", tmpSlot))
	e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", objPtr, tmpSlot))
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
func (e *Emitter) unpackNestedTupleElem(val Value, fieldTy Type, elem ast.ArrayPatternElem, pos ast.Pos) error {
	switch {
	case elem.SubArray != nil && fieldTy.IsTuple():
		return e.unpackTuplePatternInto(val.Ref, fieldTy, elem.SubArray, pos)
	case elem.SubArray != nil && fieldTy.IsArray():
		elemTy := TypeI64
		if fieldTy.ElemType != nil {
			elemTy = *fieldTy.ElemType
		}
		return e.unpackArrayPatternInto(e.arrayData(val), e.arrayLen(val), elemTy, elem.SubArray)
	case elem.SubObject != nil && fieldTy.IsObject():
		return e.unpackObjectPatternInto(val.Ref, fieldTy, elem.SubObject, pos)
	default:
		return internalAt(pos, "nested destructuring pattern over a tuple element of another type")
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
	for i := range v.Ty.Fields {
		fieldVal := e.tupleElem(v, i)
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
		// Past the tuple's end (TS2493 in the strict lane): undefined, the
		// tuple evaluated first.
		if _, err := e.emitExpr(ex.Object); err != nil {
			return Value{}, err
		}
		return Value{Ref: fmt.Sprint(nbUndefined), Ty: TypeAny}, nil
	}
	objVal, err := e.emitExpr(ex.Object)
	if err != nil {
		return Value{}, err
	}
	objVal.Ty = tupleTy
	return e.tupleElem(objVal, int(idx)), nil
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
	e.emitInstr(fmt.Sprintf("store i64 %d, "+irPtr+" %s, align 8", nbUndefined, slot))
	doneL := e.freshLabel("tupidx.done")
	var cases strings.Builder
	labels := make([]string, len(tupleTy.Fields))
	for i := range tupleTy.Fields {
		labels[i] = e.freshLabel("tupidx.at")
		fmt.Fprintf(&cases, " i64 %d, label %%%s", i, labels[i])
	}
	e.emitTerminator(fmt.Sprintf("switch i64 %s, label %%%s [%s ]", idx.Ref, doneL, cases.String()))
	objVal.Ty = tupleTy
	for i := range tupleTy.Fields {
		e.emitLabel(labels[i])
		v := e.tupleElem(objVal, i)
		b, err := e.emitBoxValue(v)
		if err != nil {
			return Value{}, err
		}
		e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", b.Ref, slot))
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	}
	e.emitLabel(doneL)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", r, slot))
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
	if ot := e.typeOfExpr(mem.Object); !ot.IsTuple() || ot.IsDynamic() {
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

// tupleAsArray reports the array type a tuple asserted to one
// (`t as any[]`) converts to: a tuple's struct is not an array's storage.
func (e *Emitter) tupleAsArray(ex *ast.AsExpression) (Type, bool) {
	if ex.TypeAnnot == nil || ast.ArrayElemNode(ex.TypeAnnot) == nil {
		return Type{}, false
	}
	t := e.annotationType(ex.TypeAnnot)
	if !t.IsArray() || t.IsTuple() || t.IsDynamic() {
		return Type{}, false
	}
	if it := e.typeOfExpr(ex.Expr); !it.IsTuple() || it.IsDynamic() {
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
			if _, fixed := e.spreadArity(sp.Arg); fixed {
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
		if _, fixed := e.spreadArity(sp.Arg); !fixed {
			t, err := e.receiverTemp(sp.Arg)
			if err != nil {
				return handled(Value{}, err)
			}
			c.Args = append(c.Args, ast.NewSpreadElement(t, sp.GetPos()))
			continue
		}
		xs, err := e.spreadOnce(sp.Arg)
		if err != nil {
			return handled(Value{}, err)
		}
		c.Args = append(c.Args, xs...)
	}
	return handled(e.emitCall(&c))
}

// spreadArity is the static element count of a spread argument: a tuple's,
// or the checker's for a fixed tuple held as an array (`[4, 5, 6] as
// const`, a readonly tuple).
func (e *Emitter) spreadArity(x ast.Expression) (int, bool) {
	if t := e.typeOfExpr(x); t.IsTuple() && !t.IsDynamic() {
		return len(t.Fields), true
	} else if !t.IsArray() || t.IsDynamic() {
		return 0, false
	}
	o := e.reprOracle()
	if o == nil {
		return 0, false
	}
	ct := o.c.TypeOf(x)
	if o.c.Unanswered(ct) || ct.Flags&checker.Object == 0 || ct.Kind != checker.Tuple || ct.Opt != 0 {
		return 0, false
	}
	return len(ct.Elems), true
}

// applySpreadRule is a JavaScript call spreading a value of no static
// arity into a function with no rest parameter (`add(...xs)`): the call
// `apply` makes, `f.apply(receiver, [...args])`, which fills the fixed
// parameters from the spread and counts every argument in `arguments`.
func (e *Emitter) applySpreadRule(ex *ast.CallExpression) (Value, bool, error) {
	if !e.compatJS() || ex.Optional {
		return Value{}, false, nil
	}
	o := e.reprOracle()
	if o == nil {
		return Value{}, false, nil
	}
	ct := o.c.TypeOf(ex.Callee)
	if o.c.Unanswered(ct) || ct.Flags&checker.Object == 0 || ct.Kind != checker.Function || len(ct.Overloads) > 0 {
		return Value{}, false, nil
	}
	// The parameters before the rest, if any (an implicit `arguments` rest
	// included): a spread there has no static place.
	fixedParams := len(ct.Params)
	if ct.HasRestParam() {
		fixedParams--
	}
	spreads := false
	for i, a := range ex.Args {
		if sp, ok := a.(*ast.SpreadElement); ok {
			if _, fixed := e.spreadArity(sp.Arg); !fixed && (i < fixedParams || !ct.HasRestParam()) {
				spreads = true
			}
		}
	}
	if !spreads {
		return Value{}, false, nil
	}
	pos := ex.GetPos()
	var recv, callee ast.Expression
	switch c := ex.Callee.(type) {
	case *ast.Identifier:
		recv, callee = ast.NewNullLiteral(true, pos), c // undefined
	case *ast.MemberExpression:
		if c.Optional {
			return Value{}, false, nil
		}
		obj, err := e.receiverTemp(c.Object)
		if err != nil {
			return handled(Value{}, err)
		}
		m := *c
		m.Object = obj
		recv, callee = obj, &m
	default:
		return Value{}, false, nil
	}
	apply := ast.NewMemberExpression(callee, "apply", pos)
	return handled(e.emitCall(ast.NewCallExpression(apply, []ast.Expression{recv, ast.NewArrayLiteral(ex.Args, pos)}, pos)))
}

// optionalSpreadRule is a TypeScript call spreading a value of no static
// arity over optional parameters (`f(...arr)` or `f(...arr, 9)` of
// `f(a?: number, b?: number, c?: number)`, which tsc accepts): the
// arguments from that spread on are collected once into an array, each
// remaining parameter takes its element where there is one and undefined
// past the end, and a rest parameter the elements after them.
func (e *Emitter) optionalSpreadRule(ex *ast.CallExpression) (Value, bool, error) {
	if e.compatJS() || ex.Optional {
		return Value{}, false, nil
	}
	first := -1
	for i, a := range ex.Args {
		if sp, ok := a.(*ast.SpreadElement); ok {
			if _, fixed := e.spreadArity(sp.Arg); !fixed {
				first = i
				break
			}
		}
	}
	if first < 0 {
		return Value{}, false, nil
	}
	o := e.reprOracle()
	if o == nil {
		return Value{}, false, nil
	}
	ct := o.c.TypeOf(ex.Callee)
	if o.c.Unanswered(ct) || ct.Flags&checker.Object == 0 || ct.Kind != checker.Function || len(ct.Overloads) > 0 || len(ct.TypeParams) > 0 {
		return Value{}, false, nil
	}
	fixedParams := len(ct.Params)
	if ct.HasRestParam() {
		fixedParams--
	}
	if first >= fixedParams {
		return Value{}, false, nil // the spreads fill the rest alone
	}
	pos := ex.Args[first].GetPos()
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
	for _, a := range ex.Args[:first] {
		t, err := e.receiverTemp(a)
		if err != nil {
			return handled(Value{}, err)
		}
		c.Args = append(c.Args, t)
	}
	arr, err := e.receiverTemp(ast.NewArrayLiteral(ex.Args[first:], pos))
	if err != nil {
		return handled(Value{}, err)
	}
	var elemTy Type
	if at := e.typeOfExpr(arr); at.IsArray() && at.ElemType != nil {
		elemTy = *at.ElemType
	}
	for j := 0; j < fixedParams-first; j++ {
		idx := ast.NewNumberLiteral(strconv.Itoa(j), pos)
		has := ast.NewBinaryExpression("<", idx, ast.NewMemberExpression(arr, "length", pos), pos)
		var el ast.Expression = ast.NewIndexExpression(arr, idx, pos)
		// An element of another kind than its parameter (tsc checks only
		// the spread's own elements against the parameters they reach
		// first): converted from its box at run time.
		if pt, ok := o.r.of(ct.Params[first+j]); !ok || reprKey(pt.withoutNullable()) != reprKey(elemTy.withoutNullable()) {
			el = ast.NewAsExpression(el, ast.AnyAnnotation(""), pos)
		}
		c.Args = append(c.Args, ast.NewConditionalExpression(has, el, ast.NewNullLiteral(true, pos), pos))
	}
	if ct.HasRestParam() {
		from := ast.NewNumberLiteral(strconv.Itoa(fixedParams-first), pos)
		c.Args = append(c.Args, ast.NewSpreadElement(ast.NewCallExpression(ast.NewMemberExpression(arr, "slice", pos), []ast.Expression{from}, pos), pos))
	}
	return handled(e.emitCall(&c))
}
