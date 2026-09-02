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
//   - a heap reference (object/array/function) in arithmetic → NaN (the
//     full ToPrimitive ladder is a later stage, disclosed).
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
	e.ensureStrtod()
	e.ensureStrHeaderRuntime()
	e.emitGlobal(`
; JS ToNumber over a NaN-boxed word. Numbers decode; true/false -> 1/0;
; null -> 0; undefined -> NaN; a string parses fully or yields NaN (empty/
; whitespace-only -> 0); heap references -> NaN (no ToPrimitive ladder yet).
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
  br i1 %isstr, label %str, label %retnan
str:
  %s = inttoptr i64 %v to ptr
  %len = call i64 @__kml_str_len(ptr %s)
  %empty = icmp eq i64 %len, 0
  br i1 %empty, label %zero, label %parse
parse:
  %endp = alloca ptr, align 8
  %pv = call double @strtod(ptr %s, ptr %endp)
  %end = load ptr, ptr %endp, align 8
  %si = ptrtoint ptr %s to i64
  %ei = ptrtoint ptr %end to i64
  %consumed = sub i64 %ei, %si
  %full = icmp eq i64 %consumed, %len
  br i1 %full, label %ok, label %retnan
ok:
  ret double %pv
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
		ld, rd := e.emitAnyToNum(lb), e.emitAnyToNum(rb)
		sum := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = fadd double %s, %s", sum, ld, rd))
		enc := e.emitNbEncodeDouble(sum)
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
