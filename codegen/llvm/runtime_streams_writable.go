// runtime_streams_writable.go — the WHATWG WritableStream state machine
// (TDD-00097 Stage 2). One malloc'd %kml.wstream struct fuses the stream, its
// default controller, and its writer (compile-time retyping, exactly the
// %kml.rstream convention in runtime_streams.go). The write queue, drain loop
// and backpressure (writer.ready) live in streamssrc/streams.c (TDD-00240),
// which documents the field layout.
package llvm

const (
	wstreamStructIR   = "{ i64, i64, " + irPtr + ", i64, i64, i64, double, double, " + irPtr + ", " + irPtr + ", " + irPtr + ", " + irPtr + ", i64, " + irPtr + ", " + irPtr + ", " + irPtr + ", " + irPtr + " }"
	wstreamStructSize = 136
)
