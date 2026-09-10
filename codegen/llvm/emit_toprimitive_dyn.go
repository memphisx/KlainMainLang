package llvm

// emit_toprimitive_dyn.go — runtime ToPrimitive over a NaN-boxed dynamic object
// (TDD-00201 Stage 4, `-compat=js`). Under compat=js a plain object literal is a
// D1 dynamic bag (tag 10), so the static ladder in emit_toprimitive.go cannot see
// its methods — they are looked up and invoked at runtime here instead. This is
// what makes the faithful lane faithful: `{valueOf(){return 5}} * 2` is 10 (not
// NaN) and `String({toString(){return "hi"}})` is "hi" (not "[object Object]").
//
// @__kml_toprimitive(box, strHint) returns the RAW primitive box a method
// produced: Symbol.toPrimitive (@@toPrimitive) first, then valueOf/toString in
// hint order (valueOf→toString for number/default, toString→valueOf for string);
// the first callable whose result is a primitive (nb_tag < 6) wins, else the
// literal "[object Object]". A non-object box passes through unchanged, so the
// helper is safe to apply to every dynamic operand.

import "fmt"

func (e *Emitter) ensureAnyToPrimitive() {
	if e.usedAnyToPrimitive {
		return
	}
	e.usedAnyToPrimitive = true
	e.ensureNanBox()
	e.ensureDynObj()
	keyVO := e.internString("valueOf")
	keyTS := e.internString("toString")
	keyTP := e.internString("@@toPrimitive")
	strNum := e.internString("number")
	strStr := e.internString("string")
	objObj := e.internString("[object Object]")

	// Two tag-12 invoke shims (0-arg valueOf/toString; 1-arg @@toPrimitive(hint)).
	e.emitGlobal(`
define i64 @__kml_tp_invoke0(i64 %fnbox, i64 %recv) {
entry:
  %argv = alloca i64, align 8
  %pay = call i64 @__kml_nb_pay(i64 %fnbox)
  %rec = inttoptr i64 %pay to ptr
  %fp = load ptr, ptr %rec, align 8
  %es = getelementptr i8, ptr %rec, i64 8
  %env = load ptr, ptr %es, align 8
  %r = call i64 %fp(ptr %env, i64 %recv, i64 0, ptr %argv)
  ret i64 %r
}
define i64 @__kml_tp_invoke1(i64 %fnbox, i64 %recv, i64 %arg) {
entry:
  %argv = alloca [1 x i64], align 8
  %a0 = getelementptr [1 x i64], ptr %argv, i64 0, i64 0
  store i64 %arg, ptr %a0, align 8
  %pay = call i64 @__kml_nb_pay(i64 %fnbox)
  %rec = inttoptr i64 %pay to ptr
  %fp = load ptr, ptr %rec, align 8
  %es = getelementptr i8, ptr %rec, i64 8
  %env = load ptr, ptr %es, align 8
  %r = call i64 %fp(ptr %env, i64 %recv, i64 1, ptr %argv)
  ret i64 %r
}`)

	// The ladder. Keys/strings are interned constants spliced in as ptr operands.
	e.emitGlobal(fmt.Sprintf(`
define i64 @__kml_toprimitive(i64 %%v, i1 %%strHint) {
entry:
  %%tag = call i8 @__kml_nb_tag(i64 %%v)
  %%isobj = icmp eq i8 %%tag, 10
  br i1 %%isobj, label %%obj, label %%pass
pass:
  ret i64 %%v
obj:
  %%bag64 = call i64 @__kml_nb_pay(i64 %%v)
  %%bag = inttoptr i64 %%bag64 to ptr
  %%tp = call i64 @__kml_dynobj_get(ptr %%bag, ptr %s)
  %%tptag = call i8 @__kml_nb_tag(i64 %%tp)
  %%tpfn = icmp eq i8 %%tptag, 12
  br i1 %%tpfn, label %%ctp, label %%pick
ctp:
  %%hn = ptrtoint ptr %s to i64
  %%hs = ptrtoint ptr %s to i64
  %%hb = select i1 %%strHint, i64 %%hs, i64 %%hn
  %%rtp = call i64 @__kml_tp_invoke1(i64 %%tp, i64 %%v, i64 %%hb)
  ret i64 %%rtp
pick:
  br i1 %%strHint, label %%s1, label %%n1
n1:
  %%vf = call i64 @__kml_dynobj_get(ptr %%bag, ptr %s)
  %%vft = call i8 @__kml_nb_tag(i64 %%vf)
  %%vffn = icmp eq i8 %%vft, 12
  br i1 %%vffn, label %%cvf, label %%n2
cvf:
  %%rvf = call i64 @__kml_tp_invoke0(i64 %%vf, i64 %%v)
  %%rvft = call i8 @__kml_nb_tag(i64 %%rvf)
  %%rvfp = icmp ult i8 %%rvft, 6
  br i1 %%rvfp, label %%retvf, label %%n2
retvf:
  ret i64 %%rvf
n2:
  %%ts = call i64 @__kml_dynobj_get(ptr %%bag, ptr %s)
  %%tst = call i8 @__kml_nb_tag(i64 %%ts)
  %%tsfn = icmp eq i8 %%tst, 12
  br i1 %%tsfn, label %%cts, label %%deflt
cts:
  %%rts = call i64 @__kml_tp_invoke0(i64 %%ts, i64 %%v)
  %%rtst = call i8 @__kml_nb_tag(i64 %%rts)
  %%rtsp = icmp ult i8 %%rtst, 6
  br i1 %%rtsp, label %%retts, label %%deflt
retts:
  ret i64 %%rts
s1:
  %%ts2 = call i64 @__kml_dynobj_get(ptr %%bag, ptr %s)
  %%ts2t = call i8 @__kml_nb_tag(i64 %%ts2)
  %%ts2fn = icmp eq i8 %%ts2t, 12
  br i1 %%ts2fn, label %%cts2, label %%s2
cts2:
  %%rts2 = call i64 @__kml_tp_invoke0(i64 %%ts2, i64 %%v)
  %%rts2t = call i8 @__kml_nb_tag(i64 %%rts2)
  %%rts2p = icmp ult i8 %%rts2t, 6
  br i1 %%rts2p, label %%retts2, label %%s2
retts2:
  ret i64 %%rts2
s2:
  %%vf2 = call i64 @__kml_dynobj_get(ptr %%bag, ptr %s)
  %%vf2t = call i8 @__kml_nb_tag(i64 %%vf2)
  %%vf2fn = icmp eq i8 %%vf2t, 12
  br i1 %%vf2fn, label %%cvf2, label %%deflt
cvf2:
  %%rvf2 = call i64 @__kml_tp_invoke0(i64 %%vf2, i64 %%v)
  %%rvf2t = call i8 @__kml_nb_tag(i64 %%rvf2)
  %%rvf2p = icmp ult i8 %%rvf2t, 6
  br i1 %%rvf2p, label %%retvf2, label %%deflt
retvf2:
  ret i64 %%rvf2
deflt:
  %%oo = ptrtoint ptr %s to i64
  ret i64 %%oo
}`, keyTP, strNum, strStr, keyVO, keyTS, keyTS, keyVO, objObj))
}

// ensureAnyLooseEq emits @__kml_any_loose_eq — JS's Abstract Equality (`==`)
// over two NaN-boxed values (TDD-00201 Stage 4). Distinct from @__kml_any_eq
// (Strict Equality, `===`): loose `==` coerces. Algorithm: both heap objects →
// reference compare (`{}=={}` is false); exactly one object → ToPrimitive it;
// null/undefined loosely equal only each other; same primitive type → strict
// compare; mixed primitives (number/string/boolean) → ToNumber both and compare
// (a NaN result makes the comparison false, as in JS).
func (e *Emitter) ensureAnyLooseEq() {
	if e.usedAnyLooseEq {
		return
	}
	e.usedAnyLooseEq = true
	e.ensureNanBox()
	e.ensureAnyEq()
	e.ensureAnyOps() // __kml_any_tonum
	e.ensureAnyToPrimitive()
	e.emitGlobal(`
define i1 @__kml_any_loose_eq(i64 %a0, i64 %b0) {
entry:
  %ta0 = call i8 @__kml_nb_tag(i64 %a0)
  %tb0 = call i8 @__kml_nb_tag(i64 %b0)
  %aobj = icmp uge i8 %ta0, 6
  %bobj = icmp uge i8 %tb0, 6
  %both = and i1 %aobj, %bobj
  br i1 %both, label %refeq, label %coerce
refeq:
  %re = call i1 @__kml_any_eq(i64 %a0, i64 %b0)
  ret i1 %re
coerce:
  br i1 %aobj, label %ca, label %aok
ca:
  %apc = call i64 @__kml_toprimitive(i64 %a0, i1 0)
  br label %aok
aok:
  %a = phi i64 [ %a0, %coerce ], [ %apc, %ca ]
  br i1 %bobj, label %cb, label %bok
cb:
  %bpc = call i64 @__kml_toprimitive(i64 %b0, i1 0)
  br label %bok
bok:
  %b = phi i64 [ %b0, %aok ], [ %bpc, %cb ]
  %ta = call i8 @__kml_nb_tag(i64 %a)
  %tb = call i8 @__kml_nb_tag(i64 %b)
  %an4 = icmp eq i8 %ta, 4
  %an5 = icmp eq i8 %ta, 5
  %anull = or i1 %an4, %an5
  %bn4 = icmp eq i8 %tb, 4
  %bn5 = icmp eq i8 %tb, 5
  %bnull = or i1 %bn4, %bn5
  %eithernull = or i1 %anull, %bnull
  br i1 %eithernull, label %nullcase, label %notnull
nullcase:
  %bothnull = and i1 %anull, %bnull
  ret i1 %bothnull
notnull:
  %sametype = icmp eq i8 %ta, %tb
  br i1 %sametype, label %strict, label %mixed
strict:
  %se = call i1 @__kml_any_eq(i64 %a, i64 %b)
  ret i1 %se
mixed:
  %na = call double @__kml_any_tonum(i64 %a)
  %nb = call double @__kml_any_tonum(i64 %b)
  %me = fcmp oeq double %na, %nb
  ret i1 %me
}`)
}

// emitAnyLooseEquals implements `==`/`!=` when either operand is dynamic
// (`-compat=js`): the JS Abstract Equality Comparison, with object coercion.
func (e *Emitter) emitAnyLooseEquals(a, b Value, negate bool) (Value, error) {
	boxedA, err := e.emitBoxValue(a)
	if err != nil {
		return Value{}, err
	}
	boxedB, err := e.emitBoxValue(b)
	if err != nil {
		return Value{}, err
	}
	e.ensureAnyLooseEq()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_any_loose_eq(i64 %s, i64 %s)", r, boxedA.Ref, boxedB.Ref))
	if negate {
		neg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = xor i1 %s, true", neg, r))
		return Value{Ref: neg, Ty: TypeBool}, nil
	}
	return Value{Ref: r, Ty: TypeBool}, nil
}

// emitAnyToPrimitive runs the runtime ToPrimitive ladder on a boxed value,
// returning a boxed primitive (a non-object box passes through). strHint selects
// the string-hint method order.
func (e *Emitter) emitAnyToPrimitive(boxRef string, strHint bool) string {
	e.ensureAnyToPrimitive()
	hint := "0"
	if strHint {
		hint = "1"
	}
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_toprimitive(i64 %s, i1 %s)", r, boxRef, hint))
	return r
}
