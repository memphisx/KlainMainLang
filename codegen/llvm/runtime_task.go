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
	taskFrameBase    = 14 // its fiber's base frame (frames.go): an awaiter's frame for a stack's async part
	taskStructBytes  = 120
	taskStructIR     = "{ " + irPtr + ", " + irPtr + ", " + irPtr + ", i64, " + irPtr + ", " + irPtr + ", " + irPtr + ", " + irPtr + ", " + irPtr + ", " + irPtr + ", " + irPtr + ", i64, " + irPtr + ", i64, " + irPtr + " }"
	// promise resolved: 0 = pending, 1 = fulfilled (v0/v1 hold the value), 2 =
	// rejected (v0 holds the error object pointer's bits) — TDD-00083 Stage 2.
	// Field 4 (reactions) is the head of a { ptr closure, ptr next } list of
	// .then/.catch/.finally reactions, enqueued as microtasks when it settles.
	// Field 5 (promiseBoxedSlot) caches the promise's box wrapper once it has
	// been held in a dynamic value (emit_anyprom.go). Field 7 (TWIN) links a
	// typed promise and its Promise<any> view, one object in JS, in a ring:
	// a handler on either is a handler on both.
	promiseStructIR = "{ i64, " + irPtr + ", i64, i64, " + irPtr + ", " + irPtr + ", i64, " + irPtr + " }"
	// promiseHeaderWord is the word before a promise's body: the header of
	// a host value naming no class (TDD-00247).
	promiseHeaderWord = kmlHdrMagic | hostTypeIDFlag | hostInlineFlag
	promiseStructSize = 64
	// async-task fiber stacks are far shallower than the HTTP path's 1 MiB
	// (most async bodies are a handful of frames); 256 KiB keeps memory per
	// in-flight async call modest while leaving generous headroom.
	taskStackBytes = 256 * 1024
)

// emitLoopTaskStubs emits no-op definitions for the symbols __kml_event_loop_run
// references so it can drive coroutine tasks (TDD-00084 Part B) —
// __kml_task_active / __kml_task_sched_step / __kml_drain_microtasks — for a
// program that uses the event loop but not the task runtime
// and/or not microtasks. When the real runtimes are present their definitions are
// used and the corresponding stub is skipped. Called once at program finalization.
func (e *Emitter) emitLoopTaskStubs() {
	// timers.c's handles and fetch.c's sockets are the event loop's (loop.c,
	// on libuv), whether or not the program runs it: linked first, so the
	// stubs below cover it.
	if (e.UsesFetch() || e.UsesTimers()) && !e.usedEventLoop {
		e.ensureEventLoop()
	}
	// A top-level await's tick (@__kml_microtask_tick): microtask.c's.
	if e.needMicrotaskTick {
		e.ensureMicrotasks()
	}
	if !e.usedEventLoop {
		return
	}
	if !e.usedTaskRuntime {
		e.emitGlobal("@__kml_task_active = internal thread_local" + irGlobalAS + " global i64 0, align 8")
	}
	e.ensureMicrotasks()
}

func (e *Emitter) ensureTaskRuntime() {
	if e.usedTaskRuntime {
		return
	}
	e.usedTaskRuntime = true
	e.ensureMicrotasks()
	e.ensureExceptionHelpers() // @__kml_cur_jmp_stk / setjmp / __kml_throw (task rejection)
	e.ensureUnhandledRejections()
	// The scheduler pumps libcurl when the program fetches (fetch.c);
	// otherwise its step is hooks.c's no-op. Both waits on members and
	// task_await_ready's top-level branch take turns of the real loop.
	e.noteLoopTurn()
}

// TaskSource is the task runtime's C source, behind kml_layout.h.
func TaskSource() string { return layoutHeader() + taskSource }

// PromiseSource is the promise allocator's C source, behind kml_layout.h.
func PromiseSource() string { return layoutHeader() + promiseSource }
