package llvm

// Node http client — `http.get(url, cb)` / `http.request(...)` (TDD-00138
// Stage 1). Built on the existing libcurl `fetch` primitive (`__kml_fetch_async`
// + `__kml_await_fetch`), exposed through Node's callback/event surface: the
// callback receives an `IncomingMessage` (`res.statusCode`, `res.on('data'|
// 'end')`). V1 delivers the response body as a single 'data' chunk then 'end',
// synchronously right after the callback registers its listeners.

import (
	"fmt"

	"KlainMainLang/ast"
)

// ensureHTTPClientReactions emits the http-client completion-reaction registry
// (TDD-00138 Stage 2): a { pending, closure } list an async `http.get` registers
// into, fired by the event loop after each fetch-message drain when the
// pending's transfer is done. This is what lets `http.get` call the program's
// own in-process `http.listen` server without deadlocking — it registers a
// reaction and returns, and the already-running loop drives both the server and
// the client transfer, then fires the callback. `__kml_httpc_drive` is the
// no-event-loop fallback (busy-pump curl + fire until a specific pending done).
func (e *Emitter) ensureHTTPClientReactions() {
	if e.usedHTTPClientReactions {
		return
	}
	e.usedHTTPClientReactions = true
	e.ensureMalloc()
	e.ensureRealloc()
	e.ensureFetch() // @__kml_curl_multi, curl_multi_perform, __kml_curl_drain_messages
	e.emitGlobal(`
@__kml_httpc_data = internal global ptr null, align 8
@__kml_httpc_len = internal global i64 0, align 8
@__kml_httpc_cap = internal global i64 0, align 8

; register a { ptr pending, ptr closure } pair (16-byte entries).
define void @__kml_httpc_register(ptr %pending, ptr %closure) {
entry:
  %len = load i64, ptr @__kml_httpc_len, align 8
  %cap = load i64, ptr @__kml_httpc_cap, align 8
  %full = icmp sge i64 %len, %cap
  br i1 %full, label %grow, label %store
grow:
  %cap2 = mul i64 %cap, 2
  %ge4 = icmp sgt i64 %cap2, 4
  %newcap = select i1 %ge4, i64 %cap2, i64 4
  %old = load ptr, ptr @__kml_httpc_data, align 8
  %bytes = mul i64 %newcap, 16
  %new = call ptr @realloc(ptr %old, i64 %bytes)
  store ptr %new, ptr @__kml_httpc_data, align 8
  store i64 %newcap, ptr @__kml_httpc_cap, align 8
  br label %store
store:
  %data = load ptr, ptr @__kml_httpc_data, align 8
  %slot = getelementptr { ptr, ptr }, ptr %data, i64 %len
  %pp = getelementptr { ptr, ptr }, ptr %slot, i32 0, i32 0
  store ptr %pending, ptr %pp, align 8
  %cp = getelementptr { ptr, ptr }, ptr %slot, i32 0, i32 1
  store ptr %closure, ptr %cp, align 8
  %newlen = add i64 %len, 1
  store i64 %newlen, ptr @__kml_httpc_len, align 8
  ret void
}

; fire every registered reaction whose pending transfer is done (pending field
; 2 == done flag), then clear its slot (pending=null) so it fires at most once.
define void @__kml_httpc_fire_ready() {
entry:
  %len = load i64, ptr @__kml_httpc_len, align 8
  %data = load ptr, ptr @__kml_httpc_data, align 8
  br label %loop
loop:
  %i = phi i64 [ 0, %entry ], [ %inext, %cont ]
  %done = icmp sge i64 %i, %len
  br i1 %done, label %ret, label %body
body:
  %slot = getelementptr { ptr, ptr }, ptr %data, i64 %i
  %pp = getelementptr { ptr, ptr }, ptr %slot, i32 0, i32 0
  %pending = load ptr, ptr %pp, align 8
  %isnull = icmp eq ptr %pending, null
  br i1 %isnull, label %cont, label %check
check:
  %dp = getelementptr { ptr, ptr, i64, i64, i64 }, ptr %pending, i32 0, i32 2
  %d = load i64, ptr %dp, align 8
  %rdy = icmp ne i64 %d, 0
  br i1 %rdy, label %fire, label %cont
fire:
  %cp = getelementptr { ptr, ptr }, ptr %slot, i32 0, i32 1
  %closure = load ptr, ptr %cp, align 8
  store ptr null, ptr %pp, align 8
  %fpp = getelementptr { ptr, ptr }, ptr %closure, i32 0, i32 0
  %fp = load ptr, ptr %fpp, align 8
  %epp = getelementptr { ptr, ptr }, ptr %closure, i32 0, i32 1
  %ep = load ptr, ptr %epp, align 8
  call void %fp(ptr %ep)
  br label %cont
cont:
  %inext = add i64 %i, 1
  br label %loop
ret:
  ret void
}

; busy-drive: pump curl + drain + fire until the given pending is done. Used
; when http.get runs with no event loop (no http.listen) to service the request.
define void @__kml_httpc_drive(ptr %pending) {
entry:
  br label %loop
loop:
  %dp = getelementptr { ptr, ptr, i64, i64, i64 }, ptr %pending, i32 0, i32 2
  %d = load i64, ptr %dp, align 8
  %isdone = icmp ne i64 %d, 0
  br i1 %isdone, label %fireleft, label %pump
pump:
  %rp = alloca i32, align 4
  %cm = load ptr, ptr @__kml_curl_multi, align 8
  call i32 @curl_multi_perform(ptr %cm, ptr %rp)
  call void @__kml_curl_drain_messages()
  call void @__kml_httpc_fire_ready()
  br label %loop
fireleft:
  ; the pending is done; fire_ready above (or here) delivered its reaction.
  call void @__kml_httpc_fire_ready()
  ret void
}

; flush: pump curl until EVERY registered reaction has fired. Installed into
; @__kml_httpc_flush_hook and invoked after the event loop exits, so a
; server.close() from inside a request handler can't strand a client response
; whose transfer completes as the loop winds down.
define void @__kml_httpc_flush() {
entry:
  br label %scan
scan:
  %len = load i64, ptr @__kml_httpc_len, align 8
  %data = load ptr, ptr @__kml_httpc_data, align 8
  br label %loop
loop:
  %i = phi i64 [ 0, %scan ], [ %inext, %cont ]
  %done = icmp sge i64 %i, %len
  br i1 %done, label %ret, label %body
body:
  %slot = getelementptr { ptr, ptr }, ptr %data, i64 %i
  %pp = getelementptr { ptr, ptr }, ptr %slot, i32 0, i32 0
  %pending = load ptr, ptr %pp, align 8
  %isnull = icmp eq ptr %pending, null
  br i1 %isnull, label %cont, label %haswork
cont:
  %inext = add i64 %i, 1
  br label %loop
haswork:
  %rp = alloca i32, align 4
  %cm = load ptr, ptr @__kml_curl_multi, align 8
  call i32 @curl_multi_perform(ptr %cm, ptr %rp)
  call void @__kml_curl_drain_messages()
  call void @__kml_httpc_fire_ready()
  br label %scan
ret:
  ret void
}`)
	e.ensureHTTPCFlushHook()
}

// ensureHTTPCFlushHook defines the post-event-loop flush hook global exactly
// once. The client runtime stores @__kml_httpc_flush into it at each
// http.get/request site; the listen emitters call through it (null-guarded)
// after @__kml_event_loop_run returns. Split from ensureHTTPClientReactions so
// server-only programs can reference the (null) hook without dragging curl in.
func (e *Emitter) ensureHTTPCFlushHook() {
	if e.usedHTTPCFlushHook {
		return
	}
	e.usedHTTPCFlushHook = true
	e.emitGlobal("@__kml_httpc_flush_hook = internal global ptr null, align 8")
}

// emitPostLoopFlush emits the null-guarded indirect call through the flush
// hook — placed immediately after every @__kml_event_loop_run call site.
func (e *Emitter) emitPostLoopFlush() {
	e.ensureHTTPCFlushHook()
	h := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr @__kml_httpc_flush_hook, align 8", h))
	isnull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isnull, h))
	skipL := e.freshLabel("postloop.skip")
	callL := e.freshLabel("postloop.flush")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isnull, skipL, callL))
	e.emitLabel(callL)
	e.emitInstr(fmt.Sprintf("call void %s()", h))
	e.emitTerminator(fmt.Sprintf("br label %%%s", skipL))
	e.emitLabel(skipL)
}

// emitHTTPClientGet implements http.get(url, cb?) — a GET request whose response
// is handed to cb as an IncomingMessage. http.request(url, cb) reduces here too
// in V1 (method defaults to GET; request-body writes are Stage 2).
func (e *Emitter) emitHTTPClientGet(args []ast.Expression, pos ast.Pos, deferred bool) (Value, error) {
	return e.emitHTTPClientGetScheme(args, pos, "http", deferred)
}

// emitHTTPClientGetScheme is emitHTTPClientGet with the URL scheme the
// options-object form composes with — "https" when dispatched through the
// https module.
func (e *Emitter) emitHTTPClientGetScheme(args []ast.Expression, pos ast.Pos, scheme string, deferred bool) (Value, error) {
	if len(args) < 1 || len(args) > 2 {
		return Value{}, fmt.Errorf("%d:%d: http.get takes (url, callback?)", pos.Line, pos.Col)
	}
	// Options-object form — `http.get({ port, path?, host?/hostname? }, cb)`,
	// the shape Node code uses with server.address().port — composes the URL
	// at runtime: http://<host>:<port><path>. Other option keys (headers,
	// method, agent, …) are rejected rather than silently ignored.
	var urlVal Value
	methodRef, headersRef := "null", "null"
	if lit, ok := args[0].(*ast.ObjectLiteral); ok {
		var portExpr, pathExpr, hostExpr, methodExpr, headersExpr ast.Expression
		for _, prop := range lit.Properties {
			switch prop.Key {
			case "port":
				portExpr = prop.Value
			case "path":
				pathExpr = prop.Value
			case "host", "hostname":
				hostExpr = prop.Value
			case "method":
				methodExpr = prop.Value
			case "headers":
				headersExpr = prop.Value
			case "agent":
				// Accepted, evaluated for effect, and otherwise ignored: this
				// client opens one connection per request — exactly Node's
				// behavior with `agent: false`/a non-keepAlive Agent, so no
				// observable pooling semantics are being silently dropped.
				if _, err := e.emitExpr(prop.Value); err != nil {
					return Value{}, err
				}
			default:
				return Value{}, fmt.Errorf("%d:%d: http.get options support { port, path, host, method, headers, agent } only (got '%s')", pos.Line, pos.Col, prop.Key)
			}
		}
		if portExpr == nil {
			return Value{}, fmt.Errorf("%d:%d: http.get's options object requires a 'port'", pos.Line, pos.Col)
		}
		portVal, err := e.emitExpr(portExpr)
		if err != nil {
			return Value{}, err
		}
		portStr, err := e.emitValueToString(portVal)
		if err != nil {
			return Value{}, err
		}
		host := Value{Ref: e.internString("127.0.0.1"), Ty: TypePtr}
		if hostExpr != nil {
			hv, err := e.emitExpr(hostExpr)
			if err != nil {
				return Value{}, err
			}
			host = e.coerce(hv, TypePtr)
		}
		acc, err := e.emitStringConcat(Value{Ref: e.internString(scheme + "://"), Ty: TypePtr}, host)
		if err != nil {
			return Value{}, err
		}
		if acc, err = e.emitStringConcat(acc, Value{Ref: e.internString(":"), Ty: TypePtr}); err != nil {
			return Value{}, err
		}
		if acc, err = e.emitStringConcat(acc, portStr); err != nil {
			return Value{}, err
		}
		if pathExpr != nil {
			pv, err := e.emitExpr(pathExpr)
			if err != nil {
				return Value{}, err
			}
			if acc, err = e.emitStringConcat(acc, e.coerce(pv, TypePtr)); err != nil {
				return Value{}, err
			}
		} else if acc, err = e.emitStringConcat(acc, Value{Ref: e.internString("/"), Ty: TypePtr}); err != nil {
			return Value{}, err
		}
		urlVal = acc
		if methodExpr != nil {
			mv, err := e.emitExpr(methodExpr)
			if err != nil {
				return Value{}, err
			}
			methodRef = e.coerce(mv, TypePtr).Ref
		}
		if headersExpr != nil {
			hl, ok := headersExpr.(*ast.ObjectLiteral)
			if !ok {
				return Value{}, fmt.Errorf("%d:%d: http.get's headers option must be an object literal", pos.Line, pos.Col)
			}
			// Build a string map from the literal, then the same curl_slist
			// fetch(url, init) uses (ADR-00500).
			e.ensureMapStrHelpers()
			m := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_map_str_create()", m))
			for _, hp := range hl.Properties {
				hv, err := e.emitExpr(hp.Value)
				if err != nil {
					return Value{}, err
				}
				hv = e.coerce(hv, TypePtr)
				vi := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = ptrtoint ptr %s to i64", vi, hv.Ref))
				e.emitInstr(fmt.Sprintf("call void @__kml_map_str_set(ptr %s, ptr %s, i64 %s)", m, e.internString(hp.Key), vi))
			}
			hlist, err := e.buildFetchHeaderList(m)
			if err != nil {
				return Value{}, err
			}
			headersRef = hlist
		}
	} else if objTy := e.inferExprType(args[0]); objTy.IsObject {
		// A *variable-bound* options object (`const options = { port, … };
		// http.get(options, cb)`). Before this branch existed the object
		// pointer fell through as the URL string — a silent-garbage request
		// that hung; read the typed fields instead.
		objVal, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		loadField := func(name string) (Value, bool) {
			idx, fty, ok := objVal.Ty.FieldIndex(name)
			if !ok {
				return Value{}, false
			}
			gep := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, objVal.Ty.StructIR(), objVal.Ref, idx))
			r := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", r, fty.IR, gep, fty.Align()))
			return Value{Ref: r, Ty: fty}, true
		}
		for _, f := range objVal.Ty.Fields {
			switch f.Name {
			case "port", "path", "host", "hostname", "agent", "method":
			default:
				return Value{}, fmt.Errorf("%d:%d: http.get options support { port, path, host, method, headers, agent } only (got '%s')", pos.Line, pos.Col, f.Name)
			}
		}
		portVal, ok := loadField("port")
		if !ok {
			return Value{}, fmt.Errorf("%d:%d: http.get's options object requires a 'port'", pos.Line, pos.Col)
		}
		portStr, err := e.emitValueToString(portVal)
		if err != nil {
			return Value{}, err
		}
		host := Value{Ref: e.internString("127.0.0.1"), Ty: TypePtr}
		if hv, ok := loadField("host"); ok && isStringTy(hv.Ty) {
			host = hv
		} else if hv, ok := loadField("hostname"); ok && isStringTy(hv.Ty) {
			host = hv
		}
		if mv, ok := loadField("method"); ok && isStringTy(mv.Ty) {
			methodRef = mv.Ref
		}
		acc, err := e.emitStringConcat(Value{Ref: e.internString(scheme + "://"), Ty: TypePtr}, host)
		if err != nil {
			return Value{}, err
		}
		if acc, err = e.emitStringConcat(acc, Value{Ref: e.internString(":"), Ty: TypePtr}); err != nil {
			return Value{}, err
		}
		if acc, err = e.emitStringConcat(acc, portStr); err != nil {
			return Value{}, err
		}
		if pv, ok := loadField("path"); ok && isStringTy(pv.Ty) {
			if acc, err = e.emitStringConcat(acc, pv); err != nil {
				return Value{}, err
			}
		} else if acc, err = e.emitStringConcat(acc, Value{Ref: e.internString("/"), Ty: TypePtr}); err != nil {
			return Value{}, err
		}
		urlVal = acc
	} else {
		v, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		urlVal = v
	}
	if urlVal.Ty.IR != "ptr" {
		return Value{}, fmt.Errorf("%d:%d: http.get's first argument must be a URL string or a { port, path, host } options object", pos.Line, pos.Col)
	}
	e.ensureFetchAsync()
	e.ensureStrHeaderRuntime()
	e.ensureCalloc()
	e.ensureMalloc()
	e.ensureHTTPRuntime()           // @__kml_listen_fd + event loop that drives the reaction
	e.ensureHTTPClientReactions()   // registry + fire + drive

	// Resolve the response callback (hinted so res.statusCode/res.on(...) inside
	// it resolve). V1 requires an arrow/function-expression literal.
	userCb := "null"
	if len(args) == 2 {
		contextTypeArrowParams(args[1], "__kml_client_response")
		cb, err := e.resolveCallbackWithHints(args[1], []Type{IncomingMessageType()})
		if err != nil {
			return Value{}, err
		}
		if cb.kind != cbClosure {
			return Value{}, fmt.Errorf("%d:%d: http.get's callback must be an arrow function literal", pos.Line, pos.Col)
		}
		userCb = cb.hdrPtr
	}

	// Build the ClientRequest handle (ADR-00430): { ptr url, ptr cb, i64
	// state }. `get` fires immediately (state 1); `request` waits for
	// .end() (state 0), matching Node's "nothing is sent until end()".
	e.ensureHTTPClientBegin()
	req := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @calloc(i64 1, i64 40)", req))
	g0 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr, i64 }, ptr %s, i32 0, i32 0", g0, req))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", urlVal.Ref, g0))
	g1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr, i64 }, ptr %s, i32 0, i32 1", g1, req))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", userCb, g1))
	g2 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr, i64 }, ptr %s, i32 0, i32 2", g2, req))
	g3 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 3", g3, req))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", methodRef, g3))
	g4 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 4", g4, req))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", headersRef, g4))
	if deferred {
		e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", g2))
	} else {
		e.emitInstr(fmt.Sprintf("store i64 1, ptr %s, align 8", g2))
		e.emitInstr(fmt.Sprintf("call void @__kml_httpc_begin(ptr %s, ptr %s, ptr %s, ptr %s)", urlVal.Ref, userCb, methodRef, headersRef))
	}
	return Value{Ref: req, Ty: ClientRequestType()}, nil
}

// ensureHTTPClientBegin emits @__kml_httpc_begin(url, usercb) once: start the
// transfer (fetch_async), register the completion reaction, and either defer
// to the running event loop or busy-drive when there is none. Also
// @__kml_httpc_req_end(req) — the ClientRequest .end() trigger.
func (e *Emitter) ensureHTTPClientBegin() {
	if e.usedHTTPCBegin {
		return
	}
	e.usedHTTPCBegin = true
	thunk := e.emitHTTPCompletionThunk()
	e.emitStandaloneFunc("void @__kml_httpc_begin(ptr %url, ptr %usercb, ptr %method, ptr %headers)", func() string {
		defMethod := e.internString("GET")
		mNull := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %%method, null", mNull))
		method := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %%method", method, mNull, defMethod))
		pending := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_fetch_async(ptr %%url, ptr %s, ptr %%headers, ptr null, ptr null)", pending, method))
		env := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", env))
		e0 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 0", e0, env))
		e.emitInstr(fmt.Sprintf("store ptr %%usercb, ptr %s, align 8", e0))
		e1 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 1", e1, env))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", pending, e1))
		closure := e.buildBuiltinClosure(thunk, env)
		e.emitInstr(fmt.Sprintf("call void @__kml_httpc_register(ptr %s, ptr %s)", pending, closure))
		e.emitInstr("store ptr @__kml_httpc_flush, ptr @__kml_httpc_flush_hook, align 8")
		listenfd := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr @__kml_listen_fd, align 8", listenfd))
		hasLoop := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp sge i64 %s, 0", hasLoop, listenfd))
		deferL := e.freshLabel("httpc.defer")
		driveL := e.freshLabel("httpc.drive")
		afterL := e.freshLabel("httpc.after")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", hasLoop, deferL, driveL))
		e.emitLabel(driveL)
		e.emitInstr(fmt.Sprintf("call void @__kml_httpc_drive(ptr %s)", pending))
		e.emitTerminator(fmt.Sprintf("br label %%%s", afterL))
		e.emitLabel(deferL)
		e.emitTerminator(fmt.Sprintf("br label %%%s", afterL))
		e.emitLabel(afterL)
		return "ret void"
	})
	e.emitGlobal(`
define void @__kml_httpc_req_end(ptr %req) {
entry:
  %st_p = getelementptr { ptr, ptr, i64 }, ptr %req, i32 0, i32 2
  %st = load i64, ptr %st_p, align 8
  %pending = icmp eq i64 %st, 0
  br i1 %pending, label %fire, label %ret
fire:
  store i64 1, ptr %st_p, align 8
  %url_p = getelementptr { ptr, ptr, i64 }, ptr %req, i32 0, i32 0
  %url = load ptr, ptr %url_p, align 8
  %cb_p = getelementptr { ptr, ptr, i64 }, ptr %req, i32 0, i32 1
  %cb = load ptr, ptr %cb_p, align 8
  %m_p = getelementptr ptr, ptr %req, i64 3
  %m = load ptr, ptr %m_p, align 8
  %h_p = getelementptr ptr, ptr %req, i64 4
  %h = load ptr, ptr %h_p, align 8
  call void @__kml_httpc_begin(ptr %url, ptr %cb, ptr %m, ptr %h)
  br label %ret
ret:
  ret void
}`)
}

// emitHTTPCompletionThunk emits (once) the reaction fired when a client transfer
// completes: read status/body off the (now-done) pending, build the
// IncomingMessage, invoke the user callback (which registers res.on(...)), then
// deliver the body as one 'data' chunk + 'end'. env = { ptr userCb, ptr pending }.
func (e *Emitter) emitHTTPCompletionThunk() string {
	fn := "@__kml_httpc_thunk"
	if e.usedHTTPCThunk {
		return fn
	}
	e.usedHTTPCThunk = true
	restore := e.beginThunkEmit()

	userCb := e.freshReg()
	ucp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %%env, i32 0, i32 0", ucp))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", userCb, ucp))
	pending := e.freshReg()
	pp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %%env, i32 0, i32 1", pp))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", pending, pp))

	raw := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call { i64, ptr, i64 } @__kml_await_fetch(ptr %s)", raw, pending))
	status := e.freshReg()
	bodyRaw := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue { i64, ptr, i64 } %s, 0", status, raw))
	e.emitInstr(fmt.Sprintf("%s = extractvalue { i64, ptr, i64 } %s, 1", bodyRaw, raw))
	bodyStr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_str_from_cstr(ptr %s)", bodyStr, bodyRaw))

	ty := IncomingMessageType()
	res := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @calloc(i64 1, i64 %d)", res, ty.StructSize()))
	statusD := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sitofp i64 %s to double", statusD, status))
	e.storeIncomingField(res, ty, "statusCode", "double", statusD)
	e.storeIncomingField(res, ty, incomingBodyField, "ptr", bodyStr)

	// Call the user callback if present, then deliver.
	isnull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isnull, userCb))
	callL := e.freshLabel("thunk.call")
	skipL := e.freshLabel("thunk.skip")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isnull, skipL, callL))
	e.emitLabel(callL)
	cb := Callback{kind: cbClosure, hdrPtr: userCb, ty: FuncType([]Type{ty}, TypeVoid)}
	_, _ = e.emitCBCall(cb, []Value{{Ref: res, Ty: ty}})
	e.emitIncomingDeliver(res, ty)
	e.emitTerminator(fmt.Sprintf("br label %%%s", skipL))
	e.emitLabel(skipL)
	e.emitInstr("ret void")

	body := e.allocas.String() + e.body.String()
	restore()
	e.functions.WriteString(fmt.Sprintf("\ndefine void %s(ptr %%env) {\nentry:\n%s}\n", fn, body))
	return fn
}

// storeIncomingField GEPs an IncomingMessage field by name and stores val.
func (e *Emitter) storeIncomingField(res string, ty Type, name, ir, val string) {
	idx, _, _ := ty.FieldIndex(name)
	g := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", g, ty.StructIR(), res, idx))
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align 8", ir, val, g))
}

// emitIncomingDeliver fires the stored 'data' listener with the buffered body
// string, then the 'end' listener — each guarded by a null check.
func (e *Emitter) emitIncomingDeliver(res string, ty Type) {
	bodyIdx, _, _ := ty.FieldIndex(incomingBodyField)
	bg := e.freshReg()
	body := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", bg, ty.StructIR(), res, bodyIdx))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", body, bg))
	e.fireIncomingListener(res, ty, incomingDataListenerField, body, TypePtr)
	e.fireIncomingListener(res, ty, incomingEndListenerField, "", Type{})
}

// fireIncomingListener loads the closure header at fieldName; if non-null,
// calls it with the optional single argument (arg=="" for the zero-arg 'end').
func (e *Emitter) fireIncomingListener(res string, ty Type, fieldName, arg string, argTy Type) {
	idx, _, _ := ty.FieldIndex(fieldName)
	g := e.freshReg()
	hdr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", g, ty.StructIR(), res, idx))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", hdr, g))
	isnull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isnull, hdr))
	fireL := e.freshLabel("im.fire")
	doneL := e.freshLabel("im.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isnull, doneL, fireL))
	e.emitLabel(fireL)
	var params []Type
	var callArgs []Value
	if arg != "" {
		params = []Type{argTy}
		callArgs = []Value{{Ref: arg, Ty: argTy}}
	}
	cb := Callback{kind: cbClosure, hdrPtr: hdr, ty: FuncType(params, TypeVoid)}
	_, _ = e.emitCBCall(cb, callArgs)
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
}

// emitIncomingMessageCall dispatches res.on('data'|'end'|…) and the
// accepted-but-ignored lifecycle methods on an IncomingMessage.
func (e *Emitter) emitIncomingMessageCall(objExpr ast.Expression, method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	objVal, err := e.emitExpr(objExpr)
	if err != nil {
		return Value{}, err
	}
	ty := objVal.Ty
	switch method {
	case "on", "once":
		if len(args) != 2 {
			return Value{}, fmt.Errorf("%d:%d: res.%s takes (event, listener)", pos.Line, pos.Col, method)
		}
		lit, ok := args[0].(*ast.StringLiteral)
		if !ok {
			return Value{}, fmt.Errorf("%d:%d: res.%s requires a string-literal event name", pos.Line, pos.Col, method)
		}
		switch lit.Value {
		case "data":
			cb, err := e.resolveCallbackWithHints(args[1], []Type{TypePtr})
			if err != nil {
				return Value{}, err
			}
			if cb.kind != cbClosure {
				return Value{}, fmt.Errorf("%d:%d: a res.on('data') listener must be an arrow function literal", pos.Line, pos.Col)
			}
			e.storeIncomingField(objVal.Ref, ty, incomingDataListenerField, "ptr", cb.hdrPtr)
		case "end":
			cb, err := e.resolveCallbackWithHints(args[1], nil)
			if err != nil {
				return Value{}, err
			}
			if cb.kind != cbClosure {
				return Value{}, fmt.Errorf("%d:%d: a res.on('end') listener must be an arrow function literal", pos.Line, pos.Col)
			}
			e.storeIncomingField(objVal.Ref, ty, incomingEndListenerField, "ptr", cb.hdrPtr)
		case "error", "close", "aborted", "readable":
			// Accepted: our fetch primitive throws on a network error rather than
			// emitting 'error', and the body arrives whole — so these never fire.
		default:
			return Value{}, fmt.Errorf("%d:%d: an http IncomingMessage supports .on('data'|'end') (got '%s')", pos.Line, pos.Col, lit.Value)
		}
		return objVal, nil
	case "setEncoding", "resume", "pause", "destroy", "setTimeout":
		// Accepted, no-op (single-chunk buffered body; no schedulable flow).
		for _, a := range args {
			if _, err := e.emitExpr(a); err != nil {
				return Value{}, err
			}
		}
		return objVal, nil
	}
	return Value{}, fmt.Errorf("%d:%d: an http IncomingMessage has no method '%s'", pos.Line, pos.Col, method)
}

// emitClientRequestMethod dispatches methods on the http.get/request handle
// (ADR-00430): .end() fires a deferred request; .abort()/.destroy() cancel a
// not-yet-fired one; .on('response') binds the callback slot, .on('error')
// is accepted (there is no async transport-error delivery to feed it — a
// transport failure surfaces as a thrown Error instead, so a mustNotCall
// error listener behaves correctly on the success path).
func (e *Emitter) emitClientRequestMethod(objExpr ast.Expression, method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	objVal, err := e.emitExpr(objExpr)
	if err != nil {
		return Value{}, err
	}
	self := Value{Ref: objVal.Ref, Ty: ClientRequestType()}
	stGEP := func() string {
		g := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr, i64 }, ptr %s, i32 0, i32 2", g, objVal.Ref))
		return g
	}
	switch method {
	case "end":
		if len(args) != 0 {
			return Value{}, fmt.Errorf("%d:%d: req.end() with a body is not supported (this client sends GET requests only — TDD-00138 Stage 2)", pos.Line, pos.Col)
		}
		e.ensureHTTPClientBegin()
		e.emitInstr(fmt.Sprintf("call void @__kml_httpc_req_end(ptr %s)", objVal.Ref))
		return self, nil
	case "abort", "destroy":
		// Cancel only a not-yet-fired request (state 0 → 2); an in-flight
		// libcurl transfer is not torn down (disclosed caveat).
		g := stGEP()
		st := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", st, g))
		isPending := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", isPending, st))
		doL := e.freshLabel("creq.cancel")
		doneL := e.freshLabel("creq.done")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isPending, doL, doneL))
		e.emitLabel(doL)
		e.emitInstr(fmt.Sprintf("store i64 2, ptr %s, align 8", g))
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
		e.emitLabel(doneL)
		return self, nil
	case "on", "once":
		evt, err := stringLiteralArg(args, 0, "req.on", pos)
		if err != nil {
			return Value{}, err
		}
		if len(args) != 2 {
			return Value{}, fmt.Errorf("%d:%d: req.on takes (event, listener)", pos.Line, pos.Col)
		}
		switch evt {
		case "response":
			contextTypeArrowParams(args[1], "__kml_client_response")
			cb, err := e.resolveCallbackWithHints(args[1], []Type{IncomingMessageType()})
			if err != nil {
				return Value{}, err
			}
			if cb.kind != cbClosure {
				return Value{}, fmt.Errorf("%d:%d: req.on('response') needs an arrow function literal", pos.Line, pos.Col)
			}
			g := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr, i64 }, ptr %s, i32 0, i32 1", g, objVal.Ref))
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", cb.hdrPtr, g))
			return self, nil
		case "error":
			// Evaluated and held nowhere: no async transport-error delivery
			// exists to fire it (failures throw instead).
			if _, err := e.emitExpr(args[1]); err != nil {
				return Value{}, err
			}
			return self, nil
		}
		return Value{}, fmt.Errorf("%d:%d: req.on supports 'response' and 'error' (got '%s')", pos.Line, pos.Col, evt)
	}
	return Value{}, fmt.Errorf("%d:%d: a ClientRequest supports .end(), .abort()/.destroy(), and .on('response'|'error') (got '%s')", pos.Line, pos.Col, method)
}

// emitNewHTTPAgent implements `new http.Agent(options?)` (ADR-00432): an
// inert pool-config token. This client opens one connection per request —
// Node's own behavior with a non-keepAlive Agent — so the recognized options
// are validated (unknown keys are a clean rejection) and their values
// evaluated, but nothing is stored: there is no pool to configure.
func (e *Emitter) emitNewHTTPAgent(ex *ast.NewHTTPAgentExpression) (Value, error) {
	pos := ex.GetPos()
	if ex.Options != nil {
		lit, ok := ex.Options.(*ast.ObjectLiteral)
		if !ok {
			return Value{}, fmt.Errorf("%d:%d: new Agent's options must be an object literal", pos.Line, pos.Col)
		}
		allowed := map[string]bool{
			"keepAlive": true, "keepAliveMsecs": true, "maxSockets": true,
			"maxFreeSockets": true, "maxTotalSockets": true, "timeout": true,
			"scheduling": true,
		}
		for _, prop := range lit.Properties {
			if !allowed[prop.Key] {
				return Value{}, fmt.Errorf("%d:%d: unknown Agent option '%s'", pos.Line, pos.Col, prop.Key)
			}
			if _, err := e.emitExpr(prop.Value); err != nil {
				return Value{}, err
			}
		}
	}
	e.ensureCalloc()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @calloc(i64 1, i64 8)", r))
	return Value{Ref: r, Ty: HTTPAgentType()}, nil
}

// emitHTTPAgentMethod: `.destroy()` is the one supported Agent method — a
// no-op (there are no pooled sockets to destroy).
func (e *Emitter) emitHTTPAgentMethod(objExpr ast.Expression, method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	if _, err := e.emitExpr(objExpr); err != nil {
		return Value{}, err
	}
	if method == "destroy" && len(args) == 0 {
		return Value{Ty: TypeVoid}, nil
	}
	return Value{}, fmt.Errorf("%d:%d: an http.Agent supports .destroy() only (there is no connection pool — one connection per request)", pos.Line, pos.Col)
}
