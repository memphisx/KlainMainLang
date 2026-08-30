package llvm

// Node streams as real classes — `class X extends Readable/Writable`
// (TDD-00132). Mirrors the `extends EventEmitter` synthetic-root model
// (emit_classes.go): the stream roots are never registered classes, a
// subclass carries a hidden Node-stream handle field (ClassNodeStreamField),
// and the options-form stream runtime (emit_node_stream.go) is reused
// wholesale. The one new mechanism is routing the runtime's pull/sink to the
// subclass's `_read`/`_write` override method instead of an options-object
// callback.

import (
	"fmt"

	"KlainMainLang/ast"
)

// nodeStreamTy synthesizes the options-form node-stream Type for a stream
// class, so the shared emitNodeStreamCallOn dispatch and nodeEventPayload see
// the right readable/writable flags and element types.
func (info ClassInfo) nodeStreamTy() Type {
	switch {
	case info.HasNodeReadable && info.HasNodeWritable:
		return NodeTransformType(info.StreamInTy, info.StreamOutTy)
	case info.HasNodeReadable:
		return NodeReadableType(info.StreamOutTy)
	default:
		return NodeWritableType(info.StreamInTy)
	}
}

// loadClassNodeStreamHandle loads the hidden Node-stream handle off a stream-
// class instance.
func (e *Emitter) loadClassNodeStreamHandle(classTy Type, instRef string) string {
	gep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, classTy.StructIR(), instRef, classNodeStreamFieldIndex(classTy)))
	h := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", h, gep))
	return h
}

// asNodeStreamValue normalizes a value that may be either an options-form
// node stream (already IsNodeReadable/Writable) or a `class X extends
// Readable/Writable` instance (TDD-00132) into a plain node-stream Value —
// loading the hidden handle and synthesizing the node-stream Type in the
// class case. Any site that consumes a node stream as a bare expression
// (finished/pipeline args, pipe destinations) routes through here so a
// stream-class instance is accepted wherever an options-form stream is.
func (e *Emitter) asNodeStreamValue(v Value) Value {
	if v.Ty.IsNodeReadable || v.Ty.IsNodeWritable {
		return v
	}
	if v.Ty.IsClass {
		if info, ok := e.classes[v.Ty.ClassName]; ok && (info.HasNodeReadable || info.HasNodeWritable) {
			ptr := e.loadClassNodeStreamHandle(v.Ty, v.Ref)
			return Value{Ref: ptr, Ty: info.nodeStreamTy()}
		}
	}
	return v
}

// emitClassNodeStreamCall dispatches a hand-dispatched stream method
// (on/push/pipe/…) on a `class X extends Readable/Writable` instance by
// loading its hidden handle and delegating to the shared node-stream core.
func (e *Emitter) emitClassNodeStreamCall(classTy Type, info ClassInfo, objExpr ast.Expression, method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	thisVal, err := e.emitExpr(objExpr)
	if err != nil {
		return Value{}, err
	}
	nsPtr := e.loadClassNodeStreamHandle(classTy, thisVal.Ref)
	res, err := e.emitNodeStreamCallOn(info.nodeStreamTy(), nsPtr, method, args, pos)
	if err != nil {
		return Value{}, err
	}
	// on/once/pause/resume/end return the stream itself for chaining — hand
	// back the class instance so `counter.on(...).on(...)` keeps its class
	// type, rather than degrading to the raw node-stream value.
	switch method {
	case "on", "once", "pause", "resume", "end":
		return thisVal, nil
	}
	return res, nil
}

// streamSuperHWM resolves the `highWaterMark` a stream-class constructor
// threads via `super({ highWaterMark, objectMode })` (TDD-00132) into a
// `double` operand, defaulting to 1.0. Only the two options this compiler
// models are read off the super() object literal; other Node stream options
// (`encoding`, `emitClose`, …) are silently ignored rather than rejected, and
// the HWM expression is evaluated in the construction-site scope, so it must
// not reference a constructor parameter (a literal is the common Node form).
func (e *Emitter) streamSuperHWM(info ClassInfo, pos ast.Pos) (string, error) {
	if info.Constructor == nil || info.Constructor.Body == nil {
		return "1.0", nil
	}
	idx := topLevelSuperCallIndex(info.Constructor.Body)
	if idx < 0 {
		return "1.0", nil
	}
	es, ok := info.Constructor.Body.Body[idx].(*ast.ExpressionStatement)
	if !ok {
		return "1.0", nil
	}
	call, ok := es.Expr.(*ast.CallExpression)
	if !ok || len(call.Args) == 0 {
		return "1.0", nil
	}
	ol, ok := call.Args[0].(*ast.ObjectLiteral)
	if !ok {
		return "1.0", nil
	}
	if omExpr := objectLiteralProp(ol, "objectMode"); omExpr != nil {
		v, err := e.emitExpr(omExpr)
		if err != nil {
			return "", err
		}
		if v.Ty.IR != "i1" {
			return "", fmt.Errorf("%d:%d: a stream's objectMode option must be a boolean", pos.Line, pos.Col)
		}
	}
	hwmExpr := objectLiteralProp(ol, "highWaterMark")
	if hwmExpr == nil {
		return "1.0", nil
	}
	v, err := e.emitExpr(hwmExpr)
	if err != nil {
		return "", err
	}
	return e.coerce(v, TypeF64).Ref, nil
}

// emitConstructNodeStreamHandle builds the Node-stream runtime handle for a
// `class X extends Readable/Writable` instance and stores it into the hidden
// field, wiring the runtime pull/sink to the subclass's `_read`/`_write`
// override method. Called during construction, right after the instance is
// allocated and before the user constructor runs.
func (e *Emitter) emitConstructNodeStreamHandle(info ClassInfo, className, dataReg string, pos ast.Pos) error {
	e.ensureNodeStreamRuntime()

	hwm, err := e.streamSuperHWM(info, pos)
	if err != nil {
		return err
	}

	// Transform (TDD-00132 Stage C2): both-sided, but the writable sink routes
	// each chunk through the subclass's `_transform(chunk, enc, cb)` override
	// (whose `this.push` feeds the readable side) rather than an independent
	// `_read`/`_write` pair. Reuse the TransformStream TSCTX sink/pull machine —
	// the readable side is pushed to, not pulled, and closes when the writable
	// side ends. Handled here and returned before the Readable/Writable path.
	if info.HasNodeTransform {
		return e.emitConstructNodeTransformHandle(info, className, dataReg, hwm, pos)
	}

	rs, ws := "null", "null"

	if info.HasNodeReadable {
		outTy := info.StreamOutTy
		fulfill := e.emitStreamFulfillThunk(outTy)
		rsReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_rs_alloc(double %s, ptr %s)", rsReg, hwm, fulfill))
		impl, ok := info.MethodImplementor["_read"]
		if !ok {
			return fmt.Errorf("%d:%d: class '%s' extends Readable but does not implement _read()", pos.Line, pos.Col, className)
		}
		sig := info.MethodSigs["_read"]
		wrap := e.emitClassStreamPull(llvmSafeSymbol(impl+"_"+"_read"), sig.IsAsync)
		e.storeStreamField(rsReg, 9, e.buildBuiltinClosure(wrap, dataReg))
		e.emitInstr(fmt.Sprintf("call void @__kml_microtask_enqueue(ptr %s)", e.buildBuiltinClosure("@__kml_rs_started", rsReg)))
		rs = rsReg
	}

	if info.HasNodeWritable {
		inTy := info.StreamInTy
		wsReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_ws_alloc(double %s)", wsReg, hwm))
		impl, ok := info.MethodImplementor["_write"]
		if !ok {
			return fmt.Errorf("%d:%d: class '%s' extends Writable but does not implement _write()", pos.Line, pos.Col, className)
		}
		sig := info.MethodSigs["_write"]
		if n := len(sig.ParamTypes); n != 1 && n != 3 {
			return fmt.Errorf("%d:%d: class '%s' _write must take (chunk) or Node's (chunk, encoding, callback), got %d parameter(s)", pos.Line, pos.Col, className, n)
		}
		wrap := e.emitClassStreamWrite(llvmSafeSymbol(impl+"_"+"_write"), inTy, len(sig.ParamTypes), sig.IsAsync)
		e.storeWStreamField(wsReg, 9, e.buildBuiltinClosure(wrap, dataReg))
		e.emitInstr(fmt.Sprintf("call void @__kml_microtask_enqueue(ptr %s)", e.buildBuiltinClosure("@__kml_ws_started", wsReg)))
		ws = wsReg
	}

	inv, dec := "null", "null"
	if info.HasNodeReadable {
		inv = e.emitNodeInvokeDataThunk(info.StreamOutTy)
		dec = e.emitStreamDecodeThunk(info.StreamOutTy)
	}
	nsReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_ns_alloc(ptr %s, ptr %s, ptr %s, ptr %s)", nsReg, rs, ws, inv, dec))
	if info.HasNodeWritable {
		e.emitInstr(fmt.Sprintf("call void @__kml_ns_arm_writable(ptr %s)", nsReg))
	}

	classTy := info.Ty
	gep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, classTy.StructIR(), dataReg, classNodeStreamFieldIndex(classTy)))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", nsReg, gep))
	return nil
}

// emitClassStreamPull emits the readable pull thunk: the runtime hands it the
// instance pointer (stored as the closure env), and it invokes the subclass's
// `_read(this)` override. The `this` binding — absent in options form — is the
// whole point of the class shape.
func (e *Emitter) emitClassStreamPull(implFn string, isAsync bool) string {
	e.streamSiteCtr++
	fn := fmt.Sprintf("@__kml_ns_classpull_%d", e.streamSiteCtr)
	call := fmt.Sprintf("call void @%s(ptr %%inst)", implFn)
	if isAsync {
		r := e.freshReg()
		call = fmt.Sprintf("%s = call ptr @%s(ptr %%inst)", r, implFn)
	}
	e.functions.WriteString(fmt.Sprintf("\ndefine ptr %s(ptr %%inst) {\nentry:\n  %s\n  ret ptr null\n}\n", fn, call))
	return fn
}

// emitClassStreamWrite emits the writable sink thunk. The runtime invokes it
// with the instance pointer (closure env) and the chunk words; it rebuilds the
// chunk and calls the subclass's `_write(this, chunk)` override. The Node
// `_write(chunk, enc, cb)` encoding/callback params are not threaded in V1 —
// the write is treated as synchronously accepted.
func (e *Emitter) emitClassStreamWrite(implFn string, chunkTy Type, nParams int, isAsync bool) string {
	e.streamSiteCtr++
	fn := fmt.Sprintf("@__kml_ns_classwrite_%d", e.streamSiteCtr)

	restore := e.beginThunkEmit()
	var chunkArg string
	if chunkTy.IsArray {
		p := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %%v0 to ptr", p))
		// The subclass method's array param expects a header (TDD-00127).
		hdr := e.newArrayHeader(p, "%v1")
		chunkArg = fmt.Sprintf("ptr %s, i64 %%v1", hdr)
	} else {
		chunk := e.streamChunkFromWords("%v0", "%v1", chunkTy)
		chunkArg = fmt.Sprintf("%s %s", chunkTy.IR, chunk.Ref)
	}
	args := "ptr %inst, " + chunkArg
	// Node's `_write(chunk, encoding, callback)`: the encoding string and the
	// completion callback are supplied so the idiomatic `cb()` inside the body
	// runs, but our runtime treats the write as complete when the sink thunk
	// returns (V1), so the callback is a no-op closure and the write cannot be
	// deferred past the call.
	if nParams == 3 {
		enc := e.internString("buffer")
		cb := e.buildBuiltinClosure(e.ensureNoopCallback(), "null")
		args += fmt.Sprintf(", ptr %s, ptr %s", enc, cb)
	}
	if isAsync {
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @%s(%s)", r, implFn, args))
	} else {
		e.emitInstr(fmt.Sprintf("call void @%s(%s)", implFn, args))
	}
	e.emitInstr("ret ptr null")
	body := e.allocas.String() + e.body.String()
	restore()

	e.functions.WriteString(fmt.Sprintf("\ndefine ptr %s(ptr %%inst, i64 %%v0, i64 %%v1) {\nentry:\n%s}\n", fn, body))
	return fn
}

// emitConstructNodeTransformHandle builds a `class X extends Transform`
// instance's Node-stream handle (TDD-00132 Stage C2). It mirrors the
// options-form Transform wiring (emit_node_stream.go, `case "transform"`): an
// rs + ws joined by a 72-byte TSCTX whose sink runs the transform when the
// readable side has capacity (else parks the chunk) and whose pull resumes a
// parked chunk. The one class-specific piece is the slot-2 transform closure:
// it invokes the subclass's `_transform(chunk, enc, cb)` method, whose
// `this.push(...)` enqueues into this same rs (the hidden handle set below).
func (e *Emitter) emitConstructNodeTransformHandle(info ClassInfo, className, dataReg, hwm string, pos ast.Pos) error {
	impl, ok := info.MethodImplementor["_transform"]
	if !ok {
		return fmt.Errorf("%d:%d: class '%s' extends Transform but does not implement _transform()", pos.Line, pos.Col, className)
	}
	sig := info.MethodSigs["_transform"]
	if n := len(sig.ParamTypes); n != 1 && n != 3 {
		return fmt.Errorf("%d:%d: class '%s' _transform must take (chunk) or Node's (chunk, encoding, callback), got %d parameter(s)", pos.Line, pos.Col, className, n)
	}

	outTy := info.StreamOutTy
	inTy := info.StreamInTy

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
	// slot 2 — the transform callback. The class-form `_transform` needs no
	// controller arg: `this.push` inside the method already reaches rs through
	// the hidden handle, so the same `emitClassStreamWrite` thunk that drives a
	// `_write` sink drives `_transform` here (its ABI is the TSCTX slot-2 ABI:
	// `ptr(ptr %env, i64 %v0, i64 %v1)` returning a promise-or-null).
	trWrap := e.emitClassStreamWrite(llvmSafeSymbol(impl+"_"+"_transform"), inTy, len(sig.ParamTypes), sig.IsAsync)
	storeCtxPtr(2, e.buildBuiltinClosure(trWrap, dataReg))
	// slot 3 — flush: `_flush` is out of V1 scope (TDD-00132), so no flush hook.
	storeCtxPtr(3, "null")
	// slots 4-6 — parked flags/words (i64 0); slots 7,8 — parked prom, spare.
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

	classTy := info.Ty
	gep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, classTy.StructIR(), dataReg, classNodeStreamFieldIndex(classTy)))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", nsReg, gep))
	return nil
}

// ensureNoopCallback emits (once) a void(ptr) function usable as the fn of a
// no-op closure — the `_write` completion callback our runtime auto-completes.
func (e *Emitter) ensureNoopCallback() string {
	const fn = "@__kml_noop_cb"
	if !e.noopCallbackEmitted {
		e.noopCallbackEmitted = true
		e.functions.WriteString(fmt.Sprintf("\ndefine void %s(ptr %%env) {\nentry:\n  ret void\n}\n", fn))
	}
	return fn
}
