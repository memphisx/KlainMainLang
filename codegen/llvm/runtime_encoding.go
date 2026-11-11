// runtime_encoding.go — btoa/atob, the UTF-8 label check and the URI
// percent encoders/decoders. They live in encodingsrc/encoding.c
// (TDD-00240), linked by the symbols a program names.
package llvm

import _ "embed"

//go:embed encodingsrc/encoding.c
var encodingSource string

// EncodingSource is the encoding runtime's C source, behind kml_layout.h.
func EncodingSource() string { return layoutHeader() + encodingSource }

// ensurePercentEncode backs encodeURI/encodeURIComponent (encoding.c).
func (e *Emitter) ensurePercentEncode() { e.ensureStrHeaderRuntime() }

// ensurePercentDecode backs the global decodeURIComponent()/decodeURI(),
// which throw URIError on a malformed escape (ADR-00556).
func (e *Emitter) ensurePercentDecode() {
	e.ensureStrHeaderRuntime()
	e.ensureExceptionHelpers()
}
