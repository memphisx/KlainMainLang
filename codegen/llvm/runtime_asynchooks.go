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
// The routines live in asynchookssrc/asynchooks.c (TDD-00240).
package llvm

import (
	_ "embed"
	"strconv"
)

//go:embed asynchookssrc/asynchooks.c
var asyncHooksSource string

// AsyncHooksSource is the AsyncLocalStorage runtime's C source, behind
// kml_layout.h.
func AsyncHooksSource() string { return layoutHeader() + asyncHooksSource }

// UsesAsyncHooks reports whether the program links asynchooks.c.
func (e *Emitter) UsesAsyncHooks() bool { return e.usedAsyncCtxAccessors }

// AsyncHooksCFlags passes the NaN-box undefined word.
func (e *Emitter) AsyncHooksCFlags() []string {
	return []string{"-DKML_NB_UNDEFINED=" + strconv.Itoa(nbUndefined) + "LL"}
}

// ensureAsyncCtxAccessors declares the context head get/set accessors plus the
// timer-callback context-binding trampoline (TDD-00168 Stage 3) — the subset
// the timer runtime needs to propagate context across a schedule→fire boundary,
// Called by ensureNativeAsyncContext and the timer-wrapping path.
func (e *Emitter) ensureAsyncCtxAccessors() {
	if e.usedAsyncCtxAccessors {
		return
	}
	e.usedAsyncCtxAccessors = true
	e.ensureCurrentTaskGlobal() // @__kml_current_task + @__kml_root_async_ctx
	e.emitGlobal(`declare ptr @__kml_als_ctx_get()
declare void @__kml_als_ctx_set(ptr)
declare void @__kml_als_timer_tramp(ptr)`)
}

// ensureNativeAsyncContext declares lib/native.d.ts's asyncContextGet and
// asyncContextSet: the current frame (the Map lib/node/async_hooks.ts keeps)
// as an `any` word in the context slot, undefined while there is none.
func (e *Emitter) ensureNativeAsyncContext() {
	if e.fnDecls["__kml_native_async_context_get"] {
		return
	}
	e.fnDecls["__kml_native_async_context_get"] = true
	e.ensureAsyncCtxAccessors()
	e.emitGlobal(`declare i64 @__kml_native_async_context_get()
declare void @__kml_native_async_context_set(i64)`)
}
