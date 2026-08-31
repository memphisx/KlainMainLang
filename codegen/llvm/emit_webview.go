package llvm

import (
	"fmt"

	"KlainMainLang/ast"
)

// emit_webview.go — `new Webview(opts)` and its method dispatch (TDD-00142).
//
// The handle is a calloc'd `{ ptr webview_t, ptr boundListHead }` struct. Field
// 0 is the opaque webview_t the C API takes; field 1 heads a linked list of
// per-bind nodes `{ ptr w, ptr closureHdr, ptr next }` that both carry the bind
// trampoline's env AND retain the user closure so it is never freed (manual
// mode) / stays reachable (gc mode) — the retention the C side depends on for
// the lifetime of the window.
//
// One window per process in V1: a second `new Webview` is a clean compile-time
// rejection (webviewConstructed guard on the emitter).

const webviewHandleIR = "{ ptr, ptr }"    // { webview_t, boundListHead }
const webviewBindNodeIR = "{ ptr, ptr, ptr }" // { w, closureHdr, next }

// emitNewWebview implements `new Webview({ title, width, height, debug })`.
func (e *Emitter) emitNewWebview(ex *ast.NewWebviewExpression) (Value, error) {
	pos := ex.GetPos()
	if e.webviewConstructed {
		return Value{}, fmt.Errorf("%d:%d: only one Webview window per process is supported (V1)", pos.Line, pos.Col)
	}
	e.webviewConstructed = true
	e.ensureWebviewRuntime()
	e.ensureCalloc()

	// Parse the options object literal (all fields optional).
	var titleExpr, widthExpr, heightExpr, debugExpr ast.Expression
	var bindingsExpr ast.Expression
	servePath := ""
	if ex.Options != nil {
		lit, ok := ex.Options.(*ast.ObjectLiteral)
		if !ok {
			return Value{}, fmt.Errorf("%d:%d: new Webview's options must be an object literal", pos.Line, pos.Col)
		}
		for _, prop := range lit.Properties {
			switch prop.Key {
			case "title":
				titleExpr = prop.Value
			case "width":
				widthExpr = prop.Value
			case "height":
				heightExpr = prop.Value
			case "debug":
				debugExpr = prop.Value
			case "bindings":
				bindingsExpr = prop.Value
			case "serve":
				p, ok := staticStringValue(prop.Value)
				if !ok {
					return Value{}, fmt.Errorf("%d:%d: Webview's serve path must be a string literal (the directory is embedded at compile time)", pos.Line, pos.Col)
				}
				servePath = p
			default:
				return Value{}, fmt.Errorf("%d:%d: unknown Webview option '%s' (expected title/width/height/debug/bindings/serve)", pos.Line, pos.Col, prop.Key)
			}
		}
	}

	// debug flag → i32 (default 0).
	debugReg := "0"
	if debugExpr != nil {
		dv, err := e.emitExpr(debugExpr)
		if err != nil {
			return Value{}, err
		}
		dv = e.coerce(dv, TypeBool)
		z := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = zext i1 %s to i32", z, dv.Ref))
		debugReg = z
	}

	w := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @webview_create(i32 %s, ptr null)", w, debugReg))

	handle := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @calloc(i64 1, i64 16)", handle))
	w0 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 0", w0, webviewHandleIR, handle))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", w, w0))

	if titleExpr != nil {
		tv, err := e.emitExpr(titleExpr)
		if err != nil {
			return Value{}, err
		}
		e.emitInstr(fmt.Sprintf("call void @webview_set_title(ptr %s, ptr %s)", w, tv.Ref))
	}
	if widthExpr != nil && heightExpr != nil {
		if err := e.emitWebviewSetSize(w, widthExpr, heightExpr); err != nil {
			return Value{}, err
		}
	} else if (widthExpr == nil) != (heightExpr == nil) {
		return Value{}, fmt.Errorf("%d:%d: Webview width and height must be given together", pos.Line, pos.Col)
	}

	// The `bindings` object (TDD-00142 Stage 5): each key becomes a typed
	// window.* native function. Bound here (after the handle exists) — binds may
	// be registered any time before run().
	if bindingsExpr != nil {
		if err := e.emitWebviewBindings(w, handle, bindingsExpr, pos); err != nil {
			return Value{}, err
		}
	}

	// `serve: "./dist"` (TDD-00142 Stage 7): embed the directory, start the
	// in-binary static server on a detached thread (ephemeral loopback port,
	// returned synchronously), and navigate — a single-file SPA desktop app.
	if servePath != "" {
		sym := e.requireEmbed(servePath)
		e.ensureEmbedSymbol(sym)
		e.ensureEmbedAssetsRuntime()
		e.ensureMalloc()
		e.ensureSprintf()
		port := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_embed_serve(ptr @%s)", port, sym))
		// Build "http://127.0.0.1:<port>/" into a heap buffer and navigate.
		urlBuf := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 64)", urlBuf))
		fmtStr := e.internString("http://127.0.0.1:%d/")
		e.emitInstr(fmt.Sprintf("call i32 (ptr, ptr, ...) @sprintf(ptr %s, ptr %s, i32 %s)", urlBuf, fmtStr, port))
		e.emitInstr(fmt.Sprintf("call void @webview_navigate(ptr %s, ptr %s)", w, urlBuf))
	}

	return Value{Ref: handle, Ty: WebviewType()}, nil
}

// emitWebviewSetSize evaluates width/height to i32 and calls webview_set_size
// with WEBVIEW_HINT_NONE (0).
func (e *Emitter) emitWebviewSetSize(w string, widthExpr, heightExpr ast.Expression) error {
	wv, err := e.emitExpr(widthExpr)
	if err != nil {
		return err
	}
	hv, err := e.emitExpr(heightExpr)
	if err != nil {
		return err
	}
	wi := e.coerce(wv, TypeI32)
	hi := e.coerce(hv, TypeI32)
	e.emitInstr(fmt.Sprintf("call void @webview_set_size(ptr %s, i32 %s, i32 %s, i32 0)", w, wi.Ref, hi.Ref))
	return nil
}

// loadWebviewHandle evaluates the handle expression and returns the loaded
// webview_t (field 0) plus the handle struct pointer (for the bound-list head).
func (e *Emitter) loadWebviewHandle(objExpr ast.Expression) (w, handle string, err error) {
	v, err := e.emitExpr(objExpr)
	if err != nil {
		return "", "", err
	}
	handle = v.Ref
	w0 := e.freshReg()
	w = e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 0", w0, webviewHandleIR, handle))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", w, w0))
	return w, handle, nil
}

// emitWebviewMethod dispatches the window methods on a Webview handle.
func (e *Emitter) emitWebviewMethod(objExpr ast.Expression, method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	w, handle, err := e.loadWebviewHandle(objExpr)
	if err != nil {
		return Value{}, err
	}

	// One-string-argument window mutators.
	strMut := map[string]string{
		"navigate": "webview_navigate",
		"html":     "webview_set_html",
		"setTitle": "webview_set_title",
		"init":     "webview_init",
		"unbind":   "webview_unbind",
	}
	if cfn, ok := strMut[method]; ok {
		if len(args) != 1 {
			return Value{}, fmt.Errorf("%d:%d: Webview.%s(s) takes one string argument", pos.Line, pos.Col, method)
		}
		sv, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		e.emitInstr(fmt.Sprintf("call void @%s(ptr %s, ptr %s)", cfn, w, sv.Ref))
		return Value{Ty: TypeVoid}, nil
	}

	switch method {
	case "eval":
		// Routed through webview_dispatch so eval is thread-safe from any
		// thread (a Worker pushing a UI update) — the js string is the dispatch
		// arg, run by __kml_wv_eval_tramp on the GUI thread.
		if len(args) != 1 {
			return Value{}, fmt.Errorf("%d:%d: Webview.eval(js) takes one string argument", pos.Line, pos.Col)
		}
		sv, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		e.emitInstr(fmt.Sprintf("call void @webview_dispatch(ptr %s, ptr @__kml_wv_eval_tramp, ptr %s)", w, sv.Ref))
		return Value{Ty: TypeVoid}, nil

	case "setSize":
		if len(args) != 2 {
			return Value{}, fmt.Errorf("%d:%d: Webview.setSize(width, height) takes two numbers", pos.Line, pos.Col)
		}
		if err := e.emitWebviewSetSize(w, args[0], args[1]); err != nil {
			return Value{}, err
		}
		return Value{Ty: TypeVoid}, nil

	case "run":
		if len(args) != 0 {
			return Value{}, fmt.Errorf("%d:%d: Webview.run() takes no arguments", pos.Line, pos.Col)
		}
		// Loop fusion V1 (TDD-00142 Stage 3): install a page-driven tick pump so
		// this runtime's timers/microtasks/tasks run on the GUI thread — the
		// page's setInterval calls a bound native pump that drains them. Enables
		// setTimeout-driven UI updates and async bind without a Worker.
		e.emitWebviewPumpInstall(w, handle)
		e.emitInstr(fmt.Sprintf("call void @webview_run(ptr %s)", w))
		return Value{Ty: TypeVoid}, nil

	case "terminate":
		if len(args) != 0 {
			return Value{}, fmt.Errorf("%d:%d: Webview.terminate() takes no arguments", pos.Line, pos.Col)
		}
		e.emitInstr(fmt.Sprintf("call void @webview_terminate(ptr %s)", w))
		return Value{Ty: TypeVoid}, nil

	case "destroy":
		if len(args) != 0 {
			return Value{}, fmt.Errorf("%d:%d: Webview.destroy() takes no arguments", pos.Line, pos.Col)
		}
		e.emitInstr(fmt.Sprintf("call void @webview_destroy(ptr %s)", w))
		return Value{Ty: TypeVoid}, nil

	case "bind":
		return e.emitWebviewBind(w, handle, args, pos)
	case "bindTyped":
		return e.emitWebviewBindTyped(w, handle, args, pos)
	}

	return Value{}, fmt.Errorf("%d:%d: Webview has no method '%s'", pos.Line, pos.Col, method)
}

// emitWebviewBind implements `w.bind(name, cb)`: registers a per-bind
// trampoline whose env is a retained node carrying (w, closure). The callback
// receives the page's JSON-array argument string and returns a JSON string
// (V1 contract: sync string-in / string-out; a throw rejects the page promise).
func (e *Emitter) emitWebviewBind(w, handle string, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 2 {
		return Value{}, fmt.Errorf("%d:%d: Webview.bind(name, callback) takes a name and a callback", pos.Line, pos.Col)
	}
	nameV, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	cb, err := e.resolveCallback(args[1])
	if err != nil {
		return Value{}, err
	}
	// V1 contract: the callback must be a closure (arrow/function value) so it
	// can be retained on the handle; a bare named function has no env to retain
	// but the trampoline still needs a closure header. resolveCallback already
	// yields a closure header for arrows/function-typed values; a named-function
	// ref (cbNamed) is wrapped into one.
	closureHdr, cbTy, err := e.webviewCallbackHeader(cb)
	if err != nil {
		return Value{}, fmt.Errorf("%d:%d: %v", pos.Line, pos.Col, err)
	}
	nm, _ := staticStringValue(args[0])
	if err := e.bindOne(w, handle, nm, nameV.Ref, closureHdr, cbTy, false, pos); err != nil {
		return Value{}, err
	}
	return Value{Ty: TypeVoid}, nil
}

// emitWebviewBindTyped implements `w.bindTyped(name, fn)` (TDD-00142 Stage 5):
// the imperative counterpart to the constructor's `bindings` object — same as
// bind, but the trampoline auto-decodes the page's arguments into fn's declared
// parameter types and JSON-encodes the return.
func (e *Emitter) emitWebviewBindTyped(w, handle string, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 2 {
		return Value{}, fmt.Errorf("%d:%d: Webview.bindTyped(name, callback) takes a name and a callback", pos.Line, pos.Col)
	}
	nameV, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	cb, err := e.resolveCallback(args[1])
	if err != nil {
		return Value{}, err
	}
	closureHdr, cbTy, err := e.webviewCallbackHeader(cb)
	if err != nil {
		return Value{}, fmt.Errorf("%d:%d: %v", pos.Line, pos.Col, err)
	}
	nm, _ := staticStringValue(args[0])
	if err := e.bindOne(w, handle, nm, nameV.Ref, closureHdr, cbTy, true, pos); err != nil {
		return Value{}, err
	}
	return Value{Ty: TypeVoid}, nil
}

// bindOne registers one binding: allocates + retains the { w, closureHdr, next }
// node onto handle.boundListHead, emits the (typed or raw) trampoline, and calls
// webview_bind under nameRef (a ptr register to a kml string). Shared by
// w.bind, w.bindTyped, and the `bindings` constructor object.
func (e *Emitter) bindOne(w, handle, name, nameRef, closureHdr string, cbTy Type, typed bool, pos ast.Pos) error {
	// Record typed bindings with a compile-time-known name for --emit-window-dts.
	if typed && name != "" {
		e.webviewBindings = append(e.webviewBindings, webviewBindingSig{Name: name, Params: cbTy.FuncParams, Ret: cbTy.FuncRetType})
	}
	// Allocate + retain the bind node { w, closureHdr, next=oldHead }.
	e.ensureMalloc()
	node := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 24)", node))
	n0 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 0", n0, webviewBindNodeIR, node))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", w, n0))
	n1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 1", n1, webviewBindNodeIR, node))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", closureHdr, n1))
	// Link into handle.boundListHead (field 1).
	headP := e.freshReg()
	oldHead := e.freshReg()
	n2 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 1", headP, webviewHandleIR, handle))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", oldHead, headP))
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 2", n2, webviewBindNodeIR, node))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", oldHead, n2))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", node, headP))

	thunk := e.emitWebviewBindThunk(cbTy, typed, pos)
	e.emitInstr(fmt.Sprintf("call void @webview_bind(ptr %s, ptr %s, ptr %s, ptr %s)", w, nameRef, thunk, node))
	return nil
}

// emitWebviewBindings desugars the constructor's `bindings` object (TDD-00142
// Stage 5): each key becomes a typed `window.*` native function. The keys are
// the whole exposed surface (an explicit allowlist). Accepts an object literal
// `{ f, g }` or a variable whose static type is an object of functions.
func (e *Emitter) emitWebviewBindings(w, handle string, expr ast.Expression, pos ast.Pos) error {
	if lit, ok := expr.(*ast.ObjectLiteral); ok {
		for _, prop := range lit.Properties {
			cb, err := e.resolveCallback(prop.Value)
			if err != nil {
				return fmt.Errorf("%d:%d: bindings.%s must be a function: %v", pos.Line, pos.Col, prop.Key, err)
			}
			hdr, cbTy, err := e.webviewCallbackHeader(cb)
			if err != nil {
				return fmt.Errorf("%d:%d: bindings.%s: %v", pos.Line, pos.Col, prop.Key, err)
			}
			if err := e.bindOne(w, handle, prop.Key, e.internString(prop.Key), hdr, cbTy, true, pos); err != nil {
				return err
			}
		}
		return nil
	}
	// Variable / object-typed form: the shape travels with the type.
	ty := e.inferExprType(expr)
	if !ty.IsObject {
		return fmt.Errorf("%d:%d: Webview bindings must be an object literal or a variable of type object-of-functions", pos.Line, pos.Col)
	}
	objV, err := e.emitExpr(expr)
	if err != nil {
		return err
	}
	structIR := ty.StructIR()
	for i, f := range ty.Fields {
		if !f.Ty.IsFunc {
			return fmt.Errorf("%d:%d: bindings.%s must be a function, got a non-function field", pos.Line, pos.Col, f.Name)
		}
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, structIR, objV.Ref, i))
		hdr := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", hdr, gep))
		if err := e.bindOne(w, handle, f.Name, e.internString(f.Name), hdr, f.Ty, true, pos); err != nil {
			return err
		}
	}
	return nil
}

// emitWebviewPumpInstall wires the page-tick pump (TDD-00142 Stage 3): it binds
// an internal `__kml_tick` native handler that drains this runtime's timers +
// microtasks + task scheduler on the GUI thread, and injects a `setInterval`
// (via init, so it re-arms on every page load, guarded so it installs once) that
// calls it. The pump's env is the handle itself (already retained), and its
// webview_t is field 0 of that handle.
func (e *Emitter) emitWebviewPumpInstall(w, handle string) {
	pump := e.emitWebviewPumpThunk()
	nameV := e.internString("__kml_tick")
	e.emitInstr(fmt.Sprintf("call void @webview_bind(ptr %s, ptr %s, ptr %s, ptr %s)", w, nameV, pump, handle))
	js := e.internString("if(!window.__kml_pump){window.__kml_pump=setInterval(function(){window.__kml_tick&&window.__kml_tick()},16)}")
	e.emitInstr(fmt.Sprintf("call void @webview_init(ptr %s, ptr %s)", w, js))
}

// emitWebviewPumpThunk emits the internal `__kml_tick` handler (once per
// program): drain timers (non-blocking), the task scheduler, and microtasks,
// then resolve the page-side promise. env is the Webview handle; w = env[0].
func (e *Emitter) emitWebviewPumpThunk() string {
	fn := "@__kml_wv_pump"
	if e.webviewPumpEmitted {
		return fn
	}
	e.webviewPumpEmitted = true
	restore := e.beginThunkEmit()
	defer restore()

	e.emitInstr(fmt.Sprintf("%%w0 = getelementptr %s, ptr %%env, i32 0, i32 0", webviewHandleIR))
	e.emitInstr("%w = load ptr, ptr %w0, align 8")
	e.emitInstr("call void @__kml_timer_tick()")
	e.emitInstr("call void @__kml_task_sched_step()")
	e.emitInstr("call void @__kml_drain_microtasks()")
	nullJSON := e.internString("null")
	e.emitInstr(fmt.Sprintf("call void @webview_return(ptr %%w, ptr %%id, i32 0, ptr %s)", nullJSON))
	e.emitInstr("ret void")

	e.functions.WriteString(fmt.Sprintf("\ndefine void %s(ptr %%id, ptr %%req, ptr %%env) {\nentry:\n", fn))
	e.functions.WriteString(e.allocas.String())
	e.functions.WriteString(e.body.String())
	e.functions.WriteString("}\n")
	return fn
}

// decodeTypedBindArgs decodes the copied request string `%rstr` (a JSON array of
// the page's call arguments) into the callback's declared parameter types
// (TDD-00142 Stage 5). It parses into a tuple of the param types via the shared
// JSON projection, then GEP+loads each field into a Value ready for emitCBCall.
// Emitted inside the bind thunk, so `%rstr` is in scope.
func (e *Emitter) decodeTypedBindArgs(cbTy Type, pos ast.Pos) ([]Value, error) {
	if len(cbTy.FuncParams) == 0 {
		return nil, nil
	}
	tupleTy := TupleType(cbTy.FuncParams)
	argsTuple, err := e.emitJSONParseValue(Value{Ref: "%rstr", Ty: TypePtr}, tupleTy, pos)
	if err != nil {
		return nil, err
	}
	structIR := tupleTy.StructIR()
	argVals := make([]Value, len(cbTy.FuncParams))
	for i, p := range cbTy.FuncParams {
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, structIR, argsTuple.Ref, i))
		ld := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", ld, StructFieldIR(p), gep, p.Align()))
		argVals[i] = Value{Ref: ld, Ty: p}
	}
	return argVals, nil
}

// emitWebviewTypedReturnRunner emits (once per inner type) the promise-settlement
// reaction for an async *typed* binding (TDD-00142 Stage 6): a microtask runner
// `void @__kml_wv_tret_<n>(ptr env)` whose env is { promise, w, id }. On
// fulfillment it loads the settled value as innerTy, JSON-encodes it, and
// webview_return(0, json); on rejection, status 1 with a fixed message. Unlike
// the raw `@__kml_wv_return_runner` (which forwards a Promise<string> verbatim),
// this stringifies an arbitrary settled T.
func (e *Emitter) emitWebviewTypedReturnRunner(innerTy Type) string {
	key := StructFieldIR(innerTy) + "|" + fmt.Sprintf("%v%v%v", innerTy.IsObject, innerTy.IsArray, innerTy.IsTuple)
	if e.webviewTypedRunners == nil {
		e.webviewTypedRunners = map[string]string{}
	}
	if fn, ok := e.webviewTypedRunners[key]; ok {
		return fn
	}
	fn := fmt.Sprintf("@__kml_wv_tret_%d", len(e.webviewTypedRunners))
	e.webviewTypedRunners[key] = fn
	rejMsg := e.internString(`"native handler rejected"`)

	restore := e.beginThunkEmit()
	defer restore()
	e.emitInstr("%p_p = getelementptr { ptr, ptr, ptr }, ptr %env, i32 0, i32 0")
	e.emitInstr("%p = load ptr, ptr %p_p, align 8")
	e.emitInstr("%w_p = getelementptr { ptr, ptr, ptr }, ptr %env, i32 0, i32 1")
	e.emitInstr("%w = load ptr, ptr %w_p, align 8")
	e.emitInstr("%id_p = getelementptr { ptr, ptr, ptr }, ptr %env, i32 0, i32 2")
	e.emitInstr("%id = load ptr, ptr %id_p, align 8")
	e.emitInstr(fmt.Sprintf("%%state_p = getelementptr %s, ptr %%p, i32 0, i32 0", promiseStructIR))
	e.emitInstr("%state = load i64, ptr %state_p, align 8")
	e.emitInstr("%fulfilled = icmp eq i64 %state, 1")
	fulL := e.freshLabel("tret.ful")
	rejL := e.freshLabel("tret.rej")
	e.emitTerminator(fmt.Sprintf("br i1 %%fulfilled, label %%%s, label %%%s", fulL, rejL))

	e.emitLabel(fulL)
	if innerTy.IR == "void" || innerTy.IR == "" {
		nullS := e.internString("null")
		e.emitInstr(fmt.Sprintf("call void @webview_return(ptr %%w, ptr %%id, i32 0, ptr %s)", nullS))
	} else {
		val := e.loadPromiseValue("%p", innerTy)
		strVal, _ := e.emitJSONStringifyValue(val, jsonIndent{})
		e.emitInstr(fmt.Sprintf("call void @webview_return(ptr %%w, ptr %%id, i32 0, ptr %s)", strVal.Ref))
	}
	e.emitInstr("ret void")

	e.emitLabel(rejL)
	e.emitInstr(fmt.Sprintf("call void @webview_return(ptr %%w, ptr %%id, i32 1, ptr %s)", rejMsg))
	e.emitInstr("ret void")

	e.functions.WriteString(fmt.Sprintf("\ndefine void %s(ptr %%env) {\nentry:\n", fn))
	e.functions.WriteString(e.allocas.String())
	e.functions.WriteString(e.body.String())
	e.functions.WriteString("}\n")
	return fn
}

// webviewCallbackHeader normalizes a resolved Callback to a closure header
// pointer plus its function Type, wrapping a named function reference into a
// closure so the trampoline has a uniform { fp, env } to call.
func (e *Emitter) webviewCallbackHeader(cb Callback) (hdr string, ty Type, err error) {
	if cb.kind == cbClosure {
		return cb.hdrPtr, cb.ty, nil
	}
	// cbNamed: build a { fnptr, env=null } header around the named function.
	fnTy := FuncType(cb.sig.ParamTypes, cb.sig.RetType)
	h := e.buildBuiltinClosure("@"+cb.name, "null")
	return h, fnTy, nil
}

// emitWebviewAsyncReturn registers the webview_return settlement reaction on a
// promise returned by an async `bind` callback (TDD-00142 Stage 3). It copies
// the page's call id (owned by webview only during the callback; the promise
// settles later), builds a { promise, w, id } env + a { runner, env } closure,
// and either enqueues it now (promise already settled) or attaches it to the
// promise's reaction list — the exact pattern emitPromiseAdopt uses. Emitted
// inside the bind thunk, so `%w`/`%id` are in scope.
func (e *Emitter) emitWebviewAsyncReturn(promiseRef, runnerFn string) {
	e.ensureMicrotasks()
	e.ensureMalloc()
	e.ensureStrHeaderRuntime()
	e.ensureStrlen()
	e.ensureMemcpy()

	// Copy id → a kml string that outlives the callback.
	e.emitInstr("%idlen = call i64 @strlen(ptr %id)")
	e.emitInstr("%idcopy = call ptr @__kml_str_alloc(i64 %idlen)")
	e.emitInstr("call ptr @memcpy(ptr %idcopy, ptr %id, i64 %idlen)")
	e.emitInstr("%idnul = getelementptr i8, ptr %idcopy, i64 %idlen")
	e.emitInstr("store i8 0, ptr %idnul, align 1")

	// env = { promise, w, idcopy }
	env := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 24)", env))
	e0 := e.freshReg()
	e1 := e.freshReg()
	e2 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr, ptr }, ptr %s, i32 0, i32 0", e0, env))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", promiseRef, e0))
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr, ptr }, ptr %s, i32 0, i32 1", e1, env))
	e.emitInstr(fmt.Sprintf("store ptr %%w, ptr %s, align 8", e1))
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr, ptr }, ptr %s, i32 0, i32 2", e2, env))
	e.emitInstr(fmt.Sprintf("store ptr %%idcopy, ptr %s, align 8", e2))

	// closure = { runner, env }
	clo := e.freshReg()
	cfp := e.freshReg()
	cep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", clo))
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 0", cfp, clo))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", runnerFn, cfp))
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 1", cep, clo))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", env, cep))

	// If the promise is already settled → enqueue now; else attach a reaction
	// node onto its reaction list (field 4).
	res := e.freshReg()
	resP := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 0", resP, promiseStructIR, promiseRef))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", res, resP))
	settled := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", settled, res))
	nowL := e.freshLabel("wvret.now")
	laterL := e.freshLabel("wvret.later")
	doneL := e.freshLabel("wvret.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", settled, nowL, laterL))
	e.emitLabel(nowL)
	e.emitInstr(fmt.Sprintf("call void @__kml_microtask_enqueue(ptr %s)", clo))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(laterL)
	node := e.freshReg()
	rxP := e.freshReg()
	oldHead := e.freshReg()
	nodeClo := e.freshReg()
	nodeNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", node))
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 4", rxP, promiseStructIR, promiseRef))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", oldHead, rxP))
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 0", nodeClo, node))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", clo, nodeClo))
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 1", nodeNext, node))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", oldHead, nodeNext))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", node, rxP))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
}

// emitWebviewBindThunk emits the per-bind C-ABI trampoline
// `void @__kml_wv_bind_N(ptr %id, ptr %req, ptr %env)`: copy req to a kml
// string, invoke the user closure under a setjmp guard, and resolve
// (webview_return status 0) with its JSON string result or reject (status 1)
// with a JSON error string on throw. env is the bind node { w, closure, next }.
func (e *Emitter) emitWebviewBindThunk(cbTy Type, typed bool, pos ast.Pos) string {
	fn := fmt.Sprintf("@__kml_wv_bind_%d", e.nextWebviewThunkID())
	restore := e.beginThunkEmit()
	defer restore()

	// Load w and closure header from the env node.
	e.emitInstr(fmt.Sprintf("%%w0 = getelementptr %s, ptr %%env, i32 0, i32 0", webviewBindNodeIR))
	e.emitInstr("%w = load ptr, ptr %w0, align 8")
	e.emitInstr(fmt.Sprintf("%%c0 = getelementptr %s, ptr %%env, i32 0, i32 1", webviewBindNodeIR))
	e.emitInstr("%clo = load ptr, ptr %c0, align 8")

	// Copy the C req string into a kml (NUL-terminated) string.
	e.ensureStrHeaderRuntime() // __kml_str_alloc
	e.ensureStrlen()
	e.ensureMemcpy()
	e.emitInstr("%rlen = call i64 @strlen(ptr %req)")
	e.emitInstr("%rstr = call ptr @__kml_str_alloc(i64 %rlen)")
	e.emitInstr("call ptr @memcpy(ptr %rstr, ptr %req, i64 %rlen)")
	e.emitInstr("%rnul = getelementptr i8, ptr %rstr, i64 %rlen")
	e.emitInstr("store i8 0, ptr %rnul, align 1")

	cb := Callback{kind: cbClosure, hdrPtr: "%clo", ty: cbTy}
	errJSON := e.internString(`"native handler threw"`)

	err := e.emitFsGuarded(
		func() error {
			// Typed bind (TDD-00142 Stage 5): decode the page's JSON-array
			// arguments into the callback's declared parameter types, call it,
			// and JSON-encode the return. A malformed body aborts here and routes
			// to the catch (reject status 1). bindOne already rejected an async
			// typed callback, so the return is synchronous.
			if typed {
				argVals, derr := e.decodeTypedBindArgs(cbTy, pos)
				if derr != nil {
					return derr
				}
				res, cerr := e.emitCBCall(cb, argVals)
				if cerr != nil {
					return cerr
				}
				e.emitInstr("call void @__kml_pop_jmpbuf()")
				// Async typed bind (TDD-00142 Stage 6): a typed callback returning
				// a Promise<T> settles the page promise with the JSON-encoded T at
				// settlement, via a per-inner-type runner.
				if cbTy.FuncRetType != nil && cbTy.FuncRetType.IsPromise {
					var innerTy Type
					if cbTy.FuncRetType.PromiseType != nil {
						innerTy = *cbTy.FuncRetType.PromiseType
					} else {
						innerTy = TypeVoid
					}
					runner := e.emitWebviewTypedReturnRunner(innerTy)
					e.emitWebviewAsyncReturn(res.Ref, runner)
					return nil
				}
				resultRef := ""
				if cbTy.FuncRetType == nil || cbTy.FuncRetType.IR == "void" {
					resultRef = e.internString("null")
				} else {
					strVal, serr := e.emitJSONStringifyValue(res, jsonIndent{})
					if serr != nil {
						return serr
					}
					resultRef = strVal.Ref
				}
				e.emitInstr(fmt.Sprintf("call void @webview_return(ptr %%w, ptr %%id, i32 0, ptr %s)", resultRef))
				return nil
			}

			res, cerr := e.emitCBCall(cb, []Value{{Ref: "%rstr", Ty: TypePtr}})
			if cerr != nil {
				return cerr
			}
			e.emitInstr("call void @__kml_pop_jmpbuf()")
			// Async bind (TDD-00142 Stage 3): a callback returning a Promise
			// resolves the page-side promise at *settlement*, not now — attach a
			// native reaction that fires webview_return when it settles (driven
			// on the GUI thread by the page-tick pump's microtask drain).
			if cbTy.FuncRetType != nil && cbTy.FuncRetType.IsPromise {
				e.ensureWebviewReturnRunner()
				e.emitWebviewAsyncReturn(res.Ref, "@__kml_wv_return_runner")
				return nil
			}
			resultRef := "null"
			if cbTy.FuncRetType != nil && cbTy.FuncRetType.IR == "ptr" {
				resultRef = res.Ref
			} else {
				// void / non-string return resolves with JSON null.
				nullS := e.internString("null")
				resultRef = nullS
			}
			e.emitInstr(fmt.Sprintf("call void @webview_return(ptr %%w, ptr %%id, i32 0, ptr %s)", resultRef))
			return nil
		},
		func(errPtr string) error {
			e.emitInstr(fmt.Sprintf("call void @webview_return(ptr %%w, ptr %%id, i32 1, ptr %s)", errJSON))
			return nil
		},
	)
	if err != nil {
		// A thunk-build error is a compiler bug in this path; surface it by
		// emitting nothing callable would be worse, so panic-free fallback:
		// leave the function body as-is (the outer emit already validated types).
		_ = err
	}
	e.emitInstr("ret void")

	// noinline optnone: this thunk calls setjmp (the fs-guard) and is re-entered
	// via longjmp when the user closure — or a nested fs-guard inside it — throws.
	// Under clang -O2 that setjmp handling is miscompiled once the module also
	// contains a bind whose closure calls an fs op (its own nested setjmp),
	// corrupting *every* bind's dispatch (a spurious longjmp makes an ordinary
	// call reject as "native handler threw"). Excluding the cold per-invocation
	// thunk from optimization sidesteps the miscompile (ADR-00523).
	e.functions.WriteString(fmt.Sprintf("\ndefine void %s(ptr %%id, ptr %%req, ptr %%env) noinline optnone {\nentry:\n", fn))
	e.functions.WriteString(e.allocas.String())
	e.functions.WriteString(e.body.String())
	e.functions.WriteString("}\n")
	return fn
}
