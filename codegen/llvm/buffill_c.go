package llvm

import _ "embed"

// buffill_c.go — the embedded Buffer fill runtime (buffillsrc/buffill.c):
// Node's _fill argument reading and validation over boxed arguments,
// compiled in when a program fills a Buffer.

//go:embed buffillsrc/buffill.c
var bufFillSource string

// ensureBufFill declares __kml_buf_fill_any once and links buffill.c with
// what it calls: dynjson.c (inspection, a view's bytes, ToNumber) and the
// Buffer codecs.
func (e *Emitter) ensureBufFill() {
	if e.usedBufFill {
		return
	}
	e.usedBufFill = true
	e.ensureDynJSONC()
	e.ensureBufferCodecs()
	e.emitGlobal(`declare i64 @__kml_buf_fill_any(ptr, i64, i64, ptr, ptr)`)
}
