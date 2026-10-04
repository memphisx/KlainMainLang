package llvm

// emit_anyops.go — runtime operator dispatch on NaN-boxed dynamic values
// (TDD-00076 A2, `-compat=js` only; `-compat=strict` keeps the clean
// compile-time rejection). The NaN-box (TDD-00156) is what makes this
// cheap: the both-numbers fast path is two unsigned compares, a decode,
// the fop, and a re-encode.
//
// V1 semantics matrix (the TDD's unambiguous set, plus the numeric
// coercions that are exact and cheap):
//   - number ⊕ number: all arithmetic and relational operators.
//   - `+` with a string side: ToString concatenation (JS's + overload).
//   - null/undefined/boolean coerce numerically (null+1===1, true+1===2,
//     undefined+1 → NaN); a numeric string coerces via ToNumber in
//     arithmetic ("5"*2===10, ""*1===0, junk → NaN).
//   - relational on two strings: lexicographic; mixed: numeric.
//   - a heap reference is run through ToPrimitive (number hint) inside
//     __kml_any_tonum, so `{valueOf(){return 5}}*2` is 10, not NaN, at
//     every ToNumber site, not only the operators.
//   - ToBoolean: false/null/undefined/±0/NaN/"" are false, all else true.

import (
	"fmt"

	"KlainMainLang/ast"
)

// ensureAnyOps defines the ToNumber/ToBoolean runtime over NaN-boxed words.
func (e *Emitter) ensureAnyOps() {
	if e.usedAnyOps {
		return
	}
	e.usedAnyOps = true
	e.ensureNanBox()
	e.ensureStrHeaderRuntime()
	e.ensureToNumber()
	e.ensureNullDerefThrow()
	e.ensureAnyToPrimitive()
	e.emitGlobal("declare double @__kml_obj_tonum(i64)") // boxsrc/toprim.c
	symMsg := e.internString("Cannot convert a Symbol value to a number")
	e.emitGlobal(`
; JS ToNumber over a NaN-boxed word. Numbers decode; true/false -> 1/0;
; null -> 0; undefined -> NaN; a string goes through the same StringToNumber
; as Number(s) (@__kml_to_number: whitespace trimmed, Infinity, 0x/0o/0b,
; junk -> NaN — strtod alone took "inf" and rejected "  Infinity  ",
; ADR-01057); a boxed Symbol (an object whose hidden field 0 carries
; symbolTypeIDFlag, ADR-01059) throws the spec's TypeError; any other bare
; heap reference goes through ToPrimitive with the number hint first
; (@__kml_obj_tonum, boxsrc/toprim.c), as the spec's ToNumber does.
define double @__kml_any_tonum(i64 %v) {
entry:
  %isnum = icmp uge i64 %v, 562949953421312
  br i1 %isnum, label %num, label %notnum
num:
  %bits = sub i64 %v, 562949953421312
  %d = bitcast i64 %bits to double
  ret double %d
notnum:
  %isimm = icmp ult i64 %v, 65536
  br i1 %isimm, label %imm, label %ptr
imm:
  switch i64 %v, label %retnan [
    i64 2, label %zero
    i64 6, label %zero
    i64 7, label %one
  ]
zero:
  ret double 0.0
one:
  ret double 1.0
ptr:
  %kind = and i64 %v, 7
  %isstr = icmp eq i64 %kind, 0
  br i1 %isstr, label %str, label %notstr
str:
  %s = inttoptr i64 %v to ptr
  %pv = call double @__kml_to_number(ptr %s)
  ret double %pv
notstr:
  %isobj = icmp eq i64 %kind, 1
  br i1 %isobj, label %obj, label %objnum
obj:
  %opay = and i64 %v, -8
  %optr = inttoptr i64 %opay to ptr
  %f0 = load i64, ptr %optr, align 8
  %symbit = and i64 %f0, ` + fmt.Sprintf("%d", symbolTypeIDFlag) + `
  %issym = icmp ne i64 %symbit, 0
  br i1 %issym, label %symthrow, label %objnum
objnum:
  %on = call double @__kml_obj_tonum(i64 %v)
  ret double %on
symthrow:
  call void @__kml_throw_nullderef(ptr ` + symMsg + `)
  unreachable
retnan:
  ret double 0x7FF8000000000000
}

; JS ToBoolean over a NaN-boxed word.
define i1 @__kml_any_tobool(i64 %v) {
entry:
  %isnum = icmp uge i64 %v, 562949953421312
  br i1 %isnum, label %num, label %notnum
num:
  %bits = sub i64 %v, 562949953421312
  %d = bitcast i64 %bits to double
  %nz = fcmp one double %d, 0.0
  ret i1 %nz
notnum:
  %isimm = icmp ult i64 %v, 65536
  br i1 %isimm, label %imm, label %ptr
imm:
  %istrue = icmp eq i64 %v, 7
  ret i1 %istrue
ptr:
  %kind = and i64 %v, 7
  %isstr = icmp eq i64 %kind, 0
  br i1 %isstr, label %str, label %rettrue
str:
  %s = inttoptr i64 %v to ptr
  %len = call i64 @__kml_str_len(ptr %s)
  %ne = icmp ne i64 %len, 0
  ret i1 %ne
rettrue:
  ret i1 true
}`)
}

// emitAnyToNum coerces a boxed value to a double register.
func (e *Emitter) emitAnyToNum(v Value) string {
	e.ensureAnyOps()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call double @__kml_any_tonum(i64 %s)", r, v.Ref))
	return r
}

// emitAnyToF64Slot converts a boxed value to the double a number slot holds:
// ToNumber, except that undefined stays undefined, as the slot's sentinel
// (TDD-00241). Arithmetic on the box uses emitAnyToNum.
func (e *Emitter) emitAnyToF64Slot(v Value) string {
	num := e.emitAnyToNum(v)
	isU := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", isU, v.Ref, nbUndefined))
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, double bitcast (i64 %d to double), double %s", r, isU, undefF64, num))
	return r
}

// emitAnyTruthy coerces a boxed value to i1 via JS ToBoolean.
func (e *Emitter) emitAnyTruthy(v Value) Value {
	e.ensureAnyOps()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_any_tobool(i64 %s)", r, v.Ref))
	return Value{Ref: r, Ty: TypeBool}
}

// emitAnyBinary dispatches an arithmetic/relational operator with at least
// one dynamic operand (`-compat=js`). `+` with a runtime string side
// concatenates; everything else runs through ToNumber. Arithmetic returns a
// boxed number; relational returns a bool.
func (e *Emitter) emitAnyBinary(op string, left, right Value, pos ast.Pos) (Value, error) {
	lb, err := e.emitBoxValue(left)
	if err != nil {
		return Value{}, err
	}
	rb, err := e.emitBoxValue(right)
	if err != nil {
		return Value{}, err
	}

	// TDD-00201 Stage 4: an object operand is a NaN-boxed dynamic bag under
	// compat=js, so run runtime ToPrimitive (default hint — valueOf→toString)
	// before the concat/ToNumber logic below. A non-object box passes through
	// unchanged, and the `+` concat detection then sees the resulting primitive
	// (a string result concatenates, a number adds), matching JS.
	hint := hintNumber
	if op == "+" {
		hint = hintDefault
	}
	lb = Value{Ref: e.emitAnyToPrimitiveHint(lb.Ref, hint), Ty: TypeAny}
	rb = Value{Ref: e.emitAnyToPrimitiveHint(rb.Ref, hint), Ty: TypeAny}
	// An `any` against a bigint: a bigint operator when the `any` holds a
	// bigint too, a TypeError for any other non-string primitive.
	_, bigArith := bigIntBinFn[op]
	bigArith = bigArith && (left.Ty.IsBigInt || right.Ty.IsBigInt)
	bigOp := func() (string, error) {
		bl, br := e.emitUnboxBoxToType(lb.Ref, BigIntType()), e.emitUnboxBoxToType(rb.Ref, BigIntType())
		bl.Ty.Nullable, br.Ty.Nullable = true, true
		v, err := e.emitBigIntBinary(op, bl, br, pos)
		if err != nil {
			return "", err
		}
		return e.emitBoxBigInt(v).Ref, nil
	}
	if bigArith && op != "+" {
		r, err := bigOp()
		return Value{Ref: r, Ty: TypeAny}, err
	}

	if op == "+" {
		// Runtime string check: pointer range with string kind bits on either
		// side → ToString both and concatenate; otherwise numeric add.
		isStr := func(ref string) string {
			ge := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp uge i64 %s, 65536", ge, ref))
			lt := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp ult i64 %s, 562949953421312", lt, ref))
			k := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = and i64 %s, 7", k, ref))
			k0 := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", k0, k))
			a1 := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", a1, ge, lt))
			a2 := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", a2, a1, k0))
			return a2
		}
		ls, rs := isStr(lb.Ref), isStr(rb.Ref)
		either := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", either, ls, rs))
		concatL := e.freshLabel("anyadd.concat")
		numL := e.freshLabel("anyadd.num")
		mergeL := e.freshLabel("anyadd.merge")
		resPtr := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", resPtr))
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", either, concatL, numL))

		e.emitLabel(concatL)
		lstr, err := e.emitDynamicToString(lb)
		if err != nil {
			return Value{}, err
		}
		rstr, err := e.emitDynamicToString(rb)
		if err != nil {
			return Value{}, err
		}
		cat, err := e.emitStringConcat(lstr, rstr)
		if err != nil {
			return Value{}, err
		}
		catInt := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = ptrtoint ptr %s to i64", catInt, cat.Ref))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", catInt, resPtr))
		e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

		e.emitLabel(numL)
		var enc string
		if bigArith {
			if enc, err = bigOp(); err != nil {
				return Value{}, err
			}
		} else {
			ld, rd := e.emitAnyToNum(lb), e.emitAnyToNum(rb)
			sum := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = fadd double %s, %s", sum, ld, rd))
			enc = e.emitNbEncodeDouble(sum)
		}
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", enc, resPtr))
		e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

		e.emitLabel(mergeL)
		out := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", out, resPtr))
		return Value{Ref: out, Ty: TypeAny}, nil
	}

	switch op {
	case "-", "*", "/", "%", "**":
		ld, rd := e.emitAnyToNum(lb), e.emitAnyToNum(rb)
		r := e.freshReg()
		switch op {
		case "-":
			e.emitInstr(fmt.Sprintf("%s = fsub double %s, %s", r, ld, rd))
		case "*":
			e.emitInstr(fmt.Sprintf("%s = fmul double %s, %s", r, ld, rd))
		case "/":
			e.emitInstr(fmt.Sprintf("%s = fdiv double %s, %s", r, ld, rd))
		case "%":
			e.emitInstr(fmt.Sprintf("%s = frem double %s, %s", r, ld, rd))
		case "**":
			e.ensureMathFuncs()
			e.emitInstr(fmt.Sprintf("%s = call double @pow(double %s, double %s)", r, ld, rd))
		}
		return Value{Ref: e.emitNbEncodeDouble(r), Ty: TypeAny}, nil
	case "<", ">", "<=", ">=":
		// Two strings compare lexicographically; anything else numerically
		// (which is what JS does for string↔number too).
		e.ensureStrcmp()
		bothStrL := e.freshLabel("anycmp.str")
		numCmpL := e.freshLabel("anycmp.num")
		mergeL := e.freshLabel("anycmp.merge")
		resPtr := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i1, align 1", resPtr))
		bothStr := e.emitBothStringsCheck(lb.Ref, rb.Ref)
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", bothStr, bothStrL, numCmpL))

		e.emitLabel(bothStrL)
		lp, rp := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", lp, lb.Ref))
		e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", rp, rb.Ref))
		c := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @strcmp(ptr %s, ptr %s)", c, lp, rp))
		var scond string
		switch op {
		case "<":
			scond = "slt"
		case ">":
			scond = "sgt"
		case "<=":
			scond = "sle"
		case ">=":
			scond = "sge"
		}
		sr := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp %s i32 %s, 0", sr, scond, c))
		e.emitInstr(fmt.Sprintf("store i1 %s, ptr %s, align 1", sr, resPtr))
		e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

		e.emitLabel(numCmpL)
		// A bigint on either side compares by value (JS's IsLessThan).
		e.ensureBoxedBigIntHooks()
		e.usedAnyBigIntRel = true
		rel := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_any_bigint_rel(i64 %s, i64 %s)", rel, lb.Ref, rb.Ref))
		isBigRel := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne i32 %s, 0", isBigRel, rel))
		bigL, plainL := e.freshLabel("anycmp.big"), e.freshLabel("anycmp.plain")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isBigRel, bigL, plainL))
		e.emitLabel(bigL)
		var want string
		switch op {
		case "<":
			want = "1"
		case ">":
			want = "3"
		}
		br := e.freshReg()
		if want != "" {
			e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, %s", br, rel, want))
		} else {
			// <= is less or equal, >= greater or equal; unordered is false.
			a, b := "1", "2"
			if op == ">=" {
				a = "3"
			}
			x, y := e.freshReg(), e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, %s", x, rel, a))
			e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, %s", y, rel, b))
			e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", br, x, y))
		}
		e.emitInstr(fmt.Sprintf("store i1 %s, ptr %s, align 1", br, resPtr))
		e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
		e.emitLabel(plainL)
		ld, rd := e.emitAnyToNum(lb), e.emitAnyToNum(rb)
		var fcond string
		switch op {
		case "<":
			fcond = "olt"
		case ">":
			fcond = "ogt"
		case "<=":
			fcond = "ole"
		case ">=":
			fcond = "oge"
		}
		nr := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = fcmp %s double %s, %s", nr, fcond, ld, rd))
		e.emitInstr(fmt.Sprintf("store i1 %s, ptr %s, align 1", nr, resPtr))
		e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

		e.emitLabel(mergeL)
		out := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i1, ptr %s, align 1", out, resPtr))
		return Value{Ref: out, Ty: TypeBool}, nil
	case "&", "|", "^":
		// JS bitwise ops ToInt32 both operands, compute in the 32-bit domain,
		// and yield a Number (double) — the same path a typed `number & number`
		// takes, applied to the any operands' numeric coercion.
		ld, rd := e.emitAnyToNum(lb), e.emitAnyToNum(rb)
		l32 := e.toInt32(Value{Ref: ld, Ty: TypeF64})
		r32 := e.toInt32(Value{Ref: rd, Ty: TypeF64})
		iop := map[string]string{"&": "and", "|": "or", "^": "xor"}[op]
		bit := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = %s i32 %s, %s", bit, iop, l32, r32))
		num := e.int32ToNumber(bit)
		return Value{Ref: e.emitNbEncodeDouble(num.Ref), Ty: TypeAny}, nil
	case "<<", ">>", ">>>":
		// JS shift semantics (ToInt32 operand, 0-31 shift count, signed/unsigned
		// result) — reuse emitBitShift over the any operands' numeric coercion.
		ld, rd := e.emitAnyToNum(lb), e.emitAnyToNum(rb)
		shifted, err := e.emitBitShift(op, Value{Ref: ld, Ty: TypeF64}, Value{Ref: rd, Ty: TypeF64})
		if err != nil {
			return Value{}, err
		}
		return Value{Ref: e.emitNbEncodeDouble(shifted.Ref), Ty: TypeAny}, nil
	}
	return Value{}, fmt.Errorf("%d:%d: operator '%s' on any/unknown is not yet supported", pos.Line, pos.Col, op)
}

// emitBothStringsCheck answers whether two boxed words are both string-kind.
func (e *Emitter) emitBothStringsCheck(aRef, bRef string) string {
	one := func(ref string) string {
		ge := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp uge i64 %s, 65536", ge, ref))
		lt := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ult i64 %s, 562949953421312", lt, ref))
		k := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = and i64 %s, 7", k, ref))
		k0 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", k0, k))
		a := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", a, ge, lt))
		b := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", b, a, k0))
		return b
	}
	la, lb := one(aRef), one(bRef)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", r, la, lb))
	return r
}
