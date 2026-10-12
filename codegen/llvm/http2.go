package llvm

import (
	_ "embed"
	"os/exec"
	"strings"
)

// http2.go — the HTTP/2 server driver plumbing (TDD-00111 Stage 3).
// http2src/http2.c wraps nghttp2's session API behind the __kml_h2_* ABI the
// event loop drives; main.go writes it next to the .ll and compiles it alongside
// (linking -lnghttp2) only when the program uses the h2 server path — the same
// shape as tlssrc/tls.c. Keeps all nghttp2 headers out of the emitted IR.

//go:embed http2src/http2.c
var http2ServerSource string

// HTTP2ServerSource returns the C source implementing the __kml_h2_* ABI.
func HTTP2ServerSource() string { return layoutHeader() + http2ServerSource }

//go:embed http2src/h2node.c
var h2NodeSource string

// H2NodeSource returns the nghttp2 session natives lib/node/http2.ts calls
// (`@link http2`).
func H2NodeSource() string { return h2NodeSource }

// UsesH2Node reports whether the program links the http2 module's natives.
func (e *Emitter) UsesH2Node() bool { return e.usedH2Node }

// ensureH2Natives compiles h2node.c in; it reports strings through the
// pool's lastString.
func (e *Emitter) ensureH2Natives() {
	e.ensureNativePool()
	e.usedH2Node = true
}

// UsesHTTP2 reports whether the program uses the h2 server path, so main.go only
// compiles http2.c + links nghttp2 when needed (mirrors UsesTLS/UsesCrypto).
func (e *Emitter) UsesHTTP2() bool { return e.usedHTTP2 }

// emitHTTP2ServerDecls declares the nghttp2 driver's C ABI (http2src/http2.c)
// as extern in the IR — the session lifecycle functions the connection path
// drives. The __kml_h2_dispatch/__kml_h2_resp_* side is defined by the IR bridge
// (buildHTTP2Bridge) and called from C, so it isn't declared here.
func (e *Emitter) emitHTTP2ServerDecls() {
	// Idempotent: the h2 session ABI declares are the same for every server, so a
	// program with more than one h2-capable server (e.g. a createServer primary +
	// a klain:http cluster-combo listener, ADR-00989) emits them once.
	if e.usedHTTP2ServerDecls {
		return
	}
	e.usedHTTP2ServerDecls = true
	// The 5th ptr is the per-server dispatch vtable (TDD-00191 Stage 4).
	e.emitGlobal("declare ptr @__kml_h2_session_server_new(i32, ptr, ptr, ptr, ptr)")
	e.emitGlobal("declare void @__kml_h2_session_feed(ptr, ptr, i64)")
	e.emitGlobal("declare i32 @__kml_h2_session_recv(ptr)")
	e.emitGlobal("declare i32 @__kml_h2_session_send(ptr)")
	e.emitGlobal("declare i32 @__kml_h2_session_want_read(ptr)")
	e.emitGlobal("declare i32 @__kml_h2_session_want_write(ptr)")
	e.emitGlobal("declare void @__kml_h2_session_del(ptr)")
}

// LocateHTTP2 returns the clang cflags/libs to compile and link http2src/http2.c:
// libnghttp2's include path via pkg-config, plus -lnghttp2 (added via
// requireLink so it also flows through LinkLibs). Falls back to a bare -lnghttp2
// when pkg-config isn't present (Homebrew/Linux both ship the .pc).
func LocateHTTP2() (cflags, libs []string) {
	if out, err := exec.Command("pkg-config", "--cflags", "libnghttp2").Output(); err == nil {
		cflags = strings.Fields(strings.TrimSpace(string(out)))
	}
	if out, err := exec.Command("pkg-config", "--libs", "libnghttp2").Output(); err == nil {
		libs = strings.Fields(strings.TrimSpace(string(out)))
	}
	if len(libs) == 0 {
		libs = []string{"-lnghttp2"}
	}
	return cflags, libs
}
