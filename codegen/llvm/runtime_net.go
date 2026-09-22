// runtime_net.go — Node `net` TCP server + client: net.createServer/listen plus
// the connection Socket surface (socket.on('data'|'end'), socket.write,
// socket.end) and the asynchronous net.connect client.
//
// A listening server's fd and every accepted/connecting connection's fd are made
// non-blocking and folded into the central select() event loop exactly like
// the child_process read pipes (runtime_childprocess.go): @__kml_net_fdset_add
// adds the listen fd and every live connection fd (read-interest),
// @__kml_net_conn_wset_add adds every still-connecting client fd to the write +
// except sets (connect-completion readiness), @__kml_net_dispatch accepts
// pending connections, completes in-progress connects, and drains readable
// sockets after select(), @__kml_net_keepalive holds the loop open while a
// server listens or a connection is open or connecting. No-op stubs
// (emitLoopTaskStubs) stand in when the program never creates a server.
//
// Two process-wide registries: servers (@__kml_net_srv_*) and connection
// sockets (@__kml_net_conn_*). The connection listener and each socket's
// 'data'/'end'/'error' listeners are stored as raw closure headers the dispatch
// invokes directly, the same posture as child_process.
//
// net.connect is fully asynchronous (ADR-01021): connect() is issued
// non-blocking, the socket is returned immediately, and the connect either
// completes (→ 'connect'/'ready') or fails (→ 'error' with a coded Error, then
// 'close') on a later dispatch pass — never blocking the loop, never throwing.
package llvm

import (
	"fmt"
)

// netServerIR: 0 i64 listenfd (-1 before listen / after close) · 1 ptr
// connection listener (closure header, or null) · 2 i64 closed (0 open · 1) ·
// 3 ptr server SSL_CTX* (null for plaintext; set by tls.createServer,
// TDD-00110) · 4 ptr 'listening' listener (closure header, or null).
const netServerIR = "{ i64, ptr, i64, ptr, ptr }"
const netServerStructSize = 40

// netSocketIR: 0 i64 fd (-1 after close/EOF) · 1 i64 state (0 open · 1 closed)
// · 2 ptr 'data' listener · 3 ptr 'end' listener · 4 ptr pending 'connect'
// listener (client sockets only; fired once when the connect completes so it
// runs after net.connect returns and the socket variable is bound, then
// cleared — server-accepted sockets leave it null) · 5 ptr SSL* (null for
// plaintext; set for a tls.connect socket, TDD-00109) · 6 ptr 'close'
// listener (fired once — on EOF teardown or an explicit close/destroy —
// then cleared, ADR-00501) · 7 spare · 8 ptr 'error' listener (client sockets;
// fired on an async connect failure with a coded Error, ADR-01021) · 9 ptr
// connect-state blob (netConnStateIR, or null for server/accepted/tls sockets
// that never run the async-connect path).
const netSocketIR = "{ i64, i64, ptr, ptr, ptr, ptr, ptr, ptr, ptr, ptr }"
const netSocketStructSize = 80

// netConnStateIR: the async-connect state a client socket carries in field 9
// while its non-blocking connect() is outstanding. 0 i32 status
// (netConnConnecting / netConnDone / netConnErr / netConnDNSErr) · 1 i32
// addrlen (the sockaddr length for the connect() retry) · 2 i32 err (the errno
// captured on a connect failure; 0 otherwise) · 3 i32 pad · 4 [128 x i8] addr
// (the target sockaddr, replayed by the connect() completion retry). Heap blob
// (calloc), freed when the connect resolves.
const netConnStateIR = "{ i32, i32, i32, i32, [128 x i8] }"
const netConnStateSize = 144

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
}`, sock, sock, sock, sock, sock))
}

func (e *Emitter) ensureNetRuntime() {
	if e.usedNetRuntime {
		return
	}
	e.usedNetRuntime = true
	e.ensureStrHeaderRuntime()
	e.ensureMalloc()
	e.ensureCalloc()
	e.ensureFree() // dispatch frees the connect-state blob when a connect resolves
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
	e.ensureStrlen()         // for __kml_net_connect_unix's sun_path length
	e.ensureMemcpy()         // for __kml_net_connect_unix's sun_path copy
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
	connstate := netConnStateIR
	solSocket, soReuseAddr := httpSockConstants()
	fam0, fam1 := httpSockaddrFamilyBytes()
	nonblock := httpNonblockFlag()
	e.ensureErrnoAccessor() // async connect reads errno to classify EINPROGRESS vs failure
	einprog, ewouldblk, ealready, eintr, eisconn := netConnectErrnos()
	soErr := netSOError() // getsockopt(SO_ERROR) for connect-completion failure detection
	e.emitGlobal("declare i32 @getsockopt(i32, i32, i32, ptr, ptr)")

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

	// __kml_net_connect(port, host): asynchronous TCP connect (ADR-01021). The
	// socket is created non-blocking, connect() is issued without blocking, and a
	// connect-state blob (field 9) records the outcome the dispatch pass will act
	// on: netConnConnecting (in progress → completed via the connect()-retry idiom
	// in dispatch, then 'connect'/'ready'), netConnErr (immediate connect failure,
	// errno stored), or netConnDNSErr (host did not resolve; the host string is
	// stashed in the addr blob for the 'error' message). Always returns a socket —
	// never blocks, never throws; a failure surfaces as an async 'error' event.
	// Status enum: 0 = done/connected · 1 = connecting · 2 = connect error · 3 = DNS error.
	e.emitGlobal(fmt.Sprintf(`
define ptr @__kml_net_connect(i32 %%port, ptr %%host) {
entry:
  %%sk = call ptr @calloc(i64 1, i64 80)
  %%fd_p = getelementptr %[1]s, ptr %%sk, i32 0, i32 0
  store i64 -1, ptr %%fd_p, align 8
  %%cs = call ptr @calloc(i64 1, i64 144)
  %%cs_p = getelementptr %[1]s, ptr %%sk, i32 0, i32 9
  store ptr %%cs, ptr %%cs_p, align 8
  %%ip = call ptr @__kml_dns_lookup(ptr %%host)
  %%ipok = icmp ne ptr %%ip, null
  br i1 %%ipok, label %%mksock, label %%dnsfail
mksock:
  %%fd = call i32 @socket(i32 2, i32 1, i32 0)
  %%fdok = icmp sge i32 %%fd, 0
  br i1 %%fdok, label %%build, label %%sockfail
build:
  %%fl = call i32 (i32, i32, ...) @fcntl(i32 %%fd, i32 3)
  %%fln = or i32 %%fl, %[4]d
  call i32 (i32, i32, ...) @fcntl(i32 %%fd, i32 4, i32 %%fln)
  %%addr = getelementptr %[2]s, ptr %%cs, i32 0, i32 4
  store i8 %[5]d, ptr %%addr, align 1
  %%b1p = getelementptr i8, ptr %%addr, i64 1
  store i8 %[6]d, ptr %%b1p, align 1
  %%portu16 = trunc i32 %%port to i16
  %%portn = call i16 @htons(i16 %%portu16)
  %%portp = getelementptr i8, ptr %%addr, i64 2
  store i16 %%portn, ptr %%portp, align 1
  %%sinaddr = getelementptr i8, ptr %%addr, i64 4
  call i32 @inet_pton(i32 2, ptr %%ip, ptr %%sinaddr)
  %%alen_p = getelementptr %[2]s, ptr %%cs, i32 0, i32 1
  store i32 16, ptr %%alen_p, align 4
  %%fd64 = sext i32 %%fd to i64
  store i64 %%fd64, ptr %%fd_p, align 8
  %%connrc = call i32 @connect(i32 %%fd, ptr %%addr, i32 16)
  %%connok = icmp eq i32 %%connrc, 0
  br i1 %%connok, label %%connecting, label %%chkerr
chkerr:
  %%errptr = call ptr @%[3]s()
  %%e = load i32, ptr %%errptr, align 4
  %%ipa = icmp eq i32 %%e, %[7]d
  %%ipb = icmp eq i32 %%e, %[8]d
  %%ipc = icmp eq i32 %%e, %[9]d
  %%ipd = icmp eq i32 %%e, %[10]d
  %%p1 = or i1 %%ipa, %%ipb
  %%p2 = or i1 %%p1, %%ipc
  %%inprog = or i1 %%p2, %%ipd
  br i1 %%inprog, label %%connecting, label %%connerr
connecting:
  %%st_p = getelementptr %[2]s, ptr %%cs, i32 0, i32 0
  store i32 1, ptr %%st_p, align 4
  br label %%reg
connerr:
  %%este_p = getelementptr %[2]s, ptr %%cs, i32 0, i32 0
  store i32 2, ptr %%este_p, align 4
  %%err_p = getelementptr %[2]s, ptr %%cs, i32 0, i32 2
  store i32 %%e, ptr %%err_p, align 4
  br label %%reg
sockfail:
  %%serrptr = call ptr @%[3]s()
  %%se = load i32, ptr %%serrptr, align 4
  %%sst_p = getelementptr %[2]s, ptr %%cs, i32 0, i32 0
  store i32 2, ptr %%sst_p, align 4
  %%serr_p = getelementptr %[2]s, ptr %%cs, i32 0, i32 2
  store i32 %%se, ptr %%serr_p, align 4
  br label %%reg
dnsfail:
  %%dst_p = getelementptr %[2]s, ptr %%cs, i32 0, i32 0
  store i32 3, ptr %%dst_p, align 4
  %%dhost = getelementptr %[2]s, ptr %%cs, i32 0, i32 4
  %%hlen = call i64 @strlen(ptr %%host)
  %%hcap = icmp ult i64 %%hlen, 127
  %%hcp = select i1 %%hcap, i64 %%hlen, i64 127
  call ptr @memcpy(ptr %%dhost, ptr %%host, i64 %%hcp)
  br label %%reg
reg:
  call void @__kml_net_conn_register(ptr %%sk)
  ret ptr %%sk
}`, sock, connstate, errnoAccessor(), nonblock, fam0, fam1, einprog, ewouldblk, ealready, eintr))

	// __kml_net_connect_unix(path): AF_UNIX (Unix-domain socket) asynchronous
	// connect — Node's IPC `net.connect({ path })`, ADR-01021. The same
	// connect-state machinery as the TCP path: non-blocking connect() into the
	// socket's field-9 blob, completed (or failed → 'error') on a later dispatch
	// pass. sockaddr_un's sun_path starts at offset 2 on both Linux and macOS; the
	// two bytes before it differ (Linux: a 2-byte sa_family_t; macOS/BSD: a 1-byte
	// sun_len + 1-byte sun_family), so the family bytes are stamped per-platform.
	// NB: famStore is spliced in via %[5]s below, so its LLVM locals use a single
	// `%` (they are not run back through Sprintf's %%-reduction).
	famStore := "  store i16 1, ptr %addr, align 2\n" // Linux: sa_family_t = AF_UNIX(1)
	if targetGOOS() == "darwin" {
		// macOS: sun_len (offset 0) = the address length, sun_family (offset 1) = AF_UNIX.
		famStore = "  %lenb = trunc i64 %alen64 to i8\n" +
			"  store i8 %lenb, ptr %addr, align 1\n" +
			"  %famp = getelementptr i8, ptr %addr, i64 1\n" +
			"  store i8 1, ptr %famp, align 1\n"
	}
	e.emitGlobal(fmt.Sprintf(`
define ptr @__kml_net_connect_unix(ptr %%path) {
entry:
  %%sk = call ptr @calloc(i64 1, i64 80)
  %%fd_p = getelementptr %[1]s, ptr %%sk, i32 0, i32 0
  store i64 -1, ptr %%fd_p, align 8
  %%cs = call ptr @calloc(i64 1, i64 144)
  %%cs_p = getelementptr %[1]s, ptr %%sk, i32 0, i32 9
  store ptr %%cs, ptr %%cs_p, align 8
  %%len = call i64 @strlen(ptr %%path)
  %%fd = call i32 @socket(i32 1, i32 1, i32 0)
  %%fdok = icmp sge i32 %%fd, 0
  br i1 %%fdok, label %%build, label %%sockfail
build:
  %%fl = call i32 (i32, i32, ...) @fcntl(i32 %%fd, i32 3)
  %%fln = or i32 %%fl, %[4]d
  call i32 (i32, i32, ...) @fcntl(i32 %%fd, i32 4, i32 %%fln)
  %%addr = getelementptr %[2]s, ptr %%cs, i32 0, i32 4
  %%cap = icmp ult i64 %%len, 104
  %%cplen = select i1 %%cap, i64 %%len, i64 104
  %%pathdst = getelementptr i8, ptr %%addr, i64 2
  call ptr @memcpy(ptr %%pathdst, ptr %%path, i64 %%cplen)
  %%alen64 = add i64 %%cplen, 3
%[5]s  %%alen = trunc i64 %%alen64 to i32
  %%alen_p = getelementptr %[2]s, ptr %%cs, i32 0, i32 1
  store i32 %%alen, ptr %%alen_p, align 4
  %%fd64 = sext i32 %%fd to i64
  store i64 %%fd64, ptr %%fd_p, align 8
  %%connrc = call i32 @connect(i32 %%fd, ptr %%addr, i32 %%alen)
  %%connok = icmp eq i32 %%connrc, 0
  br i1 %%connok, label %%connecting, label %%chkerr
chkerr:
  %%errptr = call ptr @%[3]s()
  %%e = load i32, ptr %%errptr, align 4
  %%ipa = icmp eq i32 %%e, %[6]d
  %%ipb = icmp eq i32 %%e, %[7]d
  %%ipc = icmp eq i32 %%e, %[8]d
  %%ipd = icmp eq i32 %%e, %[9]d
  %%p1 = or i1 %%ipa, %%ipb
  %%p2 = or i1 %%p1, %%ipc
  %%inprog = or i1 %%p2, %%ipd
  br i1 %%inprog, label %%connecting, label %%connerr
connecting:
  %%st_p = getelementptr %[2]s, ptr %%cs, i32 0, i32 0
  store i32 1, ptr %%st_p, align 4
  br label %%reg
connerr:
  %%este_p = getelementptr %[2]s, ptr %%cs, i32 0, i32 0
  store i32 2, ptr %%este_p, align 4
  %%err_p = getelementptr %[2]s, ptr %%cs, i32 0, i32 2
  store i32 %%e, ptr %%err_p, align 4
  br label %%reg
sockfail:
  %%serrptr = call ptr @%[3]s()
  %%se = load i32, ptr %%serrptr, align 4
  %%sst_p = getelementptr %[2]s, ptr %%cs, i32 0, i32 0
  store i32 2, ptr %%sst_p, align 4
  %%serr_p = getelementptr %[2]s, ptr %%cs, i32 0, i32 2
  store i32 %%se, ptr %%serr_p, align 4
  br label %%reg
reg:
  call void @__kml_net_conn_register(ptr %%sk)
  ret ptr %%sk
}`, sock, connstate, errnoAccessor(), nonblock, famStore, einprog, ewouldblk, ealready, eintr))

	// The two low-level byte-IO helpers (__kml_net_sock_write/close) live in
	// ensureNetSockIO so a path that hands out a net.Socket without standing up a
	// net server (an HTTP 'clientError' listener calling socket.end()) can pull
	// in just those two.
	e.ensureNetSockIO()

	// __kml_net_conn_errobj(status, err, addr): build the Node Error the async
	// connect failure path (ADR-01021) delivers through the socket 'error' event.
	// status 3 (DNS) → `getaddrinfo ENOTFOUND <host>` (the host string was stashed
	// in the addr blob), code ENOTFOUND, syscall getaddrinfo, errno -3008 (libuv's
	// UV_EAI_NONAME). status 2 (connect) → `connect <CODE> <ip>:<port>` from the
	// captured errno, code via __kml_errno_code, syscall connect, errno the
	// negative platform errno, plus err.address/err.port (fields 11/12). calloc'd
	// so unset fields (errstr/path/dest) default cleanly.
	e.ensureNtohs()
	e.ensureSprintf()
	{
		eIR := errorObjType.StructIR()
		esz := errorObjType.StructSize()
		errName := e.internString("Error")
		codeNotfound := e.internString("ENOTFOUND")
		scGetaddr := e.internString("getaddrinfo")
		scConnect := e.internString("connect")
		fmtDns := e.internString("getaddrinfo ENOTFOUND %s")
		fmtConn := e.internString("connect %s %s:%d")
		e.emitGlobal(fmt.Sprintf(`
declare ptr @inet_ntop(i32, ptr, ptr, i32)
define ptr @__kml_net_conn_errobj(i32 %%status, i32 %%err, ptr %%addr) {
entry:
  %%obj = call ptr @calloc(i64 1, i64 %[1]d)
  %%kind = getelementptr %[2]s, ptr %%obj, i32 0, i32 0
  store i64 0, ptr %%kind, align 8
  %%nm = getelementptr %[2]s, ptr %%obj, i32 0, i32 2
  store ptr %[3]s, ptr %%nm, align 8
  %%cause = getelementptr %[2]s, ptr %%obj, i32 0, i32 10
  store i64 %[4]d, ptr %%cause, align 8
  %%isdns = icmp eq i32 %%status, 3
  br i1 %%isdns, label %%dns, label %%conn
dns:
  %%dmsg = call ptr @__kml_str_alloc(i64 160)
  call i32 (ptr, ptr, ...) @sprintf(ptr %%dmsg, ptr %[5]s, ptr %%addr)
  call void @__kml_str_finalize(ptr %%dmsg)
  %%dm = getelementptr %[2]s, ptr %%obj, i32 0, i32 1
  store ptr %%dmsg, ptr %%dm, align 8
  %%dc = getelementptr %[2]s, ptr %%obj, i32 0, i32 3
  store ptr %[6]s, ptr %%dc, align 8
  %%dsc = getelementptr %[2]s, ptr %%obj, i32 0, i32 6
  store ptr %[7]s, ptr %%dsc, align 8
  %%den = getelementptr %[2]s, ptr %%obj, i32 0, i32 8
  store double -3.008e3, ptr %%den, align 8
  ret ptr %%obj
conn:
  %%tmp = alloca [46 x i8], align 1
  %%sinaddr = getelementptr i8, ptr %%addr, i64 4
  call ptr @inet_ntop(i32 2, ptr %%sinaddr, ptr %%tmp, i32 46)
  %%ipbox = call ptr @__kml_str_from_cstr(ptr %%tmp)
  %%portp = getelementptr i8, ptr %%addr, i64 2
  %%portn = load i16, ptr %%portp, align 1
  %%porth = call i16 @ntohs(i16 %%portn)
  %%port32 = zext i16 %%porth to i32
  %%code = call ptr @__kml_errno_code(i32 %%err)
  %%cmsg = call ptr @__kml_str_alloc(i64 96)
  call i32 (ptr, ptr, ...) @sprintf(ptr %%cmsg, ptr %[8]s, ptr %%code, ptr %%tmp, i32 %%port32)
  call void @__kml_str_finalize(ptr %%cmsg)
  %%cm = getelementptr %[2]s, ptr %%obj, i32 0, i32 1
  store ptr %%cmsg, ptr %%cm, align 8
  %%cc = getelementptr %[2]s, ptr %%obj, i32 0, i32 3
  store ptr %%code, ptr %%cc, align 8
  %%csc = getelementptr %[2]s, ptr %%obj, i32 0, i32 6
  store ptr %[9]s, ptr %%csc, align 8
  %%neg = call i32 @__kml_uv_errno(i32 %%err)
  %%negd = sitofp i32 %%neg to double
  %%cen = getelementptr %[2]s, ptr %%obj, i32 0, i32 8
  store double %%negd, ptr %%cen, align 8
  %%addrf = getelementptr %[2]s, ptr %%obj, i32 0, i32 11
  store ptr %%ipbox, ptr %%addrf, align 8
  %%portf = getelementptr %[2]s, ptr %%obj, i32 0, i32 12
  %%portd = sitofp i32 %%port32 to double
  store double %%portd, ptr %%portf, align 8
  ret ptr %%obj
}`, esz, eIR, errName, nbUndefined, fmtDns, codeNotfound, scGetaddr, fmtConn, scConnect))
	}
	e.ensureErrnoCode() // __kml_net_conn_errobj maps the captured errno → err.code

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
	// every open connection fd to the read set. A still-connecting client fd is
	// added too (harmless — it is not readable until connected); its connect
	// completion is driven by the write/except sets (__kml_net_conn_wset_add).
	// Never forces a zero timeout.
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
  br label %%cnext
cnext:
  %%cinext = add i64 %%civ, 1
  store i64 %%cinext, ptr %%ci, align 8
  br label %%cloop
done:
  %%f = load i1, ptr %%force, align 1
  ret i1 %%f
}`, srv, sock))

	// __kml_net_conn_wset_add(wfdset, efdset, maxfd): add every still-connecting
	// client fd to the write set (connect completes when the socket becomes
	// writable) and the except set (connect *failure* is reported there on the
	// POSIX "writable-then-SO_ERROR / excepted" contract). Returns force=true when
	// any socket already carries a resolved error (immediate connect error, or a
	// DNS failure whose fd is -1 and so appears in no fd_set) so the loop takes a
	// zero-timeout pass and dispatch delivers the 'error' event promptly.
	e.emitGlobal(fmt.Sprintf(`
define i1 @__kml_net_conn_wset_add(ptr %%wfdset, ptr %%efdset, ptr %%maxfd) {
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
  %%cs_p = getelementptr %[1]s, ptr %%sk, i32 0, i32 9
  %%cs = load ptr, ptr %%cs_p, align 8
  %%hascs = icmp ne ptr %%cs, null
  br i1 %%hascs, label %%chkstatus, label %%cnext
chkstatus:
  %%stp = getelementptr %[2]s, ptr %%cs, i32 0, i32 0
  %%status = load i32, ptr %%stp, align 4
  %%connecting = icmp eq i32 %%status, 1
  br i1 %%connecting, label %%addw, label %%chkerr
addw:
  %%fd_p = getelementptr %[1]s, ptr %%sk, i32 0, i32 0
  %%fd64 = load i64, ptr %%fd_p, align 8
  %%fdok = icmp sge i64 %%fd64, 0
  br i1 %%fdok, label %%doaddw, label %%cnext
doaddw:
  %%fd32 = trunc i64 %%fd64 to i32
  call void @__kml_worker_fd_setbit(i32 %%fd32, ptr %%wfdset, ptr %%maxfd)
  call void @__kml_worker_fd_setbit(i32 %%fd32, ptr %%efdset, ptr %%maxfd)
  br label %%cnext
chkerr:
  %%iserr = icmp sge i32 %%status, 2
  br i1 %%iserr, label %%setforce, label %%cnext
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
}`, sock, connstate))

	// connBlock is the per-socket connect-completion logic spliced into the drain
	// loop's %%cbody below (ADR-01021). It replaces the old "fire the pending
	// connect listener unconditionally" step. A socket carrying connect-state
	// (field 9) is either still connecting (completed via the portable connect()-
	// retry idiom → EISCONN done, EINPROGRESS/EWOULDBLOCK/EALREADY/EINTR keep
	// waiting, else a real error), already failed (build the coded Error → 'error'
	// or uncaught throw, then close + 'close'), or done (fall through to the read
	// drain). A socket without connect-state (server-accepted / tls.connect) keeps
	// the legacy field-4 fire-once behavior. Built as its own indexed-arg Sprintf
	// and spliced as a single %%s so the outer function's positional args are
	// undisturbed; it branches into the outer %%rloop / %%cnext labels.
	connBlock := fmt.Sprintf(`cbody:
  %%cslot = getelementptr ptr, ptr %%cdata, i64 %%civ
  %%sk2 = load ptr, ptr %%cslot, align 8
  %%cs_p = getelementptr %[1]s, ptr %%sk2, i32 0, i32 9
  %%cs = load ptr, ptr %%cs_p, align 8
  %%hascs = icmp ne ptr %%cs, null
  br i1 %%hascs, label %%asyncc, label %%legacyc
legacyc:
  %%lconl_p = getelementptr %[1]s, ptr %%sk2, i32 0, i32 4
  %%lconl = load ptr, ptr %%lconl_p, align 8
  %%lhascon = icmp ne ptr %%lconl, null
  br i1 %%lhascon, label %%firecon, label %%rloop
firecon:
  store ptr null, ptr %%lconl_p, align 8
  %%confp_p = getelementptr { ptr, ptr }, ptr %%lconl, i32 0, i32 0
  %%confp = load ptr, ptr %%confp_p, align 8
  %%conep_p = getelementptr { ptr, ptr }, ptr %%lconl, i32 0, i32 1
  %%conep = load ptr, ptr %%conep_p, align 8
  call void %%confp(ptr %%conep)
  br label %%rloop
asyncc:
  %%stp = getelementptr %[2]s, ptr %%cs, i32 0, i32 0
  %%status = load i32, ptr %%stp, align 4
  %%isconning = icmp eq i32 %%status, 1
  br i1 %%isconning, label %%complc, label %%chkfail
chkfail:
  %%isfail = icmp sge i32 %%status, 2
  br i1 %%isfail, label %%connfail, label %%rloop
complc:
  ; Completion is decided portably: getsockopt(SO_ERROR) reports a connect
  ; *failure* on every platform (Windows does not surface it through a second
  ; connect(), the POSIX retry idiom's blind spot). SO_ERROR == 0 then means
  ; either connected or still in progress, disambiguated by the connect() retry
  ; (rc 0 / EISCONN = connected; anything else = keep waiting).
  %%cfd_p = getelementptr %[1]s, ptr %%sk2, i32 0, i32 0
  %%cfd64 = load i64, ptr %%cfd_p, align 8
  %%cfd = trunc i64 %%cfd64 to i32
  %%soerr_p = alloca i32, align 4
  %%solen_p = alloca i32, align 4
  store i32 4, ptr %%solen_p, align 4
  call i32 @getsockopt(i32 %%cfd, i32 %[5]d, i32 %[6]d, ptr %%soerr_p, ptr %%solen_p)
  %%soerr = load i32, ptr %%soerr_p, align 4
  %%sofail = icmp ne i32 %%soerr, 0
  br i1 %%sofail, label %%complso, label %%compltry
complso:
  store i32 2, ptr %%stp, align 4
  %%cerrp = getelementptr %[2]s, ptr %%cs, i32 0, i32 2
  store i32 %%soerr, ptr %%cerrp, align 4
  br label %%connfail
compltry:
  %%caddr = getelementptr %[2]s, ptr %%cs, i32 0, i32 4
  %%calen_p = getelementptr %[2]s, ptr %%cs, i32 0, i32 1
  %%calen = load i32, ptr %%calen_p, align 4
  %%crc = call i32 @connect(i32 %%cfd, ptr %%caddr, i32 %%calen)
  %%crcok = icmp eq i32 %%crc, 0
  br i1 %%crcok, label %%conndone, label %%compltry2
compltry2:
  %%ceptr = call ptr @%[3]s()
  %%ce = load i32, ptr %%ceptr, align 4
  %%isconn = icmp eq i32 %%ce, %[4]d
  br i1 %%isconn, label %%conndone, label %%cnext
conndone:
  store ptr null, ptr %%cs_p, align 8
  call void @free(ptr %%cs)
  %%dconl_p = getelementptr %[1]s, ptr %%sk2, i32 0, i32 4
  %%dconl = load ptr, ptr %%dconl_p, align 8
  %%dhascon = icmp ne ptr %%dconl, null
  br i1 %%dhascon, label %%firecon2, label %%rloop
firecon2:
  store ptr null, ptr %%dconl_p, align 8
  %%dconfp_p = getelementptr { ptr, ptr }, ptr %%dconl, i32 0, i32 0
  %%dconfp = load ptr, ptr %%dconfp_p, align 8
  %%dconep_p = getelementptr { ptr, ptr }, ptr %%dconl, i32 0, i32 1
  %%dconep = load ptr, ptr %%dconep_p, align 8
  call void %%dconfp(ptr %%dconep)
  br label %%rloop
connfail:
  %%fstatus = load i32, ptr %%stp, align 4
  %%ferr_p = getelementptr %[2]s, ptr %%cs, i32 0, i32 2
  %%ferr = load i32, ptr %%ferr_p, align 4
  %%faddr = getelementptr %[2]s, ptr %%cs, i32 0, i32 4
  %%errobj = call ptr @__kml_net_conn_errobj(i32 %%fstatus, i32 %%ferr, ptr %%faddr)
  store ptr null, ptr %%cs_p, align 8
  call void @free(ptr %%cs)
  %%ffd_p = getelementptr %[1]s, ptr %%sk2, i32 0, i32 0
  %%ffd64 = load i64, ptr %%ffd_p, align 8
  %%ffdopen = icmp sge i64 %%ffd64, 0
  br i1 %%ffdopen, label %%fclose, label %%fmark
fclose:
  %%ffd = trunc i64 %%ffd64 to i32
  call i32 @close(i32 %%ffd)
  store i64 -1, ptr %%ffd_p, align 8
  br label %%fmark
fmark:
  %%fst_p = getelementptr %[1]s, ptr %%sk2, i32 0, i32 1
  store i64 1, ptr %%fst_p, align 8
  %%el8_p = getelementptr %[1]s, ptr %%sk2, i32 0, i32 8
  %%el8 = load ptr, ptr %%el8_p, align 8
  %%hasel8 = icmp ne ptr %%el8, null
  br i1 %%hasel8, label %%fireerr, label %%uncaught
fireerr:
  %%efp8_p = getelementptr { ptr, ptr }, ptr %%el8, i32 0, i32 0
  %%efp8 = load ptr, ptr %%efp8_p, align 8
  %%eep8_p = getelementptr { ptr, ptr }, ptr %%el8, i32 0, i32 1
  %%eep8 = load ptr, ptr %%eep8_p, align 8
  call void %%efp8(ptr %%eep8, ptr %%errobj)
  br label %%fireclose
uncaught:
  call void @__kml_throw(ptr %%errobj)
  br label %%fireclose
fireclose:
  %%cl6_p = getelementptr %[1]s, ptr %%sk2, i32 0, i32 6
  %%cl6 = load ptr, ptr %%cl6_p, align 8
  %%hascl6 = icmp ne ptr %%cl6, null
  br i1 %%hascl6, label %%fireclose2, label %%cnext
fireclose2:
  store ptr null, ptr %%cl6_p, align 8
  %%cl6fp_p = getelementptr { ptr, ptr }, ptr %%cl6, i32 0, i32 0
  %%cl6fp = load ptr, ptr %%cl6fp_p, align 8
  %%cl6ep_p = getelementptr { ptr, ptr }, ptr %%cl6, i32 0, i32 1
  %%cl6ep = load ptr, ptr %%cl6ep_p, align 8
  call void %%cl6fp(ptr %%cl6ep)
  br label %%cnext`, sock, connstate, errnoAccessor(), eisconn, solSocket, soErr)

	// __kml_net_dispatch(): accept pending connections on every listening
	// server (non-blocking accept loops until EAGAIN), complete any in-progress
	// async connects and deliver their 'connect'/'error' events (connBlock),
	// then drain every open connection socket. Fires the connection listener with
	// the new socket, each socket's 'data' listener with a fresh Buffer, and
	// 'end' on EOF.
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
  %%sk = call ptr @calloc(i64 1, i64 80)
  %%skfd_p = getelementptr %s, ptr %%sk, i32 0, i32 0
  %%newfd64 = sext i32 %%newfd to i64
  store i64 %%newfd64, ptr %%skfd_p, align 8
  %%sk_ssl_p = getelementptr { i64, i64, ptr, ptr, ptr, ptr }, ptr %%sk, i32 0, i32 5
  store ptr %%sslval, ptr %%sk_ssl_p, align 8
  call void @__kml_net_conn_register(ptr %%sk)
  ; fire the server's connection listener (socket)
  %%clsn_p = getelementptr %s, ptr %%srv, i32 0, i32 1
  %%clsn = load ptr, ptr %%clsn_p, align 8
  %%hasclsn = icmp ne ptr %%clsn, null
  br i1 %%hasclsn, label %%firecl, label %%acc
firecl:
  %%clsnfp_p = getelementptr { ptr, ptr }, ptr %%clsn, i32 0, i32 0
  %%clsnfp = load ptr, ptr %%clsnfp_p, align 8
  %%clsnep_p = getelementptr { ptr, ptr }, ptr %%clsn, i32 0, i32 1
  %%clsnep = load ptr, ptr %%clsnep_p, align 8
  call void %%clsnfp(ptr %%clsnep, ptr %%sk)
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
%s
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
}`, srv, nonblock, sock, srv, connBlock, sock, sock, sock, sock))

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

	// __kml_net_is_ip: 4 if s parses as an IPv4 literal, 6 if IPv6, else 0 —
	// backs net.isIP/isIPv4/isIPv6.
	// __kml_net_v4_strict: Node rejects leading-zero octets ('001.2.3.4') in
	// dotted-decimal IPv4 while macOS's inet_pton accepts them — this
	// pre-check rejects any octet-initial '0' followed by another digit
	// (ADR-00457, found via the Node oracle's test-net-isipv4).
	e.emitGlobal(`
define i1 @__kml_net_v4_strict(ptr %s) {
entry:
  br label %loop
loop:
  %i = phi i64 [0, %entry], [%inext, %cont]
  %prev = phi i8 [46, %entry], [%c, %cont]
  %p = getelementptr i8, ptr %s, i64 %i
  %c = load i8, ptr %p, align 1
  %isnul = icmp eq i8 %c, 0
  br i1 %isnul, label %ok, label %chk
chk:
  %iszero = icmp eq i8 %c, 48
  %afterdot = icmp eq i8 %prev, 46
  %lead = and i1 %iszero, %afterdot
  br i1 %lead, label %chknext, label %cont
chknext:
  %in2 = add i64 %i, 1
  %pn = getelementptr i8, ptr %s, i64 %in2
  %cn = load i8, ptr %pn, align 1
  %ge0 = icmp sge i8 %cn, 48
  %le9 = icmp sle i8 %cn, 57
  %dig = and i1 %ge0, %le9
  br i1 %dig, label %bad, label %cont
cont:
  %inext = add i64 %i, 1
  br label %loop
ok:
  ret i1 1
bad:
  ret i1 0
}`)
	// __kml_net_v6_strict: rejects two shapes the platform inet_pton is lax
	// about vs Node — a hex group longer than 4 digits, and leading-zero
	// octets in an embedded dotted-v4 tail (checked via v4_strict from the
	// current group's start). ADR-00457.
	e.emitGlobal(`
define i1 @__kml_net_v6_strict(ptr %s) {
entry:
  br label %loop
loop:
  %i = phi i64 [0, %entry], [%inext, %cont]
  %run = phi i64 [0, %entry], [%runNext, %cont]
  %gs = phi i64 [0, %entry], [%gsNext, %cont]
  %p = getelementptr i8, ptr %s, i64 %i
  %c = load i8, ptr %p, align 1
  %isnul = icmp eq i8 %c, 0
  br i1 %isnul, label %ok, label %chk
chk:
  %iscolon = icmp eq i8 %c, 58
  br i1 %iscolon, label %reset, label %chkdot
chkdot:
  %isdot = icmp eq i8 %c, 46
  br i1 %isdot, label %v4, label %chkpct
chkpct:
  %ispct = icmp eq i8 %c, 37
  br i1 %ispct, label %ok, label %grow
grow:
  %run1 = add i64 %run, 1
  %toolong = icmp sgt i64 %run1, 4
  br i1 %toolong, label %bad, label %cont
reset:
  %gs1 = add i64 %i, 1
  br label %cont
cont:
  %runNext = phi i64 [%run1, %grow], [0, %reset]
  %gsNext = phi i64 [%gs, %grow], [%gs1, %reset]
  %inext = add i64 %i, 1
  br label %loop
v4:
  %gp = getelementptr i8, ptr %s, i64 %gs
  %v4ok = call i1 @__kml_net_v4_strict(ptr %gp)
  ret i1 %v4ok
ok:
  ret i1 1
bad:
  ret i1 0
}`)
	// __kml_net_pton6z: inet_pton(AF_INET6) with a Node-style zone ID
	// (`fe80::1%%eth0`) stripped first — the platform inet_pton rejects the
	// %%zone suffix that Node accepts (ADR-00457).
	e.emitGlobal(fmt.Sprintf(`
define i32 @__kml_net_pton6z(ptr %%s, ptr %%buf) {
entry:
  %%zb = alloca [48 x i8], align 1
  br label %%scan
scan:
  %%i = phi i64 [0, %%entry], [%%in, %%cont]
  %%p = getelementptr i8, ptr %%s, i64 %%i
  %%c = load i8, ptr %%p, align 1
  %%ispct = icmp eq i8 %%c, 37
  br i1 %%ispct, label %%term, label %%chknul
chknul:
  %%isnul = icmp eq i8 %%c, 0
  br i1 %%isnul, label %%term, label %%store
store:
  %%toolong = icmp sge i64 %%i, 46
  br i1 %%toolong, label %%fail, label %%st2
st2:
  %%q = getelementptr i8, ptr %%zb, i64 %%i
  store i8 %%c, ptr %%q, align 1
  br label %%cont
cont:
  %%in = add i64 %%i, 1
  br label %%scan
term:
  %%qe = getelementptr i8, ptr %%zb, i64 %%i
  store i8 0, ptr %%qe, align 1
  %%r = call i32 @inet_pton(i32 %d, ptr %%zb, ptr %%buf)
  ret i32 %%r
fail:
  ret i32 0
}`, netAFInet6()))
	e.emitGlobal(`
define i32 @__kml_net_is_ip(ptr %s) {
entry:
  %buf = alloca [16 x i8], align 4
  %strict = call i1 @__kml_net_v4_strict(ptr %s)
  br i1 %strict, label %do4, label %try6
do4:
  %r4 = call i32 @inet_pton(i32 2, ptr %s, ptr %buf)
  %is4 = icmp eq i32 %r4, 1
  br i1 %is4, label %ret4, label %try6
try6:
  %s6 = call i1 @__kml_net_v6_strict(ptr %s)
  br i1 %s6, label %do6, label %ret0
do6:
  %r6 = call i32 @__kml_net_pton6z(ptr %s, ptr %buf)
  %is6 = icmp eq i32 %r6, 1
  br i1 %is6, label %ret6, label %ret0
ret4:
  ret i32 4
ret6:
  ret i32 6
ret0:
  ret i32 0
}`)
}

// netConnectErrnos returns the platform's (EINPROGRESS, EWOULDBLOCK, EALREADY,
// EINTR, EISCONN) errno values — the set the async-connect state machine
// classifies a non-blocking connect() by. Windows uses the win32 shim's Linux
// errno namespace (win32io.c L_E*), so it shares the Linux column; macOS/BSD
// carry their own numbers. Host-only, matching netAFInet6's no-cross-compile note.
func netConnectErrnos() (einprogress, ewouldblock, ealready, eintr, eisconn int) {
	if targetGOOS() == "darwin" {
		return 36, 35, 37, 4, 56
	}
	return 115, 11, 114, 4, 106 // linux + windows (shim)
}

// netSOError returns the platform's SO_ERROR option value for getsockopt
// (Linux 4; macOS/BSD 0x1007). On Windows the shim's getsockopt accepts the
// Linux value 4 (L_SO_ERROR), translates it to WS_SO_ERROR, and maps the
// returned code back into the Linux errno namespace — so 4 is correct there too.
// The level is always httpSockConstants()'s SOL_SOCKET.
func netSOError() int {
	if targetGOOS() == "darwin" {
		return 0x1007
	}
	return 4 // linux + windows (shim)
}

// netAFInet6 is the platform's AF_INET6 value (macOS 30, Linux 10) — host-only,
// since this compiler doesn't cross-compile.
func netAFInet6() int {
	if targetGOOS() == "darwin" {
		return 30
	}
	return 10
}

// netKeepAliveConst returns the platform's (SOL_SOCKET, SO_KEEPALIVE) pair for
// setsockopt (macOS 0xffff/0x0008, Linux 1/9).
func netKeepAliveConst() (solSocket, soKeepAlive int) {
	sol, _ := httpSockConstants()
	if targetGOOS() == "darwin" {
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
func netKeepIdleConst() (ipprotoTCP, tcpKeepIdle int) {
	if targetGOOS() == "darwin" {
		return 6, 0x10
	}
	return 6, 4
}
