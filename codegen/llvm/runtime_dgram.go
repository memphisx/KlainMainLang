// runtime_dgram.go — Node `dgram`: UDP sockets (createSocket/bind/send/close +
// 'message' events). Each open socket's fd is made non-blocking and folded into
// the central select() event loop through the standard hook trio
// (@__kml_dgram_keepalive/fdset_add/dispatch), the same posture as
// child_process and net. recvfrom on a ready socket fires the 'message'
// listener with a Buffer and an rinfo { address, port }.
package llvm

import "fmt"

// dgramSocketIR: 0 i64 fd (-1 after close) · 1 i64 state (0 open · 1 closed) ·
// 2 ptr 'message' listener (closure header, or null).
const dgramSocketIR = "{ i64, i64, ptr }"

// dgramRinfoStructIR: the rinfo passed to a 'message' listener —
// { ptr address, i64 port }. Matches dgramRinfoType()'s object layout so the
// listener body's rinfo.address / rinfo.port resolve to the right offsets.
const dgramRinfoStructIR = "{ ptr, i64 }"

// dgramRinfoType is the object type of a 'message' listener's rinfo argument.
func dgramRinfoType() Type {
	return ObjectType([]Field{
		{Name: "address", Ty: TypePtr},
		{Name: "port", Ty: TypeI64},
	})
}

func (e *Emitter) ensureDgramRuntime() {
	if e.usedDgramRuntime {
		return
	}
	e.usedDgramRuntime = true
	e.ensureMalloc()
	e.ensureCalloc()
	e.ensureRealloc()
	e.ensureMemcpy()
	e.ensureMemset()
	e.ensureCloseDecl()
	e.ensureFcntlDecl()
	e.ensureSprintf()
	e.ensureHTTPRuntime()    // socket/bind/htons decls + the event loop
	e.ensureWorkerFdSetbit() // shared @__kml_worker_fd_setbit
	e.ensureDNSRuntime()     // @__kml_dns_lookup, for send()'s host resolution

	e.emitGlobal("declare i64 @sendto(i32 noundef, ptr noundef, i64 noundef, i32 noundef, ptr noundef, i32 noundef)")
	e.emitGlobal("declare i64 @recvfrom(i32 noundef, ptr noundef, i64 noundef, i32 noundef, ptr noundef, ptr noundef)")
	// @inet_pton is declared by the WebSocket-client runtime, which
	// ensureHTTPRuntime (called above) always pulls in — don't redeclare it.
	e.emitGlobal("declare i16 @ntohs(i16 noundef)")

	e.emitGlobal("@__kml_dgram_data = internal global ptr null, align 8")
	e.emitGlobal("@__kml_dgram_len = internal global i64 0, align 8")
	e.emitGlobal("@__kml_dgram_cap = internal global i64 0, align 8")

	sock := dgramSocketIR
	rinfo := dgramRinfoStructIR
	fam0, fam1 := httpSockaddrFamilyBytes()
	nonblock := httpNonblockFlag()
	fmtIP := e.internString("%u.%u.%u.%u")

	// registry append (realloc-doubling)
	e.emitGlobal(`
define void @__kml_dgram_register(ptr %s) {
entry:
  %len = load i64, ptr @__kml_dgram_len, align 8
  %cap = load i64, ptr @__kml_dgram_cap, align 8
  %full = icmp sge i64 %len, %cap
  br i1 %full, label %grow, label %store
grow:
  %cap2 = mul i64 %cap, 2
  %atleast4 = icmp sgt i64 %cap2, 4
  %newcap = select i1 %atleast4, i64 %cap2, i64 4
  %olddata = load ptr, ptr @__kml_dgram_data, align 8
  %bytes = mul i64 %newcap, 8
  %newdata = call ptr @realloc(ptr %olddata, i64 %bytes)
  store ptr %newdata, ptr @__kml_dgram_data, align 8
  store i64 %newcap, ptr @__kml_dgram_cap, align 8
  br label %store
store:
  %data = load ptr, ptr @__kml_dgram_data, align 8
  %slot = getelementptr ptr, ptr %data, i64 %len
  store ptr %s, ptr %slot, align 8
  %newlen = add i64 %len, 1
  store i64 %newlen, ptr @__kml_dgram_len, align 8
  ret void
}`)

	// __kml_dgram_create(): a non-blocking AF_INET/SOCK_DGRAM socket handle.
	e.emitGlobal(fmt.Sprintf(`
define ptr @__kml_dgram_create() {
entry:
  %%fd = call i32 @socket(i32 2, i32 2, i32 0)
  %%fl = call i32 (i32, i32, ...) @fcntl(i32 %%fd, i32 3)
  %%fln = or i32 %%fl, %d
  call i32 (i32, i32, ...) @fcntl(i32 %%fd, i32 4, i32 %%fln)
  %%sk = call ptr @calloc(i64 1, i64 24)
  %%fd_p = getelementptr %s, ptr %%sk, i32 0, i32 0
  %%fd64 = sext i32 %%fd to i64
  store i64 %%fd64, ptr %%fd_p, align 8
  call void @__kml_dgram_register(ptr %%sk)
  ret ptr %%sk
}`, nonblock, sock))

	// __kml_dgram_bind(sock, port): bind to 0.0.0.0:port.
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_dgram_bind(ptr %%sock, i32 %%port) {
entry:
  %%fd_p = getelementptr %s, ptr %%sock, i32 0, i32 0
  %%fd64 = load i64, ptr %%fd_p, align 8
  %%fd = trunc i64 %%fd64 to i32
  %%addr = alloca [16 x i8], align 4
  call ptr @memset(ptr %%addr, i32 0, i64 16)
  store i8 %d, ptr %%addr, align 1
  %%b1p = getelementptr i8, ptr %%addr, i64 1
  store i8 %d, ptr %%b1p, align 1
  %%portu16 = trunc i32 %%port to i16
  %%portn = call i16 @htons(i16 %%portu16)
  %%portp = getelementptr i8, ptr %%addr, i64 2
  store i16 %%portn, ptr %%portp, align 1
  call i32 @bind(i32 %%fd, ptr %%addr, i32 16)
  ret void
}`, sock, fam0, fam1))

	// __kml_dgram_send(sock, data, n, port, host): resolve host to an IPv4
	// address, then sendto(). No-op if resolution fails.
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_dgram_send(ptr %%sock, ptr %%data, i64 %%n, i32 %%port, ptr %%host) {
entry:
  %%fd_p = getelementptr %s, ptr %%sock, i32 0, i32 0
  %%fd64 = load i64, ptr %%fd_p, align 8
  %%fd = trunc i64 %%fd64 to i32
  %%ip = call ptr @__kml_dns_lookup(ptr %%host)
  %%ipok = icmp ne ptr %%ip, null
  br i1 %%ipok, label %%dosend, label %%ret
dosend:
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
  call i64 @sendto(i32 %%fd, ptr %%data, i64 %%n, i32 0, ptr %%addr, i32 16)
  br label %%ret
ret:
  ret void
}`, sock, fam0, fam1))

	// __kml_dgram_close(sock): close the fd, mark closed.
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_dgram_close(ptr %%sock) {
entry:
  %%fd_p = getelementptr %s, ptr %%sock, i32 0, i32 0
  %%fd64 = load i64, ptr %%fd_p, align 8
  %%open = icmp sge i64 %%fd64, 0
  br i1 %%open, label %%cl, label %%ret
cl:
  %%fd = trunc i64 %%fd64 to i32
  call i32 @close(i32 %%fd)
  store i64 -1, ptr %%fd_p, align 8
  %%st_p = getelementptr %s, ptr %%sock, i32 0, i32 1
  store i64 1, ptr %%st_p, align 8
  br label %%ret
ret:
  ret void
}`, sock, sock))

	// __kml_dgram_keepalive(): true while any open socket has a 'message'
	// listener registered (a bound receiver keeps the loop alive; a
	// send-then-close socket with no listener does not).
	e.emitGlobal(fmt.Sprintf(`
define i1 @__kml_dgram_keepalive() {
entry:
  %%len = load i64, ptr @__kml_dgram_len, align 8
  %%data = load ptr, ptr @__kml_dgram_data, align 8
  %%i = alloca i64, align 8
  store i64 0, ptr %%i, align 8
  br label %%loop
loop:
  %%iv = load i64, ptr %%i, align 8
  %%inb = icmp slt i64 %%iv, %%len
  br i1 %%inb, label %%body, label %%no
body:
  %%slot = getelementptr ptr, ptr %%data, i64 %%iv
  %%sk = load ptr, ptr %%slot, align 8
  %%st_p = getelementptr %s, ptr %%sk, i32 0, i32 1
  %%st = load i64, ptr %%st_p, align 8
  %%open = icmp eq i64 %%st, 0
  %%ml_p = getelementptr %s, ptr %%sk, i32 0, i32 2
  %%ml = load ptr, ptr %%ml_p, align 8
  %%haslistener = icmp ne ptr %%ml, null
  %%alive = and i1 %%open, %%haslistener
  br i1 %%alive, label %%yes, label %%next
next:
  %%inext = add i64 %%iv, 1
  store i64 %%inext, ptr %%i, align 8
  br label %%loop
yes:
  ret i1 1
no:
  ret i1 0
}`, sock, sock))

	// __kml_dgram_fdset_add(fdset, maxfd): add every open socket fd.
	e.emitGlobal(fmt.Sprintf(`
define i1 @__kml_dgram_fdset_add(ptr %%fdset, ptr %%maxfd) {
entry:
  %%len = load i64, ptr @__kml_dgram_len, align 8
  %%data = load ptr, ptr @__kml_dgram_data, align 8
  %%i = alloca i64, align 8
  store i64 0, ptr %%i, align 8
  br label %%loop
loop:
  %%iv = load i64, ptr %%i, align 8
  %%inb = icmp slt i64 %%iv, %%len
  br i1 %%inb, label %%body, label %%done
body:
  %%slot = getelementptr ptr, ptr %%data, i64 %%iv
  %%sk = load ptr, ptr %%slot, align 8
  %%fd_p = getelementptr %s, ptr %%sk, i32 0, i32 0
  %%fd64 = load i64, ptr %%fd_p, align 8
  %%open = icmp sge i64 %%fd64, 0
  br i1 %%open, label %%addfd, label %%next
addfd:
  %%fd32 = trunc i64 %%fd64 to i32
  call void @__kml_worker_fd_setbit(i32 %%fd32, ptr %%fdset, ptr %%maxfd)
  br label %%next
next:
  %%inext = add i64 %%iv, 1
  store i64 %%inext, ptr %%i, align 8
  br label %%loop
done:
  ret i1 0
}`, sock))

	// __kml_dgram_dispatch(): recvfrom every open socket until EAGAIN, firing
	// its 'message' listener with a Buffer and an rinfo { address, port }.
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_dgram_dispatch() {
entry:
  %%len = load i64, ptr @__kml_dgram_len, align 8
  %%data = load ptr, ptr @__kml_dgram_data, align 8
  %%i = alloca i64, align 8
  store i64 0, ptr %%i, align 8
  %%chunk = alloca [65536 x i8], align 1
  %%chunkptr = getelementptr [65536 x i8], ptr %%chunk, i32 0, i32 0
  %%src = alloca [16 x i8], align 4
  %%srclen = alloca i32, align 4
  br label %%loop
loop:
  %%iv = load i64, ptr %%i, align 8
  %%inb = icmp slt i64 %%iv, %%len
  br i1 %%inb, label %%body, label %%done
body:
  %%slot = getelementptr ptr, ptr %%data, i64 %%iv
  %%sk = load ptr, ptr %%slot, align 8
  br label %%rloop
rloop:
  %%fd_p = getelementptr %s, ptr %%sk, i32 0, i32 0
  %%fd64 = load i64, ptr %%fd_p, align 8
  %%closed = icmp slt i64 %%fd64, 0
  br i1 %%closed, label %%next, label %%doread
doread:
  %%fd = trunc i64 %%fd64 to i32
  store i32 16, ptr %%srclen, align 4
  %%n = call i64 @recvfrom(i32 %%fd, ptr %%chunkptr, i64 65536, i32 0, ptr %%src, ptr %%srclen)
  %%hasdata = icmp sgt i64 %%n, 0
  br i1 %%hasdata, label %%onmsg, label %%next
onmsg:
  %%ml_p = getelementptr %s, ptr %%sk, i32 0, i32 2
  %%ml = load ptr, ptr %%ml_p, align 8
  %%hasml = icmp ne ptr %%ml, null
  br i1 %%hasml, label %%firemsg, label %%rloop
firemsg:
  %%buf = call ptr @malloc(i64 %%n)
  call ptr @memcpy(ptr %%buf, ptr %%chunkptr, i64 %%n)
  ; rinfo.address from src sin_addr (offset 4), rinfo.port from sin_port (offset 2)
  %%sap = getelementptr i8, ptr %%src, i64 4
  %%a0p = getelementptr i8, ptr %%sap, i64 0
  %%a1p = getelementptr i8, ptr %%sap, i64 1
  %%a2p = getelementptr i8, ptr %%sap, i64 2
  %%a3p = getelementptr i8, ptr %%sap, i64 3
  %%a0 = load i8, ptr %%a0p, align 1
  %%a1 = load i8, ptr %%a1p, align 1
  %%a2 = load i8, ptr %%a2p, align 1
  %%a3 = load i8, ptr %%a3p, align 1
  %%a0i = zext i8 %%a0 to i32
  %%a1i = zext i8 %%a1 to i32
  %%a2i = zext i8 %%a2 to i32
  %%a3i = zext i8 %%a3 to i32
  %%addrstr = call ptr @malloc(i64 16)
  call i32 (ptr, ptr, ...) @sprintf(ptr %%addrstr, ptr %s, i32 %%a0i, i32 %%a1i, i32 %%a2i, i32 %%a3i)
  %%portp = getelementptr i8, ptr %%src, i64 2
  %%portn = load i16, ptr %%portp, align 1
  %%porth = call i16 @ntohs(i16 %%portn)
  %%port64 = zext i16 %%porth to i64
  %%rinfo = call ptr @malloc(i64 16)
  %%ri_addr = getelementptr %s, ptr %%rinfo, i32 0, i32 0
  store ptr %%addrstr, ptr %%ri_addr, align 8
  %%ri_port = getelementptr %s, ptr %%rinfo, i32 0, i32 1
  store i64 %%port64, ptr %%ri_port, align 8
  %%mfp_p = getelementptr { ptr, ptr }, ptr %%ml, i32 0, i32 0
  %%mfp = load ptr, ptr %%mfp_p, align 8
  %%mep_p = getelementptr { ptr, ptr }, ptr %%ml, i32 0, i32 1
  %%mep = load ptr, ptr %%mep_p, align 8
  call void %%mfp(ptr %%mep, ptr %%buf, i64 %%n, ptr %%rinfo)
  br label %%rloop
next:
  %%inext = add i64 %%iv, 1
  store i64 %%inext, ptr %%i, align 8
  br label %%loop
done:
  ret void
}`, sock, sock, fmtIP, rinfo, rinfo))
}
