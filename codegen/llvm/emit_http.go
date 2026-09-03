// emit_http.go — http.listen(port, handler) and http.close() (TDD-00027): a
// minimal HTTP server (TDD-00004 V1) built on the generalized event loop
// (TDD-00006 Part 1, see runtime_http.go's ensureHTTPRuntime). Both are bare
// global functions, like fetch/process.exit — not methods on a returned
// Server handle, since http.close() can only ever be reached from code
// already running inside the event loop itself (a request handler, a
// setTimeout/setInterval callback, or a process.on(...) handler): there is
// no point in a program's control flow where TS code could hold and later
// invoke a handle object instead. http.listen() blocks until the loop
// actually stops — either because it never does (V1 before TDD-00027), or
// because http.close() was called and every already-accepted connection has
// since finished.
package llvm

import (
	"fmt"
	"strings"

	"KlainMainLang/ast"
)

// isPlainStringType reports whether t is a bare string (IR "ptr" with none
// of the other flags that mark a more specific ptr-shaped type — object,
// array, Map/Set, closure, Promise, Date, class instance, ...). Used to
// validate that a http.listen response's optional `headers` field really is
// Map<string, string>, since (unlike status/body, which just get coerced)
// treating a wrong-shaped value as a Map and walking its "entries" at
// runtime would corrupt memory rather than just misbehave.
func isPlainStringType(t Type) bool {
	return t.IR == "ptr" && !t.IsArray && !t.IsObject && !t.IsMap && !t.IsSet &&
		!t.IsFunc && !t.IsDynamic && !t.IsPromise && !t.IsDate && !t.IsResponse &&
		!t.IsClass && !t.IsGroupMap && !t.IsNull && !t.IsUndefined
}

// emitHTTPListen validates its arguments (port: number, handler:
// (req: HttpRequest) => T where T has status/body fields, and optionally a
// headers: Map<string,string> field, plus an optional third { workers: N }
// options object — TDD-00025), binds and listens on the given port, builds a
// dispatcher function specialized to the handler's own closure/return type
// (since reading status/body/headers off an arbitrary user-declared return
// type needs Go-side knowledge of its field offsets, unlike the fully
// generic timer/qsort trampolines), forks into N worker processes sharing
// that one listening socket (a no-op when the third argument is omitted or
// N <= 1 — today's single-process behavior, unchanged), registers the
// dispatcher with the event loop, and hands control to it — returning once
// the loop actually stops (TDD-00027: http.close(), called from inside the
// handler/a timer/a signal handler, plus every already-accepted connection
// finishing naturally).
//
// Only one http.listen call site is allowed per program (httpListenCallSeen
// below): buildHTTPDispatcher emits a fixed-name @__kml_http_dispatch, which
// was harmless when a second call site was always dead code (the first call
// never returned) but would now produce a confusing duplicate-symbol error
// from the LLVM backend instead, since http.close() lets control genuinely
// reach a second call site.
func (e *Emitter) emitHTTPListen(args []ast.Expression, pos ast.Pos) (Value, error) {
	if e.httpListenCallSeen {
		return Value{}, fmt.Errorf("%d:%d: http.listen may only be called once per program (V1 limitation, unchanged by http.close())", pos.Line, pos.Col)
	}
	e.httpListenCallSeen = true
	if len(args) != 2 && len(args) != 3 {
		return Value{}, fmt.Errorf("%d:%d: http.listen takes 2 arguments (port, handler) or 3 (port, handler, { workers: N })", pos.Line, pos.Col)
	}
	portVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	portVal = e.coerce(portVal, TypeI64)

	// The handler runs on a connection fiber and returns its response object
	// straight out of the bare async slot — it is NOT a task promise. Keep the
	// old inline async model for it (no settled-promise/setjmp wrapper), TDD-00084
	// Part A. A handler defined as a separate top-level function that awaits fetch
	// is a may-suspend body and untouched by that wrapper anyway.
	e.emittingHTTPHandler = true
	e.httpHandlerNode = args[1]
	handlerVal, err := e.emitExpr(args[1])
	e.emittingHTTPHandler = false
	e.httpHandlerNode = nil
	if err != nil {
		return Value{}, err
	}
	if !handlerVal.Ty.IsFunc {
		return Value{}, fmt.Errorf("%d:%d: http.listen's second argument must be a function", pos.Line, pos.Col)
	}
	if len(handlerVal.Ty.FuncParams) != 1 {
		return Value{}, fmt.Errorf("%d:%d: http.listen's handler must take exactly one parameter (req: HttpRequest)", pos.Line, pos.Col)
	}
	if handlerVal.Ty.FuncRetType == nil || (!handlerVal.Ty.FuncRetType.IsObject && !handlerVal.Ty.FuncRetType.IsPromise) {
		return Value{}, fmt.Errorf("%d:%d: http.listen's handler must return an object type (or Promise of one, for an async handler) with status/body fields", pos.Line, pos.Col)
	}
	// An async handler (needed for `await fetch(...)` inside it, the main
	// reason to want one) declares itself as returning Promise<T>; the
	// dispatcher calls it exactly like a sync handler (the call doesn't
	// return until the handler's body has actually finished — any internal
	// await yields the *connection's own fiber* via swapcontext, transparent
	// to this call) and then unwraps T from the returned promise slot, the
	// same load+free shape emitAwait's own generic (non-fetch) unwrap uses.
	isAsyncHandler := handlerVal.Ty.FuncRetType.IsPromise
	retTy := *handlerVal.Ty.FuncRetType
	if isAsyncHandler {
		if retTy.PromiseType == nil {
			return Value{}, fmt.Errorf("%d:%d: http.listen's async handler must return Promise<T> where T has status/body fields", pos.Line, pos.Col)
		}
		retTy = *retTy.PromiseType
	}
	if _, _, ok := retTy.FieldIndex("status"); !ok {
		return Value{}, fmt.Errorf("%d:%d: http.listen's handler return type must have a 'status: number' field", pos.Line, pos.Col)
	}
	if _, _, ok := retTy.FieldIndex("body"); !ok {
		return Value{}, fmt.Errorf("%d:%d: http.listen's handler return type must have a 'body: string' field", pos.Line, pos.Col)
	}
	if _, hTy, ok := retTy.FieldIndex("headers"); ok {
		if !hTy.IsMap || hTy.MapKey == nil || hTy.MapVal == nil ||
			!isPlainStringType(*hTy.MapKey) || !isPlainStringType(*hTy.MapVal) {
			return Value{}, fmt.Errorf("%d:%d: http.listen's handler return type's 'headers' field must be Map<string, string>", pos.Line, pos.Col)
		}
	}
	// Optional binary response body (TDD-00026/ADR-00106): bodyBytes wins
	// over body's own strlen-computed length when present and non-null at
	// runtime, mirroring headers' own "field present at the type level,
	// null-checked at runtime" convention above — see
	// buildHTTPDispatcher's response-writing tail.
	if _, bbTy, ok := retTy.FieldIndex("bodyBytes"); ok {
		if !bbTy.IsArrayBuffer {
			return Value{}, fmt.Errorf("%d:%d: http.listen's handler return type's 'bodyBytes' field must be ArrayBuffer", pos.Line, pos.Col)
		}
	}
	paramTy := handlerVal.Ty.FuncParams[0]

	// Third argument, { workers: N } (TDD-00025) and/or { ws } (TDD-00039
	// Stage 1): any value whose inferred type has these fields — no shared
	// ListenOptions interface has to exist, matching the same
	// FieldIndex-on-the-inferred-type pattern fetch's own optional init
	// object already uses (emit_fetch.go). Absent 'workers' (the two-argument
	// form) means 1 worker, byte-identical to today's single-process behavior
	// — the interned "1" is a literal operand, not a register, since nothing
	// needed evaluating. The former `ws` handler option was removed (TDD-00158)
	// — a WebSocket server is klain:ws's `new WebSocketServer({server})` now.
	workersRef := "1"
	if len(args) == 3 {
		optsVal, err := e.emitExpr(args[2])
		if err != nil {
			return Value{}, err
		}
		if !optsVal.Ty.IsObject {
			return Value{}, fmt.Errorf("%d:%d: http.listen's third argument must be an object with a 'workers' field", pos.Line, pos.Col)
		}
		// The former `ws` handler option (a WebSocket server directly on
		// http.listen) was removed (TDD-00158): a built-in WebSocket server is
		// not Node, so it lives under the explicit klain:ws specifier now —
		// `new WebSocketServer({ server }).on('connection', …)` on an
		// http/https server. Reject with a pointer there rather than silently
		// ignoring the field.
		if _, _, hasWSField := optsVal.Ty.FieldIndex("ws"); hasWSField {
			return Value{}, fmt.Errorf("%d:%d: http.listen no longer takes a 'ws' handler — use klain:ws: `import { WebSocketServer } from 'klain:ws'`, then `new WebSocketServer({ server }).on('connection', socket => …)`", pos.Line, pos.Col)
		}
		_, _, hasWorkersField := optsVal.Ty.FieldIndex("workers")
		if hasWorkersField {
			// TDD-00098: fork-based clustering and Worker threads are a
			// known-hazardous mix (fork() in a multi-threaded process only
			// reproduces the calling thread; the children would inherit
			// locked/torn thread state) — rejected outright for V1.
			if e.usedWorkerRuntime {
				return Value{}, fmt.Errorf("%d:%d: http.listen's { workers: N } fork-clustering cannot be combined with Worker threads in the same program", pos.Line, pos.Col)
			}
			idx, fieldTy, _ := optsVal.Ty.FieldIndex("workers")
			if !fieldTy.Float && !isIntegerNumberTy(fieldTy) {
				return Value{}, fmt.Errorf("%d:%d: http.listen's 'workers' field must be a number", pos.Line, pos.Col)
			}
			// `workers` is a `number` (float64, TDD-00123); the worker count is an
			// integer, so coerce it (truncating) to i64.
			workersRef = e.coerce(e.loadFieldValue(optsVal, idx, fieldTy), TypeI64).Ref
		}
		if !hasWorkersField {
			return Value{}, fmt.Errorf("%d:%d: http.listen's third argument must have a 'workers' field", pos.Line, pos.Col)
		}
	}

	e.ensureHTTPRuntime()

	port32 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = trunc i64 %s to i32", port32, portVal.Ref))
	listenfd := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_http_bind_and_listen(i32 %s)", listenfd, port32))

	// Fork right here — after bind+listen succeeds, before any
	// connection-fiber state exists (@__kml_conn_data/len/cap, set up by
	// buildHTTPDispatcher/the event loop below). Every process that falls
	// through (the original plus every fork) shares this same listenfd and
	// proceeds identically from here on — see TDD-00025's Design section.
	e.emitInstr(fmt.Sprintf("call void @__kml_http_cluster_fork(i64 %s)", workersRef))

	// HTTP/2 server (TDD-00111 Stage 3): an http.listen server transparently
	// accepts h2c (cleartext prior-knowledge) connections — the nghttp2 driver
	// (http2src/http2.c) is compiled + linked, and the connection path routes a
	// request whose first bytes are the h2 preface into it. Enabling this makes
	// nghttp2 a link dependency for http.listen programs.
	e.usedHTTP2 = true
	e.emitHTTP2ServerDecls()

	if err := e.buildHTTPDispatcher(paramTy, retTy, isAsyncHandler); err != nil {
		return Value{}, err
	}

	e.emitInstr(fmt.Sprintf("store i32 %s, ptr @__kml_listen_fd, align 4", listenfd))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr @__kml_listen_handler, align 8", handlerVal.Ref))
	e.emitInstr("store ptr @__kml_http_dispatch, ptr @__kml_listen_dispatch, align 8")
	e.emitInstr("call void @__kml_event_loop_run()")
	e.emitPostLoopFlush()
	return Value{Ty: TypeVoid}, nil
}

// emitNewServerResponse allocates a fresh ServerResponseType `res` for Node's
// http.createServer path (TDD-00131), initialized to status 200, an empty body,
// and an empty headers map. res.writeHead/setHeader/write/end then mutate these.
func (e *Emitter) emitNewServerResponse() string {
	ty := ServerResponseType()
	structIR := ty.StructIR()
	res := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", res, ty.StructSize()))
	store := func(field, valIR, val string) {
		idx, _, _ := ty.FieldIndex(field)
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, structIR, res, idx))
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align 8", valIR, val, gep))
	}
	store("status", "i64", "200")
	store("body", "ptr", e.internString(""))
	emptyMap := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_map_str_create()", emptyMap))
	store("headers", "ptr", emptyMap)
	return res
}

// chainedCreateServerListen matches the chained Node idiom
// `http.createServer(...).listen(...)` (either the qualified form or a named
// `createServer` import — both reach codegen as a call on the http marker),
// returning the two calls' argument lists. Shared by expression emission, the
// var-decl split (`const server = …` must store the handle *before* the ready
// callback and the event loop run inside listen), and call-type inference.
func chainedCreateServerListen(ex *ast.CallExpression) (createArgs, listenArgs []ast.Expression, ok bool) {
	mem, isMem := ex.Callee.(*ast.MemberExpression)
	if !isMem || mem.Property != "listen" {
		return nil, nil, false
	}
	inner, isCall := mem.Object.(*ast.CallExpression)
	if !isCall {
		return nil, nil, false
	}
	im, isMem2 := inner.Callee.(*ast.MemberExpression)
	if !isMem2 || im.Property != "createServer" {
		return nil, nil, false
	}
	id, isID := im.Object.(*ast.Identifier)
	if !isID || id.Name != "http__kml_builtin" {
		return nil, nil, false
	}
	return inner.Args, ex.Args, true
}

// emitChainedCreateServerListen emits the chained form via the bound-handle
// machinery: build the handle, optionally pre-store it (the var-decl split),
// then listen — which fires the ready callback, runs the event loop, and
// returns the handle (Node's listen() returns the server).
func (e *Emitter) emitChainedCreateServerListen(createArgs, listenArgs []ast.Expression, store func(Value), pos ast.Pos) (Value, error) {
	handle, err := e.emitHTTPCreateServer(createArgs, pos)
	if err != nil {
		return Value{}, err
	}
	if store != nil {
		store(handle)
	}
	return e.emitHTTPServerListen(handle, listenArgs, pos)
}

// unwrapTestWrapper returns the callback wrapped by a `test` builtin counting
// wrapper (`mustCall(fn)`/`mustCallAtLeast`/`mustSucceed` — whose value has
// the wrapped callback's own function type), or expr itself when unwrapped.
func unwrapTestWrapper(expr ast.Expression) ast.Expression {
	call, ok := expr.(*ast.CallExpression)
	if !ok || len(call.Args) < 1 {
		return expr
	}
	m, ok := call.Callee.(*ast.MemberExpression)
	if !ok {
		return expr
	}
	id, ok := m.Object.(*ast.Identifier)
	if !ok || id.Name != "test__kml_builtin" {
		return expr
	}
	switch m.Property {
	case "mustCall", "mustCallAtLeast", "mustSucceed":
		return call.Args[0]
	}
	return expr
}

// contextTypeArrowParams contextually types an inline arrow callback the way
// real Node infers it from the API signature: each un-annotated simple
// parameter (up to len(names)) gets the corresponding type name. Sees through
// a `test` counting wrapper. A no-op for anything that isn't an inline arrow
// or already carries annotations/patterns.
func contextTypeArrowParams(expr ast.Expression, names ...string) {
	var params []ast.Param
	switch fn := unwrapTestWrapper(expr).(type) {
	case *ast.ArrowFunction:
		params = fn.Params
	case *ast.FunctionExpression:
		params = fn.Params
	default:
		return
	}
	if len(params) > len(names) {
		return
	}
	names = names[:len(params)]
	for i := range names {
		p := &params[i]
		if p.Type == nil && p.ArrayPattern == nil && p.ObjectPattern == nil && !p.Rest {
			p.Type = &ast.TypeAnnotation{Name: names[i], Source: "ts"}
		}
	}
}

// emitHTTPCreateServerCore validates and emits the (req, res) handler closure,
// builds the res-mode dispatcher, and registers handler + dispatcher with the
// event-loop globals. Shared by the chained `http.createServer(cb).listen(...)`
// expression and the variable-bound handle form (`const server =
// http.createServer(cb)`); neither binds a port here — that stays in the
// respective listen path.
func (e *Emitter) emitHTTPCreateServerCore(cbExpr ast.Expression, pos ast.Pos) error {
	// Contextual typing: Node handlers are written untyped — `(req, res) =>
	// …` — because real Node infers both from the createServer signature. An
	// inline arrow with two un-annotated params gets the same treatment here,
	// so the corpus/Node idiom compiles without this compiler's explicit
	// `req: IncomingMessage, res: ServerResponse` annotations. The arrow may
	// also be wrapped in a `test` builtin counting wrapper —
	// `createServer(mustCall((req, res) => …))` is Node's own test idiom —
	// whose return type mirrors the wrapped callback, so annotating the inner
	// arrow types the whole expression.
	contextTypeArrowParams(cbExpr, "IncomingMessage", "ServerResponse")
	// A zero-parameter listener — Node's `createServer(common.mustNotCall())`
	// idiom (a server that must never see a request), or any `() => …` —
	// wraps into a synthesized (req, res) handler that invokes it, so the
	// mustNotCall wrapper still registers its exit-verified failure on a hit.
	if t := e.inferExprType(cbExpr); t.IsFunc && len(t.FuncParams) == 0 {
		cbExpr = ast.NewArrowFunction(
			[]ast.Param{
				{Name: "req", Type: &ast.TypeAnnotation{Name: "IncomingMessage", Source: "ts"}},
				{Name: "res", Type: &ast.TypeAnnotation{Name: "ServerResponse", Source: "ts"}},
			},
			nil, nil,
			ast.NewBlockStatement([]ast.Statement{
				ast.NewExpressionStatement(ast.NewCallExpression(cbExpr, nil, pos), pos),
			}, pos), pos)
	}
	e.emittingHTTPHandler = true
	e.httpHandlerNode = cbExpr
	handlerVal, err := e.emitExpr(cbExpr)
	e.emittingHTTPHandler = false
	e.httpHandlerNode = nil
	if err != nil {
		return err
	}
	if !handlerVal.Ty.IsFunc || len(handlerVal.Ty.FuncParams) != 2 {
		return fmt.Errorf("%d:%d: http.createServer's listener must be (req: IncomingMessage, res: ServerResponse) => void", pos.Line, pos.Col)
	}
	if !handlerVal.Ty.FuncParams[1].IsServerResponse {
		return fmt.Errorf("%d:%d: http.createServer's listener's second parameter must be typed ServerResponse (`res: ServerResponse`)", pos.Line, pos.Col)
	}
	paramTy := handlerVal.Ty.FuncParams[0]
	retTy := ServerResponseType()

	e.ensureHTTPRuntime()
	e.usedHTTP2 = true
	e.emitHTTP2ServerDecls()

	e.httpResMode = true
	dispErr := e.buildHTTPDispatcher(paramTy, retTy, false)
	e.httpResMode = false
	if dispErr != nil {
		return dispErr
	}

	e.emitInstr(fmt.Sprintf("store ptr %s, ptr @__kml_listen_handler, align 8", handlerVal.Ref))
	e.emitInstr("store ptr @__kml_http_dispatch, ptr @__kml_listen_dispatch, align 8")
	return nil
}

// emitHTTPCreateServer implements the variable-bound form `const server =
// http.createServer((req, res) => …)` — the standard Node idiom the chained
// form doesn't cover. The handle is a single heap i64 holding the listen fd
// (-1 until .listen()); the handler/dispatcher are registered immediately
// (single-server V1, same @__kml_listen_* globals as the chained form).
func (e *Emitter) emitHTTPCreateServer(args []ast.Expression, pos ast.Pos) (Value, error) {
	// Node's (options, listener) two-arg form: an empty options literal is
	// accepted (the common `createServer({}, cb)`), as is
	// `{requireHostHeader: false}` — this dispatcher never enforced a Host
	// header, so accepting the flag states existing behavior rather than
	// silently changing any (ADR-00503). Every other option (timeouts,
	// h2 settings, …) stays a clean rejection rather than a silent ignore.
	if len(args) == 2 {
		lit, ok := args[0].(*ast.ObjectLiteral)
		if !ok {
			return Value{}, fmt.Errorf("%d:%d: createServer's options object is not supported (only the bare listener form, or an options literal)", pos.Line, pos.Col)
		}
		for _, prop := range lit.Properties {
			if prop.Key == "requireHostHeader" {
				if bl, isB := prop.Value.(*ast.BooleanLiteral); isB && !bl.Value {
					continue
				}
				return Value{}, fmt.Errorf("%d:%d: createServer's requireHostHeader option supports only the literal false (this dispatcher never enforces a Host header)", pos.Line, pos.Col)
			}
			return Value{}, fmt.Errorf("%d:%d: createServer option '%s' is not supported (only {} or {requireHostHeader: false})", pos.Line, pos.Col, prop.Key)
		}
		args = args[1:]
	}
	if len(args) > 1 {
		return Value{}, fmt.Errorf("%d:%d: http.createServer takes one listener (req, res) => void (or none, with a later server.on('request', listener))", pos.Line, pos.Col)
	}
	if e.httpListenCallSeen {
		return Value{}, fmt.Errorf("%d:%d: only one HTTP server (http.listen or http.createServer) is supported per program (V1)", pos.Line, pos.Col)
	}
	e.httpListenCallSeen = true
	if len(args) == 0 {
		// Node's `const s = http.createServer(); s.on('request', cb)` split —
		// the handler arrives via .on('request', …) before .listen().
		e.httpServerHandlerPending = true
	} else if err := e.emitHTTPCreateServerCore(args[0], pos); err != nil {
		return Value{}, err
	}
	e.ensureCalloc()
	// 16 bytes: slot 0 = listen fd (-1 until .listen()), slot 1 = a
	// pending 'listening' listener closure (ADR-00502).
	srv := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @calloc(i64 1, i64 16)", srv))
	e.emitInstr(fmt.Sprintf("store i64 -1, ptr %s, align 8", srv))
	return Value{Ref: srv, Ty: HTTPServerType()}, nil
}

// emitNewH2ServerStream allocates a fresh Http2ServerStream (TDD-00139 Stage
// 2): status 200 / empty body / empty headers (the response side, mirroring
// ServerResponse) plus the request body for stream.on('data').
func (e *Emitter) emitNewH2ServerStream(reqBody, reqBodyLen string) string {
	ty := Http2ServerStreamType()
	structIR := ty.StructIR()
	st := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", st, ty.StructSize()))
	store := func(field, valIR, val string) {
		idx, _, _ := ty.FieldIndex(field)
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, structIR, st, idx))
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align 8", valIR, val, gep))
	}
	store("status", "i64", "200")
	store("body", "ptr", e.internString(""))
	emptyMap := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_map_str_create()", emptyMap))
	store("headers", "ptr", emptyMap)
	store("reqBody", "ptr", reqBody)
	store("reqBodyLen", "i64", reqBodyLen)
	return st
}

// emitHTTPStreamHandlerCore is emitHTTPCreateServerCore's http2 core-streams
// twin (TDD-00139 Stage 2): validates and emits the `(stream, headers)`
// handler, builds the dispatcher in stream mode, and registers it.
func (e *Emitter) emitHTTPStreamHandlerCore(cbExpr ast.Expression, pos ast.Pos) error {
	// Node's listener is (stream, headers, flags) — flags is a number most
	// handlers omit.
	contextTypeArrowParams(cbExpr, "__kml_h2_stream", "__kml_h2_headers", "number")
	e.emittingHTTPHandler = true
	e.httpHandlerNode = cbExpr
	handlerVal, err := e.emitExpr(cbExpr)
	e.emittingHTTPHandler = false
	e.httpHandlerNode = nil
	if err != nil {
		return err
	}
	np := 0
	if handlerVal.Ty.IsFunc {
		np = len(handlerVal.Ty.FuncParams)
	}
	if !handlerVal.Ty.IsFunc || np < 2 || np > 3 || !handlerVal.Ty.FuncParams[0].IsH2ServerStream {
		return fmt.Errorf("%d:%d: an http2 'stream' listener must be (stream, headers[, flags]) => void", pos.Line, pos.Col)
	}
	e.httpStreamHandlerArity = np
	paramTy := handlerVal.Ty.FuncParams[0]
	retTy := Http2ServerStreamType()

	e.ensureHTTPRuntime()
	e.usedHTTP2 = true
	e.emitHTTP2ServerDecls()

	e.httpStreamMode = true
	dispErr := e.buildHTTPDispatcher(paramTy, retTy, false)
	e.httpStreamMode = false
	if dispErr != nil {
		return dispErr
	}

	e.emitInstr(fmt.Sprintf("store ptr %s, ptr @__kml_listen_handler, align 8", handlerVal.Ref))
	e.emitInstr("store ptr @__kml_http_dispatch, ptr @__kml_listen_dispatch, align 8")
	return nil
}

// emitH2StreamMethod dispatches respond/end/write/on/close on a server-side
// Http2Stream (TDD-00139 Stage 2).
func (e *Emitter) emitH2StreamMethod(objExpr ast.Expression, method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	objVal, err := e.emitExpr(objExpr)
	if err != nil {
		return Value{}, err
	}
	ty := Http2ServerStreamType()
	structIR := ty.StructIR()
	fieldGEP := func(name string) string {
		idx, _, _ := ty.FieldIndex(name)
		g := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", g, structIR, objVal.Ref, idx))
		return g
	}
	appendBody := func(chunkExpr ast.Expression) error {
		cv, err := e.emitExpr(chunkExpr)
		if err != nil {
			return err
		}
		cur := e.freshReg()
		bg := fieldGEP("body")
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", cur, bg))
		joined, err := e.emitStringConcat(Value{Ref: cur, Ty: TypePtr}, e.coerce(cv, TypePtr))
		if err != nil {
			return err
		}
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", joined.Ref, bg))
		return nil
	}
	switch method {
	case "respond":
		if len(args) > 1 {
			return Value{}, fmt.Errorf("%d:%d: stream.respond takes one headers object", pos.Line, pos.Col)
		}
		if len(args) == 0 {
			return Value{Ty: TypeVoid}, nil // respond() → the 200 default
		}
		lit, ok := args[0].(*ast.ObjectLiteral)
		if !ok {
			return Value{}, fmt.Errorf("%d:%d: stream.respond's headers must be an object literal ({ ':status': …, name: value })", pos.Line, pos.Col)
		}
		for _, prop := range lit.Properties {
			vv, err := e.emitExpr(prop.Value)
			if err != nil {
				return Value{}, err
			}
			if prop.Key == ":status" {
				sv := e.coerce(vv, TypeI64)
				e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", sv.Ref, fieldGEP("status")))
				continue
			}
			if strings.HasPrefix(prop.Key, ":") {
				return Value{}, fmt.Errorf("%d:%d: stream.respond supports the ':status' pseudo-header only (got '%s')", pos.Line, pos.Col, prop.Key)
			}
			hmap := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", hmap, fieldGEP("headers")))
			sv := e.coerce(vv, TypePtr)
			vi := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = ptrtoint ptr %s to i64", vi, sv.Ref))
			e.emitInstr(fmt.Sprintf("call void @__kml_map_str_set(ptr %s, ptr %s, i64 %s)", hmap, e.internString(prop.Key), vi))
		}
		return Value{Ty: TypeVoid}, nil
	case "write":
		if len(args) != 1 {
			return Value{}, fmt.Errorf("%d:%d: stream.write takes one chunk", pos.Line, pos.Col)
		}
		if err := appendBody(args[0]); err != nil {
			return Value{}, err
		}
		return Value{Ty: TypeVoid}, nil
	case "end":
		if len(args) > 1 {
			return Value{}, fmt.Errorf("%d:%d: stream.end takes at most one chunk", pos.Line, pos.Col)
		}
		if len(args) == 1 {
			if err := appendBody(args[0]); err != nil {
				return Value{}, err
			}
		}
		return Value{Ty: TypeVoid}, nil
	case "on", "once":
		evt, err := stringLiteralArg(args, 0, "stream.on", pos)
		if err != nil {
			return Value{}, err
		}
		if len(args) != 2 {
			return Value{}, fmt.Errorf("%d:%d: stream.on takes (event, listener)", pos.Line, pos.Col)
		}
		switch evt {
		case "data":
			// V1 synchronous delivery: the whole request body as one chunk,
			// fired at registration when non-empty (the handler runs after the
			// request is fully assembled, so the body is complete here).
			cb, err := e.resolveCallbackWithHints(args[1], []Type{TypePtr})
			if err != nil {
				return Value{}, err
			}
			lenReg := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", lenReg, fieldGEP("reqBodyLen")))
			nonEmpty := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp sgt i64 %s, 0", nonEmpty, lenReg))
			fireL := e.freshLabel("h2s.data")
			doneL := e.freshLabel("h2s.datadone")
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", nonEmpty, fireL, doneL))
			e.emitLabel(fireL)
			bodyReg := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", bodyReg, fieldGEP("reqBody")))
			if _, err := e.emitCBCall(cb, []Value{{Ref: bodyReg, Ty: TypePtr}}); err != nil {
				return Value{}, err
			}
			e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
			e.emitLabel(doneL)
			return Value{Ty: TypeVoid}, nil
		case "end":
			cb, err := e.resolveCallback(args[1])
			if err != nil {
				return Value{}, err
			}
			if _, err := e.emitCBCall(cb, nil); err != nil {
				return Value{}, err
			}
			return Value{Ty: TypeVoid}, nil
		}
		return Value{}, fmt.Errorf("%d:%d: an Http2Stream supports .on('data'|'end') (got '%s')", pos.Line, pos.Col, evt)
	case "close", "setEncoding", "resume", "pause":
		// Accepted no-ops in V1: the response is written when the handler
		// returns; there is no mid-stream RST or flow control to express.
		return Value{Ty: TypeVoid}, nil
	}
	return Value{}, fmt.Errorf("%d:%d: an Http2Stream supports .respond(headers), .write(chunk), .end(chunk?), .on('data'|'end'), .close() (got '%s')", pos.Line, pos.Col, method)
}

// emitHTTPServerMethod dispatches server.listen/close/closeAllConnections/
// address on a variable-bound http.Server handle.
func (e *Emitter) emitHTTPServerMethod(objExpr ast.Expression, method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	objVal, err := e.emitExpr(objExpr)
	if err != nil {
		return Value{}, err
	}
	switch method {
	case "listen":
		if e.httpServerHandlerPending {
			// A server with no request handler is legitimate Node — client-
			// behavior tests listen without ever responding. Synthesize an
			// empty (req, res) handler; it answers 200/empty (this dispatcher
			// always writes a response when the handler returns — a disclosed
			// divergence from Node's hang-until-timeout).
			empty := ast.NewArrowFunction(
				[]ast.Param{
					{Name: "req", Type: &ast.TypeAnnotation{Name: "IncomingMessage", Source: "ts"}},
					{Name: "res", Type: &ast.TypeAnnotation{Name: "ServerResponse", Source: "ts"}},
				},
				nil, nil, ast.NewBlockStatement(nil, pos), pos)
			if err := e.emitHTTPCreateServerCore(empty, pos); err != nil {
				return Value{}, err
			}
			e.httpServerHandlerPending = false
		}
		return e.emitHTTPServerListen(objVal, args, pos)
	case "on", "once":
		evt, err := stringLiteralArg(args, 0, "server.on", pos)
		if err != nil {
			return Value{}, err
		}
		if evt == "listening" {
			// Registered before .listen(): stash in handle slot 1;
			// emitHTTPServerListen fires it right after binding. If the
			// server is already listening (fd >= 0), fire now (ADR-00502).
			if len(args) != 2 {
				return Value{}, fmt.Errorf("%d:%d: server.on takes (event, listener)", pos.Line, pos.Col)
			}
			cb, err := e.resolveCallback(args[1])
			if err != nil {
				return Value{}, err
			}
			if cb.kind != cbClosure {
				return Value{}, fmt.Errorf("%d:%d: a 'listening' listener must be a function literal", pos.Line, pos.Col)
			}
			slot := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 1", slot, objVal.Ref))
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", cb.hdrPtr, slot))
			fd := e.freshReg()
			bound := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", fd, objVal.Ref))
			e.emitInstr(fmt.Sprintf("%s = icmp sge i64 %s, 0", bound, fd))
			nowL := e.freshLabel("httplisten.now")
			afterL := e.freshLabel("httplisten.after")
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", bound, nowL, afterL))
			e.emitLabel(nowL)
			e.emitHTTPFireListeningSlot(objVal.Ref)
			e.emitTerminator(fmt.Sprintf("br label %%%s", afterL))
			e.emitLabel(afterL)
			return Value{Ty: TypeVoid}, nil
		}
		if evt == "upgrade" {
			// Node-faithful HTTP upgrade event (TDD-00158): store the handler
			// in the module global the dispatcher's upgrade block reads. The
			// block itself is emitted only when e.usedHTTPUpgrade — set by the
			// whole-program pre-scan (programUsesHTTPUpgrade). If that pre-scan
			// somehow missed this very call, the handler would be stored but
			// never read; fail loudly rather than silently drop it.
			if !e.usedHTTPUpgrade {
				return Value{}, fmt.Errorf("%d:%d: internal: server.on('upgrade') was not detected by the upgrade pre-scan — please report", pos.Line, pos.Col)
			}
			if len(args) != 2 {
				return Value{}, fmt.Errorf("%d:%d: server.on takes (event, listener)", pos.Line, pos.Col)
			}
			// Contextually type the `(req, socket, head)` params from the
			// faithful Node shapes (HttpRequest / net.Socket / Buffer) so an
			// unannotated arrow resolves correctly (ADR-00632 hint path).
			cb, err := e.resolveCallbackWithHints(args[1], []Type{RequestType(), NetSocketType(), BufferType()})
			if err != nil {
				return Value{}, err
			}
			if cb.kind != cbClosure {
				return Value{}, fmt.Errorf("%d:%d: an 'upgrade' listener must be a function literal", pos.Line, pos.Col)
			}
			e.ensureHTTPRuntime()
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr @__kml_listen_upgrade_handler, align 8", cb.hdrPtr))
			return Value{Ty: TypeVoid}, nil
		}
		if evt != "request" && evt != "stream" {
			return Value{}, fmt.Errorf("%d:%d: an http.Server supports .on('request'|'stream'|'listening'|'upgrade', listener) (got '%s')", pos.Line, pos.Col, evt)
		}
		if len(args) != 2 {
			return Value{}, fmt.Errorf("%d:%d: server.on takes (event, listener)", pos.Line, pos.Col)
		}
		if !e.httpServerHandlerPending {
			return Value{}, fmt.Errorf("%d:%d: this http.Server already has a request handler (one listener per server, V1)", pos.Line, pos.Col)
		}
		if evt == "stream" {
			// TDD-00139 Stage 2: the http2 core-streams API.
			if err := e.emitHTTPStreamHandlerCore(args[1], pos); err != nil {
				return Value{}, err
			}
		} else if err := e.emitHTTPCreateServerCore(args[1], pos); err != nil {
			return Value{}, err
		}
		e.httpServerHandlerPending = false
		return Value{Ty: TypeVoid}, nil
	case "close":
		if len(args) > 1 {
			return Value{}, fmt.Errorf("%d:%d: server.close takes (callback?)", pos.Line, pos.Col)
		}
		e.ensureHTTPRuntime()
		e.ensureHTTPClose()
		e.emitInstr("call void @__kml_http_close()")
		if len(args) == 1 {
			cb, err := e.resolveCallback(args[0])
			if err != nil {
				return Value{}, err
			}
			if _, err := e.emitCBCall(cb, nil); err != nil {
				return Value{}, err
			}
		}
		return Value{Ty: TypeVoid}, nil
	case "closeAllConnections":
		if len(args) != 0 {
			return Value{}, fmt.Errorf("%d:%d: server.closeAllConnections takes no arguments", pos.Line, pos.Col)
		}
		e.ensureHTTPRuntime()
		e.ensureHTTPCloseAllConns()
		e.emitInstr("call void @__kml_http_close_all_conns()")
		return Value{Ty: TypeVoid}, nil
	case "address":
		if len(args) != 0 {
			return Value{}, fmt.Errorf("%d:%d: server.address takes no arguments", pos.Line, pos.Col)
		}
		e.ensureNetRuntime()
		fd64 := e.freshReg()
		fd32 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", fd64, objVal.Ref))
		e.emitInstr(fmt.Sprintf("%s = trunc i64 %s to i32", fd32, fd64))
		return e.emitNetAddressObject(fd32), nil
	}
	return Value{}, fmt.Errorf("%d:%d: an http.Server supports .listen(port?, cb?), .close(cb?), .closeAllConnections(), and .address() (got '%s')", pos.Line, pos.Col, method)
}

// emitHTTPServerListen implements server.listen(port?, callback?) on a
// variable-bound handle: binds (port 0 — an ephemeral port — when omitted or
// when the only argument is the callback), fires the callback, and enters the
// event loop. Like the chained form (and http.listen), this call blocks until
// the loop stops (server.close() + connections drained) — Node's non-blocking
// listen-then-continue top-level flow is not modeled.
func (e *Emitter) emitHTTPServerListen(objVal Value, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) > 2 {
		return Value{}, fmt.Errorf("%d:%d: server.listen takes (port?, callback?)", pos.Line, pos.Col)
	}
	var portExpr, cbExpr ast.Expression
	if len(args) >= 1 {
		if e.inferExprType(args[0]).IsFunc {
			cbExpr = args[0]
		} else {
			portExpr = args[0]
		}
	}
	if len(args) == 2 {
		if cbExpr != nil {
			return Value{}, fmt.Errorf("%d:%d: server.listen takes (port?, callback?)", pos.Line, pos.Col)
		}
		cbExpr = args[1]
	}

	port := "0"
	if portExpr != nil {
		portVal, err := e.emitExpr(portExpr)
		if err != nil {
			return Value{}, err
		}
		port = e.coerce(portVal, TypeI64).Ref
	}

	e.ensureHTTPRuntime()
	port32 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = trunc i64 %s to i32", port32, port))
	listenfd := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_http_bind_and_listen(i32 %s)", listenfd, port32))
	e.emitInstr("call void @__kml_http_cluster_fork(i64 0)")
	e.emitInstr(fmt.Sprintf("store i32 %s, ptr @__kml_listen_fd, align 4", listenfd))
	fd64 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sext i32 %s to i64", fd64, listenfd))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", fd64, objVal.Ref))
	e.emitHTTPFireListeningSlot(objVal.Ref)

	if cbExpr != nil {
		cb, err := e.resolveCallback(cbExpr)
		if err != nil {
			return Value{}, err
		}
		if _, err := e.emitCBCall(cb, nil); err != nil {
			return Value{}, err
		}
	}

	// Non-blocking (TDD-00131 / ADR-00514): the listener is registered above and
	// the ready callback has already fired synchronously; the event loop is NOT
	// run here. The Pass 3 tail (emitter.go) drives it after the top-level
	// script, so code following `server.listen(...)` runs first — Node's
	// listen-then-continue flow. (`.address().port` works because the fd was
	// bound and stored above, before this returns.)
	e.usedHTTPListen = true
	// Node's listen() returns the server, enabling the chained-binding idiom
	// `const server = http.createServer(cb).listen(0, cb2)`.
	return objVal, nil
}

// emitServerResponseMethod translates a `res.writeHead/setHeader/write/end`
// call (TDD-00131) into a mutation of the ServerResponseType `res` object's
// status/body/headers fields, which the dispatcher reads after the handler
// returns. Returns void.
func (e *Emitter) emitServerResponseMethod(resExpr ast.Expression, method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	resVal, err := e.emitExpr(resExpr)
	if err != nil {
		return Value{}, err
	}
	ty := ServerResponseType()
	structIR := ty.StructIR()
	fieldGEP := func(field string) string {
		idx, _, _ := ty.FieldIndex(field)
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, structIR, resVal.Ref, idx))
		return gep
	}
	switch method {
	case "writeHead":
		if len(args) < 1 {
			return Value{}, fmt.Errorf("%d:%d: res.writeHead needs a status code", pos.Line, pos.Col)
		}
		sv, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		sv = e.coerce(sv, TypeI64)
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", sv.Ref, fieldGEP("status")))
		if len(args) >= 2 {
			if err := e.emitResSetHeadersFromObject(fieldGEP("headers"), args[1], pos); err != nil {
				return Value{}, err
			}
		}
		return Value{Ty: TypeVoid}, nil
	case "setHeader":
		if len(args) != 2 {
			return Value{}, fmt.Errorf("%d:%d: res.setHeader takes (name, value)", pos.Line, pos.Col)
		}
		kv, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		vv, err := e.emitExpr(args[1])
		if err != nil {
			return Value{}, err
		}
		hmap := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", hmap, fieldGEP("headers")))
		vi := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = ptrtoint ptr %s to i64", vi, vv.Ref))
		e.emitInstr(fmt.Sprintf("call void @__kml_map_str_set(ptr %s, ptr %s, i64 %s)", hmap, kv.Ref, vi))
		return Value{Ty: TypeVoid}, nil
	case "write", "end":
		// Append the chunk (if any) to the accumulated body.
		if len(args) >= 1 {
			chunk, err := e.emitExpr(args[0])
			if err != nil {
				return Value{}, err
			}
			bgep := fieldGEP("body")
			cur := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", cur, bgep))
			joined, err := e.emitStringConcat(Value{Ref: cur, Ty: TypePtr}, chunk)
			if err != nil {
				return Value{}, err
			}
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", joined.Ref, bgep))
		}
		// Node's res.write returns a boolean backpressure signal: false when the
		// kernel buffer is full and the caller should await 'drain'. This
		// response sink is buffered (flushed after the handler returns), so it
		// never applies backpressure — write always reports true, matching a sink
		// that accepted the chunk. res.end returns void here (Node returns the
		// stream; the chaining return value is rarely used).
		if method == "write" {
			return Value{Ref: "true", Ty: TypeBool}, nil
		}
		return Value{Ty: TypeVoid}, nil
	case "cork", "uncork":
		// Writable hints to batch writes. For a buffered sink they are valid
		// no-ops (nothing is written incrementally to coalesce). Accepted so
		// portable Node code using them compiles and runs unchanged.
		if len(args) != 0 {
			return Value{}, fmt.Errorf("%d:%d: res.%s takes no arguments", pos.Line, pos.Col, method)
		}
		return Value{Ty: TypeVoid}, nil
	}
	return Value{}, fmt.Errorf("%d:%d: res.%s is not yet supported on a ServerResponse (V1: writeHead/setHeader/write/end)", pos.Line, pos.Col, method)
}

// emitResSetHeadersFromObject sets each field of an object-literal headers map
// (`res.writeHead(200, { 'Content-Type': 'text/plain' })`) into res.headers.
func (e *Emitter) emitResSetHeadersFromObject(headersGEP string, obj ast.Expression, pos ast.Pos) error {
	lit, ok := obj.(*ast.ObjectLiteral)
	if !ok {
		return fmt.Errorf("%d:%d: res.writeHead's headers argument must be an object literal (V1)", pos.Line, pos.Col)
	}
	hmap := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", hmap, headersGEP))
	for _, prop := range lit.Properties {
		if prop.Key == "" {
			return fmt.Errorf("%d:%d: a spread in res.writeHead's headers is not supported (V1)", pos.Line, pos.Col)
		}
		vv, err := e.emitExpr(prop.Value)
		if err != nil {
			return err
		}
		kPtr := e.internString(prop.Key)
		vi := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = ptrtoint ptr %s to i64", vi, vv.Ref))
		e.emitInstr(fmt.Sprintf("call void @__kml_map_str_set(ptr %s, ptr %s, i64 %s)", hmap, kPtr, vi))
	}
	return nil
}

// emitClosureCallByPtrVoid invokes a zero-argument void closure value.
func (e *Emitter) emitClosureCallByPtrVoid(cbVal Value) error {
	fpSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr {ptr, ptr}, ptr %s, i32 0, i32 0", fpSlot, cbVal.Ref))
	fp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", fp, fpSlot))
	epSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr {ptr, ptr}, ptr %s, i32 0, i32 1", epSlot, cbVal.Ref))
	ep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", ep, epSlot))
	e.emitInstr(fmt.Sprintf("call void (ptr) %s(ptr %s)", fp, ep))
	return nil
}

// emitRequestBodyBytes implements req.bodyBytes(): ArrayBuffer (TDD-00026/
// ADR-00106): the binary-safe counterpart to req.body: string, returning the
// exact byte range buildHTTPDispatcher's Content-Length-aware read loop
// already accumulated (RequestType's own hidden bodyLength field) — a
// request body containing an embedded null byte reaches TS code whole
// through this accessor, unlike req.body itself, whose ordinary string
// operations (`.length` included) revert to strlen semantics past that
// point. Hand-builds an ArrayBuffer header exactly like
// emitResponseArrayBuffer (emit_fetch.go, ADR-00094) does — wraps the
// request's own already-buffered body pointer directly instead of
// malloc'ing a fresh one, no copy needed.
// emitRequestBodyDrain emits the Stage 5b drain call: under headers-complete
// dispatch, complete the pre-allocated body buffer in place before any
// buffered accessor reads it. A no-op stub resolves the call when the
// streaming runtime was never emitted, and the runtime itself no-ops on a
// null context — so buffered-mode programs are unaffected.
func (e *Emitter) emitRequestBodyDrain(objVal Value) {
	ctxIdx, _, ok := objVal.Ty.FieldIndex("__kml_bodyctx")
	if !ok {
		return
	}
	e.usedReqBodyDrain = true
	ctxVal := e.loadFieldValue(objVal, ctxIdx, TypePtr)
	e.emitInstr(fmt.Sprintf("call void @__kml_reqbody_drain(ptr %s)", ctxVal.Ref))
}

// emitRequestStream implements req.stream(): ReadableStream<Uint8Array>
// (TDD-00097 Stage 5b) — activate the request's streaming body.
func (e *Emitter) emitRequestStream(objVal Value, pos ast.Pos) (Value, error) {
	e.usedReqBodyStream = true
	e.ensureReqBodyRuntime()
	ctxIdx, _, ok := objVal.Ty.FieldIndex("__kml_bodyctx")
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: not a HttpRequest", pos.Line, pos.Col)
	}
	ctxVal := e.loadFieldValue(objVal, ctxIdx, TypePtr)
	chunkTy := TypedArrayType("uint8")
	fulfill := e.emitStreamFulfillThunk(chunkTy)
	s := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_rs_alloc(double 1.0, ptr %s)", s, fulfill))
	e.storeStreamField(s, 9, e.buildBuiltinClosure("@__kml_reqbody_pull", ctxVal.Ref))
	started := e.buildBuiltinClosure("@__kml_rs_started", s)
	e.emitInstr(fmt.Sprintf("call void @__kml_microtask_enqueue(ptr %s)", started))
	actual := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_reqbody_stream(ptr %s, ptr %s)", actual, ctxVal.Ref, s))
	return Value{Ref: actual, Ty: ReadableStreamType(chunkTy)}, nil
}

func (e *Emitter) emitRequestBodyBytes(objVal Value, pos ast.Pos) (Value, error) {
	e.emitRequestBodyDrain(objVal)
	bodyIdx, bodyFieldTy, ok := objVal.Ty.FieldIndex("body")
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: not a HttpRequest", pos.Line, pos.Col)
	}
	bodyVal := e.loadFieldValue(objVal, bodyIdx, bodyFieldTy)

	lenIdx, lenFieldTy, ok := objVal.Ty.FieldIndex("bodyLength")
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: not a HttpRequest", pos.Line, pos.Col)
	}
	lenVal := e.loadFieldValue(objVal, lenIdx, lenFieldTy)

	e.ensureMalloc()
	hdrReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", hdrReg))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", lenVal.Ref, hdrReg))
	dataSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { i64, ptr }, ptr %s, i32 0, i32 1", dataSlot, hdrReg))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", bodyVal.Ref, dataSlot))

	return Value{Ref: hdrReg, Ty: ArrayBufferType()}, nil
}

// emitHTTPClose implements http.close() (TDD-00027): a bare global function,
// not a method on a Server handle — see this file's own doc comment for why.
// Reachable only from code already running inside the event loop (a request
// handler, a setTimeout/setInterval callback, or a process.on(...) signal
// handler), so — unlike SIGINT/SIGTERM (runtime_process.go) — this needs no
// volatile-pending-flag indirection: it's always safe to mutate
// @__kml_listen_fd immediately, right here. Calls ensureHTTPRuntime() itself
// (idempotent, same defensive-call pattern emitClusterIsPrimary uses for
// ensureHTTPClusterFork) so @__kml_listen_fd exists even in the unusual but
// valid case http.close() is registered (e.g. inside a process.on('SIGINT',
// ...) handler) textually before http.listen() itself.
func (e *Emitter) emitHTTPClose(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 0 {
		return Value{}, fmt.Errorf("%d:%d: http.close takes no arguments", pos.Line, pos.Col)
	}
	e.ensureHTTPRuntime()
	e.ensureHTTPClose()
	e.emitInstr("call void @__kml_http_close()")
	return Value{Ty: TypeVoid}, nil
}

// emitHTTPCloseAllConnections implements http.closeAllConnections() (TDD-00118):
// the forceful counterpart to http.close() — it terminates in-flight connections
// (via shutdown(2), letting each fiber unwind through its own EOF finish path)
// rather than letting them drain. Like http.close() it's a bare global reachable
// only from inside the running event loop, and it propagates across a
// { workers: N } cluster. ensureHTTPRuntime() (idempotent) guarantees the
// runtime and the shared cluster flag exist even if this is registered textually
// before http.listen (e.g. inside a process.on('SIGINT', ...) handler).
func (e *Emitter) emitHTTPCloseAllConnections(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 0 {
		return Value{}, fmt.Errorf("%d:%d: http.closeAllConnections takes no arguments", pos.Line, pos.Col)
	}
	e.ensureHTTPRuntime()
	e.ensureHTTPCloseAllConns()
	e.emitInstr("call void @__kml_http_close_all_conns()")
	return Value{Ty: TypeVoid}, nil
}

// emitClusterIsPrimary/emitClusterWorkerID implement the read-only
// cluster.isPrimary/cluster.workerId globals (TDD-00025) — 0 for the
// original process, 1..N-1 for each fork spawned by
// __kml_http_cluster_fork. ensureHTTPClusterFork() is called here too (not
// just from http.listen itself) so @__kml_cluster_worker_id is guaranteed
// declared even in the (unusual but valid) case a program reads cluster.*
// textually before its http.listen call.
func (e *Emitter) emitClusterIsPrimary() (Value, error) {
	e.ensureHTTPClusterFork()
	id := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr @__kml_cluster_worker_id, align 8", id))
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", r, id))
	return Value{Ref: r, Ty: TypeBool}, nil
}

func (e *Emitter) emitClusterWorkerID() (Value, error) {
	e.ensureHTTPClusterFork()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr @__kml_cluster_worker_id, align 8", r))
	return Value{Ref: r, Ty: TypeI64}, nil
}

// maxHTTPRequestBytes bounds how far buildHTTPDispatcher's read buffer will
// grow for a single request (headers + body together) — a safety cap
// against a hostile/malformed Content-Length (or a client that never sends
// the blank-line header terminator) trying to make this fiber grow its
// buffer without bound. Hitting the cap aborts the connection the same way
// any other malformed-request case here does (noReqL: close, no response) —
// not a proper 413 response yet, a documented V1 limitation.
const maxHTTPRequestBytes = 10 * 1024 * 1024

// buildHTTPDispatcher emits @__kml_http_dispatch, a void() top-level function
// that becomes each accepted connection's own fiber entry point (via
// makecontext, in runtime.go's __kml_http_append_conn) — not a single
// shared dispatcher called once per event-loop wakeup the way V1 originally
// worked, but a per-connection fiber body that can yield (swapcontext back
// to the scheduler) and be resumed later, exactly where it left off, when
// its socket has no data yet. Finds "which connection is this" via
// @__kml_current_conn_idx (set by the scheduler immediately before
// resuming a fiber — safe since fibers are cooperative, never preempted).
//
// Only the read path is fiber-aware (non-blocking read + yield-on-EAGAIN):
// write() is kept as a single blocking call in __kml_http_send_response,
// a deliberate V1 simplification — local socket writes essentially never
// block for responses this small, so making them fiber-aware too would add
// real complexity for a case that doesn't come up in practice at this scope.
//
// The read buffer accumulates across any number of individual read()
// calls (growing via realloc as needed, up to maxHTTPRequestBytes) rather
// than assuming one read() call returns an entire request — necessary once
// a request's headers+body can legitimately exceed one read()'s worth of
// kernel buffer (ADR-00072). The buffer pointer/capacity/bytes-read-so-far,
// and header-parsing state, all live as plain allocas in this function's
// entry block: since this whole function is the fiber's own permanent
// stack frame (only ever entered once, via makecontext, not re-entered per
// event-loop tick), those allocas persist naturally across any number of
// internal swapcontext suspends — exactly like the fd/ctx GEPs below
// already do.
//
// paramTy/retTy are captured from the call site — this is why the
// dispatcher is built per call site rather than being one fully generic
// hand-written IR helper like the timer trampoline: reading status/body off
// an arbitrary user-declared return type needs Go-side knowledge of its
// field offsets. A fixed name is safe: only one http.listen call site is
// ever reachable in V1, since the first one never returns (any second call
// in the same program is dead code). isAsyncHandler is true when the
// handler itself is `async` (needed to `await fetch(...)` inside it, the
// main reason to want one) — retTy is already unwrapped from Promise<T> to
// T by the caller (emitHTTPListen) either way. When the program uses klain:ws
// (e.usedKlainWS, TDD-00158) or a Node `'upgrade'` handler (e.usedHTTPUpgrade),
// right after headers are parsed in parseL below a case-insensitive
// Upgrade/Connection header check diverts a matching request into the
// handshake + persistent WS read loop (emit_websocket.go) or the raw upgrade
// socket loop (emit_http_upgrade.go)
// instead of ever reaching the normal single-request/single-response path
// — false means none of that extra branching is emitted at all, so a
// program with no `ws` handler is byte-for-byte what it was before this
// feature existed.
// httpReqInputs carries the register names of a parsed request's parts — the
// inputs to emitHTTPCallHandler. Both the HTTP/1.1 fiber dispatcher and (Stage 3)
// the nghttp2 driver populate these their own way (socket parse vs nghttp2
// callbacks), then share the same handler-invocation core.
type httpReqInputs struct {
	method, path, query, headers, body, bodyLength, bodyctx string
}

// emitHTTPCallHandler builds the HttpRequest record from an already-parsed
// request (whatever produced its parts), invokes the user handler, and returns
// the response object's register (unwrapping a Promise<T> for an async handler).
// Factored out of buildHTTPDispatcher (TDD-00111 Stage 3 groundwork) so the
// nghttp2 server path can reuse the exact same request-build + handler-call
// without the HTTP/1.1 socket I/O — the response is then read/written by each
// caller its own way (1.1 write vs nghttp2 submit). Emits byte-identical IR for
// the 1.1 path (the code moved here unchanged).
func (e *Emitter) emitHTTPCallHandler(paramTy, retTy Type, isAsyncHandler bool, in httpReqInputs) string {
	reqTy := RequestType()
	reqReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", reqReg, reqTy.StructSize()))
	reqStructIR := reqTy.StructIR()
	storeReqField := func(name, ref string) {
		idx, fieldTy, _ := reqTy.FieldIndex(name)
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, reqStructIR, reqReg, idx))
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", fieldTy.IR, ref, gep, fieldTy.Align()))
	}
	storeReqField("method", in.method)
	storeReqField("path", in.path)
	storeReqField("query", in.query)
	storeReqField("headers", in.headers)
	storeReqField("body", in.body)
	storeReqField("bodyLength", in.bodyLength)
	storeReqField("__kml_bodyctx", in.bodyctx)
	reqVal := e.coerce(Value{Ref: reqReg, Ty: reqTy}, paramTy)

	handlerPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr @__kml_listen_handler, align 8", handlerPtr))
	fpSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr {ptr, ptr}, ptr %s, i32 0, i32 0", fpSlot, handlerPtr))
	fp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", fp, fpSlot))
	epSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr {ptr, ptr}, ptr %s, i32 0, i32 1", epSlot, handlerPtr))
	ep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", ep, epSlot))

	// http2 core-streams shape (TDD-00139 Stage 2): the handler is
	// `(stream, headers) => void`. The request's headers map gains the h2
	// pseudo-headers, a fresh Http2ServerStream (whose first three fields
	// mirror ServerResponse, so the same response-writing tail applies) carries
	// the request body for `stream.on('data')`, and the handler mutates the
	// stream via respond/end/write.
	if e.httpStreamMode {
		setHdr := func(key, valRef string) {
			vi := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = ptrtoint ptr %s to i64", vi, valRef))
			e.emitInstr(fmt.Sprintf("call void @__kml_map_str_set(ptr %s, ptr %s, i64 %s)", in.headers, e.internString(key), vi))
		}
		setHdr(":method", in.method)
		setHdr(":path", in.path)
		setHdr(":scheme", e.internString("http"))
		stream := e.emitNewH2ServerStream(in.body, in.bodyLength)
		if e.httpStreamHandlerArity == 3 {
			e.emitInstr(fmt.Sprintf("call void (ptr, ptr, ptr, double) %s(ptr %s, ptr %s, ptr %s, double 0.0)",
				fp, ep, stream, in.headers))
		} else {
			e.emitInstr(fmt.Sprintf("call void (ptr, ptr, ptr) %s(ptr %s, ptr %s, ptr %s)",
				fp, ep, stream, in.headers))
		}
		return stream
	}

	// Node's http.createServer `(req, res) => void` shape (TDD-00131): build a
	// fresh `res` (ServerResponseType) initialized to status 200 / empty body /
	// empty headers, call handler(req, res), and hand `res` back as the response
	// object — its {status, body, headers} fields are exactly what the response
	// -writing tail below reads. res.writeHead/end/etc. have mutated them.
	if e.httpResMode {
		res := e.emitNewServerResponse()
		e.emitInstr(fmt.Sprintf("call void (ptr, %s, ptr) %s(ptr %s, %s %s, ptr %s)",
			paramTy.IR, fp, ep, paramTy.IR, reqVal.Ref, res))
		return res
	}

	callReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call %s (ptr, %s) %s(ptr %s, %s %s)",
		callReg, retTy.LLVMRetType(), paramTy.IR, fp, ep, paramTy.IR, reqVal.Ref))

	// An async handler's call above doesn't return until its body has fully
	// run (any internal `await` yields this same connection fiber via
	// swapcontext, transparent to this call) — callReg is then a Promise<T>
	// slot pointer, not T directly, needing one more unwrap (matching
	// emitAwait's own generic, non-fetch unwrap: load then free the slot).
	// Promise<T> and a plain object T share IR="ptr", so the call syntax
	// above is identical either way — only this extra indirection differs.
	respReg := callReg
	if isAsyncHandler {
		respReg = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", respReg, callReg))
		e.ensureFree()
		e.emitInstr(fmt.Sprintf("call void @free(ptr %s)", callReg))
	}
	return respReg
}

func (e *Emitter) buildHTTPDispatcher(paramTy, retTy Type, isAsyncHandler bool) error {
	savedAllocas := e.allocas
	savedBody := e.body
	savedRegCtr := e.regCtr
	savedLabelCtr := e.labelCtr
	savedScopes := e.scopes
	savedRetType := e.currentRetType
	savedBlockDone := e.blockDone

	e.allocas = strings.Builder{}
	e.body = strings.Builder{}
	e.regCtr = 0
	e.labelCtr = 0
	e.scopes = nil
	e.blockDone = false
	e.currentRetType = TypeVoid
	e.pushScope()

	e.ensureMalloc()
	e.ensureRealloc()
	e.ensureMemcpy()
	e.ensureStrstr()
	e.ensureAtoll()
	e.ensureMapStrHelpers()
	e.ensureHTTPParseHeaders()
	e.ensureHTTPParseQuery()
	e.ensureSplitFirst()

	bufPtrA := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", bufPtrA))
	bufCapA := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", bufCapA))
	totalReadA := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", totalReadA))
	headersParsedA := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i1, align 1", headersParsedA))
	headerEndA := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", headerEndA))
	contentLenA := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", contentLenA))
	headersMapA := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", headersMapA))

	initBuf := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 8192)", initBuf))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", initBuf, bufPtrA))
	e.emitInstr(fmt.Sprintf("store i64 8192, ptr %s, align 8", bufCapA))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", totalReadA))
	e.emitInstr(fmt.Sprintf("store i1 0, ptr %s, align 1", headersParsedA))
	e.emitInstr(fmt.Sprintf("store i64 -1, ptr %s, align 8", headerEndA))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", contentLenA))
	e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", headersMapA))

	readLoopL := e.freshLabel("http.readloop")
	growL := e.freshLabel("http.grow")
	doGrowL := e.freshLabel("http.dogrow")
	haveCapL := e.freshLabel("http.havecap")
	checkErrL := e.freshLabel("http.checkerr")
	checkEagainL := e.freshLabel("http.checkeagain")
	doYieldL := e.freshLabel("http.doyield")
	accumulateL := e.freshLabel("http.accumulate")
	checkCompleteL := e.freshLabel("http.checkcomplete")
	findHeaderEndL := e.freshLabel("http.findheaderend")
	gotHeaderEndL := e.freshLabel("http.gotheaderend")
	haveCLL := e.freshLabel("http.havecl")
	noCLL := e.freshLabel("http.nocl")
	mergeCLL := e.freshLabel("http.mergecl")
	checkBodyCompleteL := e.freshLabel("http.checkbodycomplete")
	parseL := e.freshLabel("http.parse")
	haveQueryL := e.freshLabel("http.havequery")
	noQueryL := e.freshLabel("http.noquery")
	mergeQueryL := e.freshLabel("http.mergequery")
	noReqL := e.freshLabel("http.noreq")
	e.emitTerminator(fmt.Sprintf("br label %%%s", readLoopL))

	// readLoopL: is there enough spare capacity to read into? Grow first if
	// not, then issue the read() at the current end-of-buffer offset.
	// fd64/fd32 are computed here (not in haveCapL) so they dominate every
	// path that can reach noReqL, including growL's "too big" abort path,
	// which never passes through haveCapL at all.
	e.emitLabel(readLoopL)
	// selfIdx/connData/selfSlot/fdPtr/ctxPtrSlot are recomputed here, at the
	// top of the read loop, rather than once at fiber-entry — this label is
	// re-entered both on first entry and on every resume-after-yield
	// (doYieldL's own "br label %readLoopL" below), and @__kml_conn_data's
	// backing buffer can be realloc()'d (moved) by __kml_http_append_conn
	// while this fiber is suspended, if enough new connections are accepted
	// concurrently to grow the array past its current capacity. A pointer
	// derived from a pre-suspend snapshot of that buffer would dangle after
	// such a move — a real, confirmed use-after-free (found chasing an
	// intermittent request-never-answered hang under concurrent load from a
	// persistent-connection HTTP client; see docs/adr for the ADR fixing
	// this). Recomputing here instead means every use of fdPtr/ctxPtrSlot
	// between here and the next swapcontext (checkErrL/checkEagainL/
	// doYieldL, and — since no further yield happens once the request is
	// fully buffered — all the way through parseL/noReqL's own final
	// `store i64 -1, ptr fdPtr`) is always derived from the current buffer.
	selfIdx := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr @__kml_current_conn_idx, align 8", selfIdx))
	connData := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr @__kml_conn_data, align 8", connData))
	selfSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { i64, ptr, ptr, ptr, ptr }, ptr %s, i64 %s", selfSlot, connData, selfIdx))
	fdPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { i64, ptr, ptr, ptr, ptr }, ptr %s, i32 0, i32 0", fdPtr, selfSlot))
	ctxPtrSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { i64, ptr, ptr, ptr, ptr }, ptr %s, i32 0, i32 1", ctxPtrSlot, selfSlot))

	fd64 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", fd64, fdPtr))
	fd32 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = trunc i64 %s to i32", fd32, fd64))
	capNow0 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", capNow0, bufCapA))
	trNow0 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", trNow0, totalReadA))
	usedPlus1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", usedPlus1, trNow0))
	remain := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", remain, capNow0, usedPlus1))
	needGrow := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 4096", needGrow, remain))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", needGrow, growL, haveCapL))

	// growL: double the buffer, aborting instead (closing the connection,
	// same as any other malformed-request case here) if that would exceed
	// maxHTTPRequestBytes — see its doc comment.
	e.emitLabel(growL)
	curCap := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", curCap, bufCapA))
	newCap := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, 2", newCap, curCap))
	tooBig := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp sgt i64 %s, %d", tooBig, newCap, maxHTTPRequestBytes))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", tooBig, noReqL, doGrowL))

	e.emitLabel(doGrowL)
	curBuf := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", curBuf, bufPtrA))
	newBuf := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @realloc(ptr %s, i64 %s)", newBuf, curBuf, newCap))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", newBuf, bufPtrA))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", newCap, bufCapA))
	e.emitTerminator(fmt.Sprintf("br label %%%s", haveCapL))

	e.emitLabel(haveCapL)
	bufForRead := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", bufForRead, bufPtrA))
	trForRead := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", trForRead, totalReadA))
	capForRead := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", capForRead, bufCapA))
	readPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", readPtr, bufForRead, trForRead))
	readCapMinus1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", readCapMinus1, capForRead, trForRead))
	readCap := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, 1", readCap, readCapMinus1))
	nReg := e.freshReg()
	// When the HTTPS/1.1 path is in use this fd may be a TLS connection —
	// __kml_http_conn_recv routes it through SSL_read (EAGAIN on WANT, so the
	// yield path below is unchanged); a plain connection falls through to raw
	// read(). A program with no HTTPS server keeps the bare read() unchanged.
	readFn := fmt.Sprintf("@read(i32 %s, ptr %s, i64 %s)", fd32, readPtr, readCap)
	if e.usedHTTPS1Server {
		e.emitHTTPSConnShims()
		readFn = fmt.Sprintf("@__kml_http_conn_recv(i32 %s, ptr %s, i64 %s)", fd32, readPtr, readCap)
	}
	e.emitInstr(fmt.Sprintf("%s = call i64 %s", nReg, readFn))
	gotData := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp sgt i64 %s, 0", gotData, nReg))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", gotData, accumulateL, checkErrL))

	// checkErrL/checkEagainL/doYieldL: unchanged from before this feature —
	// they only ever reference nReg/fdPtr/ctxPtrSlot, never buf itself, so
	// doYieldL's own "br label %readLoopL" is already the right resume
	// point now that readLoopL itself is offset-aware.
	e.emitLabel(checkErrL)
	isZero := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", isZero, nReg))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isZero, noReqL, checkEagainL))

	e.emitLabel(checkEagainL)
	e.ensureErrnoAccessor()
	errnoPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @%s()", errnoPtr, errnoAccessor()))
	errnoVal := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i32, ptr %s, align 4", errnoVal, errnoPtr))
	isEagain := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, %d", isEagain, errnoVal, httpEagainErrno()))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isEagain, doYieldL, noReqL))

	e.emitLabel(doYieldL)
	ctxPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", ctxPtr, ctxPtrSlot))
	// gc mode: GC_stackbottom is restored on the *resumer's* side (see the
	// gcRestoreStackbottom/gcRestoreRStackbottom comments in
	// runtime_http.go's __kml_http_append_conn/__kml_event_loop_run), right
	// after this swapcontext call returns there — not here, since a fiber
	// that runs to completion instead of yielding again has no swapcontext
	// call of its own to hang a restore off of, so the resumer has to
	// handle it unconditionally either way.
	e.emitInstr(fmt.Sprintf("call i32 @swapcontext(ptr %s, ptr @__kml_main_ctx)", ctxPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", readLoopL))

	// accumulateL: nReg new bytes arrived — extend totalRead and keep the
	// buffer NUL-terminated at the new end, then check completeness.
	e.emitLabel(accumulateL)
	trOld := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", trOld, totalReadA))
	trNew := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, %s", trNew, trOld, nReg))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", trNew, totalReadA))
	bufForTerm := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", bufForTerm, bufPtrA))
	termPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", termPtr, bufForTerm, trNew))
	e.emitInstr(fmt.Sprintf("store i8 0, ptr %s, align 1", termPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", checkCompleteL))

	// checkCompleteL: have we already found the header terminator? If not,
	// look for it now; if so, skip straight to the body-length check.
	e.emitLabel(checkCompleteL)
	hp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i1, ptr %s, align 1", hp, headersParsedA))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", hp, checkBodyCompleteL, findHeaderEndL))

	e.emitLabel(findHeaderEndL)
	bufForFind := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", bufForFind, bufPtrA))
	// HTTP/2 (h2c, TDD-00111 Stage 3): divert an h2-preface connection to the
	// nghttp2 driver BEFORE the 1.1 header scan below (which would crash on the
	// preface's binary frames). Falls through to 1.1 when it isn't h2.
	if e.usedHTTP2 {
		e.emitHTTP2PrefaceDivert(fd32, fdPtr, bufForFind, totalReadA)
	}
	blankLine := e.internString("\r\n\r\n")
	foundBlank := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @strstr(ptr %s, ptr %s)", foundBlank, bufForFind, blankLine))
	blankNotFound := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", blankNotFound, foundBlank))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", blankNotFound, readLoopL, gotHeaderEndL))

	// gotHeaderEndL (reached exactly once per request): record where the
	// header block ends, parse every header line (skipping the request
	// line itself — found via the first, always-present "\r\n"), and pull
	// out Content-Length if present.
	e.emitLabel(gotHeaderEndL)
	foundInt := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = ptrtoint ptr %s to i64", foundInt, foundBlank))
	bufInt := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = ptrtoint ptr %s to i64", bufInt, bufForFind))
	headerEndVal := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", headerEndVal, foundInt, bufInt))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", headerEndVal, headerEndA))
	e.emitInstr(fmt.Sprintf("store i8 0, ptr %s, align 1", foundBlank))
	crlf := e.internString("\r\n")
	reqLineEnd := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @strstr(ptr %s, ptr %s)", reqLineEnd, bufForFind, crlf))
	headerBlockStart := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 2", headerBlockStart, reqLineEnd))
	newMap := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_map_str_create()", newMap))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", newMap, headersMapA))
	e.emitInstr(fmt.Sprintf("call void @__kml_http_parse_headers(ptr %s, ptr %s)", headerBlockStart, newMap))
	clKey := e.internString("content-length")
	hasCL := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_map_str_has(ptr %s, ptr %s)", hasCL, newMap, clKey))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", hasCL, haveCLL, noCLL))

	e.emitLabel(haveCLL)
	clInt := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_map_str_get(ptr %s, ptr %s)", clInt, newMap, clKey))
	clStr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", clStr, clInt))
	clParsed := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @atoll(ptr %s)", clParsed, clStr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeCLL))

	e.emitLabel(noCLL)
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeCLL))

	e.emitLabel(mergeCLL)
	clFinal := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = phi i64 [ %s, %%%s ], [ 0, %%%s ]", clFinal, clParsed, haveCLL, noCLL))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", clFinal, contentLenA))
	e.emitInstr(fmt.Sprintf("store i1 1, ptr %s, align 1", headersParsedA))
	e.emitTerminator(fmt.Sprintf("br label %%%s", checkBodyCompleteL))

	// checkBodyCompleteL: do we have Content-Length bytes past the header
	// block yet? If not, go read more; if so, the request is fully
	// buffered and parsing can proceed.
	e.emitLabel(checkBodyCompleteL)
	headerEndNow := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", headerEndNow, headerEndA))
	contentLenNow := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", contentLenNow, contentLenA))
	trNow1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", trNow1, totalReadA))
	bodyStart0 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 4", bodyStart0, headerEndNow))
	haveBytes := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", haveBytes, trNow1, bodyStart0))
	bodyComplete := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp sge i64 %s, %s", bodyComplete, haveBytes, contentLenNow))
	if e.usedReqBodyStream {
		// Streaming request bodies (TDD-00097 Stage 5b): dispatch as soon as
		// the headers are parsed — the remaining body flows through the
		// request's body context instead of this buffer.
		e.emitTerminator(fmt.Sprintf("br label %%%s", parseL))
	} else {
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", bodyComplete, parseL, readLoopL))
	}

	// parseL: the request is now fully buffered — parse the request line,
	// split the query string out of the path, load the already-parsed
	// headers map, extract the body, and dispatch to the handler.
	e.emitLabel(parseL)
	bufFinal := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", bufFinal, bufPtrA))

	e.ensureSscanf()
	methodPtr := e.emitStringScratch(16) // TDD-00120: req.method is length-prefixed
	pathPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 2048)", pathPtr)) // intermediate; split_first copies out
	scanFmt := e.internString("%15s %2047s")
	e.emitInstr(fmt.Sprintf("call i32 (ptr, ptr, ...) @sscanf(ptr %s, ptr %s, ptr %s, ptr %s)", bufFinal, scanFmt, methodPtr, pathPtr))
	e.emitStringFinalizeLen(methodPtr)

	qMark := e.internString("?")
	qSplit := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call {ptr, ptr} @__kml_split_first(ptr %s, ptr %s)", qSplit, pathPtr, qMark))
	pathOnly := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, ptr} %s, 0", pathOnly, qSplit))
	queryRaw := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, ptr} %s, 1", queryRaw, qSplit))
	hasQuery := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", hasQuery, queryRaw))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", hasQuery, haveQueryL, noQueryL))

	e.emitLabel(haveQueryL)
	qMap1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_map_str_create()", qMap1))
	e.emitInstr(fmt.Sprintf("call void @__kml_http_parse_query(ptr %s, ptr %s)", queryRaw, qMap1))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeQueryL))

	e.emitLabel(noQueryL)
	qMap2 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_map_str_create()", qMap2))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeQueryL))

	e.emitLabel(mergeQueryL)
	queryMapFinal := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = phi ptr [ %s, %%%s ], [ %s, %%%s ]", queryMapFinal, qMap1, haveQueryL, qMap2, noQueryL))

	headersMapFinal := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", headersMapFinal, headersMapA))

	// WebSocket upgrade handling (TDD-00039 codec; TDD-00158 re-homed under
	// klain:ws). Emitted when the program imports klain:ws (usedKlainWS); a
	// matching `Upgrade: websocket` request diverts into the RFC 6455
	// handshake + persistent frame loop and never returns to normal request
	// handling. Guarded at runtime by the connection-handler global that
	// `new WebSocketServer({server}).on('connection', …)` populates — so a
	// websocket upgrade arriving before any handler was registered falls
	// through to normal handling rather than calling a null closure. wss://
	// falls out: emitWSHandshakeAndLoop routes its I/O through the TLS-aware
	// conn shims when usedHTTPS1Server.
	if e.usedKlainWS {
		wsUpgradeL := e.freshLabel("http.wsupgrade")
		wsNormalL := e.freshLabel("http.wsnormal")
		wsHaveHandlerL := e.freshLabel("http.wshavehandler")
		wsH := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr @__kml_listen_ws_handler, align 8", wsH))
		wsHasH := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", wsHasH, wsH))
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", wsHasH, wsHaveHandlerL, wsNormalL))
		e.emitLabel(wsHaveHandlerL)
		e.emitWSUpgradeDetect(headersMapFinal, wsUpgradeL, wsNormalL)
		e.emitLabel(wsUpgradeL)
		if err := e.emitWSHandshakeAndLoop(headersMapFinal, fd32, fd64, fdPtr, noReqL); err != nil {
			return err
		}
		e.emitLabel(wsNormalL)
	}

	// Node-faithful HTTP upgrade event (TDD-00158): emitted when the program
	// registers a `server.on('upgrade', …)` handler anywhere (pre-scanned into
	// e.usedHTTPUpgrade). Diverts an upgrade request into the (req, socket,
	// head) handler + generic read loop; every other request continues below.
	if e.usedHTTPUpgrade {
		e.emitHTTPUpgradeBlock(headersMapFinal, methodPtr, pathOnly, queryMapFinal, fd32, fd64, bufFinal, headerEndA, totalReadA, noReqL)
	}

	headerEndFinal := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", headerEndFinal, headerEndA))
	contentLenFinal := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", contentLenFinal, contentLenA))
	bodyStartFinal := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 4", bodyStartFinal, headerEndFinal))
	bodySrc := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", bodySrc, bufFinal, bodyStartFinal))
	// TDD-00120: req.body is length-prefixed (its .length now reads the header),
	// binary-safe past an embedded NUL. bodyBytes() uses the same buffer with the
	// exact bodyLength, unaffected.
	bodyBuf := e.emitStringAlloc(contentLenFinal)
	// Copy only what has actually arrived (== Content-Length in buffered
	// mode; possibly less under Stage 5b's headers-complete dispatch).
	trFinal := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", trFinal, totalReadA))
	haveFinal := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", haveFinal, trFinal, bodyStartFinal))
	haveNeg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 0", haveNeg, haveFinal))
	haveClamped := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 0, i64 %s", haveClamped, haveNeg, haveFinal))
	haveOver := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp sgt i64 %s, %s", haveOver, haveClamped, contentLenFinal))
	copiedFinal := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", copiedFinal, haveOver, contentLenFinal, haveClamped))
	e.emitInstr(fmt.Sprintf("call ptr @memcpy(ptr %s, ptr %s, i64 %s)", bodyBuf, bodySrc, copiedFinal))
	bodyTerm := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", bodyTerm, bodyBuf, copiedFinal))
	e.emitInstr(fmt.Sprintf("store i8 0, ptr %s, align 1", bodyTerm))
	bodyCtxRef := "null"
	if e.usedReqBodyStream {
		e.ensureReqBodyRuntime()
		remFinal := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", remFinal, contentLenFinal, copiedFinal))
		ctxReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 48)", ctxReg))
		storeCtx := func(idx int, ir, ref string) {
			gep := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, reqbodyStructIR, ctxReg, idx))
			e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align 8", ir, ref, gep))
		}
		storeCtx(0, "i64", fd64)
		storeCtx(1, "i64", remFinal)
		storeCtx(2, "ptr", bodyBuf)
		storeCtx(3, "i64", copiedFinal)
		storeCtx(4, "ptr", "null")
		storeCtx(5, "i64", "0")
		bodyCtxRef = ctxReg
	}

	respReg := e.emitHTTPCallHandler(paramTy, retTy, isAsyncHandler, httpReqInputs{
		method: methodPtr, path: pathOnly, query: queryMapFinal,
		headers: headersMapFinal, body: bodyBuf, bodyLength: contentLenFinal,
		bodyctx: bodyCtxRef,
	})

	statusIdx, statusTy, _ := retTy.FieldIndex("status")
	statusGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", statusGep, retTy.StructIR(), respReg, statusIdx))
	statusReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", statusReg, statusTy.IR, statusGep, statusTy.Align()))
	statusVal := e.coerce(Value{Ref: statusReg, Ty: statusTy}, TypeI64)

	bodyIdx, bodyTy, _ := retTy.FieldIndex("body")
	bodyGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", bodyGep, retTy.StructIR(), respReg, bodyIdx))
	bodyReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", bodyReg, bodyTy.IR, bodyGep, bodyTy.Align()))

	// Optional response headers: absent (the common case) needs zero extra
	// branches — extraHeadersRef is just the interned empty string, so
	// __kml_http_send_response's output is byte-identical to before this
	// field existed. Present means load it, and (only then) pull in
	// ensureHTTPSerializeHeaders — matching this file's existing "only pull
	// in what's used" discipline.
	var extraHeadersRef string
	if hIdx, hTy, ok := retTy.FieldIndex("headers"); ok {
		hGep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", hGep, retTy.StructIR(), respReg, hIdx))
		hReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", hReg, hTy.IR, hGep, hTy.Align()))
		hNotNull := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", hNotNull, hReg))

		haveRespHeadersL := e.freshLabel("http.haverespheaders")
		noRespHeadersL := e.freshLabel("http.norespheaders")
		mergeRespHeadersL := e.freshLabel("http.mergerespheaders")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", hNotNull, haveRespHeadersL, noRespHeadersL))

		e.emitLabel(haveRespHeadersL)
		e.ensureHTTPSerializeHeaders()
		serialized := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_http_serialize_headers(ptr %s)", serialized, hReg))
		e.emitTerminator(fmt.Sprintf("br label %%%s", mergeRespHeadersL))

		e.emitLabel(noRespHeadersL)
		emptyHdrs := e.internString("")
		e.emitTerminator(fmt.Sprintf("br label %%%s", mergeRespHeadersL))

		e.emitLabel(mergeRespHeadersL)
		extraHeadersReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = phi ptr [ %s, %%%s ], [ %s, %%%s ]", extraHeadersReg, serialized, haveRespHeadersL, emptyHdrs, noRespHeadersL))
		extraHeadersRef = extraHeadersReg
	} else {
		extraHeadersRef = e.internString("")
	}

	// A statically ReadableStream-typed body (TDD-00097 Stage 5) selects the
	// chunked-transfer tail at compile time: send the chunked response head,
	// hand the connection's fd to a reaction-driven %kml.hws writer, and
	// return — the writer reads chunk-at-a-time, writes chunked framing, and
	// closes the fd (and decrements the active-connection count) when the
	// stream ends. A slow producer therefore never blocks other connections.
	streamingBody := bodyTy.IsReadableStream

	// A union body `string | ReadableStream<...>` (TDD-00119): the body field is
	// a runtime { i8, i64 } box. The compile-time buffered-vs-chunked choice
	// becomes a runtime branch on the box's tag (kmlTagStream vs kmlTagString) —
	// handled in its own tail below, skipping the single-shape buffered/bodyBytes
	// length machinery entirely.
	unionBody := bodyTy.IsDynamic && len(bodyTy.UnionMembers) > 0
	var unionStreamMember *Type
	if unionBody {
		for i := range bodyTy.UnionMembers {
			if bodyTy.UnionMembers[i].IsReadableStream {
				unionStreamMember = &bodyTy.UnionMembers[i]
			}
		}
		if unionStreamMember == nil {
			return fmt.Errorf("a union http response body must include a ReadableStream member (e.g. string | ReadableStream<Uint8Array>)")
		}
	}

	// Optional binary response body (TDD-00026/ADR-00106): bodyBytes wins
	// over body's own strlen-computed length whenever it's present at the
	// type level *and* non-null at runtime (a null bodyBytes falls back to
	// body/strlen(body) exactly like before this field existed) — the same
	// "field present at the type level, defensively null-checked at
	// runtime" shape headers' own extraHeadersRef just above already uses.
	// __kml_http_send_response always writes bodyDataRef[0:bodyLenRef] via
	// an explicit-length write(), never a NUL-terminated %s, so a real
	// binary payload (embedded null bytes and all) reaches the socket whole
	// — see runtime_http.go.
	var bodyDataRef, bodyLenRef string
	if bbIdx, bbTy, ok := retTy.FieldIndex("bodyBytes"); streamingBody || unionBody {
		// The buffered body refs are unused on the streaming/union tails below
		// (the union tail computes its own from the unboxed string payload).
		bodyDataRef, bodyLenRef = "null", "0"
	} else if ok {
		bbGep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", bbGep, retTy.StructIR(), respReg, bbIdx))
		bbReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", bbReg, bbTy.IR, bbGep, bbTy.Align()))
		bbNotNull := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", bbNotNull, bbReg))

		haveBBL := e.freshLabel("http.havebodybytes")
		noBBL := e.freshLabel("http.nobodybytes")
		mergeBBL := e.freshLabel("http.mergebodybytes")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", bbNotNull, haveBBL, noBBL))

		e.emitLabel(haveBBL)
		// ArrayBuffer's hidden { i64 byteLength, ptr data } header — see
		// emit_arraybuffer.go's emitNewArrayBufferExpression.
		bbLen := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", bbLen, bbReg))
		bbDataSlot := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr { i64, ptr }, ptr %s, i32 0, i32 1", bbDataSlot, bbReg))
		bbData := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", bbData, bbDataSlot))
		e.emitTerminator(fmt.Sprintf("br label %%%s", mergeBBL))

		e.emitLabel(noBBL)
		strLen := e.emitStrLenHeader(bodyReg) // TDD-00120: binary-safe response body length
		e.emitTerminator(fmt.Sprintf("br label %%%s", mergeBBL))

		e.emitLabel(mergeBBL)
		dataFinal := e.freshReg()
		lenFinal := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = phi ptr [ %s, %%%s ], [ %s, %%%s ]", dataFinal, bbData, haveBBL, bodyReg, noBBL))
		e.emitInstr(fmt.Sprintf("%s = phi i64 [ %s, %%%s ], [ %s, %%%s ]", lenFinal, bbLen, haveBBL, strLen, noBBL))
		bodyDataRef = dataFinal
		bodyLenRef = lenFinal
	} else {
		strLen := e.emitStrLenHeader(bodyReg) // TDD-00120: binary-safe response body length
		bodyDataRef = bodyReg
		bodyLenRef = strLen
	}

	// emitConnActiveDecrement mirrors @__kml_http_append_conn's own increment
	// (runtime_http.go) — TDD-00027's http.close() lets already-accepted
	// connections finish naturally rather than orphaning them, which needs
	// __kml_event_loop_run's scandone to know when the last one is done;
	// factored into a helper since both of this function's connection-finish
	// paths (normal completion below, and the malformed-request abort at
	// noReqL) need the identical three instructions.
	emitConnActiveDecrement := func() {
		activeNow := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr @__kml_conn_active, align 8", activeNow))
		activeNew := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = sub i64 %s, 1", activeNew, activeNow))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr @__kml_conn_active, align 8", activeNew))
	}

	if unionBody {
		// TDD-00119: the body box's tag picks the tail at runtime — kmlTagStream
		// → chunked writer (unbox the stream pointer), otherwise → buffered writer
		// (unbox the string pointer). bodyReg is the { i8, i64 } box.
		tagReg, payloadReg := e.emitUnboxTagPayload(Value{Ref: bodyReg, Ty: TypeAny})
		isStreamReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isStreamReg, tagReg, kmlTagStream))
		streamL := e.freshLabel("http.unionstream")
		bufferL := e.freshLabel("http.unionbuffer")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isStreamReg, streamL, bufferL))

		// Stream branch — same chunked tail as the statically-streaming case.
		e.emitLabel(streamL)
		streamPtr := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", streamPtr, payloadReg))
		chunkTy := TypeI64
		if unionStreamMember.StreamChunk != nil {
			chunkTy = *unionStreamMember.StreamChunk
		}
		isText := "1"
		if chunkTy.IsArray {
			isText = "0"
		} else if chunkTy.IR != "ptr" {
			return fmt.Errorf("a streaming http response body must be ReadableStream<Uint8Array> or ReadableStream<string>")
		}
		e.ensureHTTPStreamRuntime()
		decode := e.emitStreamDecodeThunk(chunkTy)
		e.emitInstr(fmt.Sprintf("call void @__kml_http_send_stream_head(i32 %s, i64 %s, ptr %s)", fd32, statusVal.Ref, extraHeadersRef))
		ufd64 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = sext i32 %s to i64", ufd64, fd32))
		e.emitInstr(fmt.Sprintf("call void @__kml_hws_start(i64 %s, ptr %s, ptr %s, i64 %s)", ufd64, streamPtr, decode, isText))
		e.emitInstr(fmt.Sprintf("store i64 -1, ptr %s, align 8", fdPtr))
		e.emitTerminator("ret void")

		// Buffered branch — unbox the string and write it whole via strlen.
		e.emitLabel(bufferL)
		strPtr := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", strPtr, payloadReg))
		e.ensureStrlen()
		uStrLen := e.emitStrLenHeader(strPtr) // TDD-00120: binary-safe
		e.emitInstr(fmt.Sprintf("call void @__kml_http_send_response(i32 %s, i64 %s, ptr %s, i64 %s, ptr %s)", fd32, statusVal.Ref, strPtr, uStrLen, extraHeadersRef))
		e.emitInstr(fmt.Sprintf("store i64 -1, ptr %s, align 8", fdPtr))
		emitConnActiveDecrement()
		e.emitTerminator("ret void")
	} else if streamingBody {
		// TDD-00097 Stage 5: chunked-transfer tail. Send the chunked head,
		// hand the fd to the reaction-driven %kml.hws writer, and return —
		// the writer closes the fd and decrements the active-connection
		// count when the stream ends, so a slow producer never blocks other
		// connections (and http.close() still waits for it).
		chunkTy := TypeI64
		if bodyTy.StreamChunk != nil {
			chunkTy = *bodyTy.StreamChunk
		}
		isText := "1"
		if chunkTy.IsArray {
			isText = "0"
		} else if chunkTy.IR != "ptr" {
			return fmt.Errorf("a streaming http response body must be ReadableStream<Uint8Array> or ReadableStream<string>")
		}
		e.ensureHTTPStreamRuntime()
		decode := e.emitStreamDecodeThunk(chunkTy)
		e.emitInstr(fmt.Sprintf("call void @__kml_http_send_stream_head(i32 %s, i64 %s, ptr %s)", fd32, statusVal.Ref, extraHeadersRef))
		fd64 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = sext i32 %s to i64", fd64, fd32))
		e.emitInstr(fmt.Sprintf("call void @__kml_hws_start(i64 %s, ptr %s, ptr %s, i64 %s)", fd64, bodyReg, decode, isText))
		e.emitInstr(fmt.Sprintf("store i64 -1, ptr %s, align 8", fdPtr))
		e.emitTerminator("ret void")
	} else {
		e.emitInstr(fmt.Sprintf("call void @__kml_http_send_response(i32 %s, i64 %s, ptr %s, i64 %s, ptr %s)", fd32, statusVal.Ref, bodyDataRef, bodyLenRef, extraHeadersRef))
		e.emitInstr(fmt.Sprintf("store i64 -1, ptr %s, align 8", fdPtr))
		emitConnActiveDecrement()
		e.emitTerminator("ret void")
	}

	e.emitLabel(noReqL)
	if e.usedHTTPS1Server {
		e.emitInstr(fmt.Sprintf("call void @__kml_http_conn_close(i32 %s)", fd32))
	} else {
		e.emitInstr(fmt.Sprintf("call i32 @close(i32 %s)", fd32))
	}
	e.emitInstr(fmt.Sprintf("store i64 -1, ptr %s, align 8", fdPtr))
	emitConnActiveDecrement()
	e.emitTerminator("ret void")

	e.functions.WriteString("\ndefine void @__kml_http_dispatch() {\nentry:\n")
	e.functions.WriteString(e.allocas.String())
	e.functions.WriteString(e.body.String())
	e.functions.WriteString("}\n")

	e.allocas = savedAllocas
	e.body = savedBody
	e.regCtr = savedRegCtr
	e.labelCtr = savedLabelCtr
	e.scopes = savedScopes
	e.currentRetType = savedRetType
	e.blockDone = savedBlockDone

	// HTTP/2 server bridge (TDD-00111 Stage 3): the C nghttp2 driver calls these
	// to run the shared handler core and read the response. Emitted only when the
	// h2 server path is used.
	if e.usedHTTP2 {
		e.buildHTTP2Bridge(paramTy, retTy, isAsyncHandler)
	}
	return nil
}

// emitStandaloneFunc emits a top-level IR function with the given signature and
// a body produced by fn (which emits into e.body/e.allocas and returns the
// terminator, e.g. "ret ptr %x"). Uses the same builder-swap pattern as
// buildHTTPDispatcher so a helper can be assembled mid-compilation.
func (e *Emitter) emitStandaloneFunc(signature string, fn func() string) {
	sa, sb := e.allocas, e.body
	sr, sl, ss := e.regCtr, e.labelCtr, e.scopes
	srt, sbd := e.currentRetType, e.blockDone
	e.allocas, e.body = strings.Builder{}, strings.Builder{}
	e.regCtr, e.labelCtr, e.scopes = 0, 0, nil
	e.blockDone = false
	e.pushScope()
	term := fn()
	e.functions.WriteString("\ndefine " + signature + " {\nentry:\n")
	e.functions.WriteString(e.allocas.String())
	e.functions.WriteString(e.body.String())
	e.functions.WriteString("  " + term + "\n}\n")
	e.allocas, e.body = sa, sb
	e.regCtr, e.labelCtr, e.scopes = sr, sl, ss
	e.currentRetType, e.blockDone = srt, sbd
}

// emitHTTP2PrefaceDivert emits, at the point where a request's bytes are first
// scanned, a check for the HTTP/2 client preface ("PRI * HTTP/2.0\r\n...") — when
// matched, the connection is driven as an nghttp2 session (TDD-00111 Stage 3)
// instead of parsed as HTTP/1.1, and the fiber returns when it's done. Must run
// BEFORE any 1.1 header parsing (the preface's binary frames would crash the 1.1
// header splitter). Control falls through to the caller's 1.1 path when it isn't
// h2. bufReg is the (NUL-terminated) request buffer; totalReadA holds the bytes
// read so far; fd32/fdPtr are the connection's fd and its slot in the conn table.
func (e *Emitter) emitHTTP2PrefaceDivert(fd32, fdPtr, bufReg, totalReadA string) {
	e.ensureMemcmp()
	e.ensureCloseDecl()
	prefacePtr := e.internString("PRI * HTTP/2.0\r\n")
	trReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", trReg, totalReadA))
	bigEnough := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp uge i64 %s, 16", bigEnough, trReg))
	h2checkL := e.freshLabel("http.h2check")
	contL := e.freshLabel("http.h2cont1")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", bigEnough, h2checkL, contL))

	e.emitLabel(h2checkL)
	mc := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @memcmp(ptr %s, ptr %s, i64 16)", mc, bufReg, prefacePtr))
	isH2 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 0", isH2, mc))
	h2driveL := e.freshLabel("http.h2drive")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isH2, h2driveL, contL))

	e.emitLabel(h2driveL)
	e.emitInstr(fmt.Sprintf("call void @__kml_h2_set_blocking(i32 %s)", fd32))
	sess := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_h2_session_server_new(i32 %s, ptr null, ptr null, ptr null)", sess, fd32))
	e.emitInstr(fmt.Sprintf("call void @__kml_h2_session_feed(ptr %s, ptr %s, i64 %s)", sess, bufReg, trReg))
	loopL := e.freshLabel("http.h2loop")
	recvL := e.freshLabel("http.h2recv")
	doneL := e.freshLabel("http.h2done")
	e.emitTerminator(fmt.Sprintf("br label %%%s", loopL))

	e.emitLabel(loopL)
	srx := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_h2_session_send(ptr %s)", srx, sess))
	sbad := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp slt i32 %s, 0", sbad, srx))
	contL2 := e.freshLabel("http.h2cont2")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", sbad, doneL, contL2))

	e.emitLabel(contL2)
	wr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_h2_session_want_read(ptr %s)", wr, sess))
	ww := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_h2_session_want_write(ptr %s)", ww, sess))
	wsum := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = or i32 %s, %s", wsum, wr, ww))
	more := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i32 %s, 0", more, wsum))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", more, recvL, doneL))

	e.emitLabel(recvL)
	rrc := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_h2_session_recv(ptr %s)", rrc, sess))
	rbad := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp slt i32 %s, 0", rbad, rrc))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", rbad, doneL, loopL))

	e.emitLabel(doneL)
	e.emitInstr(fmt.Sprintf("call void @__kml_h2_session_del(ptr %s)", sess))
	e.emitInstr(fmt.Sprintf("%s = call i32 @close(i32 %s)", e.freshReg(), fd32))
	e.emitInstr(fmt.Sprintf("store i64 -1, ptr %s, align 8", fdPtr))
	actNow := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr @__kml_conn_active, align 8", actNow))
	actNew := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, 1", actNew, actNow))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr @__kml_conn_active, align 8", actNew))
	e.emitTerminator("ret void")

	e.emitLabel(contL)
}

// buildHTTP2Bridge emits the C-callable ABI the nghttp2 driver (http2src/http2.c)
// invokes per completed request: __kml_h2_dispatch builds the HttpRequest from
// the nghttp2-parsed parts and runs the handler (reusing emitHTTPCallHandler),
// and three getters read the response object's status/body. Specialized to the
// handler's paramTy/retTy, like the 1.1 dispatcher.
func (e *Emitter) buildHTTP2Bridge(paramTy, retTy Type, isAsyncHandler bool) {
	e.ensureMapStrHelpers()
	e.ensureStrlen()

	// ptr @__kml_h2_dispatch(ptr %method, ptr %path, ptr %headers, ptr %body, i64 %bodyLen)
	e.emitStandaloneFunc("ptr @__kml_h2_dispatch(ptr %method, ptr %path, ptr %headers, ptr %body, i64 %bodyLen)", func() string {
		// h2's :path carries any query string; V1 hands the handler an empty
		// query map (query parsing off the h2 path is a follow-on).
		emptyQuery := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_map_str_create()", emptyQuery))
		// TDD-00120: method/path/body arrive as foreign nghttp2 char* with no
		// length header — copy them into length-prefixed strings (body keeps its
		// exact byte count, so it's binary-safe).
		e.ensureStrHeaderRuntime()
		e.ensureMemcpy()
		mh := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_str_from_cstr(ptr %%method)", mh))
		ph := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_str_from_cstr(ptr %%path)", ph))
		bh := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_str_alloc(i64 %%bodyLen)", bh))
		e.emitInstr(fmt.Sprintf("call ptr @memcpy(ptr %s, ptr %%body, i64 %%bodyLen)", bh))
		bnul := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %%bodyLen", bnul, bh))
		e.emitInstr(fmt.Sprintf("store i8 0, ptr %s, align 1", bnul))
		resp := e.emitHTTPCallHandler(paramTy, retTy, isAsyncHandler, httpReqInputs{
			method: mh, path: ph, query: emptyQuery,
			headers: "%headers", body: bh, bodyLength: "%bodyLen",
			bodyctx: "null",
		})
		return "ret ptr " + resp
	})

	// i64 @__kml_h2_resp_status(ptr %resp)
	statusIdx, statusTy, _ := retTy.FieldIndex("status")
	e.emitStandaloneFunc("i64 @__kml_h2_resp_status(ptr %resp)", func() string {
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %%resp, i32 0, i32 %d", gep, retTy.StructIR(), statusIdx))
		v := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", v, statusTy.IR, gep, statusTy.Align()))
		out := e.coerce(Value{Ref: v, Ty: statusTy}, TypeI64)
		return "ret i64 " + out.Ref
	})

	// ptr @__kml_h2_resp_body(ptr %resp)
	bodyIdx, bodyFieldTy, _ := retTy.FieldIndex("body")
	e.emitStandaloneFunc("ptr @__kml_h2_resp_body(ptr %resp)", func() string {
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %%resp, i32 0, i32 %d", gep, retTy.StructIR(), bodyIdx))
		v := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align %d", v, gep, bodyFieldTy.Align()))
		return "ret ptr " + v
	})

	// i64 @__kml_h2_resp_bodylen(ptr %resp) — strlen of the body string (V1;
	// binary bodyBytes is a follow-on, matching the 1.1 path's own default).
	e.emitStandaloneFunc("i64 @__kml_h2_resp_bodylen(ptr %resp)", func() string {
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %%resp, i32 0, i32 %d", gep, retTy.StructIR(), bodyIdx))
		bp := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align %d", bp, gep, bodyFieldTy.Align()))
		n := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @strlen(ptr %s)", n, bp))
		return "ret i64 " + n
	})

	// Response-header getters (TDD-00139 Stage 2): the C driver appends these
	// to :status when submitting. Indexed straight off the string-map layout
	// ([0] size, [16] keys array, [24] vals array — runtime_collections.go).
	// A response shape with no headers field compiles them to constants.
	hdrIdx, hdrFieldTy, hasHdrs := retTy.FieldIndex("headers")
	loadHdrMap := func() string {
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %%resp, i32 0, i32 %d", gep, retTy.StructIR(), hdrIdx))
		m := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align %d", m, gep, hdrFieldTy.Align()))
		return m
	}
	e.emitStandaloneFunc("i64 @__kml_h2_resp_hdr_count(ptr %resp)", func() string {
		if !hasHdrs {
			return "ret i64 0"
		}
		m := loadHdrMap()
		isnull := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isnull, m))
		zL := e.freshLabel("h2hc.zero")
		nL := e.freshLabel("h2hc.n")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isnull, zL, nL))
		e.emitLabel(zL)
		e.emitTerminator("ret i64 0")
		e.emitLabel(nL)
		n := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", n, m))
		return "ret i64 " + n
	})
	hdrSlot := func(byteOff int) func() string {
		return func() string {
			if !hasHdrs {
				return "ret ptr null"
			}
			m := loadHdrMap()
			ap := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %d", ap, m, byteOff))
			arr := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", arr, ap))
			sp := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %s, i64 %%i", sp, arr))
			raw := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", raw, sp))
			out := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", out, raw))
			return "ret ptr " + out
		}
	}
	e.emitStandaloneFunc("ptr @__kml_h2_resp_hdr_name(ptr %resp, i64 %i)", hdrSlot(16))
	e.emitStandaloneFunc("ptr @__kml_h2_resp_hdr_val(ptr %resp, i64 %i)", hdrSlot(24))
}

// emitH2Connect implements http2.connect(authority[, listener]) — TDD-00139
// Stage 3. h2c (http://) prior-knowledge only; a connect failure or an
// https:// authority throws. The optional listener is Node's 'connect' event
// callback, fired immediately (the TCP connect is blocking).
func (e *Emitter) emitH2Connect(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 1 || len(args) > 2 {
		return Value{}, fmt.Errorf("%d:%d: http2.connect takes (authority, listener?)", pos.Line, pos.Col)
	}
	e.ensureH2ClientRuntime()
	auth, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	auth = e.coerce(auth, TypePtr)
	sess := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_h2c_connect_url(ptr %s)", sess, auth.Ref))
	isnull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isnull, sess))
	okL := e.freshLabel("h2c.ok")
	failL := e.freshLabel("h2c.fail")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isnull, failL, okL))
	e.emitLabel(failL)
	e.emitInternalThrow(e.internString("http2.connect failed — connection refused, or a non-http:// authority (TLS client sessions are not supported; use an http:// h2c authority)"))
	e.emitLabel(okL)
	// Flush at process exit so a client with no event loop still completes.
	e.emitInstr(fmt.Sprintf("%s = call i32 @atexit(ptr @__kml_h2c_flush)", e.freshReg()))
	obj := e.freshReg()
	e.ensureCalloc()
	e.emitInstr(fmt.Sprintf("%s = call ptr @calloc(i64 1, i64 8)", obj))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", sess, obj))
	if len(args) == 2 {
		cb, err := e.resolveCallback(args[1])
		if err != nil {
			return Value{}, err
		}
		if _, err := e.emitCBCall(cb, nil); err != nil {
			return Value{}, err
		}
	}
	return Value{Ref: obj, Ty: Http2ClientSessionType()}, nil
}

// emitH2ClientSessionMethod dispatches request/close/destroy/on on a
// ClientHttp2Session handle.
func (e *Emitter) emitH2ClientSessionMethod(objExpr ast.Expression, method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	objVal, err := e.emitExpr(objExpr)
	if err != nil {
		return Value{}, err
	}
	loadSess := func() string {
		s := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", s, objVal.Ref))
		return s
	}
	switch method {
	case "request":
		if len(args) > 1 {
			return Value{}, fmt.Errorf("%d:%d: session.request takes one headers object", pos.Line, pos.Col)
		}
		methodRef := e.internString("GET")
		pathRef := e.internString("/")
		var extraNames []string
		var extraVals []string
		if len(args) == 1 {
			lit, ok := args[0].(*ast.ObjectLiteral)
			if !ok {
				return Value{}, fmt.Errorf("%d:%d: session.request's headers must be an object literal", pos.Line, pos.Col)
			}
			for _, prop := range lit.Properties {
				vv, err := e.emitExpr(prop.Value)
				if err != nil {
					return Value{}, err
				}
				sv := e.coerce(vv, TypePtr)
				switch prop.Key {
				case ":path":
					pathRef = sv.Ref
				case ":method":
					methodRef = sv.Ref
				case ":scheme", ":authority":
					// fixed by the session (h2c + the connect authority)
				default:
					if strings.HasPrefix(prop.Key, ":") {
						return Value{}, fmt.Errorf("%d:%d: session.request supports the ':path'/':method' pseudo-headers (got '%s')", pos.Line, pos.Col, prop.Key)
					}
					extraNames = append(extraNames, prop.Key)
					extraVals = append(extraVals, sv.Ref)
				}
			}
		}
		namesPtr, valsPtr := "null", "null"
		if n := len(extraNames); n > 0 {
			e.ensureMalloc()
			np := e.freshReg()
			vp := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", np, n*8))
			e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", vp, n*8))
			for i := range extraNames {
				ns := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %d", ns, np, i))
				e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", e.internString(extraNames[i]), ns))
				vs := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %d", vs, vp, i))
				e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", extraVals[i], vs))
			}
			namesPtr, valsPtr = np, vp
		}
		// ctx: {cbResp, cbData, cbEnd, headersMap}
		e.ensureCalloc()
		ctx := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @calloc(i64 1, i64 32)", ctx))
		hmap := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_map_str_create()", hmap))
		hslot := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 24", hslot, ctx))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", hmap, hslot))
		e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_h2c_request(ptr %s, ptr %s, ptr %s, ptr %s, ptr %s, ptr %s, i64 %d)",
			e.freshReg(), loadSess(), ctx, methodRef, pathRef, namesPtr, valsPtr, len(extraNames)))
		// Push the frames out promptly (preface/SETTINGS/HEADERS).
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_h2c_pump_all()", e.freshReg()))
		return Value{Ref: ctx, Ty: Http2ClientStreamType()}, nil
	case "close":
		if len(args) > 1 {
			return Value{}, fmt.Errorf("%d:%d: session.close takes (callback?)", pos.Line, pos.Col)
		}
		e.emitInstr(fmt.Sprintf("call void @__kml_h2c_close(ptr %s)", loadSess()))
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_h2c_pump_all()", e.freshReg()))
		if len(args) == 1 {
			cb, err := e.resolveCallback(args[0])
			if err != nil {
				return Value{}, err
			}
			if _, err := e.emitCBCall(cb, nil); err != nil {
				return Value{}, err
			}
		}
		return Value{Ty: TypeVoid}, nil
	case "destroy":
		e.emitInstr(fmt.Sprintf("call void @__kml_h2c_destroy(ptr %s)", loadSess()))
		return Value{Ty: TypeVoid}, nil
	case "on", "once":
		// 'error'/'close'/'goaway' listeners are accepted no-ops in V1: a
		// connect failure throws instead, and close is synchronous-ish.
		if len(args) != 2 {
			return Value{}, fmt.Errorf("%d:%d: session.on takes (event, listener)", pos.Line, pos.Col)
		}
		return Value{Ty: TypeVoid}, nil
	case "ping", "settings", "setTimeout", "ref", "unref":
		return Value{Ty: TypeVoid}, nil
	}
	return Value{}, fmt.Errorf("%d:%d: a ClientHttp2Session supports .request(headers?), .close(cb?), .destroy(), .on(event, cb) (got '%s')", pos.Line, pos.Col, method)
}

// emitH2ClientStreamMethod dispatches on/end/write/close on a
// ClientHttp2Stream (the request handle).
func (e *Emitter) emitH2ClientStreamMethod(objExpr ast.Expression, method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	objVal, err := e.emitExpr(objExpr)
	if err != nil {
		return Value{}, err
	}
	storeCB := func(offset int, cbExpr ast.Expression, hints []Type) error {
		cb, err := e.resolveCallbackWithHints(cbExpr, hints)
		if err != nil {
			return err
		}
		if cb.kind != cbClosure {
			return fmt.Errorf("%d:%d: a stream listener must be an arrow/function-expression literal", pos.Line, pos.Col)
		}
		slot := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %d", slot, objVal.Ref, offset))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", cb.hdrPtr, slot))
		return nil
	}
	switch method {
	case "on", "once":
		evt, err := stringLiteralArg(args, 0, "stream.on", pos)
		if err != nil {
			return Value{}, err
		}
		if len(args) != 2 {
			return Value{}, fmt.Errorf("%d:%d: stream.on takes (event, listener)", pos.Line, pos.Col)
		}
		switch evt {
		case "response":
			contextTypeArrowParams(args[1], "__kml_h2_headers")
			return Value{Ty: TypeVoid}, storeCB(0, args[1], []Type{MapType(TypePtr, TypePtr)})
		case "data":
			contextTypeArrowParams(args[1], "string")
			return Value{Ty: TypeVoid}, storeCB(8, args[1], []Type{TypePtr})
		case "end":
			return Value{Ty: TypeVoid}, storeCB(16, args[1], nil)
		case "close", "error":
			// accepted no-ops (V1): 'end' is the completion signal
			return Value{Ty: TypeVoid}, nil
		}
		return Value{}, fmt.Errorf("%d:%d: a ClientHttp2Stream supports .on('response'|'data'|'end') (got '%s')", pos.Line, pos.Col, evt)
	case "end":
		if len(args) > 0 {
			return Value{}, fmt.Errorf("%d:%d: request bodies are not supported yet — the request is sent (with END_STREAM) at session.request time; req.end() is a no-op", pos.Line, pos.Col)
		}
		return Value{Ty: TypeVoid}, nil
	case "write":
		return Value{}, fmt.Errorf("%d:%d: request bodies are not supported yet — the request is sent (with END_STREAM) at session.request time", pos.Line, pos.Col)
	case "close", "setEncoding", "resume", "pause", "setTimeout":
		return Value{Ty: TypeVoid}, nil
	}
	return Value{}, fmt.Errorf("%d:%d: a ClientHttp2Stream supports .on('response'|'data'|'end'), .end(), .close() (got '%s')", pos.Line, pos.Col, method)
}

// emitHTTPFireListeningSlot fires (once, then clears) the pending
// 'listening' listener in an http.Server handle's slot 1 (ADR-00502).
func (e *Emitter) emitHTTPFireListeningSlot(srvRef string) {
	slot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 1", slot, srvRef))
	cb := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", cb, slot))
	has := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", has, cb))
	fireL := e.freshLabel("httplfire")
	afterL := e.freshLabel("httplafter")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", has, fireL, afterL))
	e.emitLabel(fireL)
	e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", slot))
	fp := e.freshReg()
	ep := e.freshReg()
	fpp := e.freshReg()
	epp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 0", fpp, cb))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", fp, fpp))
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 1", epp, cb))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", ep, epp))
	e.emitInstr(fmt.Sprintf("call void %s(ptr %s)", fp, ep))
	e.emitTerminator(fmt.Sprintf("br label %%%s", afterL))
	e.emitLabel(afterL)
}
