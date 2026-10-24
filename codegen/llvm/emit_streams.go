// emit_streams.go — WHATWG ReadableStream codegen (TDD-00097 Stage 1):
// `new ReadableStream<T>({start, pull, cancel}, strategy?)` construction,
// stream/reader/controller method dispatch, `ReadableStream.from(array)`, and
// `for await...of` over a stream/reader. Mirrors emit_eventemitter.go's split:
// Go codegen here, the hand-written state machine in runtime_streams.go.
//
// The chunk type T is marshalled through the queue as two raw i64 words (the
// promise v0/v1 idiom): scalars/pointers use one word; an array is its header
// pointer and its length, so a chunk read is the chunk enqueued. read() promises are settled by a per-construction-site
// "fulfill thunk" this file emits — it rebuilds the typed {value, done} record
// so the runtime stays type-agnostic.
package llvm

import (
	"fmt"
	"slices"
	"strings"

	"KlainMainLang/ast"
)

// streamChunkWords marshals an already-evaluated chunk value into its two
// queue words (v0, v1) — storePromiseValue's register-level sibling.
func (e *Emitter) streamChunkWords(val Value) (string, string) {
	if val.Ty.IsArray() {
		l := e.arrayLen(val)
		pi := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = ptrtoint ptr %s to i64", pi, val.Ref))
		return pi, l
	}
	if isNullableScalar(val.Ty) {
		present, payload := e.nullableScalarAggParts(val)
		presI64 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = zext i1 %s to i64", presI64, present))
		return e.promiseBitsOf(payload), presI64
	}
	return e.promiseBitsOf(val), "0"
}

// streamChunkFromWords rebuilds a chunk value of type ty from its two queue
// words — loadPromiseValue's register-level sibling.
func (e *Emitter) streamChunkFromWords(v0, v1 string, ty Type) Value {
	if ty.IsArray() {
		h := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", h, v0))
		return Value{Ref: h, Ty: ty}
	}
	if isNullableScalar(ty) {
		return e.nullableScalarFromWords(v0, v1, ty)
	}
	return e.promiseValFromBits(v0, ty)
}

// emitRsAlloc allocates a readable stream into reg dst: its high-water mark,
// the fulfill routine of its chunk type, and the routine that boxes one of
// its chunks for a reader reading them as `any` (none when they already are).
func (e *Emitter) emitRsAlloc(dst, hwm, fulfill string, chunkTy Type) {
	box := "null"
	if !chunkTy.IsDynamic() {
		box = e.emitStreamBoxThunk(chunkTy)
	}
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_rs_alloc(double %s, ptr %s, ptr %s)", dst, hwm, fulfill, box))
}

// emitStreamBoxThunk emits `i64 (i64 %v0, i64 %v1)`: a chunk of chunkTy
// from its queue words, boxed.
func (e *Emitter) emitStreamBoxThunk(chunkTy Type) string {
	restore := e.beginThunkEmit()
	chunk := e.streamChunkFromWords("%v0", "%v1", chunkTy)
	boxed, err := e.emitBoxValue(chunk)
	if err != nil {
		restore()
		return "null"
	}
	e.emitInstr(fmt.Sprintf("ret i64 %s", boxed.Ref))
	body := e.allocas.String() + e.body.String()
	restore()
	return e.defineContentNamed("@__kml_rs_box.", "i64", "i64 %v0, i64 %v1", body)
}

// emitRsRead emits a read() of stream ptr s by a reader of chunkTy: a reader
// reading `any` takes the producer's chunks boxed.
func (e *Emitter) emitRsRead(s string, chunkTy Type) string {
	prom := e.freshReg()
	if chunkTy.IsDynamic() {
		ff := e.emitStreamFulfillThunk(chunkTy)
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_rs_read_any(ptr %s, ptr %s)", prom, s, ff))
		return prom
	}
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_rs_read(ptr %s)", prom, s))
	return prom
}

// streamReadResultType is `reader.read()`'s own record shape — `{value: T |
// undefined, done: bool}`. Unlike a generator's `.next()` result (whose `value`
// stays bare `T`, deliberately — tsc types it `any`, ADR-00781), a closed
// stream's read() yields a real `undefined` value, so the `value` field is
// flipped to `T | undefined` via the shared sentinel (TDD-00196): a scalar
// chunk rides the `{ i1, T }` optional, a pointer chunk the null pointer. An
// array/tuple/dynamic chunk passes through unchanged — undefinedableElem leaves
// those bare (ADR-00246: an array slot has no spare absent state), so an
// array-chunk stream keeps its documented zero-on-close behaviour.
func streamReadResultType(chunkTy Type) Type {
	return ObjectType([]Field{
		{Name: "value", Ty: undefinedableElem(chunkTy)},
		{Name: "done", Ty: TypeBool},
	})
}

// buildStreamReadRecord mallocs a {value, done} record — buildGenNextResult's
// sibling. The `value` field is stored in its (possibly `T | undefined`)
// field type: a scalar chunk becomes a `{ i1, T }` aggregate whose presence bit
// is `present` (= not done), a pointer/array chunk stores its register directly
// (a done read's rebuilt chunk is already the null/zero that reads as absent).
func (e *Emitter) buildStreamReadRecord(resultTy, chunkTy Type, chunk Value, doneI1, presentI1 string) string {
	e.ensureMalloc()
	rec := e.freshReg()
	e.emitObjMallocInto(rec, resultTy)
	vIdx, valTy, _ := resultTy.FieldIndex("value")
	vGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", vGep, resultTy.StructIR(), rec, vIdx))
	if isNullableScalar(valTy) {
		agg := e.makeNullableScalarAgg(valTy, presentI1, chunk.Ref)
		e.heapStore(nullableScalarStorageIR(valTy), agg, vGep, 8)
	} else if valTy.IsArray() {
		e.storeArrayFieldHeader(vGep, chunk) // header-ptr slot (TDD-00213 S2)
	} else {
		e.heapStore(StructFieldIR(valTy), chunk.Ref, vGep, 8)
	}
	dIdx, _, _ := resultTy.FieldIndex("done")
	dGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", dGep, resultTy.StructIR(), rec, dIdx))
	e.heapStore("i1", doneI1, dGep, 1)
	return rec
}

// loadStreamResultValue loads a read-result record's `value` field back to a
// bare chunk Value — the read-side inverse of buildStreamReadRecord's store.
// A `T | undefined` scalar field is demoted to its payload (a present value on
// the done:false path this is only called on); a pointer/array field loads as
// the bare chunk type directly. Shared by the for-await loop and the pipe/tee
// decode thunk.
func (e *Emitter) loadStreamResultValue(recPtr string, resultTy, chunkTy Type) Value {
	vIdx, valTy, _ := resultTy.FieldIndex("value")
	vGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", vGep, resultTy.StructIR(), recPtr, vIdx))
	if valTy.IsArray() {
		v := e.loadArraySlotAggregate(vGep, chunkTy) // header-ptr slot (TDD-00213 S2)
		return v
	}
	loaded := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", loaded, StructFieldIR(valTy), vGep, chunkTy.Align()))
	if isNullableScalar(valTy) {
		return e.nullableScalarPayloadOf(Value{Ref: loaded, Ty: valTy})
	}
	return Value{Ref: loaded, Ty: chunkTy}
}

// emitStreamFulfillThunk emits `void (ptr %p, i64 %v0, i64 %v1, i64 %done)`,
// the routine the runtime settles read() promises through, and returns its
// symbol (named by its content): rebuild the typed chunk, build the
// {value,done} record, store it as the promise's value, settle fulfilled.
func (e *Emitter) emitStreamFulfillThunk(chunkTy Type) string {
	resultTy := streamReadResultType(chunkTy)

	restore := e.beginThunkEmit()
	chunk := e.streamChunkFromWords("%v0", "%v1", chunkTy)
	doneI1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %%done, 0", doneI1))
	presentI1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %%done, 0", presentI1))
	rec := e.buildStreamReadRecord(resultTy, chunkTy, chunk, doneI1, presentI1)
	e.storePromiseValue("%p", Value{Ref: rec, Ty: resultTy})
	e.emitInstr("call void @__kml_promise_settle(ptr %p, i64 1)")
	e.emitInstr("ret void")
	body := e.allocas.String() + e.body.String()
	restore()

	return e.defineContentNamed("@__kml_rs_fulfill.", "void", "ptr %p, i64 %v0, i64 %v1, i64 %done", body)
}

// streamCallbackClosure evaluates an underlying-source callback expression
// (arrow / function expression only — a later invocation needs a real
// closure header) with the given param hints.
func (e *Emitter) streamCallbackClosure(arg ast.Expression, hints []Type, what string, pos ast.Pos) (Value, error) {
	var val Value
	var err error
	switch fn := arg.(type) {
	case *ast.ArrowFunction:
		val, err = e.emitArrowFunctionWithHints(fn, hints)
	case *ast.FunctionExpression:
		val, err = e.emitFunctionExpression(fn, hints)
	default:
		return Value{}, fmt.Errorf("%d:%d: a ReadableStream's %s must be an arrow function or function expression", pos.Line, pos.Col, what)
	}
	if err != nil {
		return Value{}, err
	}
	if !val.Ty.IsFunc() {
		return Value{}, fmt.Errorf("%d:%d: a ReadableStream's %s must be a function", pos.Line, pos.Col, what)
	}
	return val, nil
}

// callbackReturnsPromise reports whether a source callback's static return
// type is a Promise (an async callback) — the pull/cancel wrappers forward
// that promise to the runtime, which reacts to its settlement.
func callbackReturnsPromise(ty Type) bool {
	return ty.FuncRetType != nil && ty.FuncRetType.IsPromise()
}

// emitStreamPullWrap emits `ptr @__kml_rs_pullwrap_N(ptr %env)` where env is a
// malloc'd {userClosure, stream} pair: invoke the user's pull/start-shaped
// callback with the controller (the stream pointer itself) and return its
// promise, or null for a synchronous callback.
func (e *Emitter) emitStreamPullWrap(userTy Type) string {
	isAsync := callbackReturnsPromise(userTy)
	hasParam := len(userTy.FuncParams) > 0

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
	if hasParam {
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

	fn := e.defineContentNamed("@__kml_rs_pullwrap.", "ptr", "ptr %env", body)
	return fn
}

// emitStreamCancelWrap emits `ptr @__kml_rs_cancelwrap_N(ptr %env, i64
// %reason)` where env is the user closure header directly. V1 supports a
// zero-parameter cancel callback (validated by the caller); the reason word is
// accepted for ABI stability but not forwarded.
func (e *Emitter) emitStreamCancelWrap(userTy Type) string {
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

	fn := e.defineContentNamed("@__kml_rs_cancelwrap.", "ptr", "ptr %env, i64 %reason", body)
	return fn
}

// emitStreamSizeWrap emits `double @__kml_rs_sizewrap_N(ptr %env, i64 %v0, i64
// %v1)` where env is the user size closure header: rebuild the chunk, invoke
// the size algorithm, convert its number result to double.
func (e *Emitter) emitStreamSizeWrap(userTy, chunkTy Type, pos ast.Pos) (string, error) {
	retTy := TypeI64
	if userTy.FuncRetType != nil {
		retTy = *userTy.FuncRetType
	}
	if retTy.IR != "i64" && retTy.IR != "double" {
		return "", fmt.Errorf("%d:%d: a queuing strategy's size() must return a number", pos.Line, pos.Col)
	}

	restore := e.beginThunkEmit()
	fp := e.freshReg()
	fpp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %%env, i32 0, i32 0", fpp))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", fp, fpp))
	ep := e.freshReg()
	epp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %%env, i32 0, i32 1", epp))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", ep, epp))
	var args, sig string
	if chunkTy.IsArray() {
		hdr := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %%v0 to ptr", hdr))
		args = fmt.Sprintf("ptr %s, ptr %s, i64 %%v1", ep, hdr)
		sig = "ptr, ptr, i64"
	} else {
		chunk := e.streamChunkFromWords("%v0", "%v1", chunkTy)
		args = fmt.Sprintf("ptr %s, %s %s", ep, chunkTy.IR, chunk.Ref)
		sig = "ptr, " + chunkTy.IR
	}
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call %s (%s) %s(%s)", r, retTy.IR, sig, fp, args))
	if retTy.IR == "i64" {
		d := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = sitofp i64 %s to double", d, r))
		e.emitInstr(fmt.Sprintf("ret double %s", d))
	} else {
		e.emitInstr(fmt.Sprintf("ret double %s", r))
	}
	body := e.allocas.String() + e.body.String()
	restore()

	fn := e.defineContentNamed("@__kml_rs_sizewrap.", "double", "ptr %env, i64 %v0, i64 %v1", body)
	return fn, nil
}

// emitStreamByteLengthSizeWrap emits the built-in ByteLengthQueuingStrategy
// size: the chunk's element count (its {ptr,i64} length word) — for the byte
// chunks (Uint8Array) it is meant for, that IS the byteLength.
func (e *Emitter) emitStreamByteLengthSizeWrap() string {
	return e.defineContentNamed("@__kml_rs_sizewrap.", "double", "ptr %env, i64 %v0, i64 %v1", "  %d = sitofp i64 %v1 to double\n  ret double %d\n")
}

// storeStreamField stores a ptr into the rstream struct field at idx.
func (e *Emitter) storeStreamField(s string, idx int, val string) {
	gep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, rstreamStructIR, s, idx))
	e.heapStore("ptr", val, gep, 8)
}

// resolveStreamStrategy destructures a queuing-strategy argument —
// {highWaterMark, size}, or a CountQueuingStrategy/ByteLengthQueuingStrategy
// construction — into the evaluated high-water-mark ref (a double), the size
// callback expression (nil for none), and whether the built-in byte-length
// size applies. Shared by both stream constructors (TDD-00097 Stages 1–2).
func (e *Emitter) resolveStreamStrategy(strategy ast.Expression, chunkTy Type, pos ast.Pos) (string, ast.Expression, bool, error) {
	var hwmExpr, sizeExpr ast.Expression
	byteLengthStrategy := false
	if strategy != nil {
		strat := strategy
		if ne, ok := strat.(*ast.NewExpression); ok && (ne.ClassName == "CountQueuingStrategy" || ne.ClassName == "ByteLengthQueuingStrategy") {
			byteLengthStrategy = ne.ClassName == "ByteLengthQueuingStrategy"
			if len(ne.Args) != 1 {
				return "", nil, false, fmt.Errorf("%d:%d: new %s expects one {highWaterMark} argument", pos.Line, pos.Col, ne.ClassName)
			}
			strat = ne.Args[0]
		}
		ol, ok := strat.(*ast.ObjectLiteral)
		if !ok {
			return "", nil, false, fmt.Errorf("%d:%d: a queuing strategy must be an object literal ({highWaterMark, size}) or a CountQueuingStrategy/ByteLengthQueuingStrategy", pos.Line, pos.Col)
		}
		for _, p := range ol.Properties {
			switch p.Key {
			case "highWaterMark":
				hwmExpr = p.Value
			case "size":
				sizeExpr = p.Value
			default:
				return "", nil, false, fmt.Errorf("%d:%d: unknown queuing strategy member '%s'", pos.Line, pos.Col, p.Key)
			}
		}
		if byteLengthStrategy && sizeExpr != nil {
			return "", nil, false, fmt.Errorf("%d:%d: ByteLengthQueuingStrategy defines its own size()", pos.Line, pos.Col)
		}
		if byteLengthStrategy && !chunkTy.IsArray() {
			return "", nil, false, fmt.Errorf("%d:%d: ByteLengthQueuingStrategy requires byte-array chunks (e.g. Uint8Array)", pos.Line, pos.Col)
		}
	}
	hwmRef := "1.0"
	if hwmExpr != nil {
		hv, err := e.emitExpr(hwmExpr)
		if err != nil {
			return "", nil, false, err
		}
		hv = e.convert(hv, TypeF64)
		hwmRef = hv.Ref
	}
	return hwmRef, sizeExpr, byteLengthStrategy, nil
}

// emitNewReadableStream implements `new ReadableStream<T>(source?, strategy?)`.
func (e *Emitter) emitNewReadableStream(ex *ast.NewReadableStreamExpression) (Value, error) {
	pos := ex.GetPos()
	chunkTy := TypeAny // ReadableStream<R = any>
	if ex.ChunkType != nil {
		chunkTy = e.resolveType(ex.ChunkType)
	}
	controllerTy := RSControllerType(chunkTy)
	e.ensureStreamRuntime()

	// Destructure the underlying source at compile time.
	var startExpr, pullExpr, cancelExpr ast.Expression
	if ex.Source != nil {
		ol, ok := ex.Source.(*ast.ObjectLiteral)
		if !ok {
			return Value{}, fmt.Errorf("%d:%d: new ReadableStream's underlying source must be an object literal ({start, pull, cancel})", pos.Line, pos.Col)
		}
		members, unknown := literalMembers(ol, "start", "pull", "cancel")
		if unknown == "type" || unknown == "autoAllocateChunkSize" {
			return Value{}, fmt.Errorf("%d:%d: byte streams (underlying source '%s') are not supported — use a default stream of Uint8Array chunks", pos.Line, pos.Col, unknown)
		}
		if unknown != "" {
			return Value{}, fmt.Errorf("%d:%d: unknown underlying source member '%s' (expected start, pull, cancel)", pos.Line, pos.Col, unknown)
		}
		startExpr, pullExpr, cancelExpr = members["start"], members["pull"], members["cancel"]
	}

	hwmRef, sizeExpr, byteLengthStrategy, err := e.resolveStreamStrategy(ex.Strategy, chunkTy, pos)
	if err != nil {
		return Value{}, err
	}

	fulfillFn := e.emitStreamFulfillThunk(chunkTy)
	s := e.freshReg()
	e.emitRsAlloc(s, hwmRef, fulfillFn, chunkTy)

	if sizeExpr != nil {
		userClo, err := e.streamCallbackClosure(sizeExpr, []Type{chunkTy}, "size algorithm", pos)
		if err != nil {
			return Value{}, err
		}
		wrap, err := e.emitStreamSizeWrap(userClo.Ty, chunkTy, pos)
		if err != nil {
			return Value{}, err
		}
		e.storeStreamField(s, 8, e.buildBuiltinClosure(wrap, userClo.Ref))
	} else if byteLengthStrategy {
		wrap := e.emitStreamByteLengthSizeWrap()
		e.storeStreamField(s, 8, e.buildBuiltinClosure(wrap, "null"))
	}

	if pullExpr != nil {
		userClo, err := e.streamCallbackClosure(pullExpr, []Type{controllerTy}, "pull callback", pos)
		if err != nil {
			return Value{}, err
		}
		if len(userClo.Ty.FuncParams) > 1 {
			return Value{}, fmt.Errorf("%d:%d: a pull callback takes at most one (controller) parameter", pos.Line, pos.Col)
		}
		wrap := e.emitStreamPullWrap(userClo.Ty)
		env := e.freshReg()
		e.heapMallocInto(env, "16")
		e0 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 0", e0, env))
		e.heapStore("ptr", userClo.Ref, e0, 8)
		e1 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 1", e1, env))
		e.heapStore("ptr", s, e1, 8)
		e.storeStreamField(s, 9, e.buildBuiltinClosure(wrap, env))
	}

	if cancelExpr != nil {
		userClo, err := e.streamCallbackClosure(cancelExpr, nil, "cancel callback", pos)
		if err != nil {
			return Value{}, err
		}
		if len(userClo.Ty.FuncParams) > 0 {
			return Value{}, fmt.Errorf("%d:%d: a cancel callback with a reason parameter is not supported yet — use a zero-parameter cancel", pos.Line, pos.Col)
		}
		wrap := e.emitStreamCancelWrap(userClo.Ty)
		e.storeStreamField(s, 10, e.buildBuiltinClosure(wrap, userClo.Ref))
	}

	// start(controller) runs synchronously in the constructor; the started
	// flag (which gates the first pull) is set a microtask later — or when an
	// async start's promise settles — matching spec timing.
	startedClo := e.buildBuiltinClosure("@__kml_rs_started", s)
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

	return Value{Ref: s, Ty: ReadableStreamType(chunkTy)}, nil
}

// newStreamHandle is a reader or writer of the stream at stream: its own
// object (getReader() answers a new one each time, as in Node), the stream
// after its host header word (TDD-00230 P3.3).
func (e *Emitter) newStreamHandle(stream string, ty Type) Value {
	e.ensureMalloc()
	base, h := e.freshReg(), e.freshReg()
	e.heapMallocInto(base, "32")
	e.heapStore("i64", fmt.Sprint(e.hostHeaderWord(ty)), base, 8)
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 8", h, base))
	e.heapStore("ptr", stream, h, 8)
	for _, off := range []int{8, 16} {
		p := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %d", p, h, off))
		e.heapStore("ptr", "null", p, 8)
	}
	return Value{Ref: h, Ty: ty}
}

// A reader's or writer's handle: { stream, closed, ready } after its header.
// The stream slot is null once the lock is released; the promise slots then
// hold the released handle's own closed (and a writer's ready).
const (
	handleClosedSlot = 1
	handleReadySlot  = 2
)

// handleSlot loads slot i of handle h.
func (e *Emitter) handleSlot(h string, i int) string {
	p, v := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %d", p, h, i))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", v, p))
	return v
}

// emitIfReleased emits bound (the handle's stream is attached) and released
// (its lock was released) as two paths and joins their results, which are
// both ptr-typed or both void.
func (e *Emitter) emitIfReleased(stream string, bound, released func() (Value, error)) (Value, error) {
	isNull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, stream))
	relL, boundL, joinL := e.freshLabel("handle.released"), e.freshLabel("handle.bound"), e.freshLabel("handle.join")
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNull, relL, boundL))
	e.emitLabel(boundL)
	bv, err := bound()
	if err != nil {
		// A caller that recovers from the error (a host method adapter
		// throwing instead) continues in a block both paths reach.
		e.emitTerminator(fmt.Sprintf("br label %%%s", relL))
		e.emitLabel(relL)
		return Value{}, err
	}
	isPtr := bv.Ty.IR == "ptr" && bv.Ref != ""
	if isPtr {
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", bv.Ref, slot))
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", joinL))
	e.emitLabel(relL)
	rv, err := released()
	if err != nil {
		return Value{}, err
	}
	if isPtr && rv.Ref != "" {
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", rv.Ref, slot))
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", joinL))
	e.emitLabel(joinL)
	if !isPtr {
		return bv, nil
	}
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", r, slot))
	return Value{Ref: r, Ty: bv.Ty}, nil
}

// emitStreamStateError builds Node's `TypeError [ERR_INVALID_STATE]` with
// msg and returns its bits.
func (e *Emitter) emitStreamStateError(msg string) string {
	errPtr := e.emitStreamStateErrorPtr(msg)
	bits := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = ptrtoint ptr %s to i64", bits, errPtr))
	return bits
}

// emitStreamStateErrorPtr is emitStreamStateError's error object.
func (e *Emitter) emitStreamStateErrorPtr(msg string) string {
	e.ensureExceptionHelpers()
	return e.buildErrorObjWithCode(errorKindIDs["TypeError"], e.internString(msg), e.internString("TypeError"), e.internString("ERR_INVALID_STATE"), "0.0", "null")
}

// emitRejectedStreamPromise is a Promise rejected with Node's stream state
// error msg.
func (e *Emitter) emitRejectedStreamPromise(msg string, ty Type) Value {
	e.ensurePromiseSettle()
	bits := e.emitStreamStateError(msg)
	p := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_task_alloc_promise()", p))
	e.emitInstr(fmt.Sprintf("call void @__kml_promise_reject_err(ptr %s, i64 %s)", p, bits))
	return Value{Ref: p, Ty: ty}
}

// streamOfHandle is the stream a stream-typed value works on: a reader's or
// writer's stream, any other's own pointer.
func (e *Emitter) streamOfHandle(ty Type, ref string) string {
	if !ty.IsStreamReader() && !ty.IsStreamWriter() {
		return ref
	}
	s := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", s, ref))
	return s
}

// resolveStreamForCall resolves a stream/reader/controller receiver expression
// to its loaded heap pointer.
func (e *Emitter) resolveStreamForCall(objExpr ast.Expression, pos ast.Pos) (Type, string, error) {
	ty, _, ptr, err := e.resolveStreamHandle(objExpr, pos)
	return ty, ptr, err
}

// resolveStreamHandle is resolveStreamForCall that also gives the receiver
// itself (a reader's handle).
func (e *Emitter) resolveStreamHandle(objExpr ast.Expression, pos ast.Pos) (Type, string, string, error) {
	if id, ok := objExpr.(*ast.Identifier); ok {
		sym, found := e.lookup(id.Name)
		if found && (sym.Ty.IsReadableStream() || sym.Ty.IsStreamReader() || sym.Ty.IsRSController()) {
			ptr := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", ptr, sym.Ptr))
			return sym.Ty, ptr, e.streamOfHandle(sym.Ty, ptr), nil
		}
	}
	val, err := e.emitExpr(objExpr)
	if err != nil {
		return Type{}, "", "", err
	}
	if !val.Ty.IsReadableStream() && !val.Ty.IsStreamReader() && !val.Ty.IsRSController() {
		return Type{}, "", "", fmt.Errorf("%d:%d: value is not a ReadableStream/reader/controller", pos.Line, pos.Col)
	}
	return val.Ty, val.Ref, e.streamOfHandle(val.Ty, val.Ref), nil
}

// streamThrowTypeError throws a TypeError with msg when flagReg (an i64) is 0.
func (e *Emitter) streamThrowTypeError(flagReg, msg string) {
	e.ensureExceptionHelpers()
	bad := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", bad, flagReg))
	throwL := e.freshLabel("rs.throw")
	okL := e.freshLabel("rs.ok")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", bad, throwL, okL))
	e.emitLabel(throwL)
	var errPtr string
	if strings.HasPrefix(msg, "Invalid state: ") {
		// Node's stream state errors carry code ERR_INVALID_STATE.
		errPtr = e.buildErrorObjWithCode(errorKindIDs["TypeError"], e.internString(msg), e.internString("TypeError"), e.internString("ERR_INVALID_STATE"), "0.0", "null")
	} else {
		errPtr = e.buildErrorObj(errorKindIDs["TypeError"], e.internString(msg), e.internString("TypeError"))
	}
	e.excThrow(errPtr)
	e.emitTerminator("unreachable")
	e.emitLabel(okL)
}

// streamReasonBits evaluates an optional cancel/error reason argument to its
// error-pointer bits (0 when absent).
func (e *Emitter) streamReasonBits(args []ast.Expression, pos ast.Pos) (string, error) {
	if len(args) == 0 {
		return "0", nil
	}
	v, err := e.emitExpr(args[0])
	if err != nil {
		return "", err
	}
	errPtr, err := e.errorPtrFromValue(v)
	if err != nil {
		return "", err
	}
	bits := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = ptrtoint ptr %s to i64", bits, errPtr))
	return bits, nil
}

// emitStreamMethodCall dispatches stream/reader/controller method calls.
func (e *Emitter) emitStreamMethodCall(objExpr ast.Expression, method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	ty, handle, ptr, err := e.resolveStreamHandle(objExpr, pos)
	if err != nil {
		return Value{}, err
	}
	e.ensureStreamRuntime()
	chunkTy := TypeI64
	if ty.StreamChunk != nil {
		chunkTy = *ty.StreamChunk
	}

	c := &streamCall{ptr: ptr, handle: handle, chunkTy: chunkTy, method: method, args: args, pos: pos}
	if f, ok := streamReaderMethods[method]; ok && ty.IsStreamReader() {
		// A released reader is attached to no stream (Node's state errors).
		return e.emitIfReleased(ptr, func() (Value, error) { return f(e, c) }, func() (Value, error) {
			if method == "releaseLock" {
				return Value{Ty: TypeVoid}, nil
			}
			pt := PromiseOf(TypeVoid)
			if method == "read" {
				pt = PromiseOf(streamReadResultType(chunkTy))
			}
			pt.PromiseTask = true
			return e.emitRejectedStreamPromise("Invalid state: The reader is not attached to a stream", pt), nil
		})
	}
	var table map[string]func(e *Emitter, c *streamCall) (Value, error)
	var what string
	switch {
	case ty.IsReadableStream():
		table, what = readableStreamMethods, "ReadableStream"
	case ty.IsStreamReader():
		table, what = streamReaderMethods, "stream reader"
	case ty.IsRSController():
		table, what = rsControllerMethods, "stream controller"
	default:
		return Value{}, fmt.Errorf("%d:%d: unknown stream method '%s'", pos.Line, pos.Col, method)
	}
	if f, ok := table[method]; ok {
		return f(e, c)
	}
	return Value{}, fmt.Errorf("%d:%d: unknown %s method '%s'", pos.Line, pos.Col, what, method)
}

// streamCall is a stream method call: the receiver's stream, its chunk type
// and the call's arguments.
type streamCall struct {
	ptr     string
	handle  string // a reader's or writer's own handle (releaseLock)
	chunkTy Type
	method  string
	args    []ast.Expression
	pos     ast.Pos
}

// readableStreamMethods, streamReaderMethods and rsControllerMethods are a
// ReadableStream's, its default reader's and its controller's methods, by
// name.
var readableStreamMethods, streamReaderMethods, rsControllerMethods map[string]func(e *Emitter, c *streamCall) (Value, error)

func init() {
	readableStreamMethods = map[string]func(e *Emitter, c *streamCall) (Value, error){
		"getReader": (*Emitter).streamGetReader,
		"values":    (*Emitter).streamGetReader,
		"cancel":    (*Emitter).streamCancel,
		"pipeTo": func(e *Emitter, c *streamCall) (Value, error) {
			return e.emitStreamPipeTo(c.ptr, c.chunkTy, c.args, c.pos)
		},
		"pipeThrough": func(e *Emitter, c *streamCall) (Value, error) {
			return e.emitStreamPipeThrough(c.ptr, c.chunkTy, c.args, c.pos)
		},
		"tee": func(e *Emitter, c *streamCall) (Value, error) {
			return e.emitStreamTee(c.ptr, c.chunkTy, c.pos)
		},
	}
	streamReaderMethods = map[string]func(e *Emitter, c *streamCall) (Value, error){
		"read":        (*Emitter).streamRead,
		"releaseLock": (*Emitter).streamReleaseLock,
		"cancel":      (*Emitter).streamCancel,
	}
	rsControllerMethods = map[string]func(e *Emitter, c *streamCall) (Value, error){
		"enqueue": (*Emitter).streamEnqueue,
		"close":   (*Emitter).streamClose,
		"error":   (*Emitter).streamError,
	}
}

func (e *Emitter) streamGetReader(c *streamCall) (Value, error) {
	if c.method == "getReader" && len(c.args) > 0 {
		return Value{}, fmt.Errorf("%d:%d: getReader() options (BYOB mode) are not supported", c.pos.Line, c.pos.Col)
	}
	ok := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_rs_lock(ptr %s)", ok, c.ptr))
	e.streamThrowTypeError(ok, "Invalid state: ReadableStream is locked")
	return e.newStreamHandle(c.ptr, StreamReaderType(c.chunkTy)), nil
}

// streamCancel is a stream's and its reader's cancel(reason).
func (e *Emitter) streamCancel(c *streamCall) (Value, error) {
	bits, err := e.streamReasonBits(c.args, c.pos)
	if err != nil {
		return Value{}, err
	}
	prom := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_rs_cancel(ptr %s, i64 %s)", prom, c.ptr, bits))
	pt := PromiseOf(TypeVoid)
	pt.PromiseTask = true
	return Value{Ref: prom, Ty: pt}, nil
}

func (e *Emitter) streamRead(c *streamCall) (Value, error) {
	prom := e.emitRsRead(c.ptr, c.chunkTy)
	pt := PromiseOf(streamReadResultType(c.chunkTy))
	pt.PromiseTask = true
	return Value{Ref: prom, Ty: pt}, nil
}

// streamReleaseLock is reader.releaseLock(): the reader's closed and pending
// reads reject (__kml_rs_release) and it detaches from its stream.
func (e *Emitter) streamReleaseLock(c *streamCall) (Value, error) {
	closedErr := e.emitStreamStateError("Invalid state: Reader released")
	readErr := e.emitStreamStateError("Invalid state: Releasing reader")
	e.emitInstr(fmt.Sprintf("call void @__kml_rs_release(ptr %s, ptr %s, i64 %s, i64 %s)", c.ptr, c.handle, closedErr, readErr))
	e.heapStore("ptr", "null", c.handle, 8)
	return Value{Ty: TypeVoid}, nil
}

func (e *Emitter) streamEnqueue(c *streamCall) (Value, error) {
	if len(c.args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: enqueue() expects 1 chunk argument", c.pos.Line, c.pos.Col)
	}
	cv, err := e.emitExprWithObjectHint(c.args[0], c.chunkTy)
	if err != nil {
		return Value{}, err
	}
	cv = e.convert(cv, c.chunkTy)
	v0, v1 := e.streamChunkWords(cv)
	ok := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_rs_enqueue(ptr %s, i64 %s, i64 %s)", ok, c.ptr, v0, v1))
	e.streamThrowTypeError(ok, "cannot enqueue on a closed or errored ReadableStream")
	return Value{Ty: TypeVoid}, nil
}

func (e *Emitter) streamClose(c *streamCall) (Value, error) {
	ok := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_rs_close(ptr %s)", ok, c.ptr))
	e.streamThrowTypeError(ok, "cannot close an already-closed or errored ReadableStream")
	return Value{Ty: TypeVoid}, nil
}

func (e *Emitter) streamError(c *streamCall) (Value, error) {
	bits, err := e.streamReasonBits(c.args, c.pos)
	if err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("call void @__kml_rs_error(ptr %s, i64 %s)", c.ptr, bits))
	return Value{Ty: TypeVoid}, nil
}

// emitReadableStreamFrom implements `ReadableStream.from(array)` — a closed
// stream pre-filled with the array's elements.
func (e *Emitter) emitReadableStreamFrom(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: ReadableStream.from() expects 1 argument", pos.Line, pos.Col)
	}
	arrTy := e.inferExprType(args[0])
	if !arrTy.IsArray() {
		return Value{}, fmt.Errorf("%d:%d: ReadableStream.from() supports arrays (an async-iterable source is not supported yet)", pos.Line, pos.Col)
	}
	elemTy := *arrTy.ElemType
	e.ensureStreamRuntime()

	ptrReg, lenReg, _, err := e.resolveArrayForHOF(args[0], pos)
	if err != nil {
		return Value{}, err
	}
	fulfillFn := e.emitStreamFulfillThunk(elemTy)
	s := e.freshReg()
	e.emitRsAlloc(s, "1.0", fulfillFn, elemTy)

	idxA := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxA))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idxA))
	condL := e.freshLabel("rs.from.cond")
	bodyL := e.freshLabel("rs.from.body")
	doneL := e.freshLabel("rs.from.done")
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	idx := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idx, idxA))
	end := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp sge i64 %s, %s", end, idx, lenReg))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", end, doneL, bodyL))
	e.emitLabel(bodyL)
	gep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", gep, elemTy.IR, ptrReg, idx))
	elem := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", elem, elemTy.IR, gep, elemTy.Align()))
	v0, v1 := e.streamChunkWords(Value{Ref: elem, Ty: elemTy})
	tmp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_rs_enqueue(ptr %s, i64 %s, i64 %s)", tmp, s, v0, v1))
	next := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", next, idx))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", next, idxA))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(doneL)
	closed := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_rs_close(ptr %s)", closed, s))

	return Value{Ref: s, Ty: ReadableStreamType(elemTy)}, nil
}

// emitForAwaitOfStream implements `for await (const chunk of stream)` — the
// stream (or an already-obtained reader) is read chunk-at-a-time, each read()
// promise awaited through the shared task-promise await (microtask-accurate in
// both runtime tiers).
func (e *Emitter) emitForAwaitOfStream(s *ast.ForOfStatement, ty Type, streamVal Value, condL, bodyL, incL, endL string) error {
	e.ensureStreamRuntime()
	chunkTy := TypeI64
	if ty.StreamChunk != nil {
		chunkTy = *ty.StreamChunk
	}
	resultTy := streamReadResultType(chunkTy)
	streamVal.Ref = e.streamOfHandle(ty, streamVal.Ref)

	if ty.IsReadableStream() {
		// Lock like getReader() does; an already-locked stream throws.
		ok := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_rs_lock(ptr %s)", ok, streamVal.Ref))
		e.streamThrowTypeError(ok, "Invalid state: ReadableStream is locked")
	}

	isPattern := s.ArrayPattern != nil || s.ObjectPattern != nil
	varPtr := e.freshReg()
	if !isPattern {
		if chunkTy.IsArray() {
			// An array-shaped chunk variable is a stable slot holding a pointer
			// to the current chunk's header (object-reference model, TDD-00127).
			e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", varPtr))
			e.define(s.VarName, Symbol{Ptr: varPtr, Ty: chunkTy})
		} else {
			e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", varPtr, StructFieldIR(chunkTy), chunkTy.Align()))
			e.define(s.VarName, Symbol{Ptr: varPtr, Ty: chunkTy})
		}
	}
	resultAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", resultAlloca))

	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	prom := e.emitRsRead(streamVal.Ref, chunkTy)
	rec, err := e.emitAwaitTaskPromise(prom, resultTy)
	if err != nil {
		return err
	}
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", rec.Ref, resultAlloca))
	recR := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", recR, resultAlloca))
	dIdx, _, _ := resultTy.FieldIndex("done")
	dGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", dGep, resultTy.StructIR(), recR, dIdx))
	doneReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i1, ptr %s, align 1", doneReg, dGep))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", doneReg, endL, bodyL))

	e.emitLabel(bodyL)
	recB := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", recB, resultAlloca))
	// done:false here, so the value field is always present — demote the
	// `T | undefined` field back to its bare chunk value.
	loaded := e.loadStreamResultValue(recB, resultTy, chunkTy).Ref
	switch {
	case s.ObjectPattern != nil:
		if err := e.unpackObjectPatternInto(loaded, chunkTy, s.ObjectPattern, s.GetPos()); err != nil {
			return err
		}
	case s.ArrayPattern != nil:
		if !chunkTy.IsTuple() {
			return fmt.Errorf("%d:%d: cannot array-destructure a stream chunk of non-tuple type", s.GetPos().Line, s.GetPos().Col)
		}
		if err := e.unpackTuplePatternInto(loaded, chunkTy, s.ArrayPattern, s.GetPos()); err != nil {
			return err
		}
	default:
		if chunkTy.IsArray() {
			// Point the chunk variable's slot at a fresh header for this chunk.
			chunkHeader := e.boxArrayValue(Value{Ref: loaded, Ty: chunkTy})
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", chunkHeader, varPtr))
		} else {
			e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", StructFieldIR(chunkTy), loaded, varPtr, chunkTy.Align()))
		}
	}
	if err := e.emitForOfBody(s); err != nil {
		return err
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", incL))
	e.emitLabel(incL)
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(endL)
	if ty.IsReadableStream() {
		e.emitInstr(fmt.Sprintf("call void @__kml_rs_unlock(ptr %s)", streamVal.Ref))
	}
	return nil
}

// The WHATWG stream methods, reached through their `@intrinsic`
// declarations: the readable family's on emitStreamMethodCall, the
// writable family's on emitWStreamMethodCall.
func init() {
	for owner, names := range map[string][]string{
		"ReadableStream":                  {"cancel", "getReader", "pipeThrough", "pipeTo", "tee", "values"},
		"ReadableStreamGenericReader":     {"cancel"},
		"ReadableStreamDefaultReader":     {"read", "releaseLock"},
		"ReadableStreamDefaultController": {"close", "enqueue", "error", "terminate"},
		"WritableStream":                  {"abort", "close", "getWriter"},
		"WritableStreamDefaultWriter":     {"abort", "close", "releaseLock", "write"},
		"WritableStreamDefaultController": {"error"},
	} {
		writable := strings.HasPrefix(owner, "Writable")
		for _, name := range names {
			name := name
			intrinsics[owner+".prototype."+name] = intrinsic{
				emit: func(e *Emitter, ex *ast.CallExpression) (Value, error) {
					mem := ex.Callee.(*ast.MemberExpression)
					if writable {
						return e.emitWStreamMethodCall(mem.Object, name, ex.Args, ex.GetPos())
					}
					return e.emitStreamMethodCall(mem.Object, name, ex.Args, ex.GetPos())
				},
				ty: func(e *Emitter, ex *ast.CallExpression) Type {
					t, _ := e.streamCallType(ex, ex.Callee.(*ast.MemberExpression))
					return t
				},
			}
		}
	}
}

// literalMembers picks ol's members named in names (a later one of a name
// wins, as in the object), and the first key that is none of them.
func literalMembers(ol *ast.ObjectLiteral, names ...string) (map[string]ast.Expression, string) {
	members := map[string]ast.Expression{}
	for _, p := range ol.Properties {
		if !slices.Contains(names, p.Key) {
			return members, p.Key
		}
		members[p.Key] = p.Value
	}
	return members, ""
}
