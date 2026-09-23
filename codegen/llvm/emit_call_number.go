package llvm

import (
	"KlainMainLang/ast"
	"fmt"
)

func (e *Emitter) emitNumberStaticCall(property string, args []ast.Expression, pos ast.Pos) (Value, error) {
	switch property {
	case "isInteger":
		return e.emitNumberIsInteger(args, pos)
	case "isFinite":
		return e.emitNumberIsFinite(args, pos, false)
	case "isNaN":
		return e.emitNumberIsNaN(args, pos, false)
	case "isSafeInteger":
		return e.emitNumberIsSafeInteger(args, pos)
	case "parseInt":
		return e.emitParseInt(args, pos)
	case "parseFloat":
		return e.emitParseFloat(args, pos)
	}
	return Value{}, fmt.Errorf("%d:%d: Number.%s is not supported", pos.Line, pos.Col, property)
}

// emitNumberPredicateOperand evaluates the argument of a Number.isX / global isX
// predicate. A `T | undefined` / `T | null` scalar — an element read that may be
// out of range, a nullable local, a Map lookup — arrives as its `{ i1, T }`
// aggregate: the predicates answer for the *payload* and then for the absence
// (numberPredicateResult), so present is the aggregate's presence bit and val
// its bare payload. present is "" for a value that cannot be absent;
// absentIsUndefined tells an absent `undefined` from an absent `null`.
func (e *Emitter) emitNumberPredicateOperand(arg ast.Expression) (val Value, present string, absentIsUndefined bool, err error) {
	val, err = e.emitPreserveNullableOperand(arg)
	if err != nil {
		return Value{}, "", false, err
	}
	if isNullableScalar(val.Ty) {
		absentIsUndefined = val.Ty.IsUndefined
		present, val = e.nullableScalarAggParts(val)
	}
	return val, present, absentIsUndefined, nil
}

// numberPredicateResult is r for a present operand and the constant absent for
// an absent one (present == "" means the operand cannot be absent).
func (e *Emitter) numberPredicateResult(r Value, present string, absent bool) Value {
	if present == "" {
		return r
	}
	absentRef := "0"
	if absent {
		absentRef = "1"
	}
	out := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i1 %s, i1 %s", out, present, r.Ref, absentRef))
	return Value{Ref: out, Ty: TypeBool}
}

func (e *Emitter) emitNumberIsInteger(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: Number.isInteger expects 1 argument", pos.Line, pos.Col)
	}
	val, present, _, err := e.emitNumberPredicateOperand(args[0])
	if err != nil {
		return Value{}, err
	}
	if !val.Ty.Float {
		return e.numberPredicateResult(Value{Ref: "1", Ty: TypeBool}, present, false), nil
	}
	e.ensureMathFuncs()
	floored := e.freshReg()
	isWhole := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call double @floor(double %s)", floored, val.Ref))
	e.emitInstr(fmt.Sprintf("%s = fcmp oeq double %s, %s", isWhole, val.Ref, floored))
	// floor(±Infinity) == ±Infinity, so the whole-number test alone answers
	// true for Infinity — real JS's Number.isInteger is false for any
	// non-finite value. Gate on finiteness: (x - x) is 0 for a finite x but
	// NaN for ±Infinity/NaN, so `fcmp oeq (x - x), 0` is the finiteness test
	// (no extra libm decl needed). NaN already fails isWhole, but this keeps
	// the intent explicit and covers ±Infinity.
	sub := e.freshReg()
	finite := e.freshReg()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = fsub double %s, %s", sub, val.Ref, val.Ref))
	e.emitInstr(fmt.Sprintf("%s = fcmp oeq double %s, 0.0", finite, sub))
	e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", r, isWhole, finite))
	return e.numberPredicateResult(Value{Ref: r, Ty: TypeBool}, present, false), nil
}

// global selects the global `isNaN` (which ToNumbers its argument) over
// `Number.isNaN` (which answers false for anything that is not a number): they
// differ on an absent value — `isNaN(undefined)` is true, `Number.isNaN(undefined)`
// false, and both are false for `null` (ToNumber(null) is 0).
func (e *Emitter) emitNumberIsNaN(args []ast.Expression, pos ast.Pos, global bool) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: isNaN expects 1 argument", pos.Line, pos.Col)
	}
	val, present, absentIsUndefined, err := e.emitNumberPredicateOperand(args[0])
	if err != nil {
		return Value{}, err
	}
	absent := global && absentIsUndefined
	if !val.Ty.Float {
		// Global `isNaN` applies `ToNumber` to its argument (unlike `Number.isNaN`),
		// so a non-numeric operand — reachable through `any`, or through an erased
		// `expr as any` assertion that keeps the concrete type (ADR-00371) — must be
		// coerced, not answered with a trivial false. A boxed `any` unboxes and runs
		// the numeric conversion (ADR-00902); a concrete string/object/undefined is
		// boxed first, then `ToNumber`'d the same way (`isNaN('hello' as any)` → true,
		// `isNaN('42' as any)` → false). An integer or boolean always `ToNumber`s to a
		// finite value, so those keep the fast trivial false.
		switch {
		case val.Ty.IsDynamic:
			val = e.coerce(val, TypeF64)
		case toNumberCanBeNaN(val.Ty):
			boxed, err := e.emitBoxValue(val)
			if err != nil {
				return Value{}, err
			}
			val = e.coerce(boxed, TypeF64)
		default:
			return e.numberPredicateResult(Value{Ref: "0", Ty: TypeBool}, present, absent), nil
		}
	}
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = fcmp uno double %s, %s", r, val.Ref, val.Ref))
	return e.numberPredicateResult(Value{Ref: r, Ty: TypeBool}, present, absent), nil
}

// toNumberCanBeNaN reports whether a concrete (non-float, non-dynamic) operand's
// `ToNumber` conversion can produce a non-finite result — i.e. it is NOT an
// integer or boolean (both of which always convert to a finite number). Strings
// (`ToNumber('x')` → NaN), objects, and `undefined` all can, so global
// `isNaN`/`isFinite` must actually convert them rather than shortcut.
func toNumberCanBeNaN(ty Type) bool {
	if ty.IR == "i1" {
		return false // boolean → 0 or 1
	}
	if ty.IR == "i64" || ty.IR == "i32" || ty.IR == "i16" || ty.IR == "i8" {
		return ty.IsUndefined // an integer converts to a finite number; undefined → NaN
	}
	return true // string / object / undefined / other pointer types
}

// global: see emitNumberIsNaN. `isFinite(null)` is true (ToNumber(null) is 0),
// `isFinite(undefined)` false; `Number.isFinite` is false for both.
func (e *Emitter) emitNumberIsFinite(args []ast.Expression, pos ast.Pos, global bool) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: isFinite expects 1 argument", pos.Line, pos.Col)
	}
	val, present, absentIsUndefined, err := e.emitNumberPredicateOperand(args[0])
	if err != nil {
		return Value{}, err
	}
	absent := global && !absentIsUndefined
	if !val.Ty.Float {
		// Global `isFinite` applies `ToNumber` first (ADR-00902): a boxed `any`, or a
		// concrete string/object/undefined reachable through an erased `as any`
		// (ADR-00371), is converted and tested — `isFinite('x' as any)` → false — not
		// defaulted to finite. Integers and booleans always convert to a finite value,
		// so they keep the trivial true.
		switch {
		case val.Ty.IsDynamic:
			val = e.coerce(val, TypeF64)
		case toNumberCanBeNaN(val.Ty):
			boxed, err := e.emitBoxValue(val)
			if err != nil {
				return Value{}, err
			}
			val = e.coerce(boxed, TypeF64)
		default:
			return e.numberPredicateResult(Value{Ref: "1", Ty: TypeBool}, present, absent), nil
		}
	}
	// x - x == 0.0 is true only for finite values (Inf → NaN, NaN → NaN)
	diff := e.freshReg()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = fsub double %s, %s", diff, val.Ref, val.Ref))
	e.emitInstr(fmt.Sprintf("%s = fcmp oeq double %s, 0.0", r, diff))
	return e.numberPredicateResult(Value{Ref: r, Ty: TypeBool}, present, absent), nil
}

func (e *Emitter) emitNumberIsSafeInteger(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: Number.isSafeInteger expects 1 argument", pos.Line, pos.Col)
	}
	val, present, _, err := e.emitNumberPredicateOperand(args[0])
	if err != nil {
		return Value{}, err
	}
	const maxSafe = "9007199254740991"
	if !val.Ty.Float {
		neg := e.freshReg()
		cmpNeg := e.freshReg()
		absVal := e.freshReg()
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = sub i64 0, %s", neg, val.Ref))
		e.emitInstr(fmt.Sprintf("%s = icmp sge i64 %s, 0", cmpNeg, val.Ref))
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", absVal, cmpNeg, val.Ref, neg))
		e.emitInstr(fmt.Sprintf("%s = icmp sle i64 %s, %s", r, absVal, maxSafe))
		return e.numberPredicateResult(Value{Ref: r, Ty: TypeBool}, present, false), nil
	}
	e.ensureMathFuncs()
	floored := e.freshReg()
	isInt := e.freshReg()
	absVal := e.freshReg()
	inRange := e.freshReg()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call double @floor(double %s)", floored, val.Ref))
	e.emitInstr(fmt.Sprintf("%s = fcmp oeq double %s, %s", isInt, val.Ref, floored))
	e.emitInstr(fmt.Sprintf("%s = call double @fabs(double %s)", absVal, val.Ref))
	e.emitInstr(fmt.Sprintf("%s = fcmp ole double %s, 9.007199254740991e+15", inRange, absVal))
	e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", r, isInt, inRange))
	return e.numberPredicateResult(Value{Ref: r, Ty: TypeBool}, present, false), nil
}

// emitGlobalStringConv implements the String(x) conversion call — routes
// through emitValueToString, the same rendering template-literal
// interpolation uses. String() with no argument is "" (real JS: "undefined",
// but a call with a genuinely absent value doesn't arise in typed code —
// the empty string is this compiler's deterministic default).
func (e *Emitter) emitGlobalStringConv(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) == 0 {
		return Value{Ref: e.internString(""), Ty: TypePtr}, nil
	}
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: String() takes at most 1 argument", pos.Line, pos.Col)
	}
	v, err := e.emitPreserveNullableOperand(args[0])
	if err != nil {
		return Value{}, err
	}
	if v.Ty.IsDynamic {
		return e.emitDynamicToString(v)
	}
	return e.emitValueToString(v)
}

// emitGlobalBooleanConv implements Boolean(x) — JS truthiness via the shared
// toBool (ADR-00116: NaN is falsy, "" is falsy, 0 is falsy).
func (e *Emitter) emitGlobalBooleanConv(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) == 0 {
		return Value{Ref: "0", Ty: TypeBool}, nil
	}
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: Boolean() takes at most 1 argument", pos.Line, pos.Col)
	}
	v, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	return e.emitToBool(v), nil
}

// emitGlobalNumberConv implements Number(x) — JS ToNumber. A numeric input
// passes through; a boolean is 0/1; a string parses whole-string via
// @__kml_to_number ("" and whitespace-only are 0, a trailing-junk or
// no-digit string is NaN — unlike parseFloat's prefix parse); null is 0.
func (e *Emitter) emitGlobalNumberConv(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) == 0 {
		return Value{Ref: "0", Ty: TypeI64}, nil
	}
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: Number() takes at most 1 argument", pos.Line, pos.Col)
	}
	if nl, isNull := args[0].(*ast.NullLiteral); isNull {
		// Number(null) is 0, but Number(undefined) is NaN (JS).
		if nl.IsUndefined {
			return Value{Ref: "0x7FF8000000000000", Ty: TypeF64}, nil
		}
		return Value{Ref: "0", Ty: TypeI64}, nil
	}
	v, err := e.emitExprKeepNullable(args[0])
	if err != nil {
		return Value{}, err
	}
	switch {
	case v.Ty.IsDynamic:
		// Number(x: any) is ordinary typed TS, so it runs in both lanes:
		// ToPrimitive(number) then ToNumber (ADR-01057). Checked before the
		// numeric case: the NaN-box is an i64, so IsInteger() is true for it.
		prim := e.emitAnyToPrimitive(v.Ref, false)
		return Value{Ref: e.emitAnyToNum(Value{Ref: prim, Ty: TypeAny}), Ty: TypeF64}, nil
	case isNullableScalar(v.Ty):
		return e.emitUnaryPlus(v, pos) // absent → 0 (null) / NaN (undefined)
	case v.Ty.IR == "i1":
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = zext i1 %s to i64", r, v.Ref))
		return Value{Ref: r, Ty: TypeI64}, nil
	case v.Ty.Float || v.Ty.IsInteger() || v.Ty.IR == "i64":
		return v, nil
	case v.Ty.IsBigInt:
		// Number(bigint) → the nearest double (Infinity when out of range), like JS.
		e.ensureBigInt()
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call double @__kml_bigint_to_double(ptr %s)", r, v.Ref))
		return Value{Ref: r, Ty: TypeF64}, nil
	case isStringTy(v.Ty):
		e.ensureToNumber()
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call double @__kml_to_number(ptr %s)", r, v.Ref))
		return Value{Ref: r, Ty: TypeF64}, nil
	}
	// Everything else (null/undefined-typed values, nullable scalars, objects,
	// arrays, Symbol) is exactly unary `+`.
	return e.emitUnaryPlus(v, pos)
}

func (e *Emitter) emitParseInt(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 1 || len(args) > 2 {
		return Value{}, fmt.Errorf("%d:%d: parseInt expects 1 or 2 arguments", pos.Line, pos.Col)
	}
	e.ensureStrtoll()
	strVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	// parseInt ToStrings its input (`parseInt(true)` parses "true" → NaN); a
	// non-string arg otherwise left a non-ptr word where strtoll wants a `ptr`
	// (invalid IR). A string passes through unchanged, then null-safed below.
	if strVal, err = e.emitArgToString(strVal); err != nil {
		return Value{}, err
	}
	strVal = e.nullSafeParseInput(strVal)
	radixRef := ""
	autoRadixReg := "" // non-empty only in the omitted-radix (auto-detect) path
	if len(args) == 2 {
		rv, err := e.emitExpr(args[1])
		if err != nil {
			return Value{}, err
		}
		// The radix is ToNumber'd then ToInt32'd (spec): `parseInt("11", "2")` has
		// a string radix that must parse to 2, not reach strtoll as a `ptr` (which
		// emitted `i32 <string ptr>`, invalid IR — ADR-00907). A string/dynamic
		// radix goes through the real ToNumber; a numeric radix coerces directly.
		var r32 Value
		switch {
		case isStringTy(rv.Ty):
			e.ensureToNumber()
			d := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call double @__kml_to_number(ptr %s)", d, rv.Ref))
			r32 = e.coerce(Value{Ref: d, Ty: TypeF64}, TypeI32)
		case rv.Ty.IsDynamic:
			r32 = e.coerce(Value{Ref: e.emitAnyToNum(rv), Ty: TypeF64}, TypeI32)
		default:
			r32 = e.coerce(rv, TypeI32)
		}
		radixRef = r32.Ref
	} else {
		// No radix argument: real JS auto-detects base 16 for a "0x"/"0X"
		// prefix and base 10 otherwise (no octal auto-detect) — computed at
		// runtime since the string is a runtime value. strtoll accepts the
		// "0x" prefix under base 16, so the value flows straight through.
		e.ensureParseIntBase()
		rr := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_parseint_base(ptr %s)", rr, strVal.Ref))
		radixRef = rr
		autoRadixReg = rr
	}
	// parseInt returns a double, as real JS: values beyond 2^53 lose
	// precision in JS too, and only a double can represent the NaN that a
	// no-digits input must produce. strtoll's endptr tells the two cases
	// apart — it stays at the start of the string exactly when no digits
	// were consumed (strtoll itself skips leading whitespace, same as JS).
	endSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", endSlot))
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @strtoll(ptr %s, ptr %s, i32 %s)", r, strVal.Ref, endSlot, radixRef))
	endPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", endPtr, endSlot))
	noDigits := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, %s", noDigits, endPtr, strVal.Ref))
	if autoRadixReg != "" {
		// Auto-detected base 16 ("0x"/"0X" prefix) with no hex digit after the
		// prefix ("0x", "0x ") — strtoll consumes just the leading "0" and
		// stops on the 'x'/'X', so endptr lands on it. Real JS's parseInt("0x")
		// is NaN, not 0; fold that into the no-digits condition.
		endChar := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i8, ptr %s, align 1", endChar, endPtr))
		isX := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, 120", isX, endChar))
		isBigX := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, 88", isBigX, endChar))
		stuckOnX := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", stuckOnX, isX, isBigX))
		isHex := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 16", isHex, autoRadixReg))
		hexStuck := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", hexStuck, isHex, stuckOnX))
		merged := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", merged, noDigits, hexStuck))
		noDigits = merged
	}
	asF := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sitofp i64 %s to double", asF, r))
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, double 0x7FF8000000000000, double %s", result, noDigits, asF))
	return Value{Ref: result, Ty: TypeF64}, nil
}

func (e *Emitter) emitParseFloat(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: parseFloat expects 1 argument", pos.Line, pos.Col)
	}
	e.ensureStrtodParseFloat()
	strVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	// parseFloat ToStrings its input, like parseInt (see there).
	if strVal, err = e.emitArgToString(strVal); err != nil {
		return Value{}, err
	}
	strVal = e.nullSafeParseInput(strVal)
	// A no-conversion input must give NaN (real JS), not strtod's bare 0 —
	// endptr stays at the start of the string exactly in that case. Via
	// __kml_strtod_parsefloat so a non-JS infinity spelling ("inf"/"infinity"/
	// case variants) is rejected to NaN and a "0x10" hex prefix reads only its
	// leading "0" → 0 (real parseFloat, unlike Number/ToNumber), while the exact
	// word "Infinity" and a numeric overflow like "1e999" still parse to
	// Infinity as real JS does.
	endSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", endSlot))
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call double @__kml_strtod_parsefloat(ptr %s, ptr %s)", r, strVal.Ref, endSlot))
	endPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", endPtr, endSlot))
	noDigits := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, %s", noDigits, endPtr, strVal.Ref))
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, double 0x7FF8000000000000, double %s", result, noDigits, r))
	return Value{Ref: result, Ty: TypeF64}, nil
}

// nullSafeParseInput returns the C string parseInt/parseFloat should scan:
// the value itself, or the literal "null" when the pointer is null at run
// time — what a dynamic null/undefined stringifies to under JS's ToString,
// which both functions then reject as NaN. strtoll on a null pointer is
// undefined behaviour that LLVM lowers to a crash on Linux and a self-loop
// on Windows (found by Test262's parseInt(null) case on the Windows port).
func (e *Emitter) nullSafeParseInput(v Value) Value {
	if v.Ty.IR != "ptr" {
		return v
	}
	isNull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, v.Ref))
	safe := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", safe, isNull, e.internString("null"), v.Ref))
	return Value{Ref: safe, Ty: v.Ty}
}
