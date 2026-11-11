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
	_ "embed"
	"fmt"

	"KlainMainLang/ast"
)

// Resolving a promise with a dynamic value and the Promise<any> view live in
// promisesrc/anyprom.c (TDD-00240); the reaction runner and attach stay
// generated (the runner is built from the program's dynamic calls).
//
//go:embed promisesrc/promisebox.c
var promiseBoxSource string

// PromiseBoxSource is promisebox.c, behind kml_layout.h.
func PromiseBoxSource() string { return layoutHeader() + promiseBoxSource }

//go:embed promisesrc/anyprom.c
var anyPromSource string

// AnyPromSource is anyprom.c, behind kml_layout.h.
func AnyPromSource() string { return layoutHeader() + anyPromSource }

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
	e.ensureExceptionHelpers()
	e.ensureMicrotasks()
	e.ensureUnhandledRejections()
	e.ensureNanBox() // the inspect hook boxes a rejection's reason
	e.usedPromiseBox = true
	boxTy := promiseBoxType()
	e.noteBoxedLayout(boxTy)
	// An absent promise (a null slot) is undefined.
	res := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", res))
	e.emitInstr(fmt.Sprintf("store i64 %d, "+irPtr+" %s, align 8", nbUndefined, res))
	isNull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq "+irPtr+" %s, null", isNull, v.Ref))
	liveL, endL := e.freshLabel("pbox.live"), e.freshLabel("pbox.end")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNull, endL, liveL))
	e.emitLabel(liveL)
	slot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 %d", slot, promiseStructIR, v.Ref, promiseBoxedSlot))
	cached := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", cached, slot))
	have := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne "+irPtr+" %s, null", have, cached))
	wslot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca "+irPtr+", align 8", wslot))
	e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", cached, wslot))
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
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 %d", p, boxTy.StructIR(), w, idx))
		e.heapStore(ir, val, p, 8)
	}
	store(1, irPtr, v.Ref)
	store(2, irPtr, e.promiseValueBoxer(elem))
	anyElem := "false"
	if isUnconstrainedDynamic(elem) {
		anyElem = "true"
	}
	store(3, "i1", anyElem)
	e.heapStore(irPtr, w, slot, 8)
	e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", w, wslot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	out := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", out, wslot))
	e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", e.emitNbTagPtr(out, kmlTagObject), res))
	e.emitTerminator(fmt.Sprintf("br label %%%s", endL))
	e.emitLabel(endL)
	boxed := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", boxed, res))
	return Value{Ref: boxed, Ty: TypeAny}
}

// promiseValueBoxer is `i64 (ptr promise)`: a fulfilled Promise<elem>'s value,
// boxed. One per element representation.
func (e *Emitter) promiseValueBoxer(elem Type) string {
	// The boxed value is read dynamically: its layout needs its row, in
	// every program using the boxer (a cached one emits no box here).
	e.noteBoxedLayout(elem)
	key := layoutFieldKey(elem)
	if e.promiseBoxRunners == nil {
		e.promiseBoxRunners = map[string]string{}
	}
	if name, ok := e.promiseBoxRunners[key]; ok {
		return name
	}
	restore := e.beginDetachedFunc()
	restore = e.sharedBody(restore) // content-named: the same in every program
	boxed := fmt.Sprintf("%d", nbUndefined)
	if elem.IR != "" && elem.IR != "void" && !elem.IsNever() {
		if bv, err := e.emitBoxValue(e.loadPromiseValue("%p", elem)); err == nil {
			boxed = bv.Ref
		}
	}
	e.emitTerminator(fmt.Sprintf("ret i64 %s", boxed))
	body := e.allocas.String() + e.body.String()
	restore()
	name := e.defineContentNamed("@__kml_pval_box.", "i64", irPtr+" %p", body)
	e.promiseBoxRunners[key] = name
	return name
}

// emitPromiseWrapperOrNull is the wrapper behind a boxed promise, or null
// when the dynamic value is not one.
func (e *Emitter) emitPromiseWrapperOrNull(v Value) string {
	e.ensurePromiseWrapperOf()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_promise_wrapper_of(i64 %s)", r, v.Ref))
	return r
}

// emitWrapperField loads field idx (1 promise, 2 boxer, 3 any-element flag)
// of a promise wrapper.
func (e *Emitter) emitWrapperField(w string, idx int, ir string) string {
	p := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 %d", p, promiseBoxType().StructIR(), w, idx))
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, "+irPtr+" %s, align 8", r, ir, p))
	return r
}

// ensurePromiseWrapperOf links @__kml_promise_wrapper_of(i64 v) -> ptr
// (promisebox.c, emitPromiseInspectHook).
func (e *Emitter) ensurePromiseWrapperOf() {
	e.usedPromiseWrapperOf = true
	e.ensureNanBox()
}

// emitAwaitAny awaits a dynamic value: the promise it holds (its value then
// boxed), or — any other value — the value itself, after the microtask tick
// every await takes.
func (e *Emitter) emitAwaitAny(v Value) (Value, error) {
	e.ensureExceptionHelpers()
	e.ensureMicrotasks()
	e.ensureUnhandledRejections()
	e.ensureMicrotasks()
	w := e.emitPromiseWrapperOrNull(v)
	isNull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq "+irPtr+" %s, null", isNull, w))
	res := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", res))
	plainL := e.freshLabel("awaitany.plain")
	promL := e.freshLabel("awaitany.prom")
	doneL := e.freshLabel("awaitany.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNull, plainL, promL))

	e.emitLabel(promL)
	src := e.emitWrapperField(w, 1, irPtr)
	if _, err := e.emitAwaitTaskPromise(src, TypeVoid); err != nil {
		return Value{}, err
	}
	w2 := e.emitPromiseWrapperOrNull(v)
	src2 := e.emitWrapperField(w2, 1, irPtr)
	boxer := e.emitWrapperField(w2, 2, irPtr)
	val := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 %s("+irPtr+" %s)", val, boxer, src2))
	e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", val, res))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))

	e.emitLabel(plainL)
	// Any other value is PromiseResolve's: a thenable (an object with a
	// callable `then`, an instance of a class extending Promise) adopted
	// through it, anything else fulfilled.
	e.ensurePromiseDynRuntime()
	prom := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_task_alloc_promise()", prom))
	e.emitInstr(fmt.Sprintf("call void @__kml_promise_resolve_any("+irPtr+" %s, i64 %s)", prom, v.Ref))
	pv, err := e.emitAwaitTaskPromise(prom, TypeAny)
	if err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", pv.Ref, res))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))

	e.emitLabel(doneL)
	out := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", out, res))
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
//	    registers the reaction) or another thenable (a job that calls its
//	    `then`), else fulfilling q with v.
func (e *Emitter) ensurePromiseDynRuntime() {
	if e.usedPromiseResolveAny {
		return
	}
	e.usedPromiseResolveAny = true
	e.ensureDynJSONC() // a thenable's `then` is read through the object hooks
	e.ensureExceptionHelpers()
	e.ensureMicrotasks()
	e.ensureUnhandledRejections()
	e.ensureMicrotasks()
	e.ensureNanBox()
	e.ensurePromiseWrapperOf()
	envIR := "{ " + irPtr + ", i64, i64, i64, " + irPtr + ", " + irPtr + ", i64 }"
	p := promiseStructIR

	// The runner, emitted through the ordinary builders so the dynamic calls
	// and value conversions are the ones typed code uses.
	restore := e.beginDetachedFunc()
	ld := func(idx int, ir string) string {
		g := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %%env, i32 0, i32 %d", g, envIR, idx))
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load %s, "+irPtr+" %s, align 8", r, ir, g))
		return r
	}
	src, onF, onR, onFin, q, boxer, kind := ld(0, irPtr), ld(1, "i64"), ld(2, "i64"), ld(3, "i64"), ld(4, irPtr), ld(5, irPtr), ld(6, "i64")
	isFn := func(box string) string {
		tag, _ := e.emitUnboxTagPayload(Value{Ref: box, Ty: TypeAny})
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", r, tag, kmlTagDynFunc))
		return r
	}
	copyReject := func() {
		e.emitInstr(fmt.Sprintf("call void @__kml_promise_reject_from("+irPtr+" %s, "+irPtr+" %s)", q, src))
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
			e.emitInstr(fmt.Sprintf("%s = getelementptr i64, "+irPtr+" %s, i64 %d", s, argv, i))
			e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", a, s))
		}
		catchL, tryL := e.freshLabel("pdyn.catch"), e.freshLabel("pdyn.try")
		e.excEnter(catchL, tryL)
		e.emitLabel(catchL)
		tp := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i8 @__kml_get_thrown_tag()", tp))
		pp := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_get_thrown_pay()", pp))
		e.emitRejectPromise(q, tp, true, pp)
		e.emitTerminator("ret void")
		e.emitLabel(tryL)
		r := e.emitDynFnBoxCallUnchecked(fn, fmt.Sprintf("%d", nbUndefined), argv, n)
		e.excLeave()
		return r
	}
	stp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 0", stp, p, src))
	st := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", st, stp))
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
	e.emitInstr(fmt.Sprintf("%s = call i64 %s("+irPtr+" %s)", pv, boxer, src))
	e.emitInstr(fmt.Sprintf("call void @__kml_promise_resolve_any("+irPtr+" %s, i64 %s)", q, pv))
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
	e.emitInstr(fmt.Sprintf("%s = call i64 %s("+irPtr+" %s)", val, boxer, src))
	hasF := isFn(onF)
	callFL, passFL := e.freshLabel("pdyn.callf"), e.freshLabel("pdyn.passf")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", hasF, callFL, passFL))
	e.emitLabel(callFL)
	rf := callGuarded(onF, []string{val})
	e.emitInstr(fmt.Sprintf("call void @__kml_promise_resolve_any("+irPtr+" %s, i64 %s)", q, rf))
	e.emitTerminator("ret void")
	e.emitLabel(passFL)
	e.emitInstr(fmt.Sprintf("call void @__kml_promise_resolve_any("+irPtr+" %s, i64 %s)", q, val))
	e.emitTerminator("ret void")
	e.emitLabel(rejL)
	hasR := isFn(onR)
	callRL, passRL := e.freshLabel("pdyn.callr"), e.freshLabel("pdyn.passr")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", hasR, callRL, passRL))
	e.emitLabel(callRL)
	reason := e.emitCaughtToAny(e.loadRejectReasonCaught(src))
	rr := callGuarded(onR, []string{reason.Ref})
	e.emitInstr(fmt.Sprintf("call void @__kml_promise_resolve_any("+irPtr+" %s, i64 %s)", q, rr))
	e.emitTerminator("ret void")
	e.emitLabel(passRL)
	copyReject()
	e.emitTerminator("ret void")
	body := e.allocas.String() + e.body.String()
	restore()
	e.writeFunc("internal void @__kml_pdyn_run("+irPtr+" %env)", body)

	// __kml_pdyn_attach(src, onF, onR, onFin, q, boxer, kind): src marked
	// handled, a reaction running __kml_pdyn_run over the env queued now
	// when src is settled, else pushed onto its reaction list.
	restore = e.beginDetachedFunc()
	e.emitInstr("call void @__kml_promise_mark_handled(" + irPtr + " %src)")
	env := e.freshReg()
	e.heapMallocInto(env, allocClosure, "56")
	for k, a := range []string{irPtr + " %src", "i64 %onF", "i64 %onR", "i64 %onFin", irPtr + " %q", irPtr + " %boxer", "i64 %kind"} {
		f := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 %d", f, envIR, env, k))
		e.heapStoreTV(a, f, 8)
	}
	clo, cep := e.freshReg(), e.freshReg()
	e.emitClosureAlloc(clo, "16", "@__kml_pdyn_run")
	e.heapStore(irPtr, "@__kml_pdyn_run", clo, 8)
	e.emitInstr(fmt.Sprintf("%s = getelementptr { "+irPtr+", "+irPtr+" }, "+irPtr+" %s, i32 0, i32 1", cep, clo))
	e.heapStore(irPtr, env, cep, 8)
	resP, res, settled := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %%src, i32 0, i32 0", resP, p))
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", res, resP))
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", settled, res))
	nowL, laterL := e.freshLabel("pdyn.now"), e.freshLabel("pdyn.later")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", settled, nowL, laterL))
	e.emitLabel(nowL)
	e.emitInstr(fmt.Sprintf("call void @__kml_microtask_enqueue("+irPtr+" %s)", clo))
	e.emitTerminator("ret void")
	e.emitLabel(laterL)
	node, rxP, old, nn := e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg()
	e.heapMallocInto(node, allocHost, "16")
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %%src, i32 0, i32 4", rxP, p))
	e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", old, rxP))
	e.heapStore(irPtr, clo, node, 8)
	e.emitInstr(fmt.Sprintf("%s = getelementptr { "+irPtr+", "+irPtr+" }, "+irPtr+" %s, i32 0, i32 1", nn, node))
	e.heapStore(irPtr, old, nn, 8)
	e.heapStore(irPtr, node, rxP, 8)
	e.emitTerminator("ret void")
	body = e.allocas.String() + e.body.String()
	restore()
	e.writeFunc("void @__kml_pdyn_attach("+irPtr+" %src, i64 %onF, i64 %onR, i64 %onFin, "+irPtr+" %q, "+irPtr+" %boxer, i64 %kind)", body)
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
	e.emitAlloca(fmt.Sprintf("%s = alloca "+irPtr+", align 8", res))
	isW := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne "+irPtr+" %s, null", isW, w))
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
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_promise_view_any("+irPtr+" %s)", vq, w))
	e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", vq, res))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(sameL)
	src := e.emitWrapperField(w, 1, irPtr)
	e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", src, res))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(newL)
	q := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_task_alloc_promise()", q))
	e.emitInstr(fmt.Sprintf("call void @__kml_promise_resolve_any("+irPtr+" %s, i64 %s)", q, v.Ref))
	e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", q, res))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	out := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", out, res))
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
		e.heapStore("i32", fmt.Sprint(shapeMember), "%found", 4)
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
	e.emitStaticFnRecord(rec, "internal", "{ "+irPtr+", "+irPtr+", i64 }", fmt.Sprintf("{ "+irPtr+" %s, "+irPtr+" null, i64 %d }", fn, arity), fn)
	restore := e.beginDetachedFunc()
	if v, err := intrinsics["Promise.prototype."+m].dyn(e, "%this", TypeAny); err != nil {
		e.emitThrowTypeError("the promise method '" + m + "' can't be called through a dynamic value")
	} else if !e.blockDone {
		e.emitTerminator(fmt.Sprintf("ret i64 %s", v.Ref))
	}
	body := e.allocas.String() + e.body.String()
	restore()
	e.writeFunc(fmt.Sprintf("internal i64 %s("+irPtr+" %%env, i64 %%this, i64 %%argc, "+irPtr+" %%argv)", fn), body)
	return rec
}

// promiseDynThen, promiseDynCatch and promiseDynFinally are the promise
// methods as dynamic functions on a receiver held boxed (self): the
// reactions each registers.
func (e *Emitter) promiseDynThen(self string, _ Type) (Value, error) {
	undef := fmt.Sprint(nbUndefined)
	return e.emitPromiseDynReaction(self, e.dynThunkArg(0).Ref, e.dynThunkArg(1).Ref, undef, pdynThen, false)
}

func (e *Emitter) promiseDynCatch(self string, _ Type) (Value, error) {
	undef := fmt.Sprint(nbUndefined)
	return e.emitPromiseDynReaction(self, undef, e.dynThunkArg(0).Ref, undef, pdynThen, true)
}

func (e *Emitter) promiseDynFinally(self string, _ Type) (Value, error) {
	undef := fmt.Sprint(nbUndefined)
	return e.emitPromiseDynReaction(self, undef, undef, e.dynThunkArg(0).Ref, pdynFinally, true)
}

// emitPromiseDynReaction registers reactions on the promise self holds and
// is the derived Promise<any>, boxed. On another receiver, a generic method
// (catch, finally) is an Invoke of the receiver's own `then`, with
// finally's callback on both paths; then throws Node's TypeError.
func (e *Emitter) emitPromiseDynReaction(self, onF, onR, onFin string, kind int, generic bool) (Value, error) {
	w := e.emitPromiseWrapperOrNull(Value{Ref: self, Ty: TypeAny})
	isNull, genL, promL := e.freshReg(), e.freshLabel("pdyn.generic"), e.freshLabel("pdyn.promise")
	e.emitInstr(fmt.Sprintf("%s = icmp eq "+irPtr+" %s, null", isNull, w))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNull, genL, promL))
	e.emitLabel(genL)
	if !generic {
		e.emitThrowTypeErrorValue(e.emitReceiverMessage("Method Promise.prototype.then called on incompatible receiver ", self))
	} else {
		thenFn, err := e.emitDynAnyMemberGetNamed(Value{Ref: self, Ty: TypeAny}, e.internString("then"), "then", ast.Pos{})
		if err == nil {
			a, b := onF, onR
			if kind == pdynFinally {
				a, b = onFin, onFin
			}
			e.ensureShapeRuntime()
			e.declareFn("__kml_invoke_then", "declare i64 @__kml_invoke_then(i64, i64, i64, i64)")
			r := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_invoke_then(i64 %s, i64 %s, i64 %s, i64 %s)", r, self, thenFn.Ref, a, b))
			e.emitTerminator(fmt.Sprintf("ret i64 %s", r))
		} else if !e.blockDone {
			e.emitTerminator(fmt.Sprintf("ret i64 %d", nbUndefined))
		}
	}
	e.emitLabel(promL)
	src := e.emitWrapperField(w, 1, irPtr)
	boxer := e.emitWrapperField(w, 2, irPtr)
	q := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_task_alloc_promise()", q))
	e.emitInstr(fmt.Sprintf("call void @__kml_pdyn_attach("+irPtr+" %s, i64 %s, i64 %s, i64 %s, "+irPtr+" %s, "+irPtr+" %s, i64 %d)", src, onF, onR, onFin, q, boxer, kind))
	qt := PromiseOf(TypeAny)
	qt.PromiseTask = true
	return e.emitBoxPromise(Value{Ref: q, Ty: qt}), nil
}

// emitConvertPromise is convert(Promise<A> → Promise<B>) where exactly one of
// A and B is `any` (an erased generic's Promise<T> meeting its caller's
// Promise<number>): a promise that settles when v does, its value converted
// between the box and B's representation; a rejection passes through.
func (e *Emitter) emitConvertPromise(v Value, target Type) Value {
	e.ensureExceptionHelpers()
	e.ensureMicrotasks()
	e.ensureUnhandledRejections()
	e.ensureMicrotasks()
	e.ensureNanBox()
	from := TypeVoid
	if v.Ty.PromiseType != nil {
		from = *v.Ty.PromiseType
	}
	to := TypeVoid
	if target.PromiseType != nil {
		to = *target.PromiseType
	}
	q := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_task_alloc_promise()", q))
	env := e.freshReg()
	e.heapMallocInto(env, allocClosure, "16")
	e.heapStore(irPtr, v.Ref, env, 8)
	qp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, "+irPtr+" %s, i64 8", qp, env))
	e.heapStore(irPtr, q, qp, 8)
	runner := e.promiseConvertRunner(from, to)
	// One object with the source: a handler on either handles both.
	e.declareFn("__kml_promise_twin", "declare void @__kml_promise_twin("+irPtr+", "+irPtr+")")
	e.emitInstr(fmt.Sprintf("call void @__kml_promise_twin("+irPtr+" %s, "+irPtr+" %s)", v.Ref, q))
	// A settled source converts at once (one object in JS: its value or
	// reason is there now; a reaction attached to it would never run);
	// otherwise when it settles.
	sp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 0", sp, promiseStructIR, v.Ref))
	st := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", st, sp))
	ful := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", ful, st))
	nowL, laterL, doneL := e.freshLabel("pconv.now"), e.freshLabel("pconv.later"), e.freshLabel("pconv.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", ful, nowL, laterL))
	e.emitLabel(nowL)
	e.emitInstr(fmt.Sprintf("call void %s("+irPtr+" %s)", runner, env))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(laterL)
	// JS has one object: the converted view settles with the source, inside
	// its settlement, not a job later.
	clo := e.buildBuiltinClosure(runner, env)
	e.declareFn("__kml_promise_attach_sync", "declare void @__kml_promise_attach_sync("+irPtr+", "+irPtr+")")
	e.emitInstr(fmt.Sprintf("call void @__kml_promise_attach_sync("+irPtr+" %s, "+irPtr+" %s)", v.Ref, clo))
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
	restore := e.beginDetachedFunc()
	restore = e.sharedBody(restore) // content-named: the same in every program
	src := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %%env, align 8", src))
	qp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, "+irPtr+" %%env, i64 8", qp))
	q := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", q, qp))
	sp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 0", sp, promiseStructIR, src))
	st := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", st, sp))
	ok := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 1", ok, st))
	okL, rejL := e.freshLabel("pconv.ok"), e.freshLabel("pconv.rej")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", ok, okL, rejL))
	e.emitLabel(okL)
	if from.IR != "" && from.IR != "void" && !from.IsNever() && to.IR != "" && to.IR != "void" {
		val := e.loadPromiseValue(src, from)
		var conv Value
		if to.IsDynamic() {
			if bv, err := e.emitBoxValue(val); err == nil {
				conv = bv
			} else {
				conv = Value{Ref: fmt.Sprintf("%d", nbUndefined), Ty: TypeAny}
			}
		} else if to.IsArray() && from.IsDynamic() {
			conv = e.emitUnboxBoxToType(val.Ref, to)
		} else {
			conv = e.convert(val, to)
		}
		e.storePromiseValue(q, conv)
	}
	e.emitInstr(fmt.Sprintf("call void @__kml_promise_settle("+irPtr+" %s, i64 1)", q))
	e.emitTerminator("ret void")
	e.emitLabel(rejL)
	// The converted view and its source are one promise in JS: the
	// rejection is the view's to report (__kml_promise_twin silenced the
	// source).
	e.emitInstr(fmt.Sprintf("call void @__kml_promise_reject_from("+irPtr+" %s, "+irPtr+" %s)", q, src))
	e.emitTerminator("ret void")
	body := e.allocas.String() + e.body.String()
	restore()
	name := e.defineContentNamed("@__kml_pconv_run.", "void", irPtr+" %env", body)
	e.promiseConvRunners[key] = name
	return name
}

// promiseElemsNeedConvert reports whether a Promise<a> value read as a
// Promise<b> needs emitConvertPromise: exactly one element is `any`.
func promiseElemsNeedConvert(a, b Type) bool {
	if !a.IsPromise() || !b.IsPromise() || a.PromiseType == nil || b.PromiseType == nil {
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
	// A union held boxed (`IteratorResult<T>`) settles with the same word
	// an `any` one does: nothing to convert.
	return other.IR != "" && other.IR != "void" && !other.IsNever() && !other.IsDynamic()
}
