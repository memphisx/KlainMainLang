// emit_streams_pipe.go — TDD-00097 Stage 3 codegen: pipeTo/pipeThrough over
// the runtime pipe state machine, tee(), and `new TransformStream<I, O>`.
package llvm

import (
	"fmt"

	"KlainMainLang/ast"
)

// emitStreamDecodeThunk emits `{i64,i64,i64} @__kml_rs_decode_N(ptr %rec)` —
// the inverse of the fulfill thunk: unpack a typed {value, done} record back
// into raw queue words + a done flag, so the runtime pipe/tee machinery can
// move chunks without knowing their type.
func (e *Emitter) emitStreamDecodeThunk(chunkTy Type) string {
	e.streamSiteCtr++
	fn := fmt.Sprintf("@__kml_rs_decode_%d", e.streamSiteCtr)
	resultTy := genNextResultType(chunkTy)

	restore := e.beginThunkEmit()
	vIdx, _, _ := resultTy.FieldIndex("value")
	vGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %%rec, i32 0, i32 %d", vGep, resultTy.StructIR(), vIdx))
	loaded := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align 8", loaded, StructFieldIR(chunkTy), vGep))
	v0, v1 := e.streamChunkWords(Value{Ref: loaded, Ty: chunkTy})
	dIdx, _, _ := resultTy.FieldIndex("done")
	dGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %%rec, i32 0, i32 %d", dGep, resultTy.StructIR(), dIdx))
	dI1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i1, ptr %s, align 1", dI1, dGep))
	dI64 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = zext i1 %s to i64", dI64, dI1))
	a0 := e.freshReg()
	a1 := e.freshReg()
	a2 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue { i64, i64, i64 } undef, i64 %s, 0", a0, v0))
	e.emitInstr(fmt.Sprintf("%s = insertvalue { i64, i64, i64 } %s, i64 %s, 1", a1, a0, v1))
	e.emitInstr(fmt.Sprintf("%s = insertvalue { i64, i64, i64 } %s, i64 %s, 2", a2, a1, dI64))
	e.emitInstr(fmt.Sprintf("ret { i64, i64, i64 } %s", a2))
	body := e.allocas.String() + e.body.String()
	restore()

	e.functions.WriteString(fmt.Sprintf("\ndefine { i64, i64, i64 } %s(ptr %%rec) {\nentry:\n%s}\n", fn, body))
	return fn
}

// resolvePipeOptions evaluates a pipeTo/pipeThrough options object literal
// into a runtime flags register (1 preventClose · 2 preventAbort · 4
// preventCancel) and the abort-signal field pointers (or nulls).
func (e *Emitter) resolvePipeOptions(opt ast.Expression, pos ast.Pos) (flagsRef, sigARef, sigRRef string, err error) {
	flagsRef, sigARef, sigRRef = "0", "null", "null"
	if opt == nil {
		return
	}
	ol, ok := opt.(*ast.ObjectLiteral)
	if !ok {
		return "", "", "", fmt.Errorf("%d:%d: pipe options must be an object literal ({preventClose, preventAbort, preventCancel, signal})", pos.Line, pos.Col)
	}
	cur := "0"
	orFlag := func(valExpr ast.Expression, bit int) error {
		v, err := e.emitExpr(valExpr)
		if err != nil {
			return err
		}
		v = e.coerce(v, TypeBool)
		z := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = zext i1 %s to i64", z, v.Ref))
		sh := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %d", sh, z, bit))
		nx := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = or i64 %s, %s", nx, cur, sh))
		cur = nx
		return nil
	}
	for _, p := range ol.Properties {
		switch p.Key {
		case "preventClose":
			if err := orFlag(p.Value, 1); err != nil {
				return "", "", "", err
			}
		case "preventAbort":
			if err := orFlag(p.Value, 2); err != nil {
				return "", "", "", err
			}
		case "preventCancel":
			if err := orFlag(p.Value, 4); err != nil {
				return "", "", "", err
			}
		case "signal":
			sv, err := e.emitExpr(p.Value)
			if err != nil {
				return "", "", "", err
			}
			if !sv.Ty.IsAbortSignal {
				return "", "", "", fmt.Errorf("%d:%d: pipe option 'signal' must be an AbortSignal", pos.Line, pos.Col)
			}
			aIdx, _, _ := sv.Ty.FieldIndex("aborted")
			rIdx, _, _ := sv.Ty.FieldIndex("reason")
			ag := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", ag, sv.Ty.StructIR(), sv.Ref, aIdx))
			rg := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", rg, sv.Ty.StructIR(), sv.Ref, rIdx))
			sigARef, sigRRef = ag, rg
		default:
			return "", "", "", fmt.Errorf("%d:%d: unknown pipe option '%s'", pos.Line, pos.Col, p.Key)
		}
	}
	flagsRef = cur
	return
}

// emitStreamPipeTo implements `src.pipeTo(dest, options?)`.
func (e *Emitter) emitStreamPipeTo(srcPtr string, chunkTy Type, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 1 || len(args) > 2 {
		return Value{}, fmt.Errorf("%d:%d: pipeTo(dest, options?) expects 1–2 arguments", pos.Line, pos.Col)
	}
	e.ensureStreamPipeRuntime()
	dv, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	if !dv.Ty.IsWritableStream {
		return Value{}, fmt.Errorf("%d:%d: pipeTo's destination must be a WritableStream", pos.Line, pos.Col)
	}
	var opt ast.Expression
	if len(args) == 2 {
		opt = args[1]
	}
	flags, sigA, sigR, err := e.resolvePipeOptions(opt, pos)
	if err != nil {
		return Value{}, err
	}
	// Lock both ends like the spec does.
	l1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_rs_lock(ptr %s)", l1, srcPtr))
	e.streamThrowTypeError(l1, "pipeTo: source ReadableStream is already locked")
	l2 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_ws_lock(ptr %s)", l2, dv.Ref))
	e.streamThrowTypeError(l2, "pipeTo: destination WritableStream is already locked")
	decode := e.emitStreamDecodeThunk(chunkTy)
	prom := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_pipe_to(ptr %s, ptr %s, ptr %s, i64 %s, ptr %s, ptr %s)",
		prom, srcPtr, dv.Ref, decode, flags, sigA, sigR))
	pt := PromiseOf(TypeVoid)
	pt.PromiseTask = true
	return Value{Ref: prom, Ty: pt}, nil
}

// emitStreamPipeThrough implements `src.pipeThrough(transform, options?)` —
// start the pipe into the transform's writable side, return its readable.
func (e *Emitter) emitStreamPipeThrough(srcPtr string, chunkTy Type, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 1 || len(args) > 2 {
		return Value{}, fmt.Errorf("%d:%d: pipeThrough(transform, options?) expects 1–2 arguments", pos.Line, pos.Col)
	}
	e.ensureStreamPipeRuntime()
	tv, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	if !tv.Ty.IsTransformStream {
		return Value{}, fmt.Errorf("%d:%d: pipeThrough's argument must be a TransformStream", pos.Line, pos.Col)
	}
	var opt ast.Expression
	if len(args) == 2 {
		opt = args[1]
	}
	flags, sigA, sigR, err := e.resolvePipeOptions(opt, pos)
	if err != nil {
		return Value{}, err
	}
	l1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_rs_lock(ptr %s)", l1, srcPtr))
	e.streamThrowTypeError(l1, "pipeThrough: source ReadableStream is already locked")
	// transform value is the ts ctx: field 0 readable, field 1 writable.
	wGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 1", wGep, tv.Ref))
	wPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", wPtr, wGep))
	l2 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_ws_lock(ptr %s)", l2, wPtr))
	e.streamThrowTypeError(l2, "pipeThrough: the transform's writable side is already locked")
	decode := e.emitStreamDecodeThunk(chunkTy)
	ign := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_pipe_to(ptr %s, ptr %s, ptr %s, i64 %s, ptr %s, ptr %s)",
		ign, srcPtr, wPtr, decode, flags, sigA, sigR))
	rGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 0", rGep, tv.Ref))
	rPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", rPtr, rGep))
	outTy := TypeI64
	if tv.Ty.StreamOut != nil {
		outTy = *tv.Ty.StreamOut
	}
	return Value{Ref: rPtr, Ty: ReadableStreamType(outTy)}, nil
}

// emitStreamTee implements `src.tee()` — two branch streams over a shared
// one-read-at-a-time context.
func (e *Emitter) emitStreamTee(srcPtr string, chunkTy Type, pos ast.Pos) (Value, error) {
	e.ensureStreamPipeRuntime()
	l1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_rs_lock(ptr %s)", l1, srcPtr))
	e.streamThrowTypeError(l1, "tee: ReadableStream is already locked")

	fulfill := e.emitStreamFulfillThunk(chunkTy)
	decode := e.emitStreamDecodeThunk(chunkTy)
	b1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_rs_alloc(double 1.0, ptr %s)", b1, fulfill))
	b2 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_rs_alloc(double 1.0, ptr %s)", b2, fulfill))

	ctx := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 72)", ctx))
	storePtrAt := func(base string, idx int, val string) {
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %d", gep, base, idx))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", val, gep))
	}
	storePtrAt(ctx, 0, srcPtr)
	storePtrAt(ctx, 1, b1)
	storePtrAt(ctx, 2, b2)
	storePtrAt(ctx, 3, decode)
	for i := 4; i <= 8; i++ {
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %s, i64 %d", gep, ctx, i))
		e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", gep))
	}

	pullClo := e.buildBuiltinClosure("@__kml_tee_pull", ctx)
	e.storeStreamField(b1, 9, pullClo)
	e.storeStreamField(b2, 9, pullClo)
	for i, b := range []string{b1, b2} {
		env := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", env))
		g0 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 0", g0, env))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", ctx, g0))
		g1 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 1", g1, env))
		which := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %d to ptr", which, i+1))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", which, g1))
		e.storeStreamField(b, 10, e.buildBuiltinClosure("@__kml_tee_cancel", env))
		started := e.buildBuiltinClosure("@__kml_rs_started", b)
		e.emitInstr(fmt.Sprintf("call void @__kml_microtask_enqueue(ptr %s)", started))
	}

	// Result: a two-element array of branch streams.
	e.ensureMalloc()
	arr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", arr))
	s0 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 0", s0, arr))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", b1, s0))
	s1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 1", s1, arr))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", b2, s1))
	agg0 := e.freshReg()
	agg1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue { ptr, i64 } undef, ptr %s, 0", agg0, arr))
	e.emitInstr(fmt.Sprintf("%s = insertvalue { ptr, i64 } %s, i64 2, 1", agg1, agg0))
	return Value{Ref: agg1, Ty: ArrayOf(ReadableStreamType(chunkTy))}, nil
}

// emitNewTransformStream implements
// `new TransformStream<I, O>({transform?, flush?}?, writableStrategy?, readableStrategy?)`.
func (e *Emitter) emitNewTransformStream(ex *ast.NewTransformStreamExpression) (Value, error) {
	pos := ex.GetPos()
	inTy, outTy := TypeI64, TypeI64
	if ex.InType != nil {
		inTy = e.resolveType(ex.InType)
	}
	if ex.OutType != nil {
		outTy = e.resolveType(ex.OutType)
	}
	e.ensureStreamPipeRuntime()

	var transformExpr, flushExpr ast.Expression
	if ex.Transformer != nil {
		ol, ok := ex.Transformer.(*ast.ObjectLiteral)
		if !ok {
			return Value{}, fmt.Errorf("%d:%d: new TransformStream's transformer must be an object literal ({transform, flush})", pos.Line, pos.Col)
		}
		for _, p := range ol.Properties {
			switch p.Key {
			case "transform":
				transformExpr = p.Value
			case "flush":
				flushExpr = p.Value
			default:
				return Value{}, fmt.Errorf("%d:%d: unknown transformer member '%s' (expected transform, flush)", pos.Line, pos.Col, p.Key)
			}
		}
	}

	// Spec defaults: writable HWM 1, readable HWM 0 (transform on demand).
	wHwm, wSize, wByteLen, err := e.resolveStreamStrategy(ex.WritableStrategy, inTy, pos)
	if err != nil {
		return Value{}, err
	}
	if ex.WritableStrategy == nil {
		wHwm = "1.0"
	}
	rHwm, rSize, rByteLen, err := e.resolveStreamStrategy(ex.ReadableStrategy, outTy, pos)
	if err != nil {
		return Value{}, err
	}
	if ex.ReadableStrategy == nil {
		rHwm = "0.0"
	}

	fulfill := e.emitStreamFulfillThunk(outTy)
	rs := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_rs_alloc(double %s, ptr %s)", rs, rHwm, fulfill))
	ws := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_ws_alloc(double %s)", ws, wHwm))

	setSize := func(streamPtr string, store func(string, int, string), expr ast.Expression, byteLen bool, chunk Type) error {
		if expr != nil {
			userClo, err := e.streamCallbackClosure(expr, []Type{chunk}, "size algorithm", pos)
			if err != nil {
				return err
			}
			wrap, err := e.emitStreamSizeWrap(userClo.Ty, chunk, pos)
			if err != nil {
				return err
			}
			store(streamPtr, 8, e.buildBuiltinClosure(wrap, userClo.Ref))
		} else if byteLen {
			store(streamPtr, 8, e.buildBuiltinClosure(e.emitStreamByteLengthSizeWrap(), "null"))
		}
		return nil
	}
	if err := setSize(rs, e.storeStreamField, rSize, rByteLen, outTy); err != nil {
		return Value{}, err
	}
	if err := setSize(ws, e.storeWStreamField, wSize, wByteLen, inTy); err != nil {
		return Value{}, err
	}

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
	if transformExpr != nil {
		userClo, err := e.streamCallbackClosure(transformExpr, []Type{inTy, rsCtrlTy}, "transform callback", pos)
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
			return Value{}, fmt.Errorf("%d:%d: an identity TransformStream (no transform callback) requires matching input/output chunk types", pos.Line, pos.Col)
		}
		storeCtxPtr(2, "null")
	}
	if flushExpr != nil {
		userClo, err := e.streamCallbackClosure(flushExpr, []Type{rsCtrlTy}, "flush callback", pos)
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

	// Wire the writable's sink and the readable's pull to the ts machinery.
	e.storeWStreamField(ws, 9, e.buildBuiltinClosure("@__kml_ts_sink_write", ctx))
	e.storeWStreamField(ws, 10, e.buildBuiltinClosure("@__kml_ts_sink_close", ctx))
	e.storeWStreamField(ws, 11, e.buildBuiltinClosure("@__kml_ts_sink_abort", ctx))
	e.storeStreamField(rs, 9, e.buildBuiltinClosure("@__kml_ts_pull", ctx))
	e.emitInstr(fmt.Sprintf("call void @__kml_microtask_enqueue(ptr %s)", e.buildBuiltinClosure("@__kml_rs_started", rs)))
	e.emitInstr(fmt.Sprintf("call void @__kml_microtask_enqueue(ptr %s)", e.buildBuiltinClosure("@__kml_ws_started", ws)))

	i, o := inTy, outTy
	return Value{Ref: ctx, Ty: Type{IR: "ptr", IsTransformStream: true, StreamChunk: &i, StreamOut: &o}}, nil
}

// emitTransformStreamProperty implements `.readable` / `.writable`.
func (e *Emitter) emitTransformStreamProperty(ex *ast.MemberExpression, objTy Type) (Value, error) {
	tv, err := e.emitExpr(ex.Object)
	if err != nil {
		return Value{}, err
	}
	inTy, outTy := TypeI64, TypeI64
	if objTy.StreamChunk != nil {
		inTy = *objTy.StreamChunk
	}
	if objTy.StreamOut != nil {
		outTy = *objTy.StreamOut
	}
	switch ex.Property {
	case "readable":
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 0", gep, tv.Ref))
		p := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", p, gep))
		return Value{Ref: p, Ty: ReadableStreamType(outTy)}, nil
	case "writable":
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 1", gep, tv.Ref))
		p := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", p, gep))
		return Value{Ref: p, Ty: WritableStreamType(inTy)}, nil
	}
	return Value{}, fmt.Errorf("%d:%d: property '%s' is not available on a TransformStream", ex.GetPos().Line, ex.GetPos().Col, ex.Property)
}

// emitResponseBodyStream implements `res.body` (TDD-00097 Stage 4): a
// ReadableStream<Uint8Array> fed by libcurl's write callback for a still-live
// transfer (pausing at the high-water mark, unpausing on pull), or a one-chunk
// replay of the buffered body for an already-finished Response.
func (e *Emitter) emitResponseBodyStream(ex *ast.MemberExpression) (Value, error) {
	objVal, err := e.emitExpr(ex.Object)
	if err != nil {
		return Value{}, err
	}
	e.ensureFetchBodyStream()
	chunkTy := TypedArrayType("uint8")
	fulfill := e.emitStreamFulfillThunk(chunkTy)
	s := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_rs_alloc(double 1.0, ptr %s)", s, fulfill))

	pIdx, _, _ := objVal.Ty.FieldIndex("__kml_pending")
	pGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", pGep, objVal.Ty.StructIR(), objVal.Ref, pIdx))
	pending := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", pending, pGep))

	// The pull closure unpauses the transfer; started immediately via
	// microtask (pull only matters once reads park).
	e.storeStreamField(s, 9, e.buildBuiltinClosure("@__kml_fetch_body_pull", pending))
	started := e.buildBuiltinClosure("@__kml_rs_started", s)
	e.emitInstr(fmt.Sprintf("call void @__kml_microtask_enqueue(ptr %s)", started))

	bIdx, _, _ := objVal.Ty.FieldIndex("body")
	bVal := e.loadFieldValue(objVal, bIdx, TypePtr)
	lIdx, _, _ := objVal.Ty.FieldIndex("bodyLength")
	lVal := e.loadFieldValue(objVal, lIdx, TypeI64)
	actual := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_fetch_body_stream(ptr %s, ptr %s, ptr %s, i64 %s)",
		actual, pending, s, bVal.Ref, lVal.Ref))
	return Value{Ref: actual, Ty: ReadableStreamType(chunkTy)}, nil
}

// emitNewCompressionStream implements `new CompressionStream(format)` /
// `new DecompressionStream(format)` (TDD-00097 Stage 6): a TransformStream
// value whose transform/flush closures are the native zlib pump
// (runtime_streams_zlib.go). Format must be a string literal.
func (e *Emitter) emitNewCompressionStream(ex *ast.NewCompressionStreamExpression) (Value, error) {
	pos := ex.GetPos()
	lit, ok := ex.Format.(*ast.StringLiteral)
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: CompressionStream/DecompressionStream's format must be a string literal (\"gzip\", \"deflate\", \"deflate-raw\")", pos.Line, pos.Col)
	}
	// windowBits per format; inflate uses 15+32 auto-detection for the two
	// wrapped formats, raw uses -15 on both sides.
	var defWB, infWB int
	switch lit.Value {
	case "gzip":
		defWB, infWB = 31, 47
	case "deflate":
		defWB, infWB = 15, 47
	case "deflate-raw":
		defWB, infWB = -15, -15
	default:
		return Value{}, fmt.Errorf("%d:%d: unsupported compression format %q (expected \"gzip\", \"deflate\", or \"deflate-raw\")", pos.Line, pos.Col, lit.Value)
	}
	wb := defWB
	mode := 0
	if ex.Decompress {
		wb = infWB
		mode = 1
	}

	e.ensureZlibStreamRuntime()
	u8 := TypedArrayType("uint8")
	fulfill := e.emitStreamFulfillThunk(u8)
	rs := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_rs_alloc(double 0.0, ptr %s)", rs, fulfill))
	ws := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_ws_alloc(double 1.0)", ws))

	zctx := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_zs_init(i64 %d, i64 %d)", zctx, mode, wb))
	okReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", okReg, zctx))
	okI := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = zext i1 %s to i64", okI, okReg))
	e.streamThrowTypeError(okI, "zlib initialization failed")
	// Patch the readable side into the zlib context.
	rGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 1", rGep, zctxStructIR, zctx))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", rs, rGep))

	ctx := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 72)", ctx))
	storeCtxPtr := func(idx int, val string) {
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %d", gep, ctx, idx))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", val, gep))
	}
	storeCtxPtr(0, rs)
	storeCtxPtr(1, ws)
	storeCtxPtr(2, e.buildBuiltinClosure("@__kml_zs_transform", zctx))
	storeCtxPtr(3, e.buildBuiltinClosure("@__kml_zs_flush", zctx))
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

	i8in, i8out := u8, u8
	return Value{Ref: ctx, Ty: Type{IR: "ptr", IsTransformStream: true, StreamChunk: &i8in, StreamOut: &i8out}}, nil
}
