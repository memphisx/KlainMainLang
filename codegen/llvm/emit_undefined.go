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
	return e.emitFieldPresentAt(gepReg, field)
}

// emitFieldPresentAt is emitFieldPresent for the field at address gepReg
// (a record view's slot, emitRecordGep).
func (e *Emitter) emitFieldPresentAt(gepReg string, field Field) (present string, val Value) {
	if field.Ty.IsArray {
		val = e.loadArrayFieldValue(gepReg, field.Ty) // header-pointer slot (TDD-00213 S2)
	} else {
		loadReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", loadReg, StructFieldIR(field.Ty), gepReg, field.Ty.Align()))
		val = Value{Ref: loadReg, Ty: field.Ty}
	}
	// An UncheckedIndex field (a spawnSync result's stdout, typed plain `T`
	// by tsc) always has its key; only its value may be undefined.
	// A field that is not optional always has its key, whatever its value.
	if !jsonFieldSkippable(field.Ty) || field.Ty.UncheckedIndex || !field.Optional {
		return "true", val
	}
	switch {
	case isNullableScalar(field.Ty) && field.Ty.NullAndUndef:
		// A null has its key; only undefined (a missing member) has none.
		p, payload := e.nullableScalarAggParts(val)
		present = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", present, p, e.triPayloadIsMarker(payload.Ref, field.Ty)))
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
	if pty.IR == "void" {
		// A `void` parameter (written, or a type argument `T = void`) holds
		// only undefined.
		return TypeUndefined
	}
	if !p.Optional || p.Default != nil ||
		p.ArrayPattern != nil || p.ObjectPattern != nil {
		return pty
	}
	return optionalFieldType(pty)
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
	if ctx.IR == "" || ctx.Inferred || ctx.IsDynamic {
		return paramTy // no authoritative context (an `any` argument may be null)
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
	if t, ok := e.nullAssertedType(ex); ok {
		// `undefined as number | undefined`, `[1] as number[] | undefined`:
		// the value in the asserted type, which can also hold the absence.
		v, err := e.emitExpr(ex.Expr)
		if err != nil {
			return Value{}, err
		}
		return e.coerce(v, t), nil
	}
	v, err := e.emitExpr(ex.Expr)
	if err != nil {
		return Value{}, err
	}
	if ex.TypeAnnot != nil && v.Ty.IsFunc && !v.Ty.IsDynamic {
		// A function asserted to `any` is the boxed function object, whose
		// own properties the dynamic paths read and write (TDD-00229).
		if t := e.resolveType(ex.TypeAnnot); isUnconstrainedDynamic(t) {
			return e.emitBoxValue(v)
		}
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
	// A union's object member asserted as another shape (`address() as
	// { port: number }` over `AddressInfo | string | null`): the member,
	// then its fields by name.
	if plainRecordType(target) {
		for _, m := range v.Ty.UnionMembers {
			if plainRecordType(m) && !sameFieldLayout(m, target) && needsObjectRelayout(m, target) {
				return e.coerce(e.coerce(v, m), target), nil
			}
		}
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

// nullAssertedType is the type a `null`/`undefined` literal asserted to a type
// that holds it (`undefined as number | undefined`, `null as T[] | null`)
// takes: the asserted one, so a binding or field typed from it can hold both.
func (e *Emitter) nullAssertedType(ex *ast.AsExpression) (Type, bool) {
	if ex.TypeAnnot == nil {
		return Type{}, false
	}
	nl, ok := ex.Expr.(*ast.NullLiteral)
	if !ok {
		// A value asserted to its own type widened with null or undefined
		// (`[1] as number[] | undefined`) is typed as the widened one.
		t := e.resolveType(ex.TypeAnnot)
		if !t.Nullable || t.IsDynamic {
			return Type{}, false
		}
		inner := e.inferExprType(ex.Expr)
		strip := func(t Type) Type {
			t.Nullable, t.IsUndefined, t.IsNull = false, false, false
			return t
		}
		b, ib := strip(t), strip(inner)
		if inner.IsDynamic || b.IR != ib.IR || b.IsArray != ib.IsArray || b.IsObject || ib.IsObject || b.IsFunc || ib.IsFunc ||
			b.IsArray && (b.ElemType == nil || ib.ElemType == nil || b.ElemType.IR != ib.ElemType.IR || b.IsTypedArray != ib.IsTypedArray) {
			return Type{}, false
		}
		return t, true
	}
	t := e.resolveType(ex.TypeAnnot)
	switch {
	case t.IsDynamic:
		return t, true // a union or any: the boxed null/undefined
	case isNullableScalar(t) && (t.IsUndefined == nl.IsUndefined || !t.IsUndefined && !nl.IsUndefined):
		return t, true
	case t.Nullable && t.IR == "ptr":
		return t, true
	}
	return Type{}, false
}

// threeStateEligible reports whether t represents `T | null | undefined` in
// three states: a string, object, class instance, Map or Set pointer (null
// is nullRef), or a number or boolean in its { i1, T } slot (null is the
// absent slot with the triMarker payload).
func threeStateEligible(t Type) bool {
	if t.IR == "ptr" {
		return !t.IsArray && !t.IsDynamic && !t.IsFunc && !t.IsNull && !t.IsTuple &&
			(isStringTy(t) || t.IsObject || t.IsClass || t.IsMap || t.IsSet)
	}
	return triScalar(t)
}

// triScalar reports a number or boolean representation that holds a three-
// state payload marker.
func triScalar(t Type) bool {
	if t.IsDynamic || t.IsBigInt || t.IsDate || t.IsArray || t.IsObject || t.IsNull {
		return false
	}
	switch t.IR {
	case "double", "i64", "i32", "i16", "i8", "i1":
		return true
	}
	return false
}

// triMarker is the payload of a three-state scalar holding null: the
// absent slot with the integer bits 1 (an absent slot's payload is
// otherwise ignored, and a missing field's calloc zero reads undefined).
func triMarker(t Type) string {
	switch t.IR {
	case "double":
		return "0x0000000000000001"
	case "i1":
		return "true"
	}
	return "1"
}

// withNullAndUndef is t able to hold null and undefined both.
func withNullAndUndef(t Type) Type {
	t.Nullable, t.IsUndefined, t.NullAndUndef = true, true, true
	return t
}

// optionalFieldType is the storage type of an optional (`x?: T`) member:
// T | undefined, and three-state when T already holds null.
func optionalFieldType(t Type) Type {
	if t.Nullable && !t.IsUndefined && !t.IsNull && threeStateEligible(t) {
		return withNullAndUndef(t)
	}
	return undefinedableElem(t)
}

// nullRef is the null of a three-state pointer: the address of a zeroed
// cell with a string header, so a read that skipped the null check sees an
// empty string or zeroed fields rather than faulting.
func (e *Emitter) nullRef() string {
	if !e.usedNullRef {
		e.usedNullRef = true
		e.emitGlobal("@__kml_nullref_cell = internal global { i64, [248 x i8] } zeroinitializer, align 8")
	}
	return "getelementptr inbounds ({ i64, [248 x i8] }, ptr @__kml_nullref_cell, i32 0, i32 1)"
}

// toThreeState converts a value into a three-state target: a null that
// meant null becomes nullRef (or the scalar marker), and the null pointer or
// absent slot stays undefined.
func (e *Emitter) toThreeState(v Value, target Type) Value {
	if target.IR != "ptr" {
		return e.toThreeStateScalar(v, target)
	}
	switch {
	case v.Ty.IsNull && !v.Ty.IsUndefined:
		return Value{Ref: e.nullRef(), Ty: target}
	case v.Ty.IsNull || v.Ty.IsUndefined && !v.Ty.Nullable:
		return Value{Ref: "null", Ty: target}
	case v.Ty.Nullable && !v.Ty.IsUndefined:
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", r, e.ptrIsNull(v.Ref), e.nullRef(), v.Ref))
		return Value{Ref: r, Ty: target}
	}
	return Value{Ref: v.Ref, Ty: target}
}

// toThreeStateScalar is toThreeState for a number or boolean target.
func (e *Emitter) toThreeStateScalar(v Value, target Type) Value {
	agg := nullableScalarStorageIR(target)
	plainTarget := target
	plainTarget.NullAndUndef = false
	switch {
	case v.Ty.IsNull && !v.Ty.IsUndefined:
		return Value{Ref: e.makeNullableScalarAgg(target, "false", triMarker(target)), Ty: target}
	case v.Ty.IsNull || v.Ty.IR == "void":
		return Value{Ref: fmt.Sprintf("%s zeroinitializer", agg)[len(agg)+1:], Ty: target}
	}
	w := e.coerce(v, plainTarget)
	if !isNullableScalar(w.Ty) {
		return Value{Ref: w.Ref, Ty: target}
	}
	if !v.Ty.IsUndefined && v.Ty.Nullable {
		// A `T | null` value's absence is null.
		present, payload := e.nullableScalarAggParts(w)
		pl := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, %s %s, %s %s", pl, present, target.IR, payload.Ref, target.IR, triMarker(target)))
		return Value{Ref: e.makeNullableScalarAgg(target, present, pl), Ty: target}
	}
	return Value{Ref: w.Ref, Ty: target}
}

// fromThreeState turns a three-state value into a plain nullable one: both
// null and undefined become the null pointer (a scalar keeps its absent
// slot), typed as the value's own absence would read.
func (e *Emitter) fromThreeState(v Value) Value {
	t := v.Ty
	t.NullAndUndef = false
	if v.Ty.IR != "ptr" {
		return Value{Ref: v.Ref, Ty: t}
	}
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr null, ptr %s", r, e.isNullRef(v.Ref), v.Ref))
	return Value{Ref: r, Ty: t}
}

// isTriNull tests a three-state value for null: a pointer against nullRef,
// a scalar slot for absence with the marker payload.
func (e *Emitter) isTriNull(v Value) string {
	if v.Ty.IR == "ptr" {
		return e.isNullRef(v.Ref)
	}
	present, payload := e.nullableScalarAggParts(v)
	marked := e.triPayloadIsMarker(payload.Ref, v.Ty)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ugt i1 %s, %s", r, marked, present)) // marked && !present
	return r
}

// triPayloadIsMarker tests a three-state scalar's payload for triMarker.
func (e *Emitter) triPayloadIsMarker(payload string, t Type) string {
	r := e.freshReg()
	if t.IR == "double" {
		bits := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = bitcast double %s to i64", bits, payload))
		e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 1", r, bits))
		return r
	}
	e.emitInstr(fmt.Sprintf("%s = icmp eq %s %s, %s", r, t.IR, payload, triMarker(t)))
	return r
}

// isNullRef tests a three-state pointer for null.
func (e *Emitter) isNullRef(ref string) string {
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, %s", r, ref, e.nullRef()))
	return r
}

// isAbsentPtr tests a pointer of type t for null or undefined: the null
// pointer, and for a three-state one nullRef as well.
func (e *Emitter) isAbsentPtr(ref string, t Type) string {
	isNull := e.ptrIsNull(ref)
	if !t.NullAndUndef {
		return isNull
	}
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", r, isNull, e.isNullRef(ref)))
	return r
}

// absentWordRef is the keyword an absent pointer of type t renders as:
// for a three-state one, "null" for nullRef and "undefined" otherwise.
func (e *Emitter) absentWordRef(ref string, t Type) string {
	if !t.NullAndUndef {
		return e.internString(absentLiteral(t))
	}
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", r, e.isNullRef(ref), e.internString("null"), e.internString("undefined")))
	return r
}

// renderThreeState renders a three-state pointer through render: its null
// (nullRef) as the null literal, anything else as a plain nullable pointer
// whose null reads as undefined. Each branch renders on its own, so a
// renderer that returns an owned string keeps ownership.
func (e *Emitter) renderThreeState(v Value, render func(Value) (Value, error)) (Value, error) {
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
	isNull := e.isTriNull(v)
	nullL, plainL, doneL := e.freshLabel("tri.null"), e.freshLabel("tri.plain"), e.freshLabel("tri.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNull, nullL, plainL))
	e.emitLabel(nullL)
	nv, err := render(Value{Ref: "null", Ty: TypeNull})
	if err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", nv.Ref, slot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(plainL)
	pv, err := render(e.fromThreeState(v))
	if err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", pv.Ref, slot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", r, slot))
	return Value{Ref: r, Ty: pv.Ty}, nil
}
