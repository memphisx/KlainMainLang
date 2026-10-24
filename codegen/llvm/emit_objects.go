package llvm

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"KlainMainLang/ast"
)

// isArrayIndexName reports whether a field/property name is a canonical ES array
// index — the decimal string of an integer in [0, 2^32-1), no leading zero —
// returning its numeric value. Mirrors __kml_dynobj_is_index (runtime_dynobj.go)
// and es_is_index (dynjsonsrc/dynjson.c), the dynamic-object counterparts.
func isArrayIndexName(s string) (uint64, bool) {
	if len(s) == 0 || len(s) > 10 {
		return 0, false
	}
	if s[0] == '0' && len(s) > 1 {
		return 0, false
	}
	var v uint64
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, false
		}
		v = v*10 + uint64(c-'0')
	}
	if v >= 4294967295 {
		return 0, false
	}
	return v, true
}

// esOrderedFields returns fields in ES own-property enumeration order — array-
// index-named fields ascending numeric first, then the rest in their original
// declaration order — so Object.keys/values/entries, for...in and JSON over a
// static object match Node's key order. A new slice is returned only when a
// reorder is actually needed (the common no-index-key case returns the input
// unchanged). Field access stays keyed by name (FieldIndex), so reordering the
// iteration order never disturbs physical layout.
func esOrderedFields(fields []Field) []Field {
	type indexed struct {
		v uint64
		f Field
	}
	var idx []indexed
	var rest []Field
	for _, f := range fields {
		if v, ok := isArrayIndexName(f.Name); ok {
			idx = append(idx, indexed{v, f})
		} else {
			rest = append(rest, f)
		}
	}
	if len(idx) == 0 {
		return fields
	}
	sort.SliceStable(idx, func(i, j int) bool { return idx[i].v < idx[j].v })
	out := make([]Field, 0, len(fields))
	for _, e := range idx {
		out = append(out, e.f)
	}
	return append(out, rest...)
}

// Object variable declarations, destructuring, and Object static methods (groupBy, keys).

// emitObjectLiteral allocates a heap struct for an object literal and returns
// a ptr Value, with no externally-declared expected type available (the
// literal's own self-inferred shape, from inferObjectType, is the only type
// information there is — e.g. an untyped `const x = {...}`). See
// emitObjectLiteralWithHint for the case where one is known.
func (e *Emitter) emitObjectLiteral(lit *ast.ObjectLiteral) (Value, error) {
	return e.emitObjectLiteralWithHint(lit, nil)
}

// emitExprWithObjectHint evaluates expr normally, except when expr is an
// object literal and hint is a known object type: then the literal's fields
// are coerced against hint's declared field types instead of the literal's
// own self-inferred ones (see docs/tdd/TDD-00007.md); or when expr is an
// array literal (or `new Array<T>(n)` with no explicit `<T>`), in which case
// it's built against hint's declared element type instead of the literal's
// own self-inferred one, the identical reasoning applied to the other legal
// aggregate-literal shape (see docs/tdd/TDD-00028.md). Despite the name
// (kept for the many existing call sites already using it), this now covers
// both hintable literal kinds, not just objects. Every call site that knows
// an expression's statically-declared expected type (a variable
// declaration's annotation, a function parameter's declared type, a
// function's declared return type, an array's declared element type) should
// go through this instead of a bare e.emitExpr, so `{ x: 1, y: 40.6 }`
// assigned/passed/returned into a `{ x: number, y: number }`-shaped slot
// gets `y` coerced to i64 (40) rather than silently reinterpreting its raw
// float64 bit pattern as an i64 — the exact bug TDD-00007 found — and so
// `[1, 2, 3]` passed into a `float64[]`-typed slot gets every element
// coerced to double rather than left as the literal's own self-inferred i64.
func (e *Emitter) emitExprWithObjectHint(expr ast.Expression, hint Type) (Value, error) {
	// A wrapper returning its callback (`mustCall((req, res) => …)`) in a
	// function slot: the literal takes the slot's parameter types, as tsc
	// infers the wrapper's T from the context, and the result is that
	// function again.
	fnHint := hint
	if !fnHint.IsFunc() || fnHint.IsDynamic() {
		fnHint = Type{}
		for _, m := range hint.UnionMembers {
			if m.IsFunc() && !m.IsDynamic() {
				fnHint = m
			}
		}
	}
	if call, ok := expr.(*ast.CallExpression); ok && fnHint.IsFunc() && !fnHint.FuncHasRest && len(call.Args) > 0 && e.isIdentityWrapperCall(call) {
		hint := fnHint
		var inner Value
		var err error
		switch fn := call.Args[0].(type) {
		case *ast.ArrowFunction:
			inner, err = e.emitArrowFunctionWithHints(fn, hint.FuncParams)
		case *ast.FunctionExpression:
			inner, err = e.emitFunctionExpression(fn, hint.FuncParams)
		default:
			goto plain
		}
		if err != nil {
			return Value{}, err
		}
		{
			slot := e.freshReg()
			e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", inner.Ref, slot))
			name := "__kml_wrapped_cb" + slot[1:]
			e.define(name, Symbol{Ptr: slot, Ty: inner.Ty})
			args := append([]ast.Expression{ast.NewIdentifier(name, call.GetPos())}, call.Args[1:]...)
			v, err := e.emitExpr(ast.NewCallExpression(call.Callee, args, call.GetPos()))
			if err != nil {
				return Value{}, err
			}
			if v.Ty.IsFunc() {
				return v, nil
			}
			if c, ok := e.emitAnyToClosure(e.convert(v, TypeAny), hint); ok {
				return c, nil
			}
			return v, nil
		}
	}
plain:
	// An array literal stored where any is expected is an array of anything
	// (`const a: any = [1]; a.push({})`), not its elements' own type.
	if lit, ok := expr.(*ast.ArrayLiteral); ok && isUnconstrainedDynamic(hint) && !hint.IsCaught() {
		v, err := e.emitExprWithObjectHint(lit, ArrayOf(TypeAny))
		if err != nil {
			return Value{}, err
		}
		return e.convert(v, hint), nil
	}
	// An array literal where a union with one array member is expected is
	// built as that member (`["a", 2]` for `string | (string | number)[]`).
	if lit, ok := expr.(*ast.ArrayLiteral); ok && hint.IsDynamic() && len(hint.UnionMembers) > 0 {
		var arr *Type
		n := 0
		for i := range hint.UnionMembers {
			if hint.UnionMembers[i].IsArray() {
				arr = &hint.UnionMembers[i]
				n++
			}
		}
		if n == 1 {
			v, err := e.emitExprWithObjectHint(lit, *arr)
			if err != nil {
				return Value{}, err
			}
			return v, nil
		}
	}
	// `Object.create(null)` where a string-keyed dictionary is expected is an
	// empty dictionary with no prototype.
	if hint.IsDynamicObject() && hint.IsMap() && isObjectCreateNull(expr) {
		e.ensureMapStrHelpers()
		m := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_map_str_create()", m))
		fp := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 56", fp, m))
		e.emitInstr(fmt.Sprintf("store i64 1, ptr %s, align 8", fp))
		return Value{Ref: m, Ty: hint}, nil
	}
	// A numeric literal bound into an explicit int64/uint64 slot is parsed
	// straight to a 64-bit integer, so a value above 2^53 stays exact rather
	// than rounding through the default float64 literal model (TDD-00123 — the
	// integer escape hatch). Narrow-int targets and values ≤ 2^53 lose nothing
	// on the normal path, so this only intercepts the 64-bit case.
	if nl, ok := expr.(*ast.NumberLiteral); ok {
		if v, done := e.emitInt64LiteralForTarget(nl, hint); done {
			return v, nil
		}
	}
	// A `T | undefined` local bound into a box (`any`, a union, a boxed array
	// element) keeps its presence bit so coerce can box an absent one as
	// `undefined` — a plain read surfaces the bare payload zero.
	if hint.IsDynamic() {
		if _, ok := e.nullableScalarLValue(expr); ok {
			return e.emitPreserveNullableOperand(expr)
		}
	}
	// A literal bound into a union slot is built as the union's member of
	// its kind (`O | ((e) => void)`: an object literal as O, an arrow as the
	// function), then boxed by the caller.
	if len(hint.UnionMembers) > 0 {
		if m, ok := unionLiteralMember(expr, hint); ok {
			return e.emitExprWithObjectHint(expr, m)
		}
	}
	// An object literal bound into a bare any/unknown slot becomes a D1
	// dynamic object (TDD-00155) — a real runtime property bag, not a static
	// struct boxed as an opaque tag-6 pointer.
	if lit, ok := expr.(*ast.ObjectLiteral); ok && isUnconstrainedDynamic(hint) {
		return e.emitDynObjLiteral(lit)
	}
	// An array literal in a dynamic position: a HOMOGENEOUS scalar literal
	// keeps the typed tag-7 form (the ADR-00478 boxed-array round trip the
	// closure adapters rely on — a tag-11 body under a tag-7 unbox read the
	// dynarr header as a {ptr,len} pair, found as a real segfault in the
	// any_unknown example); only a heterogeneous or nested literal — the
	// shapes tag 7 cannot represent — becomes a D1 dynamic array
	// (TDD-00155 Stage 2).
	if lit, ok := expr.(*ast.ArrayLiteral); ok && isUnconstrainedDynamic(hint) {
		homogeneous := len(lit.Elements) > 0
		var kind string
		for _, el := range lit.Elements {
			t := e.inferExprType(el)
			k := scalarTypeKind(t)
			if k == "" {
				homogeneous = false
				break
			}
			if kind == "" {
				kind = k
			} else if k != kind {
				homogeneous = false
				break
			}
		}
		if homogeneous {
			return e.emitExpr(expr) // typed array literal; boxed tag 7 downstream
		}
		return e.emitDynArrLiteral(lit)
	}
	// A function expression in a dynamic position — `F.prototype.m =
	// function() {...}`, a dyn-object method property — compiles under the
	// uniform dynamic ABI (TDD-00155 Stage 4), with `this` as the boxed
	// receiver.
	if fe, ok := expr.(*ast.FunctionExpression); ok && isUnconstrainedDynamic(hint) {
		return e.emitDynFunctionExpression(fe, fe.GetPos())
	}
	// An arrow in a dynamic position compiles the same way (lexical `this`).
	if af, ok := expr.(*ast.ArrowFunction); ok && isUnconstrainedDynamic(hint) {
		return e.emitDynArrowFunction(af, af.GetPos())
	}
	// A ternary into an `any` slot boxes both branches, each built against
	// `any` (an object literal branch is a dynamic object).
	if ce, ok := expr.(*ast.ConditionalExpression); ok && isUnconstrainedDynamic(hint) {
		return e.emitConditionalAny(ce, nil)
	}
	// A function literal into a `(this: T, …) => R` slot takes its `this` as
	// the leading parameter the caller passes the receiver in.
	if hint.IsFunc() && hint.FuncThis {
		if v, ok, err := e.emitThisTakingClosure(expr, hint); ok {
			return v, err
		}
	}
	// A closure bound into a known function type (`const f: F = …`, a
	// function-typed argument slot): emit it against the expected type's
	// parameter types so its optionality ABI matches the slot (ADR-00963).
	if hint.IsFunc() {
		if af, ok := expr.(*ast.ArrowFunction); ok {
			return e.emitClosureAgainstHint(e.emitArrowFunctionWithHints(af, contextParamHints(hint, af.Params)))(hint, af.GetPos())
		}
		if fe, ok := expr.(*ast.FunctionExpression); ok {
			return e.emitClosureAgainstHint(e.emitFunctionExpression(fe, contextParamHints(hint, fe.Params)))(hint, fe.GetPos())
		}
	}
	// A ternary into an object or dictionary slot builds each branch against
	// it (`const init: Init = c ? read() : {}`: the `{}` is the dictionary).
	if ce, ok := expr.(*ast.ConditionalExpression); ok && (hint.IsObject() || hint.IsDynamicObject() || hint.IsTuple()) && hint.IR == "ptr" && !hint.IsDynamic() &&
		(!hint.IsArray() || hint.IsTuple()) && e.ptrShapedBranch(ce.Consequent) && e.ptrShapedBranch(ce.Alternate) {
		return e.emitConditionalHinted(ce, hint)
	}
	if lit, ok := expr.(*ast.ObjectLiteral); ok && (hint.IsObject() || hint.IsDynamicObject()) {
		return e.emitObjectLiteralWithHint(lit, &hint)
	}
	if lit, ok := expr.(*ast.ArrayLiteral); ok && hint.IsTuple() {
		return e.emitTupleLiteral(lit, lit.Elements, hint)
	}
	// `a ?? [x, y]` / `a || [x, y]` into a tuple slot: a once, the literal
	// built as the tuple when it is the result.
	if be, ok := expr.(*ast.BinaryExpression); ok && hint.IsTuple() && !hint.IsDynamic() && (be.Op == "??" || be.Op == "||") {
		if _, lit := be.Right.(*ast.ArrayLiteral); lit && e.ptrShapedBranch(be.Left) {
			tmp, err := e.receiverTemp(be.Left)
			if err != nil {
				return Value{}, err
			}
			var test ast.Expression = tmp
			if be.Op == "??" {
				test = ast.NewBinaryExpression("!=", tmp, ast.NewNullLiteral(false, be.GetPos()), be.GetPos())
			}
			return e.emitConditionalHinted(ast.NewConditionalExpression(test, tmp, be.Right, be.GetPos()), hint)
		}
	}
	if lit, ok := expr.(*ast.ArrayLiteral); ok && hint.IsArray() {
		return e.emitArrayLiteralAggregate(lit, hint.ElemType)
	}
	if na, ok := expr.(*ast.NewArrayExpression); ok && na.ElemType == nil && hint.IsArray() {
		return e.emitNewArraySizedAggregate(na, *hint.ElemType)
	}
	// `Promise.resolve(v)` into a declared `Promise<any>` slot must box the
	// value at the store so a later `.then`/`await` reads it back correctly
	// (see emitPromiseResolve). The hint reaches here from a typed var-init,
	// argument, or array element; without it the call self-types
	// `Promise<typeof v>` and stores the raw scalar bits.
	if ce, ok := expr.(*ast.CallExpression); ok && hint.IsPromise() {
		if mem, ok := ce.Callee.(*ast.MemberExpression); ok {
			if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "Promise" &&
				mem.Property == "resolve" && !e.isShadowedByLocal(id.Name) {
				return e.emitPromiseResolve(ce.Args, ce.GetPos(), hint)
			}
		}
	}
	// An arrow function assigned/passed into a declared function-typed slot
	// (`let cb: (b: Box) => void = (b) => b.value`, or `es.onmessage = (ev)
	// => console.log(ev.data)`) gets its own unannotated parameters typed
	// from the hint's declared param types — the same "propagate the known
	// expected shape into the literal instead of leaving it to self-infer"
	// principle TDD-00007/TDD-00028 already established for object/array
	// literals, just for the one other literal-like expression kind
	// (arrow functions) that can carry an unannotated parameter needing
	// outside context to resolve correctly. Found missing while wiring
	// an `.onmessage` handler (TDD-00038 Stage 1): without this,
	// an unannotated `ev` defaulted to plain `number` (ADR-00042), so
	// `ev.data` failed to compile as "field access on non-object" — a real,
	// pre-existing gap confirmed directly against a plain, handler-
	// unrelated `let cb: (b: Box) => void = (b) => b.value` snippet too.
	if af, ok := expr.(*ast.ArrowFunction); ok && hint.IsFunc() {
		return e.emitClosureAgainstHint(e.emitArrowFunctionWithHints(af, contextParamHints(hint, af.Params)))(hint, af.GetPos())
	}
	// Same hint propagation, for a function expression assigned/passed into
	// a declared function-typed slot (`let cb: (b: Box) => number =
	// function(b) { return b.value; }`) — function expressions need the
	// same outside-context typing an arrow function does (TDD-00060).
	if fe, ok := expr.(*ast.FunctionExpression); ok && hint.IsFunc() {
		return e.emitClosureAgainstHint(e.emitFunctionExpression(fe, contextParamHints(hint, fe.Params)))(hint, fe.GetPos())
	}
	v, err := e.emitExpr(expr)
	if err == nil && hint.IsArray() && isSelfDescribingBox(v.Ty) {
		// An `any` into an array (or Buffer) slot is the box's array.
		v = e.emitUnboxBoxToType(v.Ref, hint)
	}
	if err == nil && needsObjectRelayout(v.Ty, hint) {
		v = e.emitObjectAsRecord(v, hint)
	}
	return v, err
}

// emitClosureAgainstHint wraps a hinted closure emission: the closure's own
// inferred return type is checked against the expected function type's
// (closureFallOffMismatch, ADR-01065) before the value is handed to the slot.
func (e *Emitter) emitClosureAgainstHint(v Value, err error) func(hint Type, pos ast.Pos) (Value, error) {
	return func(hint Type, pos ast.Pos) (Value, error) {
		if err != nil {
			return Value{}, err
		}
		if merr := closureFallOffMismatch(v.Ty, hint, pos, "a function-typed slot"); merr != nil {
			return Value{}, merr
		}
		// A parameter annotated with another representation than the slot's
		// (`(d: Buffer) => …` into `(...args: any[]) => void`) calls through
		// an adapter.
		if needed, supported := funcAdapterPlan(v.Ty, hint); needed && supported {
			if adapted, ok := e.emitClosureAdapter(v, hint); ok {
				return adapted, nil
			}
		} else if needed || (v.Ty.FuncThis && !hint.FuncThis) {
			// A conversion the direct adapter does not take: through the box.
			if adapted, ok := e.emitBoxedClosureAs(v, hint); ok {
				return adapted, nil
			}
		}
		return v, nil
	}
}

// literalFitsLayout reports whether each plain property of lit can be stored
// in t's field of that name: an array into an array slot, anything else
// into a non-array one (a value of another kind converts or boxes).
func (e *Emitter) literalFitsLayout(lit *ast.ObjectLiteral, t Type) bool {
	for _, p := range lit.Properties {
		if p.Key == "" || p.KeyExpr != nil || p.AccessorKind != "" {
			continue
		}
		f, ok := fieldByName(t, p.Key)
		if !ok || f.Ty.IsDynamic() {
			continue
		}
		vt := e.inferExprType(p.Value)
		if vt.IsDynamic() || vt.IsUndefined() || vt.IsNull() {
			continue
		}
		if vt.IsArray() != f.Ty.IsArray() {
			return false
		}
	}
	return true
}

// emitRecordSpreadLiteral builds a literal spreading a structural type's
// value, which may be another layout or a bag at run time: then the source's
// own keys, its others included, in its order, as JavaScript copies them.
// Each property is evaluated once, in order, into a temporary; a literal
// whose spread sources all have their static layout is built as before,
// and otherwise as a bag (a value of the literal's structural type).
func (e *Emitter) emitRecordSpreadLiteral(lit *ast.ObjectLiteral, hint *Type) (Value, bool, error) {
	if e.recordSpreadInner || (hint != nil && (!hint.IsObject() || !isRecordView(*hint))) {
		return Value{}, false, nil
	}
	has := false
	for _, p := range lit.Properties {
		if sp, ok := p.Value.(*ast.SpreadElement); ok && p.Key == "" && p.KeyExpr == nil {
			if t := e.inferExprType(sp.Arg); isRecordView(t) && !t.Nullable() {
				has = true
			}
		}
	}
	if !has || !isRecordView(e.inferObjectType(lit)) {
		return Value{}, false, nil
	}
	pos := lit.GetPos()
	props := make([]ast.ObjectProperty, len(lit.Properties))
	foreign := "false"
	for i, p := range lit.Properties {
		props[i] = p
		sp, isSpread := p.Value.(*ast.SpreadElement)
		src := p.Value
		if isSpread {
			src = sp.Arg
		}
		v, err := e.emitExpr(src)
		if err != nil {
			return Value{}, true, err
		}
		id := e.bindValue(v, src.GetPos())
		if !isSpread {
			props[i].Value = id
			continue
		}
		props[i].Value = ast.NewSpreadElement(id, sp.GetPos())
		if isRecordView(v.Ty) && !v.Ty.Nullable() {
			f := e.emitRecordForeign(v.Ref, v.Ty)
			e.usedObjExtra = true
			e.extraTypes = append(e.extraTypes, v.Ty)
			has, either := e.freshReg(), e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_obj_has_extra(ptr %s)", has, v.Ref))
			e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", either, f, has))
			acc := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", acc, foreign, either))
			foreign = acc
		}
	}
	nl := ast.NewObjectLiteral(props, pos)
	out := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", out))
	dynL, staticL, doneL := e.freshLabel("recspread.dyn"), e.freshLabel("recspread.static"), e.freshLabel("recspread.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", foreign, dynL, staticL))
	e.recordSpreadInner = true
	defer func() { e.recordSpreadInner = false }()
	e.emitLabel(dynL)
	dv, err := e.emitDynObjLiteral(nl)
	if err != nil {
		return Value{}, true, err
	}
	_, pay := e.emitUnboxTagPayload(Value{Ref: dv.Ref, Ty: TypeAny})
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", e.emitIntToPtr(pay), out))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(staticL)
	sv, err := e.emitObjectLiteralWithHint(nl, hint)
	if err != nil {
		return Value{}, true, err
	}
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", sv.Ref, out))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", r, out))
	return Value{Ref: r, Ty: sv.Ty}, true, nil
}

// emitObjectLiteralWithHint is emitObjectLiteral's real implementation. When
// hint is non-nil and IsObject, the literal is built against hint's declared
// field layout (types, and therefore struct size/GEP indices) instead of the
// literal's own self-inferred one — see docs/tdd/TDD-00007.md for why this
// is the fix (the coercion mechanism, `storeField`'s `e.convert(val,
// fieldTy)`, already existed and already worked; it was just never given
// the declared type to coerce against). A nested object-literal-typed field
// gets its own field type threaded through as the hint for that nested
// literal, via emitExprWithObjectHint below, so nesting depth needs no
// special handling here either.
//
// Properties (including spreads) are processed in source order, each storing
// straight into its field's slot in the final (already fully-merged) struct
// layout — a later property or spread simply overwrites an earlier store at
// the same GEP index, which is exactly JS's last-write-wins object spread
// semantics, with no separate merge bookkeeping needed here.
func (e *Emitter) emitObjectLiteralWithHint(lit *ast.ObjectLiteral, hint *Type) (Value, error) {
	if lit.HasComputedKey() {
		// The object itself, as JavaScript builds it (its keys exist only
		// at run time).
		v, err := e.emitDynObjLiteral(lit)
		if err != nil {
			return Value{}, err
		}
		v.Ty = e.inferDynamicObjectType(lit)
		return v, nil
	}
	if lit.HasAccessors() {
		return e.emitObjectLiteralWithAccessors(lit)
	}
	// A spread of a bare any value has no compile-time key set: the literal
	// is a D1 dynamic object, whatever the hint (ADR-01077).
	if e.hasDynamicSpread(lit) || hasProtoKey(lit) {
		return e.emitDynObjLiteral(lit)
	}
	if v, ok, err := e.emitRecordSpreadLiteral(lit, hint); ok || err != nil {
		return v, err
	}
	// A plain object literal assigned to a string index-signature target
	// (TDD-00130) is built as a map, not a fixed struct, so `d[key]` access
	// works — the same map-backed representation a computed-key literal uses.
	if hint != nil && hint.IsDynamicObject() {
		return e.emitDynamicObjectLiteralAs(lit, hint)
	}
	ty := e.inferObjectType(lit)
	if hint != nil && hint.IsObject() {
		// A named shape's self-reference is its placeholder (`kids:
		// Tree[]` inside Tree): the live layout.
		h := e.canonicalizeClassTy(*hint)
		hint = &h
		if isRecordView(*hint) && isRecordView(ty) && !e.literalFitsLayout(lit, *hint) {
			// A property the hint's slot cannot hold (js lane): the literal
			// keeps its own layout, which the structural type reads by name.
			e.noteRecordViewSource(ty, *hint)
			hint = nil
		} else {
			ty = *hint
		}
	}
	// calloc, not malloc: a field absent from lit (an omitted `?:` optional
	// interface field never gets a storeField call below) must read back a
	// deterministic zero, not whatever garbage malloc happened to hand back
	// — a real bug found investigating destructuring defaults, see
	// ADR-00157.
	// TDD-00134 Stage 1 (-optimize-memory): a proven-non-escaping literal
	// becomes an entry-block alloca; structAlloc keeps calloc's zeroing
	// guarantee either way.
	dataReg := e.structAlloc(lit, ty)
	structIR := ty.StructIR()
	// An absent field of a box type (any, a union) is undefined, not the
	// zero word calloc leaves.
	for i, f := range ty.Fields {
		if f.Ty.IsDynamic() && !f.Ty.IsArray() && StructFieldIR(f.Ty) == "i64" {
			g := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", g, structIR, dataReg, i))
			e.heapStore("i64", fmt.Sprint(nbUndefined), g, 8)
		}
	}

	var keyNames []string // the literal's keys in creation order
	// A spread of an object that may list its keys in its own order makes
	// the literal's order a run-time one: a block built key by key.
	runBlk, inSpread := "", false
	if ty.orderIndex() >= 0 {
		for _, prop := range lit.Properties {
			if sp, ok := prop.Value.(*ast.SpreadElement); ok && prop.Key == "" && e.inferExprType(sp.Arg).orderIndex() >= 0 {
				e.ensureKeyOrder()
				runBlk = e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_keyorder_new()", runBlk))
				break
			}
		}
	}
	pushKey := func(name string) {
		if b := ty.presenceBit(name); runBlk != "" && !inSpread && b >= 0 {
			e.emitInstr(fmt.Sprintf("call void @__kml_keyorder_push(ptr %s, i64 %d)", runBlk, b))
		}
	}
	storeField := func(name string, val Value) error {
		idx, fieldTy, ok := ty.FieldIndex(name)
		if !ok {
			return fmt.Errorf("%d:%d: object has no field '%s'", lit.GetPos().Line, lit.GetPos().Col, name)
		}
		if !convertiblePure(val.Ty, fieldTy) {
			return fmt.Errorf("%d:%d: field '%s' is assigned a value of an incompatible type — this compiler is a typed subset", lit.GetPos().Line, lit.GetPos().Col, name)
		}
		gepReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gepReg, structIR, dataReg, idx))
		e.storeScalarOrNullableField(gepReg, fieldTy, val)
		e.emitPresenceInit(dataReg, ty, name)
		keyNames = append(keyNames, name)
		pushKey(name)
		return nil
	}
	// storeFieldExpr stores a property's value expression directly, so a
	// nullable-scalar field keeps a null-valued source lvalue's null-ness (which
	// would be lost if the value were pre-evaluated and auto-unwrapped to its
	// payload first). See storeScalarOrNullableFieldExpr.
	storeFieldExpr := func(name string, expr ast.Expression) error {
		idx, fieldTy, ok := ty.FieldIndex(name)
		if !ok {
			return fmt.Errorf("%d:%d: object has no field '%s'", lit.GetPos().Line, lit.GetPos().Col, name)
		}
		// An object/array literal value is *constructed* against fieldTy (the
		// hint threads through storeScalarOrNullableFieldExpr → emitExprWith-
		// ObjectHint), so its self-inferred type is irrelevant here — e.g. a
		// nested `{a: 3}` into an `{a: any}` field builds boxed. Skip the
		// self-inferred pre-check for those; any genuine incompatibility is
		// caught recursively during that construction.
		_, isObjLit := expr.(*ast.ObjectLiteral)
		_, isArrLit := expr.(*ast.ArrayLiteral)
		if !isObjLit && !isArrLit && !convertiblePure(e.inferExprType(expr), fieldTy) && e.compatJS() {
			// JavaScript stores the value as it is; the field's type takes it
			// as from `any`, a value it cannot hold throwing at run time.
			v, err := e.emitExpr(expr)
			if err != nil {
				return err
			}
			boxed, err := e.emitBoxValue(v)
			if err != nil {
				return err
			}
			return storeField(name, e.convert(boxed, fieldTy))
		}
		if !isObjLit && !isArrLit && !convertiblePure(e.inferExprType(expr), fieldTy) {
			return fmt.Errorf("%d:%d: field '%s' is assigned a value of an incompatible type — this compiler is a typed subset", lit.GetPos().Line, lit.GetPos().Col, name)
		}
		gepReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gepReg, structIR, dataReg, idx))
		if err := e.storeScalarOrNullableFieldExpr(gepReg, fieldTy, expr); err != nil {
			return err
		}
		e.emitPresenceInit(dataReg, ty, name)
		keyNames = append(keyNames, name)
		pushKey(name)
		return nil
	}

	for _, prop := range lit.Properties {
		if spread, ok := prop.Value.(*ast.SpreadElement); ok && prop.Key == "" {
			srcVal, err := e.emitExpr(spread.Arg)
			if err != nil {
				return Value{}, err
			}
			if obj, ok := e.falsyUnionSpreadSource(spread.Arg, srcVal); ok {
				srcVal = obj // `false | { j }`: the object or nothing
			}
			if !srcVal.Ty.IsObject() {
				return Value{}, fmt.Errorf("%d:%d: spread in object literal requires an object value", spread.GetPos().Line, spread.GetPos().Col)
			}
			// `{ ...undefined }` / `{ ...null }` copies nothing: an absent
			// optional source leaves its (optional) fields absent.
			skipL := ""
			if srcVal.Ty.Nullable() || srcVal.Ty.IsNull() {
				isNull := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, srcVal.Ref))
				copyL, nullL := e.freshLabel("objspread.copy"), e.freshLabel("objspread.null")
				skipL = e.freshLabel("objspread.skip")
				e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNull, nullL, copyL))
				// No source: its keys stay absent unless the literal wrote
				// them before.
				e.emitLabel(nullL)
				for _, f := range srcVal.Ty.SpreadFields() {
					written := false
					for _, n := range keyNames {
						written = written || n == f.Name
					}
					if !written {
						e.emitPresenceAbsentInit(dataReg, ty, f.Name)
					}
				}
				e.emitTerminator(fmt.Sprintf("br label %%%s", skipL))
				e.emitLabel(copyL)
			}
			// The source's present keys, by its bits, for its own order.
			srcMask := ""
			if runBlk != "" && srcVal.Ty.orderIndex() >= 0 {
				srcMask = e.freshReg()
				e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", srcMask))
				e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", srcMask))
				inSpread = true
			}
			for _, f := range srcVal.Ty.SpreadFields() {
				// Only the source's own properties are copied: an absent
				// optional field leaves the target's value (an earlier
				// property's) as it is. Object spread is a shallow copy, so an
				// array field shares the same array (header), as in JS.
				srcIdx, _, _ := srcVal.Ty.FieldIndex(f.Name)
				srcGep := e.emitRecordGep(srcVal.Ref, srcVal.Ty, srcIdx, f.Ty, f.Name)
				present, fv := e.emitFieldPresentAt(srcGep, f)
				// An own key holding undefined is copied too (TDD-00256); a
				// required key is there exactly when its bit says so (a JSON
				// input may have left it out).
				if bit := srcVal.Ty.presenceBit(f.Name); bit >= 0 && present != "true" {
					r := e.freshReg()
					e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", r, present, e.emitPresenceBitTest(srcVal.Ref, srcVal.Ty, bit)))
					present = r
				} else if bit >= 0 && !f.Optional && !f.Ty.UncheckedIndex {
					present = e.emitPresenceBitTest(srcVal.Ref, srcVal.Ty, bit)
				}
				// A non-enumerable own key is not copied.
				if en := e.emitPropEnumerable(srcVal.Ref, f.Name); en != "true" {
					r := e.freshReg()
					e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", r, present, en))
					present = r
				}
				doneL := ""
				if present != "true" {
					storeL, skipKeyL := e.freshLabel("objspread.field"), e.freshLabel("objspread.nokey")
					doneL = e.freshLabel("objspread.next")
					e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", present, storeL, skipKeyL))
					e.emitLabel(skipKeyL)
					// A key the source lacks (or does not list) is absent in
					// the result unless the literal wrote it before.
					written := false
					for _, n := range keyNames {
						written = written || n == f.Name
					}
					if !written {
						e.emitPresenceAbsentInit(dataReg, ty, f.Name)
					}
					e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
					e.emitLabel(storeL)
				}
				if err := storeField(f.Name, fv); err != nil {
					return Value{}, err
				}
				if b := srcVal.Ty.presenceBit(f.Name); srcMask != "" && b >= 0 {
					m, n := e.freshReg(), e.freshReg()
					e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", m, srcMask))
					e.emitInstr(fmt.Sprintf("%s = or i64 %s, %d", n, m, uint64(1)<<uint(b)))
					e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", n, srcMask))
				}
				if doneL != "" {
					e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
					e.emitLabel(doneL)
				}
			}
			// … and the properties added beside its layout (a class
			// instance's accessor Object.defineProperty gave it).
			e.ensureShapeRuntime()
			e.declareFn("__kml_shape_expando_spread", "declare void @__kml_shape_expando_spread(ptr, ptr)")
			e.emitInstr(fmt.Sprintf("call void @__kml_shape_expando_spread(ptr %s, ptr %s)", dataReg, srcVal.Ref))
			if srcMask != "" {
				inSpread = false
				seq, ints := e.keyOrderSeq(srcVal.Ty)
				order, m := e.freshReg(), e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", order, e.keyOrderSlot(Value{Ref: srcVal.Ref, Ty: srcVal.Ty.withoutNullable()})))
				e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", m, srcMask))
				e.emitInstr(fmt.Sprintf("call void @__kml_keyorder_push_src(ptr %s, ptr %s, ptr %s, i64 %d, i64 %s, ptr %s)", runBlk, order, seq, ints, m, e.keyOrderMap(srcVal.Ty, ty)))
			}
			if skipL != "" {
				e.emitTerminator(fmt.Sprintf("br label %%%s", skipL))
				e.emitLabel(skipL)
			}
			continue
		}
		// Look up the field's declared type (from the hinted/self-inferred
		// ty, whichever applies) before evaluating the property's value, so
		// a nested object-literal-typed field can have its own type
		// threaded through as a hint too.
		if err := storeFieldExpr(prop.Key, prop.Value); err != nil {
			return Value{}, err
		}
	}
	if runBlk != "" {
		e.heapStore("ptr", runBlk, e.keyOrderGep(dataReg, ty), 8)
	} else {
		e.emitKeyOrderInit(dataReg, ty, keyNames)
	}
	return Value{Ref: dataReg, Ty: ty}, nil
}

// emitDynamicObjectLiteralAs builds the literal as the dictionary type as
// (its value type, named properties), or the literal's own when nil.
func (e *Emitter) emitDynamicObjectLiteralAs(lit *ast.ObjectLiteral, as *Type) (Value, error) {
	for _, prop := range lit.Properties {
		if _, ok := prop.Value.(*ast.SpreadElement); ok && prop.Key == "" {
			pos := prop.Value.GetPos()
			return Value{}, fmt.Errorf("%d:%d: object spread cannot be combined with a computed property key yet", pos.Line, pos.Col)
		}
	}

	ty := e.inferDynamicObjectType(lit)
	if as != nil && as.IsDynamicObject() && as.MapKey != nil && isStringTy(*as.MapKey) {
		ty = *as
		ty.setNullable(false)
		ty.setUndefined(false)
	}
	e.ensureMapStrHelpers()
	mapPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_map_str_create()", mapPtr))

	for _, prop := range lit.Properties {
		keyExpr := prop.KeyExpr
		if keyExpr == nil {
			keyExpr = ast.NewStringLiteral(prop.Key, lit.GetPos())
		} else {
			var err error
			keyExpr, err = e.dynObjectKeyExpr(keyExpr, keyExpr.GetPos())
			if err != nil {
				return Value{}, err
			}
		}
		if _, err := e.emitMapCall(ty, mapPtr, "set", []ast.Expression{keyExpr, prop.Value}, lit.GetPos()); err != nil {
			return Value{}, err
		}
	}
	return Value{Ref: mapPtr, Ty: ty}, nil
}

// emitDynamicObjectGet handles `obj.field` / `obj[expr]` reads when obj is a
// computed-key object literal — a real Map<string,V> under the hood
// (docs/tdd/TDD-00012.md). Thin wrapper over emitMapCall's "get" that adds
// the same clean "must be a string" rejection emitDynamicObjectAssign and
// emitDynamicObjectLiteral already apply to a computed key, rather than
// letting a non-string key silently bit-reinterpret via valueToMapKey.
func (e *Emitter) emitDynamicObjectGet(ty Type, mapPtr string, keyExpr ast.Expression, pos ast.Pos) (Value, error) {
	keyExpr, err := e.dynObjectKeyExpr(keyExpr, pos)
	if err != nil {
		return Value{}, err
	}
	v, err := e.emitMapCall(ty, mapPtr, "get", []ast.Expression{keyExpr}, pos)
	if err != nil {
		return Value{}, err
	}
	if ft, ok := dictFieldType(ty, keyExpr); ok {
		return e.convert(v, ft), nil
	}
	return v, nil
}

// dictFieldType is the declared type of the named property a constant key
// reads from an index-signature dictionary.
func dictFieldType(ty Type, keyExpr ast.Expression) (Type, bool) {
	lit, ok := keyExpr.(*ast.StringLiteral)
	if !ok {
		return Type{}, false
	}
	for _, f := range ty.DictFields {
		if f.Name == lit.Value {
			return f.Ty, true
		}
	}
	return Type{}, false
}

// emitDynamicObjectAssign handles `obj.field = val` / `obj[expr] = val` (plain
// or compound) when obj is a computed-key object literal — a real
// Map<string,V> under the hood (docs/tdd/TDD-00012.md). keyExpr and rhsExpr
// are each evaluated exactly once (mirroring the array-element/object-field
// assignment branches in emitAssign), so this doesn't reuse emitMapCall
// directly — it needs the pre-evaluated key ref (for compound ops' get-then-
// set) and must return the assigned value, not emitMapCall's map-identity
// return.
func (e *Emitter) emitDynamicObjectAssign(ty Type, mapPtr string, keyExpr ast.Expression, op string, rhsExpr ast.Expression, pos ast.Pos) (Value, error) {
	valTy := TypeI64
	if ty.MapVal != nil {
		valTy = *ty.MapVal
	}
	keyExpr, err := e.dynObjectKeyExpr(keyExpr, pos)
	if err != nil {
		return Value{}, err
	}
	keyVal, err := e.emitExpr(keyExpr)
	if err != nil {
		return Value{}, err
	}
	kRef := e.valueToMapKey(keyVal, TypePtr)

	// Logical assignment (&&=/||=/??=) against a computed dynamic-object key
	// (ADR-00600): read the current value, and only store the RHS down the
	// branch the operator's short-circuit rule requires — mirroring
	// emitLogicalCompoundAssign, but through the map's get/set rather than a
	// fixed ptr. The final value is re-read from the map at the merge point.
	if isLogicalAssignOp(op) {
		e.ensureMapStrHelpers()
		rawReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_map_str_get(ptr %s, ptr %s)", rawReg, mapPtr, kRef))
		cur := e.mapValFromI64(rawReg, valTy)
		if valTy.IsDynamic() {
			// A box: a missing key is undefined.
			has := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_map_str_has(ptr %s, ptr %s)", has, mapPtr, kRef))
			sel := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %d", sel, has, rawReg, nbUndefined))
			cur = Value{Ref: sel, Ty: valTy}
		}
		// `??=` on a non-ptr value type can never trigger (no null to coalesce).
		if op == "??=" && valTy.IR != "ptr" && !valTy.IsDynamic() && !isF64Slot(valTy) {
			return cur, nil
		}
		var cond Value
		switch op {
		case "&&=":
			cond = e.toBool(cur)
		case "||=":
			notReg := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = xor i1 %s, true", notReg, e.toBool(cur).Ref))
			cond = Value{Ref: notReg, Ty: TypeBool}
		case "??=":
			nullReg := e.freshReg()
			if valTy.IsDynamic() {
				tag, _ := e.emitUnboxTagPayload(cur)
				isNull := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isNull, tag, kmlTagNull))
				isUndef := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isUndef, tag, kmlTagUndefined))
				e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", nullReg, isNull, isUndef))
			} else if isF64Slot(valTy) {
				b := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = bitcast double %s to i64", b, cur.Ref))
				e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", nullReg, b, undefF64))
			} else {
				nullReg = e.isAbsentPtr(cur.Ref, cur.Ty)
			}
			cond = Value{Ref: nullReg, Ty: TypeBool}
		}
		storeL := e.freshLabel("dynlogassign.store")
		mergeL := e.freshLabel("dynlogassign.merge")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", cond.Ref, storeL, mergeL))
		e.emitLabel(storeL)
		rhsVal, err := e.emitExprWithObjectHint(rhsExpr, valTy)
		if err != nil {
			return Value{}, err
		}
		rhsVal = e.convert(rhsVal, valTy)
		e.emitInstr(fmt.Sprintf("call void @__kml_map_str_set(ptr %s, ptr %s, i64 %s)", mapPtr, kRef, e.valueToMapVal(rhsVal, valTy)))
		e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
		e.emitLabel(mergeL)
		finalRaw := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_map_str_get(ptr %s, ptr %s)", finalRaw, mapPtr, kRef))
		return e.mapValFromI64(finalRaw, valTy), nil
	}

	var rhs Value
	if op == "=" {
		rhs, err = e.emitExprWithObjectHint(rhsExpr, valTy)
		if err != nil {
			return Value{}, err
		}
	} else {
		e.ensureMapStrHelpers()
		rawReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_map_str_get(ptr %s, ptr %s)", rawReg, mapPtr, kRef))
		cur := e.mapValFromI64(rawReg, valTy)
		rhsVal, err := e.emitExpr(rhsExpr)
		if err != nil {
			return Value{}, err
		}
		if err := dateCompoundAssignGuard(op, valTy.IsDate(), rhsVal.Ty.IsDate()); err != nil {
			return Value{}, fmt.Errorf("%d:%d: %s", pos.Line, pos.Col, err)
		}
		rhsVal = e.convert(rhsVal, valTy)
		rhs, err = e.emitArith(strings.TrimSuffix(op, "="), cur, rhsVal, valTy, pos)
		if err != nil {
			return Value{}, err
		}
	}
	rhs = e.convert(rhs, valTy)
	vRef := e.valueToMapVal(rhs, valTy)
	e.ensureMapStrHelpers()
	e.emitInstr(fmt.Sprintf("call void @__kml_map_str_set(ptr %s, ptr %s, i64 %s)", mapPtr, kRef, vRef))
	return rhs, nil
}

// emitDeclJSONProjection detects a var-decl initializer that needs the
// declaration's target type for correct field projection (TDD-00077 P3): a
// `JSON.parse(...)` call, optionally wrapped in `await`, or an awaited
// `Response.json()` call.
// `await` is identity over these synchronous calls, so `await res.json()` must
// project into ty exactly as `res.json()` does rather than fall through to the
// generic, type-context-free path (which yields a null/default value). Returns
// (value, true, nil) when expr was such a call — already projected into ty — and
// (_, false, nil) otherwise, so the caller uses its own generic path. The three
// var-decl emitters (scalar/object/array) share this one detector and each store
// the returned value in their own slot format.
func (e *Emitter) emitDeclJSONProjection(expr ast.Expression, ty Type) (Value, bool, error) {
	awaited := false
	if aw, ok := expr.(*ast.AwaitExpression); ok {
		expr = aw.Argument
		awaited = true
	}
	ce, ok := expr.(*ast.CallExpression)
	if !ok {
		return Value{}, false, nil
	}
	mem, ok := ce.Callee.(*ast.MemberExpression)
	if !ok {
		return Value{}, false, nil
	}
	if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "JSON" && !e.isShadowedByLocal(id.Name) && mem.Property == "parse" {
		val, err := e.emitJSONParse(ce.Args, ty, ce.GetPos())
		return val, true, err
	}
	if mem.Property == "json" && hasBodyMixin(e.inferExprType(mem.Object)) {
		if awaited && len(ce.Args) == 0 {
			// `await res.json()`: the lazy body promise parsed into ty, awaited
			// — the body may still be arriving, or be a stream. The enclosing
			// async callable waits, as emitAwait marks it.
			e.sawAwait = true
			objVal, err := e.emitExpr(mem.Object)
			if err != nil {
				return Value{}, true, err
			}
			prom, err := e.emitResponseCall(objVal, "json", ce.GetPos(), ty)
			if err != nil {
				return Value{}, true, err
			}
			val, err := e.emitAwaitTaskPromise(prom.Ref, ty)
			return val, true, err
		}
		// Un-awaited, `res.json()` is its promise: the generic path's.
	}
	return Value{}, false, nil
}

func (e *Emitter) emitObjectVarDecl(v *ast.VarDeclaration, ty Type) error {
	// Module-global promotion (TDD-00093): a top-level object binding is a single
	// ptr global; store the object pointer into it rather than a fresh local
	// alloca (already in e.moduleGlobals, zero-initialized).
	var ptrName string
	if e.promotedGlobalDecls[v] {
		ptrName = e.moduleGlobals[v.Name].Ptr
	} else if e.hoistedCaptures[v.Name] {
		// Captured by a nested closure: heap-box eagerly here at the declaration
		// point (which dominates the whole lexical scope) rather than lazily at
		// the capturing closure's construction site — that site may sit inside a
		// conditional/`try` block that doesn't dominate a later read of the box
		// (e.g. the same object read in the `catch`), which clang rejects as
		// "instruction does not dominate all uses" (ADR-00619 / hoistedCaptures).
		// The scalar path in emitVarDecl does this via boxHoistedCapture; object
		// bindings are a uniform ptr slot, so the cell holds one ptr seeded null,
		// and the initializer's storeObj re-resolves through this Boxed symbol.
		// This boxes exactly the set promoteCaptureToCell would have (Boxed is set,
		// so the lazy path is skipped), only earlier. A function-scoped `var` boxes
		// in the entry block (dominates unconditionally); a `let`/`const` at its
		// declaration point (dominates its block scope, and re-mallocs per loop
		// iteration for fresh per-iteration `let` cells).
		e.ensureMalloc()
		ptrName = e.freshReg()
		atEntry := v.Kind == "var"
		e.heapMallocAt(atEntry, ptrName, "8")
		e.heapStoreAt(atEntry, "ptr", "null", ptrName, 8)
		e.define(v.Name, Symbol{Ptr: ptrName, Ty: ty, Boxed: true, IsConst: v.Kind == "const"})
	} else {
		ptrName = e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", ptrName))
		if v.Kind == "var" {
			// A skippable `var` (hoistedVarMaySkip widened it to `T | undefined`)
			// reads null — absent — on the path where this declaration never ran
			// (ADR-01057).
			e.emitAlloca(fmt.Sprintf("store ptr null, ptr %s, align 8", ptrName))
		}
		e.define(v.Name, Symbol{Ptr: ptrName, Ty: ty, IsConst: v.Kind == "const"})
	}

	if v.Init == nil {
		e.heapStore("ptr", "null", ptrName, 8)
		return nil
	}

	// Mark this binding as mid-initialization so a closure emitted inside its
	// own initializer that captures it (`const s = f(() => use(s))`) seeds its
	// capture cell with a default rather than by loading the still-unwritten
	// slot (see promoteCaptureToCell), and store the final value into the
	// re-resolved slot below so a boxed self-capture receives the real value.
	wasInitializing := e.varsBeingInitialized[v.Name]
	e.varsBeingInitialized[v.Name] = true
	defer func() { e.varsBeingInitialized[v.Name] = wasInitializing }()
	// storeObj stores into the variable's *current* storage location — the box
	// if the initializer promoted this variable (updateSymbolInPlace), else the
	// original alloca. Mirrors the generic scalar path in emitVarDecl.
	storeObj := func(ref string) {
		finalPtr := ptrName
		if sym, ok := e.lookup(v.Name); ok {
			finalPtr = sym.Ptr
		}
		e.heapStore("ptr", ref, finalPtr, 8)
	}

	// JSON.parse / Response.json() (optionally awaited) projected into ty.
	if val, ok, err := e.emitDeclJSONProjection(v.Init, ty); ok {
		if err != nil {
			return err
		}
		storeObj(val.Ref)
		return nil
	}

	switch init := v.Init.(type) {
	case *ast.ObjectLiteral:
		val, err := e.emitObjectLiteralWithHint(init, &ty)
		if err != nil {
			return err
		}
		storeObj(val.Ref)
		return nil

	case *ast.ArrayLiteral:
		// A tuple-typed declaration initialized by an array literal
		// (`const t: [string, number] = ["a", 1]`) builds the tuple struct.
		if ty.IsTuple() {
			val, err := e.emitTupleLiteral(init, init.Elements, ty)
			if err != nil {
				return err
			}
			storeObj(val.Ref)
			return nil
		}
		val, err := e.emitExpr(init)
		if err != nil {
			return err
		}
		storeObj(val.Ref)
		return nil

	case *ast.NewErrorExpression:
		val, err := e.emitNewError(init)
		if err != nil {
			return err
		}
		storeObj(val.Ref)
		return nil

	case *ast.CallExpression:
		// The JSON.parse / Response.json() type-context projections are handled
		// up front by emitDeclJSONProjection above; anything else is a generic
		// object-producing call.
		val, err := e.emitExprWithObjectHint(init, ty)
		if err != nil {
			return err
		}
		if ty.IsDynamicObject() && plainRecordType(val.Ty) {
			val = e.convert(val, ty) // its fields become the dictionary's entries
		} else if val.Ty.IsDynamic() && !ty.IsDynamic() || ty.NullAndUndef() || val.Ty.IsClass() && (headeredHost(ty) || ty.IsPromise() && !ty.IsDynamic()) {
			// An `any` result (an implementation signature, a function
			// returning any) into a class or interface binding: the
			// object, unboxed; null into a three-state one;
			// an instance of a class extending a host into a host binding:
			// its body.
			val = e.convert(val, ty)
		}
		storeObj(val.Ref)
		return nil

	default:
		// Generic fallback: any other expression whose static type is
		// already known to be an object (emitVarDecl only routes here once
		// ty.IsObject is true) — a bare identifier holding an object, a
		// member-expression field read whose field is itself object-typed
		// (`const n = outer.inner`), an index into an object array
		// (`const n = arr[0]`), `new ClassName(...)`, `await somePromise`,
		// a ternary, etc. All of these were previously rejected here with
		// "must be an object literal or function call" even though the
		// exact same expression shapes already work fine as a plain
		// argument or nested sub-expression elsewhere — emitExpr already
		// evaluates every one of them correctly (proven by emitMember's own
		// generic `e.emitExpr(ex.Object)` tail relying on exactly this), so
		// there was nothing left to specially handle beyond letting it
		// through. Found while building TDD-00009 Stage 1a's linked-list
		// iterator example; see docs/adr/00/ADR-00064.md. Hinted, so an object
		// of another layout is converted to the declared one.
		val, err := e.emitExprWithObjectHint(init, ty)
		if err != nil {
			return err
		}
		if val.Ty.IsDynamic() && !ty.IsDynamic() || ty.IsDynamicObject() && plainRecordType(val.Ty) || ty.NullAndUndef() || val.Ty.IsClass() && (headeredHost(ty) || ty.IsPromise() && !ty.IsDynamic()) {
			val = e.convert(val, ty) // converted to the declared layout
		}
		storeObj(val.Ref)
		return nil
	}
}

func (e *Emitter) emitObjectDestructuring(s *ast.ObjectDestructuring) error {
	// `const { subtle } = globalThis.crypto` / `= crypto` (the corpus's
	// standard Web Crypto binding): the name reads as the TypeScript
	// SubtleCrypto's one instance wherever it is visible, a function body
	// included (tsSubtleAlias).
	if src, ok := cryptoGlobalExpr(s.Init); ok && src && e.tsSubtle() {
		if len(s.Props) == 1 && s.Props[0].Key == "subtle" && s.Props[0].Default == nil && s.Props[0].SubObject == nil && s.Props[0].SubArray == nil {
			if e.cryptoSubtleAliases == nil {
				e.cryptoSubtleAliases = map[string]bool{}
			}
			e.cryptoSubtleAliases[s.Props[0].Local] = true
			return nil
		}
	}
	if t := e.inferExprType(s.Init); t.IsArray() && !t.IsDynamic() && !hasRestProp(s.Props) && !hasComputedKeyProp(s.Props) {
		return e.unpackObjectPatternFromArray(s)
	}
	objPtr, objTy, err := e.resolveObjectPtr(s.Init, s.GetPos())
	if err != nil {
		return err
	}
	e.destrSource = s.Init // named in a null/undefined source's TypeError
	defer func() { e.destrSource = nil }()
	return e.unpackObjectPatternInto(objPtr, objTy, s.Props, s.GetPos())
}

func hasComputedKeyProp(props []ast.DestructProp) bool {
	for _, p := range props {
		if p.KeyExpr != nil {
			return true
		}
	}
	return false
}

func hasRestProp(props []ast.DestructProp) bool {
	for _, p := range props {
		if p.Rest {
			return true
		}
	}
	return false
}

// unpackObjectPatternFromArray is an object pattern over an array
// (`const { length, 0: first } = xs`): each key read off the array, an
// index past the end as undefined, a default when the read is undefined.
func (e *Emitter) unpackObjectPatternFromArray(s *ast.ObjectDestructuring) error {
	pos := s.GetPos()
	arr, err := e.receiverTemp(s.Init)
	if err != nil {
		return err
	}
	for _, p := range s.Props {
		var val ast.Expression
		if p.Key != "" && strings.Trim(p.Key, "0123456789") == "" && (p.Key == "0" || p.Key[0] != '0') {
			val = ast.NewCallExpression(ast.NewMemberExpression(arr, "at", pos), []ast.Expression{ast.NewNumberLiteral(p.Key, pos)}, pos)
		} else {
			val = ast.NewMemberExpression(arr, p.Key, pos)
		}
		if p.Default != nil {
			undef := ast.NewNullLiteral(true, pos)
			val = ast.NewConditionalExpression(ast.NewBinaryExpression("===", val, undef, pos), p.Default, val, pos)
		}
		switch {
		case p.SubArray != nil:
			err = e.emitArrayDestructuring(ast.NewArrayDestructuring(s.Kind, p.SubArray, val, pos))
		case p.SubObject != nil:
			err = e.emitObjectDestructuring(ast.NewObjectDestructuring(s.Kind, p.SubObject, val, pos))
		default:
			err = e.emitVarDecl(ast.NewVarDeclaration(s.Kind, p.Local, nil, val, p.Pos))
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// cryptoGlobalExpr reports whether expr is the ambient WebCrypto global —
// the identifier `crypto` (unshadowed) or `globalThis.crypto`.
func cryptoGlobalExpr(expr ast.Expression) (bool, bool) {
	if id, ok := expr.(*ast.Identifier); ok && id.Name == "crypto" {
		return true, true
	}
	if mem, ok := expr.(*ast.MemberExpression); ok && mem.Property == "crypto" {
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "globalThis" {
			return true, true
		}
	}
	return false, false
}

// unpackObjectPatternInto is emitObjectDestructuring's core, factored out
// so a destructured function parameter (whose object pointer is already
// known — no Init expression to resolve, see emit_func.go's
// emitFunctionDeclAs) can share the exact same per-field unpack logic
// instead of duplicating it.
//
// A `{ key = expr }` default (ADR-00158) is only accepted when key's field
// is a pointer-backed nullable type (`T | null` where T is a string,
// array, object/interface, or class instance) — the only field shape with
// a reliable "was this actually provided" signal at all in this
// compiler's static-shape object model. Confirmed directly (not assumed):
// a nullable *scalar* field (`number | null`, `boolean | null`, ...)
// represents its "null" as a fake in-band sentinel (0 / false on the same
// storage a real value also uses — `p.y === null` literally compiles to
// `icmp eq i64 %y, 0`), indistinguishable from a legitimately-stored zero
// value; triggering a default off that would silently override a real,
// intentional zero. A pointer-backed nullable field's null check is a
// genuine `icmp eq ptr %v, null` — safe. A non-nullable field (including a
// merely-optional `?:` one, whose omitted-vs-explicit-zero ambiguity is
// the exact same problem ADR-00157 already found and could only make
// deterministic, not distinguishable) has no signal to check at all.
func (e *Emitter) unpackObjectPatternInto(objPtr string, objTy Type, props []ast.DestructProp, pos ast.Pos) error {
	// A D1 dynamic source (a bare any / dynamic object, -compat=js) has no fixed
	// struct shape to GEP — read each property through the runtime dynamic-get
	// instead (emit_dynobj_destr.go). objPtr is the boxed any value register.
	if isUnconstrainedDynamic(objTy) {
		return e.unpackDynObjectPatternInto(Value{Ref: objPtr, Ty: objTy}, props, pos)
	}
	if hasComputedKeyProp(props) {
		// A runtime key names no static field: read through the boxed value.
		b, err := e.emitBoxValue(Value{Ref: objPtr, Ty: objTy})
		if err != nil {
			return err
		}
		return e.unpackDynObjectPatternInto(b, props, pos)
	}
	structIR := objTy.StructIR()

	// Named keys consumed by non-rest properties — the residual `{ ...rest }`
	// element (Stage 3b) collects every visible source field except these.
	named := make(map[string]bool)
	for _, prop := range props {
		if !prop.Rest {
			named[prop.Key] = true
		}
	}

	for _, prop := range props {
		if prop.Rest {
			if err := e.bindObjectRest(objPtr, objTy, structIR, named, prop.Local, pos); err != nil {
				return err
			}
			continue
		}
		idx, fieldTy, ok := objTy.FieldIndex(prop.Key)
		if !ok {
			// A key the layout does not hold (`{ constructor }`, an
			// inherited method): read off the boxed object, as any
			// property read walks its prototype.
			boxed, err := e.emitBoxValue(Value{Ref: objPtr, Ty: objTy})
			if err != nil {
				return err
			}
			if err := e.unpackDynObjectPatternInto(boxed, []ast.DestructProp{prop}, pos); err != nil {
				return err
			}
			continue
		}
		fieldTy = e.canonicalizeClassTy(fieldTy)

		// Nested sub-pattern at this key (`{ k: [a, b] }` / `{ k: { a } }`,
		// TDD-00065 Stage 2) — destructure field k's own value with the
		// sub-pattern rather than binding a leaf Local. A `= default`
		// combined with a nested pattern isn't supported yet.
		if prop.SubArray != nil || prop.SubObject != nil {
			if prop.Default != nil {
				// `{ k: { a } = d }`: field k (or d) under a hidden name,
				// then that name destructured.
				hidden := e.nestedDefaultName()
				leaf := ast.DestructProp{Key: prop.Key, Local: hidden, Pos: prop.Pos, Default: prop.Default}
				if err := e.unpackObjectPatternInto(objPtr, objTy, []ast.DestructProp{leaf}, pos); err != nil {
					return err
				}
				if err := e.emitNestedPatternDecl(prop.SubArray, prop.SubObject, hidden, prop.Pos); err != nil {
					return err
				}
				continue
			}
			fieldGep := e.emitRecordGep(objPtr, objTy, idx, fieldTy, prop.Key)
			if prop.SubArray != nil {
				if !fieldTy.IsArray() || fieldTy.ElemType == nil {
					return fmt.Errorf("%d:%d: cannot array-destructure non-array field '%s'", pos.Line, pos.Col, prop.Key)
				}
				dp, lv := e.arrayParts(e.loadArrayFieldValue(fieldGep, fieldTy))
				if err := e.unpackArrayPatternInto(dp, lv, *fieldTy.ElemType, prop.SubArray); err != nil {
					return err
				}
				continue
			}
			if !fieldTy.IsObject() {
				return fmt.Errorf("%d:%d: cannot object-destructure non-object field '%s'", pos.Line, pos.Col, prop.Key)
			}
			objp := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", objp, fieldGep))
			if err := e.unpackObjectPatternInto(objp, fieldTy, prop.SubObject, pos); err != nil {
				return err
			}
			continue
		}

		if prop.Default != nil && !fieldTy.IsUndefined() && !fieldTy.IsDynamic() {
			// A default replaces only undefined, which this field never holds
			// (a `T | null` field keeps its null); the default is not evaluated.
			prop.Default = nil
		}
		if prop.Default != nil && !((fieldTy.Nullable() || fieldTy.Kind == kindUndefined) && fieldTy.IR == "ptr") && !isNullableScalar(fieldTy) && !fieldTy.IsDynamic() {
			return fmt.Errorf("%d:%d: a destructuring default requires field '%s' to be nullable/optional (T | null, T | undefined, or `key?: T`) — no other field type has a reliable way to tell a real value apart from 'not provided'", pos.Line, pos.Col, prop.Key)
		}
		gepReg := e.emitRecordGep(objPtr, objTy, idx, fieldTy, prop.Key)
		if fieldTy.IsArray() {
			// A destructured array-typed field needs a real, named array
			// Symbol (a slot holding the header) like any other array local
			// variable — otherwise later uses of this binding (e.g. .push(),
			// which writes a resized length back through the header) would
			// have nothing to write through. See docs/adr/00/ADR-00061.md.
			// The binding is the field's own array (one header: `w.push(x)`
			// is `o.w.push(x)`), null when the field holds null.
			header := e.loadArrayFieldValue(gepReg, fieldTy).Ref
			hdrSlot := e.freshReg()
			e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", hdrSlot))
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", header, hdrSlot))
			if prop.Default != nil {
				isNullReg := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNullReg, header))
				absentL := e.freshLabel("destr.absent")
				afterL := e.freshLabel("destr.after")
				e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNullReg, absentL, afterL))
				e.emitLabel(absentL)
				defVal, err := e.emitExprWithObjectHint(prop.Default, fieldTy)
				if err != nil {
					return err
				}
				if !defVal.Ty.IsArray() {
					return fmt.Errorf("%d:%d: destructuring default must be an array to match field '%s'", prop.Default.GetPos().Line, prop.Default.GetPos().Col, prop.Key)
				}
				e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", e.arrayReturnHeader(defVal), hdrSlot))
				e.emitTerminator(fmt.Sprintf("br label %%%s", afterL))
				e.emitLabel(afterL)
			}
			e.define(prop.Local, Symbol{Ptr: hdrSlot, Ty: fieldTy})
			continue
		}
		// A nullable-scalar field (`y?: number` / `y: number | null`,
		// TDD-00064/TDD-00187) destructures into a nullable-scalar *local*:
		// the whole { i1, T } slot copies over, so absence survives into the
		// binding. A `= default` keys off the presence bit — now a reliable
		// "was this provided" signal, retiring the old scalar-default
		// rejection for optional/nullable scalar fields.
		if isNullableScalar(fieldTy) {
			nsPtr := e.freshReg()
			e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", nsPtr, nullableScalarStorageIR(fieldTy), storageAlign(fieldTy)))
			e.copyNullableScalar(nsPtr, fieldTy, gepReg)
			if prop.Default != nil {
				present := e.loadNullableScalarPresent(nsPtr, fieldTy)
				if fieldTy.NullAndUndef() {
					// A null keeps its value; only undefined takes the default.
					marked := e.triPayloadIsMarker(e.loadNullableScalarPayload(nsPtr, fieldTy), fieldTy)
					keep := e.freshReg()
					e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", keep, present, marked))
					present = keep
				}
				absentL := e.freshLabel("destr.absent")
				afterL := e.freshLabel("destr.after")
				e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", present, afterL, absentL))
				e.emitLabel(absentL)
				defVal, err := e.emitExpr(prop.Default)
				if err != nil {
					return err
				}
				defVal = e.convert(defVal, fieldTy.withoutNullable())
				e.storeNullableScalarPresent(nsPtr, fieldTy, defVal.Ref)
				e.emitTerminator(fmt.Sprintf("br label %%%s", afterL))
				e.emitLabel(afterL)
			}
			e.define(prop.Local, Symbol{Ptr: nsPtr, Ty: fieldTy})
			continue
		}
		valReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", valReg, fieldTy.IR, gepReg, fieldTy.Align()))
		localPtr := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", localPtr, fieldTy.IR, fieldTy.Align()))

		if prop.Default != nil {
			isNullReg := e.freshReg()
			if fieldTy.IsDynamic() {
				// A boxed field: the default replaces its undefined.
				isNullReg = e.emitIsUndefinedValue(Value{Ref: valReg, Ty: fieldTy})
			} else {
				e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNullReg, valReg))
			}
			absentL := e.freshLabel("destr.absent")
			presentL := e.freshLabel("destr.present")
			afterL := e.freshLabel("destr.after")
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNullReg, absentL, presentL))

			e.emitLabel(absentL)
			defVal, err := e.emitExpr(prop.Default)
			if err != nil {
				return err
			}
			defVal = e.convert(defVal, fieldTy)
			e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", fieldTy.IR, defVal.Ref, localPtr, fieldTy.Align()))
			e.emitTerminator(fmt.Sprintf("br label %%%s", afterL))

			e.emitLabel(presentL)
			e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", fieldTy.IR, valReg, localPtr, fieldTy.Align()))
			e.emitTerminator(fmt.Sprintf("br label %%%s", afterL))

			e.emitLabel(afterL)
		} else {
			e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", fieldTy.IR, valReg, localPtr, fieldTy.Align()))
		}
		e.definePatternLocal(prop.Local, Symbol{Ptr: localPtr, Ty: fieldTy})
	}
	return nil
}

// bindObjectRest implements `{ ...rest }` (TDD-00065 Stage 3b): it synthesizes
// a fresh residual object holding every VISIBLE source field not already named
// by an earlier property, and binds it to `local`. The residual's type is a
// plain structural ObjectType of those remaining fields (no hidden/class
// fields — a class's `{ ...rest }` yields a plain object, matching JS's
// own-enumerable-property copy), so every downstream consumer (field access,
// JSON, console.log, a further spread) rides the existing object machinery
// unchanged. The copy is shallow — a field whose value is itself a pointer
// (string, array header, nested object) is copied by pointer, like object
// spread (emitObjectLiteralWithHint) and JS's own `{ ...x }`.
func (e *Emitter) bindObjectRest(objPtr string, objTy Type, srcStructIR string, named map[string]bool, local string, pos ast.Pos) error {
	var residual []Field
	for _, f := range objTy.VisibleFields() {
		if named[f.Name] {
			continue
		}
		residual = append(residual, f)
	}
	restTy := UserObjectType(residual)

	// calloc, not malloc — an empty residual (`{ a, ...rest }` over `{ a }`)
	// still needs a valid zero-sized-safe allocation, and any field the copy
	// below doesn't reach must read back zero, matching object-literal
	// construction (ADR-00157).
	e.ensureCalloc()
	destReg := e.freshReg()
	e.emitObjAllocInto(destReg, restTy)
	destStructIR := restTy.StructIR()

	for _, f := range residual {
		srcIdx, _, _ := objTy.FieldIndex(f.Name)
		destIdx, _, _ := restTy.FieldIndex(f.Name)
		fieldIR := StructFieldIR(f.Ty)
		srcGep := e.emitRecordGep(objPtr, objTy, srcIdx, f.Ty, f.Name)
		loadReg := e.freshReg()
		destGep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", loadReg, fieldIR, srcGep, f.Ty.Align()))
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", destGep, destStructIR, destReg, destIdx))
		e.heapStore(fieldIR, loadReg, destGep, f.Ty.Align())
	}

	localPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", localPtr))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", destReg, localPtr))
	e.define(local, Symbol{Ptr: localPtr, Ty: restTy})
	return nil
}

// resolveObjectPtr emits code to obtain the raw heap pointer for an object
// expression. Handles identifiers, function calls, and object literals.
func (e *Emitter) resolveObjectPtr(init ast.Expression, pos ast.Pos) (string, Type, error) {
	switch src := init.(type) {
	case *ast.Identifier:
		sym, found := e.lookup(src.Name)
		if found && sym.Ty.IsDynamic() && !sym.Ty.IsObject() {
			// A dynamic binding (a -compat=js module global, an `any`):
			// destructure its value through the dynamic gets.
			val, err := e.emitExpr(src)
			if err != nil {
				return "", Type{}, err
			}
			return val.Ref, val.Ty, nil
		}
		if found && !sym.Ty.IsObject() {
			return e.boxedDestructureSource(src)
		}
		if !found {
			return "", Type{}, fmt.Errorf("%d:%d: '%s' is not an object", pos.Line, pos.Col, src.Name)
		}
		objPtr := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", objPtr, sym.Ptr))
		return objPtr, sym.Ty, nil

	case *ast.CallExpression:
		val, err := e.emitExpr(src)
		if err != nil {
			return "", Type{}, err
		}
		// A call held in `any` (an overload whose implementation returns
		// any) destructures through the dynamic path.
		if !val.Ty.IsObject() && !isUnconstrainedDynamic(val.Ty) {
			// A primitive result: destructured as the object it coerces to.
			b, err := e.emitBoxValue(val)
			if err != nil {
				return "", Type{}, err
			}
			return b.Ref, b.Ty, nil
		}
		return val.Ref, val.Ty, nil

	case *ast.AwaitExpression:
		// `const { value, done } = await reader.read()` / `await gen.next()`
		// (TDD-00097): await the promise, destructure its object result.
		val, err := e.emitAwait(src)
		if err != nil {
			return "", Type{}, err
		}
		if !val.Ty.IsObject() && !isUnconstrainedDynamic(val.Ty) {
			return "", Type{}, fmt.Errorf("%d:%d: awaited value is not an object", pos.Line, pos.Col)
		}
		return val.Ref, val.Ty, nil

	case *ast.ObjectLiteral:
		ty := e.inferObjectType(src)
		// calloc, not malloc — see emitObjectLiteralWithHint's identical
		// comment; an omitted `?:` optional field must read back zero, not
		// malloc garbage.
		e.ensureCalloc()
		dataReg := e.freshReg()
		e.emitObjAllocInto(dataReg, ty)
		structIR := ty.StructIR()
		for _, prop := range src.Properties {
			idx, fieldTy, ok := ty.FieldIndex(prop.Key)
			if !ok {
				return "", Type{}, fmt.Errorf("%d:%d: object has no field '%s'", pos.Line, pos.Col, prop.Key)
			}
			val, err := e.emitExpr(prop.Value)
			if err != nil {
				return "", Type{}, err
			}
			gepReg := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gepReg, structIR, dataReg, idx))
			e.storeScalarOrNullableField(gepReg, fieldTy, val)
		}
		return dataReg, ty, nil
	}
	// Any other object-valued expression (`const { a, b } = xs[i]`, a
	// member, a ternary): its value.
	// One emitted as a box (an element of an any-held array) destructures
	// through the dynamic gets.
	if t := e.inferExprType(init); t.IsObject() || isUnconstrainedDynamic(t) {
		val, err := e.emitExpr(init)
		if err != nil {
			return "", Type{}, err
		}
		if val.Ty.IsObject() || isUnconstrainedDynamic(val.Ty) {
			return val.Ref, val.Ty, nil
		}
	}
	// Any other value (a primitive, null, undefined): destructured as the
	// object it coerces to, through the dynamic gets, which throw for null
	// and undefined.
	return e.boxedDestructureSource(init)
}

// boxedDestructureSource evaluates a destructuring source that is not an
// object and returns it boxed, for the dynamic path.
func (e *Emitter) boxedDestructureSource(init ast.Expression) (string, Type, error) {
	v, err := e.emitExprWithObjectHint(init, TypeAny)
	if err != nil {
		return "", Type{}, err
	}
	b, err := e.emitBoxValue(v)
	if err != nil {
		return "", Type{}, err
	}
	return b.Ref, b.Ty, nil
}

// emitConditional emits a ternary expression cond ? consequent : alternate.
// Uses an alloca+store/load pattern so both branches can produce a single result.
// emitObjectGroupBy is Object.groupBy(items, fn): a null-prototype object
// whose own properties, in first-seen order, hold each key's elements.
func (e *Emitter) emitObjectGroupBy(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 2 {
		return Value{}, fmt.Errorf("%d:%d: Object.groupBy takes exactly 2 arguments", pos.Line, pos.Col)
	}
	ptrReg, lenReg, elemTy, err := e.resolveArrayForHOF(args[0], pos)
	if err != nil {
		return Value{}, err
	}
	cb, err := e.resolveCallbackWithHints(args[1], []Type{elemTy})
	if err != nil {
		return Value{}, err
	}
	e.ensureDynObj()
	e.ensureDynArr()
	bag := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_dynobj_new()", bag))
	e.emitDynSetProtoChecked(bag, "null")

	idxAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idxAlloca))
	condL := e.freshLabel("grpby.cond")
	bodyL := e.freshLabel("grpby.body")
	doneL := e.freshLabel("grpby.done")
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	idxVal := e.freshReg()
	loopDone := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idxVal, idxAlloca))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", loopDone, idxVal, lenReg))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", loopDone, doneL, bodyL))

	e.emitLabel(bodyL)
	elemGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", elemGep, elemTy.IR, ptrReg, idxVal))
	elem := e.loadArrayElem(elemGep, elemTy)
	cbArgs := []Value{elem}
	if cb.arity() >= 2 {
		cbArgs = append(cbArgs, Value{Ref: idxVal, Ty: TypeI64})
	}
	keyVal, err := e.emitCBCall(cb, cbArgs)
	if err != nil {
		return Value{}, err
	}
	if !isStringTy(keyVal.Ty) {
		// The key is a property key: ToString of what the callback returns.
		if keyVal, err = e.emitValueToString(keyVal); err != nil {
			return Value{}, err
		}
	}
	boxed, err := e.emitBoxValue(elem)
	if err != nil {
		return Value{}, err
	}
	// The key's group: a dynamic array, created on its first element.
	found := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynobj_find(ptr %s, ptr %s)", found, bag, keyVal.Ref))
	has := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp sge i64 %s, 0", has, found))
	oldL := e.freshLabel("grpby.old")
	newL := e.freshLabel("grpby.new")
	pushL := e.freshLabel("grpby.push")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", has, oldL, newL))
	e.emitLabel(oldL)
	word := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynobj_get_at(ptr %s, i64 %s)", word, bag, found))
	untagged := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = and i64 %s, -8", untagged, word))
	oldArr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", oldArr, untagged))
	e.emitTerminator(fmt.Sprintf("br label %%%s", pushL))
	e.emitLabel(newL)
	newArr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_dynarr_new(i64 0)", newArr))
	arrBox := e.emitNbTagPtr(newArr, kmlTagDynArray)
	e.emitInstr(fmt.Sprintf("call void @__kml_dynobj_set(ptr %s, ptr %s, i64 %s)", bag, keyVal.Ref, arrBox))
	e.emitTerminator(fmt.Sprintf("br label %%%s", pushL))
	e.emitLabel(pushL)
	arr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = phi ptr [ %s, %%%s ], [ %s, %%%s ]", arr, oldArr, oldL, newArr, newL))
	e.emitInstr(fmt.Sprintf("call void @__kml_dynarr_push(ptr %s, i64 %s)", arr, boxed.Ref))
	idxNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", idxNext, idxVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", idxNext, idxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(doneL)
	return e.emitDynObjBox(bag), nil
}

// emitObjectKeys implements Object.keys(obj | groupMap) → string[].
func (e *Emitter) emitObjectKeys(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: Object.keys takes 1 argument", pos.Line, pos.Col)
	}
	if t := e.inferExprType(args[0]); !t.Nullable() {
		if _, ok := e.arrayClassHost(t); ok {
			// An instance of a class extending Array: its indices, then its
			// own fields.
			body, _, err := e.hostArrayReceiver(args[0])
			if err != nil {
				return Value{}, err
			}
			var names []ast.Expression
			for _, f := range t.VisibleFields() {
				names = append(names, ast.NewStringLiteral(f.Name, pos))
			}
			keys := ast.NewCallExpression(ast.NewMemberExpression(ast.NewIdentifier("Object", pos), "keys", pos), []ast.Expression{body}, pos)
			keys.Intrinsic = "Object.keys"
			return e.emitExpr(ast.NewCallExpression(ast.NewMemberExpression(keys, "concat", pos), []ast.Expression{ast.NewArrayLiteral(names, pos)}, pos))
		}
	}
	val, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	// An array: its indices.
	if val.Ty.IsArray() && !val.Ty.Nullable() && !val.Ty.IsTuple() {
		return e.emitDynAnyKeys(Value{Ref: e.emitBoxArrayPlain(val), Ty: TypeAny}, pos)
	}
	// A string: its index keys; any other primitive has none.
	if isStringTy(val.Ty) && !hasHandleFlag(val.Ty) && !val.Ty.Nullable() || val.Ty.IR == "i1" || val.Ty.Float || val.Ty.IsInteger() {
		box, err := e.emitBoxValue(val)
		if err != nil {
			return Value{}, err
		}
		return e.emitDynAnyKeys(box, pos)
	}
	// A bare any/unknown value: runtime key enumeration on a D1 dynamic object
	// (TDD-00155 Stage 1) — [] for primitives, TypeError for null/undefined.
	if isUnconstrainedDynamic(val.Ty) {
		return e.emitDynAnyKeys(val, pos)
	}
	// A caught value (`catch (e)`): whatever was thrown, boxed.
	if val.Ty.IsCaught() {
		return e.emitDynAnyKeys(e.emitCaughtToAny(val), pos)
	}
	// An Error's own keys are not its layout's fields: its system fields that
	// are set, its subclass's own, its added ones — the boxed walk's.
	if e.isErrorValue(val.Ty) && !val.Ty.Nullable() {
		box, err := e.emitBoxValue(val)
		if err != nil {
			return Value{}, err
		}
		return e.emitDynAnyKeys(box, pos)
	}
	// A function's own enumerable keys (TDD-00229): its own-property bag's
	// (`Object.defineProperty(f, "x", { enumerable: true, … })`, `f.x = 1`).
	if val.Ty.IsFunc() {
		box, err := e.emitBoxValue(val)
		if err != nil {
			return Value{}, err
		}
		return e.emitDynAnyKeys(box, pos)
	}
	// A Date has no own enumerable keys.
	if val.Ty.IsDate() && !val.Ty.IsDynamic() {
		return e.mkArray("null", "0", ArrayOf(TypePtr)), nil
	}
	// A dynamic object (or any string-keyed Map<string,V>) is backed by the
	// same runtime as Map<K,V> — delegate to its own .keys() rather than
	// walking a compile-time field list, see docs/tdd/TDD-00012.md.
	if val.Ty.IsMap() {
		if val.Ty.MapKey == nil || !isStringTy(*val.Ty.MapKey) {
			return Value{}, fmt.Errorf("%d:%d: Object.keys requires a string-keyed Map or dynamic object", pos.Line, pos.Col)
		}
		return e.emitMapMethod(val.Ty, val.Ref, "keys", nil, pos)
	}
	// A zero-field class (methods-only) has genuinely known, just-empty
	// fields — unlike a plain object literal, whose Fields being empty means
	// "unknown", so only the non-class case treats emptiness as an error.
	if !val.Ty.IsObject() || (!val.Ty.IsClass() && !hasBodyMixin(val.Ty) && len(val.Ty.VisibleFields()) == 0) {
		return Value{}, fmt.Errorf("%d:%d: Object.keys requires an object with known fields", pos.Line, pos.Col)
	}
	if box, ok, err := e.ownAccessorBox(val); ok || err != nil {
		if err != nil {
			return Value{}, err
		}
		return e.emitDynAnyKeys(box, pos)
	}
	fields := esOrderedFields(val.Ty.VisibleFields())
	own := func(v Value) (Value, error) {
		if keysAtRuntime(v.Ty, fields) {
			return e.emitObjectPresentFieldNames(v, fields)
		}
		return e.emitObjectFieldNames(fields, pos)
	}
	// Another layout behind a structural type: the object's own keys
	// (TDD-00233).
	if isRecordView(val.Ty) && !val.Ty.Nullable() {
		return e.emitRecordSplit(val, ArrayOf(TypePtr), own, func(box Value) (Value, error) { return e.emitDynAnyKeys(box, pos) })
	}
	if extraCandidate(val.Ty) {
		return e.emitExtraSplit(val, ArrayOf(TypePtr), own, func(obj string) string {
			return fmt.Sprintf("call { ptr, i64 } @__kml_obj_keys_dyn(ptr %s)", obj)
		})
	}
	return own(val)
}

// emitObjectFieldNames allocates a string[] of compile-time field names.
func (e *Emitter) emitObjectFieldNames(fields []Field, pos ast.Pos) (Value, error) {
	n := int64(len(fields))
	e.ensureMalloc()
	dataReg := e.freshReg()
	e.heapMallocInto(dataReg, fmt.Sprint(n*8))
	for i, f := range fields {
		keyPtr := e.internString(f.Name)
		slotReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %d", slotReg, dataReg, i))
		e.heapStore("ptr", keyPtr, slotReg, 8)
	}
	return e.mkArray(dataReg, fmt.Sprint(n), ArrayOf(TypePtr)), nil
}

// emitObjectPresentFieldNames is emitObjectFieldNames for an object with
// optional (`x?: T`) fields: an absent one has no key (Node), so the names
// are appended at runtime behind each field's presence test (ADR-01063).
func (e *Emitter) emitObjectPresentFieldNames(val Value, fields []Field) (Value, error) {
	n := int64(len(fields))
	e.ensureMalloc()
	dataReg := e.freshReg()
	e.heapMallocInto(dataReg, fmt.Sprint(n*8))
	rank, rankCount, ranked := e.keyRanks(val, fields)
	countA := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", countA))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", countA))
	for _, f := range fields {
		present, _ := e.emitFieldPresent(val.Ref, val.Ty, f)
		present = e.andEnumerable(present, val.Ref, f.Name)
		doL := e.freshLabel("keys.opt.add")
		contL := e.freshLabel("keys.opt.cont")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", present, doL, contL))
		e.emitLabel(doL)
		cur := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", cur, countA))
		at := cur
		if ranked {
			at = rank(f) // its place in the object's key order
		}
		slotReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %s", slotReg, dataReg, at))
		e.heapStore("ptr", e.internString(f.Name), slotReg, 8)
		next := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", next, cur))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", next, countA))
		e.emitTerminator(fmt.Sprintf("br label %%%s", contL))
		e.emitLabel(contL)
	}
	count := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", count, countA))
	if ranked {
		count = rankCount()
	}
	r1 := e.newArrayHeader(dataReg, count)
	return Value{Ref: r1, Ty: ArrayOf(TypePtr)}, nil
}

// emitObjectValues implements Object.values(obj) → string[].
// All field values are stringified (booleans → "true"/"false", numbers → decimal).
func (e *Emitter) emitObjectValues(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: Object.values takes 1 argument", pos.Line, pos.Col)
	}
	if v, ok, err := e.emitArrayClassOwnValues(args[0], false, pos); ok || err != nil {
		return v, err
	}
	objVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	// A dynamic object (or any string-keyed Map<string,V>): delegate to its
	// own .values() — see emitObjectKeys and docs/tdd/TDD-00012.md. Note this
	// returns real typed values (matching Map.values()'s convention), unlike
	// the string[] this function returns for fixed-shape objects below.
	if objVal.Ty.IsMap() {
		if objVal.Ty.MapKey == nil || !isStringTy(*objVal.Ty.MapKey) {
			return Value{}, fmt.Errorf("%d:%d: Object.values requires a string-keyed Map or dynamic object", pos.Line, pos.Col)
		}
		return e.emitMapMethod(objVal.Ty, objVal.Ref, "values", nil, pos)
	}
	// An array: its elements, in a new array.
	if objVal.Ty.IsArray() && !objVal.Ty.Nullable() && !objVal.Ty.IsTuple() {
		return e.emitCall(ast.NewCallExpression(ast.NewMemberExpression(e.bindValue(objVal, pos), "slice", pos), nil, pos))
	}
	// A bare any (D1 dynamic object / array): a dynamic array of the values.
	if isUnconstrainedDynamic(objVal.Ty) {
		return e.emitDynAnyEntries(objVal, false, pos)
	}
	// A string: its UTF-16 units, as one-unit strings.
	if isStringTy(objVal.Ty) && !hasHandleFlag(objVal.Ty) && !objVal.Ty.Nullable() {
		return e.emitCall(ast.NewCallExpression(ast.NewMemberExpression(e.bindValue(objVal, pos), "split", pos), []ast.Expression{ast.NewStringLiteral("", pos)}, pos))
	}
	if box, ok, err := e.ownAccessorBox(objVal); ok || err != nil {
		if err != nil {
			return Value{}, err
		}
		return e.emitDynAnyEntries(box, false, pos)
	}
	visFields := objVal.Ty.VisibleFields()
	if !objVal.Ty.IsObject() || (!objVal.Ty.IsClass() && len(visFields) == 0) {
		return Value{}, fmt.Errorf("%d:%d: Object.values requires an object with known fields", pos.Line, pos.Col)
	}
	// Homogeneous fixed shapes keep real typed values (ADR-00492) — same
	// rule Object.entries applies below; mixed shapes still stringify. An
	// optional field counts by its present type: only present fields are
	// listed (ADR-01066), so `{ x: number; y?: number }` values are `number[]`.
	// A heterogeneous shape's values are `any` (TypeScript's `any[]`), and
	// so are a structural type's: another layout behind it has its own
	// members (TDD-00233).
	valTy, homogeneous := homogeneousFieldType(presentFieldTypes(visFields))
	if !homogeneous || isRecordView(objVal.Ty) {
		valTy, homogeneous = TypeAny, false
	}
	if isRecordView(objVal.Ty) && !objVal.Ty.Nullable() {
		return e.emitRecordSplit(objVal, ArrayOf(TypeAny),
			func(v Value) (Value, error) { return e.emitObjectValuesOwn(v, visFields, valTy, homogeneous, pos) },
			func(box Value) (Value, error) { return e.emitShapeValuesArray(box, false), nil })
	}
	return e.emitObjectValuesOwn(objVal, visFields, valTy, homogeneous, pos)
}

// emitObjectValuesOwn is Object.values over objVal's static layout.
func (e *Emitter) emitObjectValuesOwn(objVal Value, visFields []Field, valTy Type, homogeneous bool, pos ast.Pos) (Value, error) {
	visFields = esOrderedFields(visFields) // ES enumeration order (Node key order)
	n := int64(len(visFields))
	e.ensureMalloc()
	dataReg := e.freshReg()
	e.heapMallocInto(dataReg, fmt.Sprint(n*int64(valTy.Align())))
	count, err := e.forEachPresentField(objVal, visFields, func(f Field, idxRef string, elemVal Value) error {
		if !homogeneous {
			boxed, err := e.emitBoxValue(elemVal)
			if err != nil {
				return fmt.Errorf("%d:%d: Object.values: field '%s': %w", pos.Line, pos.Col, f.Name, err)
			}
			elemVal = boxed
		}
		slotReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", slotReg, StructFieldIR(valTy), dataReg, idxRef))
		e.storeArrayElem(slotReg, valTy, elemVal)
		return nil
	})
	if err != nil {
		return Value{}, err
	}
	r1 := e.newArrayHeader(dataReg, count)
	return Value{Ref: r1, Ty: ArrayOf(valTy)}, nil
}

// presentFieldTypes maps each field to the type it has when present: an
// optional `T | undefined` field contributes its bare T (ADR-01066).
func presentFieldTypes(fields []Field) []Field {
	out := make([]Field, len(fields))
	for i, f := range fields {
		out[i] = f
		if jsonFieldSkippable(f.Ty) {
			out[i].Ty = presentFieldType(f.Ty)
		}
	}
	return out
}

// presentFieldType is the value type of an optional field once known present.
func presentFieldType(t Type) Type {
	if isNullableScalar(t) {
		return t.withoutNullable()
	}
	t.setNullable(false)
	t.setUndefined(false)
	return t
}

// forEachPresentField runs body once per *present* field of objVal, in the
// given order, handing it the field, the runtime output index (a constant when
// no field is optional) and the field's present-typed value (a nullable
// scalar's payload, an array/pointer as is). Returns the output count operand.
// An absent optional field is skipped — the one enumeration Object.values,
// Object.entries and for…in share with Object.keys (ADR-01063/ADR-01066).
func (e *Emitter) forEachPresentField(objVal Value, fields []Field, body func(f Field, idxRef string, v Value) error) (string, error) {
	if !keysAtRuntime(objVal.Ty, fields) {
		for i, f := range fields {
			_, v := e.emitFieldPresent(objVal.Ref, objVal.Ty, f)
			if err := body(f, fmt.Sprintf("%d", i), v); err != nil {
				return "", err
			}
		}
		return fmt.Sprintf("%d", len(fields)), nil
	}
	rank, rankCount, ranked := e.keyRanks(objVal, fields)
	countA := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", countA))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", countA))
	for _, f := range fields {
		present, v := e.emitFieldPresent(objVal.Ref, objVal.Ty, f)
		present = e.andEnumerable(present, objVal.Ref, f.Name)
		// A present key may still hold undefined when the layout records
		// presence (TDD-00256): the value keeps its undefined state then.
		if objVal.Ty.presenceBit(f.Name) >= 0 {
		} else if isNullableScalar(v.Ty) {
			_, v = e.nullableScalarAggParts(v)
		} else if jsonFieldSkippable(v.Ty) && !v.Ty.UncheckedIndex {
			v.Ty = presentFieldType(v.Ty)
		}
		doL := e.freshLabel("fld.opt.add")
		contL := e.freshLabel("fld.opt.cont")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", present, doL, contL))
		e.emitLabel(doL)
		cur := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", cur, countA))
		at := cur
		if ranked {
			at = rank(f) // its place in the object's key order
		}
		if err := body(f, at, v); err != nil {
			return "", err
		}
		next := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", next, cur))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", next, countA))
		e.emitTerminator(fmt.Sprintf("br label %%%s", contL))
		e.emitLabel(contL)
	}
	if ranked {
		return rankCount(), nil
	}
	count := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", count, countA))
	return count, nil
}

// emitObjectEntries implements Object.entries(obj) → {key: string, value: string}[].
// Each element of the returned array is a heap-allocated object with .key and .value fields.
// Iterate with `for (const e of Object.entries(obj))` then access `e.key` / `e.value`.
func (e *Emitter) emitObjectEntries(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: Object.entries takes 1 argument", pos.Line, pos.Col)
	}
	if v, ok, err := e.emitArrayClassOwnValues(args[0], true, pos); ok || err != nil {
		return v, err
	}
	objVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	// A dynamic object (or any string-keyed Map<string,V>): delegate to its
	// own .entries() — see emitObjectKeys and docs/tdd/TDD-00012.md. Returns
	// {key: string, value: V}[] with a real typed value, unlike the
	// {key: string, value: string}[] this function builds for fixed-shape
	// objects below.
	if objVal.Ty.IsMap() {
		if objVal.Ty.MapKey == nil || !isStringTy(*objVal.Ty.MapKey) {
			return Value{}, fmt.Errorf("%d:%d: Object.entries requires a string-keyed Map or dynamic object", pos.Line, pos.Col)
		}
		return e.emitMapMethod(objVal.Ty, objVal.Ref, "entries", nil, pos)
	}
	// An array: its [index, element] pairs.
	if objVal.Ty.IsArray() && !objVal.Ty.Nullable() && !objVal.Ty.IsTuple() && objVal.Ty.ElemType != nil {
		return e.emitObjectEntriesOfArray(objVal)
	}
	// A bare any (D1 dynamic object / array): a dynamic array of [key, value]
	// pairs — JS's real tuple shape, since the elements are themselves dynamic.
	if isUnconstrainedDynamic(objVal.Ty) {
		return e.emitDynAnyEntries(objVal, true, pos)
	}
	// A string: its [index, unit] pairs, the entries of its units.
	if isStringTy(objVal.Ty) && !hasHandleFlag(objVal.Ty) && !objVal.Ty.Nullable() {
		units, err := e.emitCall(ast.NewCallExpression(ast.NewMemberExpression(e.bindValue(objVal, pos), "split", pos), []ast.Expression{ast.NewStringLiteral("", pos)}, pos))
		if err != nil {
			return Value{}, err
		}
		return e.emitObjectEntriesOfArray(units)
	}
	if box, ok, err := e.ownAccessorBox(objVal); ok || err != nil {
		if err != nil {
			return Value{}, err
		}
		return e.emitDynAnyEntries(box, true, pos)
	}
	visFields := objVal.Ty.VisibleFields()
	if !objVal.Ty.IsObject() || (!objVal.Ty.IsClass() && len(visFields) == 0) {
		return Value{}, fmt.Errorf("%d:%d: Object.entries requires an object with known fields", pos.Line, pos.Col)
	}
	// Each entry is a real [string, V] tuple (TDD-00066). When every visible
	// field shares one type, V is that real type (ADR-00492); a heterogeneous
	// object's, and a structural type's (TDD-00233), are `any`.
	valTy, homogeneous := homogeneousFieldType(presentFieldTypes(visFields))
	if !homogeneous || isRecordView(objVal.Ty) {
		valTy, homogeneous = TypeAny, false
	}
	if isRecordView(objVal.Ty) && !objVal.Ty.Nullable() {
		entryTy := TupleType([]Type{TypePtr, TypeAny})
		return e.emitRecordSplit(objVal, ArrayOf(entryTy),
			func(v Value) (Value, error) { return e.emitObjectEntriesOwn(v, visFields, valTy, homogeneous, pos) },
			func(box Value) (Value, error) { return e.emitShapeValuesArray(box, true), nil })
	}
	return e.emitObjectEntriesOwn(objVal, visFields, valTy, homogeneous, pos)
}

// emitObjectEntriesOwn is Object.entries over objVal's static layout.
func (e *Emitter) emitObjectEntriesOwn(objVal Value, visFields []Field, valTy Type, homogeneous bool, pos ast.Pos) (Value, error) {
	entryTy := TupleType([]Type{TypePtr, valTy})
	entrySize := int64(entryTy.StructSize())
	visFields = esOrderedFields(visFields) // ES enumeration order (Node key order)
	n := int64(len(visFields))
	e.ensureMalloc()
	dataReg := e.freshReg()
	e.heapMallocInto(dataReg, fmt.Sprint(n*8))
	// Only present fields get an entry (ADR-01066) — see forEachPresentField.
	count, err := e.forEachPresentField(objVal, visFields, func(f Field, idxRef string, entryVal Value) error {
		// Allocate one {key: string, value: V} entry struct.
		entryReg := e.freshReg()
		e.heapMallocInto(entryReg, fmt.Sprint(entrySize))
		// Store the key (compile-time field name).
		keyPtr := e.internString(f.Name)
		keySlot := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 0", keySlot, entryTy.StructIR(), entryReg))
		e.heapStore("ptr", keyPtr, keySlot, 8)
		if !homogeneous {
			boxed, err := e.emitBoxValue(entryVal)
			if err != nil {
				return fmt.Errorf("%d:%d: Object.entries: field '%s': %w", pos.Line, pos.Col, f.Name, err)
			}
			entryVal = boxed
		}
		valSlot := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 1", valSlot, entryTy.StructIR(), entryReg))
		if valTy.IsArray() {
			e.storeArrayFieldHeader(valSlot, entryVal)
		} else {
			e.heapStore(StructFieldIR(valTy), entryVal.Ref, valSlot, valTy.Align())
		}
		// Store entry pointer in the outer array.
		slotReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %s", slotReg, dataReg, idxRef))
		e.heapStore("ptr", entryReg, slotReg, 8)
		return nil
	})
	if err != nil {
		return Value{}, err
	}
	r1 := e.newArrayHeader(dataReg, count)
	return Value{Ref: r1, Ty: ArrayOf(entryTy)}, nil
}

// emitObjectFromEntries implements Object.fromEntries(entries): the inverse of
// Object.entries, building a *dynamic object* (a Map<string,V>-backed value —
// see emitDynamicObjectLiteral and docs/tdd/TDD-00012.md) from a `[string, V][]`
// entries array. The keys aren't known at compile time, so a fixed-shape struct
// can't represent the result — but the dynamic-object representation exists for
// exactly this, and the entries source drops straight onto the same
// `emitMapSeedFromEntries` walker `new Map(entries)` uses (ADR-00347). Reads
// afterward go through `obj.field` / `obj[key]` (emitDynamicObjectGet). Keys
// must be strings (real JS stringifies them; here a non-string key type is a
// clean compile error).
// fromEntriesTypes is the value type of Object.fromEntries' result and the
// entries' key type (nil when unknown), from the entries array's element: a
// `[K, V]` tuple, or a `T[]` pair whose key and value are both T. A literal
// (`[["a", "b"]]`, `[["x", 10]]`) infers its element from the first pair
// alone, so it is a `T[]` pair only when every element of every pair is a
// string; otherwise the literal seeds as tuples.
func (e *Emitter) fromEntriesTypes(arg ast.Expression) (Type, *Type) {
	entries := e.inferExprType(arg)
	if !entries.IsArray() || entries.ElemType == nil {
		return TypeI64, nil
	}
	el := entries.ElemType
	switch {
	case el.IsTuple() && len(el.Fields) == 2:
		k := el.Fields[0].Ty
		return el.Fields[1].Ty, &k
	case el.IsArray() && el.ElemType != nil && !el.ElemType.IsArray():
		if lit, ok := arg.(*ast.ArrayLiteral); ok && !e.allStringPairs(lit) {
			return TypeI64, nil
		}
		k := *el.ElemType
		return k, &k
	}
	return TypeI64, nil
}

// allStringPairs reports whether every element of lit is an array literal of
// strings.
func (e *Emitter) allStringPairs(lit *ast.ArrayLiteral) bool {
	for _, el := range lit.Elements {
		pair, ok := el.(*ast.ArrayLiteral)
		if !ok {
			return false
		}
		for _, x := range pair.Elements {
			if t := e.inferExprType(x); !isStringTy(t) || t.IsObject() {
				return false
			}
		}
	}
	return true
}

func (e *Emitter) emitObjectFromEntries(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: Object.fromEntries takes 1 argument", pos.Line, pos.Col)
	}
	// Value type from the entries' value slot; key forced to string.
	valTy, _ := e.fromEntriesTypes(args[0])
	if e.fromEntriesKeyed(args[0]) {
		return e.emitObjectFromEntriesKeyed(args[0], pos)
	}

	mapKey := TypePtr
	ty := Type{IR: "ptr", Host: hostDynamicObject, MapKey: &mapKey, MapVal: &valTy}

	e.ensureMapStrHelpers()
	mapPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_map_str_create()", mapPtr))
	if err := e.emitMapSeedFromEntries(mapPtr, args[0], mapKey, valTy, pos); err != nil {
		return Value{}, err
	}
	return Value{Ref: mapPtr, Ty: ty}, nil
}

// fromEntriesKeyed reports whether Object.fromEntries(arg)'s keys are not
// all strings: its result is then an `any` object (emitObjectFromEntriesKeyed).
func (e *Emitter) fromEntriesKeyed(arg ast.Expression) bool {
	if _, keyTy := e.fromEntriesTypes(arg); keyTy != nil && !isStringTy(*keyTy) {
		return true
	}
	if t := e.inferExprType(arg); t.IsMap() && t.MapKey != nil && !isStringTy(*t.MapKey) {
		return true
	} else if isUnconstrainedDynamic(t) {
		return true // a dynamic iterable's keys may be anything
	}
	if lit, ok := arg.(*ast.ArrayLiteral); ok {
		for _, el := range lit.Elements {
			pair, isArr := el.(*ast.ArrayLiteral)
			if !isArr || len(pair.Elements) != 2 {
				continue // shape errors are reported by the seed path
			}
			if kt := e.inferExprType(pair.Elements[0]); !isStringTy(kt) || kt.IsObject() {
				return true
			}
		}
	}
	return false
}

// emitObjectFromEntriesKeyed is Object.fromEntries over keys that are not
// all strings (a number, a symbol, an object): the loop the specification
// runs, `o[k] = v` on a fresh object, whose ToPropertyKey stringifies a key
// or keeps a symbol.
func (e *Emitter) emitObjectFromEntriesKeyed(arg ast.Expression, pos ast.Pos) (Value, error) {
	e.fromEntriesCtr++
	srcName := fmt.Sprintf("__kml_fe_src_%d", e.fromEntriesCtr)
	obj := fmt.Sprintf("__kml_fe_obj_%d", e.fromEntriesCtr)
	pair := fmt.Sprintf("__kml_fe_pair_%d", e.fromEntriesCtr)
	if err := e.emitStmt(ast.NewVarDeclaration("const", srcName, nil, arg, pos)); err != nil {
		return Value{}, err
	}
	iter := ast.NewIdentifier(srcName, pos)
	decl := ast.NewVarDeclaration("const", obj, &ast.TypeAnnotation{Name: "any"}, ast.NewObjectLiteral(nil, pos), pos)
	set := ast.NewAssignmentExpression("=",
		ast.NewIndexExpression(ast.NewIdentifier(obj, pos), ast.NewIndexExpression(ast.NewIdentifier(pair, pos), ast.NewNumberLiteral("0", pos), pos), pos),
		ast.NewIndexExpression(ast.NewIdentifier(pair, pos), ast.NewNumberLiteral("1", pos), pos), pos)
	loop := ast.NewForOfStatement("const", pair, iter, ast.NewBlockStatement([]ast.Statement{ast.NewExpressionStatement(set, pos)}, pos), pos)
	if err := e.emitStmt(decl); err != nil {
		return Value{}, err
	}
	if err := e.emitStmt(loop); err != nil {
		return Value{}, err
	}
	return e.emitExpr(ast.NewIdentifier(obj, pos))
}

// emitObjectAssign implements Object.assign(target, ...sources): copies each
// source's fields into target, in argument order, later sources overwriting
// earlier ones on a shared field name — real JS's own last-write-wins
// semantics. Mutates target in place (same heap struct, no new allocation)
// and returns it, matching real JS returning the (mutated) target.
//
// Every source field copied must already exist, by name, in target's own
// struct type — this compiler's objects are fixed-shape heap structs (an
// interface's field list is fixed at compile time), not a dynamic property
// bag, so a source contributing a field target's type doesn't have can't be
// grafted on the way real JS would. Fails cleanly with a compile error
// instead, the same posture spread-in-object-literal and JSON.parse→object
// already take for shapes outside what a fixed struct can represent.
func (e *Emitter) emitObjectAssign(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 1 {
		return Value{}, fmt.Errorf("%d:%d: Object.assign requires at least 1 argument", pos.Line, pos.Col)
	}
	if e.dynamicAssign(args) {
		return e.emitDynObjectAssign(args, pos)
	}
	if lit, ok := assignAsSpread(args); ok {
		return e.emitExpr(lit)
	}
	if tt := e.inferExprType(args[0]); tt.IsArray() && !tt.IsTypedArray() && !tt.IsBuffer() {
		// An array target: its indices (and any other own keys) are written
		// through the dynamic path, onto the array itself; the result is
		// the target, typed as it is. An empty literal takes an array
		// source's type.
		var tv Value
		var err error
		if lit, ok := args[0].(*ast.ArrayLiteral); ok && len(lit.Elements) == 0 && len(args) > 1 {
			if st := e.inferExprType(args[1]); st.IsArray() && !st.IsTypedArray() {
				tv, err = e.emitExprWithObjectHint(args[0], st)
			} else {
				tv, err = e.emitExpr(args[0])
			}
		} else {
			tv, err = e.emitExpr(args[0])
		}
		if err != nil {
			return Value{}, err
		}
		boxed, err := e.emitDynObjectAssignOnto(tv, args, pos)
		if err != nil {
			return Value{}, err
		}
		// Read back through the box: the writes may have grown the array.
		return e.emitUnboxBoxToType(boxed.Ref, tv.Ty), nil
	}
	targetVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	if !targetVal.Ty.IsObject() {
		return Value{}, fmt.Errorf("%d:%d: Object.assign's target must be an object", pos.Line, pos.Col)
	}
	if len(args) > 1 {
		// Only a real write attempt (at least one source) needs the check —
		// Object.assign(frozenObj) with no sources never writes anything,
		// matching real JS not throwing for that case either.
		e.emitFrozenCheck(targetVal, "")
	}
	targetStructIR := targetVal.Ty.StructIR()

	for _, srcArg := range args[1:] {
		srcVal, err := e.emitExpr(srcArg)
		if err != nil {
			return Value{}, err
		}
		if !srcVal.Ty.IsObject() {
			return Value{}, fmt.Errorf("%d:%d: Object.assign's sources must be objects", pos.Line, pos.Col)
		}
		srcStructIR := srcVal.Ty.StructIR()
		for _, f := range srcVal.Ty.VisibleFields() {
			dstIdx, dstTy, ok := targetVal.Ty.FieldIndex(f.Name)
			if !ok {
				return Value{}, fmt.Errorf("%d:%d: Object.assign: source has field '%s' not present on target's type", pos.Line, pos.Col, f.Name)
			}
			// The target field keeps its own type in this typed-object subset,
			// so a source whose same-named field has an incompatible type
			// (`Object.assign({a:1}, {a:"c"})` — number target, string source)
			// can't merge into it. coerce alone would keep the source register
			// while relabeling its type to the target's, storing e.g. a string
			// ptr into a double slot (invalid IR). Reject cleanly instead — the
			// typed-subset equivalent of JS's last-write-wins on a differently
			// typed property.
			if !convertiblePure(f.Ty, dstTy) {
				return Value{}, fmt.Errorf("%d:%d: Object.assign: source field '%s' has a type incompatible with the target field's type — a merge that changes a field's type is not supported (this compiler's objects are a typed subset)", pos.Line, pos.Col, f.Name)
			}
			srcIdx, _, _ := srcVal.Ty.FieldIndex(f.Name)
			srcGep := e.freshReg()
			loadReg := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", srcGep, srcStructIR, srcVal.Ref, srcIdx))
			e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", loadReg, StructFieldIR(f.Ty), srcGep, f.Ty.Align()))
			val := e.convert(Value{Ref: loadReg, Ty: f.Ty}, dstTy)
			dstGep := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", dstGep, targetStructIR, targetVal.Ref, dstIdx))
			e.heapStore(StructFieldIR(dstTy), val.Ref, dstGep, dstTy.Align())
		}
	}
	return targetVal, nil
}

// dynamicAssign reports an Object.assign whose target or a source is `any`:
// the copy runs on the values' run-time properties, and the result is `any`
// (as TypeScript types it).
func (e *Emitter) dynamicAssign(args []ast.Expression) bool {
	for _, a := range args {
		if isUnconstrainedDynamic(e.inferExprType(a)) {
			return true
		}
	}
	// A primitive source (a string's indices; null, undefined, a number or
	// a boolean, which have no own enumerable keys) is read at run time.
	for _, a := range args[1:] {
		if st := e.inferExprType(a); !st.IsObject() && !st.IsArray() {
			return true
		}
	}
	// A source with a property the target's type lacks grafts it on, as in
	// JavaScript: the target then holds more than its type, so the result
	// (TypeScript's T & U) is read dynamically.
	if len(args) > 1 {
		tt := e.inferExprType(args[0])
		if tt.IsObject() && !tt.IsArray() {
			for _, a := range args[1:] {
				st := e.inferExprType(a)
				if !st.IsObject() {
					continue
				}
				for _, f := range st.VisibleFields() {
					if _, _, ok := tt.FieldIndex(f.Name); !ok {
						return true
					}
				}
			}
		}
	}
	return false
}

// emitDynObjectAssign is Object.assign over run-time properties: each
// source's own enumerable string keys (null and undefined sources skipped)
// read — getters run — and written to the target as an assignment through
// `any` writes them. The result is the target.
func (e *Emitter) emitDynObjectAssign(args []ast.Expression, pos ast.Pos) (Value, error) {
	tv, err := e.emitExprWithObjectHint(args[0], TypeAny)
	if err != nil {
		return Value{}, err
	}
	return e.emitDynObjectAssignOnto(tv, args, pos)
}

// emitDynObjectAssignOnto is emitDynObjectAssign onto an evaluated target
// (args[0] is not evaluated again).
func (e *Emitter) emitDynObjectAssignOnto(tv Value, args []ast.Expression, pos ast.Pos) (Value, error) {
	target, err := e.emitBoxValue(tv)
	if err != nil {
		return Value{}, err
	}
	for _, a := range args[1:] {
		sv, err := e.emitExprWithObjectHint(a, TypeAny)
		if err != nil {
			return Value{}, err
		}
		src, err := e.emitBoxValue(sv)
		if err != nil {
			return Value{}, err
		}
		if err := e.emitDynCopyOwnProps(target, src, pos); err != nil {
			return Value{}, err
		}
	}
	return target, nil
}

// emitDynCopyOwnProps copies the boxed src's own enumerable properties onto
// target through assignments, as Object.assign does: its string keys, then
// its symbols; a null or undefined src copies nothing.
func (e *Emitter) emitDynCopyOwnProps(target, src Value, pos ast.Pos) error {
	isU, isN, absent := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", isU, src.Ref, nbUndefined))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", isN, src.Ref, nbNull))
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", absent, isU, isN))
	copyL, nextL := e.freshLabel("dynassign.copy"), e.freshLabel("dynassign.next")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", absent, nextL, copyL))
	e.emitLabel(copyL)
	keys, err := e.emitDynAnyKeys(src, pos)
	if err != nil {
		return err
	}
	data, n := e.freshReg(), e.freshReg()
	data = e.arrayData(keys)
	n = e.arrayLen(keys)
	iSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", iSlot))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", iSlot))
	condL, bodyL := e.freshLabel("dynassign.cond"), e.freshLabel("dynassign.body")
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	i := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", i, iSlot))
	more := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, %s", more, i, n))
	symsL := e.freshLabel("dynassign.syms")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", more, bodyL, symsL))
	e.emitLabel(bodyL)
	kp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %s", kp, data, i))
	key := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", key, kp))
	v, err := e.emitDynAnyMemberGet(src, key, pos)
	if err != nil {
		return err
	}
	if _, err := e.emitDynAnyMemberSet(target, key, v, pos); err != nil {
		return err
	}
	i2 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", i2, i))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", i2, iSlot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	// Then its own symbol-keyed properties, as Object.assign copies them.
	e.emitLabel(symsL)
	syms, err := e.emitOwnEnumSymbolsOf(src)
	if err != nil {
		return err
	}
	sdata, sn := e.arrayData(syms), e.arrayLen(syms)
	jSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", jSlot))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", jSlot))
	scondL, sbodyL := e.freshLabel("dynassign.scond"), e.freshLabel("dynassign.sbody")
	e.emitTerminator(fmt.Sprintf("br label %%%s", scondL))
	e.emitLabel(scondL)
	j := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", j, jSlot))
	smore := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, %s", smore, j, sn))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", smore, sbodyL, nextL))
	e.emitLabel(sbodyL)
	sp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %s", sp, sdata, j))
	sym := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", sym, sp))
	skey := e.emitSymbolPropertyKey(Value{Ref: sym, Ty: SymbolType()})
	sv2, err := e.emitDynAnyMemberGet(src, skey, pos)
	if err != nil {
		return err
	}
	if _, err := e.emitDynAnyMemberSet(target, skey, sv2, pos); err != nil {
		return err
	}
	j2 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", j2, j))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", j2, jSlot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", scondL))
	e.emitLabel(nextL)
	return nil
}

// emitObjectFreeze implements Object.freeze(obj): marks obj's heap pointer
// in the global frozen-object set (ensureFrozenSet, runtime.go) and returns
// obj unchanged. Tracked by pointer, not by the variable/symbol that called
// freeze — matches real JS's per-value (not per-binding) semantics, so a
// write to the same object through a different alias or a function
// parameter is caught too, not just a write through the original variable.
//
// This compiler's objects are fixed-shape heap structs — no dynamic
// property add/delete exists at the language level at all yet, for any
// object, frozen or not — so freeze's "no new/no deleted fields" guarantee
// already holds structurally. The only thing freeze adds here is blocking
// writes to *existing* fields, enforced by emitFrozenCheck at every
// object-field write site (emitAssign's object-field-assignment branch,
// emitObjectAssign's target). A real dynamic property bag (add/delete at
// runtime) is a possible future direction — not designed or started here,
// tracked only as a note in docs/status/OBJECT-COLLECTIONS.md — and wouldn't change this function
// itself, only what "no dynamic add/delete" needs to actively enforce once
// it exists.
func (e *Emitter) emitObjectFreeze(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: Object.freeze takes 1 argument", pos.Line, pos.Col)
	}
	val, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	// A dynamic object freezes via its descriptor attrs (TDD-00155 Stage 5).
	if isUnconstrainedDynamic(val.Ty) {
		return e.emitDynPrevent(val, 2)
	}
	if !val.Ty.IsObject() {
		return Value{}, fmt.Errorf("%d:%d: Object.freeze requires an object", pos.Line, pos.Col)
	}
	e.emitStaticIntegrity(val, staticIntegrityFrozen)
	return val, nil
}

// emitHasOwnProperty backs Object.hasOwn(obj, key), obj.hasOwnProperty(key),
// and the `key in obj` operator (emitInOperator). Object shapes are fully
// structural/static in this compiler (every field a class/interface/
// object-literal type has is known at compile time), so the key must be a
// compile-time string literal — the result is then just a FieldIndex
// lookup, a compile-time-constant true/false, not a runtime property scan.
// A non-literal (runtime-computed) key is a clean compile error rather than
// a silent always-false/true, since there's no field-name table at runtime
// to check it against. callerName customizes the error text so it reads
// naturally regardless of which of the three call sites triggered it.
func (e *Emitter) emitHasOwnProperty(objExpr, keyExpr ast.Expression, callerName string, ownOnly bool, pos ast.Pos) (Value, error) {
	objVal, err := e.emitExpr(objExpr)
	if err != nil {
		return Value{}, err
	}
	// A bare any/unknown object is a runtime dynamic-object membership test
	// (TDD-00155 Stage 1) — the one case where the key may be runtime-computed.
	// So is a union's box (`"port" in x` on `A | P`): its object tells.
	if isUnconstrainedDynamic(objVal.Ty) || (objVal.Ty.IsDynamic() && len(objVal.Ty.UnionMembers) > 0) {
		keyRef, err := e.dynAnyKeyRef(keyExpr, pos)
		if err != nil {
			return Value{}, err
		}
		return e.emitDynAnyHas(Value{Ref: objVal.Ref, Ty: TypeAny}, keyRef, ownOnly, pos)
	}
	if box, ok, err := e.ownAccessorBox(objVal); ok || err != nil {
		if err != nil {
			return Value{}, err
		}
		keyRef, err := e.dynAnyKeyRef(keyExpr, pos)
		if err != nil {
			return Value{}, err
		}
		return e.emitDynAnyHas(box, keyRef, ownOnly, pos)
	}
	if objVal.Ty.IsArray() && !objVal.Ty.IsTuple() && !objVal.Ty.Nullable() {
		if v, ok, err := e.emitArrayHas(objVal, keyExpr, ownOnly); ok || err != nil {
			return v, err
		}
	}
	// A string-keyed dictionary is a Map underneath: membership is its has().
	if objVal.Ty.IsMap() && objVal.Ty.IsDynamicObject() && !objVal.Ty.Nullable() {
		if lit, ok := keyExpr.(*ast.StringLiteral); ok && !ownOnly && objectProtoKeys[lit.Value] {
			return Value{Ref: "true", Ty: TypeBool}, nil // inherited from Object.prototype
		}
		has, err := e.emitMapMethod(objVal.Ty, objVal.Ref, "has", []ast.Expression{keyExpr}, pos)
		if err != nil || ownOnly {
			return has, err
		}
		if _, ok := keyExpr.(*ast.StringLiteral); ok {
			return has, nil
		}
		// A runtime key may name an Object.prototype member. The key is
		// evaluated again only when it has no effects.
		if !isPureKeyExpr(keyExpr) {
			return has, nil
		}
		kv, err := e.emitExpr(keyExpr)
		if err != nil || !isStringTy(kv.Ty) || kv.Ty.Nullable() {
			return has, err
		}
		res := has.Ref
		for _, name := range sortedObjectProtoKeys {
			eq := e.emitStrEq(kv.Ref, e.internString(name))
			acc := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", acc, res, eq))
			res = acc
		}
		return Value{Ref: res, Ty: TypeBool}, nil
	}
	if !objVal.Ty.IsObject() && !ownOnly && !objVal.Ty.IsFunc() {
		return Value{}, fmt.Errorf("%d:%d: %s requires an object", pos.Line, pos.Col, callerName)
	}
	if !objVal.Ty.IsObject() {
		// A function, or a primitive's own properties (hasOwnProperty
		// converts it to its object): its boxed value answers at runtime.
		box, err := e.emitBoxValue(objVal)
		if err != nil {
			return Value{}, err
		}
		keyRef, err := e.dynAnyKeyRef(keyExpr, pos)
		if err != nil {
			return Value{}, err
		}
		return e.emitDynAnyHas(box, keyRef, ownOnly, pos)
	}
	// A structural type's value may be another layout or a bag: its own
	// keys answer (one branch runs, so the key is evaluated once).
	if isRecordView(objVal.Ty) && !objVal.Ty.Nullable() {
		return e.emitRecordSplit(objVal, TypeBool,
			func(v Value) (Value, error) { return e.emitHasOwnStatic(v, keyExpr, callerName, ownOnly, pos) },
			func(box Value) (Value, error) {
				keyRef, err := e.dynAnyKeyRef(keyExpr, pos)
				if err != nil {
					return Value{}, err
				}
				return e.emitDynAnyHas(box, keyRef, ownOnly, pos)
			})
	}
	return e.emitHasOwnStatic(objVal, keyExpr, callerName, ownOnly, pos)
}

// emitHasOwnStatic is emitHasOwnProperty over objVal's static layout.
func (e *Emitter) emitHasOwnStatic(objVal Value, keyExpr ast.Expression, callerName string, ownOnly bool, pos ast.Pos) (Value, error) {
	keyLit, ok := keyExpr.(*ast.StringLiteral)
	if !ok {
		if kt := e.inferExprType(keyExpr); isStringTy(kt) && !kt.Nullable() && !kt.IsDynamic() && !objVal.Ty.IsDynamic() {
			return e.emitStaticHasRuntimeKey(objVal, keyExpr, ownOnly)
		}
		return Value{}, fmt.Errorf("%d:%d: %s requires a string literal key (dynamic keys are not supported)", pos.Line, pos.Col, callerName)
	}
	_, fieldTy, found := objVal.Ty.FieldIndex(keyLit.Value)
	if !found && !ownOnly && (objectProtoKeys[keyLit.Value] || e.classProtoHas(objVal.Ty, keyLit.Value)) {
		return Value{Ref: "true", Ty: TypeBool}, nil // inherited: a prototype's
	}
	if found {
		// An optional field that was omitted has no key — `"age" in a` is a
		// runtime presence test, not a static true (ADR-01063).
		if jsonFieldSkippable(fieldTy) || isSettlementType(objVal.Ty) {
			f := Field{Name: keyLit.Value, Ty: fieldTy}
			for _, vf := range objVal.Ty.VisibleFields() {
				if vf.Name == keyLit.Value {
					f = vf
					break
				}
			}
			present, _ := e.emitFieldPresent(objVal.Ref, objVal.Ty, f)
			return Value{Ref: present, Ty: TypeBool}, nil
		}
		return Value{Ref: "true", Ty: TypeBool}, nil
	}
	// A key the shape lacks may still be an own property added at runtime
	// (`(o as any).b = 1`), held in the object's expando table.
	e.ensureShapeRuntime()
	has := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_shape_expando_has(ptr %s, ptr %s)", has, objVal.Ref, e.internString(keyLit.Value)))
	own := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i32 %s, 0", own, has))
	return Value{Ref: own, Ty: TypeBool}, nil
}

// emitStaticHasRuntimeKey tests a runtime string key against a fixed-shape
// object's own keys: equal to a field's name and, for an optional field,
// present.
func (e *Emitter) emitStaticHasRuntimeKey(objVal Value, keyExpr ast.Expression, ownOnly bool) (Value, error) {
	kv, err := e.emitExpr(keyExpr)
	if err != nil {
		return Value{}, err
	}
	res := "false"
	for _, f := range objVal.Ty.VisibleFields() {
		eq := e.emitStrEq(kv.Ref, e.internString(f.Name))
		if jsonFieldSkippable(f.Ty) || isSettlementType(objVal.Ty) {
			present, _ := e.emitFieldPresent(objVal.Ref, objVal.Ty, f)
			both := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", both, eq, present))
			eq = both
		}
		acc := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", acc, res, eq))
		res = acc
	}
	if !ownOnly {
		names := append([]string{}, sortedObjectProtoKeys...)
		if info, ok := e.classes[objVal.Ty.ClassName]; ok && objVal.Ty.IsClass() {
			var own []string
			for m := range info.MethodSigs {
				if n := strings.TrimPrefix(strings.TrimPrefix(m, "__kml_get_"), "__kml_set_"); !strings.HasPrefix(n, "__kml_") && !objectProtoKeys[n] {
					own = append(own, n)
				}
			}
			sort.Strings(own)
			names = append(names, own...)
		}
		for _, name := range names {
			eq := e.emitStrEq(kv.Ref, e.internString(name))
			acc := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", acc, res, eq))
			res = acc
		}
	}
	return Value{Ref: res, Ty: TypeBool}, nil
}

// classProtoHas reports a method or accessor a class instance's prototype
// chain holds.
func (e *Emitter) classProtoHas(t Type, name string) bool {
	if !t.IsClass() {
		return false
	}
	info, ok := e.classes[t.ClassName]
	if !ok {
		return false
	}
	for _, k := range []string{name, "__kml_get_" + name, "__kml_set_" + name} {
		if _, ok := info.MethodSigs[k]; ok {
			return true
		}
	}
	return false
}

// isPureKeyExpr reports a key expression evaluating it twice cannot tell.
func isPureKeyExpr(x ast.Expression) bool {
	switch k := x.(type) {
	case *ast.Identifier, *ast.StringLiteral:
		_ = k
		return true
	}
	return false
}

// objectProtoKeys are Object.prototype's own keys, which `in` finds on every
// ordinary object.
var objectProtoKeys = map[string]bool{
	"constructor": true, "hasOwnProperty": true, "isPrototypeOf": true,
	"propertyIsEnumerable": true, "toLocaleString": true, "toString": true,
	"valueOf": true, "__proto__": true, "__defineGetter__": true,
	"__defineSetter__": true, "__lookupGetter__": true, "__lookupSetter__": true,
}

var sortedObjectProtoKeys = func() []string {
	ks := make([]string, 0, len(objectProtoKeys))
	for k := range objectProtoKeys {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}()

// emitInOperator implements `key in obj` — real JS's `in` is a runtime
// property/prototype-chain scan, but this compiler's object shapes are
// fixed at compile time (no dynamic property add/delete, see
// OBJECT-COLLECTIONS.md), so it reduces to exactly the same compile-time
// FieldIndex lookup Object.hasOwn/obj.hasOwnProperty already use — reused
// directly rather than reimplemented. Note the argument order flip: `in`
// puts the key on the left and the object on the right, the opposite of
// hasOwnProperty(obj, key).
func (e *Emitter) emitInOperator(ex *ast.BinaryExpression) (Value, error) {
	return e.emitHasOwnProperty(ex.Right, ex.Left, "the 'in' operator", false, ex.GetPos())
}

// emitObjectSeal implements Object.seal(obj). Real JS's seal blocks adding
// or deleting properties but still allows mutating existing ones — this
// compiler's objects already can't gain or lose fields dynamically (see
// emitObjectFreeze's doc comment), so seal's entire guarantee already holds
// unconditionally for every object, sealed or not. A genuine no-op, not a
// scope-narrowed approximation of one: there is currently nothing for seal
// to additionally enforce.
func (e *Emitter) emitObjectSeal(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: Object.seal takes 1 argument", pos.Line, pos.Col)
	}
	val, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	// A dynamic object seals via its descriptor attrs (TDD-00155 Stage 5).
	if isUnconstrainedDynamic(val.Ty) {
		return e.emitDynPrevent(val, 1)
	}
	if !val.Ty.IsObject() {
		return Value{}, fmt.Errorf("%d:%d: Object.seal requires an object", pos.Line, pos.Col)
	}
	e.emitStaticIntegrity(val, staticIntegritySealed)
	return val, nil
}

// Static-object integrity levels, recorded in the object's header word
// (objheader.go, collections.c). A static object's shape is fixed at
// compile time, so the level only changes what writes and tests answer.
// Object.isSealed/isFrozen/isExtensible answer as JS does (TDD-00229).
const (
	staticIntegrityFrozen        = 1
	staticIntegritySealed        = 2
	staticIntegrityNonExtensible = 3
)

// emitStaticLevel is the integrity level of the static object v, an i64:
// 0 for a layout without a header word, which nothing can freeze.
func (e *Emitter) emitStaticLevel(v Value) string {
	if !hasObjHeader(v.Ty) {
		if !sideLevelObject(v.Ty) {
			return "0"
		}
		e.ensureIntegrityLevels()
		lv := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_side_level(ptr %s)", lv, v.Ref))
		return lv
	}
	e.ensureIntegrityLevels()
	lv := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_obj_level(ptr %s)", lv, v.Ref))
	return lv
}

// sideLevelObject reports an object without a header word whose integrity
// level a side table keeps (collections.c): a RegExp.
func sideLevelObject(t Type) bool {
	return t.IsRegExp() && !t.Nullable()
}

// emitStaticIntegrity raises v's recorded integrity level to `level`
// (frozen > sealed > non-extensible; a lower request never downgrades).
func (e *Emitter) emitStaticIntegrity(v Value, level int) {
	if !hasObjHeader(v.Ty) {
		if sideLevelObject(v.Ty) {
			e.ensureIntegrityLevels()
			e.emitInstr(fmt.Sprintf("call void @__kml_side_set_level(ptr %s, i64 %d)", v.Ref, level))
		}
		return
	}
	e.ensureIntegrityLevels()
	e.emitInstr(fmt.Sprintf("call void @__kml_obj_set_level(ptr %s, i64 %d)", v.Ref, level))
}

// emitStaticIntegrityTest answers Object.isExtensible (level 0), isSealed
// (1) or isFrozen (2) for a static object from its recorded level. As in JS,
// a non-extensible object with no own properties is also sealed and frozen.
func (e *Emitter) emitStaticIntegrityTest(val Value, level int) Value {
	lv := e.emitStaticLevel(val)
	empty := len(val.Ty.VisibleFields()) == 0
	r := e.freshReg()
	switch level {
	case 0: // isExtensible
		e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", r, lv))
	case 2: // isFrozen
		if empty {
			e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", r, lv))
		} else {
			e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", r, lv, staticIntegrityFrozen))
		}
	case 1: // isSealed
		if empty {
			e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", r, lv))
		} else {
			a := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", a, lv))
			b := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp ule i64 %s, %d", b, lv, staticIntegritySealed))
			e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", r, a, b))
		}
	}
	return Value{Ref: r, Ty: TypeBool}
}

// emitFrozenCheck emits a runtime guard in front of a write to the static
// object obj: if it is frozen, throws a catchable TypeError instead of
// letting the write proceed. Shared by every object-field write site —
// emitAssign's object-field-assignment branch and emitObjectAssign's
// target — so Object.freeze's guarantee holds no matter which write path a
// mutation goes through, not just plain `obj.field = val`.
func (e *Emitter) emitFrozenCheck(obj Value, name string) {
	if !hasObjHeader(obj.Ty) {
		return
	}
	level := e.emitStaticLevel(obj)
	isFrozen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", isFrozen, level, staticIntegrityFrozen))

	frozenL := e.freshLabel("frozen.reject")
	okL := e.freshLabel("frozen.ok")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isFrozen, frozenL, okL))

	e.emitLabel(frozenL)
	// A write to a frozen object is a TypeError in strict-mode JS, with V8's
	// message naming the property and the object ("Cannot assign to read
	// only property 'x' of object '#<A>'").
	if name != "" {
		e.emitStaticSetThrow(obj.Ref, e.internString(name), 1)
	} else {
		e.emitInternalThrowKind("TypeError", e.internString("Cannot assign to read only property of a frozen object"))
	}

	e.emitLabel(okL)
}

// emitStaticWriteRefused is an i1: a write to the static object's property
// name is refused, the object being frozen or the property read-only
// (emitFrozenCheck and emitReadOnlyCheck's tests, without the throw).
func (e *Emitter) emitStaticWriteRefused(obj Value, name string) string {
	frozen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", frozen, e.emitStaticLevel(obj), staticIntegrityFrozen))
	if !e.propAttrsUsed() {
		return frozen
	}
	ro := e.emitPropAttrTest(obj.Ref, name, pattrNotWritable)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", r, frozen, ro))
	return r
}

// registerCryptoSubtleAliases pre-scans top-level statements for the
// `const { subtle } = globalThis.crypto` binding (ADR-00434) so Pass 2
// function bodies see the alias before the statement itself emits.
func (e *Emitter) registerCryptoSubtleAliases(prog *ast.Program) {
	for _, stmt := range prog.Body {
		d, ok := stmt.(*ast.ObjectDestructuring)
		if !ok {
			continue
		}
		if g, _ := cryptoGlobalExpr(d.Init); !g {
			continue
		}
		if len(d.Props) == 1 && d.Props[0].Key == "subtle" && d.Props[0].Default == nil {
			if e.cryptoSubtleAliases == nil {
				e.cryptoSubtleAliases = map[string]bool{}
			}
			e.cryptoSubtleAliases[d.Props[0].Local] = true
		}
	}
}

// dynObjectKeyExpr normalizes a computed-key expression for the map-backed
// dynamic object: string keys pass through; a numeric key is wrapped in a
// synthetic String(...) conversion — JS object keys are strings, and a
// number index signature (`[i: number]: T`, ADR-00461) stores under the
// stringified key exactly as real JS does. Any other key type keeps the
// clean rejection.
func (e *Emitter) dynObjectKeyExpr(keyExpr ast.Expression, pos ast.Pos) (ast.Expression, error) {
	kt := e.inferExprType(keyExpr)
	if isStringTy(kt) {
		return keyExpr, nil
	}
	if kt.IR == "double" || kt.IR == "i64" || kt.IR == "i32" || kt.IR == "i16" || kt.IR == "i8" {
		return stringCall(keyExpr, pos), nil
	}
	return nil, fmt.Errorf("%d:%d: computed property key must be a string or number", pos.Line, pos.Col)
}

// homogeneousFieldType reports whether every field shares one storage type,
// and returns it — the "can Object.entries keep real typed values" test
// (ADR-00492). Compared on the storage IR plus the flags that change how a
// value behaves downstream; mixed shapes fall back to stringified values.
func homogeneousFieldType(fields []Field) (Type, bool) {
	if len(fields) == 0 {
		return TypePtr, false
	}
	first := fields[0].Ty
	for _, f := range fields[1:] {
		t := f.Ty
		if t.IR != first.IR || t.Float != first.Float || t.Signed != first.Signed ||
			t.IsArray() != first.IsArray() || t.IsFunc() != first.IsFunc() ||
			t.IsObject() != first.IsObject() || t.IsMap() != first.IsMap() {
			return TypePtr, false
		}
	}
	return first, true
}

// contextParamHints types a closure's parameters from the function type it
// is bound to, by position: a fixed parameter's own type, and at or past
// the expected type's rest slot its element type, or the whole rest array
// for the closure's own rest parameter (`(a, b) => …` against `(...args:
// any[]) => void` gives `a` and `b` any).
func contextParamHints(fnTy Type, params []ast.Param) []Type {
	fixed, rest := restOf(fnTy)
	if rest == nil {
		return fnTy.FuncParams
	}
	hints := make([]Type, len(params))
	for i, p := range params {
		switch {
		case i < len(fixed):
			hints[i] = fixed[i]
		case p.Rest:
			hints[i] = *rest
		default:
			hints[i] = restElem(*rest)
		}
	}
	return hints
}

// emitThisTakingClosure emits a function literal against a function type
// with a `this: T` parameter (FuncThis): a function expression's `this` is
// its leading parameter, which the caller fills with the receiver; an arrow
// keeps its lexical `this` and ignores that argument. ok is false for any
// other expression.
func (e *Emitter) emitThisTakingClosure(expr ast.Expression, hint Type) (Value, bool, error) {
	// A self-referencing interface (`handle: (this: Handler) => …` inside
	// Handler) captured a placeholder for its own type; take the live one.
	params := append([]Type(nil), hint.FuncParams...)
	params[0] = e.canonicalizeClassTy(params[0])
	hint.FuncParams = params
	var v Value
	var err error
	switch fn := expr.(type) {
	case *ast.FunctionExpression:
		// An explicit `this: T` is the function's own leading parameter.
		addThis := !e.maybeAddThisParamExpr(fn)
		cp := *fn
		if addThis {
			cp.Params = append([]ast.Param{{Name: "this"}}, fn.Params...)
		} else if e.implicitThis[fn] {
			// The receiver the slot's type names, not the bare method's any.
			cp.Params = append([]ast.Param{{Name: "this"}}, fn.Params[1:]...)
		}
		v, err = e.emitFunctionExpression(&cp, contextParamHints(hint, cp.Params))
	case *ast.ArrowFunction:
		cp := *fn
		if len(fn.Params) == 0 || fn.Params[0].Name != "__kml_this" {
			cp.Params = append([]ast.Param{{Name: "__kml_this"}}, fn.Params...)
		}
		v, err = e.emitArrowFunctionWithHints(&cp, contextParamHints(hint, cp.Params))
	default:
		return Value{}, false, nil
	}
	if err == nil {
		v.Ty.FuncThis = true
	}
	v, err = e.emitClosureAgainstHint(v, err)(hint, expr.GetPos())
	return v, true, err
}

// unionLiteralMember returns the one member of union u an object literal or
// function literal is built as: its only object member, or its only
// function member.
func unionLiteralMember(expr ast.Expression, u Type) (Type, bool) {
	var want func(Type) bool
	switch expr.(type) {
	case *ast.ObjectLiteral:
		// An object literal is never a class's instance (`string | URL |
		// RequestOptions`: the literal is the options).
		want = func(m Type) bool { return isUnionObjectMember(m) && !m.IsClass() }
	case *ast.ArrowFunction, *ast.FunctionExpression:
		want = func(m Type) bool { return m.IsFunc() }
	case *ast.ArrayLiteral:
		// An array literal is the tuple member a tuple is expected as.
		want = func(m Type) bool { return m.IsTuple() && !m.IsDynamic() }
	default:
		return Type{}, false
	}
	var found Type
	n := 0
	for _, m := range u.UnionMembers {
		if want(m) {
			found = m
			n++
		}
	}
	return found, n == 1
}

// isObjectCreateNull reports `Object.create(null)`.
func isObjectCreateNull(expr ast.Expression) bool {
	call, ok := expr.(*ast.CallExpression)
	if !ok || len(call.Args) != 1 {
		return false
	}
	mem, ok := call.Callee.(*ast.MemberExpression)
	if !ok || mem.Property != "create" {
		return false
	}
	if id, ok := mem.Object.(*ast.Identifier); !ok || id.Name != "Object" {
		return false
	}
	nl, ok := call.Args[0].(*ast.NullLiteral)
	return ok && !nl.IsUndefined
}

// assignAsSpread is `Object.assign({ …props }, a, b)` as the object literal
// `{ …props, ...a, ...b }`: a fresh literal target is referenced by nothing
// else, so filling it is building it, with every source's properties, typed
// as tsc types the call (the intersection of target and sources).
func assignAsSpread(args []ast.Expression) (*ast.ObjectLiteral, bool) {
	target, ok := args[0].(*ast.ObjectLiteral)
	if !ok || len(args) < 2 {
		return nil, false
	}
	props := append([]ast.ObjectProperty{}, target.Properties...)
	for _, src := range args[1:] {
		if _, isSpread := src.(*ast.SpreadElement); isSpread {
			return nil, false
		}
		props = append(props, ast.ObjectProperty{Value: ast.NewSpreadElement(src, src.GetPos())})
	}
	return ast.NewObjectLiteral(props, target.GetPos()), true
}

// emitObjectStatic is a call of an Object static (`Object.keys(o)`), the
// Object.* intrinsics' emitter. errNotObjectStatic: a form it does not
// emit (the caller's remaining paths decide).
func (e *Emitter) emitObjectStatic(prop string, ex *ast.CallExpression) (Value, error) {
	if f, ok := objectStatics[prop]; ok {
		return f(e, ex)
	}
	return Value{}, errNotObjectStatic
}

type objectStaticFn func(e *Emitter, ex *ast.CallExpression) (Value, error)

// objectStatics are the Object statics' emitters, by name.
var objectStatics map[string]objectStaticFn

func init() {
	args := func(f func(e *Emitter, args []ast.Expression, pos ast.Pos) (Value, error)) objectStaticFn {
		return func(e *Emitter, ex *ast.CallExpression) (Value, error) { return f(e, ex.Args, ex.GetPos()) }
	}
	objectStatics = map[string]objectStaticFn{
		"groupBy":                  args((*Emitter).emitObjectGroupBy),
		"keys":                     args((*Emitter).emitObjectKeys),
		"values":                   args((*Emitter).emitObjectValues),
		"entries":                  args((*Emitter).emitObjectEntries),
		"fromEntries":              args((*Emitter).emitObjectFromEntries),
		"assign":                   args((*Emitter).emitObjectAssign),
		"create":                   args((*Emitter).emitObjectCreate),
		"getPrototypeOf":           args((*Emitter).emitObjectGetPrototypeOf),
		"setPrototypeOf":           args((*Emitter).emitObjectSetPrototypeOf),
		"defineProperty":           args((*Emitter).emitObjectDefineProperty),
		"defineProperties":         args((*Emitter).emitObjectDefineProperties),
		"getOwnPropertyDescriptor": args((*Emitter).emitObjectGetOwnPropertyDescriptor),
		"getOwnPropertyNames":      args((*Emitter).emitObjectGetOwnPropertyNames),
		"getOwnPropertySymbols":    args((*Emitter).emitObjectGetOwnPropertySymbols),
		"hasOwn": func(e *Emitter, ex *ast.CallExpression) (Value, error) {
			if len(ex.Args) != 2 {
				return Value{}, fmt.Errorf("%d:%d: Object.hasOwn takes 2 arguments", ex.GetPos().Line, ex.GetPos().Col)
			}
			return e.emitHasOwnProperty(ex.Args[0], ex.Args[1], "Object.hasOwn", true, ex.GetPos())
		},
		"freeze":            arrayIntegrityFirst("freeze", args((*Emitter).emitObjectFreeze)),
		"seal":              arrayIntegrityFirst("seal", args((*Emitter).emitObjectSeal)),
		"preventExtensions": arrayIntegrityFirst("preventExtensions", integrityLevel("preventExtensions", 0)),
		"isExtensible":      arrayIntegrityFirst("isExtensible", integrityLevel("isExtensible", 0)),
		"isSealed":          arrayIntegrityFirst("isSealed", integrityLevel("isSealed", 1)),
		"isFrozen":          arrayIntegrityFirst("isFrozen", integrityLevel("isFrozen", 2)),
	}
}

// arrayIntegrityFirst is an integrity static whose argument may be a plain
// array, which carries its own integrity level.
func arrayIntegrityFirst(prop string, rest objectStaticFn) objectStaticFn {
	return func(e *Emitter, ex *ast.CallExpression) (Value, error) {
		if len(ex.Args) == 1 {
			if t := e.inferExprType(ex.Args[0]); t.IsArray() && !t.IsFlatArray() && !t.IsDynamic() && !t.Nullable() {
				return e.emitArrayIntegrityOp(prop, ex.Args[0])
			}
		}
		return rest(e, ex)
	}
}

// integrityLevel is preventExtensions, or a test of an integrity level
// (0 extensible, 1 sealed, 2 frozen), on a dynamic object (TDD-00155
// Stage 5), a static one (TDD-00229) or a primitive, which is frozen,
// sealed and not extensible.
func integrityLevel(prop string, level int) objectStaticFn {
	prevent := prop == "preventExtensions"
	return func(e *Emitter, ex *ast.CallExpression) (Value, error) {
		if len(ex.Args) == 1 && isUnconstrainedDynamic(e.inferExprType(ex.Args[0])) {
			v, err := e.emitExprWithObjectHint(ex.Args[0], TypeAny)
			if err != nil {
				return Value{}, err
			}
			if prevent {
				return e.emitDynPrevent(v, 0)
			}
			return e.emitDynFlagsTest(v, level)
		}
		if len(ex.Args) == 1 && e.inferExprType(ex.Args[0]).IsObject() {
			v, err := e.emitExpr(ex.Args[0])
			if err != nil {
				return Value{}, err
			}
			if prevent {
				e.emitStaticIntegrity(v, staticIntegrityNonExtensible)
				return v, nil
			}
			return e.emitStaticIntegrityTest(v, level), nil
		}
		if at := e.inferExprType(ex.Args[0]); len(ex.Args) == 1 && isPrimitiveIntegrityTy(at) {
			v, err := e.emitExpr(ex.Args[0])
			if err != nil || prevent {
				return v, err
			}
			return Value{Ref: fmt.Sprint(prop != "isExtensible"), Ty: TypeBool}, nil
		}
		return Value{}, errNotObjectStatic
	}
}

var errNotObjectStatic = errors.New("not an Object static")

// isPrimitiveIntegrityTy reports a type whose values are primitives: a
// number, string, boolean, bigint or symbol.
func isPrimitiveIntegrityTy(t Type) bool {
	if t.IsDynamic() || t.IsObject() || t.IsArray() || t.IsClass() || t.IsMap() || t.IsSet() || t.IsFunc() || t.Nullable() {
		return false
	}
	return isNumberTy(t) || isStringTy(t) || t.IR == "i1" || t.IsBigInt() || t.IsSymbol()
}

// emitArrayIntegrityOp is Object.freeze, seal, preventExtensions, isFrozen,
// isSealed or isExtensible of an array: its level lives in the frozen set,
// keyed by its shared header (arrayguard.c), which every mutation checks. A
// typed array with elements cannot be frozen or sealed (V8's TypeError).
func (e *Emitter) emitArrayIntegrityOp(prop string, arg ast.Expression) (Value, error) {
	v, err := e.emitExpr(arg)
	if err != nil {
		return Value{}, err
	}
	hdr, lenReg := e.arrayArgFromAggregate(v)
	v.Ref = hdr
	e.ensureArrayGuard()
	level := map[string]int{"freeze": staticIntegrityFrozen, "seal": staticIntegritySealed, "preventExtensions": staticIntegrityNonExtensible}
	if lvl, ok := level[prop]; ok {
		if v.Ty.IsTypedArray() && prop != "preventExtensions" {
			has := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", has, lenReg))
			badL, okL := e.freshLabel("ta.integrity.bad"), e.freshLabel("ta.integrity.ok")
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", has, badL, okL))
			e.emitLabel(badL)
			verb := map[string]string{"freeze": "freeze", "seal": "seal"}[prop]
			e.emitInternalThrowKind("TypeError", e.internString("Cannot "+verb+" array buffer views with elements"))
			e.emitLabel(okL)
		}
		e.emitArraySetLevel(hdr, lvl)
		return v, nil
	}
	which := map[string]int{"isFrozen": 0, "isSealed": 1, "isExtensible": 2}[prop]
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_array_integrity(ptr %s, i64 %d)", r, hdr, which))
	b := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i32 %s, 0", b, r))
	return Value{Ref: b, Ty: TypeBool}, nil
}

// emitArrayStatic is a call of an Array static (Array.isArray, Array.of,
// Array.from), the Array.* intrinsics' one emitter.
func (e *Emitter) emitArrayStatic(prop string, ex *ast.CallExpression) (Value, error) {
	if f, ok := arrayStatics[prop]; ok {
		return f(e, ex)
	}
	pos := ex.GetPos()
	return Value{}, fmt.Errorf("%d:%d: Array.%s is not supported", pos.Line, pos.Col, prop)
}

// arrayStatics are the Array constructor's static methods, by name.
var arrayStatics map[string]func(e *Emitter, ex *ast.CallExpression) (Value, error)

func init() {
	arrayStatics = map[string]func(e *Emitter, ex *ast.CallExpression) (Value, error){
		"isArray": (*Emitter).emitArrayIsArray,
		"of": func(e *Emitter, ex *ast.CallExpression) (Value, error) {
			return e.emitArrayOf(ex.Args, ex.GetPos())
		},
		"from": func(e *Emitter, ex *ast.CallExpression) (Value, error) {
			return e.emitArrayFrom(ex.Args, ex.GetPos())
		},
	}
}

// emitArrayIsArray is Array.isArray(v).
func (e *Emitter) emitArrayIsArray(ex *ast.CallExpression) (Value, error) {
	if len(ex.Args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: Array.isArray takes exactly 1 argument", ex.GetPos().Line, ex.GetPos().Col)
	}
	// A dynamic (`any`) argument's array-ness is only known at
	// runtime — a NaN-box can carry an array now and a plain object
	// the next line. Consult the box tag (a static-array box is
	// kmlTagArray, a D1 dynamic array kmlTagDynArray) rather than the
	// compile-time IsArray, which is always false for `any` and made
	// `Array.isArray(x)` wrongly return false for a genuine boxed
	// array (ADR-00934).
	argTy := e.inferExprType(ex.Args[0])
	if argTy.IsDynamic() {
		v, err := e.emitExpr(ex.Args[0])
		if err != nil {
			return Value{}, err
		}
		v, err = e.emitBoxValue(v)
		if err != nil {
			return Value{}, err
		}
		return e.emitDynIsArray(v), nil
	}
	if _, ok := e.arrayClassHost(argTy); ok && !argTy.Nullable() {
		// An instance of a class extending Array is an Array.
		if _, err := e.emitExpr(ex.Args[0]); err != nil {
			return Value{}, err
		}
		return Value{Ref: "true", Ty: TypeBool}, nil
	}
	if argTy.IsTypedArray() || argTy.IsBuffer() {
		// A TypedArray/Buffer is IsArray storage-wise but not a JS Array.
		if _, err := e.emitExpr(ex.Args[0]); err != nil {
			return Value{}, err
		}
		return Value{Ref: "false", Ty: TypeBool}, nil
	}
	if argTy.IsArray() {
		// A `T[] | undefined` value (a nested-array element absence,
		// TDD-00221): a miss is `undefined`, and `Array.isArray(undefined)`
		// is false — decide at runtime on the null header.
		if argTy.Nullable() {
			v, err := e.emitExpr(ex.Args[0])
			if err != nil {
				return Value{}, err
			}
			isArr := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = xor i1 %s, true", isArr, e.emitArrayIsAbsent(v)))
			return Value{Ref: isArr, Ty: TypeBool}, nil
		}
		v, err := e.emitExpr(ex.Args[0])
		if err != nil {
			return Value{}, err
		}
		// An any[] may hold an Arguments object, which is no Array.
		if mayBeArguments(argTy) && v.Ref != "zeroinitializer" {
			not := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = xor i1 %s, true", not, e.emitArrayIsArguments(v.Ref)))
			return Value{Ref: not, Ty: TypeBool}, nil
		}
		return Value{Ref: "true", Ty: TypeBool}, nil
	}
	// The argument runs whatever its type (`Array.isArray(f())`); a tuple is
	// an Array.
	if _, err := e.emitExpr(ex.Args[0]); err != nil {
		return Value{}, err
	}
	if argTy.IsTuple() && !argTy.Nullable() {
		return Value{Ref: "true", Ty: TypeBool}, nil
	}
	return Value{Ref: "false", Ty: TypeBool}, nil
}

// arrayFromCall is `Array.from(args)` as the compiler writes it: the call
// names its intrinsic, since no declaration reaches synthesized source.
// stringCall is a synthesized `String(x)`: the conversion, named directly.
func stringCall(x ast.Expression, pos ast.Pos) *ast.CallExpression {
	c := ast.NewCallExpression(ast.NewIdentifier("String", pos), []ast.Expression{x}, pos)
	c.Intrinsic = "String"
	return c
}

func arrayFromCall(pos ast.Pos, args []ast.Expression) *ast.CallExpression {
	c := ast.NewCallExpression(ast.NewMemberExpression(ast.NewIdentifier("Array", pos), "from", pos), args, pos)
	c.Intrinsic = "Array.from"
	return c
}

// ptrShapedBranch reports whether a ternary branch is held in one pointer
// (an object, a dictionary, a literal of either), not an array.
func (e *Emitter) ptrShapedBranch(x ast.Expression) bool {
	switch x.(type) {
	case *ast.ObjectLiteral, *ast.ArrayLiteral: // built against the hint
		return true
	}
	t := e.inferExprType(x)
	return t.IR == "ptr" && (!t.IsArray() || t.IsTuple()) && !t.IsDynamic()
}

// emitConditionalHinted is `c ? a : b` stored where an object of type hint
// is expected: each branch built against hint, as each would be on its own.
func (e *Emitter) emitConditionalHinted(ce *ast.ConditionalExpression, hint Type) (Value, error) {
	thenL, elseL, mergeL := e.freshLabel("ternary.then"), e.freshLabel("ternary.else"), e.freshLabel("ternary.merge")
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
	cond, err := e.emitExpr(ce.Test)
	if err != nil {
		return Value{}, err
	}
	cond = e.toBool(cond)
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", cond.Ref, thenL, elseL))
	for _, br := range []struct {
		label string
		expr  ast.Expression
	}{{thenL, ce.Consequent}, {elseL, ce.Alternate}} {
		e.emitLabel(br.label)
		v, err := e.emitExprWithObjectHint(br.expr, hint)
		if err != nil {
			return Value{}, err
		}
		v = e.convert(v, hint)
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", v.Ref, slot))
		e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
	}
	e.emitLabel(mergeL)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", r, slot))
	return Value{Ref: r, Ty: hint}, nil
}

// emitObjectEntriesOfArray is Object.entries of an array: a [string, T] tuple per
// element, its index as a string and the element.
func (e *Emitter) emitObjectEntriesOfArray(arr Value) (Value, error) {
	elemTy := *arr.Ty.ElemType
	entryTy := TupleType([]Type{TypePtr, elemTy})
	data, n := e.freshReg(), e.freshReg()
	data = e.arrayData(arr)
	n = e.arrayLen(arr)
	e.ensureMalloc()
	bytes, out := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, 8", bytes, n))
	e.heapMallocInto(out, bytes)
	iSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", iSlot))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", iSlot))
	condL, bodyL, doneL := e.freshLabel("arrent.cond"), e.freshLabel("arrent.body"), e.freshLabel("arrent.done")
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	i, more := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", i, iSlot))
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, %s", more, i, n))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", more, bodyL, doneL))
	e.emitLabel(bodyL)
	key, err := e.emitValueToString(Value{Ref: i, Ty: TypeI64})
	if err != nil {
		return Value{}, err
	}
	src := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", src, StructFieldIR(elemTy), data, i))
	el := e.loadArrayElem(src, elemTy)
	entry := e.freshReg()
	e.heapMallocInto(entry, fmt.Sprint(entryTy.StructSize()))
	kSlot, vSlot := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 0", kSlot, entryTy.StructIR(), entry))
	e.heapStore("ptr", key.Ref, kSlot, 8)
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 1", vSlot, entryTy.StructIR(), entry))
	if elemTy.IsArray() {
		e.storeArrayFieldHeader(vSlot, el)
	} else {
		e.heapStore(StructFieldIR(elemTy), el.Ref, vSlot, elemTy.Align())
	}
	dst, next := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %s", dst, out, i))
	e.heapStore("ptr", entry, dst, 8)
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", next, i))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", next, iSlot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(doneL)
	r1 := e.newArrayHeader(out, n)
	return Value{Ref: r1, Ty: ArrayOf(entryTy)}, nil
}

// emitArrayClassOwnValues is Object.values (Object.entries with entries) of
// an instance of a class extending Array: its elements, then its own
// fields' values (as [key, value] pairs), boxed when a field's type is not
// the elements'. ok is false for any other value.
func (e *Emitter) emitArrayClassOwnValues(arg ast.Expression, entries bool, pos ast.Pos) (Value, bool, error) {
	t := e.inferExprType(arg)
	host, ok := e.arrayClassHost(t)
	if !ok || t.Nullable() {
		return Value{}, false, nil
	}
	inst, err := e.emitExpr(arg)
	if err != nil {
		return Value{}, true, err
	}
	instID := e.bindValue(inst, pos)
	body, _, err := e.hostArrayReceiver(instID)
	if err != nil {
		return Value{}, true, err
	}
	valTy := *host.ElemType
	fields := esOrderedFields(t.VisibleFields())
	for _, f := range fields {
		if f.Ty.IR != valTy.IR || f.Ty.IsDynamic() != valTy.IsDynamic() || isStringTy(f.Ty) != isStringTy(valTy) {
			valTy = TypeAny
		}
	}
	method := "values"
	resTy := ArrayOf(valTy)
	if entries {
		method = "entries"
		resTy = ArrayOf(TupleType([]Type{TypePtr, valTy}))
	}
	call := ast.NewCallExpression(ast.NewMemberExpression(ast.NewIdentifier("Object", pos), method, pos), []ast.Expression{body}, pos)
	call.Intrinsic = "Object." + method
	elems := []ast.Expression{ast.NewSpreadElement(call, pos)}
	for _, f := range fields {
		var item ast.Expression = ast.NewMemberExpression(instID, f.Name, pos)
		if entries {
			item = ast.NewArrayLiteral([]ast.Expression{ast.NewStringLiteral(f.Name, pos), item}, pos)
		}
		elems = append(elems, item)
	}
	v, err := e.emitExprWithObjectHint(ast.NewArrayLiteral(elems, pos), resTy)
	return v, true, err
}

// falsyUnionSpreadSource is a boxed spread operand whose type is one
// statically shaped object beside definitely falsy members (`false | { j:
// number }`), as that object's layout, null where the box holds no object:
// a falsy primitive spreads nothing, as an absent source does.
func (e *Emitter) falsyUnionSpreadSource(arg ast.Expression, v Value) (Value, bool) {
	r, ok := e.falsyUnionSpreadType(arg, v.Ty)
	if !ok {
		return Value{}, false
	}
	tag, payload := e.emitUnboxTagPayload(v)
	isObj := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isObj, tag, kmlTagObject))
	p := e.emitIntToPtr(payload)
	sel := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr null", sel, isObj, p))
	r.setNullable(true)
	return Value{Ref: sel, Ty: r}, true
}

// falsyUnionSpreadType is the static object layout a boxed spread operand
// of type ty holds beside its definitely falsy members (falsyUnionSpreadSource).
func (e *Emitter) falsyUnionSpreadType(arg ast.Expression, ty Type) (Type, bool) {
	c := e.front()
	if c == nil || !ty.IsDynamic() || isUnconstrainedDynamic(ty) {
		return Type{}, false
	}
	t := c.TypeOf(arg)
	if c.Unanswered(t) {
		return Type{}, false
	}
	obj := c.WithoutFalsy(t)
	if obj == nil || obj == t {
		return Type{}, false
	}
	r, ok := e.repr().of(obj)
	if !ok || !r.IsObject() || r.IsDynamic() || r.IsClass() {
		return Type{}, false
	}
	return r, true
}
