// emit_undefined.go — `undefined` for a concrete result type (TDD-00187).
// Operations TypeScript types as `T | undefined` (Stage 1: `.pop()`,
// `.shift()`, `.find()`/`.findLast()`, `.at()`) return a real absent value
// instead of the historical zero-fill. The representation is entirely reused:
// a non-pointer scalar `T` rides TDD-00064's presence-flagged { i1, T }
// aggregate, a pointer `T` uses the null pointer with the static type flagged
// Nullable|IsUndefined, and a dynamic (`any`) element already encodes
// `undefined` in its NaN box. The strict/-compat=js consumer policy lives in
// checkStrictUndefinedAssign below.
package llvm

import (
	"KlainMainLang/ast"
	"fmt"
)

// undefinedableElem returns the `T | undefined` type an element-absence
// operation reports for element type t: Nullable|IsUndefined for scalars and
// pointers, t unchanged for the shapes still out of scope (a nested-array
// element — its {ptr,i64} aggregate has no spare absent state, ADR-00246; a
// by-value tuple; a dynamic element, whose box carries undefined natively; a
// type that is already nullable).
// reduceAccWidenable reports whether a reduce accumulator of type t can be
// widened to `t | undefined` under `-compat=js` when the callback may fall off
// the end: a plain scalar (which gains the { i1, T } aggregate) or a pointer
// value (a string/object, whose null pointer is the absence). Arrays and
// already-nullable/dynamic values keep the existing collapse.
func reduceAccWidenable(t Type) bool {
	if t.Nullable || t.IsDynamic || t.IsArray || t.IsNull || t.IsUndefined || t.IR == "" || t.IR == "void" {
		return false
	}
	w := undefinedableElem(t)
	return isNullableScalar(w) || w.IR == "ptr"
}

func undefinedableElem(t Type) Type {
	// A union's box holds undefined as it is; the flag keeps a guard's
	// complement (`typeof o !== "function"` on `o?: A | F`) possibly
	// undefined.
	if t.IsDynamic && len(t.UnionMembers) > 0 && !t.Nullable {
		t.Nullable = true
		t.IsUndefined = true
		return t
	}
	// A heap tuple is a pointer like any object, so null is its absence; only
	// the by-value aggregate form has no spare state (ADR-01063).
	if t.Nullable || t.IsDynamic || t.IsNull || (t.IsTuple && t.TupleByVal) ||
		t.IR == "" || t.IR == "void" {
		return t
	}
	// A nested-array element (`T[][]`) rides its {ptr,i64} aggregate with a null
	// *data-ptr* as the absent signal; the type flag is the null-vs-undefined
	// discriminator the coercion sites need (typeof/String/JSON/??), since a null
	// data-ptr alone can't tell `undefined` from an explicit `null` array
	// (TDD-00221).
	t.Nullable = true
	t.IsUndefined = true
	return t
}

// emitFieldPresent loads an object's field and reports (present, value): for a
// skippable `T | undefined` field (jsonFieldSkippable) `present` is the
// nullable-scalar presence bit / a non-null header or pointer, else the
// constant `true`. The one predicate JSON.stringify, util.inspect, Object.keys
// and `in`/hasOwnProperty share for "is this optional field there" (ADR-01063).
func (e *Emitter) emitFieldPresent(objRef string, objTy Type, field Field) (present string, val Value) {
	idx, _, _ := objTy.FieldIndex(field.Name)
	gepReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gepReg, objTy.StructIR(), objRef, idx))
	if field.Ty.IsArray {
		val = e.loadArrayFieldValue(gepReg, field.Ty) // header-pointer slot (TDD-00213 S2)
	} else {
		loadReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", loadReg, StructFieldIR(field.Ty), gepReg, field.Ty.Align()))
		val = Value{Ref: loadReg, Ty: field.Ty}
	}
	// An UncheckedIndex field (a spawnSync result's stdout, typed plain `T`
	// by tsc) always has its key; only its value may be undefined.
	if !jsonFieldSkippable(field.Ty) || field.Ty.UncheckedIndex {
		return "true", val
	}
	switch {
	case isNullableScalar(field.Ty):
		present, _ = e.nullableScalarAggParts(val)
	case field.Ty.IsArray:
		dataPtr := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", dataPtr, val.Ref))
		present = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", present, dataPtr))
	case field.Ty.IsDynamic:
		// A box: present unless it holds undefined.
		present = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, %d", present, val.Ref, nbUndefined))
	default:
		present = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", present, val.Ref))
	}
	return present, val
}

// hasSkippableField reports whether any field can be absent at runtime.
func hasSkippableField(fields []Field) bool {
	for _, f := range fields {
		if jsonFieldSkippable(f.Ty) {
			return true
		}
	}
	return false
}

// indexReadType is the type an array element read `a[i]` yields for element
// type t: `T | undefined` (an out-of-range index reads `undefined`, as in
// Node), flagged UncheckedIndex because tsc types the expression as plain `T`.
// An element type with no spare absent state passes through unchanged.
func indexReadType(t Type) Type {
	nty := undefinedableElem(t)
	if nty.Nullable && !t.Nullable {
		nty.UncheckedIndex = true
	}
	return nty
}

// isNullishLiteralTy reports the static type of a bare `null`/`undefined`
// (or void) operand — as opposed to a `T | undefined` value, which carries
// IsUndefined only as its null-vs-undefined rendering flag.
func isNullishLiteralTy(t Type) bool {
	return t.IsNull || t.IR == "void" || (t.IsUndefined && !t.Nullable)
}

// optionalParamType widens an optional parameter's declared type to
// `T | undefined` (TDD-00187): inside the body a `f(x?: T)` parameter reads as
// possibly-absent — an omitted argument is a real `undefined`, exactly as tsc
// types it — instead of the historical zero-value stand-in (ADR-00164). Only a
// `?`-declared parameter with no default (a defaulted parameter is always
// present) and a plain bindable name widens; a destructuring-pattern parameter
// is skipped (its unpack expects a concrete aggregate), and undefinedableElem
// itself passes array/tuple/dynamic/already-nullable element types through
// (their zero-arg form stays an empty/zero value with no spare absent state).
func optionalParamType(p ast.Param, pty Type) Type {
	if !p.Optional || p.Default != nil ||
		p.ArrayPattern != nil || p.ObjectPattern != nil {
		return pty
	}
	return undefinedableElem(pty)
}

// contextualParamOptionality reconciles a closure parameter's ABI with the
// optionality its *contextual* type dictates (ADR-00963). When an arrow /
// function expression is emitted into a known expected function type — a
// `const f: F = …` binding or a function-typed argument slot — the expected
// type is authoritative for whether each scalar parameter rides the plain or
// the nullable-scalar { i1, T } ABI, overriding the closure's own `?` marker.
// A `?`-param bound into a required slot is always supplied, so the plain ABI
// loses nothing; a plain param bound into an optional slot must widen to the
// aggregate the caller marshals. Only the nullable/undefined dimension of a
// scalar param is adjusted — the base type stays the closure's own.
func contextualParamOptionality(paramTy, ctx Type) Type {
	if ctx.IR == "" || ctx.Inferred {
		return paramTy // no authoritative context
	}
	ctxOptional := isNullableScalar(ctx)
	if isNullableScalar(paramTy) == ctxOptional {
		return paramTy
	}
	if ctxOptional {
		return undefinedableElem(paramTy)
	}
	// Context is a required scalar: drop the closure param to the plain ABI.
	if isNullableScalar(paramTy) {
		paramTy.Nullable = false
		paramTy.IsUndefined = false
	}
	return paramTy
}

// wrapUndefinedable converts an operation's raw result plus a presence bit
// into its `T | undefined` value. A scalar result becomes the { i1, T }
// aggregate; a pointer result keeps its register (the miss branch already
// produced null) and only gains the static flags; an out-of-scope element
// type passes through untouched.
func (e *Emitter) wrapUndefinedable(v Value, present string) Value {
	nty := undefinedableElem(v.Ty)
	if !nty.Nullable || v.Ty.Nullable {
		return v
	}
	if isNullableScalar(nty) {
		return Value{Ref: e.makeNullableScalarAgg(nty, present, v.Ref), Ty: nty}
	}
	return Value{Ref: v.Ref, Ty: nty}
}

// missRef returns the IR literal an absence-producing op yields on its miss
// branch: the `undefined` NaN-box immediate for a dynamic element (real JS's
// `undefined`, where the old zero decoded as +0.0), the type's zero otherwise
// (which the { i1, T } wrap then marks absent, or which is null for a ptr).
func missRef(t Type) string {
	if t.IsDynamic {
		return fmt.Sprintf("%d", nbUndefined)
	}
	return zeroRef(t)
}

// tsTypeName renders a type the way a TypeScript diagnostic would, for the
// strict-mode assignability error below.
func tsTypeName(t Type) string {
	switch {
	case t.IsBigInt:
		return "bigint"
	case t.IsDate:
		return "Date"
	case t.IsArray:
		return "array"
	case t.IsObject:
		return "object"
	case t.IR == "i1":
		return "boolean"
	case t.Float || t.IR == "i64" || t.IR == "i32" || t.IR == "i16" || t.IR == "i8":
		return "number"
	case isStringTy(t):
		return "string"
	case t.IR == "ptr":
		return "object"
	}
	return t.IR
}

// nullableScalarEqComparable reports whether t can take part in the
// presence-aware nullable-scalar equality below: a nullable scalar itself, or
// a plain non-pointer numeric/boolean scalar to compare its payload against.
func nullableScalarEqComparable(t Type) bool {
	if isNullableScalar(t) {
		return true
	}
	return !t.IsDynamic && !t.IsArray && !t.IsTuple && !t.IsBigInt &&
		t.IR != "ptr" && t.IR != "void" && t.IR != ""
}

// emitNullableScalarValueEq emits ==/===/!=/!== where at least one operand is
// a nullable-scalar { i1, T } aggregate and the other a scalar (nullable or
// bare). Result: equal iff the presence bits match AND (both absent, or the
// payloads compare equal) — so an absent value never equals a real zero.
func (e *Emitter) emitNullableScalarValueEq(op string, left, right Value) (Value, error) {
	lp, rp := "true", "true"
	if isNullableScalar(left.Ty) {
		lp, left = e.nullableScalarAggParts(left)
	}
	if isNullableScalar(right.Ty) {
		rp, right = e.nullableScalarAggParts(right)
	}
	// Unify the payload types: same IR compares directly; mixed numeric kinds
	// go through double, the same promotion ordinary numeric comparison uses.
	var cmp string
	if left.Ty.IR != right.Ty.IR || left.Ty.Float != right.Ty.Float {
		left = e.coerce(left, TypeF64)
		right = e.coerce(right, TypeF64)
	}
	cmp = e.freshReg()
	if left.Ty.Float {
		e.emitInstr(fmt.Sprintf("%s = fcmp oeq %s %s, %s", cmp, left.Ty.IR, left.Ref, right.Ref))
	} else {
		e.emitInstr(fmt.Sprintf("%s = icmp eq %s %s, %s", cmp, left.Ty.IR, left.Ref, right.Ref))
	}
	presEq := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i1 %s, %s", presEq, lp, rp))
	lAbsent := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = xor i1 %s, true", lAbsent, lp))
	absentOrEq := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", absentOrEq, lAbsent, cmp))
	eq := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", eq, presEq, absentOrEq))
	if op == "!=" || op == "!==" {
		ne := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = xor i1 %s, true", ne, eq))
		return Value{Ref: ne, Ty: TypeBool}, nil
	}
	return Value{Ref: eq, Ty: TypeBool}, nil
}

// emitNonNull emits TS's postfix non-null assertion `expr!`: evaluate the
// operand and unwrap its nullability at compile time — a { i1, T } aggregate
// demotes to its payload, a nullable pointer just drops the static flags. No
// runtime check, exactly as in TS.
func (e *Emitter) emitNonNull(ex *ast.NonNullExpression) (Value, error) {
	v, err := e.emitExpr(ex.Arg)
	if err != nil {
		return Value{}, err
	}
	if isNullableScalar(v.Ty) {
		return e.nullableScalarPayloadOf(v), nil
	}
	v.Ty.Nullable = false
	v.Ty.IsUndefined = false
	return v, nil
}

// emitAsExpression handles `expr as T` (ADR-00929). When the operand is a
// dynamic value (`any`/`unknown`) and the assertion names a concrete type, it
// unboxes the NaN-boxed word to that type — the sound, runtime-meaningful
// direction (TS's `any`→`T` is an unchecked assignment; here the box actually
// carries the value). A concrete operand keeps its own value and type — the
// assertion is erased, matching ADR-00371 (a reinterpret across differing
// concrete representations is not modeled).
func (e *Emitter) emitAsExpression(ex *ast.AsExpression) (Value, error) {
	// An array literal asserted to an array type is built as that type
	// (`[5, "s", null] as any[]`): the assertion is its contextual type.
	if lit, ok := ex.Expr.(*ast.ArrayLiteral); ok && ex.TypeAnnot != nil {
		if target := e.resolveType(ex.TypeAnnot); target.IsArray && target.ElemType != nil && target.ElemType.IsDynamic {
			return e.emitExprWithObjectHint(lit, target)
		}
	}
	v, err := e.emitExpr(ex.Expr)
	if err != nil {
		return Value{}, err
	}
	if ex.TypeAnnot == nil || !v.Ty.IsDynamic {
		return v, nil
	}
	target := e.resolveType(ex.TypeAnnot)
	if target.IsDynamic {
		if isUnconstrainedDynamic(target) {
			return Value{Ref: v.Ref, Ty: target}, nil // a union as any: the same box
		}
		return v, nil // `any as any`/union: nothing to unbox
	}
	// coerce carries the correct any→concrete unbox for scalars/strings/objects
	// (emitAnyToNum → fptosi for integers, truthiness for bool, payload-pointer
	// extraction for string/object). Array and nullable-scalar targets need the
	// heap-header / presence-aware decode, which emitUnboxBoxToType provides.
	if target.IsArray || isNullableScalar(target) {
		return e.emitUnboxBoxToType(v.Ref, target), nil
	}
	return e.coerce(v, target), nil
}

// dictMissReadsUndefined reports whether a string-keyed dictionary's read of
// a missing key is typed `V | undefined`: a string, object or number value
// (a box already holds undefined; an array value is its nullable header).
func dictMissReadsUndefined(v Type) bool {
	if v.Nullable || v.IsDynamic || v.IsArray || v.IsFunc {
		return false
	}
	return v.IR == "ptr" || (v.IR == "double" && v.Float)
}
