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
func HTTP2ServerSource() string { return http2ServerSource }

// UsesHTTP2 reports whether the program uses the h2 server path, so main.go only
// compiles http2.c + links nghttp2 when needed (mirrors UsesTLS/UsesCrypto).
func (e *Emitter) UsesHTTP2() bool { return e.usedHTTP2 }

// emitHTTP2ServerDecls declares the nghttp2 driver's C ABI (http2src/http2.c)
// as extern in the IR — the session lifecycle functions the connection path
// drives. The __kml_h2_dispatch/__kml_h2_resp_* side is defined by the IR bridge
// (buildHTTP2Bridge) and called from C, so it isn't declared here.
func (e *Emitter) emitHTTP2ServerDecls() {
	e.ensureH2ClientBridge()
	e.emitGlobal("declare ptr @__kml_h2_session_server_new(i32, ptr, ptr, ptr)")
	e.emitGlobal("declare void @__kml_h2_session_feed(ptr, ptr, i64)")
	e.emitGlobal("declare i32 @__kml_h2_session_recv(ptr)")
	e.emitGlobal("declare i32 @__kml_h2_session_send(ptr)")
	e.emitGlobal("declare i32 @__kml_h2_session_want_read(ptr)")
	e.emitGlobal("declare i32 @__kml_h2_session_want_write(ptr)")
	e.emitGlobal("declare void @__kml_h2_session_del(ptr)")
	e.emitGlobal("declare void @__kml_h2_set_blocking(i32)")
}

// ensureH2TLSServer wires the h2-over-TLS secure-server path (TDD-00111 Stage
// 3b / http2.createSecureServer). It sets the flag the event-loop emitter reads
// to splice its TLS accept branch, links libssl (usedTLS) + nghttp2 (usedHTTP2),
// and declares the extern C symbols that branch calls: the tls.c server
// handshake/ALPN/read/write/free helpers, plus the nghttp2 driver ABI. Also
// emits the two module-level globals the branch references — the server's
// SSL_CTX* slot (populated at the createSecureServer call site) and the wire-
// format ALPN token it matches the negotiated protocol against.
//
// Must run before ensureHTTPRuntime emits the event loop, so the flag is set in
// time; the createSecureServer emitter calls it first for that reason.
func (e *Emitter) ensureH2TLSServer() {
	if e.usedH2TLSServer {
		return
	}
	e.usedH2TLSServer = true
	e.usedTLS = true    // link libssl + compile tlssrc/tls.c (main.go UsesTLS gate)
	e.usedHTTP2 = true  // link nghttp2 + compile http2src/http2.c
	e.ensureMemcmp()
	// The __kml_tls_* server ABI (server_ctx/server_accept/alpn_selected/read/
	// write/free) is declared once by emitTLSNetSymbols, gated on usedH2TLSServer.
	e.emitGlobal(`@__kml_http_tls_ctx = global ptr null`)
	e.emitGlobal(`@__kml_h2_alpn = private unnamed_addr constant [3 x i8] c"h2\00"`)
	// The __kml_h2_session_* ABI the TLS drive loop calls is declared by the
	// shared http server core (emitHTTPCreateServer always wires the h2c path),
	// which the createSecureServer emitter delegates to — no decl needed here.
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

// ensureH2ClientRuntime (TDD-00139 Stage 3) declares the client-session C ABI
// and defines the four generic IR callbacks the driver fires as response
// frames arrive. The stream context is a fixed 32-byte layout: cbResponse@0,
// cbData@8, cbEnd@16, headersMap@24 — independent of any user types, so these
// emit once regardless of handler shapes.
func (e *Emitter) ensureH2ClientRuntime() {
	if e.usedH2Client {
		return
	}
	e.usedH2Client = true
	e.usedHTTP2 = true // links http2.c + nghttp2
	e.ensureStrHeaderRuntime()
	e.ensureMapStrHelpers()
	e.ensureMemcpy()
	e.emitGlobal(`declare ptr @__kml_h2c_connect_url(ptr)
declare i32 @__kml_h2c_request(ptr, ptr, ptr, ptr, ptr, ptr, i64)
declare void @__kml_h2c_pump_tick()
declare i64 @__kml_h2c_pump_all()
declare void @__kml_h2c_flush()
declare void @__kml_h2c_close(ptr)
declare void @__kml_h2c_destroy(ptr)`)
	e.ensureAtexitDecl()
	e.ensureH2ClientBridge()
}

// ensureH2ClientBridge defines the four generic callbacks http2.c fires as
// client response frames arrive. Emitted for EVERY program that links the h2
// driver (server-only included — the C file references these symbols
// unconditionally), not just ones that call http2.connect.
func (e *Emitter) ensureH2ClientBridge() {
	if e.usedH2ClientBridge {
		return
	}
	e.usedH2ClientBridge = true
	e.ensureStrHeaderRuntime()
	e.ensureMapStrHelpers()
	e.ensureMemcpy()
	e.emitGlobal(`define void @__kml_h2c_on_header(ptr %ctx, ptr %name, ptr %value) {
entry:
  %hp = getelementptr i8, ptr %ctx, i64 24
  %m = load ptr, ptr %hp, align 8
  %kn = call ptr @__kml_str_from_cstr(ptr %name)
  %kv = call ptr @__kml_str_from_cstr(ptr %value)
  %vi = ptrtoint ptr %kv to i64
  call void @__kml_map_str_set(ptr %m, ptr %kn, i64 %vi)
  ret void
}

define void @__kml_h2c_on_response(ptr %ctx) {
entry:
  %cp = load ptr, ptr %ctx, align 8
  %isnull = icmp eq ptr %cp, null
  br i1 %isnull, label %done, label %fire
fire:
  %hp = getelementptr i8, ptr %ctx, i64 24
  %m = load ptr, ptr %hp, align 8
  %fpp = getelementptr { ptr, ptr }, ptr %cp, i32 0, i32 0
  %fp = load ptr, ptr %fpp, align 8
  %epp = getelementptr { ptr, ptr }, ptr %cp, i32 0, i32 1
  %ep = load ptr, ptr %epp, align 8
  call void %fp(ptr %ep, ptr %m)
  br label %done
done:
  ret void
}

define void @__kml_h2c_on_data(ptr %ctx, ptr %buf, i64 %len) {
entry:
  %sp = getelementptr i8, ptr %ctx, i64 8
  %cp = load ptr, ptr %sp, align 8
  %isnull = icmp eq ptr %cp, null
  br i1 %isnull, label %done, label %fire
fire:
  %s = call ptr @__kml_str_alloc(i64 %len)
  call ptr @memcpy(ptr %s, ptr %buf, i64 %len)
  %nul = getelementptr i8, ptr %s, i64 %len
  store i8 0, ptr %nul, align 1
  %fpp = getelementptr { ptr, ptr }, ptr %cp, i32 0, i32 0
  %fp = load ptr, ptr %fpp, align 8
  %epp = getelementptr { ptr, ptr }, ptr %cp, i32 0, i32 1
  %ep = load ptr, ptr %epp, align 8
  call void %fp(ptr %ep, ptr %s)
  br label %done
done:
  ret void
}

define void @__kml_h2c_on_end(ptr %ctx) {
entry:
  %sp = getelementptr i8, ptr %ctx, i64 16
  %cp = load ptr, ptr %sp, align 8
  %isnull = icmp eq ptr %cp, null
  br i1 %isnull, label %done, label %fire
fire:
  %fpp = getelementptr { ptr, ptr }, ptr %cp, i32 0, i32 0
  %fp = load ptr, ptr %fpp, align 8
  %epp = getelementptr { ptr, ptr }, ptr %cp, i32 0, i32 1
  %ep = load ptr, ptr %epp, align 8
  call void %fp(ptr %ep)
  br label %done
done:
  ret void
}`)
}
