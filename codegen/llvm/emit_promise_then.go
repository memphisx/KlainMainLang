// emit_promise_then.go — Promise.prototype.then / .catch / .finally over a task
// promise (TDD-00083 Stage 3, value-chaining added in a follow-on). A reaction is
// a 0-arg closure {runner, env} where the runner is a per-call-site,
// type-specialized function that reads the settled source promise's value/reason,
// invokes the callback, and settles the *returned* promise Q with the callback's
// result — so `p.then(f).then(g)` chains. The runner runs as a microtask, so
// ordering matches the spec (after the current synchronous run, before timers).
//
// Chaining model:
//   - `.then(onF, onR?)`: Q resolves to onF(value) on fulfillment; on rejection Q
//     resolves to onR(reason) if onR is present (recovery), else Q rejects with the
//     same reason (propagation). Q's value type is the callback's return type.
//   - `.catch(onR)`: same as `.then(undefined, onR)`.
//   - `.finally(onFin)`: onFin runs for its side effect; Q passes the source's
//     settlement (value or rejection) straight through unchanged.
package llvm

import (
	"KlainMainLang/ast"
	"fmt"
)

// emitRejectCallback emits a `.catch`/onRejected callback, hinting an
// arrow-function parameter to the shared error object shape so `e.message`/
// `e.name` (and `AggregateError`'s `.errors`) work inside it without the caller
// having to annotate the parameter (an `Error`-family annotation resolves to the
// same shape either way). A non-arrow callback (a named function reference) is
// emitted unchanged.
func (e *Emitter) emitRejectCallback(arg ast.Expression) (Value, error) {
	if af, ok := arg.(*ast.ArrowFunction); ok {
		return e.emitArrowFunctionWithHints(af, []Type{errorObjType})
	}
	return e.emitExpr(arg)
}

// emitFulfillCallback emits a `.then` onFulfilled callback, hinting an
// arrow-function parameter with no annotation to the source promise's value type
// (e.g. a fetch chain's `Response`), so `.then(r => r.status)` works without the
// caller annotating `r`. An annotated param, or a non-arrow callback, is emitted
// unchanged.
func (e *Emitter) emitFulfillCallback(arg ast.Expression, valueTy Type) (Value, error) {
	if af, ok := arg.(*ast.ArrowFunction); ok && valueTy.IR != "void" && valueTy.IR != "" {
		return e.emitArrowFunctionWithHints(af, []Type{valueTy})
	}
	return e.emitExpr(arg)
}

// isAbsentCallback reports whether a `.then` argument is a literal `undefined`
// or `null` — JS treats either as "no callback" (pass-through), not a callable.
func isAbsentCallback(arg ast.Expression) bool {
	_, ok := arg.(*ast.NullLiteral)
	return ok
}

// emitPromiseThen handles a `.then`/`.catch`/`.finally` call on a Promise value.
func (e *Emitter) emitPromiseThen(objExpr ast.Expression, kind string, args []ast.Expression, pos ast.Pos) (Value, error) {
	pVal, err := e.emitExpr(objExpr)
	if err != nil {
		return Value{}, err
	}
	if !pVal.Ty.IsPromise {
		return Value{}, fmt.Errorf("%d:%d: .%s is only supported on a Promise", pos.Line, pos.Col, kind)
	}
	// A raw fetch()'s Promise<Response> is a still-pending fetch handle, not a
	// task-shaped promise. Bridge it to a *pending* task promise whose settle
	// (drive the fetch, build the Response) is deferred to a queued microtask —
	// so the synchronous script continues immediately and the transport wait
	// happens when the event loop drains, matching JS ordering (ADR-00258's
	// synchronous drive replaced). The !PromiseTask guard is essential: a
	// chained `.finally`/`.then` that itself settles to a Response (e.g.
	// `fetch(u).finally(f).then(g)`) returns a *task* promise that also looks
	// IsResponse — it must not be re-driven as a fetch handle.
	if !pVal.Ty.PromiseTask && pVal.Ty.PromiseType != nil && pVal.Ty.PromiseType.IsResponse && !pVal.Ty.PromiseResolved {
		pVal = e.emitFetchHandleToPendingPromise(pVal.Ref)
	}
	if !pVal.Ty.PromiseTask {
		return Value{}, fmt.Errorf("%d:%d: .%s is currently supported only on a promise from a may-suspend async function or a fetch (TDD-00083 Stage 3)", pos.Line, pos.Col, kind)
	}
	innerTy := TypeVoid
	if pVal.Ty.PromiseType != nil {
		innerTy = *pVal.Ty.PromiseType
	}
	// Only the promise + microtask machinery is needed here; the fiber scheduler
	// (if the program has may-suspend fns) is pulled in by those fns themselves.
	e.ensurePromiseRuntime()
	e.ensureMicrotasks()

	// Evaluate callbacks into closure pointers ("null" when absent) and decide the
	// returned promise Q's value type U (the type Q settles to).
	onF, onR, onFin := "null", "null", "null"
	retTy := TypeVoid
	switch kind {
	case "then":
		// JS treats a missing/`undefined`/`null` onFulfilled as a pass-through
		// (`p.then().then(g)` hands g the source value; `p.then(undefined, onR)`
		// is the .catch shape) — the runner already has the pass-through block,
		// so only the arity/argument handling lives here.
		if len(args) >= 1 && !isAbsentCallback(args[0]) {
			v, err := e.emitFulfillCallback(args[0], innerTy)
			if err != nil {
				return Value{}, err
			}
			onF = v.Ref
			if t, ok := e.callbackReturnType(args[0]); ok {
				retTy = t
			}
		} else {
			retTy = innerTy // pass-through keeps the source value type
		}
		if len(args) >= 2 && !isAbsentCallback(args[1]) {
			v2, err := e.emitRejectCallback(args[1])
			if err != nil {
				return Value{}, err
			}
			onR = v2.Ref
		}
	case "catch":
		if len(args) < 1 {
			return Value{}, fmt.Errorf("%d:%d: catch expects 1 argument", pos.Line, pos.Col)
		}
		v, err := e.emitRejectCallback(args[0])
		if err != nil {
			return Value{}, err
		}
		onR = v.Ref
		if t, ok := e.callbackReturnType(args[0]); ok {
			retTy = t
		}
	case "finally":
		if len(args) < 1 {
			return Value{}, fmt.Errorf("%d:%d: finally expects 1 argument", pos.Line, pos.Col)
		}
		v, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		onFin = v.Ref
		retTy = innerTy // finally passes the source value through unchanged
	}

	// Q: the returned promise, allocated *pending* so a chained `.then` attaches a
	// reaction that the runner fires when it settles Q.
	q := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_task_alloc_promise()", q))

	// env = { ptr p, ptr onF, ptr onR, ptr onFin, ptr q }
	e.thenCtr++
	runner := fmt.Sprintf("@__kml_then_run_%d", e.thenCtr)
	env := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 40)", env))
	storeEnv := func(idx int, ref string) {
		gp := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr, ptr, ptr, ptr }, ptr %s, i32 0, i32 %d", gp, env, idx))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", ref, gp))
	}
	storeEnv(0, pVal.Ref)
	storeEnv(1, onF)
	storeEnv(2, onR)
	storeEnv(3, onFin)
	storeEnv(4, q)

	e.emitThenRunner(runner, innerTy, retTy)

	// closure { runner, env }
	clo := e.freshReg()
	cfp := e.freshReg()
	cep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", clo))
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 0", cfp, clo))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", runner, cfp))
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 1", cep, clo))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", env, cep))

	// Register: if the source is already settled, enqueue now; else attach a
	// reaction node onto the source's reaction list.
	res := e.freshReg()
	resP := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 0", resP, promiseStructIR, pVal.Ref))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", res, resP))
	settled := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", settled, res))
	nowL := e.freshLabel("then.now")
	laterL := e.freshLabel("then.later")
	doneL := e.freshLabel("then.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", settled, nowL, laterL))
	e.emitLabel(nowL)
	e.emitInstr(fmt.Sprintf("call void @__kml_microtask_enqueue(ptr %s)", clo))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(laterL)
	// node = { closure, next = p.reactions }; p.reactions = node
	node := e.freshReg()
	rxP := e.freshReg()
	oldHead := e.freshReg()
	nodeClo := e.freshReg()
	nodeNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", node))
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 4", rxP, promiseStructIR, pVal.Ref))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", oldHead, rxP))
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 0", nodeClo, node))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", clo, nodeClo))
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 1", nodeNext, node))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", oldHead, nodeNext))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", node, rxP))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)

	qt := PromiseOf(retTy)
	qt.PromiseTask = true
	return Value{Ref: q, Ty: qt}, nil
}

// ensureFetchDriveRunner emits @__kml_fetch_drive_run(ptr %env) exactly once —
// the deferred microtask step that bridges a raw fetch handle to a task promise.
// env = { ptr slot, ptr prom }: it drives the fetch (`__kml_await_fetch`, the
// same drive `await` uses), builds the Response, stores it into prom's value
// slot, and settles prom fulfilled via __kml_promise_settle (so reactions
// attached while pending fire, and a parked awaiter wakes). A transport-level
// failure (which `__kml_await_fetch` throws) is caught via setjmp and settles
// prom *rejected* — `fetch(u).catch(e => …)` recovers it; an HTTP 4xx/5xx is a
// fulfilled Response, per WHATWG. The fetch slot is NOT freed: a fetch
// Promise<Response> is a reusable value (TDD-00090) — `const p = fetch(u);
// p.then(f); await p` must still read a live slot.
func (e *Emitter) ensureFetchDriveRunner() {
	if e.usedFetchDriveRunner {
		return
	}
	e.usedFetchDriveRunner = true
	e.ensurePromiseRuntime()
	e.ensureFetchAsync()
	e.ensureExceptionHelpers()
	e.ensureMalloc()
	e.ensurePromiseSettle()

	respTy := ResponseType()
	structIR := respTy.StructIR()
	fieldStore := func(name, ir, ref string, align int) string {
		idx, _, _ := respTy.FieldIndex(name)
		return fmt.Sprintf("  %%%s_gep = getelementptr %s, ptr %%resp, i32 0, i32 %d\n  store %s %s, ptr %%%s_gep, align %d\n",
			name, structIR, idx, ir, ref, name, align)
	}
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_fetch_drive_run(ptr %%env) {
entry:
  %%slot_p = getelementptr { ptr, ptr }, ptr %%env, i32 0, i32 0
  %%slot = load ptr, ptr %%slot_p, align 8
  %%prom_p = getelementptr { ptr, ptr }, ptr %%env, i32 0, i32 1
  %%prom = load ptr, ptr %%prom_p, align 8
  %%jb = call ptr @__kml_push_jmpbuf()
  %%sj = call i32 @setjmp(ptr %%jb)
  %%threw = icmp ne i32 %%sj, 0
  br i1 %%threw, label %%catch, label %%try
try:
  %%pending = load ptr, ptr %%slot, align 8
  %%raw = call { i64, ptr, i64 } @__kml_await_fetch(ptr %%pending)
  %%status = extractvalue { i64, ptr, i64 } %%raw, 0
  %%body = extractvalue { i64, ptr, i64 } %%raw, 1
  %%blen = extractvalue { i64, ptr, i64 } %%raw, 2
  call void @__kml_pop_jmpbuf()
  %%oklow = icmp sge i64 %%status, 200
  %%okhigh = icmp slt i64 %%status, 300
  %%ok = and i1 %%oklow, %%okhigh
  %%resp = call ptr @malloc(i64 %d)
%s%s%s%s  %%bits = ptrtoint ptr %%resp to i64
  %%v0_p = getelementptr %s, ptr %%prom, i32 0, i32 2
  store i64 %%bits, ptr %%v0_p, align 8
  call void @__kml_promise_settle(ptr %%prom, i64 1)
  ret void
catch:
  %%err = call ptr @__kml_get_thrown()
  %%ebits = ptrtoint ptr %%err to i64
  %%ev0_p = getelementptr %s, ptr %%prom, i32 0, i32 2
  store i64 %%ebits, ptr %%ev0_p, align 8
  call void @__kml_promise_settle(ptr %%prom, i64 2)
  ret void
}`,
		respTy.StructSize(),
		fieldStore("status", "i64", "%status", 8),
		fieldStore("ok", "i1", "%ok", 1),
		fieldStore("body", "ptr", "%body", 8),
		fieldStore("bodyLength", "i64", "%blen", 8),
		promiseStructIR, promiseStructIR))
}

// emitFetchHandleToPendingPromise bridges a raw fetch()'s Promise<Response>
// handle (slotRef points at the malloc'd slot holding the pending curl handle)
// to a *pending* task-shaped promise whose settle is deferred to a queued
// microtask (@__kml_fetch_drive_run) — so `fetch(u).then(f); console.log("x")`
// runs the synchronous script first and the transport wait happens when the
// event loop drains, not at the `.then` call site. The fetch transfer itself is
// already in flight from the fetch() call; only the completion wait is deferred.
func (e *Emitter) emitFetchHandleToPendingPromise(slotRef string) Value {
	e.ensureMicrotasks()
	e.ensureFetchDriveRunner()

	prom := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_task_alloc_promise()", prom))

	// env = { slot, prom }; closure = { @__kml_fetch_drive_run, env }
	env := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", env))
	sGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 0", sGep, env))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", slotRef, sGep))
	pGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 1", pGep, env))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", prom, pGep))

	clo := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", clo))
	cfp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 0", cfp, clo))
	e.emitInstr(fmt.Sprintf("store ptr @__kml_fetch_drive_run, ptr %s, align 8", cfp))
	cep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 1", cep, clo))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", env, cep))
	e.emitInstr(fmt.Sprintf("call void @__kml_microtask_enqueue(ptr %s)", clo))

	rt := PromiseOf(ResponseType())
	rt.PromiseTask = true
	return Value{Ref: prom, Ty: rt}
}

// thenValLoadIR returns the IR reconstructing `%val` (of ty.IR) from the source
// promise's `%v0` i64 bits, and the argument-type string for the callback call.
// For a void source there is no argument.
func thenValLoadIR(ty Type) (load, argIR string) {
	switch ty.IR {
	case "void", "":
		return "", ""
	case "ptr":
		return "  %val = inttoptr i64 %v0 to ptr\n", "ptr"
	case "i64":
		return "  %val = add i64 %v0, 0\n", "i64"
	case "double":
		return "  %val = bitcast i64 %v0 to double\n", "double"
	default:
		return fmt.Sprintf("  %%val = trunc i64 %%v0 to %s\n", ty.IR), ty.IR
	}
}

// thenStoreResultIR returns IR that stores a callback result register (%rv<sfx>,
// of type retTy) into the returned promise %q's value slots and marks %q
// fulfilled (res = 1). For a void result it stores 0 (undefined). The sfx keeps
// SSA names unique across the two call blocks (fulfill vs reject) of one runner.
func thenStoreResultIR(retTy Type, sfx string) (produce func(callExpr string) string, storeAndSettle string) {
	settle := `  %qres` + sfx + ` = getelementptr ` + promiseStructIR + `, ptr %q, i32 0, i32 0
  store i64 1, ptr %qres` + sfx + `, align 8
`
	v0slot := `  %qv0` + sfx + ` = getelementptr ` + promiseStructIR + `, ptr %q, i32 0, i32 2
`
	switch retTy.IR {
	case "void", "":
		return func(callExpr string) string { return "  " + callExpr + "\n" },
			v0slot + "  store i64 0, ptr %qv0" + sfx + ", align 8\n" + settle
	case "i64":
		return func(callExpr string) string { return "  %rv" + sfx + " = " + callExpr + "\n" },
			v0slot + "  store i64 %rv" + sfx + ", ptr %qv0" + sfx + ", align 8\n" + settle
	case "ptr":
		return func(callExpr string) string { return "  %rv" + sfx + " = " + callExpr + "\n" },
			"  %rvb" + sfx + " = ptrtoint ptr %rv" + sfx + " to i64\n" + v0slot +
				"  store i64 %rvb" + sfx + ", ptr %qv0" + sfx + ", align 8\n" + settle
	case "double":
		return func(callExpr string) string { return "  %rv" + sfx + " = " + callExpr + "\n" },
			"  %rvb" + sfx + " = bitcast double %rv" + sfx + " to i64\n" + v0slot +
				"  store i64 %rvb" + sfx + ", ptr %qv0" + sfx + ", align 8\n" + settle
	default:
		if retTy.IsArray {
			return func(callExpr string) string { return "  %rv" + sfx + " = " + callExpr + "\n" },
				"  %rvp" + sfx + " = extractvalue { ptr, i64 } %rv" + sfx + ", 0\n" +
					"  %rvl" + sfx + " = extractvalue { ptr, i64 } %rv" + sfx + ", 1\n" +
					"  %rvpi" + sfx + " = ptrtoint ptr %rvp" + sfx + " to i64\n" + v0slot +
					"  store i64 %rvpi" + sfx + ", ptr %qv0" + sfx + ", align 8\n" +
					"  %qv1" + sfx + " = getelementptr " + promiseStructIR + ", ptr %q, i32 0, i32 3\n" +
					"  store i64 %rvl" + sfx + ", ptr %qv1" + sfx + ", align 8\n" + settle
		}
		// small ints (i1/i8/i16/i32)
		return func(callExpr string) string { return "  %rv" + sfx + " = " + callExpr + "\n" },
			"  %rvb" + sfx + " = zext " + retTy.IR + " %rv" + sfx + " to i64\n" + v0slot +
				"  store i64 %rvb" + sfx + ", ptr %qv0" + sfx + ", align 8\n" + settle
	}
}

// thenPassThroughIR copies the source promise's settlement (res, v0, v1) straight
// into Q — used by finally and by a missing fulfillment callback. sfx keeps the
// temporaries unique across the two blocks that pass through.
func thenPassThroughIR(sfx string) string {
	return "  %pv0" + sfx + " = load i64, ptr %v0_p, align 8\n" +
		"  %pv1_p" + sfx + " = getelementptr " + promiseStructIR + ", ptr %p, i32 0, i32 3\n" +
		"  %pv1" + sfx + " = load i64, ptr %pv1_p" + sfx + ", align 8\n" +
		"  %qv0pt" + sfx + " = getelementptr " + promiseStructIR + ", ptr %q, i32 0, i32 2\n" +
		"  store i64 %pv0" + sfx + ", ptr %qv0pt" + sfx + ", align 8\n" +
		"  %qv1pt" + sfx + " = getelementptr " + promiseStructIR + ", ptr %q, i32 0, i32 3\n" +
		"  store i64 %pv1" + sfx + ", ptr %qv1pt" + sfx + ", align 8\n" +
		"  %qrespt" + sfx + " = getelementptr " + promiseStructIR + ", ptr %q, i32 0, i32 0\n" +
		"  store i64 %res, ptr %qrespt" + sfx + ", align 8\n"
}

// emitThenRunner emits the per-call-site reaction runner. argTy is the source
// promise's value type (the callback argument); retTy is the callback's return
// type (what the returned promise Q settles to). The runner reads the source's
// settled state, invokes the right callback, settles Q with its result (or
// passes the source settlement through for finally / a missing callback), then
// drains Q's own reactions so a chained `.then` fires.
func (e *Emitter) emitThenRunner(runner string, argTy, retTy Type) {
	valLoad, argIR := thenValLoadIR(argTy)
	produceF, storeSettleF := thenStoreResultIR(retTy, "f")
	produceR, storeSettleR := thenStoreResultIR(retTy, "r")

	// Fulfillment callback call expression (onF). Void source ⟹ no argument.
	loadFClosure := "  %ffp_p = getelementptr { ptr, ptr }, ptr %onF, i32 0, i32 0\n" +
		"  %ffp = load ptr, ptr %ffp_p, align 8\n" +
		"  %fep_p = getelementptr { ptr, ptr }, ptr %onF, i32 0, i32 1\n" +
		"  %fep = load ptr, ptr %fep_p, align 8\n"
	var fCall string
	if argIR == "" {
		fCall = loadFClosure + produceF(fmt.Sprintf("call %s %%ffp(ptr %%fep)", thenCallRetIR(retTy)))
	} else {
		fCall = valLoad + loadFClosure + produceF(fmt.Sprintf("call %s %%ffp(ptr %%fep, %s %%val)", thenCallRetIR(retTy), argIR))
	}

	// Rejection callback call (onR) — takes the error ptr, returns retTy.
	rCall := "  %errp = inttoptr i64 %v0 to ptr\n" +
		"  %rfp_p = getelementptr { ptr, ptr }, ptr %onR, i32 0, i32 0\n" +
		"  %rfp = load ptr, ptr %rfp_p, align 8\n" +
		"  %rep_p = getelementptr { ptr, ptr }, ptr %onR, i32 0, i32 1\n" +
		"  %rep = load ptr, ptr %rep_p, align 8\n" +
		produceR(fmt.Sprintf("call %s %%rfp(ptr %%rep, ptr %%errp)", thenCallRetIR(retTy)))

	// Pass-through blocks (finally, and a missing fulfillment callback).
	passThroughD := thenPassThroughIR("d")
	passThroughF := thenPassThroughIR("pf")
	// Propagate a rejection (no onR): Q rejects with the same reason.
	propReject := `  %qv0_pr = getelementptr ` + promiseStructIR + `, ptr %q, i32 0, i32 2
  store i64 %v0, ptr %qv0_pr, align 8
  %qres_pr = getelementptr ` + promiseStructIR + `, ptr %q, i32 0, i32 0
  store i64 2, ptr %qres_pr, align 8
`
	drainRet := "  call void @__kml_promise_drain_reactions(ptr %q)\n  ret void\n"

	e.emitGlobal(fmt.Sprintf(`
define void %s(ptr %%env) {
entry:
  %%p_p = getelementptr { ptr, ptr, ptr, ptr, ptr }, ptr %%env, i32 0, i32 0
  %%p = load ptr, ptr %%p_p, align 8
  %%onF_p = getelementptr { ptr, ptr, ptr, ptr, ptr }, ptr %%env, i32 0, i32 1
  %%onF = load ptr, ptr %%onF_p, align 8
  %%onR_p = getelementptr { ptr, ptr, ptr, ptr, ptr }, ptr %%env, i32 0, i32 2
  %%onR = load ptr, ptr %%onR_p, align 8
  %%onFin_p = getelementptr { ptr, ptr, ptr, ptr, ptr }, ptr %%env, i32 0, i32 3
  %%onFin = load ptr, ptr %%onFin_p, align 8
  %%q_p = getelementptr { ptr, ptr, ptr, ptr, ptr }, ptr %%env, i32 0, i32 4
  %%q = load ptr, ptr %%q_p, align 8
  %%res_p = getelementptr %s, ptr %%p, i32 0, i32 0
  %%res = load i64, ptr %%res_p, align 8
  %%v0_p = getelementptr %s, ptr %%p, i32 0, i32 2
  %%v0 = load i64, ptr %%v0_p, align 8
  %%hasFin = icmp ne ptr %%onFin, null
  br i1 %%hasFin, label %%dofin, label %%branch
dofin:
  %%finfp_p = getelementptr { ptr, ptr }, ptr %%onFin, i32 0, i32 0
  %%finfp = load ptr, ptr %%finfp_p, align 8
  %%finep_p = getelementptr { ptr, ptr }, ptr %%onFin, i32 0, i32 1
  %%finep = load ptr, ptr %%finep_p, align 8
  call void (ptr) %%finfp(ptr %%finep)
%s%s
branch:
  %%isful = icmp eq i64 %%res, 1
  br i1 %%isful, label %%ful, label %%rej
ful:
  %%hasF = icmp ne ptr %%onF, null
  br i1 %%hasF, label %%callF, label %%passF
callF:
%s%s
passF:
%s%s
rej:
  %%hasR = icmp ne ptr %%onR, null
  br i1 %%hasR, label %%callR, label %%propR
callR:
%s%s
propR:
%s%s
}`, runner, promiseStructIR, promiseStructIR,
		passThroughD, drainRet,
		fCall, storeSettleF+drainRet,
		passThroughF, drainRet,
		rCall, storeSettleR+drainRet,
		propReject, drainRet))
}

// thenCallRetIR gives the LLVM return-type token for a `call` to a then/catch
// callback: the empty string maps to "void".
func thenCallRetIR(retTy Type) string {
	if retTy.IR == "" || retTy.IR == "void" {
		return "void"
	}
	if retTy.IsArray {
		return "{ ptr, i64 }"
	}
	return retTy.IR
}
