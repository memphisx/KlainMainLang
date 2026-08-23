// runtime_buffer.go — the Node-Buffer codec runtime hookup (TDD-00103).
// The codecs themselves live in buffersrc/buffer_codecs.c (self-contained,
// libc only), embedded here and compiled alongside the generated .ll by
// main.go only when a program actually uses a Buffer string codec — the
// same shape as the JSON parse-tree runtime.
package llvm

import _ "embed"

//go:embed buffersrc/buffer_codecs.c
var bufferCodecsSource string

// BufferCodecsSource returns the C source implementing the __kml_buf_* ABI.
func BufferCodecsSource() string { return bufferCodecsSource }

// UsesBufferCodecs reports whether any Buffer string codec reached codegen
// (drives the compile-alongside in main.go).
func (e *Emitter) UsesBufferCodecs() bool { return e.usesBufferCodecs }

// ensureBufferCodecs declares the __kml_buf_* ABI exactly once and flags the
// program as needing the codec runtime.
func (e *Emitter) ensureBufferCodecs() {
	e.usesBufferCodecs = true
	if e.declaredBufferCodecs {
		return
	}
	e.declaredBufferCodecs = true
	for _, d := range []string{
		"declare ptr @__kml_buf_hex_enc(ptr, i64)",
		"declare i64 @__kml_buf_hex_dec(ptr, ptr)",
		"declare ptr @__kml_buf_b64_enc(ptr, i64, i32)",
		"declare i64 @__kml_buf_b64_dec(ptr, ptr)",
		"declare ptr @__kml_buf_latin1_str(ptr, i64)",
		"declare i64 @__kml_buf_latin1_bytes(ptr, ptr)",
	} {
		e.emitGlobal(d)
	}
}
