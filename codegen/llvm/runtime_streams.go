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

import _ "embed"

const (
	rstreamStructIR   = "{ i64, i64, " + irPtr + ", i64, i64, i64, double, double, " + irPtr + ", " + irPtr + ", " + irPtr + ", i64, " + irPtr + ", i64, i64, i64, " + irPtr + ", " + irPtr + ", " + irPtr + ", " + irPtr + " }"
	rstreamStructSize = 144
)

//go:embed streamssrc/streams.c
var streamsSource string

// StreamsSource is the stream runtime's C source, behind kml_layout.h.
func StreamsSource() string { return layoutHeader() + streamsSource }
