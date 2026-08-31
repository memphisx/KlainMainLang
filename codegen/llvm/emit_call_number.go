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
		return e.emitNumberIsFinite(args, pos)
	case "isNaN":
		return e.emitNumberIsNaN(args, pos)
	case "isSafeInteger":
		return e.emitNumberIsSafeInteger(args, pos)
	case "parseInt":
		return e.emitParseInt(args, pos)
	case "parseFloat":
		return e.emitParseFloat(args, pos)
	}
	return Value{}, fmt.Errorf("%d:%d: Number.%s is not supported", pos.Line, pos.Col, property)
}

func (e *Emitter) emitNumberIsInteger(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: Number.isInteger expects 1 argument", pos.Line, pos.Col)
	}
	val, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	if !val.Ty.Float {
		return Value{Ref: "1", Ty: TypeBool}, nil
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
	return Value{Ref: r, Ty: TypeBool}, nil
}

func (e *Emitter) emitNumberIsNaN(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: isNaN expects 1 argument", pos.Line, pos.Col)
	}
	val, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	if !val.Ty.Float {
		return Value{Ref: "0", Ty: TypeBool}, nil
	}
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = fcmp uno double %s, %s", r, val.Ref, val.Ref))
	return Value{Ref: r, Ty: TypeBool}, nil
}

func (e *Emitter) emitNumberIsFinite(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: isFinite expects 1 argument", pos.Line, pos.Col)
	}
	val, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	if !val.Ty.Float {
		return Value{Ref: "1", Ty: TypeBool}, nil
	}
	// x - x == 0.0 is true only for finite values (Inf → NaN, NaN → NaN)
	diff := e.freshReg()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = fsub double %s, %s", diff, val.Ref, val.Ref))
	e.emitInstr(fmt.Sprintf("%s = fcmp oeq double %s, 0.0", r, diff))
	return Value{Ref: r, Ty: TypeBool}, nil
}

func (e *Emitter) emitNumberIsSafeInteger(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: Number.isSafeInteger expects 1 argument", pos.Line, pos.Col)
	}
	val, err := e.emitExpr(args[0])
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
		return Value{Ref: r, Ty: TypeBool}, nil
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
	return Value{Ref: r, Ty: TypeBool}, nil
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
	v, err := e.emitExpr(args[0])
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
	if _, isNull := args[0].(*ast.NullLiteral); isNull {
		return Value{Ref: "0", Ty: TypeI64}, nil
	}
	v, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	switch {
	case v.Ty.IR == "i1":
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = zext i1 %s to i64", r, v.Ref))
		return Value{Ref: r, Ty: TypeI64}, nil
	case v.Ty.Float || v.Ty.IsInteger() || v.Ty.IR == "i64":
		return v, nil
	case isStringTy(v.Ty):
		e.ensureToNumber()
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call double @__kml_to_number(ptr %s)", r, v.Ref))
		return Value{Ref: r, Ty: TypeF64}, nil
	}
	return Value{}, fmt.Errorf("%d:%d: Number() conversion from this operand type is not supported", pos.Line, pos.Col)
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
	radixRef := ""
	autoRadixReg := "" // non-empty only in the omitted-radix (auto-detect) path
	if len(args) == 2 {
		rv, err := e.emitExpr(args[1])
		if err != nil {
			return Value{}, err
		}
		r32 := e.coerce(rv, TypeI32)
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
	e.ensureStrtodJS()
	strVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	// A no-conversion input must give NaN (real JS), not strtod's bare 0 —
	// endptr stays at the start of the string exactly in that case. Via
	// __kml_strtod_js so a non-JS infinity spelling ("inf"/"infinity"/case
	// variants) is rejected to NaN, while the exact word "Infinity" and a
	// numeric overflow like "1e999" still parse to Infinity as real JS does.
	endSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", endSlot))
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call double @__kml_strtod_js(ptr %s, ptr %s)", r, strVal.Ref, endSlot))
	endPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", endPtr, endSlot))
	noDigits := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, %s", noDigits, endPtr, strVal.Ref))
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, double 0x7FF8000000000000, double %s", result, noDigits, r))
	return Value{Ref: result, Ty: TypeF64}, nil
}
