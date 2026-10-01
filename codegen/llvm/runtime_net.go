// runtime_net.go — the net socket the HTTP server hands out on 'upgrade',
// 'connection' and 'clientError' (Node's `net` module itself is
// lib/node/net.ts): socket.on('data'|'end'|'close'), socket.write, socket.end.
//
// Every open socket's fd is non-blocking and folded into the central select()
// event loop like the child_process read pipes (runtime_childprocess.go):
// @__kml_net_fdset_add adds each one's fd (read-interest), @__kml_net_dispatch
// drains the readable ones after select(), and @__kml_net_keepalive holds the
// loop open while one is open. No-op stubs (emitLoopTaskStubs) stand in when
// the program hands out none. The sockets live in one process-wide registry
// (@__kml_net_conn_*); each socket's 'data'/'end'/'close' listeners are raw
// closure headers the dispatch invokes directly, the same posture as
// child_process.
package llvm

import (
	"fmt"
)

// netSocketIR: 0 i64 fd (-1 after close/EOF) · 1 i64 state (0 open · 1 closed)
// · 2 ptr 'data' listener · 3 ptr 'end' listener · 4 spare · 5 ptr SSL*
// (null for plaintext; set on an HTTPS server's upgraded socket) · 6 ptr
// 'close' listener (fired once — on EOF teardown or an explicit close/destroy
// — then cleared, ADR-00501) · 7 spare · 8 ptr 'error' listener · 9 spare.
const netSocketIR = "{ i64, i64, ptr, ptr, ptr, ptr, ptr, ptr, ptr, ptr }"
const netSocketStructSize = 80

// ensureNtohs declares ntohs exactly once — both the net and dgram runtimes
// need it, and duplicate declarations are an LLVM redefinition error.
func (e *Emitter) ensureNtohs() {
	if e.usedNtohs {
		return
	}
	e.usedNtohs = true
	e.emitGlobal("declare i16 @ntohs(i16 noundef)")
}

// ensureNetSockIO emits the two low-level net.Socket byte-IO helpers
// (__kml_net_sock_write, __kml_net_sock_close) and nothing else, so a path that
// hands out a net.Socket without standing up a net server (the HTTP
// 'clientError'/'connection' events) can still support socket.write/end/destroy.
// The full net runtime calls this too; the flag keeps it single-definition.
// __kml_tls_write/free are resolved (real extern or no-op stub) by
// emitTLSNetSymbols in the finalize pass, whose gate includes usedNetSockIO.
func (e *Emitter) ensureNetSockIO() {
	if e.usedNetSockIO {
		return
	}
	e.usedNetSockIO = true
	e.ensureWriteDecl()
	e.ensureCloseDecl()
	sock := netSocketIR
	// __kml_net_sock_write(sock, data, n): write n bytes to the connection fd
	// (no-op once closed).
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_net_sock_write(ptr %%sock, ptr %%data, i64 %%n) {
entry:
  %%fd_p = getelementptr %[1]s, ptr %%sock, i32 0, i32 0
  %%fd64 = load i64, ptr %%fd_p, align 8
  %%open = icmp sge i64 %%fd64, 0
  br i1 %%open, label %%wr, label %%ret
wr:
  %%fd = trunc i64 %%fd64 to i32
  %%ssl_p = getelementptr %[1]s, ptr %%sock, i32 0, i32 5
  %%ssl = load ptr, ptr %%ssl_p, align 8
  %%istls = icmp ne ptr %%ssl, null
  br i1 %%istls, label %%wtls, label %%wraw
wtls:
  call i64 @__kml_tls_write(ptr %%ssl, ptr %%data, i64 %%n)
  br label %%ret
wraw:
  call i64 @write(i32 %%fd, ptr %%data, i64 %%n)
  br label %%ret
ret:
  ret void
}
define void @__kml_net_sock_close(ptr %%sock) {
entry:
  %%fd_p = getelementptr %[1]s, ptr %%sock, i32 0, i32 0
  %%fd64 = load i64, ptr %%fd_p, align 8
  %%open = icmp sge i64 %%fd64, 0
  br i1 %%open, label %%cl, label %%ret
cl:
  %%fd = trunc i64 %%fd64 to i32
  %%ssl_p = getelementptr %[1]s, ptr %%sock, i32 0, i32 5
  %%ssl = load ptr, ptr %%ssl_p, align 8
  %%istls = icmp ne ptr %%ssl, null
  br i1 %%istls, label %%cfree, label %%craw
cfree:
  call void @__kml_tls_free(ptr %%ssl)
  store ptr null, ptr %%ssl_p, align 8
  br label %%craw
craw:
  call i32 @close(i32 %%fd)
  store i64 -1, ptr %%fd_p, align 8
  %%st_p = getelementptr %[1]s, ptr %%sock, i32 0, i32 1
  store i64 1, ptr %%st_p, align 8
  %%clsn_p = getelementptr { i64, i64, ptr, ptr, ptr, ptr, ptr, ptr }, ptr %%sock, i32 0, i32 6
  %%clsn = load ptr, ptr %%clsn_p, align 8
  %%hasclsn = icmp ne ptr %%clsn, null
  br i1 %%hasclsn, label %%firecl, label %%ret
firecl:
  store ptr null, ptr %%clsn_p, align 8
  %%clsnfp_p = getelementptr { ptr, ptr }, ptr %%clsn, i32 0, i32 0
  %%clsnfp = load ptr, ptr %%clsnfp_p, align 8
  %%clsnep_p = getelementptr { ptr, ptr }, ptr %%clsn, i32 0, i32 1
  %%clsnep = load ptr, ptr %%clsnep_p, align 8
  call void %%clsnfp(ptr %%clsnep)
  br label %%ret
ret:
  ret void
}`, sock))
}

func (e *Emitter) ensureNetRuntime() {
	if e.usedNetRuntime {
		return
	}
	e.usedNetRuntime = true
	e.ensureStrHeaderRuntime()
	e.ensureMalloc()
	e.ensureCalloc()
	e.ensureRealloc()
	e.ensureMemcpy()
	e.ensureMemset()
	e.ensureCloseDecl()
	e.ensureReadDecl()
	e.ensureWriteDecl()
	e.ensureFcntlDecl()
	e.ensureExceptionHelpers()
	e.ensureHTTPRuntime()    // socket/setsockopt/bind/listen/accept/htons decls + the event loop
	e.ensureWorkerFdSetbit() // shared @__kml_worker_fd_setbit

	e.emitGlobal("@__kml_net_conn_data = internal global ptr null, align 8")
	e.emitGlobal("@__kml_net_conn_len = internal global i64 0, align 8")
	e.emitGlobal("@__kml_net_conn_cap = internal global i64 0, align 8")

	sock := netSocketIR

	// __kml_net_conn_register: append a connection to the process-wide
	// registry (realloc-doubling, same shape as __kml_cp_register).
	e.emitGlobal(`
define void @__kml_net_conn_register(ptr %c) {
entry:
  %len = load i64, ptr @__kml_net_conn_len, align 8
  %cap = load i64, ptr @__kml_net_conn_cap, align 8
  %full = icmp sge i64 %len, %cap
  br i1 %full, label %grow, label %store
grow:
  %cap2 = mul i64 %cap, 2
  %atleast4 = icmp sgt i64 %cap2, 4
  %newcap = select i1 %atleast4, i64 %cap2, i64 4
  %olddata = load ptr, ptr @__kml_net_conn_data, align 8
  %bytes = mul i64 %newcap, 8
  %newdata = call ptr @realloc(ptr %olddata, i64 %bytes)
  store ptr %newdata, ptr @__kml_net_conn_data, align 8
  store i64 %newcap, ptr @__kml_net_conn_cap, align 8
  br label %store
store:
  %data = load ptr, ptr @__kml_net_conn_data, align 8
  %slot = getelementptr ptr, ptr %data, i64 %len
  store ptr %c, ptr %slot, align 8
  %newlen = add i64 %len, 1
  store i64 %newlen, ptr @__kml_net_conn_len, align 8
  ret void
}`)

	// The two low-level byte-IO helpers (__kml_net_sock_write/close) live in
	// ensureNetSockIO so a path that hands out a net.Socket without standing up a
	// net server (an HTTP 'clientError' listener calling socket.end()) can pull
	// in just those two.
	e.ensureNetSockIO()

	// __kml_net_keepalive(): true while any connection is still open.
	e.emitGlobal(fmt.Sprintf(`
define i1 @__kml_net_keepalive() {
entry:
  %%clen = load i64, ptr @__kml_net_conn_len, align 8
  %%cdata = load ptr, ptr @__kml_net_conn_data, align 8
  %%ci = alloca i64, align 8
  store i64 0, ptr %%ci, align 8
  br label %%cloop
cloop:
  %%civ = load i64, ptr %%ci, align 8
  %%cinb = icmp slt i64 %%civ, %%clen
  br i1 %%cinb, label %%cbody, label %%no
cbody:
  %%cslot = getelementptr ptr, ptr %%cdata, i64 %%civ
  %%sk = load ptr, ptr %%cslot, align 8
  %%st_p = getelementptr %s, ptr %%sk, i32 0, i32 1
  %%st = load i64, ptr %%st_p, align 8
  %%copen = icmp eq i64 %%st, 0
  br i1 %%copen, label %%yes, label %%cnext
cnext:
  %%cinext = add i64 %%civ, 1
  store i64 %%cinext, ptr %%ci, align 8
  br label %%cloop
yes:
  ret i1 1
no:
  ret i1 0
}`, sock))

	// __kml_net_fdset_add(fdset, maxfd): add every open connection fd to the
	// read set. Never forces a zero timeout.
	e.emitGlobal(fmt.Sprintf(`
define i1 @__kml_net_fdset_add(ptr %%fdset, ptr %%maxfd) {
entry:
  %%force = alloca i1, align 1
  store i1 0, ptr %%force, align 1
  %%clen = load i64, ptr @__kml_net_conn_len, align 8
  %%cdata = load ptr, ptr @__kml_net_conn_data, align 8
  %%ci = alloca i64, align 8
  store i64 0, ptr %%ci, align 8
  br label %%cloop
cloop:
  %%civ = load i64, ptr %%ci, align 8
  %%cinb = icmp slt i64 %%civ, %%clen
  br i1 %%cinb, label %%cbody, label %%done
cbody:
  %%cslot = getelementptr ptr, ptr %%cdata, i64 %%civ
  %%sk = load ptr, ptr %%cslot, align 8
  %%fd_p = getelementptr %s, ptr %%sk, i32 0, i32 0
  %%fd64 = load i64, ptr %%fd_p, align 8
  %%copen = icmp sge i64 %%fd64, 0
  br i1 %%copen, label %%caddfd, label %%cnext
caddfd:
  %%fd32 = trunc i64 %%fd64 to i32
  call void @__kml_worker_fd_setbit(i32 %%fd32, ptr %%fdset, ptr %%maxfd)
  br label %%cnext
cnext:
  %%cinext = add i64 %%civ, 1
  store i64 %%cinext, ptr %%ci, align 8
  br label %%cloop
done:
  %%f = load i1, ptr %%force, align 1
  ret i1 %%f
}`, sock))

	// __kml_net_dispatch(): drain every open connection socket — each socket's
	// 'data' listener gets a fresh Buffer, 'end' then 'close' fire on EOF.
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_net_dispatch() {
entry:
  %%clen = load i64, ptr @__kml_net_conn_len, align 8
  %%cdata = load ptr, ptr @__kml_net_conn_data, align 8
  %%ci = alloca i64, align 8
  store i64 0, ptr %%ci, align 8
  %%chunk = alloca [4096 x i8], align 1
  %%chunkptr = getelementptr [4096 x i8], ptr %%chunk, i32 0, i32 0
  br label %%cloop
cloop:
  %%civ = load i64, ptr %%ci, align 8
  %%cinb = icmp slt i64 %%civ, %%clen
  br i1 %%cinb, label %%cbody, label %%done
cbody:
  %%cslot = getelementptr ptr, ptr %%cdata, i64 %%civ
  %%sk2 = load ptr, ptr %%cslot, align 8
  br label %%rloop
rloop:
  %%fd_p = getelementptr %s, ptr %%sk2, i32 0, i32 0
  %%fd64 = load i64, ptr %%fd_p, align 8
  %%closed = icmp slt i64 %%fd64, 0
  br i1 %%closed, label %%cnext, label %%doread
doread:
  %%fd = trunc i64 %%fd64 to i32
  %%rssl_p = getelementptr { i64, i64, ptr, ptr, ptr, ptr }, ptr %%sk2, i32 0, i32 5
  %%rssl = load ptr, ptr %%rssl_p, align 8
  %%ristls = icmp ne ptr %%rssl, null
  br i1 %%ristls, label %%rtls, label %%rraw
rtls:
  %%ntls = call i64 @__kml_tls_read(ptr %%rssl, ptr %%chunkptr, i64 4096)
  br label %%readdone
rraw:
  %%nraw = call i64 @read(i32 %%fd, ptr %%chunkptr, i64 4096)
  br label %%readdone
readdone:
  %%n = phi i64 [ %%ntls, %%rtls ], [ %%nraw, %%rraw ]
  %%hasdata = icmp sgt i64 %%n, 0
  br i1 %%hasdata, label %%ondata, label %%ckeof
ondata:
  %%dl_p = getelementptr %s, ptr %%sk2, i32 0, i32 2
  %%dl = load ptr, ptr %%dl_p, align 8
  %%hasdl = icmp ne ptr %%dl, null
  br i1 %%hasdl, label %%firedata, label %%rloop
firedata:
  ; TDD-00120: length-prefixed chunk. A (chunk: string) listener binds this
  ; pointer directly, so it carries the 8-byte length header (ptr-8) like every
  ; other string, and stays NUL-terminated for strlen consumers. The listener is
  ; still called with length n, so Buffer consumers are unaffected.
  %%buf = call ptr @__kml_str_alloc(i64 %%n)
  call ptr @memcpy(ptr %%buf, ptr %%chunkptr, i64 %%n)
  %%bufend = getelementptr i8, ptr %%buf, i64 %%n
  store i8 0, ptr %%bufend, align 1
  %%dfp_p = getelementptr { ptr, ptr }, ptr %%dl, i32 0, i32 0
  %%dfp = load ptr, ptr %%dfp_p, align 8
  %%dep_p = getelementptr { ptr, ptr }, ptr %%dl, i32 0, i32 1
  %%dep = load ptr, ptr %%dep_p, align 8
  call void %%dfp(ptr %%dep, ptr %%buf, i64 %%n)
  br label %%rloop
ckeof:
  %%iseof = icmp eq i64 %%n, 0
  br i1 %%iseof, label %%oneof, label %%cnext
oneof:
  %%essl_p = getelementptr { i64, i64, ptr, ptr, ptr, ptr }, ptr %%sk2, i32 0, i32 5
  %%essl = load ptr, ptr %%essl_p, align 8
  call void @__kml_tls_free(ptr %%essl)
  call i32 @close(i32 %%fd)
  store i64 -1, ptr %%fd_p, align 8
  %%st_p = getelementptr %s, ptr %%sk2, i32 0, i32 1
  store i64 1, ptr %%st_p, align 8
  %%el_p = getelementptr %s, ptr %%sk2, i32 0, i32 3
  %%el = load ptr, ptr %%el_p, align 8
  %%hasel = icmp ne ptr %%el, null
  br i1 %%hasel, label %%fireend, label %%eofclose
fireend:
  %%efp_p = getelementptr { ptr, ptr }, ptr %%el, i32 0, i32 0
  %%efp = load ptr, ptr %%efp_p, align 8
  %%eep_p = getelementptr { ptr, ptr }, ptr %%el, i32 0, i32 1
  %%eep = load ptr, ptr %%eep_p, align 8
  call void %%efp(ptr %%eep)
  br label %%eofclose
eofclose:
  ; 'close' fires after 'end' (Node ordering), listener or not (ADR-00501).
  %%ecl_p = getelementptr { i64, i64, ptr, ptr, ptr, ptr, ptr, ptr }, ptr %%sk2, i32 0, i32 6
  %%ecl = load ptr, ptr %%ecl_p, align 8
  %%ehascl = icmp ne ptr %%ecl, null
  br i1 %%ehascl, label %%eoffirecl, label %%cnext
eoffirecl:
  store ptr null, ptr %%ecl_p, align 8
  %%eclfp_p = getelementptr { ptr, ptr }, ptr %%ecl, i32 0, i32 0
  %%eclfp = load ptr, ptr %%eclfp_p, align 8
  %%eclep_p = getelementptr { ptr, ptr }, ptr %%ecl, i32 0, i32 1
  %%eclep = load ptr, ptr %%eclep_p, align 8
  call void %%eclfp(ptr %%eclep)
  br label %%cnext
cnext:
  %%cinext = add i64 %%civ, 1
  store i64 %%cinext, ptr %%ci, align 8
  br label %%cloop
done:
  ret void
}`, sock, sock, sock, sock))

	// __kml_net_sockname_port: getsockname(fd) → the host-order local port, or
	// -1 on error. Backs server.address()/socket.address() and, crucially, the
	// `listen(0)` ephemeral-port idiom (bind picks the port; this reads it back).
	e.ensureNtohs()
	e.emitGlobal(`
declare i32 @getsockname(i32, ptr, ptr)
define i32 @__kml_net_sockname_port(i32 %fd) {
entry:
  %addr = alloca [16 x i8], align 4
  %lenp = alloca i32, align 4
  store i32 16, ptr %lenp, align 4
  %rc = call i32 @getsockname(i32 %fd, ptr %addr, ptr %lenp)
  %ok = icmp eq i32 %rc, 0
  br i1 %ok, label %read, label %fail
read:
  %portp = getelementptr i8, ptr %addr, i64 2
  %portn = load i16, ptr %portp, align 1
  %porth = call i16 @ntohs(i16 %portn)
  %port32 = zext i16 %porth to i32
  ret i32 %port32
fail:
  ret i32 -1
}`)

	// __kml_net_sockname_addr: getsockname(fd) → the local IPv4 address as a
	// length-prefixed string ("0.0.0.0" for a wildcard-bound server, the real
	// peer-local IP like "127.0.0.1" for a connected socket), or "" on error.
	// sockaddr_in's sin_addr is at byte offset 4 on both Linux and BSD/macOS.
	e.ensureStrHeaderRuntime()
	e.emitGlobal(`
declare ptr @inet_ntop(i32, ptr, ptr, i32)
define ptr @__kml_net_sockname_addr(i32 %fd) {
entry:
  %addr = alloca [16 x i8], align 4
  %tmp = alloca [46 x i8], align 1
  %lenp = alloca i32, align 4
  store i32 16, ptr %lenp, align 4
  %rc = call i32 @getsockname(i32 %fd, ptr %addr, ptr %lenp)
  %ok = icmp eq i32 %rc, 0
  br i1 %ok, label %conv, label %fail
conv:
  %sinaddr = getelementptr i8, ptr %addr, i64 4
  %res = call ptr @inet_ntop(i32 2, ptr %sinaddr, ptr %tmp, i32 46)
  %resok = icmp ne ptr %res, null
  br i1 %resok, label %box, label %fail
box:
  %boxed = call ptr @__kml_str_from_cstr(ptr %tmp)
  ret ptr %boxed
fail:
  %empty = call ptr @__kml_str_from_cstr(ptr @.kml_net_emptystr)
  ret ptr %empty
}
@.kml_net_emptystr = private unnamed_addr constant [1 x i8] c"\00"`)

}

// netKeepAliveConst returns the platform's (SOL_SOCKET, SO_KEEPALIVE) pair for
// setsockopt (macOS 0xffff/0x0008, Linux 1/9).
func (e *Emitter) netKeepAliveConst() (solSocket, soKeepAlive int) {
	sol, _ := e.httpSockConstants()
	if e.opts.Target.OS() == "darwin" {
		return sol, 0x0008
	}
	return sol, 9
}

// netKeepIdleConst returns the (IPPROTO_TCP, keepalive-idle option) pair for
// the setsockopt threading setKeepAlive's idle time. IPPROTO_TCP is 6
// everywhere; the option is macOS's TCP_KEEPALIVE (0x10) and Linux's
// TCP_KEEPIDLE (4). Windows uses the Linux number, which the win32 shim
// remaps to SIO_KEEPALIVE_VALS — a bare setsockopt(4) there is TCP_MAXSEG,
// not a keepalive control (ADR-00760).
func (e *Emitter) netKeepIdleConst() (ipprotoTCP, tcpKeepIdle int) {
	if e.opts.Target.OS() == "darwin" {
		return 6, 0x10
	}
	return 6, 4
}
