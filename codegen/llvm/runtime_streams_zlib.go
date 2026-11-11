// runtime_streams_zlib.go — TDD-00097 Stage 6: CompressionStream /
// DecompressionStream. Each is an ordinary %kml.ts TransformStream context
// (so .readable/.writable/pipeThrough ride the Stage 3 machinery unchanged)
// whose transform/flush closures are native functions over a zlib z_stream
// handle (streamssrc/zlibstream.c, TDD-00240). `-lz` is linked only when one
// is actually constructed — the same used-only discipline libcurl has.
//
// %kml.zctx (32 B): 0 z_stream* · 1 readable rstream · 2 mode (0 deflate ·
// 1 inflate) · 3 finished flag.
package llvm

import _ "embed"

const zctxStructIR = "{ " + irPtr + ", " + irPtr + ", i64, i64 }"

//go:embed streamssrc/zlibstream.c
var zlibStreamSource string

// ZlibStreamSource is the Compression/DecompressionStream C source, behind
// kml_layout.h.
func ZlibStreamSource() string { return layoutHeader() + zlibStreamSource }
