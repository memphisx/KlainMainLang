// emit_nullable_scalar.go — codegen for a nullable non-pointer scalar
// (`number | null`, `boolean | null`, an annotated `float64 | null`,
// `Date | null`, ...), TDD-00064 Option A. Such a value has no spare null
// pointer to mean "absent," so it is stored as a presence-flagged
// { i1 present, T value } aggregate (see isNullableScalar/storageIR in
// types.go). Stage 1 covers *locals*: the alloca shape, the store, and a real
// presence test at the null-aware operators (`??`, `??=`, `=== null`,
// `!== null`). Everywhere a plain scalar is expected, a loaded nullable-scalar
// identifier auto-unwraps to its payload (emitIdent) — with a null's payload
// deliberately stored as the type's zero, so ordinary truthiness/arithmetic
// keep the exact lenient behavior they had before, only the null *test* is now
// answered from the presence bit instead of colliding with a real 0.
package llvm

import (
	"KlainMainLang/ast"
	"fmt"
	"strings"
)

// withoutNullable returns a copy of t with the Nullable flag cleared — the
// bare payload type a nullable scalar unwraps to once its presence bit has
// been consulted (or once it is read into an ordinary scalar expression).
func (t Type) withoutNullable() Type {
	t.Nullable = false
	return t
}

// nullableScalarFieldPtr returns a register pointing at field `idx` (0 =
// presence bit, 1 = payload) of the { i1, T } aggregate at ptr.
func (e *Emitter) nullableScalarFieldPtr(ptr string, ty Type, idx int) string {
	agg := nullableScalarStorageIR(ty)
	fp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d", fp, agg, ptr, idx))
	return fp
}

// storeNullableScalarFields writes both fields of the { i1, T } slot at ptr.
func (e *Emitter) storeNullableScalarFields(ptr string, ty Type, presentRef, payloadRef string) {
	f0 := e.nullableScalarFieldPtr(ptr, ty, 0)
	e.emitInstr(fmt.Sprintf("store i1 %s, ptr %s, align 1", presentRef, f0))
	f1 := e.nullableScalarFieldPtr(ptr, ty, 1)
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", ty.IR, payloadRef, f1, ty.Align()))
}

// storeNullableScalarAbsent writes { present=0, payload=zero } — the "null"/
// "undefined" state. The payload is a defined zero (not undef) so that an
// ordinary read of the unwrapped value stays deterministic and keeps null
// falsy under plain truthiness, exactly as the pre-Option-A 0-representation
// already did.
func (e *Emitter) storeNullableScalarAbsent(ptr string, ty Type) {
	e.storeNullableScalarFields(ptr, ty, "false", zeroRef(ty.withoutNullable()))
}

// storeNullableScalarPresent writes { present=1, payload=payloadRef }.
func (e *Emitter) storeNullableScalarPresent(ptr string, ty Type, payloadRef string) {
	e.storeNullableScalarFields(ptr, ty, "true", payloadRef)
}

// copyNullableScalar copies a whole { i1, T } aggregate from srcPtr to dstPtr,
// preserving both the presence bit and the payload — so `let b: number | null
// = a` keeps a's null-ness instead of collapsing it to a present zero.
func (e *Emitter) copyNullableScalar(dstPtr string, ty Type, srcPtr string) {
	agg := nullableScalarStorageIR(ty)
	tmp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", tmp, agg, srcPtr, storageAlign(ty)))
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", agg, tmp, dstPtr, storageAlign(ty)))
}

// loadNullableScalarPayload loads the bare payload T from the { i1, T } slot.
func (e *Emitter) loadNullableScalarPayload(ptr string, ty Type) string {
	f1 := e.nullableScalarFieldPtr(ptr, ty, 1)
	reg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", reg, ty.IR, f1, ty.Align()))
	return reg
}

// loadNullableScalarPresent loads the i1 presence bit from the { i1, T } slot.
func (e *Emitter) loadNullableScalarPresent(ptr string, ty Type) string {
	f0 := e.nullableScalarFieldPtr(ptr, ty, 0)
	reg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i1, ptr %s, align 1", reg, f0))
	return reg
}

// nullableScalarLValue returns the symbol of a nullable-scalar *local*
// referenced by expr, or ok=false. Stage 1 recognizes only a plain local
// identifier here; a nullable-scalar object/interface field or `V | null` map
// value is Stage 3 (boundaries), so those deliberately fall through to the
// pre-existing (still-buggy, documented) path meanwhile. The returned Symbol's
// NarrowedNonNull tells a null-aware caller whether flow analysis (Stage 2) has
// already proven it present.
func (e *Emitter) nullableScalarLValue(expr ast.Expression) (sym Symbol, ok bool) {
	if id, isID := expr.(*ast.Identifier); isID {
		if s, found := e.lookup(id.Name); found && s.isNullableScalarLocal() {
			return s, true
		}
	}
	return Symbol{}, false
}

// --- Stage 3: nullable-scalar aggregate *values* -------------------------
//
// At a boundary (a function return, a parameter, an object field, a Map value)
// a nullable scalar has to travel as a first-class value, not just live in a
// known local slot. The invariant: a Value whose Ty satisfies isNullableScalar
// carries the { i1, T } aggregate in its Ref (an SSA register). A bare payload
// Value always has Nullable cleared, so isNullableScalar(v.Ty) reliably means
// "this Ref is an aggregate." The helpers below build and take apart that
// aggregate; coerce() demotes one to its bare payload wherever a plain scalar
// is wanted, so most consumers never need to know the aggregate shape exists.

// makeNullableScalarAgg builds a { i1, T } aggregate register from a presence
// bit and a payload.
func (e *Emitter) makeNullableScalarAgg(ty Type, presentRef, payloadRef string) string {
	agg := nullableScalarStorageIR(ty)
	r0 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue %s undef, i1 %s, 0", r0, agg, presentRef))
	r1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue %s %s, %s %s, 1", r1, agg, r0, ty.IR, payloadRef))
	return r1
}

// nullableScalarAggParts extracts the presence bit and the (bare) payload from
// a nullable-scalar aggregate Value.
func (e *Emitter) nullableScalarAggParts(v Value) (present string, payload Value) {
	agg := nullableScalarStorageIR(v.Ty)
	p := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue %s %s, 0", p, agg, v.Ref))
	pl := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue %s %s, 1", pl, agg, v.Ref))
	return p, Value{Ref: pl, Ty: v.Ty.withoutNullable()}
}

// nullableScalarPayloadOf demotes a nullable-scalar aggregate Value to its bare
// payload — the value's numeric/boolean content, with a null read back as its
// zero (the same lenient collapse a local read performs). Called by coerce.
func (e *Emitter) nullableScalarPayloadOf(v Value) Value {
	_, payload := e.nullableScalarAggParts(v)
	return payload
}

// emitNullableScalarBoxedValue evaluates expr and produces a { i1, T }
// aggregate register of type ty — the single "box any expression into a
// nullable scalar" entry point shared by return values, arguments, field
// stores, and Map values. A null/undefined literal (or a null-typed value)
// boxes as absent; a still-boxed nullable-scalar source (a boxed local, or a
// nested call already returning T | null) is forwarded with its presence bit
// intact; any other value boxes as present.
func (e *Emitter) emitNullableScalarBoxedValue(expr ast.Expression, ty Type) (string, error) {
	if _, ok := expr.(*ast.NullLiteral); ok {
		return e.makeNullableScalarAgg(ty, "false", zeroRef(ty.withoutNullable())), nil
	}
	if sym, ok := e.nullableScalarLValue(expr); ok {
		agg := nullableScalarStorageIR(ty)
		reg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", reg, agg, sym.Ptr, storageAlign(ty)))
		return reg, nil
	}
	v, err := e.emitExpr(expr)
	if err != nil {
		return "", err
	}
	return e.boxNullableScalarFromValue(v, ty), nil
}

// boxNullableScalarFromValue boxes an already-evaluated Value into a { i1, T }
// aggregate of type ty. See emitNullableScalarBoxedValue for the cases.
func (e *Emitter) boxNullableScalarFromValue(v Value, ty Type) string {
	if v.Ty.IsNull {
		return e.makeNullableScalarAgg(ty, "false", zeroRef(ty.withoutNullable()))
	}
	if isNullableScalar(v.Ty) && v.Ty.IR == ty.IR {
		return v.Ref // already a matching aggregate
	}
	bare := e.coerce(v, ty.withoutNullable())
	return e.makeNullableScalarAgg(ty, "true", bare.Ref)
}

// emitNullableScalarArg formats a call argument for a nullable-scalar
// parameter: the boxed { i1, T } aggregate prefixed with its storage type, ready
// to drop into a call instruction's argument list (TDD-00064 Stage 3).
func (e *Emitter) emitNullableScalarArg(arg ast.Expression, paramTy Type) (string, error) {
	agg, err := e.emitNullableScalarBoxedValue(arg, paramTy)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s %s", nullableScalarStorageIR(paramTy), agg), nil
}

// nullableScalarParamDecl returns the LLVM parameter declaration for a
// nullable-scalar parameter (its incoming { i1, T } aggregate SSA value).
func nullableScalarParamDecl(name string, pty Type) string {
	return fmt.Sprintf("%s %%p_%s", nullableScalarStorageIR(pty), name)
}

// defineNullableScalarParam stores an incoming nullable-scalar parameter's
// { i1, T } aggregate into its own local slot and binds it as a boxed local, so
// the body's null-aware paths (?? / === null / printing / narrowing) treat it
// correctly. ptrName is the alloca register to use (each param emitter names its
// own slots, e.g. "%v_x").
func (e *Emitter) defineNullableScalarParam(name, ptrName string, pty Type) {
	agg := nullableScalarStorageIR(pty)
	e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", ptrName, agg, storageAlign(pty)))
	e.emitInstr(fmt.Sprintf("store %s %%p_%s, ptr %s, align %d", agg, name, ptrName, storageAlign(pty)))
	e.define(name, Symbol{Ptr: ptrName, Ty: pty, NullableBoxed: true})
}

// storeNullableScalar writes an init/RHS expression into the { i1, T } slot at
// ptr, preserving absent-ness across the shapes a source can take (a null/
// undefined literal, another nullable-scalar local, or a plain bare-T value).
// See the file header for why the bare-T fallback marks the result present.
func (e *Emitter) storeNullableScalar(ptr string, ty Type, init ast.Expression) error {
	if _, isNull := init.(*ast.NullLiteral); isNull {
		e.storeNullableScalarAbsent(ptr, ty)
		return nil
	}
	if src, ok := e.nullableScalarLValue(init); ok && src.Ty.IR == ty.IR {
		// A narrowed source has present==1 in storage (narrowing only ever
		// follows a proof of presence), so a whole-aggregate copy is correct
		// either way and keeps a still-nullable source's null-ness.
		e.copyNullableScalar(ptr, ty, src.Ptr)
		return nil
	}
	val, err := e.emitExpr(init)
	if err != nil {
		return err
	}
	// Box whatever we got (a bare value -> present; a null-typed value ->
	// absent; a T|null-returning call's aggregate -> forwarded) and store the
	// whole { i1, T } aggregate so null-ness survives.
	e.storeNullableScalarAggregate(ptr, ty, e.boxNullableScalarFromValue(val, ty))
	return nil
}

// storeScalarOrNullableField stores a Value into an object/interface/class
// field slot at gepReg. A nullable-scalar field (TDD-00064 Stage 3) is boxed
// into its { i1, T } aggregate, preserving a null as absent; every other field
// type coerces and stores exactly as before (StructFieldIR handles the array
// {ptr,i64} shape).
func (e *Emitter) storeScalarOrNullableField(gepReg string, fieldTy Type, val Value) {
	if isNullableScalar(fieldTy) {
		agg := e.boxNullableScalarFromValue(val, fieldTy)
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", nullableScalarStorageIR(fieldTy), agg, gepReg, storageAlign(fieldTy)))
		return
	}
	val = e.coerce(val, fieldTy)
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", StructFieldIR(fieldTy), val.Ref, gepReg, fieldTy.Align()))
}

// storeScalarOrNullableFieldExpr stores an *expression* into a field slot. For
// a nullable-scalar field it boxes straight from the expression via
// emitNullableScalarBoxedValue, which preserves null-ness of a nullable-scalar
// source lvalue (a boxed local/param that emitIdent would otherwise unwrap to a
// present payload) and of a null literal. Non-nullable fields emit + coerce +
// store, honoring an object/array literal's target-type hint.
func (e *Emitter) storeScalarOrNullableFieldExpr(gepReg string, fieldTy Type, expr ast.Expression) error {
	if isNullableScalar(fieldTy) {
		agg, err := e.emitNullableScalarBoxedValue(expr, fieldTy)
		if err != nil {
			return err
		}
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", nullableScalarStorageIR(fieldTy), agg, gepReg, storageAlign(fieldTy)))
		return nil
	}
	val, err := e.emitExprWithObjectHint(expr, fieldTy)
	if err != nil {
		return err
	}
	val = e.coerce(val, fieldTy)
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", StructFieldIR(fieldTy), val.Ref, gepReg, fieldTy.Align()))
	return nil
}

// loadScalarOrNullableField loads an object/interface/class field at gepReg. A
// nullable-scalar field yields its { i1, T } aggregate as a nullable-scalar
// Value (isNullableScalar true), which downstream consumers demote or null-test
// as needed; every other field loads exactly as before.
func (e *Emitter) loadScalarOrNullableField(gepReg string, fieldTy Type) Value {
	reg := e.freshReg()
	if isNullableScalar(fieldTy) {
		e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", reg, nullableScalarStorageIR(fieldTy), gepReg, storageAlign(fieldTy)))
		return Value{Ref: reg, Ty: fieldTy}
	}
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", reg, StructFieldIR(fieldTy), gepReg, fieldTy.Align()))
	return Value{Ref: reg, Ty: fieldTy}
}

// storeNullableScalarAggregate stores a whole { i1, T } aggregate register into
// the slot at ptr.
func (e *Emitter) storeNullableScalarAggregate(ptr string, ty Type, aggRef string) {
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", nullableScalarStorageIR(ty), aggRef, ptr, storageAlign(ty)))
}

// emitNullCoalesceScalar emits `left ?? right` for a nullable-scalar left
// operand whose presence bit is presentRef and whose payload (already loaded)
// is payload. The right side is evaluated only when left is absent.
func (e *Emitter) emitNullCoalesceScalar(presentRef string, payload Value, rightExpr ast.Expression) (Value, error) {
	base := payload.Ty // already the bare (non-nullable) payload type
	resPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", resPtr, base.IR, base.Align()))

	presentL := e.freshLabel("nullc.present")
	absentL := e.freshLabel("nullc.absent")
	mergeL := e.freshLabel("nullc.merge")

	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", presentRef, presentL, absentL))

	e.emitLabel(absentL)
	right, err := e.emitExpr(rightExpr)
	if err != nil {
		return Value{}, err
	}
	right = e.coerce(right, base)
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", base.IR, right.Ref, resPtr, base.Align()))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	e.emitLabel(presentL)
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", base.IR, payload.Ref, resPtr, base.Align()))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	e.emitLabel(mergeL)
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", result, base.IR, resPtr, base.Align()))
	return Value{Ref: result, Ty: base}, nil
}

// emitNullableScalarNullCompare handles `x == null` / `!= null` / `=== null` /
// `!== null` where one operand is a nullable-scalar local and the other is a
// null/undefined literal, by comparing the stored presence bit — so a
// legitimately-present 0 no longer reads as null. Returns ok=false when the
// expression is not this exact shape, leaving the generic path untouched.
func (e *Emitter) emitNullableScalarNullCompare(ex *ast.BinaryExpression) (Value, bool, error) {
	var scalarExpr ast.Expression
	switch {
	case isNullLiteralExpr(ex.Right):
		scalarExpr = ex.Left
	case isNullLiteralExpr(ex.Left):
		scalarExpr = ex.Right
	default:
		return Value{}, false, nil
	}
	// Boxed-local path.
	if sym, ok := e.nullableScalarLValue(scalarExpr); ok {
		// Flow analysis (Stage 2) already proved presence: fold `x === null` to
		// a constant false and `x !== null` to true, with no presence load.
		if sym.NarrowedNonNull {
			return e.nullCompareConst(ex.Op)
		}
		present := e.loadNullableScalarPresent(sym.Ptr, sym.Ty)
		return e.presenceToNullCompare(ex.Op, present)
	}
	// Aggregate path: a non-lvalue that still produces a nullable scalar (a
	// T|null return/field value). Evaluate it and test its presence bit.
	if isNullableScalar(e.inferExprType(scalarExpr)) {
		v, err := e.emitExpr(scalarExpr)
		if err != nil {
			return Value{}, false, err
		}
		if !isNullableScalar(v.Ty) {
			return Value{}, false, nil
		}
		present, _ := e.nullableScalarAggParts(v)
		return e.presenceToNullCompare(ex.Op, present)
	}
	return Value{}, false, nil
}

// nullCompareConst returns the constant folding of a null comparison against a
// value already proven present: `== null` -> false, `!= null` -> true.
func (e *Emitter) nullCompareConst(op string) (Value, bool, error) {
	switch op {
	case "==", "===":
		return Value{Ref: "false", Ty: TypeBool}, true, nil
	case "!=", "!==":
		return Value{Ref: "true", Ty: TypeBool}, true, nil
	}
	return Value{}, false, nil
}

// presenceToNullCompare turns a presence bit into the boolean result of a null
// comparison: `== null` is true when *absent* (present == false), `!= null`
// true when present.
func (e *Emitter) presenceToNullCompare(op, present string) (Value, bool, error) {
	reg := e.freshReg()
	switch op {
	case "==", "===":
		e.emitInstr(fmt.Sprintf("%s = icmp eq i1 %s, false", reg, present))
	case "!=", "!==":
		e.emitInstr(fmt.Sprintf("%s = icmp eq i1 %s, true", reg, present))
	default:
		return Value{}, false, nil
	}
	return Value{Ref: reg, Ty: TypeBool}, true, nil
}

// isNullLiteralExpr reports whether expr is a bare `null`/`undefined` literal.
func isNullLiteralExpr(expr ast.Expression) bool {
	_, ok := expr.(*ast.NullLiteral)
	return ok
}

// emitNullableScalarVarDecl declares a nullable-scalar local: a { i1, T } slot
// initialized from v.Init (or defaulted to absent when there is no
// initializer, matching real JS's `let x: number | null;` === undefined).
// Unlike the generic scalar path in emitVarDecl, this does not re-resolve the
// storage pointer after evaluating the initializer for closure-capture
// promotion (ADR-00001): a captured-and-mutated nullable scalar is a Stage 1
// limitation, out of scope for the locals-only representation change.
func (e *Emitter) emitNullableScalarVarDecl(v *ast.VarDeclaration, ty Type) error {
	ptrName := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", ptrName, nullableScalarStorageIR(ty), storageAlign(ty)))
	e.define(v.Name, Symbol{Ptr: ptrName, Ty: ty, IsConst: v.Kind == "const", NullableBoxed: true})
	if v.Init == nil {
		e.storeNullableScalarAbsent(ptrName, ty)
		return nil
	}
	return e.storeNullableScalar(ptrName, ty, v.Init)
}

// emitNullableScalarAssign handles assignment to a nullable-scalar local:
// plain `=` (null-preserving via storeNullableScalar), the logical/nullish
// compound forms, and ordinary arithmetic compound (`+=`, ...). A compound
// arithmetic op reads the current payload (a null reads as its zero, the same
// lenient behavior the bare-scalar representation had) and stores the result
// as present.
func (e *Emitter) emitNullableScalarAssign(sym Symbol, ex *ast.AssignmentExpression) (Value, error) {
	ty := sym.Ty
	base := ty.withoutNullable()
	switch {
	case ex.Op == "=":
		if err := e.storeNullableScalar(sym.Ptr, ty, ex.Right); err != nil {
			return Value{}, err
		}
		payload := e.loadNullableScalarPayload(sym.Ptr, ty)
		return Value{Ref: payload, Ty: base}, nil
	case ex.Op == "??=":
		return e.emitNullableScalarNullishOrLogicalAssign(sym, "??=", ex.Right)
	case ex.Op == "&&=" || ex.Op == "||=":
		return e.emitNullableScalarNullishOrLogicalAssign(sym, ex.Op, ex.Right)
	default:
		curPayload := e.loadNullableScalarPayload(sym.Ptr, ty)
		cur := Value{Ref: curPayload, Ty: base}
		rhsVal, err := e.emitExpr(ex.Right)
		if err != nil {
			return Value{}, err
		}
		if err := dateCompoundAssignGuard(ex.Op, base.IsDate, rhsVal.Ty.IsDate); err != nil {
			return Value{}, fmt.Errorf("%d:%d: %s", ex.GetPos().Line, ex.GetPos().Col, err)
		}
		rhsVal = e.coerce(rhsVal, base)
		res, err := e.emitArith(strings.TrimSuffix(ex.Op, "="), cur, rhsVal, base, ex.GetPos())
		if err != nil {
			return Value{}, err
		}
		res = e.coerce(res, base)
		e.storeNullableScalarPresent(sym.Ptr, ty, res.Ref)
		return res, nil
	}
}

// emitNullableScalarNullishOrLogicalAssign implements ??= / &&= / ||= against a
// nullable-scalar local. ??= stores the right side only when the current value
// is *absent* (its presence bit is false); &&=/||= key off ordinary truthiness
// of the current payload (a null's payload is its zero, hence falsy), matching
// how the bare representation already behaved for those two. The right side is
// evaluated only down the branch that actually stores.
func (e *Emitter) emitNullableScalarNullishOrLogicalAssign(sym Symbol, op string, rhsExpr ast.Expression) (Value, error) {
	return e.emitNullableScalarNullishOrLogicalAssignAt(sym.Ptr, sym.Ty, op, rhsExpr)
}

// emitNullableScalarNullishOrLogicalAssignAt is the storage-generic core of the
// above: it drives ??=/&&=/||= against any `{ i1, T }` presence-flagged slot
// addressed by ptr, whether that is a local's alloca or an object/interface/
// class field's getelementptr (TDD-00064 Stage 3 — the field case was
// previously mis-routed through the generic emitLogicalCompoundAssign, which
// reads the whole aggregate as a non-`ptr` value and so no-op'd `??=`). ptr must
// point at the `{ i1, T }` aggregate; ty is the nullable type.
func (e *Emitter) emitNullableScalarNullishOrLogicalAssignAt(ptr string, ty Type, op string, rhsExpr ast.Expression) (Value, error) {
	base := ty.withoutNullable()

	var cond Value // true => store the right side
	if op == "??=" {
		present := e.loadNullableScalarPresent(ptr, ty)
		absent := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i1 %s, false", absent, present))
		cond = Value{Ref: absent, Ty: TypeBool}
	} else {
		curPayload := e.loadNullableScalarPayload(ptr, ty)
		truthy := e.toBool(Value{Ref: curPayload, Ty: base})
		if op == "&&=" {
			cond = truthy
		} else { // ||=
			notReg := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = xor i1 %s, true", notReg, truthy.Ref))
			cond = Value{Ref: notReg, Ty: TypeBool}
		}
	}

	storeL := e.freshLabel("nullscalar.assign.store")
	mergeL := e.freshLabel("nullscalar.assign.merge")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", cond.Ref, storeL, mergeL))

	e.emitLabel(storeL)
	rhs, err := e.emitExpr(rhsExpr)
	if err != nil {
		return Value{}, err
	}
	rhs = e.coerce(rhs, base)
	e.storeNullableScalarPresent(ptr, ty, rhs.Ref)
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	e.emitLabel(mergeL)
	payload := e.loadNullableScalarPayload(ptr, ty)
	return Value{Ref: payload, Ty: base}, nil
}

// --- Stage 2: flow narrowing ---------------------------------------------

// narrowingFromCondition recognizes a null-comparison guard over a
// nullable-scalar local — `x !== null` / `x != null` (non-null when the guard
// is true) and `x === null` / `x == null` (non-null when it is false), in
// either operand order. It returns the variable name and which truth value of
// the guard proves the variable present. Bare truthiness (`if (x)`) is
// deliberately not recognized: a present 0 is falsy without being null, so it
// would narrow unsoundly.
func (e *Emitter) narrowingFromCondition(test ast.Expression) (name string, nonNullWhenTrue bool, ok bool) {
	bin, isBin := test.(*ast.BinaryExpression)
	if !isBin {
		return "", false, false
	}
	switch bin.Op {
	case "===", "==", "!==", "!=":
	default:
		return "", false, false
	}
	var idExpr ast.Expression
	switch {
	case isNullLiteralExpr(bin.Right):
		idExpr = bin.Left
	case isNullLiteralExpr(bin.Left):
		idExpr = bin.Right
	default:
		return "", false, false
	}
	id, isID := idExpr.(*ast.Identifier)
	if !isID {
		return "", false, false
	}
	if sym, found := e.lookup(id.Name); !found || !sym.isNullableScalarLocal() {
		return "", false, false
	}
	return id.Name, bin.Op == "!==" || bin.Op == "!=", true
}

// narrowNonNullInCurrentScope shadows a nullable-scalar binding into the
// current (top) scope with NarrowedNonNull set — proven present for the rest of
// that scope. Reuses the outer binding's storage Ptr; popScope discards it.
func (e *Emitter) narrowNonNullInCurrentScope(name string) {
	sym, ok := e.lookup(name)
	if !ok || !sym.isNullableScalarLocal() || sym.NarrowedNonNull {
		return
	}
	sym.NarrowedNonNull = true
	e.define(name, sym)
}

// applyBranchNarrowing narrows a guarded nullable-scalar local inside whichever
// of an `if`'s branches proves it present. Call after pushing the branch's own
// scope so the narrowing is discarded on exit.
func (e *Emitter) applyBranchNarrowing(test ast.Expression, branchIsTrue bool) {
	name, nonNullWhenTrue, ok := e.narrowingFromCondition(test)
	if ok && nonNullWhenTrue == branchIsTrue {
		e.narrowNonNullInCurrentScope(name)
	}
}

// --- Stage 2: null-aware console printing ---------------------------------

// emitConsoleScalarValue prints a bare (non-null) scalar the same way
// emitConsolePrint's main loop does — a boolean as true/false, everything else
// via its printf conversion. Shared by the payload branch of a nullable
// scalar's null-aware print.
func (e *Emitter) emitConsoleScalarValue(val Value, fd int) error {
	if val.Ty.IR == "i1" {
		strVal, err := e.emitValueToString(val)
		if err != nil {
			return err
		}
		e.emitConsolePrintVal(strVal, e.internString("%s\n"), fd)
		return nil
	}
	e.emitConsolePrintVal(val, e.internString(val.Ty.PrintfFmt()+"\n"), fd)
	return nil
}

// emitConsoleNullableScalar prints an un-narrowed nullable-scalar local: its
// value when present, the literal `null` when absent — the real JS rendering,
// which the pre-Option-A representation could not produce (a null read back as
// the payload 0 and printed as "0"). A narrowed local never reaches here; it
// prints its payload through the ordinary path instead.
func (e *Emitter) emitConsoleNullableScalar(sym Symbol, fd int) error {
	present := e.loadNullableScalarPresent(sym.Ptr, sym.Ty)
	payload := Value{Ref: e.loadNullableScalarPayload(sym.Ptr, sym.Ty), Ty: sym.Ty.withoutNullable()}
	return e.emitConsolePresenceBranch(present, payload, fd)
}

// emitConsoleNullableScalarAgg prints a nullable-scalar aggregate *value* (a
// T|null return/field value) the same null-aware way a boxed local prints.
func (e *Emitter) emitConsoleNullableScalarAgg(val Value, fd int) error {
	present, payload := e.nullableScalarAggParts(val)
	return e.emitConsolePresenceBranch(present, payload, fd)
}

// emitConsolePresenceBranch prints payload when present is true, the literal
// `null` when false.
func (e *Emitter) emitConsolePresenceBranch(present string, payload Value, fd int) error {
	valL := e.freshLabel("console.nullscalar.val")
	nullL := e.freshLabel("console.nullscalar.null")
	mergeL := e.freshLabel("console.nullscalar.merge")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", present, valL, nullL))

	e.emitLabel(valL)
	if err := e.emitConsoleScalarValue(payload, fd); err != nil {
		return err
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	e.emitLabel(nullL)
	e.emitConsolePrintVal(Value{Ref: e.internString("null"), Ty: TypePtr}, e.internString("%s\n"), fd)
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	e.emitLabel(mergeL)
	return nil
}

// emitNullableScalarUpdate handles `x++`/`x--` on a nullable-scalar local: it
// reads the payload (a null reads as zero, lenient as before), applies ±1, and
// stores the result as present. Prefix/postfix return value semantics match the
// generic emitUpdate.
func (e *Emitter) emitNullableScalarUpdate(sym Symbol, ex *ast.UpdateExpression) (Value, error) {
	ty := sym.Ty
	base := ty.withoutNullable()
	oldReg := e.loadNullableScalarPayload(sym.Ptr, ty)
	newReg := e.freshReg()
	switch {
	case ex.Op == "++" && base.Float:
		e.emitInstr(fmt.Sprintf("%s = fadd %s %s, 1.0", newReg, base.IR, oldReg))
	case ex.Op == "++":
		e.emitInstr(fmt.Sprintf("%s = add %s %s, 1", newReg, base.IR, oldReg))
	case base.Float:
		e.emitInstr(fmt.Sprintf("%s = fsub %s %s, 1.0", newReg, base.IR, oldReg))
	default:
		e.emitInstr(fmt.Sprintf("%s = sub %s %s, 1", newReg, base.IR, oldReg))
	}
	e.storeNullableScalarPresent(sym.Ptr, ty, newReg)
	if ex.Prefix {
		return Value{Ref: newReg, Ty: base}, nil
	}
	return Value{Ref: oldReg, Ty: base}, nil
}
