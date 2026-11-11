// runtime_streams.go — the WHATWG ReadableStream state machine (TDD-00097
// Stage 1). One malloc'd %kml.rstream struct fuses the stream and its default
// controller: the state (readable/closed/errored), the chunk FIFO with its
// high-water mark + total-size accounting, the pending-read promise FIFO, the
// underlying-source closures, and a per-construction-site "fulfill thunk" the
// compiler emits (emit_streams.go) so the runtime can settle read() promises
// with correctly-typed {value, done} records while staying fully type-agnostic
// itself — chunks travel as two raw i64 words (the same marshalling the
// promise v0/v1 slots use).
//
// The state machines (readable, writable, pipeTo, tee, the TransformStream
// coupling) live in streamssrc/streams.c (TDD-00240); the field layout of
// each struct is documented there. This file holds the layouts the emitter
// indexes, the embed, and the ensure*() functions that declare the entry
// points.
package llvm

import "fmt"

import _ "embed"

const (
	rstreamStructIR   = "{ i64, i64, " + irPtr + ", i64, i64, i64, double, double, " + irPtr + ", " + irPtr + ", " + irPtr + ", i64, " + irPtr + ", i64, i64, i64, " + irPtr + ", " + irPtr + ", " + irPtr + ", " + irPtr + " }"
	rstreamStructSize = 144
)

//go:embed streamssrc/streams.c
var streamsSource string

// StreamsSource is the stream runtime's C source, behind kml_layout.h.
func StreamsSource() string { return layoutHeader() + streamsSource }

// ensurePromiseAddReaction declares @__kml_promise_add_reaction(ptr %p, ptr
// %clo): attach a reaction closure to a promise — enqueued as a microtask
// immediately when the promise is already settled, appended to its reaction
// list otherwise. It also builds __kml_stream_decode, the call to a
// program-emitted decoder, whose {i64,i64,i64} return the C cannot take by
// value.
func (e *Emitter) ensurePromiseAddReaction() {
	if e.usedPromiseAddReaction {
		return
	}
	e.usedPromiseAddReaction = true
	e.ensureMicrotasks()
	e.ensureNanBox()
	e.ensureExceptionHelpers()
	e.ensureUnhandledRejections()
	// __kml_stream_decode(decode, rec, out): run a per-chunk-type decoder,
	// which returns {v0, v1, done} in registers, for streams.c, storing the
	// three words at out.
	restore := e.beginDetachedFunc()
	dv := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call { i64, i64, i64 } %%decode("+irPtr+" %%rec)", dv))
	for k := 0; k < 3; k++ {
		w, p := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = extractvalue { i64, i64, i64 } %s, %d", w, dv, k))
		e.emitInstr(fmt.Sprintf("%s = getelementptr i64, "+irPtr+" %%out, i64 %d", p, k))
		e.heapStore("i64", w, p, 8)
	}
	e.emitTerminator("ret void")
	body := e.allocas.String() + e.body.String()
	restore()
	e.writeFunc("void @__kml_stream_decode("+irPtr+" %decode, "+irPtr+" %rec, "+irPtr+" %out)", body)
}
