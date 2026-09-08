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
func undefinedableElem(t Type) Type {
	if t.Nullable || t.IsArray || t.IsDynamic || t.IsNull || t.IsTuple ||
		t.IR == "" || t.IR == "void" {
		return t
	}
	t.Nullable = true
	t.IsUndefined = true
	return t
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

// checkStrictUndefinedAssign is the strict-mode gate (TDD-00187): assigning a
// `T | undefined` absence result to a bare, non-nullable `T` slot is a compile
// error — faithful strictNullChecks — unless the value was narrowed
// (`if (x !== undefined)`) or asserted (`x!`). Under `-compat=js` this is a
// no-op and coerce's silent unwrap gives strictNullChecks:false semantics.
// Call it at every explicit bare-T boundary: a typed var declaration, an
// assignment, a return, a call argument, a field store.
func (e *Emitter) checkStrictUndefinedAssign(target Type, rhs ast.Expression, pos ast.Pos, what string) error {
	if e.compatJS() || rhs == nil {
		return nil
	}
	if target.Nullable || target.IsDynamic || target.IsNull ||
		target.IR == "" || target.IR == "void" {
		return nil
	}
	src := e.inferExprType(rhs)
	if !src.Nullable || !src.IsUndefined || src.IsDynamic {
		return nil
	}
	// A flow-narrowed local is proven present; its static type still reads
	// nullable here, so consult the narrowing flag directly.
	if id, ok := rhs.(*ast.Identifier); ok {
		if sym, found := e.lookup(id.Name); found && sym.NarrowedNonNull {
			return nil
		}
	}
	return fmt.Errorf("%d:%d: type '%s | undefined' is not assignable to type '%s' (the %s may be undefined) — narrow with `if (x !== undefined)`, provide a default with `??`, or assert with `!`",
		pos.Line, pos.Col, tsTypeName(src), tsTypeName(target), what)
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
