// emit_ws_server.go — klain:ws WebSocketServer (TDD-00158 Stage 2): the
// ergonomic WebSocket-server convenience (the WSConnection frame layer the
// `ws` npm package occupies in Node), re-homed under the explicitly-non-Node
// `klain:ws` specifier and rebuilt on the faithful HTTP `'upgrade'` event.
//
//	import http from 'http'
//	import { WebSocketServer } from 'klain:ws'
//	const server = http.createServer(handler)
//	const wss = new WebSocketServer({ server })
//	wss.on('connection', (socket) => {
//	  socket.onmessage = (ev) => socket.send('echo: ' + ev.data)
//	})
//	server.listen(8080)
//
// `wss://` falls out: with `https.createServer`, the handshake + frame I/O
// route through the TLS-aware conn shims (emitWSHandshakeAndLoop, gated on
// usedHTTPS1Server), exactly like the raw upgrade socket.
//
// The http server is a process-singleton (its dispatcher + handler globals are
// module thread-locals), so the WebSocketServer handle carries no per-instance
// state; the `{ server }` option is validated for shape but not otherwise
// stored, and `.on('connection', …)` writes the connection handler into
// @__kml_listen_ws_handler — the same global the dispatcher's WS block reads.
package llvm

import (
	"fmt"

	"KlainMainLang/ast"
)

// WebSocketServerType is the klain:ws `new WebSocketServer({server})` handle —
// a bare tagged value with no per-instance fields (see the file doc comment).
func WebSocketServerType() Type {
	return Type{IR: "ptr", IsWebSocketServer: true}
}

// emitNewWebSocketServer handles `new WebSocketServer({ server })`. The single
// options argument must be an object literal carrying a `server` property (the
// http/https server to attach to); since that server is the process-singleton,
// the property is validated for presence but its value is only evaluated for
// its side effects. Returns an opaque handle whose only method is
// `.on('connection', …)`.
func (e *Emitter) emitNewWebSocketServer(ex *ast.NewExpression) (Value, error) {
	if len(ex.Args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: new WebSocketServer takes one options object: new WebSocketServer({ server })", ex.GetPos().Line, ex.GetPos().Col)
	}
	lit, ok := ex.Args[0].(*ast.ObjectLiteral)
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: new WebSocketServer's argument must be an object literal ({ server })", ex.GetPos().Line, ex.GetPos().Col)
	}
	hasServer := false
	for _, p := range lit.Properties {
		if p.Key == "server" {
			hasServer = true
			// Evaluate for side effects / binding validity; the singleton
			// server needs nothing stored from it.
			if _, err := e.emitExpr(p.Value); err != nil {
				return Value{}, err
			}
		}
	}
	if !hasServer {
		return Value{}, fmt.Errorf("%d:%d: new WebSocketServer({ server }) requires a 'server' property naming the http/https server to attach to", ex.GetPos().Line, ex.GetPos().Col)
	}
	e.ensureHTTPRuntime()
	// The handle is opaque; a null ptr is a fine value since no method reads it.
	return Value{Ref: "null", Ty: WebSocketServerType()}, nil
}

// emitWebSocketServerMethod dispatches a method call on a WebSocketServer
// handle. Only `.on('connection', listener)` is supported — it stores the
// per-connection WSConnection handler into the global the dispatcher reads.
func (e *Emitter) emitWebSocketServerMethod(objExpr ast.Expression, method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	if _, err := e.emitExpr(objExpr); err != nil {
		return Value{}, err
	}
	switch method {
	case "on":
		evt, err := stringLiteralArg(args, 0, "WebSocketServer.on", pos)
		if err != nil {
			return Value{}, err
		}
		if evt != "connection" {
			return Value{}, fmt.Errorf("%d:%d: a WebSocketServer supports .on('connection', listener) (got '%s')", pos.Line, pos.Col, evt)
		}
		if len(args) != 2 {
			return Value{}, fmt.Errorf("%d:%d: WebSocketServer.on takes (event, listener)", pos.Line, pos.Col)
		}
		// The connection listener receives a WSConnection — contextually type
		// an unannotated arrow param to it (ADR-00632 hint path).
		cb, err := e.resolveCallbackWithHints(args[1], []Type{WSConnectionType()})
		if err != nil {
			return Value{}, err
		}
		if cb.kind != cbClosure {
			return Value{}, fmt.Errorf("%d:%d: a 'connection' listener must be a function literal", pos.Line, pos.Col)
		}
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr @__kml_listen_ws_handler, align 8", cb.hdrPtr))
		return Value{Ty: TypeVoid}, nil
	}
	return Value{}, fmt.Errorf("%d:%d: a WebSocketServer supports .on('connection', listener) (got '%s')", pos.Line, pos.Col, method)
}
