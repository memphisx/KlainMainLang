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
func WorkerSource() string { return layoutHeader() + loopUser + workerSource }

// ensureWorkerRuntime brings what worker.c relies on from the program: a
// worker's tasks, and the process hooks its uncaught errors go through.
func (e *Emitter) ensureWorkerRuntime() {
	e.usesWorkers = true
	e.ensureUnhandledRejections()
}
