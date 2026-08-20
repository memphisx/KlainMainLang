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
	frameReg := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", frameReg, size))
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
	frameReg := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", frameReg, asyncSlotSize(e.currentPromiseTy)))
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
	e.emitInstr(fmt.Sprintf("%s = call i32 @setjmp(ptr %s)", sj, jb))
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
		valReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", valReg, StructFieldIR(pty), e.coroHdl, pty.Align()))
		e.storePromiseValue(prom, Value{Ref: valReg, Ty: pty})
	}
	setResolved(1)
	e.emitInstr(fmt.Sprintf("call void @free(ptr %s)", e.coroHdl))
	e.emitTerminator(fmt.Sprintf("ret ptr %s", prom))

	// --- throw caught: reject ---
	e.emitLabel(e.asyncCatchLabel)
	errReg := e.freshReg()
	errBits := e.freshReg()
	v0P := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_get_thrown()", errReg))
	e.emitInstr(fmt.Sprintf("%s = ptrtoint ptr %s to i64", errBits, errReg))
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 2", v0P, promiseStructIR, prom))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", errBits, v0P))
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
	storeField("status", "i64", status, 8)
	storeField("ok", "i1", ok, 1)
	storeField("body", "ptr", body, 8)
	storeField("bodyLength", "i64", bodyLen, 8)

	return Value{Ref: respReg, Ty: respTy}
}

// ── await expression ──────────────────────────────────────────────────────────

// emitAwait evaluates the Promise (a ptr to the heap slot), loads the resolved
// value, frees the slot, and returns the inner value.
func (e *Emitter) emitAwait(ex *ast.AwaitExpression) (Value, error) {
	hdlVal, err := e.emitExpr(ex.Argument)
	if err != nil {
		return Value{}, err
	}

	// `await` of a non-thenable is identity in JS — the value passes straight
	// through, unchanged. Response.text()/.json() (a string) and .arrayBuffer()
	// (a buffer) resolve synchronously and are not Promises; treating one as a
	// Promise slot (the load-and-free below) would free the live buffer and
	// hand back an empty value. Guard that here rather than in every caller.
	argTy := e.inferExprType(ex.Argument)
	if !hdlVal.Ty.IsPromise && !argTy.IsPromise {
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
		e.ensureFree()
		e.ensureExceptionHelpers()
		if e.hasMaySuspend {
			e.ensureTaskRuntime()
			e.emitInstr(fmt.Sprintf("call void @__kml_task_await_ready(ptr %s)", hdlVal.Ref))
		} else {
			e.ensurePromiseRuntime()
		}
		// A rejected task promise (resolved == 2) re-throws its stored error at
		// the awaiter, so `try { await f() } catch` works (TDD-00083 Stage 2).
		resReg := e.freshReg()
		resP := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 0", resP, promiseStructIR, hdlVal.Ref))
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", resReg, resP))
		rej := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 2", rej, resReg))
		rejL := e.freshLabel("await.reject")
		okL := e.freshLabel("await.ok")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", rej, rejL, okL))
		e.emitLabel(rejL)
		v0P := e.freshReg()
		v0 := e.freshReg()
		errReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 2", v0P, promiseStructIR, hdlVal.Ref))
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", v0, v0P))
		e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", errReg, v0))
		e.emitInstr(fmt.Sprintf("call void @free(ptr %s)", hdlVal.Ref))
		e.emitInstr(fmt.Sprintf("call void @__kml_throw(ptr %s)", errReg))
		e.emitTerminator("unreachable")
		e.emitLabel(okL)
		if promiseTy.IR == "void" || promiseTy.IR == "" {
			e.emitInstr(fmt.Sprintf("call void @free(ptr %s)", hdlVal.Ref))
			return Value{Ty: TypeVoid}, nil
		}
		val := e.loadPromiseValue(hdlVal.Ref, promiseTy)
		e.emitInstr(fmt.Sprintf("call void @free(ptr %s)", hdlVal.Ref))
		return val, nil
	}

	e.ensureFree()

	if promiseTy.IR == "void" || promiseTy.IR == "" {
		// Promise<void>: just free the 1-byte slot.
		e.emitInstr(fmt.Sprintf("call void @free(ptr %s)", hdlVal.Ref))
		return Value{Ty: TypeVoid}, nil
	}

	if promiseTy.IsResponse && !promiseTy.PromiseResolved {
		// fetch()'s Promise<Response>: the slot holds a still-pending fetch
		// handle (see emit_fetch.go's emitFetch), not a Response yet.
		// __kml_await_fetch does the actual wait (yielding if on a
		// connection fiber, busy-spinning otherwise — see
		// ensureFetchAsync's doc comment) and returns the final
		// status/body once the transfer completes, throwing on a
		// transfer-level failure exactly like the old blocking fetch did.
		// (A Promise<Response> with PromiseResolved set — Promise.race's
		// own Response branch, emit_promise.go — already did this waiting
		// itself and falls through to the generic branch below instead,
		// which just reads the already-built Response struct straight out
		// of the slot; see PromiseResolved's doc comment in types.go.)
		e.ensureFetchAsync()
		pendingPtr := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", pendingPtr, hdlVal.Ref))
		raw := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call { i64, ptr, i64 } @__kml_await_fetch(ptr %s)", raw, pendingPtr))
		status := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = extractvalue { i64, ptr, i64 } %s, 0", status, raw))
		body := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = extractvalue { i64, ptr, i64 } %s, 1", body, raw))
		bodyLen := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = extractvalue { i64, ptr, i64 } %s, 2", bodyLen, raw))

		respVal := e.buildResponseFromStatusBody(status, body, bodyLen)

		e.emitInstr(fmt.Sprintf("call void @free(ptr %s)", hdlVal.Ref))
		return respVal, nil
	}

	// Load the promised value then free the slot. Array-typed values are
	// stored as {ptr, i64} (see StructFieldIR/StructFieldSize), not the bare
	// ptr promiseTy.IR alone would suggest — matching emitAsyncPrologue's
	// slot sizing above.
	resultReg := e.freshReg()
	align := promiseTy.Align()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d",
		resultReg, StructFieldIR(promiseTy), hdlVal.Ref, align))
	e.emitInstr(fmt.Sprintf("call void @free(ptr %s)", hdlVal.Ref))

	return Value{Ref: resultReg, Ty: promiseTy}, nil
}