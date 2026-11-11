// emit_bigint.go — bigint codegen against the backend-agnostic __kml_bigint_*
// ABI (docs/tdd/TDD-00074.md). Every bigint value is an opaque `ptr` handle
// (BigIntType()); this file lowers literals, operators, and stringification to
// ABI calls, and the selected C backend (bigint_tommath.c / bigint_gmp.c)
// supplies the implementation. Deliberately mirrors emit_symbol.go's shape.
package llvm

import (
	"KlainMainLang/ast"
	"fmt"
	"strings"
)

// ensureBigInt declares the __kml_bigint_* ABI exactly once and flags the
// program as needing a backend (so main.go compiles+links one). Called before
// every bigint operation, like the ensure*() C-decl helpers.
func (e *Emitter) ensureBigInt() {
	e.usesBigInt = true
	if e.declaredBigInt {
		return
	}
	e.declaredBigInt = true
	for _, d := range []string{
		"declare " + irPtr + " @__kml_bigint_from_str(" + irPtr + ", i64, i32)",
		"declare " + irPtr + " @__kml_bigint_parse(" + irPtr + ")",
		"declare " + irPtr + " @__kml_bigint_from_i64(i64)",
		"declare i64 @__kml_bigint_to_i64(" + irPtr + ")",
		"declare " + irPtr + " @__kml_bigint_from_u64(i64)",
		"declare i64 @__kml_bigint_to_u64(" + irPtr + ")",
		"declare " + irPtr + " @__kml_bigint_range_msg(" + irPtr + ", i32)",
		"declare " + irPtr + " @__kml_bigint_to_str(" + irPtr + ", i32)",
		"declare " + irPtr + " @__kml_bigint_add(" + irPtr + ", " + irPtr + ")",
		"declare " + irPtr + " @__kml_bigint_sub(" + irPtr + ", " + irPtr + ")",
		"declare " + irPtr + " @__kml_bigint_mul(" + irPtr + ", " + irPtr + ")",
		"declare " + irPtr + " @__kml_bigint_tdiv(" + irPtr + ", " + irPtr + ")",
		"declare " + irPtr + " @__kml_bigint_mod(" + irPtr + ", " + irPtr + ")",
		"declare " + irPtr + " @__kml_bigint_pow(" + irPtr + ", " + irPtr + ")",
		"declare " + irPtr + " @__kml_bigint_and(" + irPtr + ", " + irPtr + ")",
		"declare " + irPtr + " @__kml_bigint_or(" + irPtr + ", " + irPtr + ")",
		"declare " + irPtr + " @__kml_bigint_xor(" + irPtr + ", " + irPtr + ")",
		"declare " + irPtr + " @__kml_bigint_shl(" + irPtr + ", " + irPtr + ")",
		"declare " + irPtr + " @__kml_bigint_shr(" + irPtr + ", " + irPtr + ")",
		"declare " + irPtr + " @__kml_bigint_neg(" + irPtr + ")",
		"declare " + irPtr + " @__kml_bigint_not(" + irPtr + ")",
		"declare i32 @__kml_bigint_cmp(" + irPtr + ", " + irPtr + ")",
		"declare i32 @__kml_bigint_cmp_double(" + irPtr + ", double)",
		"declare " + irPtr + " @__kml_bigint_as_intn(i64, " + irPtr + ")",
		"declare " + irPtr + " @__kml_bigint_as_uintn(i64, " + irPtr + ")",
		"declare double @__kml_bigint_to_double(" + irPtr + ")",
	} {
		e.emitGlobal(d)
	}
}

// emitBigIntLiteral lowers a `123n` literal to a from_str call. The digit
// string keeps whatever base prefix the lexer saw; we split it into a clean
// digit run plus an explicit radix so every backend parses it uniformly (rather
// than relying on a backend-specific auto-detect).
func (e *Emitter) emitBigIntLiteral(lit *ast.NumberLiteral) (Value, error) {
	e.ensureBigInt()
	digits, radix := lit.Value, 10
	switch {
	case strings.HasPrefix(digits, "0x"), strings.HasPrefix(digits, "0X"):
		digits, radix = digits[2:], 16
	case strings.HasPrefix(digits, "0b"), strings.HasPrefix(digits, "0B"):
		digits, radix = digits[2:], 2
	case strings.HasPrefix(digits, "0o"), strings.HasPrefix(digits, "0O"):
		digits, radix = digits[2:], 8
	}
	strPtr := e.internString(digits)
	reg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_bigint_from_str("+irPtr+" %s, i64 %d, i32 %d)", reg, strPtr, len(digits), radix))
	return Value{Ref: reg, Ty: BigIntType()}, nil
}

// emitBigIntToString renders a bigint as its base-10 digits. suffix controls the
// trailing `n` — real JS shows it for console.log(10n) → "10n" but not for
// String(10n) / `${10n}` → "10", the same console-vs-String split symbol uses.
func (e *Emitter) emitBigIntToString(val Value, suffix bool) (Value, error) {
	e.ensureBigInt()
	e.ensureStrHeaderRuntime()
	// A `bigint | undefined` / `bigint | null` slot holding no bigint is a
	// null pointer: render its keyword instead of handing null to the digit
	// conversion (a segfault — `console.log(map.get(missing))`, TDD-00229).
	if val.Ty.Nullable() || val.Ty.IsUndefined() {
		absent := "null"
		if val.Ty.IsUndefined() {
			absent = "undefined"
		}
		bare := val
		bare.Ty.setNullable(false)
		bare.Ty.setUndefined(false)
		isNull := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq "+irPtr+" %s, null", isNull, val.Ref))
		res, err := e.emitStrBranch(isNull,
			func() (string, error) { return e.internString(absent), nil },
			func() (string, error) {
				s, err := e.emitBigIntToString(bare, suffix)
				return s.Ref, err
			})
		if err != nil {
			return Value{}, err
		}
		return Value{Ref: res, Ty: TypePtr}, nil
	}
	raw := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_bigint_to_str("+irPtr+" %s, i32 10)", raw, val.Ref))
	reg := e.freshReg() // TDD-00120: header-copy the foreign bigint digit string
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_str_from_cstr("+irPtr+" %s)", reg, raw))
	s := Value{Ref: reg, Ty: TypePtr}
	if !suffix {
		return s, nil
	}
	return e.emitStringConcat(s, Value{Ref: e.internString("n"), Ty: TypePtr})
}

// emitBigIntBinary handles an operator whose operands are BOTH bigint (the mixed
// bigint/number case is rejected by the caller). Arithmetic/bitwise/shift return
// a new bigint; comparisons return i1 via cmp.
func (e *Emitter) emitBigIntBinary(op string, left, right Value, pos ast.Pos) (Value, error) {
	e.ensureBigInt()
	// An operand typed `bigint | undefined` (a missing Map entry, an absent
	// index-signature key) may hold no bigint at run time: JS orders
	// `undefined` against a bigint as false, finds it unequal, and throws on
	// arithmetic — never hand the null to the bigint runtime (TDD-00229).
	if op == ">>>" {
		// No unsigned shift on BigInt.
		e.emitThrowTypeError("BigInts have no unsigned right shift, use >> instead")
		e.emitLabel(e.freshLabel("dead")) // the caller goes on emitting
		return Value{Ref: "null", Ty: BigIntType()}, nil
	}
	lN := left.Ty.Nullable() || left.Ty.IsUndefined()
	rN := right.Ty.Nullable() || right.Ty.IsUndefined()
	if lN || rN {
		present := func(v Value, nullable bool) string {
			if !nullable {
				return "true"
			}
			r := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp ne "+irPtr+" %s, null", r, v.Ref))
			return r
		}
		lp, rp := present(left, lN), present(right, rN)
		both := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", both, lp, rp))
		bare := func(v Value) Value {
			v.Ty.setNullable(false)
			v.Ty.setUndefined(false)
			return v
		}
		if _, arith := bigIntBinFn[op]; arith {
			notBoth := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = xor i1 %s, true", notBoth, both))
			throwL := e.freshLabel("bigint.mix")
			okL := e.freshLabel("bigint.ok")
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", notBoth, throwL, okL))
			e.emitLabel(throwL)
			e.emitThrowTypeError("Cannot mix BigInt and other types, use explicit conversions")
			e.emitLabel(okL)
			return e.emitBigIntBinary(op, bare(left), bare(right), pos)
		}
		// Comparison: computed only when both hold a bigint.
		var absentRes string
		switch op {
		case "==", "===":
			// Both absent: undefined === undefined.
			nl := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = xor i1 %s, true", nl, lp))
			nr := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = xor i1 %s, true", nr, rp))
			absentRes = e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", absentRes, nl, nr))
		case "!=", "!==":
			eqBoth := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp eq i1 %s, %s", eqBoth, lp, rp))
			absentRes = e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = xor i1 %s, true", absentRes, eqBoth))
		default:
			absentRes = "false"
		}
		slot := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i1, align 1", slot))
		e.emitInstr(fmt.Sprintf("store i1 %s, "+irPtr+" %s, align 1", absentRes, slot))
		cmpL := e.freshLabel("bigint.cmp")
		doneL := e.freshLabel("bigint.cmpdone")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", both, cmpL, doneL))
		e.emitLabel(cmpL)
		r, err := e.emitBigIntBinary(op, bare(left), bare(right), pos)
		if err != nil {
			return Value{}, err
		}
		e.emitInstr(fmt.Sprintf("store i1 %s, "+irPtr+" %s, align 1", r.Ref, slot))
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
		e.emitLabel(doneL)
		out := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i1, "+irPtr+" %s, align 1", out, slot))
		return Value{Ref: out, Ty: TypeBool}, nil
	}
	if fn, ok := bigIntBinFn[op]; ok {
		if op == "/" || op == "%" {
			e.emitBigIntDivZeroGuard(right)
		}
		if op == "**" {
			e.emitBigIntNegExponentGuard(right)
		}
		reg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_bigint_%s("+irPtr+" %s, "+irPtr+" %s)", reg, fn, left.Ref, right.Ref))
		return Value{Ref: reg, Ty: BigIntType()}, nil
	}
	pred, ok := bigIntCmpPred[op]
	if !ok {
		return Value{}, internalAt(pos, "BigInt operator '%s' past the operator dispatch", op)
	}
	cmpReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_bigint_cmp("+irPtr+" %s, "+irPtr+" %s)", cmpReg, left.Ref, right.Ref))
	reg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp %s i32 %s, 0", reg, pred, cmpReg))
	return Value{Ref: reg, Ty: TypeBool}, nil
}

// emitBigIntMixed handles an operator with exactly one bigint operand.
//   - arithmetic/bitwise: a TypeError in JS → clean compile error (both modes).
//   - ===/!== across types: defined (a bigint is never a non-bigint) → constant.
//   - relational + loose ==/!=: LEGAL in JS (mathematical comparison). Supported
//     here when the other operand is an integer number (converted to bigint,
//     exact). A float operand is rejected — its exact bigint-vs-double comparison
//     is a deferred -compat=js inhabitant (TDD-00075), not an approximation.
func (e *Emitter) emitBigIntMixed(op string, left, right Value, pos ast.Pos) (Value, error) {
	other := left
	if left.Ty.IsBigInt() {
		other = right
	}
	// bigint vs null/undefined: never equal, and not orderable.
	if other.Ty.IsNull() {
		switch op {
		case "==", "===":
			return Value{Ref: "0", Ty: TypeBool}, nil
		case "!=", "!==":
			return Value{Ref: "1", Ty: TypeBool}, nil
		default:
			if relationalOps[op] {
				// Ordered as numbers: null is 0, undefined NaN.
				return e.emitBigIntBoxedBinary(op, left, right, pos)
			}
			// Arithmetic: a TypeError.
			e.emitThrowTypeError("Cannot mix BigInt and other types, use explicit conversions")
			e.emitLabel(e.freshLabel("dead")) // the caller goes on emitting
			return Value{Ref: "null", Ty: BigIntType()}, nil
		}
	}
	switch op {
	case "===":
		return Value{Ref: "0", Ty: TypeBool}, nil
	case "!==":
		return Value{Ref: "1", Ty: TypeBool}, nil
	case "<", ">", "<=", ">=", "==", "!=":
		if other.Ty.Float {
			// `number` is a double (TDD-00123), and JS permits bigint↔number
			// comparison with exact real-number semantics — so compare exactly
			// (no rounding, even past 2^53) rather than rejecting it. This is the
			// same path -compat=js already used for a float operand.
			return e.emitBigIntFloatCompare(op, left, right, pos)
		}
		if !isIntegerNumberTy(other.Ty) && e.compatJS() {
			// A string, a boolean, an object or a symbol: JavaScript's
			// comparison over the two boxes (a string's StringToBigInt, an
			// object's primitive).
			lb, err := e.emitBoxValue(left)
			if err != nil {
				return Value{}, err
			}
			rb, err := e.emitBoxValue(right)
			if err != nil {
				return Value{}, err
			}
			if op == "==" || op == "!=" {
				return e.emitAnyLooseEquals(lb, rb, op == "!=")
			}
			return e.emitAnyBinary(op, lb, rb, pos)
		}
		if !isIntegerNumberTy(other.Ty) {
			if op == "==" || op == "!=" {
				lb, err := e.emitBoxValue(left)
				if err != nil {
					return Value{}, err
				}
				rb, err := e.emitBoxValue(right)
				if err != nil {
					return Value{}, err
				}
				return e.emitAnyLooseEquals(lb, rb, op == "!=")
			}
			return e.emitBigIntBoxedBinary(op, left, right, pos)
		}
		return e.emitBigIntBinary(op, e.bigintOperand(left), e.bigintOperand(right), pos)
	default:
		// Arithmetic with a bigint and another type: JavaScript's operator
		// over the two boxes, which throws its TypeError (or, for `+` with a
		// string, concatenates). A program tsc rejects never reaches here.
		if t := other.Ty; !t.IsDynamic() && !t.Nullable() && isNumberTy(t) && (t.IR == "i1" || t.IR == "i64" || t.IR == "double" || t.IR == "i32") {
			// A number or boolean is no bigint after ToPrimitive either.
			e.emitThrowTypeError("Cannot mix BigInt and other types, use explicit conversions")
			e.emitLabel(e.freshLabel("dead")) // the caller goes on emitting
			return Value{Ref: "null", Ty: BigIntType()}, nil
		}
		return e.emitBigIntBoxedBinary(op, left, right, pos)
	}
}

// emitBigIntBoxedBinary is op over left and right boxed: the dynamic
// operator's own result or TypeError.
func (e *Emitter) emitBigIntBoxedBinary(op string, left, right Value, pos ast.Pos) (Value, error) {
	lb, err := e.emitBoxValue(left)
	if err != nil {
		return Value{}, err
	}
	rb, err := e.emitBoxValue(right)
	if err != nil {
		return Value{}, err
	}
	return e.emitAnyBinary(op, lb, rb, pos)
}

// bigintOperand returns v as a bigint — itself if already bigint, else an
// integer number widened and lifted via from_i64.
func (e *Emitter) bigintOperand(v Value) Value {
	if v.Ty.IsBigInt() {
		return v
	}
	e.ensureBigInt()
	w := e.convert(v, TypeI64)
	reg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_bigint_from_i64(i64 %s)", reg, w.Ref))
	return Value{Ref: reg, Ty: BigIntType()}
}

// emitBigIntFloatCompare implements -compat=js's exact bigint↔float comparison
// (TDD-00075) via __kml_bigint_cmp_double, which returns -1/0/1 exactly (no
// rounding, even past 2^53) or 2 for NaN. The op is applied to (cmp <=> 0); when
// the bigint is the RIGHT operand the op is swapped, since cmp is always
// bigint-relative. NaN makes every ordered comparison false and `!=` true.
func (e *Emitter) emitBigIntFloatCompare(op string, left, right Value, pos ast.Pos) (Value, error) {
	e.ensureBigInt()
	bigintVal, floatVal, bigintIsLeft := left, right, true
	if !left.Ty.IsBigInt() {
		bigintVal, floatVal, bigintIsLeft = right, left, false
	}
	dRef := floatVal.Ref
	if floatVal.Ty.IR == "float" { // widen a float32 operand to double for the ABI
		dr := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = fpext float %s to double", dr, floatVal.Ref))
		dRef = dr
	}
	cmpReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_bigint_cmp_double("+irPtr+" %s, double %s)", cmpReg, bigintVal.Ref, dRef))
	effOp := op
	if !bigintIsLeft {
		effOp = swapCompareOp(op)
	}
	pred := bigIntCmpPred[effOp]
	ordered := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp %s i32 %s, 0", ordered, pred, cmpReg))
	// NaN sentinel (cmp==2): result is `!=` → true, every ordered comparison → false.
	isNaN := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 2", isNaN, cmpReg))
	nanRes := "0"
	if effOp == "!=" {
		nanRes = "1"
	}
	res := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i1 %s, i1 %s", res, isNaN, nanRes, ordered))
	return Value{Ref: res, Ty: TypeBool}, nil
}

func swapCompareOp(op string) string {
	switch op {
	case "<":
		return ">"
	case ">":
		return "<"
	case "<=":
		return ">="
	case ">=":
		return "<="
	default: // ==, != are symmetric
		return op
	}
}

// isIntegerNumberTy reports whether ty is an integer number (i8…i64), excluding
// bool, float, ptr, and every reference type — the operands that can be lifted
// to a bigint for an exact cross-type comparison.
func isIntegerNumberTy(ty Type) bool {
	if ty.Float || ty.IsBigInt() || ty.IsObject() || ty.IsArray() || ty.IsFunc() || ty.IsSymbol() {
		return false
	}
	switch ty.IR {
	case "i8", "i16", "i32", "i64":
		return true
	}
	return false
}

var bigIntBinFn = map[string]string{
	"+": "add", "-": "sub", "*": "mul", "/": "tdiv", "%": "mod", "**": "pow",
	"&": "and", "|": "or", "^": "xor", "<<": "shl", ">>": "shr",
}

var bigIntCmpPred = map[string]string{
	"<": "slt", ">": "sgt", "<=": "sle", ">=": "sge",
	"==": "eq", "===": "eq", "!=": "ne", "!==": "ne",
}

// emitBigIntDivZeroGuard throws a catchable Error before a `/` or `%` by zero,
// matching the i64 path (emitDivZeroGuard) — real JS RangeErrors here, so this
// replaces V1's process-abort with a value a `try`/`catch` can see.
func (e *Emitter) emitBigIntDivZeroGuard(right Value) {
	zero := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_bigint_from_i64(i64 0)", zero))
	cmpReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_bigint_cmp("+irPtr+" %s, "+irPtr+" %s)", cmpReg, right.Ref, zero))
	isZero := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 0", isZero, cmpReg))
	zeroL := e.freshLabel("bigint.div.zero")
	okL := e.freshLabel("bigint.div.ok")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isZero, zeroL, okL))
	e.emitLabel(zeroL)
	e.emitThrowRangeError("Division by zero")
	e.emitLabel(okL)
}

// emitBigIntNegExponentGuard throws the RangeError a negative bigint
// exponent is (V8's own message for it).
func (e *Emitter) emitBigIntNegExponentGuard(right Value) {
	zero := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_bigint_from_i64(i64 0)", zero))
	cmpReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_bigint_cmp("+irPtr+" %s, "+irPtr+" %s)", cmpReg, right.Ref, zero))
	neg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp slt i32 %s, 0", neg, cmpReg))
	negL, okL := e.freshLabel("bigint.pow.neg"), e.freshLabel("bigint.pow.ok")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", neg, negL, okL))
	e.emitLabel(negL)
	e.emitThrowRangeError("undefined must be positive")
	e.emitLabel(okL)
}

// emitBigIntToStringMethod implements `(x).toString()` and `(x).toString(radix)`
// on a bigint receiver — bare digits in the given base (default 10), no `n`
// suffix, matching JS. Reuses the ABI's radix-aware to_str.
func (e *Emitter) emitBigIntToStringMethod(recvExpr ast.Expression, args []ast.Expression, pos ast.Pos) (Value, error) {
	recv, err := e.emitExpr(recvExpr)
	if err != nil {
		return Value{}, err
	}
	// Arguments past the radix are evaluated and ignored.
	for _, x := range args[min(len(args), 1):] {
		if _, err := e.emitExpr(x); err != nil {
			return Value{}, err
		}
	}
	args = args[:min(len(args), 1)]
	e.ensureBigInt()
	radixRef := "10"
	if len(args) == 1 {
		rv, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		// ToIntegerOrInfinity of the radix (`"16"` is 16, undefined is 10);
		// outside 2–36 is a RangeError.
		var d string
		switch {
		case rv.Ty.IsUndefined() && !rv.Ty.IsDynamic():
			d = "10.0"
		case isSafeNumericArg(rv.Ty) && !rv.Ty.IsBigInt():
			d = e.convert(rv, TypeF64).Ref
		default:
			boxed, err := e.emitBoxValue(rv)
			if err != nil {
				return Value{}, err
			}
			n := e.emitAnyToNum(boxed)
			isU := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", isU, boxed.Ref, nbUndefined))
			d = e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = select i1 %s, double 10.0, double %s", d, isU, n))
		}
		lo, hi, ok := e.freshReg(), e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = fcmp oge double %s, 2.0", lo, d))
		e.emitInstr(fmt.Sprintf("%s = fcmp olt double %s, 37.0", hi, d))
		e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", ok, lo, hi))
		badL, okL := e.freshLabel("bigint.radix.bad"), e.freshLabel("bigint.radix.ok")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", ok, okL, badL))
		e.emitLabel(badL)
		e.ensureExceptionHelpers()
		e.excThrow(e.buildErrorObj(errorKindIDs["RangeError"], e.internString("toString() radix argument must be between 2 and 36"), e.internString("RangeError")))
		e.emitTerminator("unreachable")
		e.emitLabel(okL)
		radixReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = fptosi double %s to i32", radixReg, d))
		radixRef = radixReg
	}
	e.ensureStrHeaderRuntime()
	raw := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_bigint_to_str("+irPtr+" %s, i32 %s)", raw, recv.Ref, radixRef))
	reg := e.freshReg() // TDD-00120: header-copy the foreign bigint digit string
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_str_from_cstr("+irPtr+" %s)", reg, raw))
	return Value{Ref: reg, Ty: TypePtr}, nil
}

// emitBigIntNeg / emitBigIntNot back unary `-` and `~` on a bigint operand.
func (e *Emitter) emitBigIntUnary(fn string, val Value) Value {
	e.ensureBigInt()
	reg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_bigint_%s("+irPtr+" %s)", reg, fn, val.Ref))
	return Value{Ref: reg, Ty: BigIntType()}
}

// emitBigIntConstructor lowers BigInt(x): from an integer number (i64) or a
// string. A float or other type is a clean compile error (real JS RangeErrors a
// non-integer number; we reject statically where we can).
// emitBigIntAsN implements BigInt.asIntN(bits, x) / BigInt.asUintN(bits, x):
// clamp a bigint to the low `bits` bits, either two's-complement signed
// (asIntN) or non-negative (asUintN). `bits` is a non-negative integer; `x`
// must be a bigint.
func (e *Emitter) emitBigIntAsN(which string, args []ast.Expression, pos ast.Pos) (Value, error) {
	for len(args) < 2 {
		args = append(append([]ast.Expression(nil), args...), ast.NewNullLiteral(true, pos))
	}
	bitsExpr := args[0]
	if t := e.typeOfExpr(bitsExpr); !isIntegerNumberTy(t) && !t.Float {
		// ToIndex(bits): ToNumber, NaN as 0, truncated.
		num := ast.NewCallExpression(ast.NewIdentifier("Number", pos), []ast.Expression{ast.NewAsExpression(bitsExpr, ast.AnyAnnotation("ts"), pos)}, pos)
		bitsExpr = ast.NewCallExpression(ast.NewMemberExpression(ast.NewIdentifier("Math", pos), "trunc", pos),
			[]ast.Expression{ast.NewBinaryExpression("||", num, ast.NewNumberLiteral("0", pos), pos)}, pos)
	}
	bitsVal, err := e.emitExpr(bitsExpr)
	if err != nil {
		return Value{}, err
	}
	bits := e.convert(bitsVal, TypeI64)
	for _, a := range args[2:] {
		if _, err := e.emitExpr(a); err != nil {
			return Value{}, err
		}
	}
	xVal, err := e.emitExpr(args[1])
	if err != nil {
		return Value{}, err
	}
	if !xVal.Ty.IsBigInt() {
		// ToBigInt(x): undefined, a number, a non-integer string throw.
		if xVal, err = e.bigintFromToBigInt(xVal, pos); err != nil {
			return Value{}, err
		}
	}
	e.ensureBigInt()
	fn := "__kml_bigint_as_intn"
	if which == "asUintN" {
		fn = "__kml_bigint_as_uintn"
	}
	reg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @%s(i64 %s, "+irPtr+" %s)", reg, fn, bits.Ref, xVal.Ref))
	return Value{Ref: reg, Ty: BigIntType()}, nil
}

func (e *Emitter) emitBigIntConstructor(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) == 0 {
		args = []ast.Expression{ast.NewNullLiteral(true, pos)} // BigInt(undefined)
	}
	arg, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	return e.bigintFromValue(arg, pos)
}

// bigintFromValue is BigInt(arg) for an evaluated argument (ECMA-262's
// ToBigInt after ToPrimitive): an integer number, a string, a boolean or a
// bigint converts; undefined, null and a symbol are a TypeError.
func (e *Emitter) bigintFromValue(arg Value, pos ast.Pos) (Value, error) {
	e.ensureBigInt()
	switch {
	case arg.Ty.IsBigInt():
		return arg, nil
	case isNullableScalar(arg.Ty) || arg.Ty.IsDynamic():
		return e.bigintFromRuntime(arg, pos, true)
	case arg.Ty.IsNull() || arg.Ty.IsUndefined() || arg.Ty.Nullable() || arg.Ty.IR == "" || arg.Ty.IR == "void":
		// Possibly null or undefined (or only that): decided at run time.
		boxed, err := e.emitBoxValue(arg)
		if err != nil {
			return Value{}, err
		}
		return e.bigintFromRuntime(boxed, pos, true)
	case isStringTy(arg.Ty):
		// StringToBigInt: a string that is no integer is a SyntaxError.
		reg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_bigint_parse("+irPtr+" %s)", reg, arg.Ref))
		bad := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq "+irPtr+" %s, null", bad, reg))
		badL, okL := e.freshLabel("bigint.str.bad"), e.freshLabel("bigint.str.ok")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", bad, badL, okL))
		e.emitLabel(badL)
		msg, err := e.emitStringConcat(Value{Ref: e.internString("Cannot convert "), Ty: TypePtr}, arg)
		if err != nil {
			return Value{}, err
		}
		if msg, err = e.emitStringConcat(msg, Value{Ref: e.internString(" to a BigInt"), Ty: TypePtr}); err != nil {
			return Value{}, err
		}
		e.ensureExceptionHelpers()
		errObj := e.buildErrorObj(errorKindIDs["SyntaxError"], msg.Ref, e.internString("SyntaxError"))
		e.excThrow(errObj)
		e.emitTerminator("unreachable")
		e.emitLabel(okL)
		return Value{Ref: reg, Ty: BigIntType()}, nil
	case arg.Ty.IR == "i64" || arg.Ty.IR == "i32" || arg.Ty.IR == "i16" || arg.Ty.IR == "i8" || arg.Ty.IR == "i1":
		wide := e.convert(arg, TypeI64)
		reg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_bigint_from_i64(i64 %s)", reg, wide.Ref))
		return Value{Ref: reg, Ty: BigIntType()}, nil
	case arg.Ty.Float:
		// `number` is a double (TDD-00123). BigInt(x) converts an integer-valued,
		// finite, in-i64-range double; a fractional/NaN/Infinity/out-of-range
		// value is a RangeError in JS, thrown here as a catchable Error. The
		// range guard (< |2^63|, which excludes NaN/Inf since every fcmp against
		// them is false) precedes the fptosi so it is never out-of-range UB, then
		// a fptosi→sitofp round-trip confirms integrality.
		d := e.convert(arg, TypeF64)
		inLo, inHi, inRange := e.freshReg(), e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = fcmp oge double %s, 0xC3E0000000000000", inLo, d.Ref))
		e.emitInstr(fmt.Sprintf("%s = fcmp olt double %s, 0x43E0000000000000", inHi, d.Ref))
		e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", inRange, inLo, inHi))
		rangeOkL := e.freshLabel("bigint.num.range_ok")
		badL := e.freshLabel("bigint.num.bad")
		buildL := e.freshLabel("bigint.num.build")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", inRange, rangeOkL, badL))
		e.emitLabel(rangeOkL)
		iReg, backReg, isInt := e.freshReg(), e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = fptosi double %s to i64", iReg, d.Ref))
		e.emitInstr(fmt.Sprintf("%s = sitofp i64 %s to double", backReg, iReg))
		e.emitInstr(fmt.Sprintf("%s = fcmp oeq double %s, %s", isInt, d.Ref, backReg))
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isInt, buildL, badL))
		e.emitLabel(badL)
		// NumberToBigInt: a non-integral number is a RangeError naming it.
		numStr, err := e.emitValueToString(d)
		if err != nil {
			return Value{}, err
		}
		msg, err := e.emitStringConcat(Value{Ref: e.internString("The number "), Ty: TypePtr}, numStr)
		if err != nil {
			return Value{}, err
		}
		if msg, err = e.emitStringConcat(msg, Value{Ref: e.internString(" cannot be converted to a BigInt because it is not an integer"), Ty: TypePtr}); err != nil {
			return Value{}, err
		}
		e.ensureExceptionHelpers()
		rangeErr := e.buildErrorObj(errorKindIDs["RangeError"], msg.Ref, e.internString("RangeError"))
		e.excThrow(rangeErr)
		e.emitTerminator("unreachable")
		e.emitLabel(buildL)
		reg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_bigint_from_i64(i64 %s)", reg, iReg))
		return Value{Ref: reg, Ty: BigIntType()}, nil
	default:
		// An object (an array, a function, a class instance): ToPrimitive,
		// then ToBigInt, through its box.
		boxed, err := e.emitBoxValue(arg)
		if err != nil {
			return Value{}, err
		}
		return e.bigintFromRuntime(boxed, pos, true)
	}
}

// emitToBigInt is ToBigInt(v) for a box v (a BigInt64Array element store):
// BigInt() but for a number, which is a TypeError ("Cannot convert 1 to a
// BigInt"), not converted.
func (e *Emitter) emitToBigInt(v Value, pos ast.Pos) (Value, error) {
	e.ensureBigInt()
	tag, _ := e.emitUnboxTagPayload(v)
	isInt, isFloat, isNum := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isInt, tag, kmlTagInt))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isFloat, tag, kmlTagFloat))
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", isNum, isInt, isFloat))
	numL, okL := e.freshLabel("tobigint.num"), e.freshLabel("tobigint.ok")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNum, numL, okL))
	e.emitLabel(numL)
	shown, err := e.emitDynamicToString(v)
	if err != nil {
		return Value{}, err
	}
	msg, err := e.emitStringConcat(Value{Ref: e.internString("Cannot convert "), Ty: TypePtr}, shown)
	if err != nil {
		return Value{}, err
	}
	if msg, err = e.emitStringConcat(msg, Value{Ref: e.internString(" to a BigInt"), Ty: TypePtr}); err != nil {
		return Value{}, err
	}
	e.ensureExceptionHelpers()
	e.excThrow(e.buildErrorObj(errorKindIDs["TypeError"], msg.Ref, e.internString("TypeError")))
	e.emitTerminator("unreachable")
	e.emitLabel(okL)
	return e.bigintFromRuntime(v, pos, true)
}

// bigintFromRuntime is BigInt(arg) for a value whose kind is known only at
// run time (a box, or a scalar that may be absent): each kind converts as
// its static type does, an absent one throws JavaScript's TypeError. An
// object (objects says one may be) converts its ToPrimitive (number hint)
// result, which is dispatched again without the object case.
func (e *Emitter) bigintFromRuntime(arg Value, pos ast.Pos, objects bool) (Value, error) {
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca "+irPtr+", align 8", slot))
	doneL := e.freshLabel("bigint.rt.done")
	throwL := e.freshLabel("bigint.rt.absent")
	convert := func(v Value) error {
		r, err := e.bigintFromValue(v, pos)
		if err != nil {
			return err
		}
		e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", r.Ref, slot))
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
		return nil
	}
	if isNullableScalar(arg.Ty) {
		present, payload := e.nullableScalarAggParts(arg)
		presL := e.freshLabel("bigint.rt.present")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", present, presL, throwL))
		e.emitLabel(presL)
		if err := convert(payload); err != nil {
			return Value{}, err
		}
	} else {
		tag, payload := e.emitUnboxTagPayload(arg)
		for _, k := range []struct {
			tag int
			ty  Type
		}{{kmlTagInt, TypeF64}, {kmlTagFloat, TypeF64}, {kmlTagString, TypePtr}, {kmlTagBoolean, TypeBool}} {
			is := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", is, tag, k.tag))
			hitL, nextL := e.freshLabel("bigint.rt.kind"), e.freshLabel("bigint.rt.next")
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", is, hitL, nextL))
			e.emitLabel(hitL)
			var v Value
			if k.ty.Float {
				v = Value{Ref: e.emitAnyToNum(arg), Ty: TypeF64}
			} else {
				v = e.emitUnboxBoxToType(arg.Ref, k.ty)
			}
			if err := convert(v); err != nil {
				return Value{}, err
			}
			e.emitLabel(nextL)
		}
		// A boxed bigint (emit_bigint_box.go) is itself; any other object
		// (a class instance, a dynamic bag, an array, a function) converts
		// its primitive. null and undefined fall to the TypeError.
		isObj := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isObj, tag, kmlTagObject))
		probeL := e.freshLabel("bigint.rt.probe")
		notObjL := e.freshLabel("bigint.rt.notobj")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isObj, probeL, notObjL))
		e.emitLabel(notObjL)
		isNull, isUndef, nullish := e.freshReg(), e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isNull, tag, kmlTagNull))
		e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isUndef, tag, kmlTagUndefined))
		e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", nullish, isNull, isUndef))
		primL := e.freshLabel("bigint.rt.toprim")
		if objects {
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", nullish, throwL, primL))
		} else {
			e.emitTerminator(fmt.Sprintf("br label %%%s", throwL))
		}
		e.emitLabel(probeL)
		cell, isBig := e.emitBoxedBigIntProbe(payload)
		loadL := e.freshLabel("bigint.rt.load")
		other := throwL
		if objects {
			other = primL
		}
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isBig, loadL, other))
		e.emitLabel(loadL)
		b := e.emitBoxedBigIntLoad(cell)
		e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", b.Ref, slot))
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
		if objects {
			// `{ valueOf() { return 44 } }`, `[Symbol.toPrimitive]`: its
			// primitive converts.
			e.emitLabel(primL)
			prim := e.emitAnyToPrimitiveHint(arg.Ref, hintNumber)
			pv, err := e.bigintFromRuntime(Value{Ref: prim, Ty: TypeAny}, pos, false)
			if err != nil {
				return Value{}, err
			}
			e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", pv.Ref, slot))
			e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
		}
	}
	e.emitLabel(throwL)
	boxed, err := e.emitBoxValue(arg)
	if err != nil {
		return Value{}, err
	}
	str, err := e.emitValueToString(boxed)
	if err != nil {
		return Value{}, err
	}
	msg, err := e.emitStringConcat(Value{Ref: e.internString("Cannot convert "), Ty: TypePtr}, str)
	if err != nil {
		return Value{}, err
	}
	if msg, err = e.emitStringConcat(msg, Value{Ref: e.internString(" to a BigInt"), Ty: TypePtr}); err != nil {
		return Value{}, err
	}
	e.ensureExceptionHelpers()
	errObj := e.buildErrorObj(errorKindIDs["TypeError"], msg.Ref, e.internString("TypeError"))
	e.excThrow(errObj)
	e.emitTerminator("unreachable")
	e.emitLabel(doneL)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", r, slot))
	return Value{Ref: r, Ty: BigIntType()}, nil
}

// bigintFromToBigInt is ECMA-262's ToBigInt of an evaluated value: BigInt()'s
// conversion, except that a number is a TypeError (`Cannot convert 5 to a
// BigInt`).
func (e *Emitter) bigintFromToBigInt(v Value, pos ast.Pos) (Value, error) {
	if v.Ty.Float || isIntegerNumberTy(v.Ty) && v.Ty.IR != "i1" {
		desc, err := e.emitValueToString(v)
		if err != nil {
			return Value{}, err
		}
		msg, err := e.emitStringConcat(Value{Ref: e.internString("Cannot convert "), Ty: TypePtr}, desc)
		if err != nil {
			return Value{}, err
		}
		if msg, err = e.emitStringConcat(msg, Value{Ref: e.internString(" to a BigInt"), Ty: TypePtr}); err != nil {
			return Value{}, err
		}
		e.emitThrowTypeErrorValue(msg.Ref)
		e.emitLabel(e.freshLabel("tobigint.dead"))
		e.ensureBigInt()
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_bigint_from_i64(i64 0)", r))
		return Value{Ref: r, Ty: BigIntType()}, nil
	}
	return e.bigintFromValue(v, pos)
}
