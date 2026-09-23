// emit_async.go — async function prologue/epilogue and await expression emission.
// Strategy: an async function mallocs a slot for the return value on the heap and
// returns its pointer.  await reads the value from the slot and frees it.
// No LLVM coroutine intrinsics are used.
package llvm

import (
	"KlainMainLang/ast"
	"fmt"
)

// ── Async function prologue / epilogue ────────────────────────────────────────

// emitAsyncPrologue mallocs the promise slot in the entry block (e.allocas) and
// stores its pointer in e.coroHdl.  Called before the function body is emitted.
func (e *Emitter) emitAsyncPrologue() {
	e.ensureMalloc()
	size := int64(8) // default: enough for i64 / ptr
	if e.currentPromiseTy.IR != "void" && e.currentPromiseTy.IR != "" {
		// Array-typed promises store {ptr, i64} (16 bytes), not the bare
		// 8-byte ptr Align() alone would suggest — see StructFieldSize.
		a := StructFieldSize(e.currentPromiseTy)
		if a > size {
			size = a
		}
	} else {
		size = 1 // Promise<void>: allocate one byte (never written)
	}
	// Zeroed: a body that falls off the end never writes the slot, and the
	// epilogue reads it as the fulfilment value — zero is `undefined` for a
	// `T | undefined` result (absent presence bit), 0/null otherwise (ADR-01065).
	e.ensureCalloc()
	frameReg := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = call ptr @calloc(i64 1, i64 %d)", frameReg, size))
	e.coroHdl = frameReg
}

// emitAsyncEpilogue emits an implicit branch to the async-return label and the
// return block itself.  Must be called after all body instructions.
func (e *Emitter) emitAsyncEpilogue() {
	e.emitTerminator(fmt.Sprintf("br label %%%s", e.coroRetLabel))
	e.body.WriteString(fmt.Sprintf("\n%s:\n", e.coroRetLabel))
	e.body.WriteString(fmt.Sprintf("  ret ptr %s\n", e.coroHdl))
}

// emitInlineAsyncPrologue / emitInlineAsyncEpilogue choose the inline (non-fiber)
// async model: the settled task-promise wrapper (TDD-00084 Part A) for an ordinary
// async function/arrow, or the old bare-slot model for an http.listen handler,
// which the connection-fiber dispatcher reads directly (not as a task promise).
func (e *Emitter) emitInlineAsyncPrologue() {
	if e.emittingHTTPHandler {
		e.emitAsyncPrologue()
	} else {
		e.emitSettledAsyncPrologue()
	}
}

func (e *Emitter) emitInlineAsyncEpilogue() {
	if e.emittingHTTPHandler {
		e.emitAsyncEpilogue()
	} else {
		e.emitSettledAsyncEpilogue()
	}
}

// asyncSlotSize is the byte size of the coroHdl return-value slot for a
// Promise<T> — enough for the marshalled value (array T is {ptr,i64} = 16).
func asyncSlotSize(pty Type) int64 {
	if pty.IR == "void" || pty.IR == "" {
		return 1 // Promise<void>: one byte, never written
	}
	size := int64(8)
	if a := StructFieldSize(pty); a > size {
		size = a
	}
	return size
}

// emitSettledAsyncPrologue is the non-suspending (inline) async function's
// prologue (TDD-00084 Part A, Option A2): besides the coroHdl return-value slot,
// it allocates the real task-shaped promise the function returns and brackets the
// body in a setjmp handler. Normal completion settles the promise (fulfilled);
// a throw is caught and settles it rejected — so calling a non-suspending async
// function never throws synchronously, it returns a settled promise like real JS.
// No fiber, no scheduler: the only cost over the old bare-slot path is one
// setjmp + a 40-byte promise. Used only when !taskBody (a may-suspend body keeps
// emitAsyncPrologue + emitTaskEpilogue).
func (e *Emitter) emitSettledAsyncPrologue() {
	e.ensurePromiseRuntime()
	e.ensureMalloc()

	// coroHdl slot for the body's return value (the return machinery stores here).
	// Zeroed for the fall-off path — see emitAsyncPrologue.
	e.ensureCalloc()
	frameReg := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = call ptr @calloc(i64 1, i64 %d)", frameReg, asyncSlotSize(e.currentPromiseTy)))
	e.coroHdl = frameReg

	// The settled task promise this function returns.
	prom := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = call ptr @__kml_task_alloc_promise()", prom))
	e.asyncPromiseReg = prom

	// setjmp catch: a throw in the body longjmps here (see __kml_throw). The
	// push/setjmp/branch live in the entry block after every alloca; the body
	// proper starts at async.body.
	e.asyncCatchLabel = e.freshLabel("async.caught")
	bodyL := e.freshLabel("async.body")
	jb := e.freshReg()
	sj := e.freshReg()
	threw := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_push_jmpbuf()", jb))
	e.emitInstr(fmt.Sprintf("%s = %s", sj, setjmpCall(jb)))
	e.emitInstr(fmt.Sprintf("%s = icmp ne i32 %s, 0", threw, sj))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", threw, e.asyncCatchLabel, bodyL))
	e.emitLabel(bodyL)
}

// emitSettledAsyncEpilogue emits the non-suspending async function's two return
// paths: the normal path (marshal coroHdl → promise, mark fulfilled, pop the
// jmpbuf, return the promise) and the catch path (store the thrown error, mark
// rejected, return the promise). The throw path's jmpbuf was already popped by
// __kml_throw, matching emitTry.
func (e *Emitter) emitSettledAsyncEpilogue() {
	prom := e.asyncPromiseReg
	setResolved := func(state int) {
		rp := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 0", rp, promiseStructIR, prom))
		e.emitInstr(fmt.Sprintf("store i64 %d, ptr %s, align 8", state, rp))
	}

	// --- normal completion: fulfill ---
	e.emitTerminator("br label %" + e.coroRetLabel)
	e.emitLabel(e.coroRetLabel)
	e.emitInstr("call void @__kml_pop_jmpbuf()")
	pty := e.currentPromiseTy
	if pty.IR != "void" && pty.IR != "" {
		if pty.IsArray {
			// The return-value slot holds a header pointer (TDD-00213 Stage 2);
			// deref it to the aggregate before decomposing into the promise value.
			e.storePromiseValue(prom, e.loadArraySlotAggregate(e.coroHdl, pty))
		} else {
			valReg := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", valReg, StructFieldIR(pty), e.coroHdl, pty.Align()))
			e.storePromiseValue(prom, Value{Ref: valReg, Ty: pty})
		}
	}
	setResolved(1)
	e.emitInstr(fmt.Sprintf("call void @free(ptr %s)", e.coroHdl))
	e.emitTerminator(fmt.Sprintf("ret ptr %s", prom))

	// --- throw caught: reject ---
	// Reject with the real thrown value, carried as a caught-value (tag, payload)
	// from the throw channel — so `await`ing this rejection or a .catch handler
	// sees the true value (number/string/Error), not an Error wrapper (TDD-00207).
	e.emitLabel(e.asyncCatchLabel)
	tagR := e.freshReg()
	tagW := e.freshReg()
	payR := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i8 @__kml_get_thrown_tag()", tagR))
	e.emitInstr(fmt.Sprintf("%s = zext i8 %s to i64", tagW, tagR))
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_get_thrown_pay()", payR))
	e.storeRejectReasonI64Tag(prom, tagW, payR)
	setResolved(2)
	e.emitInstr(fmt.Sprintf("call void @free(ptr %s)", e.coroHdl))
	e.emitTerminator(fmt.Sprintf("ret ptr %s", prom))
}

// buildResponseFromStatusBody builds a Response object (ADR-00021's
// {status, ok, body} struct, plus ADR-00094's bodyLength) from a completed
// fetch's raw status (i64), body (ptr), and bodyLen (i64) SSA registers —
// factored out of emitAwait's IsResponse branch below so Promise.all/.race
// (emit_promise.go, ADR-00073) can build the same shape per member of a
// group of concurrently-awaited fetches, without duplicating this
// struct-building code a third time.
func (e *Emitter) buildResponseFromStatusBody(status, body, bodyLen string) Value {
	return e.buildResponseWithPending(status, body, bodyLen, "null")
}

// buildResponseWithPending is the TDD-00097 Stage 4 core: a Response that
// additionally carries its pending-fetch handle (null for an already-finished
// body, e.g. the combinators' group waits).
func (e *Emitter) buildResponseWithPending(status, body, bodyLen, pendingRef string) Value {
	ok := e.freshReg()
	okHigh := e.freshReg()
	okLow := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp sge i64 %s, 200", okLow, status))
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 300", okHigh, status))
	e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", ok, okLow, okHigh))

	respTy := ResponseType()
	e.ensureMalloc()
	respReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", respReg, respTy.StructSize()))
	structIR := respTy.StructIR()
	storeField := func(name, ir, ref string, align int) {
		idx, _, _ := respTy.FieldIndex(name)
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, structIR, respReg, idx))
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", ir, ref, gep, align))
	}
	// status is a `number` (float64) field (TDD-00123); the runtime hands an i64.
	statusF := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sitofp i64 %s to double", statusF, status))
	storeField("status", "double", statusF, 8)
	storeField("ok", "i1", ok, 1)
	storeField("body", "ptr", body, 8)
	storeField("bodyLength", "i64", bodyLen, 8)
	storeField("__kml_pending", "ptr", pendingRef, 8)

	return Value{Ref: respReg, Ty: respTy}
}

// ── await expression ──────────────────────────────────────────────────────────

// emitAwait evaluates the Promise (a ptr to the heap slot), loads the resolved
// value, frees the slot, and returns the inner value.
func (e *Emitter) emitAwait(ex *ast.AwaitExpression) (Value, error) {
	e.sawAwait = true // the enclosing async callable runs as a coroutine (TDD-00223 §2)
	hdlVal, err := e.emitExpr(ex.Argument)
	if err != nil {
		return Value{}, err
	}

	// `await` of a non-thenable is identity in JS — the value passes straight
	// through, unchanged. Response.text()/.json() (a string) and .arrayBuffer()
	// (a buffer) resolve synchronously and are not Promises; treating one as a
	// Promise slot (the load-and-free below) would free the live buffer and
	// hand back an empty value. Guard that here rather than in every caller.
	//
	// Identity in *value* only: JS still defers the continuation of EVERY await
	// — `await 1` yields a microtask tick exactly like awaiting an already-
	// settled promise (`f(); log("c")` where f awaits a plain value prints
	// `a c b`). Inside an async fn compiled as a task, wrap the value in a
	// settled task promise and go through the shared task await, whose
	// park-and-resume-as-microtask is that tick (TDD-00088). Outside a task
	// context (an async generator's fiber has no current task to park) the
	// plain identity return stands.
	argTy := e.inferExprType(ex.Argument)
	if !hdlVal.Ty.IsPromise && !argTy.IsPromise {
		inAsyncGenBody := e.currentGenerator != nil && e.currentGenerator.genTy.GeneratorIsAsync
		if ((e.isAsync || e.inModuleTask) && e.currentGenerator == nil && e.hasMaySuspend) || inAsyncGenBody {
			e.ensurePromiseRuntime()
			prom := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_task_alloc_promise()", prom))
			if hdlVal.Ty.IR != "void" && hdlVal.Ty.IR != "" {
				e.storePromiseValue(prom, hdlVal)
			}
			// A fresh promise has no reactions/waiter yet, so a raw fulfilled
			// store (no settle-drain) is safe here.
			rp := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 0", rp, promiseStructIR, prom))
			e.emitInstr(fmt.Sprintf("store i64 1, ptr %s, align 8", rp))
			return e.emitAwaitTaskPromise(prom, hdlVal.Ty)
		}
		// Every module top level with an await is a coroutine now — the entry
		// program, a worker module, a dynamic-import island (TDD-00224,
		// TDD-00225) — so nothing reaches here from module code; the main-stack
		// tick this branch once took (ADR-01044) is gone with it.
		return hdlVal, nil
	}

	if hdlVal.Ty.IR != "ptr" {
		return Value{}, fmt.Errorf("%d:%d: await requires a Promise value",
			ex.GetPos().Line, ex.GetPos().Col)
	}

	// Determine the unwrapped type (T in Promise<T>).
	var promiseTy Type
	if hdlVal.Ty.IsPromise && hdlVal.Ty.PromiseType != nil {
		promiseTy = *hdlVal.Ty.PromiseType
	} else if argTy.IsPromise && argTy.PromiseType != nil {
		promiseTy = *argTy.PromiseType
	} else {
		promiseTy = TypeVoid
	}

	// A task-shaped promise. When the program has may-suspend async fns (fibers),
	// wait via the scheduler (parks the current task, or drives the loop at top
	// level). When it has none (TDD-00084 Part A), every async result settled
	// inline, so there is nothing to wait for — read it directly, no scheduler,
	// no libcurl. Either way, read the marshalled value and free the promise.
	if hdlVal.Ty.PromiseTask {
		return e.emitAwaitTaskPromise(hdlVal.Ref, promiseTy)
	}

	e.ensureFree()

	if promiseTy.IR == "void" || promiseTy.IR == "" {
		// Promise<void>: nothing to read. The slot is not freed — a Promise is a
		// reusable value (see emitAwaitTaskPromise); the slot leaks in manual mode
		// like any allocation (collected under `-mm=gc`).
		return Value{Ty: TypeVoid}, nil
	}

	if promiseTy.IsResponse && !promiseTy.PromiseResolved {
		return e.emitAwaitFetchSlot(hdlVal.Ref), nil
	}

	if promiseTy.IsArray {
		// The resolved-value buffer holds a header pointer for an array (TDD-00213
		// Stage 2); deref it back into the {ptr,i64} aggregate.
		return e.loadArraySlotAggregate(hdlVal.Ref, promiseTy), nil
	}
	resultReg := e.freshReg()
	align := promiseTy.Align()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d",
		resultReg, StructFieldIR(promiseTy), hdlVal.Ref, align))
	// The slot is not freed — a Promise is a reusable value (see
	// emitAwaitTaskPromise); it leaks in manual mode, collected under `-mm=gc`.
	return Value{Ref: resultReg, Ty: promiseTy}, nil
}

// emitAwaitFetchSlot drives a raw fetch Promise<Response> slot (slotRef is the
// ptr to the heap slot whose first field holds the pending struct) to completion
// and returns the built Response. Factored out of emitAwait's IsResponse branch
// so the for-await element path (emitAwaitPromiseElem) can share it.
// The slot is not freed: a fetch Promise<Response> is a reusable value
// (TDD-00090). Its pending struct is never freed (like every fetch allocation —
// see runtime_fetch.go), `__kml_await_fetch` short-circuits an already-done
// handle, and `__kml_pending_finish` is a non-destructive read of the stored
// status/body — so `await r; await r` re-reads the same Response instead of a
// freed slot. A transport-level failure throws (an HTTP 4xx/5xx is a fulfilled
// Response, per WHATWG).
func (e *Emitter) emitAwaitFetchSlot(slotRef string) Value {
	// `await p` on a fetch promise awaits the fetch's one bridge promise — the
	// same promise `p.then(...)` reacts to — so awaits and reactions interleave
	// in registration order and share one Response (TDD-00223 §3). The generator
	// fiber and the http.listen handler keep the direct wait: neither is a task,
	// and each already parks on the transfer itself.
	if e.hasMaySuspend && e.currentGenerator == nil && !e.emittingHTTPHandler {
		prom := e.emitFetchHandleToPendingPromise(slotRef)
		if v, err := e.emitAwaitTaskPromise(prom.Ref, ResponseType()); err == nil {
			return v
		}
	}
	e.ensureFetchAsync()
	e.ensureAwaitFetchHeaders()
	pendingPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", pendingPtr, slotRef))
	// Resolve at headers-complete (TDD-00097 Stage 4): the Response is built
	// with the status and its pending handle; the body is read lazily —
	// .text()/.json()/.arrayBuffer() drive the transfer to completion,
	// .body streams the rest as it arrives.
	status := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_await_fetch_headers(ptr %s)", status, pendingPtr))
	return e.buildResponseWithPending(status, "null", "0", pendingPtr)
}

// emitAwaitPromiseElem awaits an already-evaluated promise value (elemRef is the
// slot/handle ptr, elemTy the static Promise<T> type) and returns the awaited T
// — the per-element await `for await...of` performs on a promise element from an
// array, Map/Set values, or a sync generator's yields. Dispatches exactly as
// emitAwait does after evaluation: a raw fetch Promise<Response> drives the
// fetch (emitAwaitFetchSlot); anything else is a task-shaped promise driven by
// emitAwaitTaskPromise (which re-throws a rejection at the loop, stopping it —
// matching JS).
func (e *Emitter) emitAwaitPromiseElem(elemRef string, elemTy Type) (Value, error) {
	awaitedTy := TypeVoid
	if elemTy.PromiseType != nil {
		awaitedTy = *elemTy.PromiseType
	}
	if awaitedTy.IsResponse && !awaitedTy.PromiseResolved && !elemTy.PromiseTask {
		return e.emitAwaitFetchSlot(elemRef), nil
	}
	return e.emitAwaitTaskPromise(elemRef, awaitedTy)
}

// emitAwaitTaskPromise drives a task-shaped promise (already evaluated to hdlRef)
// to settled and returns its value of type promiseTy — re-throwing on the caller
// side if it rejected. Shared by `await` and by async-return flattening
// (`return <promise>` from an async fn — ADR-00265). When the program has
// may-suspend fns it waits via the scheduler; otherwise it drains microtasks and
// fires timers until the promise settles (a `.then` chain / `setTimeout`-deferred
// `new Promise`), then frees the slot.
func (e *Emitter) emitAwaitTaskPromise(hdlRef string, promiseTy Type) (Value, error) {
	e.ensureFree()
	e.ensureExceptionHelpers()
	if e.currentGenerator != nil && e.currentGenerator.genTy.GeneratorIsAsync {
		// Inside an async generator's body (the fiber): park the step on the
		// promise — the step's q stays pending, the consumer's script continues,
		// and the settle re-enters the fiber right here via a microtask.
		e.emitAsyncGenAwaitParkUntilSettled(hdlRef)
	} else if e.hasMaySuspend {
		e.ensureTaskRuntime()
		e.emitInstr(fmt.Sprintf("call void @__kml_task_await_ready(ptr %s)", hdlRef))
	} else {
		e.ensurePromiseRuntime()
		e.ensureMicrotasks()
		// A pending task promise with no coroutine to park: this is module
		// top-level code (or a synchronous-path async fn it called). Wait by
		// taking turns of the real event loop until the promise settles
		// (@__kml_top_await, TDD-00223 §1) — a `.then` chain, a timer-deferred
		// `new Promise`, a child's output, a socket: every source is served, as
		// when Node suspends a module at its await. A promise nothing can ever
		// settle ends the process with Node's exit code 13.
		e.noteLoopTurn()
		sp := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 0", sp, promiseStructIR, hdlRef))
		e.emitInstr(fmt.Sprintf("call void @__kml_top_await(ptr %s)", sp))
	}
	// A rejected task promise (resolved == 2) re-throws its stored error at the
	// awaiter, so `try { await f() } catch` works (TDD-00083 Stage 2).
	resReg := e.freshReg()
	resP := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 0", resP, promiseStructIR, hdlRef))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", resReg, resP))
	rej := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 2", rej, resReg))
	rejL := e.freshLabel("await.reject")
	okL := e.freshLabel("await.ok")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", rej, rejL, okL))
	e.emitLabel(rejL)
	// Re-throw the real reason through the throw-any channel (tag, payload) so a
	// non-Error rejection (`throw 42`, `Promise.reject("x")`) surfaces at the
	// awaiter as the true value rather than an Error wrapper (TDD-00207).
	reasonC := e.loadRejectReasonCaught(hdlRef)
	rtag, rpay := e.caughtParts(reasonC)
	// The promise slot is not freed: a Promise is a reusable value in JS,
	// awaitable any number of times (`await p; await p`) and readable after a
	// combinator — freeing it here made the second read a use-after-free. The
	// 40-byte task-promise struct leaks in manual mode, the same ambient
	// behavior every unreclaimed allocation has (collected under `-mm=gc`).
	e.emitInstr(fmt.Sprintf("call void @__kml_throw_any(i8 %s, i64 %s)", rtag, rpay))
	e.emitTerminator("unreachable")
	e.emitLabel(okL)
	if promiseTy.IR == "void" || promiseTy.IR == "" {
		return Value{Ty: TypeVoid}, nil
	}
	val := e.loadPromiseValue(hdlRef, promiseTy)
	return val, nil
}
