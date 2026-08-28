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
		chunkArg = fmt.Sprintf("ptr %s, i64 %%v1", p)
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
