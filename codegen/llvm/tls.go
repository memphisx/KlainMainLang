package llvm

import _ "embed"

// tls.go — the `tls` module's OpenSSL (libssl) client helper plumbing
// (TDD-00109). tlssrc/tls.c implements the __kml_tls_* client ABI over libssl;
// main.go writes it next to the .ll and compiles it alongside — the same shape
// as the crypto backend (crypto.go) — only when the program uses `tls`. TLS is
// OpenSSL-only (CommonCrypto has no TLS API), so it reuses LocateCrypto's
// OpenSSL discovery for cflags/-L and adds -lssl.

//go:embed tlssrc/tls.c
var tlsClientSource string

// TLSClientSource returns the C source implementing the __kml_tls_* ABI.
func TLSClientSource() string { return tlsClientSource }

// emitTLSNetSymbols resolves the __kml_tls_* ABI the net runtime references.
// When the program uses `tls`, tlssrc/tls.c defines them (linked via libssl), so
// the IR just declares them extern. Otherwise every socket's SSL* is null and
// the branches are dead, so cheap no-op stub definitions satisfy the linker.
// Only emitted when the net runtime itself was (it is the sole referencer).
func (e *Emitter) emitTLSNetSymbols() {
	if !e.usedNetRuntime && !e.usedWSClientRuntime {
		return
	}
	if e.usedTLS {
		e.emitGlobal("declare ptr @__kml_tls_client_connect(i32, ptr, i32, ptr)")
		e.emitGlobal("declare i64 @__kml_tls_read(ptr, ptr, i64)")
		e.emitGlobal("declare i64 @__kml_tls_write(ptr, ptr, i64)")
		e.emitGlobal("declare void @__kml_tls_free(ptr)")
		e.emitGlobal("declare ptr @__kml_tls_server_ctx(ptr, ptr, ptr)")
		e.emitGlobal("declare ptr @__kml_tls_server_accept(ptr, i32)")
		return
	}
	e.emitGlobal("define i64 @__kml_tls_read(ptr %s, ptr %b, i64 %n) {\nentry:\n  ret i64 -1\n}")
	e.emitGlobal("define i64 @__kml_tls_write(ptr %s, ptr %b, i64 %n) {\nentry:\n  ret i64 -1\n}")
	e.emitGlobal("define void @__kml_tls_free(ptr %s) {\nentry:\n  ret void\n}")
	e.emitGlobal("define ptr @__kml_tls_server_accept(ptr %c, i32 %f) {\nentry:\n  ret ptr null\n}")
	e.emitGlobal("define ptr @__kml_tls_client_connect(i32 %f, ptr %h, i32 %r, ptr %e) {\nentry:\n  ret ptr null\n}")
}

// LocateTLS returns the clang cflags/libs to compile and link tlssrc/tls.c:
// the OpenSSL crypto discovery (headers + -L + -lcrypto) plus -lssl.
func LocateTLS() (cflags, libs []string) {
	cflags, libs = LocateCrypto("openssl")
	return cflags, append(libs, "-lssl")
}
