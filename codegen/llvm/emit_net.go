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
	}
	return Value{}, fmt.Errorf("%d:%d: net.%s is not supported", pos.Line, pos.Col, method)
}

// emitNetConnect implements net.connect(port, host, connectListener?) (and its
// alias net.createConnection): a blocking connect that returns a NetSocket. The
// connect listener, if given, fires synchronously once the connection is
// established (the WebSocket-client "outcome known at establish time" posture).
// A failed connection throws a catchable Error (V1: no async 'error' event).
func (e *Emitter) emitNetConnect(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 2 || len(args) > 3 {
		return Value{}, fmt.Errorf("%d:%d: net.connect takes (port, host, connectListener?)", pos.Line, pos.Col)
	}
	portVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	port := e.coerce(portVal, TypeI64)
	port32 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = trunc i64 %s to i32", port32, port.Ref))
	hostVal, err := e.emitExpr(args[1])
	if err != nil {
		return Value{}, err
	}
	hostVal = e.coerce(hostVal, TypePtr)

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
	if len(args) == 3 {
		cb, err := e.netArrowClosure(args[2], nil, pos)
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
	e.emitInstr(fmt.Sprintf("%s = call ptr @calloc(i64 1, i64 32)", srv))
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
	}
	return Value{}, fmt.Errorf("%d:%d: a net.Server has no method '%s'", pos.Line, pos.Col, method)
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
	// Optional 'listening' callback fires synchronously once bound.
	if len(args) == 2 {
		cb, err := e.resolveCallback(args[1])
		if err != nil {
			return Value{}, err
		}
		if _, err := e.emitCBCall(cb, nil); err != nil {
			return Value{}, err
		}
	}
	return Value{Ty: TypeVoid}, nil
}

// emitNetServerOn implements server.on('connection', listener).
func (e *Emitter) emitNetServerOn(objVal Value, args []ast.Expression, pos ast.Pos) (Value, error) {
	evt, err := stringLiteralArg(args, 0, "server.on", pos)
	if err != nil {
		return Value{}, err
	}
	if len(args) != 2 {
		return Value{}, fmt.Errorf("%d:%d: server.on takes (event, listener)", pos.Line, pos.Col)
	}
	if evt != "connection" {
		return Value{}, fmt.Errorf("%d:%d: a net.Server supports only .on('connection', listener) (got '%s')", pos.Line, pos.Col, evt)
	}
	if err := e.netStoreConnListener(objVal.Ref, args[1], pos); err != nil {
		return Value{}, err
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
		default:
			return Value{}, fmt.Errorf("%d:%d: a net socket supports 'data' and 'end' (got '%s')", pos.Line, pos.Col, evt)
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
	}
	return Value{}, fmt.Errorf("%d:%d: a net socket has no method '%s'", pos.Line, pos.Col, method)
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
