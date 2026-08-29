// emit_node_stream.go — TDD-00097 Stage 8 codegen: Node's `stream` module.
// `new Readable<T>(opts?)` / `new Writable<T>(opts?)` / `new Transform<I, O>
// (opts?)`, the `'data'`/`'end'`/`'error'`/`'close'`/`'finish'`/`'drain'`
// event surface (Stage 7-style literal-event typing), `push`/`write`/`end`/
// `pause`/`resume`/`.pipe()`, the web-stream bridges, and the
// `stream/promises` `pipeline`/`finished`.
package llvm

import (
	"fmt"
	"strings"

	"KlainMainLang/ast"
)

// emitNodeInvokeDataThunk emits `void @__kml_ns_invoke_N(ptr %clo, i64 %v0,
// i64 %v1)` — the runtime's typed 'data'-listener caller.
func (e *Emitter) emitNodeInvokeDataThunk(chunkTy Type) string {
	e.streamSiteCtr++
	fn := fmt.Sprintf("@__kml_ns_invoke_%d", e.streamSiteCtr)

	restore := e.beginThunkEmit()
	fp := e.freshReg()
	fpp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %%clo, i32 0, i32 0", fpp))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", fp, fpp))
	ep := e.freshReg()
	epp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %%clo, i32 0, i32 1", epp))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", ep, epp))
	if chunkTy.IsArray {
		cp := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %%v0 to ptr", cp))
		e.emitInstr(fmt.Sprintf("call void (ptr, ptr, i64) %s(ptr %s, ptr %s, i64 %%v1)", fp, ep, cp))
	} else {
		chunk := e.streamChunkFromWords("%v0", "%v1", chunkTy)
		e.emitInstr(fmt.Sprintf("call void (ptr, %s) %s(ptr %s, %s %s)", chunkTy.IR, fp, ep, chunkTy.IR, chunk.Ref))
	}
	e.emitInstr("ret void")
	body := e.allocas.String() + e.body.String()
	restore()

	e.functions.WriteString(fmt.Sprintf("\ndefine void %s(ptr %%clo, i64 %%v0, i64 %%v1) {\nentry:\n%s}\n", fn, body))
	return fn
}

// destructureNodeStreamOptions splits the options object literal.
func destructureNodeStreamOptions(opts ast.Expression, allowed map[string]bool, what string, pos ast.Pos) (map[string]ast.Expression, error) {
	out := map[string]ast.Expression{}
	if opts == nil {
		return out, nil
	}
	ol, ok := opts.(*ast.ObjectLiteral)
	if !ok {
		return nil, fmt.Errorf("%d:%d: %s's options must be an object literal", pos.Line, pos.Col, what)
	}
	for _, p := range ol.Properties {
		if !allowed[p.Key] {
			return nil, fmt.Errorf("%d:%d: unknown %s option '%s'", pos.Line, pos.Col, what, p.Key)
		}
		out[p.Key] = p.Value
	}
	return out, nil
}

// streamHWMOperand resolves a stream's `highWaterMark` option to a `double`
// LLVM operand (the value `__kml_rs_alloc`/`__kml_ws_alloc` already take as
// their queue watermark), defaulting to 1.0 when absent. `objectMode`, if
// present, is validated as a boolean and otherwise ignored: this compiler's
// chunks are already typed by the `<T>` argument (effectively always
// object-mode), so the flag has no representational effect — accepting it is
// pure Node-fidelity (TDD-00132). Both are consumed out of `opts`.
func (e *Emitter) streamHWMOperand(opts map[string]ast.Expression, pos ast.Pos) (string, error) {
	if omExpr, ok := opts["objectMode"]; ok {
		v, err := e.emitExpr(omExpr)
		if err != nil {
			return "", err
		}
		if v.Ty.IR != "i1" {
			return "", fmt.Errorf("%d:%d: a stream's objectMode option must be a boolean", pos.Line, pos.Col)
		}
	}
	hwmExpr, ok := opts["highWaterMark"]
	if !ok {
		return "1.0", nil
	}
	v, err := e.emitExpr(hwmExpr)
	if err != nil {
		return "", err
	}
	d := e.coerce(v, TypeF64)
	return d.Ref, nil
}

// nodeStreamOptionKeys is the shared allowed-option set every Node-stream
// constructor accepts on top of its own callbacks (TDD-00132): the queue
// watermark and the object-mode flag.
func nodeStreamOptionKeys(extra ...string) map[string]bool {
	m := map[string]bool{"highWaterMark": true, "objectMode": true}
	for _, k := range extra {
		m[k] = true
	}
	return m
}

// emitNewNodeStream implements the three constructors.
func (e *Emitter) emitNewNodeStream(ex *ast.NewNodeStreamExpression) (Value, error) {
	pos := ex.GetPos()
	e.ensureNodeStreamRuntime()

	// Every Node-stream constructor defaults to string chunks — Node's
	// non-objectMode streams carry strings/Buffers. The old `i64` default
	// (pre-ADR-00449) silently coerced a pushed string's pointer into the
	// numeric chunk and printed garbage; `<T>` still overrides for numeric/
	// typed-array chunk streams.
	inTy, outTy := TypePtr, TypePtr
	if ex.InType != nil {
		inTy = e.resolveType(ex.InType)
	}
	if ex.OutType != nil {
		outTy = e.resolveType(ex.OutType)
	}

	switch ex.Kind {
	case "readable":
		opts, err := destructureNodeStreamOptions(ex.Options, nodeStreamOptionKeys("read"), "Readable", pos)
		if err != nil {
			return Value{}, err
		}
		hwm, err := e.streamHWMOperand(opts, pos)
		if err != nil {
			return Value{}, err
		}
		fulfill := e.emitStreamFulfillThunk(outTy)
		rs := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_rs_alloc(double %s, ptr %s)", rs, hwm, fulfill))
		inv := e.emitNodeInvokeDataThunk(outTy)
		dec := e.emitStreamDecodeThunk(outTy)
		nsTy := NodeReadableType(outTy)
		nsReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_ns_alloc(ptr %s, ptr null, ptr %s, ptr %s)", nsReg, rs, inv, dec))
		if readExpr, ok := opts["read"]; ok {
			// The read callback receives the Readable itself (this compiler
			// has no `this` binding for object-literal callbacks — a
			// documented deviation from Node's this-based `read()`), so it
			// can `r.push(...)`.
			userClo, err := e.streamCallbackClosure(readExpr, []Type{nsTy}, "read callback", pos)
			if err != nil {
				return Value{}, err
			}
			if len(userClo.Ty.FuncParams) > 1 {
				return Value{}, fmt.Errorf("%d:%d: a Readable read callback takes at most one (stream) parameter", pos.Line, pos.Col)
			}
			wrap := e.emitStreamPullWrap(userClo.Ty)
			env := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", env))
			g0 := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 0", g0, env))
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", userClo.Ref, g0))
			g1 := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 1", g1, env))
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", nsReg, g1))
			e.storeStreamField(rs, 9, e.buildBuiltinClosure(wrap, env))
		}
		e.emitInstr(fmt.Sprintf("call void @__kml_microtask_enqueue(ptr %s)", e.buildBuiltinClosure("@__kml_rs_started", rs)))
		return Value{Ref: nsReg, Ty: nsTy}, nil

	case "writable":
		opts, err := destructureNodeStreamOptions(ex.Options, nodeStreamOptionKeys("write", "final"), "Writable", pos)
		if err != nil {
			return Value{}, err
		}
		hwm, err := e.streamHWMOperand(opts, pos)
		if err != nil {
			return Value{}, err
		}
		ws := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_ws_alloc(double %s)", ws, hwm))
		if writeExpr, ok := opts["write"]; ok {
			userClo, err := e.streamCallbackClosure(writeExpr, []Type{inTy}, "write callback", pos)
			if err != nil {
				return Value{}, err
			}
			if len(userClo.Ty.FuncParams) > 1 {
				return Value{}, fmt.Errorf("%d:%d: a Writable write callback takes one (chunk) parameter", pos.Line, pos.Col)
			}
			wrap := e.emitStreamWriteWrap(userClo.Ty, inTy)
			env := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", env))
			g0 := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 0", g0, env))
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", userClo.Ref, g0))
			g1 := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 1", g1, env))
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", ws, g1))
			e.storeWStreamField(ws, 9, e.buildBuiltinClosure(wrap, env))
		}
		if finalExpr, ok := opts["final"]; ok {
			userClo, err := e.streamCallbackClosure(finalExpr, nil, "final callback", pos)
			if err != nil {
				return Value{}, err
			}
			if len(userClo.Ty.FuncParams) > 0 {
				return Value{}, fmt.Errorf("%d:%d: a Writable final callback takes no parameters", pos.Line, pos.Col)
			}
			e.storeWStreamField(ws, 10, e.buildBuiltinClosure(e.emitStreamCloseWrap(userClo.Ty), userClo.Ref))
		}
		e.emitInstr(fmt.Sprintf("call void @__kml_microtask_enqueue(ptr %s)", e.buildBuiltinClosure("@__kml_ws_started", ws)))
		nsTy := NodeWritableType(inTy)
		nsReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_ns_alloc(ptr null, ptr %s, ptr null, ptr null)", nsReg, ws))
		e.emitInstr(fmt.Sprintf("call void @__kml_ns_arm_writable(ptr %s)", nsReg))
		return Value{Ref: nsReg, Ty: nsTy}, nil

	case "duplex":
		// new Duplex({read, write, final}): two *independent* sides on one
		// handle (ADR-00493) — a Readable whose read callback pushes, plus a
		// Writable whose write/final callbacks consume — unlike Transform,
		// where the writable side feeds the readable one. Exactly Node's
		// contract: Duplex implements both, with separate internal states.
		opts, err := destructureNodeStreamOptions(ex.Options, nodeStreamOptionKeys("read", "write", "final"), "Duplex", pos)
		if err != nil {
			return Value{}, err
		}
		hwm, err := e.streamHWMOperand(opts, pos)
		if err != nil {
			return Value{}, err
		}
		fulfill := e.emitStreamFulfillThunk(outTy)
		rs := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_rs_alloc(double %s, ptr %s)", rs, hwm, fulfill))
		ws := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_ws_alloc(double %s)", ws, hwm))
		inv := e.emitNodeInvokeDataThunk(outTy)
		dec := e.emitStreamDecodeThunk(outTy)
		nsTy := NodeTransformType(inTy, outTy)
		nsReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_ns_alloc(ptr %s, ptr %s, ptr %s, ptr %s)", nsReg, rs, ws, inv, dec))
		if readExpr, ok := opts["read"]; ok {
			userClo, err := e.streamCallbackClosure(readExpr, []Type{nsTy}, "read callback", pos)
			if err != nil {
				return Value{}, err
			}
			if len(userClo.Ty.FuncParams) > 1 {
				return Value{}, fmt.Errorf("%d:%d: a Duplex read callback takes at most one (stream) parameter", pos.Line, pos.Col)
			}
			wrap := e.emitStreamPullWrap(userClo.Ty)
			env := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", env))
			g0 := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 0", g0, env))
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", userClo.Ref, g0))
			g1 := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 1", g1, env))
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", nsReg, g1))
			e.storeStreamField(rs, 9, e.buildBuiltinClosure(wrap, env))
		}
		if writeExpr, ok := opts["write"]; ok {
			userClo, err := e.streamCallbackClosure(writeExpr, []Type{inTy}, "write callback", pos)
			if err != nil {
				return Value{}, err
			}
			if len(userClo.Ty.FuncParams) > 1 {
				return Value{}, fmt.Errorf("%d:%d: a Duplex write callback takes one (chunk) parameter", pos.Line, pos.Col)
			}
			wrap := e.emitStreamWriteWrap(userClo.Ty, inTy)
			env := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", env))
			g0 := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 0", g0, env))
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", userClo.Ref, g0))
			g1 := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 1", g1, env))
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", ws, g1))
			e.storeWStreamField(ws, 9, e.buildBuiltinClosure(wrap, env))
		}
		if finalExpr, ok := opts["final"]; ok {
			userClo, err := e.streamCallbackClosure(finalExpr, nil, "final callback", pos)
			if err != nil {
				return Value{}, err
			}
			if len(userClo.Ty.FuncParams) > 0 {
				return Value{}, fmt.Errorf("%d:%d: a Duplex final callback takes no parameters", pos.Line, pos.Col)
			}
			e.storeWStreamField(ws, 10, e.buildBuiltinClosure(e.emitStreamCloseWrap(userClo.Ty), userClo.Ref))
		}
		e.emitInstr(fmt.Sprintf("call void @__kml_microtask_enqueue(ptr %s)", e.buildBuiltinClosure("@__kml_rs_started", rs)))
		e.emitInstr(fmt.Sprintf("call void @__kml_microtask_enqueue(ptr %s)", e.buildBuiltinClosure("@__kml_ws_started", ws)))
		e.emitInstr(fmt.Sprintf("call void @__kml_ns_arm_writable(ptr %s)", nsReg))
		return Value{Ref: nsReg, Ty: nsTy}, nil

	case "transform", "passthrough":
		// PassThrough is Node's identity Transform: same wiring, no
		// transform/flush callbacks allowed (there is nothing to customize).
		optKeys, what := nodeStreamOptionKeys("transform", "flush"), "Transform"
		if ex.Kind == "passthrough" {
			optKeys, what = nodeStreamOptionKeys(), "PassThrough"
		}
		opts, err := destructureNodeStreamOptions(ex.Options, optKeys, what, pos)
		if err != nil {
			return Value{}, err
		}
		hwm, err := e.streamHWMOperand(opts, pos)
		if err != nil {
			return Value{}, err
		}
		fulfill := e.emitStreamFulfillThunk(outTy)
		rs := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_rs_alloc(double 0.0, ptr %s)", rs, fulfill))
		ws := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_ws_alloc(double %s)", ws, hwm))

		ctx := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 72)", ctx))
		storeCtxPtr := func(idx int, val string) {
			gep := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %d", gep, ctx, idx))
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", val, gep))
		}
		storeCtxPtr(0, rs)
		storeCtxPtr(1, ws)
		rsCtrlTy := RSControllerType(outTy)
		if trExpr, ok := opts["transform"]; ok {
			userClo, err := e.streamCallbackClosure(trExpr, []Type{inTy, rsCtrlTy}, "transform callback", pos)
			if err != nil {
				return Value{}, err
			}
			if len(userClo.Ty.FuncParams) > 2 {
				return Value{}, fmt.Errorf("%d:%d: a transform callback takes (chunk, controller?)", pos.Line, pos.Col)
			}
			wrap := e.emitStreamWriteWrap(userClo.Ty, inTy)
			env := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", env))
			g0 := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 0", g0, env))
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", userClo.Ref, g0))
			g1 := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 1", g1, env))
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", rs, g1))
			storeCtxPtr(2, e.buildBuiltinClosure(wrap, env))
		} else {
			if inTy.IR != outTy.IR || inTy.IsArray != outTy.IsArray {
				return Value{}, fmt.Errorf("%d:%d: an identity Transform (no transform callback) requires matching chunk types", pos.Line, pos.Col)
			}
			storeCtxPtr(2, "null")
		}
		if flExpr, ok := opts["flush"]; ok {
			userClo, err := e.streamCallbackClosure(flExpr, []Type{rsCtrlTy}, "flush callback", pos)
			if err != nil {
				return Value{}, err
			}
			if len(userClo.Ty.FuncParams) > 1 {
				return Value{}, fmt.Errorf("%d:%d: a flush callback takes at most one (controller) parameter", pos.Line, pos.Col)
			}
			wrap := e.emitStreamPullWrap(userClo.Ty)
			env := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", env))
			g0 := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 0", g0, env))
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", userClo.Ref, g0))
			g1 := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 1", g1, env))
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", rs, g1))
			storeCtxPtr(3, e.buildBuiltinClosure(wrap, env))
		} else {
			storeCtxPtr(3, "null")
		}
		for i := 4; i <= 6; i++ {
			gep := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %s, i64 %d", gep, ctx, i))
			e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", gep))
		}
		storeCtxPtr(7, "null")
		storeCtxPtr(8, "null")
		e.storeWStreamField(ws, 9, e.buildBuiltinClosure("@__kml_ts_sink_write", ctx))
		e.storeWStreamField(ws, 10, e.buildBuiltinClosure("@__kml_ts_sink_close", ctx))
		e.storeWStreamField(ws, 11, e.buildBuiltinClosure("@__kml_ts_sink_abort", ctx))
		e.storeStreamField(rs, 9, e.buildBuiltinClosure("@__kml_ts_pull", ctx))
		e.emitInstr(fmt.Sprintf("call void @__kml_microtask_enqueue(ptr %s)", e.buildBuiltinClosure("@__kml_rs_started", rs)))
		e.emitInstr(fmt.Sprintf("call void @__kml_microtask_enqueue(ptr %s)", e.buildBuiltinClosure("@__kml_ws_started", ws)))

		inv := e.emitNodeInvokeDataThunk(outTy)
		dec := e.emitStreamDecodeThunk(outTy)
		nsReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_ns_alloc(ptr %s, ptr %s, ptr %s, ptr %s)", nsReg, rs, ws, inv, dec))
		e.emitInstr(fmt.Sprintf("call void @__kml_ns_arm_writable(ptr %s)", nsReg))
		return Value{Ref: nsReg, Ty: NodeTransformType(inTy, outTy)}, nil
	}
	return Value{}, fmt.Errorf("%d:%d: unknown node stream kind", pos.Line, pos.Col)
}

// nodeStreamSide loads the inner rstream (side 0) or wstream (side 1).
func (e *Emitter) nodeStreamSide(nsPtr string, side int) string {
	gep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, nodeStreamStructIR, nsPtr, side))
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", r, gep))
	return r
}

// resolveNodeStreamForCall resolves a node-stream receiver.
func (e *Emitter) resolveNodeStreamForCall(objExpr ast.Expression, pos ast.Pos) (Type, string, error) {
	if id, ok := objExpr.(*ast.Identifier); ok {
		sym, found := e.lookup(id.Name)
		if found && (sym.Ty.IsNodeReadable || sym.Ty.IsNodeWritable) {
			ptr := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", ptr, sym.Ptr))
			return sym.Ty, ptr, nil
		}
	}
	val, err := e.emitExpr(objExpr)
	if err != nil {
		return Type{}, "", err
	}
	if !val.Ty.IsNodeReadable && !val.Ty.IsNodeWritable {
		return Type{}, "", fmt.Errorf("%d:%d: value is not a Node stream", pos.Line, pos.Col)
	}
	return val.Ty, val.Ref, nil
}

// nodeEventPayload maps a literal event name to its payload for ty.
func nodeEventPayload(ty Type, event string, pos ast.Pos) (Type, bool, error) {
	switch event {
	case "data":
		if !ty.IsNodeReadable {
			return Type{}, false, fmt.Errorf("%d:%d: 'data' is a readable-side event", pos.Line, pos.Col)
		}
		out := TypeI64
		if ty.StreamOut != nil {
			out = *ty.StreamOut
		}
		return out, false, nil
	case "error":
		return errorObjType, false, nil
	case "end":
		if !ty.IsNodeReadable {
			return Type{}, false, fmt.Errorf("%d:%d: 'end' is a readable-side event", pos.Line, pos.Col)
		}
		return Type{}, true, nil
	case "finish", "drain":
		if !ty.IsNodeWritable {
			return Type{}, false, fmt.Errorf("%d:%d: '%s' is a writable-side event", pos.Line, pos.Col, event)
		}
		return Type{}, true, nil
	case "close":
		return Type{}, true, nil
	}
	return Type{}, false, fmt.Errorf("%d:%d: unsupported stream event '%s' (data, end, error, close, finish, drain)", pos.Line, pos.Col, event)
}

// nodeStreamMethodNames are the stream methods hand-dispatched by name
// (emit_call.go) against a Node-stream handle — reserved as class method
// names for a `class X extends Readable/Writable` (TDD-00132), unlike the
// `_read`/`_write`/`_transform` override hooks which are ordinary methods.
var nodeStreamMethodNames = map[string]bool{
	"on": true, "once": true, "push": true, "pause": true,
	"resume": true, "write": true, "end": true, "pipe": true,
	"destroy": true, "setEncoding": true, "read": true, "unshift": true,
}

func isNodeStreamMethodName(name string) bool { return nodeStreamMethodNames[name] }

// emitNodeStreamCall dispatches node-stream method calls.
func (e *Emitter) emitNodeStreamCall(objExpr ast.Expression, method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	ty, ptr, err := e.resolveNodeStreamForCall(objExpr, pos)
	if err != nil {
		return Value{}, err
	}
	return e.emitNodeStreamCallOn(ty, ptr, method, args, pos)
}

// emitNodeStreamCallOn is the dispatch core given an already-resolved
// node-stream handle (ptr) and its node-stream Type (ty). The receiver-
// resolution split lets a `class X extends Readable` instance (TDD-00132)
// reuse the same dispatch: it loads the hidden handle field, synthesizes a
// NodeReadable/Writable ty, and calls here directly.
func (e *Emitter) emitNodeStreamCallOn(ty Type, ptr string, method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	e.ensureNodeStreamRuntime()
	self := Value{Ref: ptr, Ty: ty}
	outTy := TypeI64
	if ty.StreamOut != nil {
		outTy = *ty.StreamOut
	}
	inTy := TypeI64
	if ty.StreamChunk != nil {
		inTy = *ty.StreamChunk
	}

	switch method {
	case "on", "once":
		if len(args) != 2 {
			return Value{}, fmt.Errorf("%d:%d: %s() requires (event, listener)", pos.Line, pos.Col, method)
		}
		lit, ok := args[0].(*ast.StringLiteral)
		if !ok {
			return Value{}, fmt.Errorf("%d:%d: a stream's %s() requires a string-literal event name", pos.Line, pos.Col, method)
		}
		evTy, evVoid, err := nodeEventPayload(ty, lit.Value, pos)
		if err != nil {
			return Value{}, err
		}
		listenerPtr, err := e.resolveEventEmitterListenerArg(args[1], evTy, evVoid, method, pos)
		if err != nil {
			return Value{}, err
		}
		once := "0"
		if method == "once" {
			once = "1"
		}
		e.emitInstr(fmt.Sprintf("call void @__kml_ns_add_listener(ptr %s, ptr %s, ptr %s, i64 %s)", ptr, e.internString(lit.Value), listenerPtr, once))
		if lit.Value == "data" {
			e.emitInstr(fmt.Sprintf("call void @__kml_ns_start_flow(ptr %s)", ptr))
		}
		return self, nil

	case "push":
		if !ty.IsNodeReadable {
			return Value{}, fmt.Errorf("%d:%d: push() requires a Readable", pos.Line, pos.Col)
		}
		if len(args) != 1 {
			return Value{}, fmt.Errorf("%d:%d: push() takes one argument (a chunk, or null to end)", pos.Line, pos.Col)
		}
		rs := e.nodeStreamSide(ptr, 0)
		if _, isNull := args[0].(*ast.NullLiteral); isNull {
			closed := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_rs_close(ptr %s)", closed, rs))
			return Value{Ref: "0", Ty: TypeBool}, nil
		}
		cv, err := e.emitExprWithObjectHint(args[0], outTy)
		if err != nil {
			return Value{}, err
		}
		cv = e.coerce(cv, outTy)
		v0, v1 := e.streamChunkWords(cv)
		okR := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_rs_enqueue(ptr %s, i64 %s, i64 %s)", okR, rs, v0, v1))
		d := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call double @__kml_rs_desired(ptr %s)", d, rs))
		b := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = fcmp ogt double %s, 0.0", b, d))
		return Value{Ref: b, Ty: TypeBool}, nil

	case "pause":
		rs := e.nodeStreamSide(ptr, 0)
		_ = rs
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 3", gep, nodeStreamStructIR, ptr))
		e.emitInstr(fmt.Sprintf("store i64 2, ptr %s, align 8", gep))
		return self, nil

	case "resume":
		e.emitInstr(fmt.Sprintf("call void @__kml_ns_resume(ptr %s)", ptr))
		return self, nil

	case "write":
		if !ty.IsNodeWritable {
			return Value{}, fmt.Errorf("%d:%d: write() requires a Writable", pos.Line, pos.Col)
		}
		if len(args) != 1 {
			return Value{}, fmt.Errorf("%d:%d: write() takes one chunk argument", pos.Line, pos.Col)
		}
		ws := e.nodeStreamSide(ptr, 1)
		cv, err := e.emitExprWithObjectHint(args[0], inTy)
		if err != nil {
			return Value{}, err
		}
		cv = e.coerce(cv, inTy)
		v0, v1 := e.streamChunkWords(cv)
		prom := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_ws_write(ptr %s, i64 %s, i64 %s)", prom, ws, v0, v1))
		isNull := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", isNull, prom))
		okI := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = zext i1 %s to i64", okI, isNull))
		e.streamThrowTypeError(okI, "cannot write to an ended Writable")
		bI := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_ns_write_done(ptr %s, ptr %s)", bI, ptr, ws))
		b := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", b, bI))
		return Value{Ref: b, Ty: TypeBool}, nil

	case "end":
		if !ty.IsNodeWritable {
			return Value{}, fmt.Errorf("%d:%d: end() requires a Writable", pos.Line, pos.Col)
		}
		if len(args) > 1 {
			return Value{}, fmt.Errorf("%d:%d: end() takes at most one final chunk", pos.Line, pos.Col)
		}
		ws := e.nodeStreamSide(ptr, 1)
		if len(args) == 1 {
			cv, err := e.emitExprWithObjectHint(args[0], inTy)
			if err != nil {
				return Value{}, err
			}
			cv = e.coerce(cv, inTy)
			v0, v1 := e.streamChunkWords(cv)
			ign := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_ws_write(ptr %s, i64 %s, i64 %s)", ign, ws, v0, v1))
		}
		closed := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_ws_close(ptr %s)", closed, ws))
		return self, nil

	case "destroy":
		// destroy([error]) tears the stream down (ADR-00483): the readable
		// side closes its source queue, the writable side closes its sink.
		// The optional error argument is evaluated (for side effects) and
		// dropped — 'error'-event re-emission isn't modeled.
		if len(args) > 1 {
			return Value{}, fmt.Errorf("%d:%d: destroy() takes at most one error argument", pos.Line, pos.Col)
		}
		if len(args) == 1 {
			if _, err := e.emitExpr(args[0]); err != nil {
				return Value{}, err
			}
		}
		if ty.IsNodeReadable {
			rs := e.nodeStreamSide(ptr, 0)
			cl := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_rs_close(ptr %s)", cl, rs))
			// A non-flowing readable has no flow loop to emit 'close' —
			// queue the guarded direct emission (async, Node's order).
			e.emitInstr(fmt.Sprintf("call void @__kml_microtask_enqueue(ptr %s)", e.buildBuiltinClosure("@__kml_ns_destroy_close", ptr)))
		}
		if ty.IsNodeWritable {
			ws := e.nodeStreamSide(ptr, 1)
			cl := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_ws_close(ptr %s)", cl, ws))
		}
		return self, nil

	case "unshift":
		// Push a chunk back onto the FRONT of the queue (ADR-00485) — the
		// standard peek-then-put-back pairing with read().
		if !ty.IsNodeReadable {
			return Value{}, fmt.Errorf("%d:%d: unshift() requires a Readable", pos.Line, pos.Col)
		}
		if len(args) != 1 {
			return Value{}, fmt.Errorf("%d:%d: unshift() takes one chunk argument", pos.Line, pos.Col)
		}
		rsSide := e.nodeStreamSide(ptr, 0)
		cvU, err := e.emitExprWithObjectHint(args[0], outTy)
		if err != nil {
			return Value{}, err
		}
		cvU = e.coerce(cvU, outTy)
		u0, u1 := e.streamChunkWords(cvU)
		e.emitInstr(fmt.Sprintf("call void @__kml_rs_qunshift(ptr %s, i64 %s, i64 %s, double 1.0)", rsSide, u0, u1))
		return self, nil

	case "read":
		// Synchronous read (ADR-00484): one queued chunk, or null (a
		// scalar-chunk stream yields the zero stand-in) when the queue is
		// empty. A `size` argument is evaluated and ignored (chunks are
		// whole pushes here, not a byte buffer).
		if !ty.IsNodeReadable {
			return Value{}, fmt.Errorf("%d:%d: read() requires a Readable", pos.Line, pos.Col)
		}
		if len(args) > 1 {
			return Value{}, fmt.Errorf("%d:%d: read() takes at most one size argument", pos.Line, pos.Col)
		}
		if len(args) == 1 {
			if _, err := e.emitExpr(args[0]); err != nil {
				return Value{}, err
			}
		}
		rs := e.nodeStreamSide(ptr, 0)
		trip := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call { i64, i64, i64 } @__kml_rs_tryread(ptr %s)", trip, rs))
		has := e.freshReg()
		v0 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = extractvalue { i64, i64, i64 } %s, 0", has, trip))
		e.emitInstr(fmt.Sprintf("%s = extractvalue { i64, i64, i64 } %s, 1", v0, trip))
		hasB := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", hasB, has))
		switch {
		case outTy.IR == "ptr":
			p0 := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", p0, v0))
			r := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr null", r, hasB, p0))
			return Value{Ref: r, Ty: outTy}, nil
		case outTy.IR == "double":
			d := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = bitcast i64 %s to double", d, v0))
			r := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = select i1 %s, double %s, double 0.0", r, hasB, d))
			return Value{Ref: r, Ty: outTy}, nil
		default:
			r := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 0", r, hasB, v0))
			return Value{Ref: r, Ty: outTy}, nil
		}

	case "setEncoding":
		// Chunks are already strings by default (ADR-00449), so
		// setEncoding('utf8') is an accepted no-op; any other encoding is a
		// clean rejection (ADR-00483).
		if len(args) == 1 {
			if sl, ok := args[0].(*ast.StringLiteral); ok && (sl.Value == "utf8" || sl.Value == "utf-8") {
				return self, nil
			}
		}
		return Value{}, fmt.Errorf("%d:%d: setEncoding supports only 'utf8' (chunks are strings already)", pos.Line, pos.Col)

	case "pipe":
		if !ty.IsNodeReadable {
			return Value{}, fmt.Errorf("%d:%d: pipe() requires a Readable source", pos.Line, pos.Col)
		}
		if len(args) != 1 {
			return Value{}, fmt.Errorf("%d:%d: pipe() takes one destination", pos.Line, pos.Col)
		}
		dv, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		dv = e.asNodeStreamValue(dv)
		if !dv.Ty.IsNodeWritable {
			return Value{}, fmt.Errorf("%d:%d: pipe()'s destination must be a Writable (or Transform)", pos.Line, pos.Col)
		}
		e.ensureStreamPipeRuntime()
		rs := e.nodeStreamSide(ptr, 0)
		dws := e.nodeStreamSide(dv.Ref, 1)
		decode := e.emitStreamDecodeThunk(outTy)
		// preventClose stays 0: source end closes the destination, which is
		// Node's default .pipe() behavior too.
		ign := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_pipe_to(ptr %s, ptr %s, ptr %s, i64 0, ptr null, ptr null)", ign, rs, dws, decode))
		return dv, nil

	case "toWeb":
		if ty.IsNodeReadable && !ty.IsNodeWritable {
			rs := e.nodeStreamSide(ptr, 0)
			return Value{Ref: rs, Ty: ReadableStreamType(outTy)}, nil
		}
		if ty.IsNodeWritable && !ty.IsNodeReadable {
			ws := e.nodeStreamSide(ptr, 1)
			return Value{Ref: ws, Ty: WritableStreamType(inTy)}, nil
		}
		return Value{}, fmt.Errorf("%d:%d: toWeb() is supported on a plain Readable or Writable", pos.Line, pos.Col)
	}
	return Value{}, fmt.Errorf("%d:%d: unknown stream method '%s'", pos.Line, pos.Col, method)
}

// emitNodeReadableStatic handles `Readable.from(...)` / `Readable.fromWeb(...)`.
func (e *Emitter) emitNodeReadableStatic(method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	e.ensureNodeStreamRuntime()
	switch method {
	case "from":
		wv, err := e.emitReadableStreamFrom(args, pos)
		if err != nil {
			return Value{}, err
		}
		return e.wrapWebReadable(wv)
	case "fromWeb":
		if len(args) != 1 {
			return Value{}, fmt.Errorf("%d:%d: Readable.fromWeb() takes one ReadableStream", pos.Line, pos.Col)
		}
		wv, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		if !wv.Ty.IsReadableStream {
			return Value{}, fmt.Errorf("%d:%d: Readable.fromWeb() requires a ReadableStream", pos.Line, pos.Col)
		}
		return e.wrapWebReadable(wv)
	}
	return Value{}, fmt.Errorf("%d:%d: unknown Readable static '%s'", pos.Line, pos.Col, method)
}

// wrapWebReadable wraps a WHATWG readable value into a Node Readable.
func (e *Emitter) wrapWebReadable(wv Value) (Value, error) {
	outTy := TypeI64
	if wv.Ty.StreamChunk != nil {
		outTy = *wv.Ty.StreamChunk
	}
	inv := e.emitNodeInvokeDataThunk(outTy)
	dec := e.emitStreamDecodeThunk(outTy)
	nsReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_ns_alloc(ptr %s, ptr null, ptr %s, ptr %s)", nsReg, wv.Ref, inv, dec))
	return Value{Ref: nsReg, Ty: NodeReadableType(outTy)}, nil
}

// emitStreamPromisesCall handles `pipeline(...)` / `finished(...)` from
// 'stream/promises'.
func (e *Emitter) emitStreamPromisesCall(method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	e.ensureNodeStreamRuntime()
	voidPromise := func(ref string) Value {
		pt := PromiseOf(TypeVoid)
		pt.PromiseTask = true
		return Value{Ref: ref, Ty: pt}
	}
	closedPromOf := func(v Value) (string, error) { return e.streamClosedProm(v, pos) }

	switch method {
	case "finished":
		if len(args) != 1 {
			return Value{}, fmt.Errorf("%d:%d: finished() takes one stream", pos.Line, pos.Col)
		}
		v, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		v = e.asNodeStreamValue(v)
		cp, err := closedPromOf(v)
		if err != nil {
			return Value{}, err
		}
		return voidPromise(cp), nil

	case "pipeline":
		last, err := e.emitStreamPipelineWire(args, pos)
		if err != nil {
			return Value{}, err
		}
		cp, err := closedPromOf(last)
		if err != nil {
			return Value{}, err
		}
		return voidPromise(cp), nil
	}
	return Value{}, fmt.Errorf("%d:%d: unknown stream/promises member '%s'", pos.Line, pos.Col, method)
}

// streamClosedProm loads the stream's completion promise: a Writable's closed
// promise (wstream field 14) or a Readable's (rstream field 16) — what
// finished()/pipeline() settle on, in both the Promise and callback forms.
func (e *Emitter) streamClosedProm(v Value, pos ast.Pos) (string, error) {
	if v.Ty.IsNodeWritable {
		ws := e.nodeStreamSide(v.Ref, 1)
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 14", gep, wstreamStructIR, ws))
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", r, gep))
		return r, nil
	}
	if v.Ty.IsNodeReadable {
		rs := e.nodeStreamSide(v.Ref, 0)
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 16", gep, rstreamStructIR, rs))
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", r, gep))
		return r, nil
	}
	return "", fmt.Errorf("%d:%d: expected a Node stream", pos.Line, pos.Col)
}

// emitStreamPipelineWire evaluates pipeline()'s stream arguments, validates
// the readable→writable chain, wires each adjacent pair with __kml_pipe_to,
// and returns the final destination's Value. Shared by the Promise form
// ('stream/promises') and the callback form ('stream').
func (e *Emitter) emitStreamPipelineWire(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 2 {
		return Value{}, fmt.Errorf("%d:%d: pipeline() takes at least a source and a destination", pos.Line, pos.Col)
	}
	vals := make([]Value, len(args))
	for i, a := range args {
		v, err := e.emitExpr(a)
		if err != nil {
			return Value{}, err
		}
		v = e.asNodeStreamValue(v)
		if !v.Ty.IsNodeReadable && !v.Ty.IsNodeWritable {
			return Value{}, fmt.Errorf("%d:%d: pipeline() arguments must be Node streams", pos.Line, pos.Col)
		}
		vals[i] = v
	}
	e.ensureStreamPipeRuntime()
	for i := 0; i < len(vals)-1; i++ {
		src, dst := vals[i], vals[i+1]
		if !src.Ty.IsNodeReadable {
			return Value{}, fmt.Errorf("%d:%d: pipeline() stage %d must be readable", pos.Line, pos.Col, i+1)
		}
		if !dst.Ty.IsNodeWritable {
			return Value{}, fmt.Errorf("%d:%d: pipeline() stage %d must be writable", pos.Line, pos.Col, i+2)
		}
		outTy := TypeI64
		if src.Ty.StreamOut != nil {
			outTy = *src.Ty.StreamOut
		}
		rs := e.nodeStreamSide(src.Ref, 0)
		dws := e.nodeStreamSide(dst.Ref, 1)
		decode := e.emitStreamDecodeThunk(outTy)
		ign := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_pipe_to(ptr %s, ptr %s, ptr %s, i64 0, ptr null, ptr null)", ign, rs, dws, decode))
	}
	return vals[len(vals)-1], nil
}

// emitStreamFinishedReaction attaches the classic Node completion callback
// (`(err?) => …`) to a stream's closed promise: a per-site runner fires on
// settlement and calls the callback with null on fulfill / the error on
// reject. The callback may declare zero parameters or one (the error).
func (e *Emitter) emitStreamFinishedReaction(promRef string, cbExpr ast.Expression, what string, pos ast.Pos) error {
	e.ensurePromiseRuntime()
	e.ensureMicrotasks()
	cb, err := e.resolveCallbackWithHints(cbExpr, []Type{errorObjType})
	if err != nil {
		return fmt.Errorf("%d:%d: %s's callback: %v", pos.Line, pos.Col, what, err)
	}
	nparams := len(cb.paramTypes())
	if nparams > 1 {
		return fmt.Errorf("%d:%d: %s's callback takes at most one (error) parameter", pos.Line, pos.Col, what)
	}

	// Per-site runner: env = { ptr prom, ptr cloOrNull }.
	e.streamSiteCtr++
	runner := fmt.Sprintf("@__kml_sfin_run_%d", e.streamSiteCtr)
	errArg := ""
	if nparams == 1 {
		errArg = ", ptr %errp"
	}
	var callIR string
	switch cb.kind {
	case cbClosure:
		callIR = "  %fp_p = getelementptr { ptr, ptr }, ptr %clo, i32 0, i32 0\n" +
			"  %fp = load ptr, ptr %fp_p, align 8\n" +
			"  %ep_p = getelementptr { ptr, ptr }, ptr %clo, i32 0, i32 1\n" +
			"  %ep = load ptr, ptr %ep_p, align 8\n" +
			fmt.Sprintf("  call void %%fp(ptr %%ep%s)\n", errArg)
	case cbNamed:
		callIR = fmt.Sprintf("  call void @%s(%s)\n", cb.name, strings.TrimPrefix(errArg, ", "))
	default:
		return fmt.Errorf("%d:%d: %s's callback must be a function", pos.Line, pos.Col, what)
	}
	e.emitGlobal(fmt.Sprintf(`
define void %s(ptr %%env) {
entry:
  %%p_p = getelementptr { ptr, ptr }, ptr %%env, i32 0, i32 0
  %%p = load ptr, ptr %%p_p, align 8
  %%clo_p = getelementptr { ptr, ptr }, ptr %%env, i32 0, i32 1
  %%clo = load ptr, ptr %%clo_p, align 8
  %%res_p = getelementptr %s, ptr %%p, i32 0, i32 0
  %%res = load i64, ptr %%res_p, align 8
  %%v0_p = getelementptr %s, ptr %%p, i32 0, i32 2
  %%v0 = load i64, ptr %%v0_p, align 8
  %%isrej = icmp eq i64 %%res, 2
  %%e0 = inttoptr i64 %%v0 to ptr
  %%errp = select i1 %%isrej, ptr %%e0, ptr null
%s  ret void
}`, runner, promiseStructIR, promiseStructIR, callIR))

	cloEnv := "null"
	if cb.kind == cbClosure {
		cloEnv = cb.hdrPtr
	}
	env := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", env))
	g0 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 0", g0, env))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", promRef, g0))
	g1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 1", g1, env))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", cloEnv, g1))
	e.emitAttachPromiseReaction(promRef, e.buildBuiltinClosure(runner, env))
	return nil
}

// emitStreamModuleCall dispatches the 'stream' module's function members —
// the callback forms of finished()/pipeline() (their Promise twins live in
// 'stream/promises') and duplexPair().
func (e *Emitter) emitStreamModuleCall(method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	e.ensureNodeStreamRuntime()
	switch method {
	case "finished":
		if len(args) != 2 {
			return Value{}, fmt.Errorf("%d:%d: finished() takes a stream and a callback", pos.Line, pos.Col)
		}
		v, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		v = e.asNodeStreamValue(v)
		cp, err := e.streamClosedProm(v, pos)
		if err != nil {
			return Value{}, err
		}
		if err := e.emitStreamFinishedReaction(cp, args[1], "finished()", pos); err != nil {
			return Value{}, err
		}
		return Value{Ref: "0", Ty: TypeVoid}, nil

	case "pipeline":
		if len(args) < 3 {
			return Value{}, fmt.Errorf("%d:%d: pipeline() takes at least a source, a destination, and a callback", pos.Line, pos.Col)
		}
		last, err := e.emitStreamPipelineWire(args[:len(args)-1], pos)
		if err != nil {
			return Value{}, err
		}
		cp, err := e.streamClosedProm(last, pos)
		if err != nil {
			return Value{}, err
		}
		if err := e.emitStreamFinishedReaction(cp, args[len(args)-1], "pipeline()", pos); err != nil {
			return Value{}, err
		}
		return last, nil

	case "duplexPair":
		if len(args) != 0 {
			return Value{}, fmt.Errorf("%d:%d: duplexPair() takes no arguments", pos.Line, pos.Col)
		}
		return e.emitDuplexPair(pos)
	}
	return Value{}, fmt.Errorf("%d:%d: unknown stream member '%s'", pos.Line, pos.Col, method)
}

// emitDuplexPair builds stream.duplexPair(): two cross-wired Duplex handles —
// whatever is written to one side comes out as 'data' on the other, and
// ending one side ends the other's readable. Each side is the existing
// identity-transform wiring (a TSCTX whose sink enqueues into the *other*
// side's rstream — the only difference from a Transform, whose sink feeds its
// own). Chunks are strings (the same default as PassThrough).
func (e *Emitter) emitDuplexPair(pos ast.Pos) (Value, error) {
	e.ensureNodeStreamRuntime()
	e.ensureStreamPipeRuntime()
	chunkTy := TypePtr

	// Per-side allocation: rstream + wstream.
	mkSide := func() (rs, ws string) {
		fulfill := e.emitStreamFulfillThunk(chunkTy)
		rs = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_rs_alloc(double 0.0, ptr %s)", rs, fulfill))
		ws = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_ws_alloc(double 1.0)", ws))
		return rs, ws
	}
	rsA, wsA := mkSide()
	rsB, wsB := mkSide()

	// ctx = { rs(other side), ws(own), transform=null (identity), flush=null,
	// parked flags/words, parked prom, spare } — the same TSCTX the Transform
	// constructor builds, with the rs swapped to the peer's.
	mkCtx := func(peerRS, ownWS string) string {
		ctx := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 72)", ctx))
		storePtr := func(idx int, val string) {
			gep := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %d", gep, ctx, idx))
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", val, gep))
		}
		storePtr(0, peerRS)
		storePtr(1, ownWS)
		storePtr(2, "null")
		storePtr(3, "null")
		for i := 4; i <= 6; i++ {
			gep := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %s, i64 %d", gep, ctx, i))
			e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", gep))
		}
		storePtr(7, "null")
		storePtr(8, "null")
		e.storeWStreamField(ownWS, 9, e.buildBuiltinClosure("@__kml_ts_sink_write", ctx))
		e.storeWStreamField(ownWS, 10, e.buildBuiltinClosure("@__kml_ts_sink_close", ctx))
		e.storeWStreamField(ownWS, 11, e.buildBuiltinClosure("@__kml_ts_sink_abort", ctx))
		return ctx
	}
	ctxA := mkCtx(rsB, wsA) // A's writes surface on B
	ctxB := mkCtx(rsA, wsB) // B's writes surface on A
	// A chunk parked in ctxA waits on rsB's capacity, so rsB's pull resumes
	// ctxA (and vice versa) — the cross of the Transform's own-ctx pull.
	e.storeStreamField(rsB, 9, e.buildBuiltinClosure("@__kml_ts_pull", ctxA))
	e.storeStreamField(rsA, 9, e.buildBuiltinClosure("@__kml_ts_pull", ctxB))
	for _, s := range []struct{ fn, arg string }{
		{"@__kml_rs_started", rsA}, {"@__kml_rs_started", rsB},
		{"@__kml_ws_started", wsA}, {"@__kml_ws_started", wsB},
	} {
		e.emitInstr(fmt.Sprintf("call void @__kml_microtask_enqueue(ptr %s)", e.buildBuiltinClosure(s.fn, s.arg)))
	}

	nsTy := NodeTransformType(chunkTy, chunkTy)
	mkNS := func(rs, ws string) string {
		inv := e.emitNodeInvokeDataThunk(chunkTy)
		dec := e.emitStreamDecodeThunk(chunkTy)
		ns := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_ns_alloc(ptr %s, ptr %s, ptr %s, ptr %s)", ns, rs, ws, inv, dec))
		e.emitInstr(fmt.Sprintf("call void @__kml_ns_arm_writable(ptr %s)", ns))
		return ns
	}
	nsA := mkNS(rsA, wsA)
	nsB := mkNS(rsB, wsB)

	// Package as a 2-element array so `const [a, b] = duplexPair()` and
	// `pair[0]` both work through the ordinary array machinery.
	data := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", data))
	g0 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 0", g0, data))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", nsA, g0))
	g1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 1", g1, data))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", nsB, g1))
	agg0 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue { ptr, i64 } undef, ptr %s, 0", agg0, data))
	agg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue { ptr, i64 } %s, i64 2, 1", agg, agg0))
	return Value{Ref: agg, Ty: ArrayOf(nsTy)}, nil
}
