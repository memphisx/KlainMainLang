// emit_dgram.go — codegen for Node's `dgram`: UDP sockets. dgram.createSocket
// plus the socket surface (bind, on('message'), send, close). Backed by
// runtime_dgram.go. The 'message' listener is an arrow/function-expression
// literal (the child_process posture) receiving a Buffer and an rinfo
// { address, port }.
package llvm

import (
	"fmt"

	"KlainMainLang/ast"
)

// emitDgramModuleCall dispatches dgram.createSocket.
func (e *Emitter) emitDgramModuleCall(method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	switch method {
	case "createSocket":
		return e.emitDgramCreateSocket(args, pos)
	}
	return Value{}, fmt.Errorf("%d:%d: dgram.%s is not supported", pos.Line, pos.Col, method)
}

// emitDgramCreateSocket implements dgram.createSocket('udp4'). Only 'udp4' is
// supported; the argument (a string literal or an options object) is otherwise
// accepted and ignored.
func (e *Emitter) emitDgramCreateSocket(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) > 2 {
		return Value{}, fmt.Errorf("%d:%d: dgram.createSocket takes (type, callback?)", pos.Line, pos.Col)
	}
	if len(args) >= 1 {
		if lit, ok := args[0].(*ast.StringLiteral); ok && lit.Value != "udp4" {
			return Value{}, fmt.Errorf("%d:%d: dgram.createSocket supports only 'udp4' (got '%s')", pos.Line, pos.Col, lit.Value)
		}
	}
	e.ensureDgramRuntime()
	sk := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_dgram_create()", sk))
	// An optional 'message' listener passed as the second arg is equivalent to
	// .on('message', cb) — store it now.
	if len(args) == 2 {
		if err := e.dgramStoreMessageListener(sk, args[1], pos); err != nil {
			return Value{}, err
		}
	}
	return Value{Ref: sk, Ty: DgramSocketType()}, nil
}

// dgramStoreMessageListener resolves a (msg, rinfo) => ... listener and stores
// its closure header into the socket's field 2.
func (e *Emitter) dgramStoreMessageListener(sk string, arg ast.Expression, pos ast.Pos) error {
	cb, err := e.resolveCallbackWithHints(arg, []Type{BufferType(), dgramRinfoType()})
	if err != nil {
		return err
	}
	if cb.kind != cbClosure {
		return fmt.Errorf("%d:%d: a dgram 'message' listener must be an arrow function literal", pos.Line, pos.Col)
	}
	// A Uint8Array message param is an object-reference array expecting a header
	// (TDD-00127); wrap the listener so the runtime's raw (ptr,len) message is
	// boxed before it is forwarded (rinfo passes through).
	listener := cb.hdrPtr
	if len(cb.ty.FuncParams) > 0 && cb.ty.FuncParams[0].IsArray {
		listener = e.dgramMsgHeaderAdapterClosure(cb.hdrPtr)
	}
	slot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 2", slot, dgramSocketIR, sk))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", listener, slot))
	return nil
}

// emitDgramMethod dispatches socket.bind/on/send/close.
func (e *Emitter) emitDgramMethod(objExpr ast.Expression, method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	objVal, err := e.emitExpr(objExpr)
	if err != nil {
		return Value{}, err
	}
	switch method {
	case "bind":
		if len(args) < 1 || len(args) > 2 {
			return Value{}, fmt.Errorf("%d:%d: socket.bind takes (port, callback?)", pos.Line, pos.Col)
		}
		portVal, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		port := e.coerce(portVal, TypeI64)
		port32 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = trunc i64 %s to i32", port32, port.Ref))
		e.emitInstr(fmt.Sprintf("call void @__kml_dgram_bind(ptr %s, i32 %s)", objVal.Ref, port32))
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
	case "on":
		evt, err := stringLiteralArg(args, 0, "socket.on", pos)
		if err != nil {
			return Value{}, err
		}
		if len(args) != 2 {
			return Value{}, fmt.Errorf("%d:%d: socket.on takes (event, listener)", pos.Line, pos.Col)
		}
		if evt != "message" {
			return Value{}, fmt.Errorf("%d:%d: a dgram socket supports only .on('message', listener) (got '%s')", pos.Line, pos.Col, evt)
		}
		if err := e.dgramStoreMessageListener(objVal.Ref, args[1], pos); err != nil {
			return Value{}, err
		}
		return Value{Ty: TypeVoid}, nil
	case "send":
		return e.emitDgramSend(objVal, args, pos)
	case "close":
		e.emitInstr(fmt.Sprintf("call void @__kml_dgram_close(ptr %s)", objVal.Ref))
		return Value{Ty: TypeVoid}, nil
	case "address":
		// getsockname on the socket fd — the `bind(0)` ephemeral-port idiom,
		// same shared AddressInfo builder the net server/socket use.
		if len(args) != 0 {
			return Value{}, fmt.Errorf("%d:%d: socket.address takes no arguments", pos.Line, pos.Col)
		}
		e.ensureNetRuntime()
		return e.emitNetAddressObject(e.netFieldFd32(objVal.Ref, dgramSocketIR)), nil
	}
	return Value{}, fmt.Errorf("%d:%d: a dgram socket has no method '%s'", pos.Line, pos.Col, method)
}

// emitDgramSend implements socket.send(msg, port, host).
func (e *Emitter) emitDgramSend(objVal Value, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 3 {
		return Value{}, fmt.Errorf("%d:%d: socket.send takes (msg, port, host)", pos.Line, pos.Col)
	}
	ptrRef, lenRef, err := e.zlibResolveInput(args[0], pos)
	if err != nil {
		return Value{}, err
	}
	portVal, err := e.emitExpr(args[1])
	if err != nil {
		return Value{}, err
	}
	port := e.coerce(portVal, TypeI64)
	port32 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = trunc i64 %s to i32", port32, port.Ref))
	hostVal, err := e.emitExpr(args[2])
	if err != nil {
		return Value{}, err
	}
	hostVal = e.coerce(hostVal, TypePtr)
	e.emitInstr(fmt.Sprintf("call void @__kml_dgram_send(ptr %s, ptr %s, i64 %s, i32 %s, ptr %s)",
		objVal.Ref, ptrRef, lenRef, port32, hostVal.Ref))
	return Value{Ty: TypeVoid}, nil
}
