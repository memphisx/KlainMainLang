// runtime_net.go — the net socket the HTTP server hands out on 'upgrade',
// 'connection' and 'clientError' (Node's `net` module itself is
// lib/node/net.ts): socket.write and socket.end/destroy (netsock.c), and the
// socket-name readers behind address() (net.c).
package llvm

import _ "embed"

// netSocketIR: 0 i64 fd (-1 after close/EOF) · 1 i64 state (0 open · 1 closed)
// · 2 ptr 'data' listener · 3 ptr 'end' listener · 4 spare · 5 ptr SSL*
// (null for plaintext; set on an HTTPS server's upgraded socket) · 6 ptr
// 'close' listener (fired once — on EOF teardown or an explicit close/destroy
// — then cleared, ADR-00501) · 7 spare · 8 ptr 'error' listener · 9 spare.
const netSocketIR = "{ i64, i64, ptr, ptr, ptr, ptr, ptr, ptr, ptr, ptr }"
const netSocketStructSize = 80

//go:embed netsrc/netsock.c
var netSockSource string

//go:embed netsrc/net.c
var netSource string

// NetSockSource is netsock.c (the byte-IO helpers), behind kml_layout.h.
func NetSockSource() string { return layoutHeader() + netSockSource }

// NetSource is net.c (the registry, the loop hooks, sockname), behind
// kml_layout.h.
func NetSource() string { return layoutHeader() + netSource }

// UsesNetSockIO reports whether the program links netsock.c.
func (e *Emitter) UsesNetSockIO() bool { return e.usedNetSockIO }

// UsesNet reports whether the program links net.c.
func (e *Emitter) UsesNet() bool { return e.usedNetRuntime }

// ensureNetSockIO declares the two low-level net.Socket byte-IO helpers
// (__kml_net_sock_write, __kml_net_sock_close; netsrc/netsock.c) and nothing
// else, so a path that hands out a net.Socket without standing up a net
// server (the HTTP 'clientError'/'connection' events) can still support
// socket.write/end/destroy. The full net runtime calls this too; the flag
// keeps it single-declaration.
func (e *Emitter) ensureNetSockIO() {
	if e.usedNetSockIO {
		return
	}
	e.usedNetSockIO = true
	e.ensureWriteDecl()
	e.ensureCloseDecl()
	e.emitGlobal(`declare void @__kml_net_sock_write(ptr, ptr, i64)
declare void @__kml_net_sock_close(ptr)`)
}

// ensureNetRuntime declares the net runtime (netsrc/net.c): the sockname
// readers, beside the socket byte-IO helpers.
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
	e.ensureHTTPRuntime() // socket/setsockopt/bind/listen/accept/htons decls + the event loop
	e.ensureNetSockIO()

	e.emitGlobal(`declare i32 @__kml_net_sockname_port(i32)
declare ptr @__kml_net_sockname_addr(i32)`)
}
