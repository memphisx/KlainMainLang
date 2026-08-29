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
	certVal, keyVal, err := e.emitCertKeyPtrs(args[0], "tls.createServer", pos)
	if err != nil {
		return Value{}, err
	}

	e.ensureTLSRuntime()
	ctx := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_tls_server_ctx(ptr %s, ptr %s, ptr null, i32 1)", ctx, certVal.Ref, keyVal.Ref))
	isnull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isnull, ctx))
	okL := e.freshLabel("tlssrvok")
	failL := e.freshLabel("tlssrvfail")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isnull, failL, okL))

	e.emitLabel(failL)
	e.emitInternalThrow(e.internString("tls.createServer: invalid certificate or private key"))

	e.emitLabel(okL)
	srv := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @calloc(i64 1, i64 %d)", srv, netServerStructSize))
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

// emitCertKeyPtrs reads a { cert, key } TLS options argument — either an inline
// object literal or a variable bound to one — and returns the two PEM strings as
// ptr Values. Shared by tls.createServer and http2.createSecureServer; `who`
// names the caller in error messages.
func (e *Emitter) emitCertKeyPtrs(optArg ast.Expression, who string, pos ast.Pos) (certVal, keyVal Value, err error) {
	if obj, ok := optArg.(*ast.ObjectLiteral); ok {
		var certExpr, keyExpr ast.Expression
		for _, prop := range obj.Properties {
			switch prop.Key {
			case "cert":
				certExpr = prop.Value
			case "key":
				keyExpr = prop.Value
			case "allowHTTP1":
				// Read by http2.createSecureServer's own option scan; ignored here.
			default:
				return Value{}, Value{}, fmt.Errorf("%d:%d: %s supports only { cert, key } (not '%s')", pos.Line, pos.Col, who, prop.Key)
			}
		}
		if certExpr == nil || keyExpr == nil {
			return Value{}, Value{}, fmt.Errorf("%d:%d: %s requires { cert, key } (PEM strings)", pos.Line, pos.Col, who)
		}
		cv, err := e.emitExpr(certExpr)
		if err != nil {
			return Value{}, Value{}, err
		}
		certVal = e.coerce(cv, TypePtr)
		kv, err := e.emitExpr(keyExpr)
		if err != nil {
			return Value{}, Value{}, err
		}
		keyVal = e.coerce(kv, TypePtr)
		return certVal, keyVal, nil
	}
	// A function first argument means the caller used the bare-listener form
	// (`createServer((req,res)=>…)`) — invalid for a TLS server, which needs the
	// { cert, key } options object first. Reject cleanly before emitting it.
	switch optArg.(type) {
	case *ast.ArrowFunction, *ast.FunctionExpression:
		return Value{}, Value{}, fmt.Errorf("%d:%d: %s requires a { cert, key } options object as its first argument (PEM strings)", pos.Line, pos.Col, who)
	}
	// Options bound to a variable (`const opts = { cert, key }`) — the binding's
	// static object type carries the fields; read them off the value.
	optVal, err := e.emitExpr(optArg)
	if err != nil {
		return Value{}, Value{}, err
	}
	if !optVal.Ty.IsObject {
		return Value{}, Value{}, fmt.Errorf("%d:%d: %s's options must be an object with { cert, key } (PEM strings)", pos.Line, pos.Col, who)
	}
	load := func(name string) (Value, error) {
		idx, fty, ok := optVal.Ty.FieldIndex(name)
		if !ok || fty.IR != "ptr" {
			return Value{}, fmt.Errorf("%d:%d: %s's options object must have a '%s: string' field (PEM)", pos.Line, pos.Col, who, name)
		}
		g := e.freshReg()
		v := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", g, optVal.Ty.StructIR(), optVal.Ref, idx))
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", v, g))
		return Value{Ref: v, Ty: TypePtr}, nil
	}
	if certVal, err = load("cert"); err != nil {
		return Value{}, Value{}, err
	}
	if keyVal, err = load("key"); err != nil {
		return Value{}, Value{}, err
	}
	return certVal, keyVal, nil
}

// emitHTTP2CreateSecureServer implements http2.createSecureServer(options,
// handler?) — an h2-over-TLS server (TDD-00111 Stage 3b). It builds the server
// SSL_CTX from { cert, key } and stores it in @__kml_http_tls_ctx, which the
// event loop's TLS accept branch (ensureH2TLSServer) reads: each accepted
// connection is TLS-handshaken, and an ALPN-negotiated `h2` client is driven as
// an nghttp2 session over the SSL shims. Non-h2 clients are closed — matching
// Node's allowHTTP1:false default. The server handle, listen/close/address, the
// compat (req,res) + the 'stream' streams API are all inherited from the shared
// http server core (emitHTTPCreateServer, the same one http2.createServer uses).
func (e *Emitter) emitHTTP2CreateSecureServer(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 1 || len(args) > 2 {
		return Value{}, fmt.Errorf("%d:%d: http2.createSecureServer takes (options, handler?)", pos.Line, pos.Col)
	}
	// Set the flag + declare the C ABI BEFORE the handler wiring triggers
	// ensureHTTPRuntime, so the event loop is emitted with its TLS accept branch.
	e.ensureH2TLSServer()
	// allowHTTP1: true opts the h2 secure server into serving an ALPN-negotiated
	// (or no-ALPN) HTTP/1.1 client over TLS instead of dropping it — the 1.1
	// fallback (TDD-00111). Enable the HTTPS/1.1 path so the accept branch routes
	// a non-h2 client into the fiber conn table.
	if e.objLiteralBoolOption(args[0], "allowHTTP1") {
		e.ensureHTTPS1Server()
	}

	certVal, keyVal, err := e.emitCertKeyPtrs(args[0], "http2.createSecureServer", pos)
	if err != nil {
		return Value{}, err
	}
	ctx := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_tls_server_ctx(ptr %s, ptr %s, ptr null, i32 1)", ctx, certVal.Ref, keyVal.Ref))
	isnull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isnull, ctx))
	okL := e.freshLabel("h2ssrvok")
	failL := e.freshLabel("h2ssrvfail")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isnull, failL, okL))
	e.emitLabel(failL)
	e.emitInternalThrow(e.internString("http2.createSecureServer: invalid certificate or private key"))
	e.emitLabel(okL)
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr @__kml_http_tls_ctx, align 8", ctx))

	// The handler (if any) drives the shared http server core, which already
	// wires the h2 dispatch bridge the TLS drive path reuses.
	return e.emitHTTPCreateServer(args[1:], pos)
}

// ensureHTTPS1Server wires the HTTPS/1.1 server path (https.createServer, or the
// allowHTTP1 fallback of http2.createSecureServer): the flag the event-loop
// emitter reads to route a non-h2 TLS connection into the fiber conn table, and
// libssl (usedTLS) so tls.c is compiled/linked. The @__kml_http_tls_ctx slot is
// shared with the h2 secure server — emit it only when that path hasn't already.
func (e *Emitter) ensureHTTPS1Server() {
	if e.usedHTTPS1Server {
		return
	}
	e.usedHTTPS1Server = true
	e.usedTLS = true
	if !e.usedH2TLSServer {
		e.emitGlobal(`@__kml_http_tls_ctx = global ptr null`)
	}
}

// objLiteralBoolOption reports whether an inline options object literal sets
// `key: true`. Only the literal form is inspected (a variable-bound options
// object is not) — enough for the allowHTTP1 toggle, which is written inline in
// practice; a bound object leaves it at its default (false).
func (e *Emitter) objLiteralBoolOption(optArg ast.Expression, key string) bool {
	obj, ok := optArg.(*ast.ObjectLiteral)
	if !ok {
		return false
	}
	for _, prop := range obj.Properties {
		if prop.Key == key {
			if lit, ok := prop.Value.(*ast.BooleanLiteral); ok {
				return lit.Value
			}
		}
	}
	return false
}

// emitHTTPSCreateServer implements https.createServer(options, handler?) — an
// HTTPS/1.1 server (TDD-00111). It builds the server SSL_CTX from { cert, key }
// with an http/1.1-only ALPN (offer_h2 = 0, since no nghttp2 driver is linked),
// stores it in @__kml_http_tls_ctx, and delegates the handle / listen / close /
// address / (req,res) wiring to the shared http server core — so an accepted
// TLS connection is served exactly like a plain http.createServer one, only
// with its socket I/O routed through the SSL shims.
func (e *Emitter) emitHTTPSCreateServer(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 1 || len(args) > 2 {
		return Value{}, fmt.Errorf("%d:%d: https.createServer takes (options, handler?)", pos.Line, pos.Col)
	}
	// Set the flag before the handler wiring triggers ensureHTTPRuntime, so the
	// event loop is emitted with its TLS accept branch.
	e.ensureHTTPS1Server()

	certVal, keyVal, err := e.emitCertKeyPtrs(args[0], "https.createServer", pos)
	if err != nil {
		return Value{}, err
	}
	ctx := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_tls_server_ctx(ptr %s, ptr %s, ptr null, i32 0)", ctx, certVal.Ref, keyVal.Ref))
	isnull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isnull, ctx))
	okL := e.freshLabel("httpssrvok")
	failL := e.freshLabel("httpssrvfail")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isnull, failL, okL))
	e.emitLabel(failL)
	e.emitInternalThrow(e.internString("https.createServer: invalid certificate or private key"))
	e.emitLabel(okL)
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr @__kml_http_tls_ctx, align 8", ctx))

	return e.emitHTTPCreateServer(args[1:], pos)
}
