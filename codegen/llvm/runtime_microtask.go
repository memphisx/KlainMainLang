// runtime_microtask.go — the microtask FIFO (TDD-00083 Stage 3): queueMicrotask
// and the reactions scheduled by Promise.prototype.then/.catch/.finally are run
// here, drained at the spec-reachable points (end of the top-level script, each
// scheduler step, each fired timer). Entries are closure headers {funcPtr,
// envPtr} invoked as 0-arg callbacks — a `.then` reaction is wrapped into such a
// closure at its call site (see emitPromiseThen), so this queue stays generic.
// The queues live in microtasksrc/microtask.c (TDD-00240).
package llvm

import _ "embed"

//go:embed microtasksrc/microtask.c
var microtaskSource string

// MicrotaskSource is the microtask runtime's C source, behind kml_layout.h.
func MicrotaskSource() string { return layoutHeader() + microtaskSource }

// ensureMicrotasks declares the microtask runtime (microtask.c) once.
func (e *Emitter) ensureMicrotasks() {
	if e.usedMicrotasks {
		return
	}
	e.usedMicrotasks = true
	e.emitGlobal(`declare void @__kml_microtask_enqueue(` + irPtr + `)`)
}
