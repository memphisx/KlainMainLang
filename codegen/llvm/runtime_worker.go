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
	return fmt.Sprintf("call void @kml_gc_set_stack_bottom("+irPtr+" %s)", val)
}

// gcSBLoad returns the IR statement that reads the CURRENT thread's GC stack
// bottom into reg — the value a later gcSBStore(reg) puts back.
func (e *Emitter) gcSBLoad(reg string) string {
	return fmt.Sprintf("%s = call "+irPtr+" @kml_gc_stack_bottom()", reg)
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
	e.ensureTaskRuntime()
	// klainpool.c's worker natives, and worker.c's own entry points.

	// @__kml_worker_uncaught(tag, payload): an uncaught error on a worker
	// thread goes to the parent's 'error' listener (through the hook
	// worker_threads.ts registers, which posts it on the internal port) and
	// ends the thread with code 1. On the main thread it returns.
	restore := e.beginDetachedFunc()
	w := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call zeroext i1 @__kml_worker_is_thread()", w))
	mainL, workerL := e.freshLabel("wu.main"), e.freshLabel("wu.worker")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", w, workerL, mainL))
	e.emitLabel(mainL)
	e.emitTerminator("ret void")
	e.emitLabel(workerL)
	rec := e.emitCaughtRecord("%tag", "%pay")
	box := e.emitCaughtToAny(Value{Ref: rec, Ty: TypeCaught})
	e.emitInstr(fmt.Sprintf("call i1 @__kml_process_hook_call(i32 3, i64 %s, i64 %d)", box.Ref, nbUndefined))
	e.emitInstr("call void @__kml_thread_exit(i32 1)")
	e.emitTerminator("unreachable")
	body := e.allocas.String() + e.body.String()
	restore()
	e.ensureProcessHooks()
	e.writeFunc("void @__kml_worker_uncaught(i8 %tag, i64 %pay)", body)
}
