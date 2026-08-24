// emit_tls.go — codegen for the `tls` module (TDD-00109). V1 is the client:
// tls.connect(port, host[, options][, onSecureConnect]). It reuses the net
// client path entirely — @__kml_tls_connect does the TCP connect + a blocking
// TLS handshake and returns a net-shaped socket (with an SSL* in field 5), so
// .on('data')/.write()/.end()/.on('close') dispatch through the net socket
// machinery unchanged.
package llvm

import (
	"fmt"

	"KlainMainLang/ast"
)

// emitTLSModuleCall dispatches tls.<method>(...).
func (e *Emitter) emitTLSModuleCall(method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	switch method {
	case "connect":
		return e.emitTLSConnect(args, pos)
	case "createServer":
		return e.emitTLSCreateServer(args, pos)
	}
	return Value{}, fmt.Errorf("%d:%d: tls.%s is not supported (V1: tls.connect, tls.createServer)", pos.Line, pos.Col, method)
}

// emitTLSConnect implements tls.connect(port, host[, options][, onSecureConnect]).
func (e *Emitter) emitTLSConnect(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 2 || len(args) > 4 {
		return Value{}, fmt.Errorf("%d:%d: tls.connect takes (port, host[, options][, onSecureConnect])", pos.Line, pos.Col)
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

	// Remaining args: an options object literal and/or a connect listener, in
	// either order after host (Node allows both).
	reject := 1
	var listener ast.Expression
	for _, a := range args[2:] {
		if obj, ok := a.(*ast.ObjectLiteral); ok {
			reject, err = e.tlsRejectUnauthorized(obj, pos)
			if err != nil {
				return Value{}, err
			}
		} else {
			listener = a
		}
	}

	e.ensureTLSRuntime()
	sk := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_tls_connect(i32 %s, ptr %s, i32 %d)", sk, port32, hostVal.Ref, reject))
	isnull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isnull, sk))
	okL := e.freshLabel("tlsconnok")
	failL := e.freshLabel("tlsconnfail")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isnull, failL, okL))

	e.emitLabel(failL)
	e.emitInternalThrow(e.internString("tls.connect: TLS connection failed"))

	e.emitLabel(okL)
	// The optional connect listener ('secureConnect', no args) fires once on the
	// first dispatch pass — the same field-4 mechanism net.connect uses.
	if listener != nil {
		cb, err := e.netArrowClosure(listener, nil, pos)
		if err != nil {
			return Value{}, err
		}
		e.netStorePtrField(sk, netSocketIR, 4, cb)
	}
	return Value{Ref: sk, Ty: NetSocketType()}, nil
}

// emitTLSCreateServer implements tls.createServer({ cert, key }, connectionListener?).
// It builds a net-shaped Server whose SSL_CTX* (field 3) makes the net accept
// loop wrap each accepted fd with a blocking SSL_accept (TDD-00110).
func (e *Emitter) emitTLSCreateServer(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 1 || len(args) > 2 {
		return Value{}, fmt.Errorf("%d:%d: tls.createServer takes (options, connectionListener?)", pos.Line, pos.Col)
	}
	obj, ok := args[0].(*ast.ObjectLiteral)
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: tls.createServer's options must be an object literal with { cert, key }", pos.Line, pos.Col)
	}
	var certExpr, keyExpr ast.Expression
	for _, prop := range obj.Properties {
		switch prop.Key {
		case "cert":
			certExpr = prop.Value
		case "key":
			keyExpr = prop.Value
		default:
			return Value{}, fmt.Errorf("%d:%d: tls.createServer supports only { cert, key } (not '%s')", pos.Line, pos.Col, prop.Key)
		}
	}
	if certExpr == nil || keyExpr == nil {
		return Value{}, fmt.Errorf("%d:%d: tls.createServer requires { cert, key } (PEM strings)", pos.Line, pos.Col)
	}
	certVal, err := e.emitExpr(certExpr)
	if err != nil {
		return Value{}, err
	}
	certVal = e.coerce(certVal, TypePtr)
	keyVal, err := e.emitExpr(keyExpr)
	if err != nil {
		return Value{}, err
	}
	keyVal = e.coerce(keyVal, TypePtr)

	e.ensureTLSRuntime()
	ctx := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_tls_server_ctx(ptr %s, ptr %s, ptr null)", ctx, certVal.Ref, keyVal.Ref))
	isnull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isnull, ctx))
	okL := e.freshLabel("tlssrvok")
	failL := e.freshLabel("tlssrvfail")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isnull, failL, okL))

	e.emitLabel(failL)
	e.emitInternalThrow(e.internString("tls.createServer: invalid certificate or private key"))

	e.emitLabel(okL)
	srv := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @calloc(i64 1, i64 32)", srv))
	// field 0 listenfd = -1 (not yet listening)
	lfd := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 0", lfd, netServerIR, srv))
	e.emitInstr(fmt.Sprintf("store i64 -1, ptr %s, align 8", lfd))
	// field 3 = the server SSL_CTX* (drives the accept-path TLS wrap)
	ctxp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 3", ctxp, netServerIR, srv))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", ctx, ctxp))
	if len(args) == 2 {
		if err := e.netStoreConnListener(srv, args[1], pos); err != nil {
			return Value{}, err
		}
	}
	e.emitInstr(fmt.Sprintf("call void @__kml_net_srv_register(ptr %s)", srv))
	return Value{Ref: srv, Ty: NetServerType()}, nil
}

// tlsRejectUnauthorized reads `{ rejectUnauthorized: false }` from a tls.connect
// options literal, returning 1 (verify — the default) or 0. Any other key is a
// clean error (the same static-options posture the rest of the compiler uses).
func (e *Emitter) tlsRejectUnauthorized(obj *ast.ObjectLiteral, pos ast.Pos) (int, error) {
	reject := 1
	for _, prop := range obj.Properties {
		if prop.Key != "rejectUnauthorized" {
			return 0, fmt.Errorf("%d:%d: only the { rejectUnauthorized } tls.connect option is supported (not '%s')", pos.Line, pos.Col, prop.Key)
		}
		lit, ok := prop.Value.(*ast.BooleanLiteral)
		if !ok {
			return 0, fmt.Errorf("%d:%d: tls.connect { rejectUnauthorized } must be a boolean literal", pos.Line, pos.Col)
		}
		if !lit.Value {
			reject = 0
		}
	}
	return reject, nil
}
