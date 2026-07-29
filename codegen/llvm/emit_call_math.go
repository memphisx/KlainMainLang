package llvm

import (
	"KlainMainLang/ast"
	"fmt"
	"runtime"
)

func (e *Emitter) emitMathCall(property string, args []ast.Expression, pos ast.Pos) (Value, error) {
	switch property {
	case "floor", "ceil", "round", "trunc":
		return e.emitMathRound(property, args, pos)
	case "abs":
		return e.emitMathAbs(args, pos)
	case "sqrt", "log", "log2", "log10", "sin", "cos", "tan",
		"asin", "acos", "atan", "sinh", "cosh", "tanh", "cbrt", "expm1", "log1p":
		return e.emitMathUnaryFloat(property, args, pos)
	case "pow":
		return e.emitMathBinaryFloat("pow", args, pos)
	case "hypot":
		return e.emitMathBinaryFloat("hypot", args, pos)
	case "atan2":
		return e.emitMathBinaryFloat("atan2", args, pos)
	case "min":
		return e.emitMathMinMax("min", args, pos)
	case "max":
		return e.emitMathMinMax("max", args, pos)
	case "sign":
		return e.emitMathSign(args, pos)
	case "random":
		return e.emitMathRandom(pos)
	case "clamp":
		return e.emitMathClamp(args, pos)
	case "clz32":
		return e.emitMathClz32(args, pos)
	case "fround":
		return e.emitMathFround(args, pos)
	case "imul":
		return e.emitMathImul(args, pos)
	}
	return Value{}, fmt.Errorf("%d:%d: Math.%s is not supported", pos.Line, pos.Col, property)
}

// emitMathClz32 counts leading zero bits in the ToUint32 of its argument,
// via LLVM's own llvm.ctlz.i32 intrinsic (the "is_zero_undef" second operand
// is false, so clz32(0) correctly returns 32, matching real JS).
func (e *Emitter) emitMathClz32(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: Math.clz32 expects 1 argument", pos.Line, pos.Col)
	}
	val, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	i64Val := e.coerce(val, TypeI64)
	trunc := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = trunc i64 %s to i32", trunc, i64Val.Ref))
	e.ensureCtlz32()
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @llvm.ctlz.i32(i32 %s, i1 false)", result, trunc))
	ext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = zext i32 %s to i64", ext, result))
	return Value{Ref: ext, Ty: TypeI64}, nil
}

// emitMathFround rounds a double to the nearest float32-representable value,
// returned widened back to double — a plain fptrunc/fpext round-trip.
func (e *Emitter) emitMathFround(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: Math.fround expects 1 argument", pos.Line, pos.Col)
	}
	val, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	dblVal := e.coerce(val, TypeF64)
	narrowed := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = fptrunc double %s to float", narrowed, dblVal.Ref))
	widened := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = fpext float %s to double", widened, narrowed))
	return Value{Ref: widened, Ty: TypeF64}, nil
}

// emitMathImul performs 32-bit integer multiplication with wraparound
// (real JS semantics — plain `a * b` uses double precision and loses exact
// integer results past 2^53; imul is exactly the escape hatch for that).
func (e *Emitter) emitMathImul(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 2 {
		return Value{}, fmt.Errorf("%d:%d: Math.imul expects 2 arguments", pos.Line, pos.Col)
	}
	aVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	bVal, err := e.emitExpr(args[1])
	if err != nil {
		return Value{}, err
	}
	a32 := e.freshReg()
	b32 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = trunc i64 %s to i32", a32, e.coerce(aVal, TypeI64).Ref))
	e.emitInstr(fmt.Sprintf("%s = trunc i64 %s to i32", b32, e.coerce(bVal, TypeI64).Ref))
	prod := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = mul i32 %s, %s", prod, a32, b32))
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sext i32 %s to i64", result, prod))
	return Value{Ref: result, Ty: TypeI64}, nil
}

func (e *Emitter) emitMathRound(fn string, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: Math.%s expects 1 argument", pos.Line, pos.Col, fn)
	}
	val, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	if val.Ty.IR == "i64" || (val.Ty.IsInteger() && !val.Ty.Float) {
		return e.coerce(val, TypeI64), nil
	}
	fval := e.coerce(val, TypeF64)
	e.ensureMathFuncs()
	rounded := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call double @%s(double %s)", rounded, fn, fval.Ref))
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = fptosi double %s to i64", result, rounded))
	return Value{Ref: result, Ty: TypeI64}, nil
}

func (e *Emitter) emitMathAbs(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: Math.abs expects 1 argument", pos.Line, pos.Col)
	}
	val, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	if val.Ty.Float {
		e.ensureMathFuncs()
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call double @fabs(double %s)", r, val.Ref))
		return Value{Ref: r, Ty: TypeF64}, nil
	}
	iVal := e.coerce(val, TypeI64)
	neg := e.freshReg()
	cmp := e.freshReg()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 0, %s", neg, iVal.Ref))
	e.emitInstr(fmt.Sprintf("%s = icmp sge i64 %s, 0", cmp, iVal.Ref))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", r, cmp, iVal.Ref, neg))
	return Value{Ref: r, Ty: TypeI64}, nil
}

func (e *Emitter) emitMathUnaryFloat(fn string, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: Math.%s expects 1 argument", pos.Line, pos.Col, fn)
	}
	val, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	fval := e.coerce(val, TypeF64)
	e.ensureMathFuncs()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call double @%s(double %s)", r, fn, fval.Ref))
	return Value{Ref: r, Ty: TypeF64}, nil
}

func (e *Emitter) emitMathBinaryFloat(fn string, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 2 {
		return Value{}, fmt.Errorf("%d:%d: Math.%s expects 2 arguments", pos.Line, pos.Col, fn)
	}
	v1, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	v2, err := e.emitExpr(args[1])
	if err != nil {
		return Value{}, err
	}
	f1 := e.coerce(v1, TypeF64)
	f2 := e.coerce(v2, TypeF64)
	e.ensureMathFuncs()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call double @%s(double %s, double %s)", r, fn, f1.Ref, f2.Ref))
	return Value{Ref: r, Ty: TypeF64}, nil
}

func (e *Emitter) emitMathMinMax(fn string, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 2 {
		return Value{}, fmt.Errorf("%d:%d: Math.%s expects at least 2 arguments", pos.Line, pos.Col, fn)
	}
	result, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	for _, arg := range args[1:] {
		next, err := e.emitExpr(arg)
		if err != nil {
			return Value{}, err
		}
		next = e.coerce(next, result.Ty)
		cmp := e.freshReg()
		r := e.freshReg()
		if result.Ty.Float {
			op := "fcmp olt"
			if fn == "max" {
				op = "fcmp ogt"
			}
			e.emitInstr(fmt.Sprintf("%s = %s double %s, %s", cmp, op, result.Ref, next.Ref))
		} else {
			op := "icmp slt"
			if fn == "max" {
				op = "icmp sgt"
			}
			e.emitInstr(fmt.Sprintf("%s = %s %s %s, %s", cmp, op, result.Ty.IR, result.Ref, next.Ref))
		}
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, %s %s, %s %s", r, cmp, result.Ty.IR, result.Ref, result.Ty.IR, next.Ref))
		result = Value{Ref: r, Ty: result.Ty}
	}
	return result, nil
}

func (e *Emitter) emitMathSign(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: Math.sign expects 1 argument", pos.Line, pos.Col)
	}
	val, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	iVal := e.coerce(val, TypeI64)
	isPos := e.freshReg()
	isNeg := e.freshReg()
	fromPos := e.freshReg()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp sgt i64 %s, 0", isPos, iVal.Ref))
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 0", isNeg, iVal.Ref))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 1, i64 0", fromPos, isPos))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 -1, i64 %s", r, isNeg, fromPos))
	return Value{Ref: r, Ty: TypeI64}, nil
}

func (e *Emitter) emitMathRandom(_ ast.Pos) (Value, error) {
	switch runtime.GOOS {
	case "darwin", "freebsd", "openbsd", "netbsd", "dragonfly":
		// arc4random() — cryptographic quality, no seeding required (BSD/macOS).
		e.ensureArc4Random()
		raw := e.freshReg()
		asFloat := e.freshReg()
		result := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @arc4random()", raw))
		e.emitInstr(fmt.Sprintf("%s = uitofp i32 %s to double", asFloat, raw))
		e.emitInstr(fmt.Sprintf("%s = fdiv double %s, 4294967295.0", result, asFloat))
		return Value{Ref: result, Ty: TypeF64}, nil
	default:
		// Portable fallback: a helper defined entirely in IR using C89 rand()/srand()/time().
		e.ensureRandRandom()
		result := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call double @__klain_math_random()", result))
		return Value{Ref: result, Ty: TypeF64}, nil
	}
}

// Math.clamp(x, lo, hi) — not in the JS spec but very handy.
func (e *Emitter) emitMathClamp(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 3 {
		return Value{}, fmt.Errorf("%d:%d: Math.clamp expects 3 arguments (value, min, max)", pos.Line, pos.Col)
	}
	vVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	loVal, err := e.emitExpr(args[1])
	if err != nil {
		return Value{}, err
	}
	hiVal, err := e.emitExpr(args[2])
	if err != nil {
		return Value{}, err
	}
	loVal = e.coerce(loVal, vVal.Ty)
	hiVal = e.coerce(hiVal, vVal.Ty)

	cmpLo := e.freshReg()
	clampedLo := e.freshReg()
	cmpHi := e.freshReg()
	r := e.freshReg()
	if vVal.Ty.Float {
		e.emitInstr(fmt.Sprintf("%s = fcmp ogt double %s, %s", cmpLo, vVal.Ref, loVal.Ref))
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, double %s, double %s", clampedLo, cmpLo, vVal.Ref, loVal.Ref))
		e.emitInstr(fmt.Sprintf("%s = fcmp olt double %s, %s", cmpHi, clampedLo, hiVal.Ref))
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, double %s, double %s", r, cmpHi, clampedLo, hiVal.Ref))
	} else {
		iV := e.coerce(vVal, TypeI64)
		iLo := e.coerce(loVal, TypeI64)
		iHi := e.coerce(hiVal, TypeI64)
		e.emitInstr(fmt.Sprintf("%s = icmp sgt i64 %s, %s", cmpLo, iV.Ref, iLo.Ref))
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", clampedLo, cmpLo, iV.Ref, iLo.Ref))
		e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, %s", cmpHi, clampedLo, iHi.Ref))
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", r, cmpHi, clampedLo, iHi.Ref))
	}
	return Value{Ref: r, Ty: vVal.Ty}, nil
}
