// runtime_fetch_stream.go — TDD-00097 Stage 4: `Response.body` as a
// ReadableStream<Uint8Array> fed straight from libcurl's write callback. The
// hooks live in fetchsrc/fetch_stream.c (TDD-00240; see there for their
// contract). Until a program touches `.body` they are no-op stubs the
// emitter defines at finalize, so the fully-buffered behavior is unchanged.
package llvm

// ensureFetchBodyStream declares the real streaming hooks.
func (e *Emitter) ensureFetchBodyStream() {
	if e.usedFetchBodyStream {
		return
	}
	e.usedFetchBodyStream = true
	e.ensureStreamRuntime()
	e.ensureAwaitFetchHeaders()
	e.ensureMemcpy()
	e.ensureStrHeaderRuntime() // error .message must be headered for concat/=== (TDD-00120)
	e.emitGlobal("declare i32 @curl_easy_pause(ptr noundef, i32 noundef)")
	e.emitGlobal(`declare i64 @__kml_fetch_body_write(ptr, ptr, i64)
declare void @__kml_fetch_body_on_done(ptr)
declare void @__kml_fetch_body_abort(ptr, ptr)
declare ptr @__kml_fetch_body_pull(ptr)
declare ptr @__kml_fetch_body_stream(ptr, ptr, ptr, i64)`)
}
