// runtime_asynchooks.go — the AsyncLocalStorage runtime (TDD-00168).
//
// "The current async context" is the head of an immutable, prepend-only
// singly-linked list of { i64 alsId, i64 value, ptr next } frames (value is a
// NaN-boxed `any` word, so any store type fits one slot). The head lives per
// task — in the coroutine task struct's field 12 (taskAsyncCtx) when a task is
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
// without pulling in the full AsyncLocalStorage instance surface. Called by both
// ensureAsyncLocalStorageRuntime and the timer-wrapping path.
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

// ensureAsyncLocalStorageRuntime emits the full AsyncLocalStorage instance
// surface — the context accessors (via ensureAsyncCtxAccessors), the per-instance
// record allocator, and the frame push/lookup ops — exactly once.
func (e *Emitter) ensureAsyncLocalStorageRuntime() {
	if e.usedAsyncLocalStorage {
		return
	}
	e.usedAsyncLocalStorage = true
	e.ensureAsyncCtxAccessors()

	// A fresh AsyncLocalStorage instance id (monotonic; 0 is never handed out).
	e.emitGlobal("@__kml_als_next_id = internal global i64 0, align 8")

	// __kml_als_new_record() -> ptr : a fresh { i64 id, i64 disabled } instance.
	e.emitGlobal(`
define ptr @__kml_als_new_record() {
entry:
  %r = call ptr @malloc(i64 16)
  %old = load i64, ptr @__kml_als_next_id, align 8
  %id = add i64 %old, 1
  store i64 %id, ptr @__kml_als_next_id, align 8
  store i64 %id, ptr %r, align 8
  %dp = getelementptr { i64, i64 }, ptr %r, i32 0, i32 1
  store i64 0, ptr %dp, align 8
  ret ptr %r
}`)

	// __kml_als_push(i64 alsId, i64 val) -> ptr : prepend a frame, return the
	// previous head (for run/exit to restore on unwind).
	e.emitGlobal(`
define ptr @__kml_als_push(i64 %alsId, i64 %val) {
entry:
  %old = call ptr @__kml_als_ctx_get()
  %n = call ptr @malloc(i64 24)
  store i64 %alsId, ptr %n, align 8
  %vp = getelementptr { i64, i64, ptr }, ptr %n, i32 0, i32 1
  store i64 %val, ptr %vp, align 8
  %np = getelementptr { i64, i64, ptr }, ptr %n, i32 0, i32 2
  store ptr %old, ptr %np, align 8
  call void @__kml_als_ctx_set(ptr %n)
  ret ptr %old
}`)

	// __kml_als_lookup(i64 alsId) -> i64 : the value of the nearest frame for
	// alsId, or the NaN-boxed `undefined` word when there is none.
	e.emitGlobal(fmt.Sprintf(`
define i64 @__kml_als_lookup(i64 %%alsId) {
entry:
  %%h0 = call ptr @__kml_als_ctx_get()
  br label %%loop
loop:
  %%h = phi ptr [ %%h0, %%entry ], [ %%next, %%cont ]
  %%end = icmp eq ptr %%h, null
  br i1 %%end, label %%none, label %%body
body:
  %%idp = load i64, ptr %%h, align 8
  %%match = icmp eq i64 %%idp, %%alsId
  br i1 %%match, label %%hit, label %%cont
hit:
  %%vp = getelementptr { i64, i64, ptr }, ptr %%h, i32 0, i32 1
  %%v = load i64, ptr %%vp, align 8
  ret i64 %%v
cont:
  %%np = getelementptr { i64, i64, ptr }, ptr %%h, i32 0, i32 2
  %%next = load ptr, ptr %%np, align 8
  br label %%loop
none:
  ret i64 %d
}`, nbUndefined))
}
