package llvm

import _ "embed"

// tls.go — the `tls` module's native side: tlssrc/tlshandle.c, TLS sessions
// over the pool's TCP handles, compiled alongside only when the program's
// TypeScript uses them. TLS is OpenSSL-only (CommonCrypto has no TLS API), so
// it reuses LocateCrypto's OpenSSL discovery for cflags/-L and adds -lssl.

//go:embed tlssrc/tlshandle.c
var tlsHandleSource string

// TLSHandleSource returns the C source of the TLS primitives over the pool's
// TCP handles (the `tls` module written in TypeScript).
func TLSHandleSource() string { return tlsHandleSource }

// UsesTLSHandles reports whether the program's TypeScript uses them.
func (e *Emitter) UsesTLSHandles() bool { return e.usedTLSHandles }

// LocateTLS returns the clang cflags/libs to compile and link tlshandle.c:
// the OpenSSL crypto discovery (headers + -L + -lcrypto) plus -lssl.
func LocateTLS() (cflags, libs []string) {
	cflags, libs = LocateCrypto("openssl")
	return cflags, append(libs, "-lssl")
}
