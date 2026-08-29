// runtime_tls.go — @__kml_tls_connect, the `tls.connect` client runtime
// (TDD-00109). Mirrors __kml_net_connect (runtime_net.go): DNS-resolve, socket,
// blocking connect — then, while the fd is still blocking, wraps it in TLS via
// tlssrc/tls.c's @__kml_tls_client_connect (a blocking handshake). Only on
// success does it switch the fd to non-blocking, store the SSL* in the socket's
// field 5, and register it in the shared net connection registry, after which
// the entire net socket machinery (.on('data')/.write()/.end()/dispatch)
// applies — reads/writes route through SSL_* because field 5 is non-null.
package llvm

import "fmt"

func (e *Emitter) ensureTLSRuntime() {
	if e.usedTLSRuntime {
		return
	}
	e.usedTLSRuntime = true
	e.usedTLS = true     // triggers tlssrc/tls.c compile + -lssl (main.go) and the extern decls
	e.ensureNetRuntime() // socket/connect/register/dns decls + the socket struct + event loop

	fam0, fam1 := httpSockaddrFamilyBytes()
	nonblock := httpNonblockFlag()
	sock := netSocketIR

	// __kml_tls_connect(port, host, reject_unauthorized) -> socket ptr (null on
	// any failure; the caller throws a catchable Error). The TLS handshake runs
	// on the still-blocking fd, matching net.connect's blocking connect.
	e.emitGlobal(fmt.Sprintf(`
define ptr @__kml_tls_connect(i32 %%port, ptr %%host, i32 %%reject) {
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
  br i1 %%connok, label %%handshake, label %%failfd
handshake:
  %%ssl = call ptr @__kml_tls_client_connect(i32 %%fd, ptr %%host, i32 %%reject, ptr null)
  %%sslok = icmp ne ptr %%ssl, null
  br i1 %%sslok, label %%secured, label %%failfd
secured:
  %%fl = call i32 (i32, i32, ...) @fcntl(i32 %%fd, i32 3)
  %%fln = or i32 %%fl, %d
  call i32 (i32, i32, ...) @fcntl(i32 %%fd, i32 4, i32 %%fln)
  %%sk = call ptr @calloc(i64 1, i64 64)
  %%fd_p = getelementptr %s, ptr %%sk, i32 0, i32 0
  %%fd64 = sext i32 %%fd to i64
  store i64 %%fd64, ptr %%fd_p, align 8
  %%ssl_p = getelementptr %s, ptr %%sk, i32 0, i32 5
  store ptr %%ssl, ptr %%ssl_p, align 8
  call void @__kml_net_conn_register(ptr %%sk)
  ret ptr %%sk
failfd:
  call i32 @close(i32 %%fd)
  ret ptr null
failnull:
  ret ptr null
}`, fam0, fam1, nonblock, sock, sock))
}
