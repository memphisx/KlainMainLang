// runtime_fetch_stream.go — TDD-00097 Stage 4: `Response.body` as a
// ReadableStream<Uint8Array> fed straight from libcurl's write callback. The
// hooks live in fetchsrc/fetch_stream.c (TDD-00240; see there for their
// contract). Until a program touches `.body` they are no-op stubs the
// emitter defines at finalize, so the fully-buffered behavior is unchanged.
package llvm
