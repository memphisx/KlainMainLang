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
)

//go:embed asynchookssrc/asynchooks.c
var asyncHooksSource string

// AsyncHooksSource is the AsyncLocalStorage runtime's C source, behind
// kml_layout.h.
func AsyncHooksSource() string { return layoutHeader() + asyncHooksSource }
