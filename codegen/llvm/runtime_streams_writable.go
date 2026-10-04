// runtime_streams_writable.go — the WHATWG WritableStream state machine
// (TDD-00097 Stage 2). One malloc'd %kml.wstream struct fuses the stream, its
// default controller, and its writer (compile-time retyping, exactly the
// %kml.rstream convention in runtime_streams.go). The write queue, drain loop
// and backpressure (writer.ready) live in streamssrc/streams.c (TDD-00240),
// which documents the field layout.
package llvm

const (
	wstreamStructIR   = "{ i64, i64, ptr, i64, i64, i64, double, double, ptr, ptr, ptr, ptr, i64, ptr, ptr, ptr }"
	wstreamStructSize = 128
)

// ensureWStreamRuntime declares the WritableStream runtime (streams.c) once.
func (e *Emitter) ensureWStreamRuntime() {
	if e.usedWStreamRuntime {
		return
	}
	e.usedWStreamRuntime = true
	e.ensurePromiseSettle()
	e.ensurePromiseAddReaction()
	e.emitGlobal(`declare ptr @__kml_ws_alloc(double)
declare void @__kml_ws_update_ready(ptr)
declare void @__kml_ws_qpush(ptr, i64, i64, double, ptr)
declare void @__kml_ws_finish_close(ptr)
declare void @__kml_ws_write_settled(ptr)
declare void @__kml_ws_close_settled(ptr)
declare void @__kml_ws_advance(ptr)
declare void @__kml_ws_error(ptr, i64)
declare ptr @__kml_ws_write(ptr, i64, i64)
declare ptr @__kml_ws_close(ptr)
declare ptr @__kml_ws_abort(ptr, i64)
declare double @__kml_ws_desired(ptr)
declare void @__kml_ws_started(ptr)
declare i64 @__kml_ws_lock(ptr)
declare void @__kml_ws_unlock(ptr)`)
}
