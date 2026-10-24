// runtime_worker.go — the thread side of Worker threads: the thread main a
// Worker runs (klainpool.c's workerSpawn starts it), the thread-aware exit
// that process.exit, an uncaught error and terminate() take on a worker. The Worker, MessagePort and
// BroadcastChannel surface is lib/node/worker_threads.ts.
package llvm

import (
	_ "embed"
	"fmt"
)

// gcSBStore returns the IR statement that repoints the CURRENT thread's GC
// stack bottom at val, a fiber's own stack, through the GC interface
// (gcshim.c), which knows whether other threads run and, on Windows, reads the
// stack actually running from the TEB instead.
func (e *Emitter) gcSBStore(val string) string {
	e.ensureGCInterface()
	return fmt.Sprintf("call void @kml_gc_set_stack_bottom(ptr %s)", val)
}

// gcSBLoad returns the IR statement that reads the CURRENT thread's GC stack
// bottom into reg — the value a later gcSBStore(reg) puts back.
func (e *Emitter) gcSBLoad(reg string) string {
	e.ensureGCInterface()
	return fmt.Sprintf("%s = call ptr @kml_gc_stack_bottom()", reg)
}

// ensureGCInterface declares the GC interface entry points codegen calls
// (gcshim.c, linked under -mm=gc): codegen never names the collector.
func (e *Emitter) ensureGCInterface() {
	if e.usedGCInterface {
		return
	}
	e.usedGCInterface = true
	e.emitGlobal(`declare void @kml_gc_main_started(i32)
declare void @kml_gc_set_stack_bottom(ptr)
declare ptr @kml_gc_stack_bottom()
declare ptr @kml_gc_alloc_pinned(i64)
declare void @kml_gc_collect()
declare i32 @kml_gc_run_finalizers()`)
}

//go:embed workersrc/worker.c
var workerSource string

// WorkerSource is the Worker thread runtime's C source, behind kml_layout.h.
func WorkerSource() string { return layoutHeader() + workerSource }

// ensureWorkerRuntime declares the Worker thread runtime (workersrc/worker.c)
// once and emits the generated @__kml_worker_uncaught.
func (e *Emitter) ensureWorkerRuntime() {
	if e.usedWorkerRuntime {
		return
	}
	e.usedWorkerRuntime = true
	e.ensureExit()
	e.ensureTaskRuntime()
	// klainpool.c's worker natives, and worker.c's own entry points.
	e.emitGlobal(`declare void @__kml_worker_register(ptr, ptr, ptr)
declare ptr @__kml_worker_enter(ptr)
declare void @__kml_worker_leave(i64)
declare zeroext i1 @__kml_worker_is_thread()
declare ptr @__kml_worker_thread(ptr)
declare void @__kml_worker_end_thread()
declare void @__kml_thread_exit(i32)
declare void @__kml_worker_abort_check()`)

	// @__kml_worker_uncaught(tag, payload): an uncaught error on a worker
	// thread goes to the parent's 'error' listener (through the hook
	// worker_threads.ts registers, which posts it on the internal port) and
	// ends the thread with code 1. On the main thread it returns.
	restore := e.beginDetachedFunc()
	box := e.emitCaughtToAny(Value{Ref: "%rec", Ty: TypeCaught})
	e.emitInstr(fmt.Sprintf("call i1 @__kml_process_hook_call(i32 3, i64 %s, i64 %d)", box.Ref, nbUndefined))
	e.emitInstr("call void @__kml_thread_exit(i32 1)")
	e.emitTerminator("unreachable")
	body := e.allocas.String() + e.body.String()
	restore()
	e.ensureProcessHooks()
	e.functions.WriteString(fmt.Sprintf(`
define void @__kml_worker_uncaught(i8 %%tag, i64 %%pay) {
entry:
  %%w = call zeroext i1 @__kml_worker_is_thread()
  br i1 %%w, label %%worker, label %%main
main:
  ret void
worker:
  %%rec0 = insertvalue { i8, i64 } undef, i8 %%tag, 0
  %%rec = insertvalue { i8, i64 } %%rec0, i64 %%pay, 1
%s}
`, body))
}
