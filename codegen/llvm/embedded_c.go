package llvm

import "fmt"

// CSource is one embedded C runtime file that must be compiled and linked
// alongside the emitted LLVM IR when the program uses the corresponding
// feature. Name is a short suffix (e.g. "dtoa") used to build a per-program
// temp filename; Content is the C source; CFlags/Libs are the extra clang
// flags and `-l` libraries that file needs.
type CSource struct {
	Name    string
	Content string
	CFlags  []string
	Libs    []string
}

// EmbeddedCSources returns every embedded C runtime file this program's emitted
// IR depends on, based on which features it actually used — the single source
// of truth shared by the CLI driver (main.go) and the conformance runner, so
// the two can never drift on which .c files get linked (a drift that silently
// under-counted conformance: any test needing dtoa/bigint/JSON/etc. failed to
// link in the runner even though the CLI compiled it fine). Order matches the
// CLI's historical order. Returns an error only for an unknown backend name.
func (e *Emitter) EmbeddedCSources() ([]CSource, error) {
	var out []CSource
	if e.UsesBigInt() {
		backend := e.BigIntBackend()
		src, ok := BigIntBackendSource(backend)
		if !ok {
			return nil, fmt.Errorf("bigint: unknown backend %q", backend)
		}
		cflags, libs := LocateBigInt(backend)
		out = append(out, CSource{"bigint", src, cflags, libs})
	}
	if e.UsesCrypto() {
		backend := e.CryptoBackend()
		src, ok := CryptoBackendSource(backend)
		if !ok {
			return nil, fmt.Errorf("crypto: unknown backend %q", backend)
		}
		cflags, libs := LocateCrypto(backend)
		out = append(out, CSource{"crypto", src, cflags, libs})
	}
	if e.UsesTLS() {
		if e.CryptoBackend() == "commoncrypto" {
			return nil, fmt.Errorf("tls: the `tls` module requires the OpenSSL crypto backend")
		}
		cflags, libs := LocateTLS()
		out = append(out, CSource{"tls", TLSClientSource(), cflags, libs})
	}
	if e.UsesHTTP2() {
		cflags, libs := LocateHTTP2()
		out = append(out, CSource{"http2", HTTP2ServerSource(), cflags, libs})
	}
	if e.UsesBufferCodecs() {
		out = append(out, CSource{"bufcodecs", BufferCodecsSource(), nil, nil})
	}
	if e.UsesJSONParse() {
		out = append(out, CSource{"jsontree", JSONParseTreeSource(), nil, nil})
	}
	if e.UsesURLPattern() {
		out = append(out, CSource{"urlpattern", URLPatternSource(), nil, nil})
	}
	if e.UsesFloatFmt() {
		out = append(out, CSource{"dtoa", DtoaSource(), nil, nil})
	}
	return out, nil
}
