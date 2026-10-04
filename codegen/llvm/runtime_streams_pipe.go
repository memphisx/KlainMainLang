// runtime_streams_pipe.go — TDD-00097 Stage 3: the pipeTo state machine,
// TransformStream coupling, and tee(), all driven purely by promise reactions
// (no fiber). They live in streamssrc/streams.c (TDD-00240); this file holds
// the context layouts C checks its structs against and declares the entry
// points.
//
// %kml.pipe (80 B): src rstream · dst wstream · pipe promise · decodeFn
// ({i64,i64,i64}(ptr rec), compiler-emitted per chunk type) · flags
// (1 preventClose · 2 preventAbort · 4 preventCancel) · sigA / sigR (the
// AbortSignal's aborted flag and reason slot, or null) · pending chunk words ·
// the promise the current reaction fires on.
//
// %kml.tee (72 B): src · b1 · b2 · decodeFn · reading · canceled1 · canceled2 ·
// reason1 · reason2.
//
// %kml.ts (72 B): readable · writable · transClo · flushClo · parked flag ·
// parked chunk words · parkedProm (the in-flight writable write promise while
// a chunk waits for readable capacity) · closeProm (settled once flush +
// readable close complete).
package llvm

const (
	pipeCtxStructIR = "{ ptr, ptr, ptr, ptr, i64, ptr, ptr, i64, i64, ptr }"
	teeCtxStructIR  = "{ ptr, ptr, ptr, ptr, i64, i64, i64, i64, i64 }"
	tsCtxStructIR   = "{ ptr, ptr, ptr, ptr, i64, i64, i64, ptr, ptr }"
)

func (e *Emitter) ensureStreamPipeRuntime() {
	if e.usedStreamPipeRuntime {
		return
	}
	e.usedStreamPipeRuntime = true
	e.ensureStreamRuntime()
	e.ensureWStreamRuntime()
	e.ensureNanBox()
	e.emitGlobal(`declare ptr @__kml_mkclo(ptr, ptr)
declare ptr @__kml_pipe_to(ptr, ptr, ptr, i64, ptr, ptr)
declare void @__kml_tee_pullhook(ptr)
declare ptr @__kml_tee_pull(ptr)
declare ptr @__kml_tee_cancel(ptr, i64)
declare ptr @__kml_ts_run_transform(ptr, i64, i64)
declare ptr @__kml_ts_sink_write(ptr, i64, i64)
declare ptr @__kml_ts_pull(ptr)
declare void @__kml_ts_mirror_settle(ptr)
declare ptr @__kml_ts_sink_close(ptr)
declare void @__kml_ts_flush_done(ptr)
declare ptr @__kml_ts_sink_abort(ptr, i64)`)
}
