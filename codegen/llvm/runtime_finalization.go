// runtime_finalization.go — FinalizationRegistry runtime (TDD-00163).
//
// One pending-callback path, several death signals, chosen by -mm:
//
//   - -mm=manual (default): Memory.free(ptr) looks the pointer up in the
//     process-global registration list (__kml_finreg_onfree, called from the
//     compiled free chokepoint) and enqueues each matching registration's
//     cleanup callback; anything still live at exit is flushed by an atexit
//     hook — deterministic destructors plus an exit sweep. With
//     -finalizers=report the atexit hook first prints one leak line per
//     surviving registration (its held value + the .register() call site).
//   - -mm=gc (Boehm): each register() call also registers a real Boehm
//     finalizer (GC_register_finalizer); when the collector proves the target
//     unreachable it fires __kml_finreg_gc_cb, which only enqueues — never
//     calls into user code from collector context.
//
// Death signals enqueue, they never execute: the callback (wrapped by a
// per-construction-site trampoline that unboxes the held value back to its
// static type) rides the existing microtask FIFO, so it runs at the same
// spec-reachable drain points .then reactions do — end of the top-level
// script, each scheduler step, each fired timer — and at the atexit flush.
//
// Registration cells live on one process-global (per-thread) singly linked
// list; each cell is { ptr next, ptr registry, ptr target, i64 held,
// ptr token, i64 line, i64 col, i64 dead, ptr gcNext } (72 bytes, all
// naturally aligned 8-byte fields). `dead` marks unregistered/already-fired
// cells (cells are never unlinked — same never-compact reasoning as the
// timer queue's -1 sentinel). Under gc, `target` is left null (a scanned
// strong reference would keep the target alive forever, defeating the
// finalizer) and `gcNext` chains earlier registrations on the same target,
// since GC_register_finalizer replaces — and hands back — the previous
// finalizer's client data.
//
// The runtime lives in finalizationsrc/finalization.c (TDD-00240); KML_GC and
// KML_FINREPORT select the mode.
package llvm

import _ "embed"

//go:embed finalizationsrc/finalization.c
var finalizationSource string

// FinalizationSource is the FinalizationRegistry runtime's C source, with the
// -mm and -finalizers mode defines the death signals depend on.
func (e *Emitter) FinalizationSource() string {
	pre := ""
	if e.isGCMode() {
		pre = "#define KML_GC 1\n"
	} else if e.opts.Finalizers == "report" {
		pre = "#define KML_FINREPORT 1\n"
	}
	return pre + finalizationSource
}

// UsesFinRegHelpers reports whether the program links the finalization runtime.
func (e *Emitter) UsesFinRegHelpers() bool { return e.usedFinRegHelpers }

// ensureGCInvokeFinalizers declares Boehm's GC_invoke_finalizers exactly once
// (TDD-00163 Stage 3): Boehm only *queues* a dead object's finalizer at
// collection time and runs the queue lazily from later allocation points, so
// gc() and the exit flush call it explicitly to make firing observable.
func (e *Emitter) ensureGCInvokeFinalizers() {
	if e.declaredGCInvokeFin {
		return
	}
	e.declaredGCInvokeFin = true
	e.emitGlobal("declare i32 @GC_invoke_finalizers()")
}

// ensureFinalizationHelpers declares the FinalizationRegistry runtime
// (finalization.c) once. The manual-mode free hook only exists there.
func (e *Emitter) ensureFinalizationHelpers() {
	if e.usedFinRegHelpers {
		return
	}
	e.usedFinRegHelpers = true
	e.ensureMicrotasks()
	e.emitGlobal(`declare ptr @__kml_finreg_create(ptr, ptr, ptr)
declare void @__kml_finreg_register(ptr, ptr, i64, ptr, i64, i64)
declare zeroext i1 @__kml_finreg_unregister(ptr, ptr)`)
	if !e.isGCMode() {
		e.emitGlobal("declare void @__kml_finreg_onfree(ptr)")
	}
}
