// runtime_net.go — Node `net` TCP server: net.createServer/listen plus the
// connection Socket surface (socket.on('data'|'end'), socket.write, socket.end).
//
// A listening server's fd and every accepted connection's fd are made
// non-blocking and folded into the central select() event loop exactly like
// the child_process read pipes (runtime_childprocess.go): @__kml_net_fdset_add
// adds the listen fd and every live connection fd, @__kml_net_dispatch accepts
// pending connections and drains readable sockets after select(),
// @__kml_net_keepalive holds the loop open while a server listens or a
// connection is open. No-op stubs (emitLoopTaskStubs) stand in when the
// program never creates a server.
//
// Two process-wide registries: servers (@__kml_net_srv_*) and connection
// sockets (@__kml_net_conn_*). The connection listener and each socket's
// 'data'/'end' listeners are stored as raw closure headers the dispatch
// invokes directly, the same posture as child_process. Server-side only for
// V1 — net.connect (client) is not implemented (would need write-fd readiness
// inspection the loop does not do for non-curl fds).
package llvm

import "fmt"

// netServerIR: 0 i64 listenfd (-1 before listen / after close) · 1 ptr
// connection listener (closure header, or null) · 2 i64 closed (0 open · 1).
const netServerIR = "{ i64, ptr, i64, ptr }" // field 3 = server SSL_CTX* (null for plaintext; set by tls.createServer, TDD-00110)

// netSocketIR: 0 i64 fd (-1 after close/EOF) · 1 i64 state (0 open · 1 closed)
// · 2 ptr 'data' listener · 3 ptr 'end' listener · 4 ptr pending 'connect'
// listener (client sockets only; fired once on the first dispatch pass so it
// runs after net.connect returns and the socket variable is bound, then
// cleared — server-accepted sockets leave it null).
const netSocketIR = "{ i64, i64, ptr, ptr, ptr, ptr }" // field 5 = SSL* (null for plaintext; set for a tls.connect socket, TDD-00109)

func (e *Emitter) ensureNetRuntime() {
	if e.usedNetRuntime {
		return
	}
	e.usedNetRuntime = true
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
	e.ensureDNSRuntime()     // @__kml_dns_lookup, for net.connect's host resolution
	// @connect / @inet_pton are declared by the WebSocket-client runtime, which
	// ensureHTTPRuntime always pulls in — reused by __kml_net_connect below.

	e.emitGlobal("@__kml_net_srv_data = internal global ptr null, align 8")
	e.emitGlobal("@__kml_net_srv_len = internal global i64 0, align 8")
	e.emitGlobal("@__kml_net_srv_cap = internal global i64 0, align 8")
	e.emitGlobal("@__kml_net_conn_data = internal global ptr null, align 8")
	e.emitGlobal("@__kml_net_conn_len = internal global i64 0, align 8")
	e.emitGlobal("@__kml_net_conn_cap = internal global i64 0, align 8")

	srv := netServerIR
	sock := netSocketIR
	solSocket, soReuseAddr := httpSockConstants()
	fam0, fam1 := httpSockaddrFamilyBytes()
	nonblock := httpNonblockFlag()

	// __kml_net_srv_register / __kml_net_conn_register: append a handle to the
	// process-wide registry (realloc-doubling, same shape as __kml_cp_register).
	e.emitGlobal(`
define void @__kml_net_srv_register(ptr %s) {
entry:
  %len = load i64, ptr @__kml_net_srv_len, align 8
  %cap = load i64, ptr @__kml_net_srv_cap, align 8
  %full = icmp sge i64 %len, %cap
  br i1 %full, label %grow, label %store
grow:
  %cap2 = mul i64 %cap, 2
  %atleast4 = icmp sgt i64 %cap2, 4
  %newcap = select i1 %atleast4, i64 %cap2, i64 4
  %olddata = load ptr, ptr @__kml_net_srv_data, align 8
  %bytes = mul i64 %newcap, 8
  %newdata = call ptr @realloc(ptr %olddata, i64 %bytes)
  store ptr %newdata, ptr @__kml_net_srv_data, align 8
  store i64 %newcap, ptr @__kml_net_srv_cap, align 8
  br label %store
store:
  %data = load ptr, ptr @__kml_net_srv_data, align 8
  %slot = getelementptr ptr, ptr %data, i64 %len
  store ptr %s, ptr %slot, align 8
  %newlen = add i64 %len, 1
  store i64 %newlen, ptr @__kml_net_srv_len, align 8
  ret void
}
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

	// __kml_net_bind_and_listen(port): create a non-blocking listening TCP
	// socket bound to 0.0.0.0:port. Returns the fd, or -1 on failure (unlike
	// http's throwing bind, net.listen returns and the caller decides). Copies
	// the sockaddr_in construction from __kml_http_bind_and_listen.
	e.emitGlobal(fmt.Sprintf(`
define i32 @__kml_net_bind_and_listen(i32 %%port) {
entry:
  %%fd = call i32 @socket(i32 2, i32 1, i32 0)
  %%fdok = icmp sge i32 %%fd, 0
  br i1 %%fdok, label %%setopt, label %%fail
setopt:
  %%one = alloca i32, align 4
  store i32 1, ptr %%one, align 4
  call i32 @setsockopt(i32 %%fd, i32 %d, i32 %d, ptr %%one, i32 4)
  %%addr = alloca [16 x i8], align 4
  call ptr @memset(ptr %%addr, i32 0, i64 16)
  store i8 %d, ptr %%addr, align 1
  %%b1p = getelementptr i8, ptr %%addr, i64 1
  store i8 %d, ptr %%b1p, align 1
  %%portu16 = trunc i32 %%port to i16
  %%portn = call i16 @htons(i16 %%portu16)
  %%portp = getelementptr i8, ptr %%addr, i64 2
  store i16 %%portn, ptr %%portp, align 1
  %%bindrc = call i32 @bind(i32 %%fd, ptr %%addr, i32 16)
  %%bindok = icmp eq i32 %%bindrc, 0
  br i1 %%bindok, label %%dolisten, label %%failfd
dolisten:
  %%listenrc = call i32 @listen(i32 %%fd, i32 128)
  %%listenok = icmp eq i32 %%listenrc, 0
  br i1 %%listenok, label %%nonblock, label %%failfd
nonblock:
  %%fl = call i32 (i32, i32, ...) @fcntl(i32 %%fd, i32 3)
  %%fln = or i32 %%fl, %d
  call i32 (i32, i32, ...) @fcntl(i32 %%fd, i32 4, i32 %%fln)
  ret i32 %%fd
failfd:
  call i32 @close(i32 %%fd)
  ret i32 -1
fail:
  ret i32 -1
}`, solSocket, soReuseAddr, fam0, fam1, nonblock))

	// __kml_net_connect(port, host): resolve host, create a TCP socket, and
	// perform a BLOCKING connect() (the same V1 simplification the WebSocket
	// client uses — no non-blocking-connect/write-set machinery in the loop).
	// On success the fd is switched to non-blocking and registered as an
	// ordinary connection socket, so all the read/'data'/'end'/write/close
	// machinery applies unchanged. Returns the socket handle, or null on
	// resolution/socket/connect failure.
	e.emitGlobal(fmt.Sprintf(`
define ptr @__kml_net_connect(i32 %%port, ptr %%host) {
entry:
  %%ip = call ptr @__kml_dns_lookup(ptr %%host)
  %%ipok = icmp ne ptr %%ip, null
  br i1 %%ipok, label %%mksock, label %%failnull
mksock:
  %%fd = call i32 @socket(i32 2, i32 1, i32 0)
  %%fdok = icmp sge i32 %%fd, 0
  br i1 %%fdok, label %%build, label %%failnull
build:
  %%addr = alloca [16 x i8], align 4
  call ptr @memset(ptr %%addr, i32 0, i64 16)
  store i8 %d, ptr %%addr, align 1
  %%b1p = getelementptr i8, ptr %%addr, i64 1
  store i8 %d, ptr %%b1p, align 1
  %%portu16 = trunc i32 %%port to i16
  %%portn = call i16 @htons(i16 %%portu16)
  %%portp = getelementptr i8, ptr %%addr, i64 2
  store i16 %%portn, ptr %%portp, align 1
  %%sinaddr = getelementptr i8, ptr %%addr, i64 4
  call i32 @inet_pton(i32 2, ptr %%ip, ptr %%sinaddr)
  %%connrc = call i32 @connect(i32 %%fd, ptr %%addr, i32 16)
  %%connok = icmp eq i32 %%connrc, 0
  br i1 %%connok, label %%connected, label %%failfd
connected:
  %%fl = call i32 (i32, i32, ...) @fcntl(i32 %%fd, i32 3)
  %%fln = or i32 %%fl, %d
  call i32 (i32, i32, ...) @fcntl(i32 %%fd, i32 4, i32 %%fln)
  %%sk = call ptr @calloc(i64 1, i64 48)
  %%fd_p = getelementptr %s, ptr %%sk, i32 0, i32 0
  %%fd64 = sext i32 %%fd to i64
  store i64 %%fd64, ptr %%fd_p, align 8
  call void @__kml_net_conn_register(ptr %%sk)
  ret ptr %%sk
failfd:
  call i32 @close(i32 %%fd)
  ret ptr null
failnull:
  ret ptr null
}`, fam0, fam1, nonblock, sock))

	// __kml_net_sock_write(sock, data, n): write n bytes to the connection fd
	// (no-op once closed).
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_net_sock_write(ptr %%sock, ptr %%data, i64 %%n) {
entry:
  %%fd_p = getelementptr %s, ptr %%sock, i32 0, i32 0
  %%fd64 = load i64, ptr %%fd_p, align 8
  %%open = icmp sge i64 %%fd64, 0
  br i1 %%open, label %%wr, label %%ret
wr:
  %%fd = trunc i64 %%fd64 to i32
  %%ssl_p = getelementptr %s, ptr %%sock, i32 0, i32 5
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
  %%fd_p = getelementptr %s, ptr %%sock, i32 0, i32 0
  %%fd64 = load i64, ptr %%fd_p, align 8
  %%open = icmp sge i64 %%fd64, 0
  br i1 %%open, label %%cl, label %%ret
cl:
  %%fd = trunc i64 %%fd64 to i32
  %%ssl_p = getelementptr %s, ptr %%sock, i32 0, i32 5
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
  %%st_p = getelementptr %s, ptr %%sock, i32 0, i32 1
  store i64 1, ptr %%st_p, align 8
  br label %%ret
ret:
  ret void
}`, sock, sock, sock, sock, sock))

	// __kml_net_keepalive(): true while any server is listening (fd >= 0,
	// not closed) or any connection is still open.
	e.emitGlobal(fmt.Sprintf(`
define i1 @__kml_net_keepalive() {
entry:
  %%slen = load i64, ptr @__kml_net_srv_len, align 8
  %%sdata = load ptr, ptr @__kml_net_srv_data, align 8
  %%si = alloca i64, align 8
  store i64 0, ptr %%si, align 8
  br label %%sloop
sloop:
  %%siv = load i64, ptr %%si, align 8
  %%sinb = icmp slt i64 %%siv, %%slen
  br i1 %%sinb, label %%sbody, label %%conns
sbody:
  %%sslot = getelementptr ptr, ptr %%sdata, i64 %%siv
  %%srv = load ptr, ptr %%sslot, align 8
  %%lfd_p = getelementptr %s, ptr %%srv, i32 0, i32 0
  %%lfd = load i64, ptr %%lfd_p, align 8
  %%listening = icmp sge i64 %%lfd, 0
  br i1 %%listening, label %%yes, label %%snext
snext:
  %%sinext = add i64 %%siv, 1
  store i64 %%sinext, ptr %%si, align 8
  br label %%sloop
conns:
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
}`, srv, sock))

	// __kml_net_fdset_add(fdset, maxfd): add every listening server fd and
	// every open connection fd. Never forces a zero timeout.
	e.emitGlobal(fmt.Sprintf(`
define i1 @__kml_net_fdset_add(ptr %%fdset, ptr %%maxfd) {
entry:
  %%force = alloca i1, align 1
  store i1 0, ptr %%force, align 1
  %%slen = load i64, ptr @__kml_net_srv_len, align 8
  %%sdata = load ptr, ptr @__kml_net_srv_data, align 8
  %%si = alloca i64, align 8
  store i64 0, ptr %%si, align 8
  br label %%sloop
sloop:
  %%siv = load i64, ptr %%si, align 8
  %%sinb = icmp slt i64 %%siv, %%slen
  br i1 %%sinb, label %%sbody, label %%conns
sbody:
  %%sslot = getelementptr ptr, ptr %%sdata, i64 %%siv
  %%srv = load ptr, ptr %%sslot, align 8
  %%lfd_p = getelementptr %s, ptr %%srv, i32 0, i32 0
  %%lfd = load i64, ptr %%lfd_p, align 8
  %%listening = icmp sge i64 %%lfd, 0
  br i1 %%listening, label %%saddfd, label %%snext
saddfd:
  %%lfd32 = trunc i64 %%lfd to i32
  call void @__kml_worker_fd_setbit(i32 %%lfd32, ptr %%fdset, ptr %%maxfd)
  br label %%snext
snext:
  %%sinext = add i64 %%siv, 1
  store i64 %%sinext, ptr %%si, align 8
  br label %%sloop
conns:
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
  ; a socket with a pending 'connect' listener forces a zero select() timeout,
  ; so dispatch fires the listener (which does the client's initial write)
  ; without first blocking in select() on a not-yet-readable fd.
  %%conl_p = getelementptr %s, ptr %%sk, i32 0, i32 4
  %%conl = load ptr, ptr %%conl_p, align 8
  %%haspend = icmp ne ptr %%conl, null
  br i1 %%haspend, label %%setforce, label %%cnext
setforce:
  store i1 1, ptr %%force, align 1
  br label %%cnext
cnext:
  %%cinext = add i64 %%civ, 1
  store i64 %%cinext, ptr %%ci, align 8
  br label %%cloop
done:
  %%f = load i1, ptr %%force, align 1
  ret i1 %%f
}`, srv, sock, sock))

	// __kml_net_dispatch(): accept pending connections on every listening
	// server (non-blocking accept loops until EAGAIN), then drain every open
	// connection socket. Fires the connection listener with the new socket,
	// each socket's 'data' listener with a fresh Buffer, and 'end' on EOF.
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_net_dispatch() {
entry:
  ; --- accept phase ---
  %%slen = load i64, ptr @__kml_net_srv_len, align 8
  %%sdata = load ptr, ptr @__kml_net_srv_data, align 8
  %%si = alloca i64, align 8
  store i64 0, ptr %%si, align 8
  br label %%sloop
sloop:
  %%siv = load i64, ptr %%si, align 8
  %%sinb = icmp slt i64 %%siv, %%slen
  br i1 %%sinb, label %%sbody, label %%drainphase
sbody:
  %%sslot = getelementptr ptr, ptr %%sdata, i64 %%siv
  %%srv = load ptr, ptr %%sslot, align 8
  %%lfd_p = getelementptr %s, ptr %%srv, i32 0, i32 0
  %%lfd64 = load i64, ptr %%lfd_p, align 8
  %%listening = icmp sge i64 %%lfd64, 0
  br i1 %%listening, label %%acc, label %%snext
acc:
  %%lfd = trunc i64 %%lfd64 to i32
  %%newfd = call i32 @accept(i32 %%lfd, ptr null, ptr null)
  %%accok = icmp sge i32 %%newfd, 0
  br i1 %%accok, label %%onconn, label %%snext
onconn:
  ; TLS server (TDD-00110): if the server has an SSL_CTX, do a blocking SSL_accept
  ; on the still-blocking accepted fd; drop the connection on handshake failure.
  %%ctx_p = getelementptr { i64, ptr, i64, ptr }, ptr %%srv, i32 0, i32 3
  %%sctx = load ptr, ptr %%ctx_p, align 8
  %%istls = icmp ne ptr %%sctx, null
  br i1 %%istls, label %%tlsacc, label %%doconn
tlsacc:
  %%ssl = call ptr @__kml_tls_server_accept(ptr %%sctx, i32 %%newfd)
  %%sslok = icmp ne ptr %%ssl, null
  br i1 %%sslok, label %%doconn, label %%tlsfail
tlsfail:
  call i32 @close(i32 %%newfd)
  br label %%acc
doconn:
  %%sslval = phi ptr [ null, %%onconn ], [ %%ssl, %%tlsacc ]
  ; make the accepted fd non-blocking
  %%nfl = call i32 (i32, i32, ...) @fcntl(i32 %%newfd, i32 3)
  %%nfln = or i32 %%nfl, %d
  call i32 (i32, i32, ...) @fcntl(i32 %%newfd, i32 4, i32 %%nfln)
  ; build the socket handle
  %%sk = call ptr @calloc(i64 1, i64 48)
  %%skfd_p = getelementptr %s, ptr %%sk, i32 0, i32 0
  %%newfd64 = sext i32 %%newfd to i64
  store i64 %%newfd64, ptr %%skfd_p, align 8
  %%sk_ssl_p = getelementptr { i64, i64, ptr, ptr, ptr, ptr }, ptr %%sk, i32 0, i32 5
  store ptr %%sslval, ptr %%sk_ssl_p, align 8
  call void @__kml_net_conn_register(ptr %%sk)
  ; fire the server's connection listener (socket)
  %%cl_p = getelementptr %s, ptr %%srv, i32 0, i32 1
  %%cl = load ptr, ptr %%cl_p, align 8
  %%hascl = icmp ne ptr %%cl, null
  br i1 %%hascl, label %%firecl, label %%acc
firecl:
  %%clfp_p = getelementptr { ptr, ptr }, ptr %%cl, i32 0, i32 0
  %%clfp = load ptr, ptr %%clfp_p, align 8
  %%clep_p = getelementptr { ptr, ptr }, ptr %%cl, i32 0, i32 1
  %%clep = load ptr, ptr %%clep_p, align 8
  call void %%clfp(ptr %%clep, ptr %%sk)
  br label %%acc
snext:
  %%sinext = add i64 %%siv, 1
  store i64 %%sinext, ptr %%si, align 8
  br label %%sloop
drainphase:
  ; --- drain phase ---
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
  ; fire a pending 'connect' listener once (client sockets), then clear it, so
  ; it runs after net.connect returned and the socket variable is bound.
  %%conl_p = getelementptr { i64, i64, ptr, ptr, ptr }, ptr %%sk2, i32 0, i32 4
  %%conl = load ptr, ptr %%conl_p, align 8
  %%hascon = icmp ne ptr %%conl, null
  br i1 %%hascon, label %%firecon, label %%rloop
firecon:
  store ptr null, ptr %%conl_p, align 8
  %%confp_p = getelementptr { ptr, ptr }, ptr %%conl, i32 0, i32 0
  %%confp = load ptr, ptr %%confp_p, align 8
  %%conep_p = getelementptr { ptr, ptr }, ptr %%conl, i32 0, i32 1
  %%conep = load ptr, ptr %%conep_p, align 8
  call void %%confp(ptr %%conep)
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
  %%buf = call ptr @malloc(i64 %%n)
  call ptr @memcpy(ptr %%buf, ptr %%chunkptr, i64 %%n)
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
  br i1 %%hasel, label %%fireend, label %%cnext
fireend:
  %%efp_p = getelementptr { ptr, ptr }, ptr %%el, i32 0, i32 0
  %%efp = load ptr, ptr %%efp_p, align 8
  %%eep_p = getelementptr { ptr, ptr }, ptr %%el, i32 0, i32 1
  %%eep = load ptr, ptr %%eep_p, align 8
  call void %%efp(ptr %%eep)
  br label %%cnext
cnext:
  %%cinext = add i64 %%civ, 1
  store i64 %%cinext, ptr %%ci, align 8
  br label %%cloop
done:
  ret void
}`, srv, nonblock, sock, srv, sock, sock, sock, sock))
}
