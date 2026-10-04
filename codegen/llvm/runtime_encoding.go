// runtime_encoding.go — btoa/atob, the UTF-8 label check and the URI
// percent encoders/decoders. They live in encodingsrc/encoding.c
// (TDD-00240); the ensure functions declare the symbols a program uses.
package llvm

import _ "embed"

//go:embed encodingsrc/encoding.c
var encodingSource string

// EncodingSource is the encoding runtime's C source.
func EncodingSource() string { return encodingSource }

// UsesEncoding reports whether the program links the encoding runtime.
func (e *Emitter) UsesEncoding() bool {
	return e.usedBase64Encode || e.usedBase64EncodeBytes || e.usedBase64Decode || e.usedUtf8LabelCheck ||
		e.usedEncodeURIComponent || e.usedEncodeURI || e.usedEncodeFileURLPath ||
		e.usedDecodeURIComponent || e.usedDecodeURIComponentStrict || e.usedDecodeURIStrict
}

// ensureBase64Encode declares __kml_btoa: '='-padded base64 of the string's
// bytes (a "binary string" here is the byte sequence itself).
func (e *Emitter) ensureBase64Encode() {
	if e.usedBase64Encode {
		return
	}
	e.usedBase64Encode = true
	e.ensureStrHeaderRuntime()
	e.emitGlobal("declare ptr @__kml_btoa(ptr)")
}

// ensureBase64Decode declares __kml_atob: WHATWG forgiving-base64
// (ADR-00458/00550/00563); throws InvalidCharacterError.
func (e *Emitter) ensureBase64Decode() {
	if e.usedBase64Decode {
		return
	}
	e.usedBase64Decode = true
	e.ensureExceptionHelpers()
	e.ensureStrHeaderRuntime()
	e.emitGlobal("declare ptr @__kml_atob(ptr)")
}

// ensureUtf8LabelCheck declares __kml_is_utf8_label(ptr) -> i1: whether a
// TextDecoder label names UTF-8, the only encoding decoded (ADR-00567).
func (e *Emitter) ensureUtf8LabelCheck() {
	if e.usedUtf8LabelCheck {
		return
	}
	e.usedUtf8LabelCheck = true
	e.emitGlobal("declare zeroext i1 @__kml_is_utf8_label(ptr)")
}

func (e *Emitter) ensurePercentFn(used *bool, fnName string, strict bool) {
	if *used {
		return
	}
	*used = true
	e.ensureStrHeaderRuntime()
	if strict {
		e.ensureExceptionHelpers()
	}
	e.emitGlobal("declare ptr @" + fnName + "(ptr)")
}

func (e *Emitter) ensureEncodeURIComponent() {
	e.ensurePercentFn(&e.usedEncodeURIComponent, "__kml_encode_uri_component", false)
}

func (e *Emitter) ensureEncodeURI() {
	e.ensurePercentFn(&e.usedEncodeURI, "__kml_encode_uri", false)
}

// ensureDecodeURIComponent declares the lenient decoder (a malformed
// escape passes through), kept for HTTP query strings.
func (e *Emitter) ensureDecodeURIComponent() {
	e.ensurePercentFn(&e.usedDecodeURIComponent, "__kml_decode_uri_component", false)
}

// The strict variants back the global decodeURIComponent()/decodeURI(),
// which throw URIError on a malformed escape (ADR-00556).
func (e *Emitter) ensureDecodeURIComponentStrict() {
	e.ensurePercentFn(&e.usedDecodeURIComponentStrict, "__kml_decode_uri_component_strict", true)
}

func (e *Emitter) ensureDecodeURIStrict() {
	e.ensurePercentFn(&e.usedDecodeURIStrict, "__kml_decode_uri_strict", true)
}
