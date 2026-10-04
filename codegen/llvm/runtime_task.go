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

import _ "embed"

//go:embed tasksrc/task.c
var taskSource string

//go:embed tasksrc/promise.c
var promiseSource string

// task struct field indices (LLVM `{ ptr, ptr, ptr, i64, ptr, ptr, ptr, ptr, ptr, ptr }`).
const (
	taskCtx          = 0
	taskStack        = 1
	taskPromiseSlot  = 2
	taskState        = 3
	taskPendingFetch = 4
	taskPendingGroup = 5
	taskPendingProm  = 6
	taskFn           = 7
	taskArgs         = 8
	taskResumerCtx   = 9
	taskJmpStk       = 10 // this task's own jmpbuf stack (fiber-safe exceptions)
	taskSavedJmpTop  = 11 // jmp_top saved across suspension
	taskAsyncCtx     = 12 // AsyncLocalStorage context-frame head (TDD-00168) — inherited at spawn, survives await with the task
	taskStackSize    = 13 // bytes in this task's stack — the module task's is far larger than taskStackBytes (TDD-00224)
	taskStructBytes  = 112
	taskStructIR     = "{ ptr, ptr, ptr, i64, ptr, ptr, ptr, ptr, ptr, ptr, ptr, i64, ptr, i64 }"
	// promise resolved: 0 = pending, 1 = fulfilled (v0/v1 hold the value), 2 =
	// rejected (v0 holds the error object pointer's bits) — TDD-00083 Stage 2.
	// Field 4 (reactions) is the head of a { ptr closure, ptr next } list of
	// .then/.catch/.finally reactions, enqueued as microtasks when it settles.
	// Field 5 (promiseBoxedSlot) caches the promise's box wrapper once it has
	// been held in a dynamic value (emit_anyprom.go).
	promiseStructIR   = "{ i64, ptr, i64, i64, ptr, ptr, i64 }"
	promiseStructSize = 56
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
	e.ensureUnhandledRejections()
	e.ensureExceptionHelpers() // setjmp / __kml_throw / __kml_get_thrown for the catch-and-settle wrapper
	e.ensureMicrotasks()       // .then/.catch/.finally reactions + __kml_promise_drain_reactions
	// promise.c: a fresh pending promise, and the scheduler-free scan for
	// Promise.any over already-settled task promises.
	e.emitGlobal(`declare ptr @__kml_task_alloc_promise()
declare i64 @__kml_promise_first_fulfilled(ptr, i64)`)
}

// emitLoopTaskStubs emits no-op definitions for the symbols __kml_event_loop_run
// references so it can drive coroutine tasks (TDD-00084 Part B) —
// __kml_task_active / __kml_task_sched_step / __kml_drain_microtasks — for a
// program that uses the event loop (SSE/WS/http.listen) but not the task runtime
// and/or not microtasks. When the real runtimes are present their definitions are
// used and the corresponding stub is skipped. Called once at program finalization.
func (e *Emitter) emitLoopTaskStubs() {
	// A top-level await's tick (@__kml_microtask_tick): microtask.c's.
	if e.needMicrotaskTick {
		e.ensureMicrotasks()
	}
	// The scheduler asks @__kml_group_satisfied about a task parked on a fetch
	// group; without the combinator runtime no task ever parks on one.
	if e.usedTaskRuntime && !e.usedPromiseCombinators {
		e.emitGlobal("define i1 @__kml_group_satisfied(ptr %g) {\n  ret i1 1\n}")
	}
	// TDD-00142 Stage 3: the webview page-tick pump (@__kml_wv_pump) calls
	// @__kml_timer_tick / @__kml_task_sched_step / @__kml_drain_microtasks
	// unconditionally. When the corresponding runtime is absent, a no-op stub
	// stands in — but only for symbols the HTTP path below does not already
	// stub (guarded on !usedHTTP), so no symbol is defined twice.
	if e.usedWebview {
		if !e.usedTimers {
			e.emitGlobal("define void @__kml_timer_tick() {\nentry:\n  ret void\n}")
		}
		if !e.usedTaskRuntime && !e.usedHTTP {
			e.emitGlobal("define void @__kml_task_sched_step() {\nentry:\n  ret void\n}")
		}
		if !e.usedHTTP {
			e.ensureMicrotasks()
		}
	}
	// Streaming-request-body pump stubs — referenced by both the event loop
	// and the task scheduler's step, so emitted whenever either exists.
	if (e.usedHTTP || e.usedTaskRuntime) && !e.usedReqBodyRuntime {
		e.emitGlobal("define void @__kml_reqbody_pump() {\nentry:\n  ret void\n}")
		e.emitGlobal("define i1 @__kml_reqbody_want() {\nentry:\n  ret i1 0\n}")
		// ADR-00986: task_await_ready's fiber-yield safety gate; no reqbody
		// streaming exists in this program, so nothing is ever active on any fd.
		e.emitGlobal("define i1 @__kml_reqbody_active_on_fd(i64 %fd) {\nentry:\n  ret i1 0\n}")
	}
	if !e.usedHTTP {
		return
	}
	if !e.usedTaskRuntime {
		e.emitGlobal("@__kml_task_active = internal thread_local global i64 0, align 8")
		e.emitGlobal("define void @__kml_task_sched_step() {\nentry:\n  ret void\n}")
		e.emitGlobal("define i1 @__kml_task_resumable() {\nentry:\n  ret i1 0\n}")
		e.emitGlobal("define i1 @__kml_task_holds_loop() {\nentry:\n  ret i1 0\n}")
	}
	e.ensureMicrotasks()
	// TDD-00098: worker hooks the event loop references unconditionally.
	if !e.usedWorkerRuntime {
		e.emitGlobal("define i1 @__kml_worker_keepalive() {\nentry:\n  ret i1 0\n}")
		e.emitGlobal("define i1 @__kml_worker_fdset_add(ptr %fdset, ptr %maxfd) {\nentry:\n  ret i1 0\n}")
		e.emitGlobal("define void @__kml_worker_dispatch() {\nentry:\n  ret void\n}")
	}
	// child_process hooks the event loop references unconditionally.
	if !e.usedChildProcRuntime {
		e.emitGlobal("define i1 @__kml_cp_keepalive() {\nentry:\n  ret i1 0\n}")
		e.emitGlobal("define i1 @__kml_cp_fdset_add(ptr %fdset, ptr %maxfd) {\nentry:\n  ret i1 0\n}")
		e.emitGlobal("define void @__kml_cp_dispatch() {\nentry:\n  ret void\n}")
		e.emitGlobal("define i64 @__kml_cp_next_timeout_ns() {\nentry:\n  ret i64 0\n}")
	}
	// dynamic-import island hooks likewise (TDD-00225).
	if !e.usedDynImportWatch {
		e.emitGlobal("define i1 @__kml_dynimport_keepalive() {\nentry:\n  ret i1 0\n}")
		e.emitGlobal("define void @__kml_dynimport_dispatch() {\nentry:\n  ret void\n}")
		e.emitGlobal("define i64 @__kml_dynimport_next_deadline_ns() {\nentry:\n  ret i64 0\n}")
	}
	// fs.watch hooks likewise (TDD-00181).
	if !e.usedFsWatchRuntime {
		e.emitGlobal("define i1 @__kml_fswatch_keepalive() {\nentry:\n  ret i1 0\n}")
		e.emitGlobal("define i1 @__kml_fswatch_fdset_add(ptr %fdset, ptr %maxfd) {\nentry:\n  ret i1 0\n}")
		e.emitGlobal("define void @__kml_fswatch_dispatch() {\nentry:\n  ret void\n}")
	}
	// TDD-00185: async-I/O thread-pool hooks. When the pool is used, klainpool.c
	// defines these three; otherwise they are no-ops, like every hook above.
	if !e.usedThreadPool {
		e.emitGlobal("define i1 @__kml_pool_keepalive() {\nentry:\n  ret i1 0\n}")
		e.emitGlobal("define i1 @__kml_pool_fdset_add(ptr %fdset, ptr %maxfd) {\nentry:\n  ret i1 0\n}")
		e.emitGlobal("define zeroext i1 @__kml_pool_dispatch() {\nentry:\n  ret i1 0\n}")
		e.emitGlobal("define i1 @__kml_tcp_keepalive() {\nentry:\n  ret i1 0\n}")
		e.emitGlobal("define i1 @__kml_tcp_fdset_add(ptr %fdset, ptr %wfdset, ptr %maxfd) {\nentry:\n  ret i1 0\n}")
		e.emitGlobal("define zeroext i1 @__kml_tcp_dispatch() {\nentry:\n  ret i1 0\n}")
	}
	// net (TCP server) hooks likewise.
	if !e.usedNetRuntime {
		e.emitGlobal("define i1 @__kml_net_keepalive() {\nentry:\n  ret i1 0\n}")
		e.emitGlobal("define i1 @__kml_net_fdset_add(ptr %fdset, ptr %maxfd) {\nentry:\n  ret i1 0\n}")
		e.emitGlobal("define void @__kml_net_dispatch() {\nentry:\n  ret void\n}")
	}
}

func (e *Emitter) ensureTaskRuntime() {
	if e.usedTaskRuntime {
		return
	}
	e.usedTaskRuntime = true
	e.ensurePromiseRuntime() // promise struct + __kml_task_alloc_promise (+ microtasks/exceptions)
	e.ensureMalloc()
	e.ensureFree()
	e.ensureFiberRuntime()       // getcontext/makecontext/swapcontext + @__kml_main_ctx
	e.ensureCurrentTaskGlobal()  // @__kml_current_task
	e.ensureConnPokeGlobal()     // task completion pokes parked connection fibers
	e.ensureExceptionHelpers()   // @__kml_cur_jmp_stk / setjmp / __kml_throw (task rejection)
	e.usedAwaitTimerDrive = true // __kml_task_await_ready's top-level drive fires timers (TDD-00088)
	e.ensureMicrotasks()         // .then/.catch/.finally reactions drain here
	// The scheduler pumps libcurl; a may-suspend program always uses fetch.
	e.ensureFetchAsync()
	// Both waits on members and task_await_ready's top-level branch take turns
	// of the real loop.
	e.noteLoopTurn()
	if e.isGCMode() {
		e.emitGlobal("declare ptr @__kml_task_stack_alloc(i64)")
		e.emitGlobal("declare void @__kml_task_stack_free(ptr, i64)")
		e.emitGlobal("declare void @__kml_task_gc_restore()")
	}
	if e.opts.Target.OS() == "windows" {
		e.emitGlobal("declare void @__kml_ctx_release(ptr)")
	}
	// task.c: the scheduler, its task array and the waits.
	e.emitGlobal(`@__kml_task_launching = external thread_local global ptr, align 8
@__kml_task_active = external thread_local global i64, align 8
declare void @__kml_task_trampoline()
declare void @__kml_task_reject(ptr, ptr)
declare ptr @__kml_spawn_task(ptr, ptr, ptr)
declare ptr @__kml_spawn_task_ex(ptr, ptr, ptr, i64, ptr)
declare void @__kml_task_register(ptr)
declare void @__kml_task_reclaim(ptr)
declare void @__kml_task_compact()
declare void @__kml_task_finish(ptr)
declare void @__kml_task_sched_step()
declare zeroext i1 @__kml_task_resumable()
declare zeroext i1 @__kml_task_holds_loop()
declare i64 @__kml_task_await_any_of(ptr, i64)
declare i64 @__kml_task_await_first_fulfilled(ptr, i64)
declare void @__kml_task_unwait(ptr, i64, ptr)
declare void @__kml_task_run_all()
declare void @__kml_task_resume(ptr)
declare void @__kml_task_await_ready(ptr)`)
}

// TaskSource is the task runtime's C source, behind kml_layout.h.
func TaskSource() string { return layoutHeader() + taskSource }

// PromiseSource is the promise allocator's C source, behind kml_layout.h.
func PromiseSource() string { return layoutHeader() + promiseSource }

// TaskCFlags are task.c's mode defines: the GC stack bottom is set three ways.
func (e *Emitter) TaskCFlags() []string {
	if !e.isGCMode() {
		return nil
	}
	if e.hasWorkers {
		return []string{"-DKLAIN_GC=1", "-DKLAIN_GC_WORKERS=1"}
	}
	return []string{"-DKLAIN_GC=1"}
}
