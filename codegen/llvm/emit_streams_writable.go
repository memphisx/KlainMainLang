// emit_streams_writable.go — WHATWG WritableStream codegen (TDD-00097
// Stage 2): `new WritableStream<T>({start, write, close, abort}, strategy?)`,
// writer/controller dispatch. Mirrors emit_streams.go over the
// runtime_streams_writable.go state machine.
package llvm

import (
	"fmt"

	"KlainMainLang/ast"
)

// emitStreamWriteWrap emits `ptr @__kml_ws_writewrap_N(ptr %env, i64 %v0, i64
// %v1)` — env is a {userClosure, stream} pair: rebuild the typed chunk, invoke
// the sink's write(chunk, controller?), return its promise or null.
func (e *Emitter) emitStreamWriteWrap(userTy, chunkTy Type) string {
	isAsync := callbackReturnsPromise(userTy)
	nParams := len(userTy.FuncParams)

	restore := e.beginThunkEmit()
	u := e.freshReg()
	up := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %%env, i32 0, i32 0", up))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", u, up))
	sp := e.freshReg()
	s := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %%env, i32 0, i32 1", sp))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", s, sp))
	fp := e.freshReg()
	fpp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 0", fpp, u))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", fp, fpp))
	ep := e.freshReg()
	epp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 1", epp, u))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", ep, epp))

	args := fmt.Sprintf("ptr %s", ep)
	sig := "ptr"
	if nParams >= 1 {
		if chunkTy.IsArray() {
			cp := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %%v0 to ptr", cp))
			// The user callback's array param expects a header (TDD-00127).
			hdr := e.newArrayHeader(cp, "%v1")
			args += fmt.Sprintf(", ptr %s, i64 %%v1", hdr)
			sig += ", ptr, i64"
		} else {
			chunk := e.streamChunkFromWords("%v0", "%v1", chunkTy)
			args += fmt.Sprintf(", %s %s", chunkTy.IR, chunk.Ref)
			sig += ", " + chunkTy.IR
		}
	}
	if nParams >= 2 {
		args += fmt.Sprintf(", ptr %s", s)
		sig += ", ptr"
	}
	if isAsync {
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr (%s) %s(%s)", r, sig, fp, args))
		// The stream reacts to it (the spec's uponPromise): handled.
		e.emitMarkPromiseHandled(r)
		e.emitInstr(fmt.Sprintf("ret ptr %s", r))
	} else {
		e.emitInstr(fmt.Sprintf("call void (%s) %s(%s)", sig, fp, args))
		e.emitInstr("ret ptr null")
	}
	body := e.allocas.String() + e.body.String()
	restore()

	fn := e.defineContentNamed("@__kml_ws_writewrap.", "ptr", "ptr %env, i64 %v0, i64 %v1", body)
	return fn
}

// emitStreamCloseWrap emits `ptr @__kml_ws_closewrap_N(ptr %env)` — env is
// the user closure header; zero-parameter close callback.
func (e *Emitter) emitStreamCloseWrap(userTy Type) string {
	isAsync := callbackReturnsPromise(userTy)

	restore := e.beginThunkEmit()
	fp := e.freshReg()
	fpp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %%env, i32 0, i32 0", fpp))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", fp, fpp))
	ep := e.freshReg()
	epp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %%env, i32 0, i32 1", epp))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", ep, epp))
	if isAsync {
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr (ptr) %s(ptr %s)", r, fp, ep))
		e.emitMarkPromiseHandled(r) // the stream reacts to it (uponPromise)
		e.emitInstr(fmt.Sprintf("ret ptr %s", r))
	} else {
		e.emitInstr(fmt.Sprintf("call void (ptr) %s(ptr %s)", fp, ep))
		e.emitInstr("ret ptr null")
	}
	body := e.allocas.String() + e.body.String()
	restore()

	fn := e.defineContentNamed("@__kml_ws_closewrap.", "ptr", "ptr %env", body)
	return fn
}

// emitNewWritableStream implements `new WritableStream<T>(sink?, strategy?)`.
func (e *Emitter) emitNewWritableStream(ex *ast.NewWritableStreamExpression) (Value, error) {
	pos := ex.GetPos()
	chunkTy := TypeAny // WritableStream<W = any>
	if ex.ChunkType != nil {
		chunkTy = e.resolveType(ex.ChunkType)
	}
	controllerTy := WSControllerType(chunkTy)
	e.ensureWStreamRuntime()

	var startExpr, writeExpr, closeExpr, abortExpr ast.Expression
	if ex.Sink != nil {
		ol, ok := ex.Sink.(*ast.ObjectLiteral)
		if !ok {
			return Value{}, fmt.Errorf("%d:%d: new WritableStream's underlying sink must be an object literal ({start, write, close, abort})", pos.Line, pos.Col)
		}
		members, unknown := literalMembers(ol, "start", "write", "close", "abort")
		if unknown != "" {
			return Value{}, fmt.Errorf("%d:%d: unknown underlying sink member '%s' (expected start, write, close, abort)", pos.Line, pos.Col, unknown)
		}
		startExpr, writeExpr, closeExpr, abortExpr = members["start"], members["write"], members["close"], members["abort"]
	}

	hwmRef, sizeExpr, byteLengthStrategy, err := e.resolveStreamStrategy(ex.Strategy, chunkTy, pos)
	if err != nil {
		return Value{}, err
	}

	s := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_ws_alloc(double %s)", s, hwmRef))

	if sizeExpr != nil {
		userClo, err := e.streamCallbackClosure(sizeExpr, []Type{chunkTy}, "size algorithm", pos)
		if err != nil {
			return Value{}, err
		}
		wrap, err := e.emitStreamSizeWrap(userClo.Ty, chunkTy, pos)
		if err != nil {
			return Value{}, err
		}
		e.storeWStreamField(s, 8, e.buildBuiltinClosure(wrap, userClo.Ref))
	} else if byteLengthStrategy {
		wrap := e.emitStreamByteLengthSizeWrap()
		e.storeWStreamField(s, 8, e.buildBuiltinClosure(wrap, "null"))
	}

	if writeExpr != nil {
		userClo, err := e.streamCallbackClosure(writeExpr, []Type{chunkTy, controllerTy}, "write callback", pos)
		if err != nil {
			return Value{}, err
		}
		if len(userClo.Ty.FuncParams) > 2 {
			return Value{}, fmt.Errorf("%d:%d: a sink write callback takes (chunk, controller?)", pos.Line, pos.Col)
		}
		wrap := e.emitStreamWriteWrap(userClo.Ty, chunkTy)
		env := e.freshReg()
		e.heapMallocInto(env, "16")
		e0 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 0", e0, env))
		e.heapStore("ptr", userClo.Ref, e0, 8)
		e1 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 1", e1, env))
		e.heapStore("ptr", s, e1, 8)
		e.storeWStreamField(s, 9, e.buildBuiltinClosure(wrap, env))
	}

	if closeExpr != nil {
		userClo, err := e.streamCallbackClosure(closeExpr, nil, "close callback", pos)
		if err != nil {
			return Value{}, err
		}
		if len(userClo.Ty.FuncParams) > 0 {
			return Value{}, fmt.Errorf("%d:%d: a sink close callback takes no parameters", pos.Line, pos.Col)
		}
		wrap := e.emitStreamCloseWrap(userClo.Ty)
		e.storeWStreamField(s, 10, e.buildBuiltinClosure(wrap, userClo.Ref))
	}

	if abortExpr != nil {
		userClo, err := e.streamCallbackClosure(abortExpr, nil, "abort callback", pos)
		if err != nil {
			return Value{}, err
		}
		if len(userClo.Ty.FuncParams) > 0 {
			return Value{}, fmt.Errorf("%d:%d: a sink abort callback with a reason parameter is not supported yet — use a zero-parameter abort", pos.Line, pos.Col)
		}
		wrap := e.emitStreamCancelWrap(userClo.Ty)
		e.storeWStreamField(s, 11, e.buildBuiltinClosure(wrap, userClo.Ref))
	}

	startedClo := e.buildBuiltinClosure("@__kml_ws_started", s)
	if startExpr != nil {
		userClo, err := e.streamCallbackClosure(startExpr, []Type{controllerTy}, "start callback", pos)
		if err != nil {
			return Value{}, err
		}
		if len(userClo.Ty.FuncParams) > 1 {
			return Value{}, fmt.Errorf("%d:%d: a start callback takes at most one (controller) parameter", pos.Line, pos.Col)
		}
		fp := e.freshReg()
		fpp := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 0", fpp, userClo.Ref))
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", fp, fpp))
		ep := e.freshReg()
		epp := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 1", epp, userClo.Ref))
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", ep, epp))
		args := fmt.Sprintf("ptr %s", ep)
		sig := "ptr"
		if len(userClo.Ty.FuncParams) == 1 {
			args += fmt.Sprintf(", ptr %s", s)
			sig += ", ptr"
		}
		if callbackReturnsPromise(userClo.Ty) {
			p := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call ptr (%s) %s(%s)", p, sig, fp, args))
			e.emitInstr(fmt.Sprintf("call void @__kml_promise_add_reaction(ptr %s, ptr %s)", p, startedClo))
		} else {
			e.emitInstr(fmt.Sprintf("call void (%s) %s(%s)", sig, fp, args))
			e.emitInstr(fmt.Sprintf("call void @__kml_microtask_enqueue(ptr %s)", startedClo))
		}
	} else {
		e.emitInstr(fmt.Sprintf("call void @__kml_microtask_enqueue(ptr %s)", startedClo))
	}

	return Value{Ref: s, Ty: WritableStreamType(chunkTy)}, nil
}

// storeWStreamField stores a ptr into the wstream struct field at idx.
func (e *Emitter) storeWStreamField(s string, idx int, val string) {
	gep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, wstreamStructIR, s, idx))
	e.heapStore("ptr", val, gep, 8)
}

// resolveWStreamForCall resolves a writable-stream/writer/controller receiver.
func (e *Emitter) resolveWStreamForCall(objExpr ast.Expression, pos ast.Pos) (Type, string, error) {
	ty, _, ptr, err := e.resolveWStreamHandle(objExpr, pos)
	return ty, ptr, err
}

// resolveWStreamHandle is resolveWStreamForCall that also gives the
// receiver itself (a writer's handle).
func (e *Emitter) resolveWStreamHandle(objExpr ast.Expression, pos ast.Pos) (Type, string, string, error) {
	if id, ok := objExpr.(*ast.Identifier); ok {
		sym, found := e.lookup(id.Name)
		if found && (sym.Ty.IsWritableStream() || sym.Ty.IsStreamWriter() || sym.Ty.IsWSController()) {
			ptr := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", ptr, sym.Ptr))
			return sym.Ty, ptr, e.streamOfHandle(sym.Ty, ptr), nil
		}
	}
	val, err := e.emitExpr(objExpr)
	if err != nil {
		return Type{}, "", "", err
	}
	if !val.Ty.IsWritableStream() && !val.Ty.IsStreamWriter() && !val.Ty.IsWSController() {
		return Type{}, "", "", fmt.Errorf("%d:%d: value is not a WritableStream/writer/controller", pos.Line, pos.Col)
	}
	return val.Ty, val.Ref, e.streamOfHandle(val.Ty, val.Ref), nil
}

// emitWStreamMethodCall dispatches writable stream/writer/controller methods.
func (e *Emitter) emitWStreamMethodCall(objExpr ast.Expression, method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	ty, handle, ptr, err := e.resolveWStreamHandle(objExpr, pos)
	if err != nil {
		return Value{}, err
	}
	e.ensureWStreamRuntime()
	chunkTy := TypeI64
	if ty.StreamChunk != nil {
		chunkTy = *ty.StreamChunk
	}
	c := &streamCall{ptr: ptr, handle: handle, chunkTy: chunkTy, method: method, args: args, pos: pos}
	if f, ok := streamWriterMethods[method]; ok && ty.IsStreamWriter() {
		// A released writer is bound to no stream (Node's state errors).
		return e.emitIfReleased(ptr, func() (Value, error) { return f(e, c) }, func() (Value, error) {
			if method == "releaseLock" {
				return Value{Ty: TypeVoid}, nil
			}
			return e.emitRejectedStreamPromise("Invalid state: Writer is not bound to a WritableStream", taskVoidPromise("").Ty), nil
		})
	}
	var table map[string]func(e *Emitter, c *streamCall) (Value, error)
	var what string
	switch {
	case ty.IsWritableStream():
		table, what = writableStreamMethods, "WritableStream"
	case ty.IsStreamWriter():
		table, what = streamWriterMethods, "stream writer"
	case ty.IsWSController():
		table, what = wsControllerMethods, "writable stream controller"
	default:
		return Value{}, fmt.Errorf("%d:%d: unknown writable stream method '%s'", pos.Line, pos.Col, method)
	}
	if f, ok := table[method]; ok {
		return f(e, c)
	}
	return Value{}, fmt.Errorf("%d:%d: unknown %s method '%s'", pos.Line, pos.Col, what, method)
}

// writableStreamMethods, streamWriterMethods and wsControllerMethods are a
// WritableStream's, its default writer's and its controller's methods, by
// name.
var writableStreamMethods, streamWriterMethods, wsControllerMethods map[string]func(e *Emitter, c *streamCall) (Value, error)

func init() {
	closeFn := func(e *Emitter, c *streamCall) (Value, error) { return e.emitWStreamClose(c.ptr) }
	writableStreamMethods = map[string]func(e *Emitter, c *streamCall) (Value, error){
		"getWriter": (*Emitter).wstreamGetWriter,
		"abort":     (*Emitter).wstreamAbort,
		"close":     closeFn,
	}
	streamWriterMethods = map[string]func(e *Emitter, c *streamCall) (Value, error){
		"write":       (*Emitter).wstreamWrite,
		"close":       closeFn,
		"abort":       (*Emitter).wstreamAbort,
		"releaseLock": (*Emitter).wstreamReleaseLock,
	}
	wsControllerMethods = map[string]func(e *Emitter, c *streamCall) (Value, error){
		"error": (*Emitter).wstreamError,
	}
}

// taskVoidPromise is the Promise<void> held in ref.
func taskVoidPromise(ref string) Value {
	pt := PromiseOf(TypeVoid)
	pt.PromiseTask = true
	return Value{Ref: ref, Ty: pt}
}

func (e *Emitter) wstreamGetWriter(c *streamCall) (Value, error) {
	ok := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_ws_lock(ptr %s)", ok, c.ptr))
	e.streamThrowTypeError(ok, "Invalid state: WritableStream is locked")
	return e.newStreamHandle(c.ptr, WSWriterType(c.chunkTy)), nil
}

// wstreamAbort is a stream's and its writer's abort(reason).
func (e *Emitter) wstreamAbort(c *streamCall) (Value, error) {
	bits, err := e.streamReasonBits(c.args, c.pos)
	if err != nil {
		return Value{}, err
	}
	prom := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_ws_abort(ptr %s, i64 %s)", prom, c.ptr, bits))
	return taskVoidPromise(prom), nil
}

func (e *Emitter) wstreamWrite(c *streamCall) (Value, error) {
	if len(c.args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: write() expects 1 chunk argument", c.pos.Line, c.pos.Col)
	}
	cv, err := e.emitExprWithObjectHint(c.args[0], c.chunkTy)
	if err != nil {
		return Value{}, err
	}
	cv = e.convert(cv, c.chunkTy)
	v0, v1 := e.streamChunkWords(cv)
	prom := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_ws_write(ptr %s, i64 %s, i64 %s)", prom, c.ptr, v0, v1))
	// A null promise means the write was disallowed (closing/closed).
	isNull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", isNull, prom))
	okI := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = zext i1 %s to i64", okI, isNull))
	e.streamThrowTypeError(okI, "cannot write to a closing or closed WritableStream")
	return taskVoidPromise(prom), nil
}

// wstreamReleaseLock is writer.releaseLock(): the writer's ready and closed
// reject (__kml_ws_release) and it detaches from its stream.
func (e *Emitter) wstreamReleaseLock(c *streamCall) (Value, error) {
	err := e.emitStreamStateError("Invalid state: Writer has been released")
	e.emitInstr(fmt.Sprintf("call void @__kml_ws_release(ptr %s, ptr %s, i64 %s)", c.ptr, c.handle, err))
	e.heapStore("ptr", "null", c.handle, 8)
	return Value{Ty: TypeVoid}, nil
}

func (e *Emitter) wstreamError(c *streamCall) (Value, error) {
	bits, err := e.streamReasonBits(c.args, c.pos)
	if err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("call void @__kml_ws_error(ptr %s, i64 %s)", c.ptr, bits))
	return Value{Ty: TypeVoid}, nil
}

// emitWStreamClose emits close() (writer or stream): TypeError when close is
// not allowed, else the close promise.
func (e *Emitter) emitWStreamClose(ptr string) (Value, error) {
	prom := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_ws_close(ptr %s)", prom, ptr))
	isNull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", isNull, prom))
	okI := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = zext i1 %s to i64", okI, isNull))
	e.streamThrowTypeError(okI, "cannot close an already-closing or closed WritableStream")
	pt := PromiseOf(TypeVoid)
	pt.PromiseTask = true
	return Value{Ref: prom, Ty: pt}, nil
}
