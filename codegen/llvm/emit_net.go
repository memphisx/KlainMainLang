// emit_net.go — codegen for Node's `net` TCP server: net.createServer plus the
// Server surface (server.listen/on('connection')/close) and the connection
// Socket surface (socket.on('data'|'end'), socket.write, socket.end). Backed by
// runtime_net.go.
//
// Listener registration mirrors the child_process posture (emit_childprocess.go):
// a connection/data/end listener is an arrow/function-expression literal, stored
// as a raw closure header the runtime dispatch invokes directly. The connection
// listener receives the connection socket (typed NetSocket via the hint below),
// so its body's socket.on(...)/write(...) dispatch through inferExprType.
package llvm

import (
	"fmt"

	"KlainMainLang/ast"
)

// emitNetModuleCall dispatches net.createServer.
func (e *Emitter) emitNetModuleCall(method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	e.ensureNetRuntime()
	switch method {
	case "createServer":
		return e.emitNetCreateServer(args, pos)
	case "connect", "createConnection":
		return e.emitNetConnect(args, pos)
	case "isIP", "isIPv4", "isIPv6":
		return e.emitNetIsIP(method, args, pos)
	}
	return Value{}, fmt.Errorf("%d:%d: net.%s is not supported", pos.Line, pos.Col, method)
}

// emitNetIsIP implements net.isIP / isIPv4 / isIPv6: parse the string via
// inet_pton. isIP returns a `number` (0/4/6); isIPv4/isIPv6 return a boolean.
func (e *Emitter) emitNetIsIP(method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: net.%s takes one string argument", pos.Line, pos.Col, method)
	}
	sv, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	if sv.Ty.IR != "ptr" {
		return Value{}, fmt.Errorf("%d:%d: net.%s takes a string argument", pos.Line, pos.Col, method)
	}
	fam := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_net_is_ip(ptr %s)", fam, sv.Ref))
	switch method {
	case "isIPv4":
		b := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 4", b, fam))
		return Value{Ref: b, Ty: TypeBool}, nil
	case "isIPv6":
		b := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 6", b, fam))
		return Value{Ref: b, Ty: TypeBool}, nil
	default: // isIP → a number (0/4/6)
		d := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = sitofp i32 %s to double", d, fam))
		return Value{Ref: d, Ty: TypeF64}, nil
	}
}

// emitNetConnect implements net.connect(port, host, connectListener?) (and its
// alias net.createConnection): a blocking connect that returns a NetSocket. The
// connect listener, if given, fires synchronously once the connection is
// established (the WebSocket-client "outcome known at establish time" posture).
// A failed connection throws a catchable Error (V1: no async 'error' event).
func (e *Emitter) emitNetConnect(args []ast.Expression, pos ast.Pos) (Value, error) {
	// Two call shapes: positional `(port, host, cb?)` or options-object
	// `({ port, host? }, cb?)` (Node's IPC `{ path }` form is not supported —
	// this is TCP only). `host` defaults to "localhost".
	var portExpr, hostExpr, cbExpr ast.Expression
	if len(args) >= 1 {
		if ol, ok := args[0].(*ast.ObjectLiteral); ok {
			portExpr = objectLiteralProp(ol, "port")
			if portExpr == nil {
				return Value{}, fmt.Errorf("%d:%d: net.connect's options object requires a 'port'", pos.Line, pos.Col)
			}
			hostExpr = objectLiteralProp(ol, "host")
			if len(args) > 2 {
				return Value{}, fmt.Errorf("%d:%d: net.connect(options, connectListener?) takes at most two arguments", pos.Line, pos.Col)
			}
			if len(args) == 2 {
				cbExpr = args[1]
			}
		} else {
			if len(args) > 3 {
				return Value{}, fmt.Errorf("%d:%d: net.connect takes (port[, host][, connectListener]) or (options, connectListener?)", pos.Line, pos.Col)
			}
			portExpr = args[0]
			// `(port)`, `(port, cb)`, `(port, host)`, `(port, host, cb)` —
			// disambiguate the second argument by its static type, as Node does
			// dynamically. host defaults to "localhost".
			if len(args) >= 2 {
				if e.inferExprType(args[1]).IsFunc || isInlineCallback(args[1]) {
					cbExpr = args[1]
					if len(args) == 3 {
						return Value{}, fmt.Errorf("%d:%d: net.connect's connectListener must be the last argument", pos.Line, pos.Col)
					}
				} else {
					hostExpr = args[1]
					if len(args) == 3 {
						cbExpr = args[2]
					}
				}
			}
		}
	} else {
		return Value{}, fmt.Errorf("%d:%d: net.connect takes (port, host, connectListener?) or (options, connectListener?)", pos.Line, pos.Col)
	}

	portVal, err := e.emitExpr(portExpr)
	if err != nil {
		return Value{}, err
	}
	port := e.coerce(portVal, TypeI64)
	port32 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = trunc i64 %s to i32", port32, port.Ref))
	var hostVal Value
	if hostExpr != nil {
		hv, err := e.emitExpr(hostExpr)
		if err != nil {
			return Value{}, err
		}
		hostVal = e.coerce(hv, TypePtr)
	} else {
		hostVal = Value{Ref: e.internString("localhost"), Ty: TypePtr}
	}

	sk := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_net_connect(i32 %s, ptr %s)", sk, port32, hostVal.Ref))
	isnull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isnull, sk))
	okL := e.freshLabel("netconnok")
	failL := e.freshLabel("netconnfail")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isnull, failL, okL))

	e.emitLabel(failL)
	e.emitInternalThrow(e.internString("net.connect: connection failed"))

	e.emitLabel(okL)
	// The optional 'connect' listener (Node's, taking no arguments) is stored in
	// the socket's field 4 and fired once on the first dispatch pass — deferred
	// so it runs after net.connect returns and the `const sock = ...` binding is
	// assigned. A listener closing over that binding therefore sees the real
	// socket (closure capture of a not-yet-initialized binding is handled by
	// ADR-00330).
	if cbExpr != nil {
		cb, err := e.netArrowClosure(cbExpr, nil, pos)
		if err != nil {
			return Value{}, err
		}
		e.netStorePtrField(sk, netSocketIR, 4, cb)
	}
	return Value{Ref: sk, Ty: NetSocketType()}, nil
}

// emitNetCreateServer implements net.createServer(connectionListener?): a
// Server handle whose connection listener (if given) is stored as a closure
// header the dispatch fires with each accepted socket.
func (e *Emitter) emitNetCreateServer(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) > 1 {
		return Value{}, fmt.Errorf("%d:%d: net.createServer takes (connectionListener?)", pos.Line, pos.Col)
	}
	srv := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @calloc(i64 1, i64 %d)", srv, netServerStructSize))
	// field 0 listenfd = -1 (calloc zeroed it; set explicitly for clarity)
	lfd := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 0", lfd, netServerIR, srv))
	e.emitInstr(fmt.Sprintf("store i64 -1, ptr %s, align 8", lfd))
	if len(args) == 1 {
		if err := e.netStoreConnListener(srv, args[0], pos); err != nil {
			return Value{}, err
		}
	}
	e.emitInstr(fmt.Sprintf("call void @__kml_net_srv_register(ptr %s)", srv))
	return Value{Ref: srv, Ty: NetServerType()}, nil
}

// netStoreConnListener resolves a (socket) => ... listener and stores its
// closure header into the server's field 1.
func (e *Emitter) netStoreConnListener(srv string, arg ast.Expression, pos ast.Pos) error {
	cb, err := e.resolveCallbackWithHints(arg, []Type{NetSocketType()})
	if err != nil {
		return err
	}
	if cb.kind != cbClosure {
		return fmt.Errorf("%d:%d: a net connection listener must be an arrow function literal", pos.Line, pos.Col)
	}
	e.netStorePtrField(srv, netServerIR, 1, cb.hdrPtr)
	return nil
}

// netStorePtrField GEPs struct field idx and stores a ptr into it.
func (e *Emitter) netStorePtrField(base, structIR string, idx int, val string) {
	slot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", slot, structIR, base, idx))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", val, slot))
}

// emitNetServerMethod dispatches server.listen/on/close.
func (e *Emitter) emitNetServerMethod(objExpr ast.Expression, method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	objVal, err := e.emitExpr(objExpr)
	if err != nil {
		return Value{}, err
	}
	switch method {
	case "listen":
		return e.emitNetServerListen(objVal, args, pos)
	case "on":
		return e.emitNetServerOn(objVal, args, pos)
	case "close":
		lfd := e.freshReg()
		fd64 := e.freshReg()
		fd32 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 0", lfd, netServerIR, objVal.Ref))
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", fd64, lfd))
		e.emitInstr(fmt.Sprintf("%s = trunc i64 %s to i32", fd32, fd64))
		e.ensureCloseDecl()
		e.emitInstr(fmt.Sprintf("call i32 @close(i32 %s)", fd32))
		e.emitInstr(fmt.Sprintf("store i64 -1, ptr %s, align 8", lfd))
		closed := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 2", closed, netServerIR, objVal.Ref))
		e.emitInstr(fmt.Sprintf("store i64 1, ptr %s, align 8", closed))
		return Value{Ty: TypeVoid}, nil
	case "address":
		return e.emitNetServerAddress(objVal, pos)
	}
	return Value{}, fmt.Errorf("%d:%d: a net.Server has no method '%s'", pos.Line, pos.Col, method)
}

// netAddressType is the shape net.Server.address() returns — Node's
// `{ address, family, port }` AddressInfo record. Declared once so member-type
// inference and the emitted struct agree on field order.
func netAddressType() Type {
	return ObjectType([]Field{
		{Name: "address", Ty: TypePtr},
		{Name: "family", Ty: TypePtr},
		{Name: "port", Ty: TypeF64},
	})
}

// emitNetAddressObject builds Node's `{ address, family, port }` AddressInfo
// from a socket/listen fd (an i32 register), reading the real bound address and
// port via getsockname. family is always "IPv4" — this compiler binds/connects
// IPv4 only (ADR-00324/00358), so it never reports Node's dual-stack IPv6.
func (e *Emitter) emitNetAddressObject(fd32 string) Value {
	e.ensureNetRuntime()
	portI := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_net_sockname_port(i32 %s)", portI, fd32))
	portD := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sitofp i32 %s to double", portD, portI))
	addrStr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_net_sockname_addr(i32 %s)", addrStr, fd32))

	ty := netAddressType()
	e.ensureCalloc()
	dataReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @calloc(i64 1, i64 %d)", dataReg, ty.StructSize()))
	structIR := ty.StructIR()
	store := func(idx int, ir, val string) {
		g := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", g, structIR, dataReg, idx))
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align 8", ir, val, g))
	}
	store(0, "ptr", addrStr)
	store(1, "ptr", e.internString("IPv4"))
	store(2, "double", portD)
	return Value{Ref: dataReg, Ty: ty}
}

// netFieldFd32 loads a net server/socket handle's fd (field 0) as an i32.
func (e *Emitter) netFieldFd32(ref, structIR string) string {
	g := e.freshReg()
	fd64 := e.freshReg()
	fd32 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 0", g, structIR, ref))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", fd64, g))
	e.emitInstr(fmt.Sprintf("%s = trunc i64 %s to i32", fd32, fd64))
	return fd32
}

// emitNetServerAddress implements server.address(): getsockname on the listen
// fd for the actual bound address+port (the crux of the `listen(0)`
// ephemeral-port idiom). The server binds IPv4 `0.0.0.0` (ADR-00324/00358), so
// it reports `{ family: "IPv4", address: "0.0.0.0", port }` rather than Node's
// dual-stack `::`/`IPv6` default.
func (e *Emitter) emitNetServerAddress(objVal Value, pos ast.Pos) (Value, error) {
	return e.emitNetAddressObject(e.netFieldFd32(objVal.Ref, netServerIR)), nil
}

// emitNetSocketAddress implements socket.address(): the socket's local
// address+port (e.g. a loopback connection reports `127.0.0.1` + the local
// ephemeral port), via getsockname on the socket fd.
func (e *Emitter) emitNetSocketAddress(objVal Value, pos ast.Pos) (Value, error) {
	return e.emitNetAddressObject(e.netFieldFd32(objVal.Ref, netSocketIR)), nil
}

// emitNetServerListen implements server.listen(port, listeningCallback?).
func (e *Emitter) emitNetServerListen(objVal Value, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 1 || len(args) > 2 {
		return Value{}, fmt.Errorf("%d:%d: server.listen takes (port, callback?)", pos.Line, pos.Col)
	}
	portVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	port := e.coerce(portVal, TypeI64)
	port32 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = trunc i64 %s to i32", port32, port.Ref))
	fd := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_net_bind_and_listen(i32 %s)", fd, port32))
	fd64 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sext i32 %s to i64", fd64, fd))
	slot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 0", slot, netServerIR, objVal.Ref))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", fd64, slot))
	// Optional 'listening' callback fires synchronously once bound (Node treats
	// listen()'s callback as a one-time 'listening' listener).
	if len(args) == 2 {
		cb, err := e.resolveCallback(args[1])
		if err != nil {
			return Value{}, err
		}
		if _, err := e.emitCBCall(cb, nil); err != nil {
			return Value{}, err
		}
	}
	// Fire any listener registered earlier via server.on('listening', …).
	e.emitNetFireListeningListener(objVal.Ref)
	return Value{Ty: TypeVoid}, nil
}

// emitNetFireListeningListener invokes the server's stored 'listening' listener
// (field 4, a zero-arg closure header) if present, then clears it so it fires at
// most once (Node's 'listening' fires once per listen()).
func (e *Emitter) emitNetFireListeningListener(srv string) {
	slot := e.freshReg()
	hdr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 4", slot, netServerIR, srv))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", hdr, slot))
	isnull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isnull, hdr))
	fireL := e.freshLabel("netlisten.fire")
	doneL := e.freshLabel("netlisten.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isnull, doneL, fireL))
	e.emitLabel(fireL)
	fp := e.freshReg()
	ep := e.freshReg()
	fpp := e.freshReg()
	epp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 0", fpp, hdr))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", fp, fpp))
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 1", epp, hdr))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", ep, epp))
	e.emitInstr(fmt.Sprintf("call void %s(ptr %s)", fp, ep))
	e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", slot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
}

// emitNetServerOn implements server.on('connection'|'listening', listener).
func (e *Emitter) emitNetServerOn(objVal Value, args []ast.Expression, pos ast.Pos) (Value, error) {
	evt, err := stringLiteralArg(args, 0, "server.on", pos)
	if err != nil {
		return Value{}, err
	}
	if len(args) != 2 {
		return Value{}, fmt.Errorf("%d:%d: server.on takes (event, listener)", pos.Line, pos.Col)
	}
	switch evt {
	case "connection":
		if err := e.netStoreConnListener(objVal.Ref, args[1], pos); err != nil {
			return Value{}, err
		}
	case "listening":
		cb, err := e.netArrowClosure(args[1], nil, pos)
		if err != nil {
			return Value{}, err
		}
		e.netStorePtrField(objVal.Ref, netServerIR, 4, cb)
		// If the server is already listening (fd >= 0), fire now — the
		// listener was registered after listen() bound.
		fdSlot := e.freshReg()
		fd := e.freshReg()
		isBound := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 0", fdSlot, netServerIR, objVal.Ref))
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", fd, fdSlot))
		e.emitInstr(fmt.Sprintf("%s = icmp sge i64 %s, 0", isBound, fd))
		nowL := e.freshLabel("netlisten.now")
		afterL := e.freshLabel("netlisten.after")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isBound, nowL, afterL))
		e.emitLabel(nowL)
		e.emitNetFireListeningListener(objVal.Ref)
		e.emitTerminator(fmt.Sprintf("br label %%%s", afterL))
		e.emitLabel(afterL)
	default:
		return Value{}, fmt.Errorf("%d:%d: a net.Server supports .on('connection'|'listening', listener) (got '%s')", pos.Line, pos.Col, evt)
	}
	return Value{Ty: TypeVoid}, nil
}

// emitNetSocketMethod dispatches socket.on/write/end on a connection socket.
func (e *Emitter) emitNetSocketMethod(objExpr ast.Expression, method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	objVal, err := e.emitExpr(objExpr)
	if err != nil {
		return Value{}, err
	}
	switch method {
	case "on":
		evt, err := stringLiteralArg(args, 0, "socket.on", pos)
		if err != nil {
			return Value{}, err
		}
		if len(args) != 2 {
			return Value{}, fmt.Errorf("%d:%d: socket.on takes (event, listener)", pos.Line, pos.Col)
		}
		switch evt {
		case "data":
			cb, err := e.netArrowClosure(args[1], []Type{BufferType()}, pos)
			if err != nil {
				return Value{}, err
			}
			e.netStorePtrField(objVal.Ref, netSocketIR, 2, cb)
		case "end":
			cb, err := e.netArrowClosure(args[1], nil, pos)
			if err != nil {
				return Value{}, err
			}
			e.netStorePtrField(objVal.Ref, netSocketIR, 3, cb)
		case "close":
			// Fired once on teardown — EOF or explicit end()/destroy()
			// (ADR-00501). No hadError argument (this path has no error
			// teardown to distinguish).
			cb, err := e.netArrowClosure(args[1], nil, pos)
			if err != nil {
				return Value{}, err
			}
			e.netStorePtrField(objVal.Ref, netSocketIR, 6, cb)
		case "connect", "ready":
			// A post-connect registration on an already-connected client
			// socket: stored in the same pending slot net.connect's own
			// callback uses; the dispatch pass fires and clears it.
			cb, err := e.netArrowClosure(args[1], nil, pos)
			if err != nil {
				return Value{}, err
			}
			e.netStorePtrField(objVal.Ref, netSocketIR, 4, cb)
		default:
			return Value{}, fmt.Errorf("%d:%d: a net socket supports 'data', 'end', 'close', and 'connect'/'ready' (got '%s')", pos.Line, pos.Col, evt)
		}
		return Value{Ty: TypeVoid}, nil
	case "write":
		if len(args) != 1 {
			return Value{}, fmt.Errorf("%d:%d: socket.write takes (data)", pos.Line, pos.Col)
		}
		ptrRef, lenRef, err := e.zlibResolveInput(args[0], pos)
		if err != nil {
			return Value{}, err
		}
		e.emitInstr(fmt.Sprintf("call void @__kml_net_sock_write(ptr %s, ptr %s, i64 %s)", objVal.Ref, ptrRef, lenRef))
		return Value{Ty: TypeVoid}, nil
	case "end":
		if len(args) == 1 {
			ptrRef, lenRef, err := e.zlibResolveInput(args[0], pos)
			if err != nil {
				return Value{}, err
			}
			e.emitInstr(fmt.Sprintf("call void @__kml_net_sock_write(ptr %s, ptr %s, i64 %s)", objVal.Ref, ptrRef, lenRef))
		} else if len(args) != 0 {
			return Value{}, fmt.Errorf("%d:%d: socket.end takes (data?)", pos.Line, pos.Col)
		}
		e.emitInstr(fmt.Sprintf("call void @__kml_net_sock_close(ptr %s)", objVal.Ref))
		return Value{Ty: TypeVoid}, nil
	case "address":
		return e.emitNetSocketAddress(objVal, pos)
	case "destroy":
		// Forcibly close the socket (no error argument threaded in V1).
		e.emitInstr(fmt.Sprintf("call void @__kml_net_sock_close(ptr %s)", objVal.Ref))
		return objVal, nil
	case "setNoDelay":
		return e.emitNetSocketSockOpt(objVal, args, "nodelay", pos)
	case "setKeepAlive":
		return e.emitNetSocketSockOpt(objVal, args, "keepalive", pos)
	case "setEncoding", "ref", "unref", "pause", "resume", "setTimeout":
		// Accepted for compatibility but a no-op in this compiler's model:
		// `setEncoding` — a socket's 'data' chunk type is already the listener's
		// declared parameter type (Buffer or string); `ref`/`unref` — the
		// program runs to completion regardless of event-loop keep-alive;
		// `pause`/`resume`/`setTimeout` — the blocking read model has no
		// separately-schedulable flow or idle timer. Arguments (a possible
		// timeout callback) are evaluated for side effects, then ignored.
		for _, a := range args {
			if _, err := e.emitExpr(a); err != nil {
				return Value{}, err
			}
		}
		return objVal, nil
	}
	return Value{}, fmt.Errorf("%d:%d: a net socket has no method '%s'", pos.Line, pos.Col, method)
}

// emitNetSocketSockOpt implements socket.setNoDelay(bool?) / setKeepAlive(bool?,
// ms?) via a real setsockopt on the socket fd. The optional boolean argument
// defaults to true (matching Node); setKeepAlive's initial-delay argument is
// accepted but not threaded (no per-socket keepalive-idle tuning in V1).
func (e *Emitter) emitNetSocketSockOpt(objVal Value, args []ast.Expression, which string, pos ast.Pos) (Value, error) {
	e.ensureNetRuntime()
	// enable flag: default 1 (true); an explicit `false` first arg disables.
	enable := "1"
	if len(args) >= 1 {
		v, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		if v.Ty.IR != "i1" {
			return Value{}, fmt.Errorf("%d:%d: socket.%s's first argument must be a boolean", pos.Line, pos.Col, map[string]string{"nodelay": "setNoDelay", "keepalive": "setKeepAlive"}[which])
		}
		z := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = zext i1 %s to i32", z, v.Ref))
		enable = z
	}
	// Evaluate any further args (e.g. setKeepAlive's initialDelay) for side
	// effects; not threaded into the socket option in V1.
	for i := 1; i < len(args); i++ {
		if _, err := e.emitExpr(args[i]); err != nil {
			return Value{}, err
		}
	}
	fd32 := e.netFieldFd32(objVal.Ref, netSocketIR)
	valp := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i32, align 4", valp))
	e.emitInstr(fmt.Sprintf("store i32 %s, ptr %s, align 4", enable, valp))
	var level, optname int
	if which == "nodelay" {
		level, optname = 6, 1 // IPPROTO_TCP, TCP_NODELAY
	} else {
		level, optname = netKeepAliveConst()
	}
	e.emitInstr(fmt.Sprintf("call i32 @setsockopt(i32 %s, i32 %d, i32 %d, ptr %s, i32 4)", fd32, level, optname, valp))
	return objVal, nil
}

// netArrowClosure resolves a listener argument to a closure header pointer,
// requiring an arrow/function-expression literal (the child_process posture).
func (e *Emitter) netArrowClosure(arg ast.Expression, hints []Type, pos ast.Pos) (string, error) {
	cb, err := e.resolveCallbackWithHints(arg, hints)
	if err != nil {
		return "", err
	}
	if cb.kind != cbClosure {
		return "", fmt.Errorf("%d:%d: a net socket listener must be an arrow function literal", pos.Line, pos.Col)
	}
	return cb.hdrPtr, nil
}

// isInlineCallback reports whether expr is a callback literal (arrow or
// function expression), possibly inside a `test` counting wrapper — the
// static stand-in for Node's dynamic typeof-function argument shuffling.
func isInlineCallback(expr ast.Expression) bool {
	switch unwrapTestWrapper(expr).(type) {
	case *ast.ArrowFunction, *ast.FunctionExpression:
		return true
	}
	return false
}
