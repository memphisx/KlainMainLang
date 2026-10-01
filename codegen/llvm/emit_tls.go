// emit_tls.go — the TLS servers codegen still builds: https.createServer's
// and http2.createSecureServer's { cert, key } options and their SSL_CTX
// global. (Node's `tls` module itself is lib/node/tls.ts.)
package llvm

import (
	"fmt"

	"KlainMainLang/ast"
)

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

// ensureHTTPS1Server wires the HTTPS/1.1 server path (https.createServer, or the
// allowHTTP1 fallback of http2.createSecureServer): the flag the event-loop
// emitter reads to route a non-h2 TLS connection into the fiber conn table, and
// libssl (usedTLS) so tls.c is compiled/linked. The @__kml_http_tls_ctx slot is
// shared with the h2 secure server — emit it only when that path hasn't already.
func (e *Emitter) ensureHTTPS1Server() {
	if e.httpS1Wired {
		return
	}
	e.httpS1Wired = true
	e.usedHTTPS1Server = true
	e.usedTLS = true
	e.ensureHTTPTLSCtxGlobal()
}

// ensureHTTPTLSCtxGlobal emits the @__kml_http_tls_ctx global once — it is shared
// by the HTTPS/1.1 and the h2-over-TLS server paths, either of which may be
// wired first (including via a TDD-00191 pre-scan), so a single dedup flag keeps
// it from being emitted twice.
func (e *Emitter) ensureHTTPTLSCtxGlobal() {
	if e.httpTLSCtxEmitted {
		return
	}
	e.httpTLSCtxEmitted = true
	e.emitGlobal(`@__kml_http_tls_ctx = global ptr null`)
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
	// TDD-00191 Stage 3: hand the SSL_CTX* to the handle builder, which stores it
	// in the handle (slot 4) and — for the primary only — into the reactor's
	// @__kml_http_tls_ctx global. An additional HTTPS server carries its ctx in
	// the handle and hands it to the extra-listener table at listen().
	e.pendingServerTLSCtx = ctx
	return e.emitHTTPCreateServer(args[1:], pos)
}
