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
		"asin", "acos", "atan", "sinh", "cosh", "tanh", "expm1", "log1p":
		return e.emitMathUnaryFloat(property, args, pos)
	case "cbrt":
		// Not the platform libm cbrt — that isn't reliably correctly-rounded
		// (glibc's runtime cbrt(27) is 3.0000000000000004, where macOS/V8/fdlibm
		// give exactly 3). @__kml_cbrt is the deterministic fdlibm algorithm.
		return e.emitMathCbrt(args, pos)
	case "pow":
		// __kml_js_pow: libm pow plus the JS deviation — |base| exactly 1
		// with an infinite exponent is NaN (ensureJsPow).
		e.ensureJsPow()
		return e.emitMathBinaryFloat("__kml_js_pow", args, pos)
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
	// Float input stays a double all the way through — real JS returns a
	// double here too, and this is what lets NaN/±Infinity pass through
	// unchanged instead of hitting an fptosi (whose result is poison for any
	// non-finite input — the pre-fix behavior printed garbage). An integral
	// double prints without a fraction (TDD-00080's shortest-round-trip
	// formatter), so Math.floor(2.9) still displays as `2`.
	fval := e.coerce(val, TypeF64)
	e.ensureMathFuncs()
	rounded := e.freshReg()
	if fn == "round" {
		// JS Math.round is floor(x + 0.5) — ties round toward +Infinity
		// (Math.round(-2.5) is -2), not libm round's half-away-from-zero.
		// A zero result from a negative input must be the *signed* zero
		// (Math.round(-0.5) is -0), which floor(0.0) alone loses.
		bumped := e.freshReg()
		floored := e.freshReg()
		isZero := e.freshReg()
		isNegIn := e.freshReg()
		needNegZero := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = fadd double %s, 5.0e-1", bumped, fval.Ref))
		e.emitInstr(fmt.Sprintf("%s = call double @floor(double %s)", floored, bumped))
		e.emitInstr(fmt.Sprintf("%s = fcmp oeq double %s, 0.0", isZero, floored))
		e.emitInstr(fmt.Sprintf("%s = fcmp olt double %s, 0.0", isNegIn, fval.Ref))
		e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", needNegZero, isZero, isNegIn))
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, double -0.0, double %s", rounded, needNegZero, floored))
	} else {
		e.emitInstr(fmt.Sprintf("%s = call double @%s(double %s)", rounded, fn, fval.Ref))
	}
	return Value{Ref: rounded, Ty: TypeF64}, nil
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

// emitMathCbrt routes Math.cbrt through @__kml_cbrt (ensureCbrt), the
// correctly-rounded fdlibm implementation, instead of the platform libm cbrt.
func (e *Emitter) emitMathCbrt(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: Math.cbrt expects 1 argument", pos.Line, pos.Col)
	}
	val, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	fval := e.coerce(val, TypeF64)
	e.ensureCbrt()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call double @__kml_cbrt(double %s)", r, fval.Ref))
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
	// A spread argument (Math.max(...arr), Math.min(a, ...arr, b)) folds the
	// array at runtime instead of over a static, compile-time-known arg list
	// (TDD-00106 V2). Any argument count is valid then, including a lone spread.
	if anySpread(args) {
		return e.emitMathMinMaxSpread(fn, args, pos)
	}
	if len(args) < 2 {
		return Value{}, fmt.Errorf("%d:%d: Math.%s expects at least 2 arguments", pos.Line, pos.Col, fn)
	}
	vals := make([]Value, 0, len(args))
	anyFloat := false
	for _, arg := range args {
		v, err := e.emitExpr(arg)
		if err != nil {
			return Value{}, err
		}
		vals = append(vals, v)
		if v.Ty.Float {
			anyFloat = true
		}
	}
	if anyFloat {
		// Any float operand promotes the whole reduction to double, and the
		// fold uses llvm.minimum/llvm.maximum — the IEEE-754 minimum/maximum
		// operations, which propagate a NaN operand and order -0.0 below
		// +0.0, exactly real Math.min/Math.max. (A plain fcmp olt/select
		// silently *dropped* NaN — the ordered compare is false for it —
		// and this is also what a mixed Math.min(1, someDouble) used to get
		// wrong by coercing the double into the first operand's i64 type.)
		e.ensureFloatMinMaxIntrinsics()
		intrinsic := "llvm.minimum.f64"
		if fn == "max" {
			intrinsic = "llvm.maximum.f64"
		}
		result := e.coerce(vals[0], TypeF64)
		for _, v := range vals[1:] {
			next := e.coerce(v, TypeF64)
			r := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call double @%s(double %s, double %s)", r, intrinsic, result.Ref, next.Ref))
			result = Value{Ref: r, Ty: TypeF64}
		}
		return result, nil
	}
	result := e.coerce(vals[0], TypeI64)
	for _, v := range vals[1:] {
		next := e.coerce(v, TypeI64)
		cmp := e.freshReg()
		r := e.freshReg()
		op := "icmp slt"
		if fn == "max" {
			op = "icmp sgt"
		}
		e.emitInstr(fmt.Sprintf("%s = %s i64 %s, %s", cmp, op, result.Ref, next.Ref))
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", r, cmp, result.Ref, next.Ref))
		result = Value{Ref: r, Ty: TypeI64}
	}
	return result, nil
}

// emitMathMinMaxSpread folds Math.min/Math.max over a mix of static arguments
// and runtime-length spread arrays (TDD-00106 V2). It seeds an accumulator with
// the reduction identity (±Infinity for a float result, INT64_MIN/MAX for an
// integer one), folds each static arg, and loops over each spread array folding
// element by element — so Math.max(...arr), Math.min(a, ...arr, b), and multiple
// spreads all work. An empty fold (Math.max(...[])) yields the seed: ±Infinity
// for a float result exactly matches JS; an i64 result has no Infinity, so it
// yields the extreme i64 value, the same concrete-type stand-in ADR-00157 uses.
func (e *Emitter) emitMathMinMaxSpread(fn string, args []ast.Expression, pos ast.Pos) (Value, error) {
	type mmItem struct {
		spread bool
		ptr    string
		length string
		elemTy Type
		val    Value
	}
	// Pass 1: evaluate every argument once, in source order; decide float vs int.
	items := make([]mmItem, 0, len(args))
	anyFloat := false
	for _, arg := range args {
		if sp, ok := arg.(*ast.SpreadElement); ok {
			ptrReg, lenReg, elemTy, err := e.resolveArrayForHOF(sp.Arg, sp.Arg.GetPos())
			if err != nil {
				return Value{}, err
			}
			if elemTy.IsArray || elemTy.IsObject {
				return Value{}, fmt.Errorf("%d:%d: Math.%s cannot spread an array of arrays or objects", sp.Arg.GetPos().Line, sp.Arg.GetPos().Col, fn)
			}
			if elemTy.Float {
				anyFloat = true
			}
			items = append(items, mmItem{spread: true, ptr: ptrReg, length: lenReg, elemTy: elemTy})
		} else {
			v, err := e.emitExpr(arg)
			if err != nil {
				return Value{}, err
			}
			if v.Ty.Float {
				anyFloat = true
			}
			items = append(items, mmItem{val: v})
		}
	}

	resTy := TypeI64
	if anyFloat {
		resTy = TypeF64
	}
	acc := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca %s, align 8", acc, resTy.IR))
	var seed string
	switch {
	case anyFloat && fn == "max":
		seed = "0xFFF0000000000000" // -Infinity
	case anyFloat:
		seed = "0x7FF0000000000000" // +Infinity
	case fn == "max":
		seed = "-9223372036854775808" // INT64_MIN
	default:
		seed = "9223372036854775807" // INT64_MAX
	}
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align 8", resTy.IR, seed, acc))
	if anyFloat {
		e.ensureFloatMinMaxIntrinsics()
	}

	// combine folds one already-result-typed value into the accumulator.
	combine := func(nextRef string) {
		cur := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align 8", cur, resTy.IR, acc))
		r := e.freshReg()
		if anyFloat {
			intrinsic := "llvm.minimum.f64"
			if fn == "max" {
				intrinsic = "llvm.maximum.f64"
			}
			e.emitInstr(fmt.Sprintf("%s = call double @%s(double %s, double %s)", r, intrinsic, cur, nextRef))
		} else {
			cmp := e.freshReg()
			op := "icmp slt"
			if fn == "max" {
				op = "icmp sgt"
			}
			e.emitInstr(fmt.Sprintf("%s = %s i64 %s, %s", cmp, op, cur, nextRef))
			e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", r, cmp, cur, nextRef))
		}
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align 8", resTy.IR, r, acc))
	}

	for _, it := range items {
		if !it.spread {
			nv := e.coerce(it.val, resTy)
			combine(nv.Ref)
			continue
		}
		// Loop over the spread array, folding each element into the accumulator.
		idxAlloca := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxAlloca))
		e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idxAlloca))
		condL := e.freshLabel("mm.cond")
		bodyL := e.freshLabel("mm.body")
		doneL := e.freshLabel("mm.done")
		e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
		e.emitLabel(condL)
		idxVal := e.freshReg()
		done := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idxVal, idxAlloca))
		e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", done, idxVal, it.length))
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", done, doneL, bodyL))
		e.emitLabel(bodyL)
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", gep, it.elemTy.IR, it.ptr, idxVal))
		elem := e.loadArrayElem(gep, it.elemTy)
		ev := e.coerce(elem, resTy)
		combine(ev.Ref)
		idxNext := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", idxNext, idxVal))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", idxNext, idxAlloca))
		e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
		e.emitLabel(doneL)
	}

	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align 8", result, resTy.IR, acc))
	return Value{Ref: result, Ty: resTy}, nil
}

func (e *Emitter) emitMathSign(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: Math.sign expects 1 argument", pos.Line, pos.Col)
	}
	val, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	if val.Ty.Float {
		// Float path: ±Infinity give ±1, and NaN/±0 return the input value
		// itself — the exact JS behavior (Math.sign(NaN) is NaN, and the
		// sign of a signed zero is that same zero). Both fcmp's are ordered,
		// so a NaN input fails both and falls through to itself.
		fval := e.coerce(val, TypeF64)
		isPos := e.freshReg()
		isNeg := e.freshReg()
		fromPos := e.freshReg()
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = fcmp ogt double %s, 0.0", isPos, fval.Ref))
		e.emitInstr(fmt.Sprintf("%s = fcmp olt double %s, 0.0", isNeg, fval.Ref))
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, double 1.0, double %s", fromPos, isPos, fval.Ref))
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, double -1.0, double %s", r, isNeg, fromPos))
		return Value{Ref: r, Ty: TypeF64}, nil
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
