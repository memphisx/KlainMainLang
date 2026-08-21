// runtime_task.go — the generalized async-task (coroutine) runtime for true
// async-function suspension (TDD-00083 Stage 2). This decouples the ucontext
// fiber machinery from HTTP connections (runtime_http.go, where fibers are only
// ever spawned on socket-accept with a hardwired dispatcher entry): here a task
// is a fiber running an arbitrary async-function body, with its own park state
// and its own promise to fulfill.
//
// Model (see TDD-00083):
//   - A *task* is `{ ptr ctx, ptr stack, ptr promiseSlot, i64 state, ptr
//     pendingFetch, ptr pendingGroup, ptr pendingPromise, ptr fn, ptr args,
//     ptr resumerCtx }` (10 fields, 80 bytes). state: 0 ready/running, 1
//     suspended, 2 done.
//   - A *pending promise* (what a may-suspend async fn returns) is `{ i64
//     resolved, ptr waiter, i64 v0, i64 v1 }` (32 bytes): v0/v1 hold the result
//     (v1 only for array-shaped {ptr,i64} values); waiter is the one task parked
//     awaiting this promise, resumed when it resolves.
//   - Every task exits — whether it suspends at an await or completes — via
//     `swapcontext(self.ctx, self.resumerCtx)`. resumerCtx is written by whoever
//     swaps *in*: the caller at spawn time (so a synchronously-completing child
//     returns to its caller, JS "run to first await" semantics), or the event
//     loop at scheduler-resume time (so a resumed task returns to the scheduler).
//     This sidesteps uc_link's single-fixed-target ambiguity across nested
//     fibers, which the HTTP model never hits (its fibers are never nested).
//
// GC-mode stack-bottom juggling mirrors runtime_http.go: whoever swaps into a
// fiber repoints GC_stackbottom at that fiber's stack and restores it after the
// swap returns. Task field offsets are bytes into the 80-byte struct.

package llvm

import "fmt"

// task struct field indices (LLVM `{ ptr, ptr, ptr, i64, ptr, ptr, ptr, ptr, ptr, ptr }`).
const (
	taskCtx           = 0
	taskStack         = 1
	taskPromiseSlot   = 2
	taskState         = 3
	taskPendingFetch  = 4
	taskPendingGroup  = 5
	taskPendingProm   = 6
	taskFn            = 7
	taskArgs          = 8
	taskResumerCtx    = 9
	taskJmpStk        = 10 // this task's own jmpbuf stack (fiber-safe exceptions)
	taskSavedJmpTop   = 11 // jmp_top saved across suspension
	taskStructBytes   = 96
	taskStructIR      = "{ ptr, ptr, ptr, i64, ptr, ptr, ptr, ptr, ptr, ptr, ptr, i64 }"
	// promise resolved: 0 = pending, 1 = fulfilled (v0/v1 hold the value), 2 =
	// rejected (v0 holds the error object pointer's bits) — TDD-00083 Stage 2.
	// Field 4 (reactions) is the head of a { ptr closure, ptr next } list of
	// .then/.catch/.finally reactions, enqueued as microtasks when it settles.
	promiseStructIR   = "{ i64, ptr, i64, i64, ptr }"
	promiseStructSize = 40
	// per-task jmpbuf stack: 16 frames * 512 bytes/frame.
	taskJmpStkBytes = 16 * 512
	// async-task fiber stacks are far shallower than the HTTP path's 1 MiB
	// (most async bodies are a handful of frames); 256 KiB keeps memory per
	// in-flight async call modest while leaving generous headroom.
	taskStackBytes = 256 * 1024
)

// ensureTaskRuntime emits the async-task coroutine runtime once. Inert until
// the async-function emitter (emit_async.go/emit_func.go) routes a may-suspend
// async call through @__kml_spawn_task — see TDD-00083 Stage 2.
// ensurePromiseRuntime emits just the promise value struct + allocator and the
// microtask/exception helpers a settled promise needs — WITHOUT the fiber
// scheduler or the libcurl fetch runtime (TDD-00084 Part A). A non-suspending
// async function (inline catch-and-settle) and `.then`/`.catch`/`.finally` use
// this, so a pure-async program that never touches fetch does not link libcurl
// or the ucontext machinery. ensureTaskRuntime is a superset that calls this.
func (e *Emitter) ensurePromiseRuntime() {
	if e.usedPromiseRuntime {
		return
	}
	e.usedPromiseRuntime = true
	e.ensureMalloc()
	e.ensureFree()
	e.ensureExceptionHelpers() // setjmp / __kml_throw / __kml_get_thrown for the catch-and-settle wrapper
	e.ensureMicrotasks()       // .then/.catch/.finally reactions + __kml_promise_drain_reactions

	// @__kml_task_alloc_promise() -> ptr : a fresh unresolved pending promise.
	e.emitGlobal(fmt.Sprintf(`
define ptr @__kml_task_alloc_promise() {
entry:
  %%p = call ptr @malloc(i64 %d)
  %%res_p = getelementptr %s, ptr %%p, i32 0, i32 0
  store i64 0, ptr %%res_p, align 8
  %%w_p = getelementptr %s, ptr %%p, i32 0, i32 1
  store ptr null, ptr %%w_p, align 8
  %%rx_p = getelementptr %s, ptr %%p, i32 0, i32 4
  store ptr null, ptr %%rx_p, align 8
  ret ptr %%p
}`, promiseStructSize, promiseStructIR, promiseStructIR, promiseStructIR))

	// @__kml_promise_first_fulfilled(members, count) -> i64: the scheduler-free
	// scan for Promise.any over already-settled task promises (TDD-00084 Part A,
	// the no-may-suspend program) — returns the first member with resolved == 1,
	// or -1 if none fulfilled. Unlike __kml_task_await_first_fulfilled it never
	// parks or drives, so it pulls in no fiber/libcurl runtime.
	e.emitGlobal(fmt.Sprintf(`
define i64 @__kml_promise_first_fulfilled(ptr %%members, i64 %%count) {
entry:
  br label %%scan
scan:
  %%i = phi i64 [ 0, %%entry ], [ %%inext, %%cont ]
  %%go = icmp slt i64 %%i, %%count
  br i1 %%go, label %%body, label %%none
body:
  %%mg = getelementptr ptr, ptr %%members, i64 %%i
  %%m = load ptr, ptr %%mg, align 8
  %%rp = getelementptr %s, ptr %%m, i32 0, i32 0
  %%r = load i64, ptr %%rp, align 8
  %%ful = icmp eq i64 %%r, 1
  br i1 %%ful, label %%found, label %%cont
cont:
  %%inext = add i64 %%i, 1
  br label %%scan
found:
  ret i64 %%i
none:
  ret i64 -1
}`, promiseStructIR))
}

// emitLoopTaskStubs emits no-op definitions for the symbols __kml_event_loop_run
// references so it can drive coroutine tasks (TDD-00084 Part B) —
// __kml_task_active / __kml_task_sched_step / __kml_drain_microtasks — for a
// program that uses the event loop (SSE/WS/http.listen) but not the task runtime
// and/or not microtasks. When the real runtimes are present their definitions are
// used and the corresponding stub is skipped. Called once at program finalization.
func (e *Emitter) emitLoopTaskStubs() {
	if !e.usedHTTP {
		return
	}
	if !e.usedTaskRuntime {
		e.emitGlobal("@__kml_task_active = internal global i64 0, align 8")
		e.emitGlobal("define void @__kml_task_sched_step() {\nentry:\n  ret void\n}")
	}
	if !e.usedMicrotasks {
		e.emitGlobal("define void @__kml_drain_microtasks() {\nentry:\n  ret void\n}")
	}
}

func (e *Emitter) ensureTaskRuntime() {
	if e.usedTaskRuntime {
		return
	}
	e.usedTaskRuntime = true
	e.ensurePromiseRuntime()    // promise struct + __kml_task_alloc_promise (+ microtasks/exceptions)
	e.ensureMalloc()
	e.ensureFree()
	e.ensureFiberRuntime()      // getcontext/makecontext/swapcontext + @__kml_main_ctx
	e.ensureCurrentTaskGlobal() // @__kml_current_task
	e.ensureExceptionHelpers()  // @__kml_cur_jmp_stk / setjmp / __kml_throw (task rejection)
	e.usedAwaitTimerDrive = true // __kml_task_await_ready's top-level drive fires timers (TDD-00088)
	e.ensureMicrotasks()        // .then/.catch/.finally reactions drain here
	// The top-level scheduler drive pumps libcurl; a may-suspend program always
	// uses fetch, so pull in that runtime (idempotent) for @__kml_curl_multi /
	// curl_multi_perform / __kml_curl_drain_messages rather than re-declaring.
	e.ensureFetchAsync()

	ctxSize, ssSpOff, ssSizeOff, ucLinkOff := ucontextLayout()

	e.emitGlobal("@__kml_task_launching = internal global ptr null, align 8")
	e.emitGlobal("@__kml_task_data = internal global ptr null, align 8")
	e.emitGlobal("@__kml_task_len = internal global i64 0, align 8")
	e.emitGlobal("@__kml_task_cap = internal global i64 0, align 8")
	e.emitGlobal("@__kml_task_active = internal global i64 0, align 8")

	// GC-mode stack-bottom set (into a fiber's stack) / restore (to whatever the
	// swapper's stack was: the real process stack when on main, else the current
	// task's stack). Mirrors runtime_http.go's append_conn juggling.
	gcSetInto := func(stackReg string) string {
		if !e.isGCMode() {
			return ""
		}
		return fmt.Sprintf("\n  %%__gchigh = getelementptr i8, ptr %s, i64 %d\n  store ptr %%__gchigh, ptr @GC_stackbottom, align 8", stackReg, taskStackBytes)
	}
	gcRestoreAfterSwap := ""
	if e.isGCMode() {
		gcRestoreAfterSwap = "\n  call void @__kml_task_gc_restore()"
	}

	// @__kml_task_gc_restore: set GC_stackbottom back to the swapper's stack —
	// the real process stack if @__kml_current_task is null, else that task's
	// own stack high. Called right after a swapcontext returns control to a
	// swapper. No-op body when not in GC mode (never emitted then).
	if e.isGCMode() {
		e.emitGlobal(fmt.Sprintf(`
define void @__kml_task_gc_restore() {
entry:
  %%ct = load ptr, ptr @__kml_current_task, align 8
  %%onmain = icmp eq ptr %%ct, null
  br i1 %%onmain, label %%main, label %%fiber
main:
  %%orig = load ptr, ptr @__kml_gc_orig_stackbottom, align 8
  store ptr %%orig, ptr @GC_stackbottom, align 8
  ret void
fiber:
  %%stk_p = getelementptr %s, ptr %%ct, i32 0, i32 %d
  %%stk = load ptr, ptr %%stk_p, align 8
  %%high = getelementptr i8, ptr %%stk, i64 %d
  store ptr %%high, ptr @GC_stackbottom, align 8
  ret void
}`, taskStructIR, taskStack, taskStackBytes))
	}

	// @__kml_task_trampoline() : makecontext entry. Reads the just-launched task
	// and calls its body fn(args). The body resolves its own promise and swaps
	// out via @__kml_task_finish before returning, so control does not fall off
	// the end here; uc_link (= @__kml_main_ctx) is only a crash safety net.
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_task_trampoline() {
entry:
  %%t = load ptr, ptr @__kml_task_launching, align 8
  %%fn_p = getelementptr %s, ptr %%t, i32 0, i32 %d
  %%fn = load ptr, ptr %%fn_p, align 8
  %%args_p = getelementptr %s, ptr %%t, i32 0, i32 %d
  %%args = load ptr, ptr %%args_p, align 8
  ; task-level catch-all: a throw the body doesn't catch rejects the promise
  %%jb = call ptr @__kml_push_jmpbuf()
  %%sj = call i32 @setjmp(ptr %%jb)
  %%threw = icmp ne i32 %%sj, 0
  br i1 %%threw, label %%rejected, label %%run
run:
  call void %%fn(ptr %%args)
  call void @__kml_pop_jmpbuf()
  call void @__kml_task_finish(ptr %%t)
  ret void
rejected:
  %%err = load ptr, ptr @__kml_thrown, align 8
  call void @__kml_task_reject(ptr %%t, ptr %%err)
  ret void
}`, taskStructIR, taskFn, taskStructIR, taskArgs))

	// @__kml_task_reject(ptr %task, ptr %err): mark the task's promise rejected
	// (resolved = 2, v0 = error object), wake a parked waiter, and swap out — the
	// throw path counterpart of __kml_task_finish. Emit code re-throws at the
	// awaiter (emitAwait's task branch checks resolved == 2).
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_task_reject(ptr %%task, ptr %%err) {
entry:
  %%prom_p = getelementptr %s, ptr %%task, i32 0, i32 %d
  %%prom = load ptr, ptr %%prom_p, align 8
  %%v0_p = getelementptr %s, ptr %%prom, i32 0, i32 2
  %%errbits = ptrtoint ptr %%err to i64
  store i64 %%errbits, ptr %%v0_p, align 8
  %%res_p = getelementptr %s, ptr %%prom, i32 0, i32 0
  store i64 2, ptr %%res_p, align 8
  %%w_p = getelementptr %s, ptr %%prom, i32 0, i32 1
  %%w = load ptr, ptr %%w_p, align 8
  %%haswaiter = icmp ne ptr %%w, null
  br i1 %%haswaiter, label %%wake, label %%nowake
wake:
  %%wpp_p = getelementptr %s, ptr %%w, i32 0, i32 %d
  store ptr null, ptr %%wpp_p, align 8
  br label %%nowake
nowake:
  %%st_p = getelementptr %s, ptr %%task, i32 0, i32 %d
  store i64 2, ptr %%st_p, align 8
  %%act = load i64, ptr @__kml_task_active, align 8
  %%act1 = sub i64 %%act, 1
  store i64 %%act1, ptr @__kml_task_active, align 8
  call void @__kml_promise_drain_reactions(ptr %%prom)
  %%rc_p = getelementptr %s, ptr %%task, i32 0, i32 %d
  %%rc = load ptr, ptr %%rc_p, align 8
  %%ctx_p = getelementptr %s, ptr %%task, i32 0, i32 %d
  %%ctx = load ptr, ptr %%ctx_p, align 8
  %%r = call i32 @swapcontext(ptr %%ctx, ptr %%rc)
  ret void
}`, taskStructIR, taskPromiseSlot, promiseStructIR, promiseStructIR, promiseStructIR,
		taskStructIR, taskPendingProm, taskStructIR, taskState,
		taskStructIR, taskResumerCtx, taskStructIR, taskCtx))

	// @__kml_spawn_task(ptr %fn, ptr %args, ptr %promiseSlot) -> ptr : allocate a
	// task + fiber, run it up to its first suspend or to completion, and return
	// the task pointer. resumerCtx is set to the *caller's* context (main if not
	// on a task, else the current task's ctx) so the task returns here.
	e.emitGlobal(fmt.Sprintf(`
define ptr @__kml_spawn_task(ptr %%fn, ptr %%args, ptr %%promiseSlot) {
entry:
  %%t = call ptr @malloc(i64 %d)
  %%ctx = call ptr @malloc(i64 %d)
  %%stack = call ptr @malloc(i64 %d)
  call void @getcontext(ptr %%ctx)
  %%ss_sp_p = getelementptr i8, ptr %%ctx, i64 %d
  store ptr %%stack, ptr %%ss_sp_p, align 8
  %%ss_size_p = getelementptr i8, ptr %%ctx, i64 %d
  store i64 %d, ptr %%ss_size_p, align 8
  %%uc_link_p = getelementptr i8, ptr %%ctx, i64 %d
  store ptr @__kml_main_ctx, ptr %%uc_link_p, align 8
  call void (ptr, ptr, i32, ...) @makecontext(ptr %%ctx, ptr @__kml_task_trampoline, i32 0)

  %%ctx_p = getelementptr %s, ptr %%t, i32 0, i32 %d
  store ptr %%ctx, ptr %%ctx_p, align 8
  %%stack_p = getelementptr %s, ptr %%t, i32 0, i32 %d
  store ptr %%stack, ptr %%stack_p, align 8
  %%prom_p = getelementptr %s, ptr %%t, i32 0, i32 %d
  store ptr %%promiseSlot, ptr %%prom_p, align 8
  %%state_p = getelementptr %s, ptr %%t, i32 0, i32 %d
  store i64 0, ptr %%state_p, align 8
  %%pf_p = getelementptr %s, ptr %%t, i32 0, i32 %d
  store ptr null, ptr %%pf_p, align 8
  %%pg_p = getelementptr %s, ptr %%t, i32 0, i32 %d
  store ptr null, ptr %%pg_p, align 8
  %%pp_p = getelementptr %s, ptr %%t, i32 0, i32 %d
  store ptr null, ptr %%pp_p, align 8
  %%fn_p = getelementptr %s, ptr %%t, i32 0, i32 %d
  store ptr %%fn, ptr %%fn_p, align 8
  %%args_p = getelementptr %s, ptr %%t, i32 0, i32 %d
  store ptr %%args, ptr %%args_p, align 8
  %%jstk = call ptr @malloc(i64 8192)
  %%jstk_p = getelementptr { ptr, ptr, ptr, i64, ptr, ptr, ptr, ptr, ptr, ptr, ptr, i64 }, ptr %%t, i32 0, i32 10
  store ptr %%jstk, ptr %%jstk_p, align 8
  %%sjt_p = getelementptr { ptr, ptr, ptr, i64, ptr, ptr, ptr, ptr, ptr, ptr, ptr, i64 }, ptr %%t, i32 0, i32 11
  store i64 0, ptr %%sjt_p, align 8

  ; register in the task array (grow by realloc-doubling, 8 min)
  call void @__kml_task_register(ptr %%t)

  ; caller ctx = main if not on a task, else current task's ctx
  %%prev = load ptr, ptr @__kml_current_task, align 8
  %%onmain = icmp eq ptr %%prev, null
  br i1 %%onmain, label %%fromMain, label %%fromTask
fromMain:
  br label %%doswap
fromTask:
  %%prevctx_p = getelementptr %s, ptr %%prev, i32 0, i32 %d
  %%prevctx = load ptr, ptr %%prevctx_p, align 8
  br label %%doswap
doswap:
  %%callerctx = phi ptr [ @__kml_main_ctx, %%fromMain ], [ %%prevctx, %%fromTask ]
  %%res_p = getelementptr %s, ptr %%t, i32 0, i32 %d
  store ptr %%callerctx, ptr %%res_p, align 8
  %%callerStk = load ptr, ptr @__kml_cur_jmp_stk, align 8
  %%callerTop = load i32, ptr @__kml_jmp_top, align 4
  %%tjstk = load ptr, ptr %%jstk_p, align 8
  store ptr %%tjstk, ptr @__kml_cur_jmp_stk, align 8
  store i32 0, ptr @__kml_jmp_top, align 4
  store ptr %%t, ptr @__kml_current_task, align 8
  store ptr %%t, ptr @__kml_task_launching, align 8%s
  %%rc = call i32 @swapcontext(ptr %%callerctx, ptr %%ctx)
  store ptr %%prev, ptr @__kml_current_task, align 8%s
  store ptr %%callerStk, ptr @__kml_cur_jmp_stk, align 8
  store i32 %%callerTop, ptr @__kml_jmp_top, align 4
  ret ptr %%t
}`,
		taskStructBytes, ctxSize, taskStackBytes,
		ssSpOff, ssSizeOff, taskStackBytes, ucLinkOff,
		taskStructIR, taskCtx, taskStructIR, taskStack, taskStructIR, taskPromiseSlot,
		taskStructIR, taskState, taskStructIR, taskPendingFetch, taskStructIR, taskPendingGroup,
		taskStructIR, taskPendingProm, taskStructIR, taskFn, taskStructIR, taskArgs,
		taskStructIR, taskCtx,
		taskStructIR, taskResumerCtx,
		gcSetInto("%stack"), gcRestoreAfterSwap))

	// @__kml_task_register(ptr %t): append to the growable task array + bump active.
	e.emitGlobal(`
define void @__kml_task_register(ptr %t) {
entry:
  %len = load i64, ptr @__kml_task_len, align 8
  %cap = load i64, ptr @__kml_task_cap, align 8
  %needp1 = add i64 %len, 1
  %needgrow = icmp sgt i64 %needp1, %cap
  br i1 %needgrow, label %grow, label %app
grow:
  %data = load ptr, ptr @__kml_task_data, align 8
  %cap2 = mul i64 %cap, 2
  %ge8 = icmp sgt i64 %cap2, 8
  %newcap = select i1 %ge8, i64 %cap2, i64 8
  %bytes = mul i64 %newcap, 8
  %newdata = call ptr @realloc(ptr %data, i64 %bytes)
  store ptr %newdata, ptr @__kml_task_data, align 8
  store i64 %newcap, ptr @__kml_task_cap, align 8
  br label %app
app:
  %d = load ptr, ptr @__kml_task_data, align 8
  %slot = getelementptr ptr, ptr %d, i64 %len
  store ptr %t, ptr %slot, align 8
  %nl = add i64 %len, 1
  store i64 %nl, ptr @__kml_task_len, align 8
  %act = load i64, ptr @__kml_task_active, align 8
  %act1 = add i64 %act, 1
  store i64 %act1, ptr @__kml_task_active, align 8
  ret void
}`)

	// @__kml_task_finish(ptr %task): the body calls this after storing its result
	// into the promise's v0/v1. Marks the promise resolved, wakes a parked waiter
	// (clears its pendingPromise so the scheduler resumes it), marks the task
	// done + decrements active, then swaps back to whoever resumed it.
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_task_finish(ptr %%task) {
entry:
  %%prom_p = getelementptr %s, ptr %%task, i32 0, i32 %d
  %%prom = load ptr, ptr %%prom_p, align 8
  %%res_p = getelementptr %s, ptr %%prom, i32 0, i32 0
  store i64 1, ptr %%res_p, align 8
  %%w_p = getelementptr %s, ptr %%prom, i32 0, i32 1
  %%w = load ptr, ptr %%w_p, align 8
  %%haswaiter = icmp ne ptr %%w, null
  br i1 %%haswaiter, label %%wake, label %%nowake
wake:
  %%wpp_p = getelementptr %s, ptr %%w, i32 0, i32 %d
  store ptr null, ptr %%wpp_p, align 8
  br label %%nowake
nowake:
  %%st_p = getelementptr %s, ptr %%task, i32 0, i32 %d
  store i64 2, ptr %%st_p, align 8
  %%act = load i64, ptr @__kml_task_active, align 8
  %%act1 = sub i64 %%act, 1
  store i64 %%act1, ptr @__kml_task_active, align 8
  call void @__kml_promise_drain_reactions(ptr %%prom)
  %%rc_p = getelementptr %s, ptr %%task, i32 0, i32 %d
  %%rc = load ptr, ptr %%rc_p, align 8
  %%ctx_p = getelementptr %s, ptr %%task, i32 0, i32 %d
  %%ctx = load ptr, ptr %%ctx_p, align 8
  %%r = call i32 @swapcontext(ptr %%ctx, ptr %%rc)
  ret void
}`, taskStructIR, taskPromiseSlot, promiseStructIR, promiseStructIR,
		taskStructIR, taskPendingProm, taskStructIR, taskState,
		taskStructIR, taskResumerCtx, taskStructIR, taskCtx))

	// @__kml_task_sched_step(): one scheduler step — pump libcurl once, then scan
	// the task array resuming any suspended task whose park reason is now
	// satisfied (a done fetch, a resolved awaited-promise, or none = woken by a
	// finishing task). Resume = swapcontext main -> task, with resumerCtx = main.
	gcSetTaskStack := ""
	if e.isGCMode() {
		gcSetTaskStack = fmt.Sprintf("\n  %%dr_stk = load ptr, ptr %%dr_stk_p, align 8\n  %%dr_high = getelementptr i8, ptr %%dr_stk, i64 %d\n  store ptr %%dr_high, ptr @GC_stackbottom, align 8", taskStackBytes)
	}
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_task_sched_step() {
entry:
  %%multi = load ptr, ptr @__kml_curl_multi, align 8
  %%hasmulti = icmp ne ptr %%multi, null
  br i1 %%hasmulti, label %%pump, label %%scan
pump:
  %%run = alloca i32, align 4
  %%pr = call i32 @curl_multi_perform(ptr %%multi, ptr %%run)
  call void @__kml_curl_drain_messages()
  br label %%scan
scan:
  %%len = load i64, ptr @__kml_task_len, align 8
  %%data = load ptr, ptr @__kml_task_data, align 8
  br label %%cond
cond:
  %%i = phi i64 [ 0, %%scan ], [ %%inext, %%next ]
  %%go = icmp slt i64 %%i, %%len
  br i1 %%go, label %%body, label %%done
body:
  %%slotp = getelementptr ptr, ptr %%data, i64 %%i
  %%t = load ptr, ptr %%slotp, align 8
  %%st_p = getelementptr %s, ptr %%t, i32 0, i32 %d
  %%st = load i64, ptr %%st_p, align 8
  %%susp = icmp eq i64 %%st, 1
  br i1 %%susp, label %%chkpf, label %%next
chkpf:
  %%pf_p = getelementptr %s, ptr %%t, i32 0, i32 %d
  %%pf = load ptr, ptr %%pf_p, align 8
  %%haspf = icmp ne ptr %%pf, null
  br i1 %%haspf, label %%chkfetch, label %%chkpp
chkfetch:
  %%d_p = getelementptr { ptr, ptr, i64, i64, i64 }, ptr %%pf, i32 0, i32 2
  %%d = load i64, ptr %%d_p, align 8
  %%fdone = icmp ne i64 %%d, 0
  br i1 %%fdone, label %%resume, label %%next
chkpp:
  %%pp_p = getelementptr %s, ptr %%t, i32 0, i32 %d
  %%pp = load ptr, ptr %%pp_p, align 8
  %%haspp = icmp ne ptr %%pp, null
  br i1 %%haspp, label %%chkpres, label %%resume
chkpres:
  %%pres_p = getelementptr %s, ptr %%pp, i32 0, i32 0
  %%pres = load i64, ptr %%pres_p, align 8
  %%presok = icmp ne i64 %%pres, 0
  br i1 %%presok, label %%resume, label %%next
resume:
  %%r_pf_p = getelementptr %s, ptr %%t, i32 0, i32 %d
  store ptr null, ptr %%r_pf_p, align 8
  %%r_pp_p = getelementptr %s, ptr %%t, i32 0, i32 %d
  store ptr null, ptr %%r_pp_p, align 8
  %%r_st_p = getelementptr %s, ptr %%t, i32 0, i32 %d
  store i64 0, ptr %%r_st_p, align 8
  %%r_rc_p = getelementptr %s, ptr %%t, i32 0, i32 %d
  store ptr @__kml_main_ctx, ptr %%r_rc_p, align 8
  %%rj_mainstk = load ptr, ptr @__kml_cur_jmp_stk, align 8
  %%rj_maintop = load i32, ptr @__kml_jmp_top, align 4
  %%rj_tjstk_p = getelementptr { ptr, ptr, ptr, i64, ptr, ptr, ptr, ptr, ptr, ptr, ptr, i64 }, ptr %%t, i32 0, i32 10
  %%rj_tjstk = load ptr, ptr %%rj_tjstk_p, align 8
  store ptr %%rj_tjstk, ptr @__kml_cur_jmp_stk, align 8
  %%rj_sjt_p = getelementptr { ptr, ptr, ptr, i64, ptr, ptr, ptr, ptr, ptr, ptr, ptr, i64 }, ptr %%t, i32 0, i32 11
  %%rj_sjt = load i64, ptr %%rj_sjt_p, align 8
  %%rj_sjt32 = trunc i64 %%rj_sjt to i32
  store i32 %%rj_sjt32, ptr @__kml_jmp_top, align 4
  store ptr %%t, ptr @__kml_current_task, align 8
  %%dr_stk_p = getelementptr %s, ptr %%t, i32 0, i32 %d
  %%r_ctx_p = getelementptr %s, ptr %%t, i32 0, i32 %d
  %%r_ctx = load ptr, ptr %%r_ctx_p, align 8%s
  %%r_sw = call i32 @swapcontext(ptr @__kml_main_ctx, ptr %%r_ctx)
  store ptr null, ptr @__kml_current_task, align 8%s
  store ptr %%rj_mainstk, ptr @__kml_cur_jmp_stk, align 8
  store i32 %%rj_maintop, ptr @__kml_jmp_top, align 4
  br label %%next
next:
  %%inext = add i64 %%i, 1
  br label %%cond
done:
  ret void
}`, taskStructIR, taskState, taskStructIR, taskPendingFetch, taskStructIR, taskPendingProm,
		promiseStructIR, taskStructIR, taskPendingFetch, taskStructIR, taskPendingProm,
		taskStructIR, taskState, taskStructIR, taskResumerCtx, taskStructIR, taskStack,
		taskStructIR, taskCtx, gcSetTaskStack, gcRestoreAfterSwap))

	// @__kml_task_await_any_of(ptr %members, i64 %count) -> i64: wait until any
	// member task-promise resolves, returning its index (Promise.race/any over
	// task-promises). At top level it drives the scheduler; on a task it registers
	// itself as the waiter on every member and sets a non-null pendingProm sentinel
	// (members[0], known pending since the scan above found none settled), so the
	// scheduler resumes it only when a member settles — a settling promise clears
	// its waiter's pendingProm (__kml_task_finish/__kml_promise_settle) — instead of
	// busy-repolling every step. A stale waiter left after this returns can only
	// cause a harmless spurious wake (the resumed wait re-checks its condition and
	// re-parks).
	e.emitGlobal(fmt.Sprintf(`
define i64 @__kml_task_await_any_of(ptr %%members, i64 %%count) {
entry:
  br label %%scan
scan:
  br label %%cond
cond:
  %%i = phi i64 [ 0, %%scan ], [ %%inext, %%snext ]
  %%go = icmp slt i64 %%i, %%count
  br i1 %%go, label %%sbody, label %%sdone
sbody:
  %%mgep = getelementptr ptr, ptr %%members, i64 %%i
  %%m = load ptr, ptr %%mgep, align 8
  %%rp = getelementptr %s, ptr %%m, i32 0, i32 0
  %%r = load i64, ptr %%rp, align 8
  %%res = icmp ne i64 %%r, 0
  br i1 %%res, label %%found, label %%snext
snext:
  %%inext = add i64 %%i, 1
  br label %%cond
found:
  ret i64 %%i
sdone:
  %%ct = load ptr, ptr @__kml_current_task, align 8
  %%top = icmp eq ptr %%ct, null
  br i1 %%top, label %%drive, label %%park
drive:
  call void @__kml_task_sched_step()
  br label %%scan
park:
  %%m0gep = getelementptr ptr, ptr %%members, i64 0
  %%m0 = load ptr, ptr %%m0gep, align 8
  %%pp_p = getelementptr %s, ptr %%ct, i32 0, i32 %d
  store ptr %%m0, ptr %%pp_p, align 8
  br label %%wreg
wreg:
  %%wi = phi i64 [ 0, %%park ], [ %%winext, %%wregnext ]
  %%wgo = icmp slt i64 %%wi, %%count
  br i1 %%wgo, label %%wregbody, label %%wregdone
wregbody:
  %%wmgep = getelementptr ptr, ptr %%members, i64 %%wi
  %%wm = load ptr, ptr %%wmgep, align 8
  %%wwp = getelementptr %s, ptr %%wm, i32 0, i32 1
  store ptr %%ct, ptr %%wwp, align 8
  br label %%wregnext
wregnext:
  %%winext = add i64 %%wi, 1
  br label %%wreg
wregdone:
  %%st_p = getelementptr %s, ptr %%ct, i32 0, i32 %d
  store i64 1, ptr %%st_p, align 8
  %%rc_p = getelementptr %s, ptr %%ct, i32 0, i32 %d
  %%rc = load ptr, ptr %%rc_p, align 8
  %%ctx_p = getelementptr %s, ptr %%ct, i32 0, i32 %d
  %%ctx = load ptr, ptr %%ctx_p, align 8
  %%ao_sjt_p = getelementptr { ptr, ptr, ptr, i64, ptr, ptr, ptr, ptr, ptr, ptr, ptr, i64 }, ptr %%ct, i32 0, i32 11
  %%ao_top = load i32, ptr @__kml_jmp_top, align 4
  %%ao_top64 = zext i32 %%ao_top to i64
  store i64 %%ao_top64, ptr %%ao_sjt_p, align 8
  %%sw = call i32 @swapcontext(ptr %%ctx, ptr %%rc)%s
  br label %%scan
}`, promiseStructIR, taskStructIR, taskPendingProm, promiseStructIR, taskStructIR, taskState, taskStructIR, taskResumerCtx,
		taskStructIR, taskCtx, gcRestoreAfterSwap))

	// @__kml_task_await_first_fulfilled(ptr %members, i64 %count) -> i64: wait for
	// the first member task-promise to *fulfill* (resolved == 1), returning its
	// index (Promise.any — unlike await_any_of, which returns the first *settled*
	// member for .race). A rejected member (resolved == 2) is skipped. If every
	// member has settled and none fulfilled (all rejected), returns -1 so the
	// caller can throw an AggregateError. While any member is still pending it
	// drives (top level) or parks (on a task), same as await_any_of.
	e.emitGlobal(fmt.Sprintf(`
define i64 @__kml_task_await_first_fulfilled(ptr %%members, i64 %%count) {
entry:
  br label %%scan
scan:
  br label %%cond
cond:
  %%i = phi i64 [ 0, %%scan ], [ %%inext, %%snext ]
  %%pend = phi i1 [ false, %%scan ], [ %%pendnext, %%snext ]
  %%go = icmp slt i64 %%i, %%count
  br i1 %%go, label %%sbody, label %%sdone
sbody:
  %%mgep = getelementptr ptr, ptr %%members, i64 %%i
  %%m = load ptr, ptr %%mgep, align 8
  %%rp = getelementptr %s, ptr %%m, i32 0, i32 0
  %%r = load i64, ptr %%rp, align 8
  %%isful = icmp eq i64 %%r, 1
  br i1 %%isful, label %%found, label %%notful
notful:
  %%ispend = icmp eq i64 %%r, 0
  %%pendnext = or i1 %%pend, %%ispend
  br label %%snext
snext:
  %%inext = add i64 %%i, 1
  br label %%cond
found:
  ret i64 %%i
sdone:
  br i1 %%pend, label %%waitmore, label %%allrej
allrej:
  ret i64 -1
waitmore:
  %%ct = load ptr, ptr @__kml_current_task, align 8
  %%top = icmp eq ptr %%ct, null
  br i1 %%top, label %%drive, label %%park
drive:
  call void @__kml_task_sched_step()
  br label %%scan
park:
  %%m0gep = getelementptr ptr, ptr %%members, i64 0
  %%m0 = load ptr, ptr %%m0gep, align 8
  %%pp_p = getelementptr %s, ptr %%ct, i32 0, i32 %d
  store ptr %%m0, ptr %%pp_p, align 8
  br label %%wreg
wreg:
  %%wi = phi i64 [ 0, %%park ], [ %%winext, %%wregnext ]
  %%wgo = icmp slt i64 %%wi, %%count
  br i1 %%wgo, label %%wregbody, label %%wregdone
wregbody:
  %%wmgep = getelementptr ptr, ptr %%members, i64 %%wi
  %%wm = load ptr, ptr %%wmgep, align 8
  %%wwp = getelementptr %s, ptr %%wm, i32 0, i32 1
  store ptr %%ct, ptr %%wwp, align 8
  br label %%wregnext
wregnext:
  %%winext = add i64 %%wi, 1
  br label %%wreg
wregdone:
  %%st_p = getelementptr %s, ptr %%ct, i32 0, i32 %d
  store i64 1, ptr %%st_p, align 8
  %%rc_p = getelementptr %s, ptr %%ct, i32 0, i32 %d
  %%rc = load ptr, ptr %%rc_p, align 8
  %%ctx_p = getelementptr %s, ptr %%ct, i32 0, i32 %d
  %%ctx = load ptr, ptr %%ctx_p, align 8
  %%ao_sjt_p = getelementptr { ptr, ptr, ptr, i64, ptr, ptr, ptr, ptr, ptr, ptr, ptr, i64 }, ptr %%ct, i32 0, i32 11
  %%ao_top = load i32, ptr @__kml_jmp_top, align 4
  %%ao_top64 = zext i32 %%ao_top to i64
  store i64 %%ao_top64, ptr %%ao_sjt_p, align 8
  %%sw = call i32 @swapcontext(ptr %%ctx, ptr %%rc)%s
  br label %%scan
}`, promiseStructIR, taskStructIR, taskPendingProm, promiseStructIR, taskStructIR, taskState, taskStructIR, taskResumerCtx,
		taskStructIR, taskCtx, gcRestoreAfterSwap))

	// @__kml_task_run_all(): drive the scheduler until no task is still active —
	// the program-exit drain for a task program (a top-level async call that was
	// spawned but not awaited must still run to completion).
	e.emitGlobal(`
define void @__kml_task_run_all() {
entry:
  br label %loop
loop:
  call void @__kml_drain_microtasks()
  %act = load i64, ptr @__kml_task_active, align 8
  %any = icmp sgt i64 %act, 0
  br i1 %any, label %step, label %done
step:
  call void @__kml_task_sched_step()
  br label %loop
done:
  call void @__kml_drain_microtasks()
  ret void
}`)

	// @__kml_task_resume(ptr %t): resume a parked task from the microtask-drain
	// context (a task's `await` continuation IS a microtask — TDD-00088). Mirrors
	// the scheduler's own resume prologue but standalone, pivoting on @__kml_main_ctx
	// (microtasks drain from main): clear pendingProm, mark running, set resumerCtx
	// = main, swap in the task's jmpbuf stack + gc + current_task, swapcontext into
	// it; when it parks/finishes it swaps back here.
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_task_resume(ptr %%t) {
entry:
  %%pp = getelementptr %s, ptr %%t, i32 0, i32 6
  store ptr null, ptr %%pp, align 8
  %%st = getelementptr %s, ptr %%t, i32 0, i32 3
  store i64 0, ptr %%st, align 8
  %%rc = getelementptr %s, ptr %%t, i32 0, i32 9
  store ptr @__kml_main_ctx, ptr %%rc, align 8
  %%mstk = load ptr, ptr @__kml_cur_jmp_stk, align 8
  %%mtop = load i32, ptr @__kml_jmp_top, align 4
  %%tjstk_p = getelementptr %s, ptr %%t, i32 0, i32 10
  %%tjstk = load ptr, ptr %%tjstk_p, align 8
  store ptr %%tjstk, ptr @__kml_cur_jmp_stk, align 8
  %%sjt_p = getelementptr %s, ptr %%t, i32 0, i32 11
  %%sjt = load i64, ptr %%sjt_p, align 8
  %%sjt32 = trunc i64 %%sjt to i32
  store i32 %%sjt32, ptr @__kml_jmp_top, align 4
  store ptr %%t, ptr @__kml_current_task, align 8
  %%dr_stk_p = getelementptr %s, ptr %%t, i32 0, i32 1
  %%ctx_p = getelementptr %s, ptr %%t, i32 0, i32 0
  %%ctx = load ptr, ptr %%ctx_p, align 8%s
  %%sw = call i32 @swapcontext(ptr @__kml_main_ctx, ptr %%ctx)
  store ptr null, ptr @__kml_current_task, align 8%s
  store ptr %%mstk, ptr @__kml_cur_jmp_stk, align 8
  store i32 %%mtop, ptr @__kml_jmp_top, align 4
  ret void
}`, taskStructIR, taskStructIR, taskStructIR, taskStructIR, taskStructIR, taskStructIR, taskStructIR, gcSetTaskStack, gcRestoreAfterSwap))

	// @__kml_task_await_ready(ptr %promise): on a task, register a resume reaction
	// on the promise and park — so `await` always yields a microtask tick and the
	// resume is a microtask ordered in FIFO with `.then`/`queueMicrotask` (TDD-00088).
	// A settled promise enqueues the resume now; a pending one attaches it to the
	// promise's reaction list (drained when it settles). The task parks in state 3
	// ("parked on a reaction") which the scheduler ignores — only the resume
	// microtask wakes it. At top level (no current task) it can't park, so it drives
	// the microtask FIFO + scheduler + timers until settled. Emit code reads the
	// value/rejection afterward.
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_task_await_ready(ptr %%promise) {
entry:
  %%ct = load ptr, ptr @__kml_current_task, align 8
  %%topq = icmp eq ptr %%ct, null
  br i1 %%topq, label %%toploop, label %%ontask
toploop:
  %%tres_p = getelementptr %s, ptr %%promise, i32 0, i32 0
  %%tres = load i64, ptr %%tres_p, align 8
  %%tdone = icmp ne i64 %%tres, 0
  br i1 %%tdone, label %%ret, label %%tdrive
tdrive:
  call void @__kml_drain_microtasks()
  call void @__kml_task_sched_step()
  %%tf = call i1 @__kml_timer_fire_next()
  br label %%toploop
ontask:
  %%clo = call ptr @malloc(i64 16)
  %%clo_fp = getelementptr { ptr, ptr }, ptr %%clo, i32 0, i32 0
  store ptr @__kml_task_resume, ptr %%clo_fp, align 8
  %%clo_ep = getelementptr { ptr, ptr }, ptr %%clo, i32 0, i32 1
  store ptr %%ct, ptr %%clo_ep, align 8
  %%res_p = getelementptr %s, ptr %%promise, i32 0, i32 0
  %%res = load i64, ptr %%res_p, align 8
  %%settled = icmp ne i64 %%res, 0
  br i1 %%settled, label %%enqnow, label %%attach
enqnow:
  call void @__kml_microtask_enqueue(ptr %%clo)
  br label %%parkit
attach:
  %%node = call ptr @malloc(i64 16)
  %%rx_p = getelementptr %s, ptr %%promise, i32 0, i32 4
  %%oldhead = load ptr, ptr %%rx_p, align 8
  %%node_clo = getelementptr { ptr, ptr }, ptr %%node, i32 0, i32 0
  store ptr %%clo, ptr %%node_clo, align 8
  %%node_next = getelementptr { ptr, ptr }, ptr %%node, i32 0, i32 1
  store ptr %%oldhead, ptr %%node_next, align 8
  store ptr %%node, ptr %%rx_p, align 8
  br label %%parkit
parkit:
  %%st_p = getelementptr %s, ptr %%ct, i32 0, i32 3
  store i64 3, ptr %%st_p, align 8
  %%sjt_p = getelementptr %s, ptr %%ct, i32 0, i32 11
  %%curtop = load i32, ptr @__kml_jmp_top, align 4
  %%curtop64 = zext i32 %%curtop to i64
  store i64 %%curtop64, ptr %%sjt_p, align 8
  %%rc_p = getelementptr %s, ptr %%ct, i32 0, i32 9
  %%rc = load ptr, ptr %%rc_p, align 8
  %%ctx_p = getelementptr %s, ptr %%ct, i32 0, i32 0
  %%ctx = load ptr, ptr %%ctx_p, align 8
  %%sw = call i32 @swapcontext(ptr %%ctx, ptr %%rc)%s
  br label %%ret
ret:
  ret void
}`, promiseStructIR, promiseStructIR, promiseStructIR, taskStructIR, taskStructIR, taskStructIR, taskStructIR, gcRestoreAfterSwap))
}
