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
	rstreamStructIR   = "{ i64, i64, ptr, i64, i64, i64, double, double, ptr, ptr, ptr, i64, ptr, i64, i64, i64, ptr, ptr }"
	rstreamStructSize = 144
)

//go:embed streamssrc/streams.c
var streamsSource string

// StreamsSource is the stream runtime's C source, behind kml_layout.h.
func StreamsSource() string { return layoutHeader() + streamsSource }

// UsesStreams reports whether the program links the stream runtime (every
// stream entry point goes through ensurePromiseAddReaction).
func (e *Emitter) UsesStreams() bool { return e.usedPromiseAddReaction }

// ensurePromiseAddReaction declares @__kml_promise_add_reaction(ptr %p, ptr
// %clo): attach a reaction closure to a promise — enqueued as a microtask
// immediately when the promise is already settled, appended to its reaction
// list otherwise. It also emits the two small IR shims over aggregates the C
// runtime cannot take by value: a program-emitted decoder returns
// {i64,i64,i64} in registers, and __kml_rs_tryread keeps its aggregate return.
func (e *Emitter) ensurePromiseAddReaction() {
	if e.usedPromiseAddReaction {
		return
	}
	e.usedPromiseAddReaction = true
	e.ensurePromiseSettle()
	e.emitGlobal(`declare void @__kml_promise_add_reaction(ptr, ptr)
declare void @__kml_rs_tryread_out(ptr, ptr)

; Run a per-chunk-type decoder and store its {v0, v1, done} words at %out.
define void @__kml_stream_decode(ptr %decode, ptr %rec, ptr %out) {
entry:
  %dv = call { i64, i64, i64 } %decode(ptr %rec)
  %v0 = extractvalue { i64, i64, i64 } %dv, 0
  %v1 = extractvalue { i64, i64, i64 } %dv, 1
  %done = extractvalue { i64, i64, i64 } %dv, 2
  store i64 %v0, ptr %out, align 8
  %o1 = getelementptr i64, ptr %out, i64 1
  store i64 %v1, ptr %o1, align 8
  %o2 = getelementptr i64, ptr %out, i64 2
  store i64 %done, ptr %o2, align 8
  ret void
}

; The synchronous Readable.read() core: {has, v0, v1}, {0,0,0} when empty.
define { i64, i64, i64 } @__kml_rs_tryread(ptr %s) {
entry:
  %buf = alloca [3 x i64], align 8
  call void @__kml_rs_tryread_out(ptr %s, ptr %buf)
  %a = load i64, ptr %buf, align 8
  %p1 = getelementptr i64, ptr %buf, i64 1
  %b = load i64, ptr %p1, align 8
  %p2 = getelementptr i64, ptr %buf, i64 2
  %c = load i64, ptr %p2, align 8
  %r0 = insertvalue { i64, i64, i64 } undef, i64 %a, 0
  %r1 = insertvalue { i64, i64, i64 } %r0, i64 %b, 1
  %r2 = insertvalue { i64, i64, i64 } %r1, i64 %c, 2
  ret { i64, i64, i64 } %r2
}`)
}

// ensureStreamRuntime declares the ReadableStream runtime (streams.c) once.
func (e *Emitter) ensureStreamRuntime() {
	if e.usedStreamRuntime {
		return
	}
	e.usedStreamRuntime = true
	e.ensurePromiseSettle()
	e.ensurePromiseAddReaction()
	e.emitGlobal(`declare ptr @__kml_rs_alloc(double, ptr)
declare void @__kml_rs_qpush(ptr, i64, i64, double)
declare void @__kml_rs_qunshift(ptr, i64, i64, double)
declare void @__kml_rs_rdpush(ptr, ptr)
declare ptr @__kml_rs_rdpop(ptr)
declare void @__kml_rs_pull_if_needed(ptr)
declare void @__kml_rs_pull_done(ptr)
declare void @__kml_rs_pull_settled(ptr)
declare void @__kml_rs_started(ptr)
declare i64 @__kml_rs_enqueue(ptr, i64, i64)
declare void @__kml_rs_finalize_close(ptr)
declare i64 @__kml_rs_close(ptr)
declare void @__kml_rs_error(ptr, i64)
declare ptr @__kml_rs_read(ptr)
declare double @__kml_rs_desired(ptr)
declare ptr @__kml_rs_cancel(ptr, i64)
declare i64 @__kml_rs_lock(ptr)
declare void @__kml_rs_unlock(ptr)`)
}
