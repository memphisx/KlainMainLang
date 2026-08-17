package llvm

import (
	"fmt"
	"strings"

	"KlainMainLang/ast"
)

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
	if lit, ok := expr.(*ast.ObjectLiteral); ok && hint.IsObject {
		return e.emitObjectLiteralWithHint(lit, &hint)
	}
	if lit, ok := expr.(*ast.ArrayLiteral); ok && hint.IsTuple {
		return e.emitTupleLiteral(lit.Elements, hint)
	}
	if lit, ok := expr.(*ast.ArrayLiteral); ok && hint.IsArray {
		return e.emitArrayLiteralAggregate(lit, hint.ElemType)
	}
	if na, ok := expr.(*ast.NewArrayExpression); ok && na.ElemType == nil && hint.IsArray {
		return e.emitNewArraySizedAggregate(na, *hint.ElemType)
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
	// EventSource's `.onmessage` handler (TDD-00038 Stage 1): without this,
	// an unannotated `ev` defaulted to plain `number` (ADR-00042), so
	// `ev.data` failed to compile as "field access on non-object" — a real,
	// pre-existing gap confirmed directly against a plain, EventSource-
	// unrelated `let cb: (b: Box) => void = (b) => b.value` snippet too.
	if af, ok := expr.(*ast.ArrowFunction); ok && hint.IsFunc {
		return e.emitArrowFunctionWithHints(af, hint.FuncParams)
	}
	// Same hint propagation, for a function expression assigned/passed into
	// a declared function-typed slot (`let cb: (b: Box) => number =
	// function(b) { return b.value; }`) — function expressions need the
	// same outside-context typing an arrow function does (TDD-00060).
	if fe, ok := expr.(*ast.FunctionExpression); ok && hint.IsFunc {
		return e.emitFunctionExpression(fe, hint.FuncParams)
	}
	return e.emitExpr(expr)
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
	ty := e.inferObjectType(lit)
	if hint != nil && hint.IsObject {
		ty = *hint
	}
	// calloc, not malloc: a field absent from lit (an omitted `?:` optional
	// interface field never gets a storeField call below) must read back a
	// deterministic zero, not whatever garbage malloc happened to hand back
	// — a real bug found investigating destructuring defaults, see
	// ADR-00157.
	e.ensureCalloc()
	dataReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @calloc(i64 1, i64 %d)", dataReg, ty.StructSize()))
	structIR := ty.StructIR()

	storeField := func(name string, val Value) error {
		idx, fieldTy, ok := ty.FieldIndex(name)
		if !ok {
			return fmt.Errorf("%d:%d: object has no field '%s'", lit.GetPos().Line, lit.GetPos().Col, name)
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
			srcStructIR := srcVal.Ty.StructIR()
			for _, f := range srcVal.Ty.VisibleFields() {
				srcIdx, _, _ := srcVal.Ty.FieldIndex(f.Name)
				srcGep := e.freshReg()
				loadReg := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", srcGep, srcStructIR, srcVal.Ref, srcIdx))
				e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", loadReg, StructFieldIR(f.Ty), srcGep, f.Ty.Align()))
				if err := storeField(f.Name, Value{Ref: loadReg, Ty: f.Ty}); err != nil {
					return Value{}, err
				}
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
	for _, prop := range lit.Properties {
		if _, ok := prop.Value.(*ast.SpreadElement); ok && prop.Key == "" {
			pos := prop.Value.GetPos()
			return Value{}, fmt.Errorf("%d:%d: object spread cannot be combined with a computed property key yet", pos.Line, pos.Col)
		}
	}

	ty := e.inferDynamicObjectType(lit)
	e.ensureMapStrHelpers()
	mapPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_map_str_create()", mapPtr))

	for _, prop := range lit.Properties {
		keyExpr := prop.KeyExpr
		if keyExpr == nil {
			keyExpr = ast.NewStringLiteral(prop.Key, lit.GetPos())
		} else if !isStringTy(e.inferExprType(keyExpr)) {
			pos := keyExpr.GetPos()
			return Value{}, fmt.Errorf("%d:%d: computed property key must be a string", pos.Line, pos.Col)
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
	if !isStringTy(e.inferExprType(keyExpr)) {
		return Value{}, fmt.Errorf("%d:%d: computed property key must be a string", pos.Line, pos.Col)
	}
	return e.emitMapCall(ty, mapPtr, "get", []ast.Expression{keyExpr}, pos)
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
	keyVal, err := e.emitExpr(keyExpr)
	if err != nil {
		return Value{}, err
	}
	if !isStringTy(keyVal.Ty) {
		return Value{}, fmt.Errorf("%d:%d: computed property key must be a string", pos.Line, pos.Col)
	}
	kRef := e.valueToMapKey(keyVal, TypePtr)

	var rhs Value
	if op == "=" {
		rhs, err = e.emitExpr(rhsExpr)
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

func (e *Emitter) emitObjectVarDecl(v *ast.VarDeclaration, ty Type) error {
	ptrName := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", ptrName))
	e.define(v.Name, Symbol{Ptr: ptrName, Ty: ty, IsConst: v.Kind == "const"})

	if v.Init == nil {
		e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", ptrName))
		return nil
	}

	switch init := v.Init.(type) {
	case *ast.ObjectLiteral:
		val, err := e.emitObjectLiteralWithHint(init, &ty)
		if err != nil {
			return err
		}
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", val.Ref, ptrName))
		return nil

	case *ast.ArrayLiteral:
		// A tuple-typed declaration initialized by an array literal
		// (`const t: [string, number] = ["a", 1]`) builds the tuple struct.
		if ty.IsTuple {
			val, err := e.emitTupleLiteral(init.Elements, ty)
			if err != nil {
				return err
			}
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", val.Ref, ptrName))
			return nil
		}
		val, err := e.emitExpr(init)
		if err != nil {
			return err
		}
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", val.Ref, ptrName))
		return nil

	case *ast.NewErrorExpression:
		val, err := e.emitNewError(init)
		if err != nil {
			return err
		}
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", val.Ref, ptrName))
		return nil

	case *ast.CallExpression:
		// JSON.parse needs the target object type to parse fields correctly;
		// the generic emitExpr path would otherwise dispatch through
		// emitCall's JSON.parse case, which has no declaration context and
		// hardcodes TypePtr as the target (correct only for JSON.parse used
		// outside a typed declaration, e.g. as a bare expression).
		if mem, ok := init.Callee.(*ast.MemberExpression); ok {
			if id, ok2 := mem.Object.(*ast.Identifier); ok2 && id.Name == "JSON" && !e.isShadowedByLocal(id.Name) && mem.Property == "parse" {
				val, err := e.emitJSONParse(init.Args, ty, init.GetPos())
				if err != nil {
					return err
				}
				e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", val.Ref, ptrName))
				return nil
			}
			// response.json() needs the same target-object-type context, for
			// the same reason.
			if mem.Property == "json" && e.inferExprType(mem.Object).IsResponse {
				val, err := e.emitResponseJSON(mem.Object, ty, init.GetPos())
				if err != nil {
					return err
				}
				e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", val.Ref, ptrName))
				return nil
			}
		}
		val, err := e.emitExpr(init)
		if err != nil {
			return err
		}
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", val.Ref, ptrName))
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
		// iterator example; see docs/adr/ADR-00064.md.
		val, err := e.emitExpr(init)
		if err != nil {
			return err
		}
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", val.Ref, ptrName))
		return nil
	}
}

func (e *Emitter) emitObjectDestructuring(s *ast.ObjectDestructuring) error {
	objPtr, objTy, err := e.resolveObjectPtr(s.Init, s.GetPos())
	if err != nil {
		return err
	}
	return e.unpackObjectPatternInto(objPtr, objTy, s.Props, s.GetPos())
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
	structIR := objTy.StructIR()
	for _, prop := range props {
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
			fieldGep := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", fieldGep, structIR, objPtr, idx))
			if prop.SubArray != nil {
				if !fieldTy.IsArray || fieldTy.ElemType == nil {
					return fmt.Errorf("%d:%d: cannot array-destructure non-array field '%s'", pos.Line, pos.Col, prop.Key)
				}
				aggReg := e.freshReg()
				dp := e.freshReg()
				lv := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = load {ptr, i64}, ptr %s, align 8", aggReg, fieldGep))
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

		if prop.Default != nil && !(fieldTy.Nullable && fieldTy.IR == "ptr") {
			return fmt.Errorf("%d:%d: a destructuring default requires field '%s' to be a nullable reference type (string | null, T[] | null, an interface/class type | null) — no other field type has a reliable way to tell a real value apart from 'not provided'", pos.Line, pos.Col, prop.Key)
		}
		gepReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gepReg, structIR, objPtr, idx))
		if fieldTy.IsArray {
			// A destructured array-typed field needs a real, named array
			// Symbol (two allocas — Ptr/LenPtr) like any other array local
			// variable, not a single alloca of the {ptr,i64} storage slot
			// itself — otherwise later uses of this binding (e.g. .push(),
			// which needs LenPtr to write a resized length back to) would
			// find no LenPtr at all. See docs/adr/ADR-00061.md.
			aggReg := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load {ptr, i64}, ptr %s, align 8", aggReg, gepReg))
			dataPtrReg := e.freshReg()
			lenValReg := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", dataPtrReg, aggReg))
			e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 1", lenValReg, aggReg))
			ptrAlloca := e.freshReg()
			lenAlloca := e.freshReg()
			e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", ptrAlloca))
			e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", lenAlloca))

			if prop.Default != nil {
				isNullReg := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNullReg, dataPtrReg))
				absentL := e.freshLabel("destr.absent")
				presentL := e.freshLabel("destr.present")
				afterL := e.freshLabel("destr.after")
				e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNullReg, absentL, presentL))

				e.emitLabel(absentL)
				defVal, err := e.emitExpr(prop.Default)
				if err != nil {
					return err
				}
				if !defVal.Ty.IsArray {
					return fmt.Errorf("%d:%d: destructuring default must be an array to match field '%s'", prop.Default.GetPos().Line, prop.Default.GetPos().Col, prop.Key)
				}
				if err := e.storeArrayAggregateInto(defVal, ptrAlloca, lenAlloca); err != nil {
					return err
				}
				e.emitTerminator(fmt.Sprintf("br label %%%s", afterL))

				e.emitLabel(presentL)
				e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", dataPtrReg, ptrAlloca))
				e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", lenValReg, lenAlloca))
				e.emitTerminator(fmt.Sprintf("br label %%%s", afterL))

				e.emitLabel(afterL)
			} else {
				e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", dataPtrReg, ptrAlloca))
				e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", lenValReg, lenAlloca))
			}
			e.define(prop.Local, Symbol{Ptr: ptrAlloca, LenPtr: lenAlloca, Ty: fieldTy})
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
		e.define(prop.Local, Symbol{Ptr: localPtr, Ty: fieldTy})
	}
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
		if !val.Ty.IsObject {
			return "", Type{}, fmt.Errorf("%d:%d: function call does not return an object", pos.Line, pos.Col)
		}
		return val.Ref, val.Ty, nil

	case *ast.ObjectLiteral:
		ty := e.inferObjectType(src)
		// calloc, not malloc — see emitObjectLiteralWithHint's identical
		// comment; an omitted `?:` optional field must read back zero, not
		// malloc garbage.
		e.ensureCalloc()
		dataReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @calloc(i64 1, i64 %d)", dataReg, ty.StructSize()))
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
	return "", Type{}, fmt.Errorf("%d:%d: object destructuring requires an object variable, function call, or object literal", pos.Line, pos.Col)
}

// emitConditional emits a ternary expression cond ? consequent : alternate.
// Uses an alloca+store/load pattern so both branches can produce a single result.

func (e *Emitter) emitObjectGroupBy(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 2 {
		return Value{}, fmt.Errorf("%d:%d: Object.groupBy takes exactly 2 arguments", pos.Line, pos.Col)
	}
	ptrReg, lenReg, elemTy, err := e.resolveArrayForHOF(args[0], pos)
	if err != nil {
		return Value{}, err
	}
	if err := e.rejectNestedArrayElem(elemTy, "groupBy", pos); err != nil {
		return Value{}, err
	}
	cb, err := e.resolveCallbackWithHints(args[1], []Type{elemTy})
	if err != nil {
		return Value{}, err
	}
	if !isStringTy(cb.retType()) {
		return Value{}, fmt.Errorf("%d:%d: Object.groupBy callback must return a string key", pos.Line, pos.Col)
	}
	e.ensureGroupMapHelpers()

	mapReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_gmap_create()", mapReg))

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
	elemVal := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", elemGep, elemTy.IR, ptrReg, idxVal))
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", elemVal, elemTy.IR, elemGep, elemTy.Align()))

	cbArgs := []Value{{Ref: elemVal, Ty: elemTy}}
	if cb.arity() >= 2 {
		cbArgs = append(cbArgs, Value{Ref: idxVal, Ty: TypeI64})
	}
	keyVal, err := e.emitCBCall(cb, cbArgs)
	if err != nil {
		return Value{}, err
	}

	bucketIdx := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_gmap_find_or_add(ptr %s, ptr %s)", bucketIdx, mapReg, keyVal.Ref))

	// Convert element to i64 for uniform storage in the bucket.
	var elemAsI64 string
	switch elemTy.IR {
	case "i64":
		elemAsI64 = elemVal
	case "ptr":
		t := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = ptrtoint ptr %s to i64", t, elemVal))
		elemAsI64 = t
	case "double":
		t := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = bitcast double %s to i64", t, elemVal))
		elemAsI64 = t
	case "i1":
		t := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = zext i1 %s to i64", t, elemVal))
		elemAsI64 = t
	default:
		t := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = sext %s %s to i64", t, elemTy.IR, elemVal))
		elemAsI64 = t
	}

	e.emitInstr(fmt.Sprintf("call void @__kml_gmap_append(ptr %s, i64 %s, i64 %s)", mapReg, bucketIdx, elemAsI64))

	idxNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", idxNext, idxVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", idxNext, idxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(doneL)
	gmapTy := Type{IR: "ptr", IsGroupMap: true, ElemType: &elemTy}
	return Value{Ref: mapReg, Ty: gmapTy}, nil
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
	if val.Ty.IsGroupMap {
		e.ensureGroupMapHelpers()
		retReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call {ptr, i64} @__kml_gmap_keys(ptr %s)", retReg, val.Ref))
		return Value{Ref: retReg, Ty: ArrayOf(TypePtr)}, nil
	}
	// A dynamic object (or any string-keyed Map<string,V>) is backed by the
	// same runtime as Map<K,V> — delegate to its own .keys() rather than
	// walking a compile-time field list, see docs/tdd/TDD-00012.md.
	if val.Ty.IsMap {
		if val.Ty.MapKey == nil || !isStringTy(*val.Ty.MapKey) {
			return Value{}, fmt.Errorf("%d:%d: Object.keys requires a string-keyed Map or dynamic object", pos.Line, pos.Col)
		}
		return e.emitMapCall(val.Ty, val.Ref, "keys", nil, pos)
	}
	// A zero-field class (methods-only) has genuinely known, just-empty
	// fields — unlike a plain object literal, whose Fields being empty means
	// "unknown", so only the non-class case treats emptiness as an error.
	if !val.Ty.IsObject || (!val.Ty.IsClass && len(val.Ty.VisibleFields()) == 0) {
		return Value{}, fmt.Errorf("%d:%d: Object.keys requires an object with known fields", pos.Line, pos.Col)
	}
	return e.emitObjectFieldNames(val.Ty.VisibleFields(), pos)
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
		return e.emitMapCall(objVal.Ty, objVal.Ref, "values", nil, pos)
	}
	visFields := objVal.Ty.VisibleFields()
	if !objVal.Ty.IsObject || (!objVal.Ty.IsClass && len(visFields) == 0) {
		return Value{}, fmt.Errorf("%d:%d: Object.values requires an object with known fields", pos.Line, pos.Col)
	}
	n := int64(len(visFields))
	e.ensureMalloc()
	dataReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", dataReg, n*8))
	for i, f := range visFields {
		idx, _, _ := objVal.Ty.FieldIndex(f.Name)
		gepReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gepReg, objVal.Ty.StructIR(), objVal.Ref, idx))
		rawReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", rawReg, f.Ty.IR, gepReg, f.Ty.Align()))
		strVal, err := e.emitValueToString(Value{Ref: rawReg, Ty: f.Ty})
		if err != nil {
			return Value{}, fmt.Errorf("%d:%d: Object.values: field '%s': %w", pos.Line, pos.Col, f.Name, err)
		}
		slotReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %d", slotReg, dataReg, i))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", strVal.Ref, slotReg))
	}
	r0 := e.freshReg()
	r1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", r0, dataReg))
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %d, 1", r1, r0, n))
	return Value{Ref: r1, Ty: ArrayOf(TypePtr)}, nil
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
		return e.emitMapCall(objVal.Ty, objVal.Ref, "entries", nil, pos)
	}
	visFields := objVal.Ty.VisibleFields()
	if !objVal.Ty.IsObject || (!objVal.Ty.IsClass && len(visFields) == 0) {
		return Value{}, fmt.Errorf("%d:%d: Object.entries requires an object with known fields", pos.Line, pos.Col)
	}
	// Each entry is a real [string, string] tuple (TDD-00066) — values are
	// still stringified (a heterogeneous object's value type is a union this
	// compiler can't yet form), but the tuple shape is what makes
	// `for (const [k, v] of Object.entries(obj))` work.
	entryTy := TupleType([]Type{TypePtr, TypePtr})
	entrySize := int64(entryTy.StructSize())
	n := int64(len(visFields))
	e.ensureMalloc()
	dataReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", dataReg, n*8))
	for i, f := range visFields {
		idx, _, _ := objVal.Ty.FieldIndex(f.Name)
		// Allocate one {key: string, value: string} entry struct.
		entryReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", entryReg, entrySize))
		// Store the key (compile-time field name).
		keyPtr := e.internString(f.Name)
		keySlot := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 0", keySlot, entryTy.StructIR(), entryReg))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", keyPtr, keySlot))
		// Read, stringify, and store the value.
		gepReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gepReg, objVal.Ty.StructIR(), objVal.Ref, idx))
		rawReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", rawReg, StructFieldIR(f.Ty), gepReg, f.Ty.Align()))
		strVal, err := e.emitValueToString(Value{Ref: rawReg, Ty: f.Ty})
		if err != nil {
			return Value{}, fmt.Errorf("%d:%d: Object.entries: field '%s': %w", pos.Line, pos.Col, f.Name, err)
		}
		valSlot := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 1", valSlot, entryTy.StructIR(), entryReg))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", strVal.Ref, valSlot))
		// Store entry pointer in the outer array.
		slotReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %d", slotReg, dataReg, i))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", entryReg, slotReg))
	}
	r0 := e.freshReg()
	r1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", r0, dataReg))
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %d, 1", r1, r0, n))
	return Value{Ref: r1, Ty: ArrayOf(entryTy)}, nil
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
	if !val.Ty.IsObject {
		return Value{}, fmt.Errorf("%d:%d: Object.freeze requires an object", pos.Line, pos.Col)
	}
	e.ensureFrozenSet()
	setPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_frozen_set_get()", setPtr))
	ptrAsInt := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = ptrtoint ptr %s to i64", ptrAsInt, val.Ref))
	e.emitInstr(fmt.Sprintf("call void @__kml_map_num_set(ptr %s, i64 %s, i64 1)", setPtr, ptrAsInt))
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
func (e *Emitter) emitHasOwnProperty(objExpr, keyExpr ast.Expression, callerName string, pos ast.Pos) (Value, error) {
	objVal, err := e.emitExpr(objExpr)
	if err != nil {
		return Value{}, err
	}
	if !objVal.Ty.IsObject {
		return Value{}, fmt.Errorf("%d:%d: %s requires an object", pos.Line, pos.Col, callerName)
	}
	keyLit, ok := keyExpr.(*ast.StringLiteral)
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: %s requires a string literal key (dynamic keys are not supported)", pos.Line, pos.Col, callerName)
	}
	_, _, found := objVal.Ty.FieldIndex(keyLit.Value)
	if found {
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
	return e.emitHasOwnProperty(ex.Right, ex.Left, "the 'in' operator", ex.GetPos())
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
	if !val.Ty.IsObject {
		return Value{}, fmt.Errorf("%d:%d: Object.seal requires an object", pos.Line, pos.Col)
	}
	return val, nil
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
	isFrozen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_map_num_has(ptr %s, i64 %s)", isFrozen, setPtr, ptrAsInt))

	frozenL := e.freshLabel("frozen.reject")
	okL := e.freshLabel("frozen.ok")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isFrozen, frozenL, okL))

	e.emitLabel(frozenL)
	e.emitInternalThrow(e.internString("Cannot assign to read only property of a frozen object"))

	e.emitLabel(okL)
}

// emitGroupMapIndex handles groupResult["stringKey"] → sub-array.
func (e *Emitter) emitGroupMapIndex(sym Symbol, indexExpr ast.Expression, pos ast.Pos) (Value, error) {
	e.ensureGroupMapHelpers()
	mapPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", mapPtr, sym.Ptr))
	keyVal, err := e.emitExpr(indexExpr)
	if err != nil {
		return Value{}, err
	}
	if !isStringTy(keyVal.Ty) {
		return Value{}, fmt.Errorf("%d:%d: group map key must be a string", pos.Line, pos.Col)
	}
	retReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call {ptr, i64} @__kml_gmap_get(ptr %s, ptr %s)", retReg, mapPtr, keyVal.Ref))
	elemTy := TypeI64
	if sym.Ty.ElemType != nil {
		elemTy = *sym.Ty.ElemType
	}
	return Value{Ref: retReg, Ty: ArrayOf(elemTy)}, nil
}
