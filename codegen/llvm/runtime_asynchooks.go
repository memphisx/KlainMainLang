// runtime_asynchooks.go — the AsyncLocalStorage runtime (TDD-00168).
//
// "The current async context" is the frame lib/node/async_hooks.ts keeps (a
// Map from each AsyncLocalStorage to its store), as an `any` word. It lives
// per task — in the coroutine task struct's field 12 (taskAsyncCtx) when a task is
// running — else in the top-level global @__kml_root_async_ctx. Because a
// parked task's struct persists and the scheduler restores @__kml_current_task
// to it before resuming, context read after an `await` is automatically the
// same task's head: cross-await propagation needs no per-resume juggling.
// __kml_spawn_task copies the spawner's head into the child (runtime_task.go),
// so a store set with als.run(...) before an async call propagates into it.
package llvm

import "fmt"

// ensureAsyncCtxAccessors emits the context head get/set accessors plus the
// timer-callback context-binding trampoline (TDD-00168 Stage 3) — the subset
// the timer runtime needs to propagate context across a schedule→fire boundary,
// Called by ensureNativeAsyncContext and the timer-wrapping path.
func (e *Emitter) ensureAsyncCtxAccessors() {
	if e.usedAsyncCtxAccessors {
		return
	}
	e.usedAsyncCtxAccessors = true
	e.ensureMalloc()
	e.ensureCurrentTaskGlobal() // @__kml_current_task + @__kml_root_async_ctx

	// __kml_als_ctx_get() -> ptr : the current context-frame head — the running
	// task's field 12, or the top-level root when no task is running.
	e.emitGlobal(fmt.Sprintf(`
define ptr @__kml_als_ctx_get() {
entry:
  %%t = load ptr, ptr @__kml_current_task, align 8
  %%isnull = icmp eq ptr %%t, null
  br i1 %%isnull, label %%root, label %%task
task:
  %%fp = getelementptr %s, ptr %%t, i32 0, i32 %d
  %%h = load ptr, ptr %%fp, align 8
  ret ptr %%h
root:
  %%r = load ptr, ptr @__kml_root_async_ctx, align 8
  ret ptr %%r
}`, taskStructIR, taskAsyncCtx))

	// __kml_als_ctx_set(ptr head) : write the current head (task field 12 or root).
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_als_ctx_set(ptr %%head) {
entry:
  %%t = load ptr, ptr @__kml_current_task, align 8
  %%isnull = icmp eq ptr %%t, null
  br i1 %%isnull, label %%root, label %%task
task:
  %%fp = getelementptr %s, ptr %%t, i32 0, i32 %d
  store ptr %%head, ptr %%fp, align 8
  ret void
root:
  store ptr %%head, ptr @__kml_root_async_ctx, align 8
  ret void
}`, taskStructIR, taskAsyncCtx))

	// __kml_als_timer_tramp(ptr env) : the timer-callback context-binding
	// trampoline (TDD-00168 Stage 3). env is a { ptr origClosure, ptr capturedCtx }
	// record built at schedule time; on fire it installs the captured context,
	// invokes the user's closure ({ptr fn, ptr env}), then restores. Because a
	// timer fires from the top-level event loop (no running task), install/restore
	// operate on the root context — so a `setTimeout` scheduled inside an
	// `als.run(...)` still sees the store when it fires.
	e.emitGlobal(`
define void @__kml_als_timer_tramp(ptr %env) {
entry:
  %oc = load ptr, ptr %env, align 8
  %cc_p = getelementptr { ptr, ptr }, ptr %env, i32 0, i32 1
  %cc = load ptr, ptr %cc_p, align 8
  %saved = call ptr @__kml_als_ctx_get()
  call void @__kml_als_ctx_set(ptr %cc)
  %fp = load ptr, ptr %oc, align 8
  %ep_p = getelementptr { ptr, ptr }, ptr %oc, i32 0, i32 1
  %ep = load ptr, ptr %ep_p, align 8
  call void (ptr) %fp(ptr %ep)
  call void @__kml_als_ctx_set(ptr %saved)
  ret void
}`)
}

// ensureNativeAsyncContext defines lib/native.d.ts's asyncContextGet and
// asyncContextSet: the current frame (the Map lib/node/async_hooks.ts keeps)
// as an `any` word in the context slot, undefined while there is none.
func (e *Emitter) ensureNativeAsyncContext() {
	if e.fnDecls["__kml_native_async_context_get"] {
		return
	}
	e.fnDecls["__kml_native_async_context_get"] = true
	e.ensureAsyncCtxAccessors()
	e.emitGlobal(fmt.Sprintf(`
define i64 @__kml_native_async_context_get() {
entry:
  %%h = call ptr @__kml_als_ctx_get()
  %%none = icmp eq ptr %%h, null
  %%w = ptrtoint ptr %%h to i64
  %%r = select i1 %%none, i64 %[1]d, i64 %%w
  ret i64 %%r
}
define void @__kml_native_async_context_set(i64 %%frame) {
entry:
  %%none = icmp eq i64 %%frame, %[1]d
  %%p = inttoptr i64 %%frame to ptr
  %%h = select i1 %%none, ptr null, ptr %%p
  call void @__kml_als_ctx_set(ptr %%h)
  ret void
}`, nbUndefined))
}
