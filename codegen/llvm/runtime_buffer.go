// runtime_buffer.go — the Node-Buffer codec runtime hookup (TDD-00103).
// The codecs themselves live in buffersrc/buffer_codecs.c (self-contained,
// libc only), embedded here and linked when a program names a __kml_buf_*
// routine (runtime_resolve.go).
package llvm

import _ "embed"

//go:embed buffersrc/buffer_codecs.c
var bufferCodecsSource string

// BufferCodecsSource returns the C source implementing the __kml_buf_* ABI.
func BufferCodecsSource() string { return bufferCodecsSource }
