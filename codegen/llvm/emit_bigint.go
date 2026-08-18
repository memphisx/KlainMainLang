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
		"declare ptr @__kml_bigint_from_str(ptr, i64, i32)",
		"declare ptr @__kml_bigint_from_i64(i64)",
		"declare i64 @__kml_bigint_to_i64(ptr)",
		"declare ptr @__kml_bigint_to_str(ptr, i32)",
		"declare ptr @__kml_bigint_add(ptr, ptr)",
		"declare ptr @__kml_bigint_sub(ptr, ptr)",
		"declare ptr @__kml_bigint_mul(ptr, ptr)",
		"declare ptr @__kml_bigint_tdiv(ptr, ptr)",
		"declare ptr @__kml_bigint_mod(ptr, ptr)",
		"declare ptr @__kml_bigint_pow(ptr, ptr)",
		"declare ptr @__kml_bigint_and(ptr, ptr)",
		"declare ptr @__kml_bigint_or(ptr, ptr)",
		"declare ptr @__kml_bigint_xor(ptr, ptr)",
		"declare ptr @__kml_bigint_shl(ptr, ptr)",
		"declare ptr @__kml_bigint_shr(ptr, ptr)",
		"declare ptr @__kml_bigint_neg(ptr)",
		"declare ptr @__kml_bigint_not(ptr)",
		"declare i32 @__kml_bigint_cmp(ptr, ptr)",
		"declare i32 @__kml_bigint_cmp_double(ptr, double)",
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
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_bigint_from_str(ptr %s, i64 %d, i32 %d)", reg, strPtr, len(digits), radix))
	return Value{Ref: reg, Ty: BigIntType()}, nil
}

// emitBigIntToString renders a bigint as its base-10 digits. suffix controls the
// trailing `n` — real JS shows it for console.log(10n) → "10n" but not for
// String(10n) / `${10n}` → "10", the same console-vs-String split symbol uses.
func (e *Emitter) emitBigIntToString(val Value, suffix bool) (Value, error) {
	e.ensureBigInt()
	reg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_bigint_to_str(ptr %s, i32 10)", reg, val.Ref))
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
	if fn, ok := bigIntBinFn[op]; ok {
		if op == "/" || op == "%" {
			e.emitBigIntDivZeroGuard(right)
		}
		reg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_bigint_%s(ptr %s, ptr %s)", reg, fn, left.Ref, right.Ref))
		return Value{Ref: reg, Ty: BigIntType()}, nil
	}
	if op == ">>>" {
		return Value{}, fmt.Errorf("%d:%d: unsigned right shift (>>>) is not defined on BigInt (a TypeError in JS)", pos.Line, pos.Col)
	}
	pred, ok := bigIntCmpPred[op]
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: operator '%s' is not supported on BigInt", pos.Line, pos.Col, op)
	}
	cmpReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_bigint_cmp(ptr %s, ptr %s)", cmpReg, left.Ref, right.Ref))
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
	if left.Ty.IsBigInt {
		other = right
	}
	// bigint vs null/undefined: never equal, and not orderable.
	if other.Ty.IsNull {
		switch op {
		case "==", "===":
			return Value{Ref: "0", Ty: TypeBool}, nil
		case "!=", "!==":
			return Value{Ref: "1", Ty: TypeBool}, nil
		default:
			return Value{}, fmt.Errorf("%d:%d: a bigint cannot be ordered against null/undefined with '%s'", pos.Line, pos.Col, op)
		}
	}
	switch op {
	case "===":
		return Value{Ref: "0", Ty: TypeBool}, nil
	case "!==":
		return Value{Ref: "1", Ty: TypeBool}, nil
	case "<", ">", "<=", ">=", "==", "!=":
		if other.Ty.Float {
			if e.compatJS() {
				return e.emitBigIntFloatCompare(op, left, right, pos)
			}
			return Value{}, fmt.Errorf("%d:%d: comparing a bigint with a floating-point number is rejected by default (it is almost always a bug) — convert explicitly with BigInt(x)/Number(x), or use -compat=js for JS's exact comparison (TDD-00075)", pos.Line, pos.Col)
		}
		if !isIntegerNumberTy(other.Ty) {
			return Value{}, fmt.Errorf("%d:%d: a bigint can only be compared with another bigint or an integer number, not this type", pos.Line, pos.Col)
		}
		return e.emitBigIntBinary(op, e.bigintOperand(left), e.bigintOperand(right), pos)
	default:
		return Value{}, fmt.Errorf("%d:%d: cannot mix BigInt and other types in operator '%s' — arithmetic between a bigint and a number is a TypeError in JS; convert explicitly with BigInt(x)/Number(x)", pos.Line, pos.Col, op)
	}
}

// bigintOperand returns v as a bigint — itself if already bigint, else an
// integer number widened and lifted via from_i64.
func (e *Emitter) bigintOperand(v Value) Value {
	if v.Ty.IsBigInt {
		return v
	}
	e.ensureBigInt()
	w := e.coerce(v, TypeI64)
	reg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_bigint_from_i64(i64 %s)", reg, w.Ref))
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
	if !left.Ty.IsBigInt {
		bigintVal, floatVal, bigintIsLeft = right, left, false
	}
	dRef := floatVal.Ref
	if floatVal.Ty.IR == "float" { // widen a float32 operand to double for the ABI
		dr := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = fpext float %s to double", dr, floatVal.Ref))
		dRef = dr
	}
	cmpReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_bigint_cmp_double(ptr %s, double %s)", cmpReg, bigintVal.Ref, dRef))
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
	if ty.Float || ty.IsBigInt || ty.IsObject || ty.IsArray || ty.IsFunc || ty.IsSymbol {
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
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_bigint_from_i64(i64 0)", zero))
	cmpReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_bigint_cmp(ptr %s, ptr %s)", cmpReg, right.Ref, zero))
	isZero := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 0", isZero, cmpReg))
	zeroL := e.freshLabel("bigint.div.zero")
	okL := e.freshLabel("bigint.div.ok")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isZero, zeroL, okL))
	e.emitLabel(zeroL)
	e.emitInternalThrow(e.internString("Division by zero"))
	e.emitLabel(okL)
}

// emitBigIntToStringMethod implements `(x).toString()` and `(x).toString(radix)`
// on a bigint receiver — bare digits in the given base (default 10), no `n`
// suffix, matching JS. Reuses the ABI's radix-aware to_str.
func (e *Emitter) emitBigIntToStringMethod(recvExpr ast.Expression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) > 1 {
		return Value{}, fmt.Errorf("%d:%d: BigInt.toString() takes at most one argument (a radix)", pos.Line, pos.Col)
	}
	recv, err := e.emitExpr(recvExpr)
	if err != nil {
		return Value{}, err
	}
	e.ensureBigInt()
	radixRef := "10"
	if len(args) == 1 {
		rv, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		if rv.Ty.IsBigInt || isStringTy(rv.Ty) || rv.Ty.Float {
			return Value{}, fmt.Errorf("%d:%d: BigInt.toString()'s radix must be an integer (2–36)", pos.Line, pos.Col)
		}
		w := e.coerce(rv, TypeI64)
		radixReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = trunc i64 %s to i32", radixReg, w.Ref))
		radixRef = radixReg
	}
	reg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_bigint_to_str(ptr %s, i32 %s)", reg, recv.Ref, radixRef))
	return Value{Ref: reg, Ty: TypePtr}, nil
}

// emitBigIntNeg / emitBigIntNot back unary `-` and `~` on a bigint operand.
func (e *Emitter) emitBigIntUnary(fn string, val Value) Value {
	e.ensureBigInt()
	reg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_bigint_%s(ptr %s)", reg, fn, val.Ref))
	return Value{Ref: reg, Ty: BigIntType()}
}

// emitBigIntConstructor lowers BigInt(x): from an integer number (i64) or a
// string. A float or other type is a clean compile error (real JS RangeErrors a
// non-integer number; we reject statically where we can).
func (e *Emitter) emitBigIntConstructor(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: BigInt() takes exactly one argument", pos.Line, pos.Col)
	}
	arg, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	e.ensureBigInt()
	switch {
	case arg.Ty.IsBigInt:
		return arg, nil
	case isStringTy(arg.Ty):
		reg := e.freshReg()
		lenReg := e.freshReg()
		e.ensureStrlen()
		e.emitInstr(fmt.Sprintf("%s = call i64 @strlen(ptr %s)", lenReg, arg.Ref))
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_bigint_from_str(ptr %s, i64 %s, i32 10)", reg, arg.Ref, lenReg))
		return Value{Ref: reg, Ty: BigIntType()}, nil
	case arg.Ty.IR == "i64" || arg.Ty.IR == "i32" || arg.Ty.IR == "i16" || arg.Ty.IR == "i8" || arg.Ty.IR == "i1":
		wide := e.coerce(arg, TypeI64)
		reg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_bigint_from_i64(i64 %s)", reg, wide.Ref))
		return Value{Ref: reg, Ty: BigIntType()}, nil
	default:
		return Value{}, fmt.Errorf("%d:%d: BigInt() accepts an integer number or a string, not this type (a non-integer number is a RangeError in JS)", pos.Line, pos.Col)
	}
}
