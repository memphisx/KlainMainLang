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
	if !fnHint.IsFunc || fnHint.IsDynamic {
		fnHint = Type{}
		for _, m := range hint.UnionMembers {
			if m.IsFunc && !m.IsDynamic {
				fnHint = m
			}
		}
	}
	if call, ok := expr.(*ast.CallExpression); ok && fnHint.IsFunc && !fnHint.FuncHasRest && len(call.Args) > 0 && e.isIdentityWrapperCall(call) {
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
			if v.Ty.IsFunc {
				return v, nil
			}
			if c, ok := e.emitAnyToClosure(e.coerce(v, TypeAny), hint); ok {
				return c, nil
			}
			return v, nil
		}
	}
plain:
	// An array literal stored where any is expected is an array of anything
	// (`const a: any = [1]; a.push({})`), not its elements' own type.
	if lit, ok := expr.(*ast.ArrayLiteral); ok && isUnconstrainedDynamic(hint) && !hint.IsCaught {
		v, err := e.emitExprWithObjectHint(lit, ArrayOf(TypeAny))
		if err != nil {
			return Value{}, err
		}
		return e.coerce(v, hint), nil
	}
	// An array literal where a union with one array member is expected is
	// built as that member (`["a", 2]` for `string | (string | number)[]`).
	if lit, ok := expr.(*ast.ArrayLiteral); ok && hint.IsDynamic && len(hint.UnionMembers) > 0 {
		var arr *Type
		n := 0
		for i := range hint.UnionMembers {
			if hint.UnionMembers[i].IsArray {
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
	if hint.IsDynamicObject && hint.IsMap && isObjectCreateNull(expr) {
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
	if hint.IsDynamic {
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
	if hint.IsFunc && hint.FuncThis {
		if v, ok, err := e.emitThisTakingClosure(expr, hint); ok {
			return v, err
		}
	}
	// A closure bound into a known function type (`const f: F = …`, a
	// function-typed argument slot): emit it against the expected type's
	// parameter types so its optionality ABI matches the slot (ADR-00963).
	if hint.IsFunc {
		if af, ok := expr.(*ast.ArrowFunction); ok {
			return e.emitClosureAgainstHint(e.emitArrowFunctionWithHints(af, contextParamHints(hint, af.Params)))(hint, af.GetPos())
		}
		if fe, ok := expr.(*ast.FunctionExpression); ok {
			return e.emitClosureAgainstHint(e.emitFunctionExpression(fe, contextParamHints(hint, fe.Params)))(hint, fe.GetPos())
		}
	}
	// A ternary into an object or dictionary slot builds each branch against
	// it (`const init: Init = c ? read() : {}`: the `{}` is the dictionary).
	if ce, ok := expr.(*ast.ConditionalExpression); ok && (hint.IsObject || hint.IsDynamicObject) && hint.IR == "ptr" && !hint.IsDynamic &&
		!hint.IsArray && e.ptrShapedBranch(ce.Consequent) && e.ptrShapedBranch(ce.Alternate) {
		return e.emitConditionalHinted(ce, hint)
	}
	if lit, ok := expr.(*ast.ObjectLiteral); ok && (hint.IsObject || hint.IsDynamicObject) {
		return e.emitObjectLiteralWithHint(lit, &hint)
	}
	if lit, ok := expr.(*ast.ArrayLiteral); ok && hint.IsTuple {
		return e.emitTupleLiteral(lit, lit.Elements, hint)
	}
	if lit, ok := expr.(*ast.ArrayLiteral); ok && hint.IsArray {
		return e.emitArrayLiteralAggregate(lit, hint.ElemType)
	}
	if na, ok := expr.(*ast.NewArrayExpression); ok && na.ElemType == nil && hint.IsArray {
		return e.emitNewArraySizedAggregate(na, *hint.ElemType)
	}
	// `Promise.resolve(v)` into a declared `Promise<any>` slot must box the
	// value at the store so a later `.then`/`await` reads it back correctly
	// (see emitPromiseResolve). The hint reaches here from a typed var-init,
	// argument, or array element; without it the call self-types
	// `Promise<typeof v>` and stores the raw scalar bits.
	if ce, ok := expr.(*ast.CallExpression); ok && hint.IsPromise {
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
	if af, ok := expr.(*ast.ArrowFunction); ok && hint.IsFunc {
		return e.emitClosureAgainstHint(e.emitArrowFunctionWithHints(af, contextParamHints(hint, af.Params)))(hint, af.GetPos())
	}
	// Same hint propagation, for a function expression assigned/passed into
	// a declared function-typed slot (`let cb: (b: Box) => number =
	// function(b) { return b.value; }`) — function expressions need the
	// same outside-context typing an arrow function does (TDD-00060).
	if fe, ok := expr.(*ast.FunctionExpression); ok && hint.IsFunc {
		return e.emitClosureAgainstHint(e.emitFunctionExpression(fe, contextParamHints(hint, fe.Params)))(hint, fe.GetPos())
	}
	v, err := e.emitExpr(expr)
	if err == nil && hint.IsArray && isSelfDescribingBox(v.Ty) {
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

// emitObjectLiteralWithHint is emitObjectLiteral's real implementation. When
// hint is non-nil and IsObject, the literal is built against hint's declared
// field layout (types, and therefore struct size/GEP indices) instead of the
// literal's own self-inferred one — see docs/tdd/TDD-00007.md for why this
// is the fix (the coercion mechanism, `storeField`'s `e.coerce(val,
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
		return e.emitDynamicObjectLiteral(lit)
	}
	if lit.HasAccessors() {
		return e.emitObjectLiteralWithAccessors(lit)
	}
	// A spread of a bare any value has no compile-time key set: the literal
	// is a D1 dynamic object, whatever the hint (ADR-01077).
	if e.hasDynamicSpread(lit) || hasProtoKey(lit) {
		return e.emitDynObjLiteral(lit)
	}
	// A plain object literal assigned to a string index-signature target
	// (TDD-00130) is built as a map, not a fixed struct, so `d[key]` access
	// works — the same map-backed representation a computed-key literal uses.
	if hint != nil && hint.IsDynamicObject {
		return e.emitDynamicObjectLiteralAs(lit, hint)
	}
	ty := e.inferObjectType(lit)
	if hint != nil && hint.IsObject {
		ty = *hint
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
		if f.Ty.IsDynamic && !f.Ty.IsArray && StructFieldIR(f.Ty) == "i64" {
			g := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", g, structIR, dataReg, i))
			e.emitInstr(fmt.Sprintf("store i64 %d, ptr %s, align 8", nbUndefined, g))
		}
	}

	storeField := func(name string, val Value) error {
		idx, fieldTy, ok := ty.FieldIndex(name)
		if !ok {
			return fmt.Errorf("%d:%d: object has no field '%s'", lit.GetPos().Line, lit.GetPos().Col, name)
		}
		if !coerciblePure(val.Ty, fieldTy) {
			return fmt.Errorf("%d:%d: field '%s' is assigned a value of an incompatible type — this compiler is a typed subset", lit.GetPos().Line, lit.GetPos().Col, name)
		}
		gepReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gepReg, structIR, dataReg, idx))
		e.storeScalarOrNullableField(gepReg, fieldTy, val)
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
		if !isObjLit && !isArrLit && !coerciblePure(e.inferExprType(expr), fieldTy) {
			return fmt.Errorf("%d:%d: field '%s' is assigned a value of an incompatible type — this compiler is a typed subset", lit.GetPos().Line, lit.GetPos().Col, name)
		}
		gepReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gepReg, structIR, dataReg, idx))
		return e.storeScalarOrNullableFieldExpr(gepReg, fieldTy, expr)
	}

	for _, prop := range lit.Properties {
		if spread, ok := prop.Value.(*ast.SpreadElement); ok && prop.Key == "" {
			srcVal, err := e.emitExpr(spread.Arg)
			if err != nil {
				return Value{}, err
			}
			if !srcVal.Ty.IsObject {
				return Value{}, fmt.Errorf("%d:%d: spread in object literal requires an object value", spread.GetPos().Line, spread.GetPos().Col)
			}
			// `{ ...undefined }` / `{ ...null }` copies nothing: an absent
			// optional source leaves its (optional) fields absent.
			skipL := ""
			if srcVal.Ty.Nullable || srcVal.Ty.IsNull {
				isNull := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, srcVal.Ref))
				copyL := e.freshLabel("objspread.copy")
				skipL = e.freshLabel("objspread.skip")
				e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNull, skipL, copyL))
				e.emitLabel(copyL)
			}
			for _, f := range srcVal.Ty.VisibleFields() {
				// Only the source's own properties are copied: an absent
				// optional field leaves the target's value (an earlier
				// property's) as it is. Object spread is a shallow copy, so an
				// array field shares the same array (header), as in JS.
				srcIdx, _, _ := srcVal.Ty.FieldIndex(f.Name)
				srcGep := e.emitRecordGep(srcVal.Ref, srcVal.Ty, srcIdx, f.Ty, f.Name)
				present, fv := e.emitFieldPresentAt(srcGep, f)
				doneL := ""
				if present != "true" {
					storeL := e.freshLabel("objspread.field")
					doneL = e.freshLabel("objspread.next")
					e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", present, storeL, doneL))
					e.emitLabel(storeL)
				}
				if err := storeField(f.Name, fv); err != nil {
					return Value{}, err
				}
				if doneL != "" {
					e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
					e.emitLabel(doneL)
				}
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
	return Value{Ref: dataReg, Ty: ty}, nil
}

// emitDynamicObjectLiteral builds a Map<string,V>-backed value for an object
// literal that has at least one computed property key (`{ [expr]: value }`).
// Storage-wise this IS a Map<string,V> (see inferDynamicObjectType and
// docs/tdd/TDD-00012.md) — construction is just a sequence of the existing
// Map .set() calls, reusing emitMapCall verbatim rather than hand-rolling new
// set-emission code. A static key in a mixed literal (`{ x: 1, [k]: 2 }`)
// becomes an interned string-literal key into the same map.
func (e *Emitter) emitDynamicObjectLiteral(lit *ast.ObjectLiteral) (Value, error) {
	return e.emitDynamicObjectLiteralAs(lit, nil)
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
	if as != nil && as.IsDynamicObject && as.MapKey != nil && isStringTy(*as.MapKey) {
		ty = *as
		ty.Nullable, ty.IsUndefined = false, false
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
		return e.coerce(v, ft), nil
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
		if valTy.IsDynamic {
			// A box: a missing key is undefined.
			has := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_map_str_has(ptr %s, ptr %s)", has, mapPtr, kRef))
			sel := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %d", sel, has, rawReg, nbUndefined))
			cur = Value{Ref: sel, Ty: valTy}
		}
		// `??=` on a non-ptr value type can never trigger (no null to coalesce).
		if op == "??=" && valTy.IR != "ptr" && !valTy.IsDynamic && !isF64Slot(valTy) {
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
			if valTy.IsDynamic {
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
				e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", nullReg, cur.Ref))
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
		rhsVal = e.coerce(rhsVal, valTy)
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
		if err := dateCompoundAssignGuard(op, valTy.IsDate, rhsVal.Ty.IsDate); err != nil {
			return Value{}, fmt.Errorf("%d:%d: %s", pos.Line, pos.Col, err)
		}
		rhsVal = e.coerce(rhsVal, valTy)
		rhs, err = e.emitArith(strings.TrimSuffix(op, "="), cur, rhsVal, valTy, pos)
		if err != nil {
			return Value{}, err
		}
	}
	rhs = e.coerce(rhs, valTy)
	vRef := e.valueToMapVal(rhs, valTy)
	e.ensureMapStrHelpers()
	e.emitInstr(fmt.Sprintf("call void @__kml_map_str_set(ptr %s, ptr %s, i64 %s)", mapPtr, kRef, vRef))
	return rhs, nil
}

// emitDeclJSONProjection detects a var-decl initializer that needs the
// declaration's target type for correct field projection (TDD-00077 P3): a
// `JSON.parse(...)` or a `Response.json()` call, optionally wrapped in `await`.
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
			// — the body may still be arriving, or be a stream.
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
		val, err := e.emitResponseJSON(mem.Object, ty, ce.GetPos())
		return val, true, err
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
		emit := e.emitInstr
		if v.Kind == "var" {
			emit = e.emitAlloca
		}
		emit(fmt.Sprintf("%s = call ptr @malloc(i64 8)", ptrName))
		emit(fmt.Sprintf("store ptr null, ptr %s, align 8", ptrName))
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
		e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", ptrName))
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
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", ref, finalPtr))
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
		if ty.IsTuple {
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
		if ty.IsDynamicObject && plainRecordType(val.Ty) {
			val = e.coerce(val, ty) // its fields become the dictionary's entries
		} else if val.Ty.IsDynamic && ty.IsClass || ty.NullAndUndef {
			// An `any` result (an implementation signature) into a class
			// binding: the instance, unboxed; null into a three-state one.
			val = e.coerce(val, ty)
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
		// iterator example; see docs/adr/ADR-00064.md. Hinted, so an object
		// of another layout is converted to the declared one.
		val, err := e.emitExprWithObjectHint(init, ty)
		if err != nil {
			return err
		}
		if val.Ty.IsDynamic && !ty.IsDynamic || ty.IsDynamicObject && plainRecordType(val.Ty) || ty.NullAndUndef {
			val = e.coerce(val, ty) // converted to the declared layout
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
	objPtr, objTy, err := e.resolveObjectPtr(s.Init, s.GetPos())
	if err != nil {
		return err
	}
	return e.unpackObjectPatternInto(objPtr, objTy, s.Props, s.GetPos())
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
			return fmt.Errorf("%d:%d: object has no field '%s'", pos.Line, pos.Col, prop.Key)
		}
		fieldTy = e.canonicalizeClassTy(fieldTy)

		// Nested sub-pattern at this key (`{ k: [a, b] }` / `{ k: { a } }`,
		// TDD-00065 Stage 2) — destructure field k's own value with the
		// sub-pattern rather than binding a leaf Local. A `= default`
		// combined with a nested pattern isn't supported yet.
		if prop.SubArray != nil || prop.SubObject != nil {
			if prop.Default != nil {
				return fmt.Errorf("%d:%d: a default value on a nested destructuring pattern is not yet supported", pos.Line, pos.Col)
			}
			fieldGep := e.emitRecordGep(objPtr, objTy, idx, fieldTy, prop.Key)
			if prop.SubArray != nil {
				if !fieldTy.IsArray || fieldTy.ElemType == nil {
					return fmt.Errorf("%d:%d: cannot array-destructure non-array field '%s'", pos.Line, pos.Col, prop.Key)
				}
				aggReg := e.loadArrayFieldValue(fieldGep, fieldTy).Ref // header-ptr slot (TDD-00213 S2)
				dp := e.freshReg()
				lv := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", dp, aggReg))
				e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 1", lv, aggReg))
				if err := e.unpackArrayPatternInto(dp, lv, *fieldTy.ElemType, prop.SubArray); err != nil {
					return err
				}
				continue
			}
			if !fieldTy.IsObject {
				return fmt.Errorf("%d:%d: cannot object-destructure non-object field '%s'", pos.Line, pos.Col, prop.Key)
			}
			objp := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", objp, fieldGep))
			if err := e.unpackObjectPatternInto(objp, fieldTy, prop.SubObject, pos); err != nil {
				return err
			}
			continue
		}

		if prop.Default != nil && !fieldTy.IsUndefined {
			// A default replaces only undefined, which this field never holds
			// (a `T | null` field keeps its null); the default is not evaluated.
			prop.Default = nil
		}
		if prop.Default != nil && !(fieldTy.Nullable && fieldTy.IR == "ptr") && !isNullableScalar(fieldTy) {
			return fmt.Errorf("%d:%d: a destructuring default requires field '%s' to be nullable/optional (T | null, T | undefined, or `key?: T`) — no other field type has a reliable way to tell a real value apart from 'not provided'", pos.Line, pos.Col, prop.Key)
		}
		gepReg := e.emitRecordGep(objPtr, objTy, idx, fieldTy, prop.Key)
		if fieldTy.IsArray {
			// A destructured array-typed field needs a real, named array
			// Symbol (two allocas — Ptr/LenPtr) like any other array local
			// variable, not a single alloca of the {ptr,i64} storage slot
			// itself — otherwise later uses of this binding (e.g. .push(),
			// which needs LenPtr to write a resized length back to) would
			// find no LenPtr at all. See docs/adr/ADR-00061.md.
			// The binding is the field's own array (one header: `w.push(x)`
			// is `o.w.push(x)`), null when the field holds null.
			header := e.loadArrayFieldValue(gepReg, fieldTy).ArrayHeader
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
				if !defVal.Ty.IsArray {
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
				if fieldTy.NullAndUndef {
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
				defVal = e.coerce(defVal, fieldTy.withoutNullable())
				e.storeNullableScalarPresent(nsPtr, fieldTy, defVal.Ref)
				e.emitTerminator(fmt.Sprintf("br label %%%s", afterL))
				e.emitLabel(afterL)
			}
			e.define(prop.Local, Symbol{Ptr: nsPtr, Ty: fieldTy, NullableBoxed: true})
			continue
		}
		valReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", valReg, fieldTy.IR, gepReg, fieldTy.Align()))
		localPtr := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", localPtr, fieldTy.IR, fieldTy.Align()))

		if prop.Default != nil {
			isNullReg := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNullReg, valReg))
			absentL := e.freshLabel("destr.absent")
			presentL := e.freshLabel("destr.present")
			afterL := e.freshLabel("destr.after")
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNullReg, absentL, presentL))

			e.emitLabel(absentL)
			defVal, err := e.emitExpr(prop.Default)
			if err != nil {
				return err
			}
			defVal = e.coerce(defVal, fieldTy)
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
	restTy := ObjectType(residual)

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
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", fieldIR, loadReg, destGep, f.Ty.Align()))
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
		if !found || !sym.Ty.IsObject {
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
		if !val.Ty.IsObject && !isUnconstrainedDynamic(val.Ty) {
			return "", Type{}, fmt.Errorf("%d:%d: function call does not return an object", pos.Line, pos.Col)
		}
		return val.Ref, val.Ty, nil

	case *ast.AwaitExpression:
		// `const { value, done } = await reader.read()` / `await gen.next()`
		// (TDD-00097): await the promise, destructure its object result.
		val, err := e.emitAwait(src)
		if err != nil {
			return "", Type{}, err
		}
		if !val.Ty.IsObject && !isUnconstrainedDynamic(val.Ty) {
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
	if t := e.inferExprType(init); t.IsObject {
		val, err := e.emitExpr(init)
		if err != nil {
			return "", Type{}, err
		}
		if val.Ty.IsObject {
			return val.Ref, val.Ty, nil
		}
	}
	return "", Type{}, fmt.Errorf("%d:%d: object destructuring requires an object variable, function call, or object literal", pos.Line, pos.Col)
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
	val, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	// A bare any/unknown value: runtime key enumeration on a D1 dynamic object
	// (TDD-00155 Stage 1) — [] for primitives, TypeError for null/undefined.
	if isUnconstrainedDynamic(val.Ty) {
		return e.emitDynAnyKeys(val, pos)
	}
	// A caught value (`catch (e)`): whatever was thrown, boxed.
	if val.Ty.IsCaught {
		return e.emitDynAnyKeys(e.emitCaughtToAny(val), pos)
	}
	// An Error's own keys are not its layout's fields: its system fields that
	// are set, its subclass's own, its added ones — the boxed walk's.
	if e.isErrorValue(val.Ty) && !val.Ty.Nullable {
		box, err := e.emitBoxValue(val)
		if err != nil {
			return Value{}, err
		}
		return e.emitDynAnyKeys(box, pos)
	}
	// A function's own enumerable keys (TDD-00229): a bound native function's
	// own-property bag; a closure has none.
	if val.Ty.IsFunc {
		return Value{Ref: "{ ptr null, i64 0 }", Ty: ArrayOf(TypePtr)}, nil
	}
	// A dynamic object (or any string-keyed Map<string,V>) is backed by the
	// same runtime as Map<K,V> — delegate to its own .keys() rather than
	// walking a compile-time field list, see docs/tdd/TDD-00012.md.
	if val.Ty.IsMap {
		if val.Ty.MapKey == nil || !isStringTy(*val.Ty.MapKey) {
			return Value{}, fmt.Errorf("%d:%d: Object.keys requires a string-keyed Map or dynamic object", pos.Line, pos.Col)
		}
		return e.emitMapMethod(val.Ty, val.Ref, "keys", nil, pos)
	}
	// A zero-field class (methods-only) has genuinely known, just-empty
	// fields — unlike a plain object literal, whose Fields being empty means
	// "unknown", so only the non-class case treats emptiness as an error.
	if !val.Ty.IsObject || (!val.Ty.IsClass && !val.Ty.IsNullProtoObject && !hasBodyMixin(val.Ty) && len(val.Ty.VisibleFields()) == 0) {
		return Value{}, fmt.Errorf("%d:%d: Object.keys requires an object with known fields", pos.Line, pos.Col)
	}
	fields := esOrderedFields(val.Ty.VisibleFields())
	own := func(v Value) (Value, error) {
		if hasSkippableField(fields) {
			return e.emitObjectPresentFieldNames(v, fields)
		}
		return e.emitObjectFieldNames(fields, pos)
	}
	// Another layout behind a structural type: the object's own keys
	// (TDD-00233).
	if isRecordView(val.Ty) && !val.Ty.Nullable {
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
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", dataReg, n*8))
	for i, f := range fields {
		keyPtr := e.internString(f.Name)
		slotReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %d", slotReg, dataReg, i))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", keyPtr, slotReg))
	}
	r0 := e.freshReg()
	r1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", r0, dataReg))
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %d, 1", r1, r0, n))
	return Value{Ref: r1, Ty: ArrayOf(TypePtr)}, nil
}

// emitObjectPresentFieldNames is emitObjectFieldNames for an object with
// optional (`x?: T`) fields: an absent one has no key (Node), so the names
// are appended at runtime behind each field's presence test (ADR-01063).
func (e *Emitter) emitObjectPresentFieldNames(val Value, fields []Field) (Value, error) {
	n := int64(len(fields))
	e.ensureMalloc()
	dataReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", dataReg, n*8))
	countA := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", countA))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", countA))
	for _, f := range fields {
		present, _ := e.emitFieldPresent(val.Ref, val.Ty, f)
		doL := e.freshLabel("keys.opt.add")
		contL := e.freshLabel("keys.opt.cont")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", present, doL, contL))
		e.emitLabel(doL)
		cur := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", cur, countA))
		slotReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %s", slotReg, dataReg, cur))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", e.internString(f.Name), slotReg))
		next := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", next, cur))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", next, countA))
		e.emitTerminator(fmt.Sprintf("br label %%%s", contL))
		e.emitLabel(contL)
	}
	count := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", count, countA))
	r0 := e.freshReg()
	r1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", r0, dataReg))
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %s, 1", r1, r0, count))
	return Value{Ref: r1, Ty: ArrayOf(TypePtr)}, nil
}

// emitObjectValues implements Object.values(obj) → string[].
// All field values are stringified (booleans → "true"/"false", numbers → decimal).
func (e *Emitter) emitObjectValues(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: Object.values takes 1 argument", pos.Line, pos.Col)
	}
	objVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	// A dynamic object (or any string-keyed Map<string,V>): delegate to its
	// own .values() — see emitObjectKeys and docs/tdd/TDD-00012.md. Note this
	// returns real typed values (matching Map.values()'s convention), unlike
	// the string[] this function returns for fixed-shape objects below.
	if objVal.Ty.IsMap {
		if objVal.Ty.MapKey == nil || !isStringTy(*objVal.Ty.MapKey) {
			return Value{}, fmt.Errorf("%d:%d: Object.values requires a string-keyed Map or dynamic object", pos.Line, pos.Col)
		}
		return e.emitMapMethod(objVal.Ty, objVal.Ref, "values", nil, pos)
	}
	// A bare any (D1 dynamic object / array): a dynamic array of the values.
	if isUnconstrainedDynamic(objVal.Ty) {
		return e.emitDynAnyEntries(objVal, false, pos)
	}
	visFields := objVal.Ty.VisibleFields()
	if !objVal.Ty.IsObject || (!objVal.Ty.IsClass && len(visFields) == 0) {
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
	if isRecordView(objVal.Ty) && !objVal.Ty.Nullable {
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
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", dataReg, n*int64(valTy.Align())))
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
	r0 := e.freshReg()
	r1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", r0, dataReg))
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %s, 1", r1, r0, count))
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
	t.Nullable = false
	t.IsUndefined = false
	return t
}

// forEachPresentField runs body once per *present* field of objVal, in the
// given order, handing it the field, the runtime output index (a constant when
// no field is optional) and the field's present-typed value (a nullable
// scalar's payload, an array/pointer as is). Returns the output count operand.
// An absent optional field is skipped — the one enumeration Object.values,
// Object.entries and for…in share with Object.keys (ADR-01063/ADR-01066).
func (e *Emitter) forEachPresentField(objVal Value, fields []Field, body func(f Field, idxRef string, v Value) error) (string, error) {
	if !hasSkippableField(fields) {
		for i, f := range fields {
			_, v := e.emitFieldPresent(objVal.Ref, objVal.Ty, f)
			if err := body(f, fmt.Sprintf("%d", i), v); err != nil {
				return "", err
			}
		}
		return fmt.Sprintf("%d", len(fields)), nil
	}
	countA := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", countA))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", countA))
	for _, f := range fields {
		present, v := e.emitFieldPresent(objVal.Ref, objVal.Ty, f)
		if isNullableScalar(v.Ty) {
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
		if err := body(f, cur, v); err != nil {
			return "", err
		}
		next := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", next, cur))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", next, countA))
		e.emitTerminator(fmt.Sprintf("br label %%%s", contL))
		e.emitLabel(contL)
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
	objVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	// A dynamic object (or any string-keyed Map<string,V>): delegate to its
	// own .entries() — see emitObjectKeys and docs/tdd/TDD-00012.md. Returns
	// {key: string, value: V}[] with a real typed value, unlike the
	// {key: string, value: string}[] this function builds for fixed-shape
	// objects below.
	if objVal.Ty.IsMap {
		if objVal.Ty.MapKey == nil || !isStringTy(*objVal.Ty.MapKey) {
			return Value{}, fmt.Errorf("%d:%d: Object.entries requires a string-keyed Map or dynamic object", pos.Line, pos.Col)
		}
		return e.emitMapMethod(objVal.Ty, objVal.Ref, "entries", nil, pos)
	}
	// A bare any (D1 dynamic object / array): a dynamic array of [key, value]
	// pairs — JS's real tuple shape, since the elements are themselves dynamic.
	if isUnconstrainedDynamic(objVal.Ty) {
		return e.emitDynAnyEntries(objVal, true, pos)
	}
	visFields := objVal.Ty.VisibleFields()
	if !objVal.Ty.IsObject || (!objVal.Ty.IsClass && len(visFields) == 0) {
		return Value{}, fmt.Errorf("%d:%d: Object.entries requires an object with known fields", pos.Line, pos.Col)
	}
	// Each entry is a real [string, V] tuple (TDD-00066). When every visible
	// field shares one type, V is that real type (ADR-00492); a heterogeneous
	// object's, and a structural type's (TDD-00233), are `any`.
	valTy, homogeneous := homogeneousFieldType(presentFieldTypes(visFields))
	if !homogeneous || isRecordView(objVal.Ty) {
		valTy, homogeneous = TypeAny, false
	}
	if isRecordView(objVal.Ty) && !objVal.Ty.Nullable {
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
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", dataReg, n*8))
	// Only present fields get an entry (ADR-01066) — see forEachPresentField.
	count, err := e.forEachPresentField(objVal, visFields, func(f Field, idxRef string, entryVal Value) error {
		// Allocate one {key: string, value: V} entry struct.
		entryReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", entryReg, entrySize))
		// Store the key (compile-time field name).
		keyPtr := e.internString(f.Name)
		keySlot := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 0", keySlot, entryTy.StructIR(), entryReg))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", keyPtr, keySlot))
		if !homogeneous {
			boxed, err := e.emitBoxValue(entryVal)
			if err != nil {
				return fmt.Errorf("%d:%d: Object.entries: field '%s': %w", pos.Line, pos.Col, f.Name, err)
			}
			entryVal = boxed
		}
		valSlot := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 1", valSlot, entryTy.StructIR(), entryReg))
		if valTy.IsArray {
			e.storeArrayFieldHeader(valSlot, entryVal)
		} else {
			e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", StructFieldIR(valTy), entryVal.Ref, valSlot, valTy.Align()))
		}
		// Store entry pointer in the outer array.
		slotReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %s", slotReg, dataReg, idxRef))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", entryReg, slotReg))
		return nil
	})
	if err != nil {
		return Value{}, err
	}
	r0 := e.freshReg()
	r1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", r0, dataReg))
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %s, 1", r1, r0, count))
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
	if !entries.IsArray || entries.ElemType == nil {
		return TypeI64, nil
	}
	el := entries.ElemType
	switch {
	case el.IsTuple && len(el.Fields) == 2:
		k := el.Fields[0].Ty
		return el.Fields[1].Ty, &k
	case el.IsArray && el.ElemType != nil && !el.ElemType.IsArray:
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
			if t := e.inferExprType(x); !isStringTy(t) || t.IsObject {
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
	valTy, keyTy := e.fromEntriesTypes(args[0])
	if keyTy != nil && !isStringTy(*keyTy) {
		return Value{}, fmt.Errorf("%d:%d: Object.fromEntries requires string keys (a [string, V][] entries array)", pos.Line, pos.Col)
	}
	// A heterogeneous `[key, value]` pair (a Symbol or object key alongside a
	// string value) doesn't infer as a 2-tuple, so the tuple-field key check
	// above is bypassed and seeding would emit an invalid store of the key.
	// Real JS runs ToPropertyKey on each key (a Symbol stays a symbol-keyed
	// property; an object is ToString'd) — neither is representable in this
	// string-keyed dynamic-object subset, so reject a non-string literal key
	// cleanly rather than emitting invalid IR.
	if lit, ok := args[0].(*ast.ArrayLiteral); ok {
		for _, el := range lit.Elements {
			pair, isArr := el.(*ast.ArrayLiteral)
			if !isArr || len(pair.Elements) != 2 {
				continue // shape errors are reported by the seed path below
			}
			if kt := e.inferExprType(pair.Elements[0]); !isStringTy(kt) || kt.IsObject {
				return Value{}, fmt.Errorf("%d:%d: Object.fromEntries requires string keys — a Symbol or object key (ToPropertyKey) is not supported in this typed subset", pair.Elements[0].GetPos().Line, pair.Elements[0].GetPos().Col)
			}
		}
	}

	mapKey := TypePtr
	ty := Type{IR: "ptr", IsMap: true, IsDynamicObject: true, MapKey: &mapKey, MapVal: &valTy}

	e.ensureMapStrHelpers()
	mapPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_map_str_create()", mapPtr))
	if err := e.emitMapSeedFromEntries(mapPtr, args[0], mapKey, valTy, pos); err != nil {
		return Value{}, err
	}
	return Value{Ref: mapPtr, Ty: ty}, nil
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
	targetVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	if !targetVal.Ty.IsObject {
		return Value{}, fmt.Errorf("%d:%d: Object.assign's target must be an object", pos.Line, pos.Col)
	}
	if len(args) > 1 {
		// Only a real write attempt (at least one source) needs the check —
		// Object.assign(frozenObj) with no sources never writes anything,
		// matching real JS not throwing for that case either.
		e.emitFrozenCheck(targetVal.Ref)
	}
	targetStructIR := targetVal.Ty.StructIR()

	for _, srcArg := range args[1:] {
		srcVal, err := e.emitExpr(srcArg)
		if err != nil {
			return Value{}, err
		}
		if !srcVal.Ty.IsObject {
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
			if !coerciblePure(f.Ty, dstTy) {
				return Value{}, fmt.Errorf("%d:%d: Object.assign: source field '%s' has a type incompatible with the target field's type — a merge that changes a field's type is not supported (this compiler's objects are a typed subset)", pos.Line, pos.Col, f.Name)
			}
			srcIdx, _, _ := srcVal.Ty.FieldIndex(f.Name)
			srcGep := e.freshReg()
			loadReg := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", srcGep, srcStructIR, srcVal.Ref, srcIdx))
			e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", loadReg, StructFieldIR(f.Ty), srcGep, f.Ty.Align()))
			val := e.coerce(Value{Ref: loadReg, Ty: f.Ty}, dstTy)
			dstGep := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", dstGep, targetStructIR, targetVal.Ref, dstIdx))
			e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", StructFieldIR(dstTy), val.Ref, dstGep, dstTy.Align()))
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
		isU, isN, absent := e.freshReg(), e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", isU, src.Ref, nbUndefined))
		e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", isN, src.Ref, nbNull))
		e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", absent, isU, isN))
		copyL, nextL := e.freshLabel("dynassign.copy"), e.freshLabel("dynassign.next")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", absent, nextL, copyL))
		e.emitLabel(copyL)
		keys, err := e.emitDynAnyKeys(src, pos)
		if err != nil {
			return Value{}, err
		}
		data, n := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = extractvalue { ptr, i64 } %s, 0", data, keys.Ref))
		e.emitInstr(fmt.Sprintf("%s = extractvalue { ptr, i64 } %s, 1", n, keys.Ref))
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
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", more, bodyL, nextL))
		e.emitLabel(bodyL)
		kp := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %s", kp, data, i))
		key := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", key, kp))
		v, err := e.emitDynAnyMemberGet(src, key, pos)
		if err != nil {
			return Value{}, err
		}
		if _, err := e.emitDynAnyMemberSet(target, key, v, pos); err != nil {
			return Value{}, err
		}
		i2 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", i2, i))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", i2, iSlot))
		e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
		e.emitLabel(nextL)
	}
	return target, nil
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
	if !val.Ty.IsObject {
		return Value{}, fmt.Errorf("%d:%d: Object.freeze requires an object", pos.Line, pos.Col)
	}
	e.ensureFrozenSet()
	setPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_frozen_set_get()", setPtr))
	ptrAsInt := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = ptrtoint ptr %s to i64", ptrAsInt, val.Ref))
	e.emitInstr(fmt.Sprintf("call void @__kml_map_num_set(ptr %s, i64 %s, i64 %d)", setPtr, ptrAsInt, staticIntegrityFrozen))
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
	if isUnconstrainedDynamic(objVal.Ty) || (objVal.Ty.IsDynamic && len(objVal.Ty.UnionMembers) > 0) {
		keyRef, err := e.dynAnyKeyRef(keyExpr, pos)
		if err != nil {
			return Value{}, err
		}
		return e.emitDynAnyHas(Value{Ref: objVal.Ref, Ty: TypeAny}, keyRef, ownOnly, pos)
	}
	if !objVal.Ty.IsObject {
		return Value{}, fmt.Errorf("%d:%d: %s requires an object", pos.Line, pos.Col, callerName)
	}
	keyLit, ok := keyExpr.(*ast.StringLiteral)
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: %s requires a string literal key (dynamic keys are not supported)", pos.Line, pos.Col, callerName)
	}
	_, fieldTy, found := objVal.Ty.FieldIndex(keyLit.Value)
	if found {
		// An optional field that was omitted has no key — `"age" in a` is a
		// runtime presence test, not a static true (ADR-01063).
		if jsonFieldSkippable(fieldTy) {
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
	return Value{Ref: "false", Ty: TypeBool}, nil
}

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
	if !val.Ty.IsObject {
		return Value{}, fmt.Errorf("%d:%d: Object.seal requires an object", pos.Line, pos.Col)
	}
	e.emitStaticIntegrity(val.Ref, staticIntegritySealed)
	return val, nil
}

// Static-object integrity levels recorded in the frozen set (the value per
// object pointer). A static object's shape is fixed at compile time, so
// sealing/preventing extensions enforce nothing extra; the level is kept so
// Object.isSealed/isFrozen/isExtensible answer as JS does (TDD-00229).
const (
	staticIntegrityFrozen        = 1
	staticIntegritySealed        = 2
	staticIntegrityNonExtensible = 3
)

// emitStaticIntegrity raises objRef's recorded integrity level to `level`
// (frozen > sealed > non-extensible; a lower request never downgrades).
func (e *Emitter) emitStaticIntegrity(objRef string, level int) {
	e.ensureFrozenSet()
	setPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_frozen_set_get()", setPtr))
	key := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = ptrtoint ptr %s to i64", key, objRef))
	cur := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_map_num_get(ptr %s, i64 %s)", cur, setPtr, key))
	// Levels are ordered 1 (strongest) .. 3 (weakest), 0 = none.
	weaker := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ugt i64 %s, %d", weaker, cur, level))
	none := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", none, cur))
	raise := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", raise, weaker, none))
	nv := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %d, i64 %s", nv, raise, level, cur))
	e.emitInstr(fmt.Sprintf("call void @__kml_map_num_set(ptr %s, i64 %s, i64 %s)", setPtr, key, nv))
}

// emitStaticIntegrityTest answers Object.isFrozen/isSealed/isExtensible for a
// static object from its recorded level. As in JS, a non-extensible object
// with no own properties is also sealed and frozen.
func (e *Emitter) emitStaticIntegrityTest(val Value, which string) Value {
	e.ensureFrozenSet()
	setPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_frozen_set_get()", setPtr))
	key := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = ptrtoint ptr %s to i64", key, val.Ref))
	lv := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_map_num_get(ptr %s, i64 %s)", lv, setPtr, key))
	empty := len(val.Ty.VisibleFields()) == 0
	r := e.freshReg()
	switch which {
	case "isExtensible":
		e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", r, lv))
	case "isFrozen":
		if empty {
			e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", r, lv))
		} else {
			e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", r, lv, staticIntegrityFrozen))
		}
	case "isSealed":
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

// emitFrozenCheck emits a runtime guard in front of a write to ptrRef (an
// object's own heap pointer): if ptrRef is in the frozen set, throws a
// catchable Error via the existing __kml_throw mechanism instead of letting
// the write proceed. Shared by every object-field write site — emitAssign's
// object-field-assignment branch (emit_exprs.go) and emitObjectAssign's
// target (this file) — so Object.freeze's guarantee holds no matter which
// write path a mutation goes through, not just plain `obj.field = val`.
func (e *Emitter) emitFrozenCheck(ptrRef string) {
	e.ensureFrozenSet()
	setPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_frozen_set_get()", setPtr))
	ptrAsInt := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = ptrtoint ptr %s to i64", ptrAsInt, ptrRef))
	// The set records an integrity level per object (1 frozen, 2 sealed, 3
	// non-extensible — emitStaticIntegrity); only a frozen one rejects writes.
	level := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_map_num_get(ptr %s, i64 %s)", level, setPtr, ptrAsInt))
	isFrozen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 1", isFrozen, level))

	frozenL := e.freshLabel("frozen.reject")
	okL := e.freshLabel("frozen.ok")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isFrozen, frozenL, okL))

	e.emitLabel(frozenL)
	// A write to a frozen object is a TypeError in strict-mode JS (matching V8's
	// "Cannot assign to read only property" message), so `instanceof TypeError`
	// narrows it.
	e.emitInternalThrowKind("TypeError", e.internString("Cannot assign to read only property of a frozen object"))

	e.emitLabel(okL)
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
			t.IsArray != first.IsArray || t.IsFunc != first.IsFunc ||
			t.IsObject != first.IsObject || t.IsMap != first.IsMap {
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
		want = func(m Type) bool { return isUnionObjectMember(m) && !m.IsClass }
	case *ast.ArrowFunction, *ast.FunctionExpression:
		want = func(m Type) bool { return m.IsFunc }
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
// Object.* intrinsics' one emitter. errNotObjectStatic: a form it does not
// emit (the caller's remaining paths decide).
func (e *Emitter) emitObjectStatic(prop string, ex *ast.CallExpression) (Value, error) {
	switch prop {
	case "freeze", "seal", "preventExtensions", "isFrozen", "isSealed", "isExtensible":
		if len(ex.Args) == 1 {
			if t := e.inferExprType(ex.Args[0]); t.IsArray && !t.IsFlatArray && !t.IsDynamic && !t.Nullable {
				return e.emitArrayIntegrityOp(prop, ex.Args[0])
			}
		}
	}
	switch prop {
	case "groupBy":
		return e.emitObjectGroupBy(ex.Args, ex.GetPos())
	case "keys":
		return e.emitObjectKeys(ex.Args, ex.GetPos())
	case "values":
		return e.emitObjectValues(ex.Args, ex.GetPos())
	case "entries":
		return e.emitObjectEntries(ex.Args, ex.GetPos())
	case "fromEntries":
		return e.emitObjectFromEntries(ex.Args, ex.GetPos())
	case "assign":
		return e.emitObjectAssign(ex.Args, ex.GetPos())
	case "freeze":
		return e.emitObjectFreeze(ex.Args, ex.GetPos())
	case "seal":
		return e.emitObjectSeal(ex.Args, ex.GetPos())
	case "hasOwn":
		if len(ex.Args) != 2 {
			return Value{}, fmt.Errorf("%d:%d: Object.hasOwn takes 2 arguments", ex.GetPos().Line, ex.GetPos().Col)
		}
		return e.emitHasOwnProperty(ex.Args[0], ex.Args[1], "Object.hasOwn", true, ex.GetPos())
	case "create":
		return e.emitObjectCreate(ex.Args, ex.GetPos())
	case "getPrototypeOf":
		return e.emitObjectGetPrototypeOf(ex.Args, ex.GetPos())
	case "setPrototypeOf":
		return e.emitObjectSetPrototypeOf(ex.Args, ex.GetPos())
	case "defineProperty":
		return e.emitObjectDefineProperty(ex.Args, ex.GetPos())
	case "defineProperties":
		return e.emitObjectDefineProperties(ex.Args, ex.GetPos())
	case "getOwnPropertyDescriptor":
		return e.emitObjectGetOwnPropertyDescriptor(ex.Args, ex.GetPos())
	case "getOwnPropertyNames":
		return e.emitObjectGetOwnPropertyNames(ex.Args, ex.GetPos())
	case "preventExtensions", "isExtensible", "isSealed", "isFrozen":
		// Dynamic-object forms (TDD-00155 Stage 5); the static-object
		// freeze/seal paths keep their own handlers below.
		if len(ex.Args) == 1 && isUnconstrainedDynamic(e.inferExprType(ex.Args[0])) {
			v, err := e.emitExprWithObjectHint(ex.Args[0], TypeAny)
			if err != nil {
				return Value{}, err
			}
			switch prop {
			case "preventExtensions":
				return e.emitDynPrevent(v, 0)
			case "isExtensible":
				return e.emitDynFlagsTest(v, 0)
			case "isSealed":
				return e.emitDynFlagsTest(v, 1)
			case "isFrozen":
				return e.emitDynFlagsTest(v, 2)
			}
		}
		// A static object's integrity level (TDD-00229).
		if len(ex.Args) == 1 && e.inferExprType(ex.Args[0]).IsObject {
			v, err := e.emitExpr(ex.Args[0])
			if err != nil {
				return Value{}, err
			}
			if prop == "preventExtensions" {
				e.emitStaticIntegrity(v.Ref, staticIntegrityNonExtensible)
				return v, nil
			}
			return e.emitStaticIntegrityTest(v, prop), nil
		}
		// A primitive is frozen, sealed and not extensible.
		if at := e.inferExprType(ex.Args[0]); len(ex.Args) == 1 && isPrimitiveIntegrityTy(at) {
			v, err := e.emitExpr(ex.Args[0])
			if err != nil || prop == "preventExtensions" {
				return v, err
			}
			return Value{Ref: fmt.Sprint(prop != "isExtensible"), Ty: TypeBool}, nil
		}
	}
	return Value{}, errNotObjectStatic
}

var errNotObjectStatic = errors.New("not an Object static")

// isPrimitiveIntegrityTy reports a type whose values are primitives: a
// number, string, boolean, bigint or symbol.
func isPrimitiveIntegrityTy(t Type) bool {
	if t.IsDynamic || t.IsObject || t.IsArray || t.IsClass || t.IsMap || t.IsSet || t.IsFunc || t.Nullable {
		return false
	}
	return isNumberTy(t) || isStringTy(t) || t.IR == "i1" || t.IsBigInt || t.IsSymbol
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
	v.ArrayHeader = hdr
	e.ensureArrayGuard()
	level := map[string]int{"freeze": staticIntegrityFrozen, "seal": staticIntegritySealed, "preventExtensions": staticIntegrityNonExtensible}
	if lvl, ok := level[prop]; ok {
		if v.Ty.IsTypedArray && prop != "preventExtensions" {
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
	switch prop {
	case "isArray":
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
		if argTy.IsDynamic {
			v, err := e.emitExpr(ex.Args[0])
			if err != nil {
				return Value{}, err
			}
			v, err = e.emitBoxValue(v)
			if err != nil {
				return Value{}, err
			}
			tag, payload := e.emitUnboxTagPayload(v)
			isArr := e.freshReg()
			isDyn := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isArr, tag, kmlTagArray))
			e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isDyn, tag, kmlTagDynArray))
			// A boxed TypedArray is kmlTagArray too, but `Array.isArray(new
			// Int32Array(1))` is false — read the box's typed byte (ADR-01059).
			// Only dereferenced when the tag says the payload is a box.
			resPtr := e.freshReg()
			e.emitAlloca(fmt.Sprintf("%s = alloca i1, align 1", resPtr))
			e.emitInstr(fmt.Sprintf("store i1 %s, ptr %s, align 1", isDyn, resPtr))
			typedL := e.freshLabel("isarray.typed")
			mergeL := e.freshLabel("isarray.merge")
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isArr, typedL, mergeL))
			e.emitLabel(typedL)
			box := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", box, payload))
			typedGep := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 2", typedGep, anyArrayBoxTy, box))
			typedB := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load i8, ptr %s, align 1", typedB, typedGep))
			plain := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", plain, typedB, anyArrayPlain))
			e.emitInstr(fmt.Sprintf("store i1 %s, ptr %s, align 1", plain, resPtr))
			e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
			e.emitLabel(mergeL)
			res := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load i1, ptr %s, align 1", res, resPtr))
			return Value{Ref: res, Ty: TypeBool}, nil
		}
		if argTy.IsTypedArray || argTy.IsBuffer {
			// A TypedArray/Buffer is IsArray storage-wise but not a JS Array.
			return Value{Ref: "false", Ty: TypeBool}, nil
		}
		if argTy.IsArray {
			// A `T[] | undefined` value (a nested-array element absence,
			// TDD-00221): a miss is `undefined`, and `Array.isArray(undefined)`
			// is false — decide at runtime on the null data-ptr.
			if argTy.Nullable {
				v, err := e.emitExpr(ex.Args[0])
				if err != nil {
					return Value{}, err
				}
				dataPtr := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", dataPtr, v.Ref))
				isArr := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", isArr, dataPtr))
				return Value{Ref: isArr, Ty: TypeBool}, nil
			}
			return Value{Ref: "true", Ty: TypeBool}, nil
		}
		return Value{Ref: "false", Ty: TypeBool}, nil
	case "of":
		return e.emitArrayOf(ex.Args, ex.GetPos())
	case "from":
		return e.emitArrayFrom(ex.Args, ex.GetPos())
	}
	pos := ex.GetPos()
	return Value{}, fmt.Errorf("%d:%d: Array.%s is not supported", pos.Line, pos.Col, prop)
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
// (an object, a dictionary, a literal of either), not an array's aggregate.
func (e *Emitter) ptrShapedBranch(x ast.Expression) bool {
	switch x.(type) {
	case *ast.ObjectLiteral:
		return true
	}
	t := e.inferExprType(x)
	return t.IR == "ptr" && !t.IsArray && !t.IsDynamic
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
		v = e.coerce(v, hint)
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", v.Ref, slot))
		e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
	}
	e.emitLabel(mergeL)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", r, slot))
	return Value{Ref: r, Ty: hint}, nil
}
