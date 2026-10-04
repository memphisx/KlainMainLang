// emit_tls.go — the TLS servers codegen still builds: https.createServer's
// and http2.createSecureServer's { cert, key } options and their SSL_CTX
// global. (Node's `tls` module itself is lib/node/tls.ts.)
package llvm

// ensureHTTPS1Server wires the HTTPS/1.1 server path (https.createServer, or the
// allowHTTP1 fallback of http2.createSecureServer): the flag the event-loop
// emitter reads to route a non-h2 TLS connection into the fiber conn table, and
// libssl (usedTLS) so tls.c is compiled/linked. The @__kml_http_tls_ctx slot is
// shared with the h2 secure server — emit it only when that path hasn't already.
func (e *Emitter) ensureHTTPS1Server() {
	if e.httpS1Wired {
		return
	}
	e.httpS1Wired = true
	e.usedHTTPS1Server = true
	e.usedTLS = true
	e.ensureHTTPTLSCtxGlobal()
}

// ensureHTTPTLSCtxGlobal emits the @__kml_http_tls_ctx global once — it is shared
// by the HTTPS/1.1 and the h2-over-TLS server paths, either of which may be
// wired first (including via a TDD-00191 pre-scan), so a single dedup flag keeps
// it from being emitted twice.
func (e *Emitter) ensureHTTPTLSCtxGlobal() {
	if e.httpTLSCtxEmitted {
		return
	}
	e.httpTLSCtxEmitted = true
	e.emitGlobal(`@__kml_http_tls_ctx = global ptr null`)
}
