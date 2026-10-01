// emit_anyprom.go — a Promise held in a dynamic value (TDD-00230 phase 5).
// A task promise stores its value in its value type's representation, so the
// box is a headered wrapper (promiseBoxType) naming the promise and a routine
// that reads its value as a box. The wrapper is cached in the promise's
// trailing slot, so one promise boxes to one value (`p === p` holds through
// `any`). `await`, `.then`/`.catch`/`.finally` and promise resolution through
// `any` work on the promise itself — no intermediate promise, so reactions run
// in the same microtask turns as in JS.
package llvm

import (
	"fmt"
)

// promiseBoxedSlot is the promise struct's trailing field: its box wrapper,
// or null until the promise is first boxed.
const promiseBoxedSlot = 5

// promiseBoxType is the wrapper a boxed promise points at: the promise, the
// routine that boxes its value (`i64 (ptr promise)`), and whether the value
// is already a box (a Promise<any>).
func promiseBoxType() Type {
	return ObjectType([]Field{
		{Name: "__kml_promise", Ty: TypePtr},
		{Name: "__kml_boxfn", Ty: TypePtr},
		{Name: "__kml_anyelem", Ty: TypeBool},
	})
}

// promiseBoxHeader is the wrapper layout's header word.
func (e *Emitter) promiseBoxHeader() int64 {
	return e.objHeaderWord(promiseBoxType())
}

// emitBoxPromise boxes a task promise (see the file comment).
func (e *Emitter) emitBoxPromise(v Value) Value {
	e.ensurePromiseRuntime()
	e.ensureNanBox() // the inspect hook boxes a rejection's reason
	e.usedPromiseBox = true
	boxTy := promiseBoxType()
	e.noteBoxedLayout(boxTy)
	// An absent promise (a null slot) is undefined.
	res := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", res))
	e.emitInstr(fmt.Sprintf("store i64 %d, ptr %s, align 8", nbUndefined, res))
	isNull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, v.Ref))
	liveL, endL := e.freshLabel("pbox.live"), e.freshLabel("pbox.end")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNull, endL, liveL))
	e.emitLabel(liveL)
	slot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", slot, promiseStructIR, v.Ref, promiseBoxedSlot))
	cached := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", cached, slot))
	have := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", have, cached))
	wslot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", wslot))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", cached, wslot))
	mkL := e.freshLabel("pbox.make")
	doneL := e.freshLabel("pbox.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", have, doneL, mkL))
	e.emitLabel(mkL)
	elem := TypeVoid
	if v.Ty.PromiseType != nil {
		elem = *v.Ty.PromiseType
	}
	w := e.freshReg()
	e.emitObjMallocInto(w, boxTy)
	store := func(idx int, ir, val string) {
		p := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", p, boxTy.StructIR(), w, idx))
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align 8", ir, val, p))
	}
	store(1, "ptr", v.Ref)
	store(2, "ptr", e.promiseValueBoxer(elem))
	anyElem := "false"
	if isUnconstrainedDynamic(elem) {
		anyElem = "true"
	}
	store(3, "i1", anyElem)
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", w, slot))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", w, wslot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	out := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", out, wslot))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", e.emitNbTagPtr(out, kmlTagObject), res))
	e.emitTerminator(fmt.Sprintf("br label %%%s", endL))
	e.emitLabel(endL)
	boxed := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", boxed, res))
	return Value{Ref: boxed, Ty: TypeAny}
}

// promiseValueBoxer is `i64 (ptr promise)`: a fulfilled Promise<elem>'s value,
// boxed. One per element representation.
func (e *Emitter) promiseValueBoxer(elem Type) string {
	key := layoutFieldKey(elem)
	if e.promiseBoxRunners == nil {
		e.promiseBoxRunners = map[string]string{}
	}
	if name, ok := e.promiseBoxRunners[key]; ok {
		return name
	}
	name := fmt.Sprintf("@__kml_pval_box_%d", len(e.promiseBoxRunners))
	e.promiseBoxRunners[key] = name
	restore := e.beginDetachedFunc()
	boxed := fmt.Sprintf("%d", nbUndefined)
	if elem.IR != "" && elem.IR != "void" && !elem.IsNever {
		if bv, err := e.emitBoxValue(e.loadPromiseValue("%p", elem)); err == nil {
			boxed = bv.Ref
		}
	}
	e.emitTerminator(fmt.Sprintf("ret i64 %s", boxed))
	body := e.allocas.String() + e.body.String()
	restore()
	e.functions.WriteString(fmt.Sprintf("\ndefine internal i64 %s(ptr %%p) {\nentry:\n%s}\n", name, body))
	return name
}

// emitPromiseWrapperOrNull is the wrapper behind a boxed promise, or null
// when the dynamic value is not one.
func (e *Emitter) emitPromiseWrapperOrNull(v Value) string {
	e.ensurePromiseWrapperOf()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_promise_wrapper_of(i64 %s)", r, v.Ref))
	return r
}

// emitWrapperField loads field idx (1 promise, 2 boxer, 3 any-element flag)
// of a promise wrapper.
func (e *Emitter) emitWrapperField(w string, idx int, ir string) string {
	p := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", p, promiseBoxType().StructIR(), w, idx))
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align 8", r, ir, p))
	return r
}

// ensurePromiseWrapperOf emits @__kml_promise_wrapper_of(i64 v) -> ptr.
func (e *Emitter) ensurePromiseWrapperOf() {
	if e.usedPromiseWrapperOf {
		return
	}
	e.usedPromiseWrapperOf = true
	e.ensureNanBox()
	e.noteBoxedLayout(promiseBoxType())
	e.emitGlobal(fmt.Sprintf(`
define ptr @__kml_promise_wrapper_of(i64 %%v) {
entry:
  %%tag = call i8 @__kml_nb_tag(i64 %%v)
  %%isobj = icmp eq i8 %%tag, %d
  br i1 %%isobj, label %%obj, label %%no
obj:
  %%pay = call i64 @__kml_nb_pay(i64 %%v)
  %%o = inttoptr i64 %%pay to ptr
  %%nn = icmp ne ptr %%o, null
  br i1 %%nn, label %%hdr, label %%no
hdr:
  %%h = load i64, ptr %%o, align 8
  %%isp = icmp eq i64 %%h, %d
  br i1 %%isp, label %%yes, label %%no
yes:
  ret ptr %%o
no:
  ret ptr null
}`, kmlTagObject, e.promiseBoxHeader()))
}

// emitAwaitAny awaits a dynamic value: the promise it holds (its value then
// boxed), or — any other value — the value itself, after the microtask tick
// every await takes.
func (e *Emitter) emitAwaitAny(v Value) (Value, error) {
	e.ensurePromiseRuntime()
	e.ensureMicrotasks()
	w := e.emitPromiseWrapperOrNull(v)
	isNull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, w))
	res := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", res))
	plainL := e.freshLabel("awaitany.plain")
	promL := e.freshLabel("awaitany.prom")
	doneL := e.freshLabel("awaitany.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNull, plainL, promL))

	e.emitLabel(promL)
	src := e.emitWrapperField(w, 1, "ptr")
	if _, err := e.emitAwaitTaskPromise(src, TypeVoid); err != nil {
		return Value{}, err
	}
	w2 := e.emitPromiseWrapperOrNull(v)
	src2 := e.emitWrapperField(w2, 1, "ptr")
	boxer := e.emitWrapperField(w2, 2, "ptr")
	val := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 %s(ptr %s)", val, boxer, src2))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", val, res))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))

	e.emitLabel(plainL)
	prom := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_task_alloc_promise()", prom))
	e.storePromiseValue(prom, v)
	rp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 0", rp, promiseStructIR, prom))
	e.emitInstr(fmt.Sprintf("store i64 1, ptr %s, align 8", rp))
	pv, err := e.emitAwaitTaskPromise(prom, TypeAny)
	if err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", pv.Ref, res))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))

	e.emitLabel(doneL)
	out := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", out, res))
	return Value{Ref: out, Ty: TypeAny}, nil
}

// Kinds of the dynamic reaction runner.
const (
	pdynThen    = 0
	pdynFinally = 1
)

// ensurePromiseDynRuntime emits the dynamic promise reactions:
//
//	@__kml_pdyn_run(ptr env)  — the reaction: env = { ptr src, i64 onF,
//	    i64 onR, i64 onFin, ptr q, ptr boxer, i64 kind }. Settles the
//	    Promise<any> q from src the way .then(onF, onR) / .finally(onFin)
//	    does, calling each callback (a boxed function, or undefined for a
//	    pass-through) through the dynamic ABI.
//	@__kml_pdyn_attach(ptr src, i64 onF, i64 onR, i64 onFin, ptr q,
//	    ptr boxer, i64 kind) — registers that reaction on src.
//	@__kml_promise_resolve_any(ptr q, i64 v) — resolve Promise<any> q with v,
//	    adopting a promise v holds (NewPromiseResolveThenableJob: a job that
//	    registers the reaction), else fulfilling q with v.
func (e *Emitter) ensurePromiseDynRuntime() {
	if e.usedPromiseResolveAny {
		return
	}
	e.usedPromiseResolveAny = true
	e.ensurePromiseRuntime()
	e.ensureMicrotasks()
	e.ensurePromiseSettle()
	e.ensureExceptionHelpers()
	e.ensureNanBox()
	e.ensureMalloc()
	e.ensurePromiseWrapperOf()
	envIR := "{ ptr, i64, i64, i64, ptr, ptr, i64 }"
	p := promiseStructIR

	// The runner, emitted through the ordinary builders so the dynamic calls
	// and value conversions are the ones typed code uses.
	restore := e.beginDetachedFunc()
	ld := func(idx int, ir string) string {
		g := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %%env, i32 0, i32 %d", g, envIR, idx))
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align 8", r, ir, g))
		return r
	}
	src, onF, onR, onFin, q, boxer, kind := ld(0, "ptr"), ld(1, "i64"), ld(2, "i64"), ld(3, "i64"), ld(4, "ptr"), ld(5, "ptr"), ld(6, "i64")
	isFn := func(box string) string {
		tag, _ := e.emitUnboxTagPayload(Value{Ref: box, Ty: TypeAny})
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", r, tag, kmlTagDynFunc))
		return r
	}
	copyReject := func() {
		for _, i := range []int{2, 3} {
			a, b, c := e.freshReg(), e.freshReg(), e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", a, p, src, i))
			e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", b, a))
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", c, p, q, i))
			e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", b, c))
		}
		e.emitInstr(fmt.Sprintf("call void @__kml_promise_settle(ptr %s, i64 2)", q))
	}
	// callGuarded calls fn(args…) through the dynamic ABI; a throw rejects q.
	// It leaves the current block at the call's normal return with the
	// result register.
	callGuarded := func(fn string, args []string) string {
		argv := e.freshReg()
		n := len(args)
		if n == 0 {
			e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", argv))
		} else {
			e.emitAlloca(fmt.Sprintf("%s = alloca [%d x i64], align 8", argv, n))
		}
		for i, a := range args {
			s := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %s, i64 %d", s, argv, i))
			e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", a, s))
		}
		jb := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_push_jmpbuf()", jb))
		sj := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = %s", sj, e.setjmpCall(jb)))
		threw := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne i32 %s, 0", threw, sj))
		catchL, tryL := e.freshLabel("pdyn.catch"), e.freshLabel("pdyn.try")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", threw, catchL, tryL))
		e.emitLabel(catchL)
		tp := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i8 @__kml_get_thrown_tag()", tp))
		pp := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_get_thrown_pay()", pp))
		e.storeRejectReason(q, tp, pp)
		e.emitInstr(fmt.Sprintf("call void @__kml_promise_settle(ptr %s, i64 2)", q))
		e.emitTerminator("ret void")
		e.emitLabel(tryL)
		r := e.emitDynFnBoxCallUnchecked(fn, fmt.Sprintf("%d", nbUndefined), argv, n)
		e.emitInstr("call void @__kml_pop_jmpbuf()")
		return r
	}
	stp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 0", stp, p, src))
	st := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", st, stp))
	ok := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 1", ok, st))
	isFin := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", isFin, kind, pdynFinally))
	finL, thenL := e.freshLabel("pdyn.fin"), e.freshLabel("pdyn.then")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isFin, finL, thenL))

	// finally: run onFin (a throw rejects q), then pass the settlement on.
	e.emitLabel(finL)
	hasFin := isFn(onFin)
	runFinL, passL := e.freshLabel("pdyn.runfin"), e.freshLabel("pdyn.pass")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", hasFin, runFinL, passL))
	e.emitLabel(runFinL)
	callGuarded(onFin, nil)
	e.emitTerminator(fmt.Sprintf("br label %%%s", passL))
	e.emitLabel(passL)
	passOkL, passRejL := e.freshLabel("pdyn.passok"), e.freshLabel("pdyn.passrej")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", ok, passOkL, passRejL))
	e.emitLabel(passOkL)
	pv := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 %s(ptr %s)", pv, boxer, src))
	e.emitInstr(fmt.Sprintf("call void @__kml_promise_resolve_any(ptr %s, i64 %s)", q, pv))
	e.emitTerminator("ret void")
	e.emitLabel(passRejL)
	copyReject()
	e.emitTerminator("ret void")

	// then: the fulfilled or rejected branch.
	e.emitLabel(thenL)
	fulL, rejL := e.freshLabel("pdyn.ful"), e.freshLabel("pdyn.rej")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", ok, fulL, rejL))
	e.emitLabel(fulL)
	val := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 %s(ptr %s)", val, boxer, src))
	hasF := isFn(onF)
	callFL, passFL := e.freshLabel("pdyn.callf"), e.freshLabel("pdyn.passf")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", hasF, callFL, passFL))
	e.emitLabel(callFL)
	rf := callGuarded(onF, []string{val})
	e.emitInstr(fmt.Sprintf("call void @__kml_promise_resolve_any(ptr %s, i64 %s)", q, rf))
	e.emitTerminator("ret void")
	e.emitLabel(passFL)
	e.emitInstr(fmt.Sprintf("call void @__kml_promise_resolve_any(ptr %s, i64 %s)", q, val))
	e.emitTerminator("ret void")
	e.emitLabel(rejL)
	hasR := isFn(onR)
	callRL, passRL := e.freshLabel("pdyn.callr"), e.freshLabel("pdyn.passr")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", hasR, callRL, passRL))
	e.emitLabel(callRL)
	reason := e.emitCaughtToAny(e.loadRejectReasonCaught(src))
	rr := callGuarded(onR, []string{reason.Ref})
	e.emitInstr(fmt.Sprintf("call void @__kml_promise_resolve_any(ptr %s, i64 %s)", q, rr))
	e.emitTerminator("ret void")
	e.emitLabel(passRL)
	copyReject()
	e.emitTerminator("ret void")
	body := e.allocas.String() + e.body.String()
	restore()
	e.functions.WriteString(fmt.Sprintf("\ndefine internal void @__kml_pdyn_run(ptr %%env) {\nentry:\n%s}\n", body))

	e.emitGlobal(fmt.Sprintf(`
define void @__kml_pdyn_attach(ptr %%src, i64 %%onF, i64 %%onR, i64 %%onFin, ptr %%q, ptr %%boxer, i64 %%kind) {
entry:
  call void @__kml_promise_mark_handled(ptr %%src)
  %%env = call ptr @malloc(i64 56)
  %%e0 = getelementptr %[1]s, ptr %%env, i32 0, i32 0
  store ptr %%src, ptr %%e0, align 8
  %%e1 = getelementptr %[1]s, ptr %%env, i32 0, i32 1
  store i64 %%onF, ptr %%e1, align 8
  %%e2 = getelementptr %[1]s, ptr %%env, i32 0, i32 2
  store i64 %%onR, ptr %%e2, align 8
  %%e3 = getelementptr %[1]s, ptr %%env, i32 0, i32 3
  store i64 %%onFin, ptr %%e3, align 8
  %%e4 = getelementptr %[1]s, ptr %%env, i32 0, i32 4
  store ptr %%q, ptr %%e4, align 8
  %%e5 = getelementptr %[1]s, ptr %%env, i32 0, i32 5
  store ptr %%boxer, ptr %%e5, align 8
  %%e6 = getelementptr %[1]s, ptr %%env, i32 0, i32 6
  store i64 %%kind, ptr %%e6, align 8
  %%clo = call ptr @malloc(i64 16)
  store ptr @__kml_pdyn_run, ptr %%clo, align 8
  %%cep = getelementptr { ptr, ptr }, ptr %%clo, i32 0, i32 1
  store ptr %%env, ptr %%cep, align 8
  %%res_p = getelementptr %[2]s, ptr %%src, i32 0, i32 0
  %%res = load i64, ptr %%res_p, align 8
  %%settled = icmp ne i64 %%res, 0
  br i1 %%settled, label %%now, label %%later
now:
  call void @__kml_microtask_enqueue(ptr %%clo)
  ret void
later:
  %%node = call ptr @malloc(i64 16)
  %%rx_p = getelementptr %[2]s, ptr %%src, i32 0, i32 4
  %%old = load ptr, ptr %%rx_p, align 8
  store ptr %%clo, ptr %%node, align 8
  %%nn = getelementptr { ptr, ptr }, ptr %%node, i32 0, i32 1
  store ptr %%old, ptr %%nn, align 8
  store ptr %%node, ptr %%rx_p, align 8
  ret void
}

; NewPromiseResolveThenableJob: register the pass-through reaction later.
define void @__kml_pdyn_adopt_job(ptr %%env) {
entry:
  %%a0 = getelementptr { ptr, ptr, ptr }, ptr %%env, i32 0, i32 0
  %%q = load ptr, ptr %%a0, align 8
  %%a1 = getelementptr { ptr, ptr, ptr }, ptr %%env, i32 0, i32 1
  %%src = load ptr, ptr %%a1, align 8
  %%a2 = getelementptr { ptr, ptr, ptr }, ptr %%env, i32 0, i32 2
  %%boxer = load ptr, ptr %%a2, align 8
  call void @__kml_pdyn_attach(ptr %%src, i64 %[4]d, i64 %[4]d, i64 %[4]d, ptr %%q, ptr %%boxer, i64 %[5]d)
  ret void
}

define void @__kml_promise_resolve_any(ptr %%q, i64 %%v) {
entry:
  %%w = call ptr @__kml_promise_wrapper_of(i64 %%v)
  %%isp = icmp ne ptr %%w, null
  br i1 %%isp, label %%adopt, label %%plain
adopt:
  %%sp = getelementptr %[3]s, ptr %%w, i32 0, i32 1
  %%src = load ptr, ptr %%sp, align 8
  %%bp = getelementptr %[3]s, ptr %%w, i32 0, i32 2
  %%boxer = load ptr, ptr %%bp, align 8
  %%env = call ptr @malloc(i64 24)
  store ptr %%q, ptr %%env, align 8
  %%s1 = getelementptr { ptr, ptr, ptr }, ptr %%env, i32 0, i32 1
  store ptr %%src, ptr %%s1, align 8
  %%s2 = getelementptr { ptr, ptr, ptr }, ptr %%env, i32 0, i32 2
  store ptr %%boxer, ptr %%s2, align 8
  %%clo = call ptr @malloc(i64 16)
  store ptr @__kml_pdyn_adopt_job, ptr %%clo, align 8
  %%cep = getelementptr { ptr, ptr }, ptr %%clo, i32 0, i32 1
  store ptr %%env, ptr %%cep, align 8
  call void @__kml_microtask_enqueue(ptr %%clo)
  ret void
plain:
  %%v0 = getelementptr %[2]s, ptr %%q, i32 0, i32 2
  store i64 %%v, ptr %%v0, align 8
  call void @__kml_promise_settle(ptr %%q, i64 1)
  ret void
}

; The Promise<any> view of a boxed typed promise (its wrapper w): JS has one
; object, so a fulfilled one is viewed fulfilled at once; a pending or
; rejected one settles the view from its own reaction.
define ptr @__kml_promise_view_any(ptr %%w) {
entry:
  %%sp = getelementptr %[3]s, ptr %%w, i32 0, i32 1
  %%src = load ptr, ptr %%sp, align 8
  %%bp = getelementptr %[3]s, ptr %%w, i32 0, i32 2
  %%boxer = load ptr, ptr %%bp, align 8
  %%q = call ptr @__kml_task_alloc_promise()
  %%stp = getelementptr %[2]s, ptr %%src, i32 0, i32 0
  %%st = load i64, ptr %%stp, align 8
  %%ful = icmp eq i64 %%st, 1
  br i1 %%ful, label %%now, label %%later
now:
  %%v = call i64 %%boxer(ptr %%src)
  %%v0 = getelementptr %[2]s, ptr %%q, i32 0, i32 2
  store i64 %%v, ptr %%v0, align 8
  call void @__kml_promise_settle(ptr %%q, i64 1)
  ret ptr %%q
later:
  call void @__kml_pdyn_attach(ptr %%src, i64 %[4]d, i64 %[4]d, i64 %[4]d, ptr %%q, ptr %%boxer, i64 %[5]d)
  ret ptr %%q
}`, envIR, p, promiseBoxType().StructIR(), nbUndefined, pdynThen))
}

// ensurePromiseResolveAny is the resolve half of the dynamic promise runtime.
func (e *Emitter) ensurePromiseResolveAny() { e.ensurePromiseDynRuntime() }

// emitAnyToPromiseAny is `Promise.resolve(v)` for a dynamic v: the promise v
// holds when it is a Promise<any> (PromiseResolve returns its argument),
// else a Promise<any> resolved with v (adopting any other promise).
func (e *Emitter) emitAnyToPromiseAny(v Value) Value {
	e.ensurePromiseDynRuntime()
	w := e.emitPromiseWrapperOrNull(v)
	res := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", res))
	isW := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", isW, w))
	chkL, newL, doneL := e.freshLabel("presolve.chk"), e.freshLabel("presolve.new"), e.freshLabel("presolve.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isW, chkL, newL))
	e.emitLabel(chkL)
	anyElem := e.emitWrapperField(w, 3, "i1")
	sameL, viewL := e.freshLabel("presolve.same"), e.freshLabel("presolve.view")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", anyElem, sameL, viewL))
	// A typed promise: its Promise<any> view (PromiseResolve returns the
	// promise itself, so no adoption ticks).
	e.emitLabel(viewL)
	vq := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_promise_view_any(ptr %s)", vq, w))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", vq, res))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(sameL)
	src := e.emitWrapperField(w, 1, "ptr")
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", src, res))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(newL)
	q := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_task_alloc_promise()", q))
	e.emitInstr(fmt.Sprintf("call void @__kml_promise_resolve_any(ptr %s, i64 %s)", q, v.Ref))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", q, res))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	out := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", out, res))
	qt := PromiseOf(TypeAny)
	qt.PromiseTask = true
	return Value{Ref: out, Ty: qt}
}

// emitPromiseBoxMembers is the layout-table getter body for the promise
// wrapper: `then`, `catch` and `finally` as dynamic functions.
func (e *Emitter) emitPromiseBoxMembers() {
	for _, m := range []string{"then", "catch", "finally"} {
		miss := e.emitKeyIs("%key", m)
		rec := e.promiseMethodRecord(m)
		e.emitInstr(fmt.Sprintf("store i32 %d, ptr %%found, align 4", shapeMember))
		e.emitTerminator(fmt.Sprintf("ret i64 %s", e.emitNbTagPtr(rec, kmlTagDynFunc)))
		e.emitLabel(miss)
	}
}

// promiseMethodRecord is the dynamic-function record for Promise.prototype.m
// on a boxed promise: it registers the dynamic reaction on the promise and
// returns the derived Promise<any>, boxed.
func (e *Emitter) promiseMethodRecord(m string) string {
	key := "Promise." + m
	if e.shapeMethodRecs == nil {
		e.shapeMethodRecs = map[string]string{}
	}
	if rec, ok := e.shapeMethodRecs[key]; ok {
		return rec
	}
	e.ensurePromiseDynRuntime()
	fn := "@__kml_promise_dyn_" + m
	rec := fn + "_rec"
	e.shapeMethodRecs[key] = rec
	arity := 2
	if m != "then" {
		arity = 1
	}
	e.registerFnMeta(fn, m, arity, fnKindPlain)
	e.emitGlobal(fmt.Sprintf("%s = internal constant { ptr, ptr, i64 } { ptr %s, ptr null, i64 %d }", rec, fn, arity))
	restore := e.beginDetachedFunc()
	arg := func(i int) string {
		have := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp sgt i64 %%argc, %d", have, i))
		sp := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %%argv, i64 %d", sp, i))
		w := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", w, sp))
		word := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %d", word, have, w, nbUndefined))
		return word
	}
	undef := fmt.Sprintf("%d", nbUndefined)
	onF, onR, onFin, kind := undef, undef, undef, pdynThen
	switch m {
	case "then":
		onF, onR = arg(0), arg(1)
	case "catch":
		onR = arg(0)
	case "finally":
		onFin, kind = arg(0), pdynFinally
	}
	w := e.emitPromiseWrapperOrNull(Value{Ref: "%this", Ty: TypeAny})
	src := e.emitWrapperField(w, 1, "ptr")
	boxer := e.emitWrapperField(w, 2, "ptr")
	q := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_task_alloc_promise()", q))
	e.emitInstr(fmt.Sprintf("call void @__kml_pdyn_attach(ptr %s, i64 %s, i64 %s, i64 %s, ptr %s, ptr %s, i64 %d)", src, onF, onR, onFin, q, boxer, kind))
	qt := PromiseOf(TypeAny)
	qt.PromiseTask = true
	e.emitTerminator(fmt.Sprintf("ret i64 %s", e.emitBoxPromise(Value{Ref: q, Ty: qt}).Ref))
	body := e.allocas.String() + e.body.String()
	restore()
	e.functions.WriteString(fmt.Sprintf("\ndefine internal i64 %s(ptr %%env, i64 %%this, i64 %%argc, ptr %%argv) {\nentry:\n%s}\n", fn, body))
	return rec
}

// emitConvertPromise is convert(Promise<A> → Promise<B>) where exactly one of
// A and B is `any` (an erased generic's Promise<T> meeting its caller's
// Promise<number>): a promise that settles when v does, its value converted
// between the box and B's representation; a rejection passes through.
func (e *Emitter) emitConvertPromise(v Value, target Type) Value {
	e.ensurePromiseRuntime()
	e.ensureMicrotasks()
	e.ensurePromiseSettle()
	from := TypeVoid
	if v.Ty.PromiseType != nil {
		from = *v.Ty.PromiseType
	}
	to := TypeVoid
	if target.PromiseType != nil {
		to = *target.PromiseType
	}
	q := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_task_alloc_promise()", q))
	env := e.freshReg()
	e.ensureMalloc()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", env))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", v.Ref, env))
	qp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 8", qp, env))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", q, qp))
	runner := e.promiseConvertRunner(from, to)
	// A fulfilled source converts at once (one object in JS: its value is
	// there to read now); otherwise when it settles.
	sp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 0", sp, promiseStructIR, v.Ref))
	st := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", st, sp))
	ful := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 1", ful, st))
	nowL, laterL, doneL := e.freshLabel("pconv.now"), e.freshLabel("pconv.later"), e.freshLabel("pconv.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", ful, nowL, laterL))
	e.emitLabel(nowL)
	e.emitInstr(fmt.Sprintf("call void %s(ptr %s)", runner, env))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(laterL)
	clo := e.buildBuiltinClosure(runner, env)
	e.emitAttachPromiseReaction(v.Ref, clo)
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	tt := target
	tt.PromiseTask = true
	return Value{Ref: q, Ty: tt}
}

// promiseConvertRunner is emitConvertPromise's reaction for one (from, to).
func (e *Emitter) promiseConvertRunner(from, to Type) string {
	key := layoutFieldKey(from) + "->" + layoutFieldKey(to)
	if e.promiseConvRunners == nil {
		e.promiseConvRunners = map[string]string{}
	}
	if name, ok := e.promiseConvRunners[key]; ok {
		return name
	}
	name := fmt.Sprintf("@__kml_pconv_run_%d", len(e.promiseConvRunners))
	e.promiseConvRunners[key] = name
	restore := e.beginDetachedFunc()
	src := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %%env, align 8", src))
	qp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %%env, i64 8", qp))
	q := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", q, qp))
	sp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 0", sp, promiseStructIR, src))
	st := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", st, sp))
	ok := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 1", ok, st))
	okL, rejL := e.freshLabel("pconv.ok"), e.freshLabel("pconv.rej")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", ok, okL, rejL))
	e.emitLabel(okL)
	if from.IR != "" && from.IR != "void" && !from.IsNever && to.IR != "" && to.IR != "void" {
		val := e.loadPromiseValue(src, from)
		var conv Value
		if to.IsDynamic {
			if bv, err := e.emitBoxValue(val); err == nil {
				conv = bv
			} else {
				conv = Value{Ref: fmt.Sprintf("%d", nbUndefined), Ty: TypeAny}
			}
		} else if to.IsArray {
			conv = e.emitUnboxBoxToType(val.Ref, to)
		} else {
			conv = e.coerce(val, to)
		}
		e.storePromiseValue(q, conv)
	}
	e.emitInstr(fmt.Sprintf("call void @__kml_promise_settle(ptr %s, i64 1)", q))
	e.emitTerminator("ret void")
	e.emitLabel(rejL)
	for _, i := range []int{2, 3} {
		a, b, c := e.freshReg(), e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", a, promiseStructIR, src, i))
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", b, a))
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", c, promiseStructIR, q, i))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", b, c))
	}
	e.emitInstr(fmt.Sprintf("call void @__kml_promise_settle(ptr %s, i64 2)", q))
	e.emitTerminator("ret void")
	body := e.allocas.String() + e.body.String()
	restore()
	e.functions.WriteString(fmt.Sprintf("\ndefine internal void %s(ptr %%env) {\nentry:\n%s}\n", name, body))
	return name
}

// promiseElemsNeedConvert reports whether a Promise<a> value read as a
// Promise<b> needs emitConvertPromise: exactly one element is `any`.
func promiseElemsNeedConvert(a, b Type) bool {
	if !a.IsPromise || !b.IsPromise || a.PromiseType == nil || b.PromiseType == nil {
		return false
	}
	ad, bd := isUnconstrainedDynamic(*a.PromiseType), isUnconstrainedDynamic(*b.PromiseType)
	if ad == bd {
		return false
	}
	other := *b.PromiseType
	if bd {
		other = *a.PromiseType
	}
	return other.IR != "" && other.IR != "void" && !other.IsNever
}
