// emit_streams.go — WHATWG ReadableStream codegen (TDD-00097 Stage 1):
// `new ReadableStream<T>({start, pull, cancel}, strategy?)` construction,
// stream/reader/controller method dispatch, `ReadableStream.from(array)`, and
// `for await...of` over a stream/reader. Mirrors emit_eventemitter.go's split:
// Go codegen here, the hand-written state machine in runtime_streams.go.
//
// The chunk type T is marshalled through the queue as two raw i64 words (the
// promise v0/v1 idiom): scalars/pointers use one word, array-shaped values
// ({ptr,i64}) both. read() promises are settled by a per-construction-site
// "fulfill thunk" this file emits — it rebuilds the typed {value, done} record
// so the runtime stays type-agnostic.
package llvm

import (
	"fmt"

	"KlainMainLang/ast"
)

// streamChunkWords marshals an already-evaluated chunk value into its two
// queue words (v0, v1) — storePromiseValue's register-level sibling.
func (e *Emitter) streamChunkWords(val Value) (string, string) {
	if val.Ty.IsArray {
		p := e.freshReg()
		l := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = extractvalue { ptr, i64 } %s, 0", p, val.Ref))
		e.emitInstr(fmt.Sprintf("%s = extractvalue { ptr, i64 } %s, 1", l, val.Ref))
		pi := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = ptrtoint ptr %s to i64", pi, p))
		return pi, l
	}
	return e.promiseBitsOf(val), "0"
}

// streamChunkFromWords rebuilds a chunk value of type ty from its two queue
// words — loadPromiseValue's register-level sibling.
func (e *Emitter) streamChunkFromWords(v0, v1 string, ty Type) Value {
	if ty.IsArray {
		p := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", p, v0))
		agg0 := e.freshReg()
		agg1 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = insertvalue { ptr, i64 } undef, ptr %s, 0", agg0, p))
		e.emitInstr(fmt.Sprintf("%s = insertvalue { ptr, i64 } %s, i64 %s, 1", agg1, agg0, v1))
		return Value{Ref: agg1, Ty: ty}
	}
	return e.promiseValFromBits(v0, ty)
}

// buildStreamReadRecord mallocs a {value, done} record — buildGenNextResult's
// sibling, but storing the value through StructFieldIR so an array-shaped
// chunk gets its full {ptr,i64} slot.
func (e *Emitter) buildStreamReadRecord(resultTy, chunkTy Type, chunk Value, doneI1 string) string {
	e.ensureMalloc()
	rec := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", rec, resultTy.StructSize()))
	vIdx, _, _ := resultTy.FieldIndex("value")
	vGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", vGep, resultTy.StructIR(), rec, vIdx))
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align 8", StructFieldIR(chunkTy), chunk.Ref, vGep))
	dIdx, _, _ := resultTy.FieldIndex("done")
	dGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", dGep, resultTy.StructIR(), rec, dIdx))
	e.emitInstr(fmt.Sprintf("store i1 %s, ptr %s, align 1", doneI1, dGep))
	return rec
}

// emitStreamFulfillThunk emits the per-site `void @__kml_rs_fulfill_N(ptr %p,
// i64 %v0, i64 %v1, i64 %done)` the runtime settles read() promises through:
// rebuild the typed chunk, build the {value,done} record, store it as the
// promise's value, settle fulfilled.
func (e *Emitter) emitStreamFulfillThunk(chunkTy Type) string {
	e.streamSiteCtr++
	fn := fmt.Sprintf("@__kml_rs_fulfill_%d", e.streamSiteCtr)
	resultTy := genNextResultType(chunkTy)

	restore := e.beginThunkEmit()
	chunk := e.streamChunkFromWords("%v0", "%v1", chunkTy)
	doneI1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %%done, 0", doneI1))
	rec := e.buildStreamReadRecord(resultTy, chunkTy, chunk, doneI1)
	e.storePromiseValue("%p", Value{Ref: rec, Ty: resultTy})
	e.emitInstr("call void @__kml_promise_settle(ptr %p, i64 1)")
	e.emitInstr("ret void")
	body := e.allocas.String() + e.body.String()
	restore()

	e.functions.WriteString(fmt.Sprintf("\ndefine void %s(ptr %%p, i64 %%v0, i64 %%v1, i64 %%done) {\nentry:\n%s}\n", fn, body))
	return fn
}

// streamCallbackClosure evaluates an underlying-source callback expression
// (arrow / function expression only, like EventEmitter listeners — .emit-style
// later invocation needs a real closure header) with the given param hints.
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
	if !val.Ty.IsFunc {
		return Value{}, fmt.Errorf("%d:%d: a ReadableStream's %s must be a function", pos.Line, pos.Col, what)
	}
	return val, nil
}

// callbackReturnsPromise reports whether a source callback's static return
// type is a Promise (an async callback) — the pull/cancel wrappers forward
// that promise to the runtime, which reacts to its settlement.
func callbackReturnsPromise(ty Type) bool {
	return ty.FuncRetType != nil && ty.FuncRetType.IsPromise
}

// emitStreamPullWrap emits `ptr @__kml_rs_pullwrap_N(ptr %env)` where env is a
// malloc'd {userClosure, stream} pair: invoke the user's pull/start-shaped
// callback with the controller (the stream pointer itself) and return its
// promise, or null for a synchronous callback.
func (e *Emitter) emitStreamPullWrap(userTy Type) string {
	e.streamSiteCtr++
	fn := fmt.Sprintf("@__kml_rs_pullwrap_%d", e.streamSiteCtr)
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
		e.emitInstr(fmt.Sprintf("ret ptr %s", r))
	} else {
		e.emitInstr(fmt.Sprintf("call void (%s) %s(%s)", sig, fp, args))
		e.emitInstr("ret ptr null")
	}
	body := e.allocas.String() + e.body.String()
	restore()

	e.functions.WriteString(fmt.Sprintf("\ndefine ptr %s(ptr %%env) {\nentry:\n%s}\n", fn, body))
	return fn
}

// emitStreamCancelWrap emits `ptr @__kml_rs_cancelwrap_N(ptr %env, i64
// %reason)` where env is the user closure header directly. V1 supports a
// zero-parameter cancel callback (validated by the caller); the reason word is
// accepted for ABI stability but not forwarded.
func (e *Emitter) emitStreamCancelWrap(userTy Type) string {
	e.streamSiteCtr++
	fn := fmt.Sprintf("@__kml_rs_cancelwrap_%d", e.streamSiteCtr)
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
		e.emitInstr(fmt.Sprintf("ret ptr %s", r))
	} else {
		e.emitInstr(fmt.Sprintf("call void (ptr) %s(ptr %s)", fp, ep))
		e.emitInstr("ret ptr null")
	}
	body := e.allocas.String() + e.body.String()
	restore()

	e.functions.WriteString(fmt.Sprintf("\ndefine ptr %s(ptr %%env, i64 %%reason) {\nentry:\n%s}\n", fn, body))
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
	e.streamSiteCtr++
	fn := fmt.Sprintf("@__kml_rs_sizewrap_%d", e.streamSiteCtr)

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
	if chunkTy.IsArray {
		p := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %%v0 to ptr", p))
		// The user listener's array parameter expects a header pointer, not the
		// raw chunk buffer (object-reference model, TDD-00127).
		hdr := e.newArrayHeader(p, "%v1")
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

	e.functions.WriteString(fmt.Sprintf("\ndefine double %s(ptr %%env, i64 %%v0, i64 %%v1) {\nentry:\n%s}\n", fn, body))
	return fn, nil
}

// emitStreamByteLengthSizeWrap emits the built-in ByteLengthQueuingStrategy
// size: the chunk's element count (its {ptr,i64} length word) — for the byte
// chunks (Uint8Array) it is meant for, that IS the byteLength.
func (e *Emitter) emitStreamByteLengthSizeWrap() string {
	e.streamSiteCtr++
	fn := fmt.Sprintf("@__kml_rs_sizewrap_%d", e.streamSiteCtr)
	e.functions.WriteString(fmt.Sprintf(`
define double %s(ptr %%env, i64 %%v0, i64 %%v1) {
entry:
  %%d = sitofp i64 %%v1 to double
  ret double %%d
}
`, fn))
	return fn
}

// storeStreamField stores a ptr into the rstream struct field at idx.
func (e *Emitter) storeStreamField(s string, idx int, val string) {
	gep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, rstreamStructIR, s, idx))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", val, gep))
}

// objectLiteralProp returns the value expression for key in an object literal,
// or nil.
func objectLiteralProp(ol *ast.ObjectLiteral, key string) ast.Expression {
	for _, p := range ol.Properties {
		if p.Key == key {
			return p.Value
		}
	}
	return nil
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
		if byteLengthStrategy && !chunkTy.IsArray {
			return "", nil, false, fmt.Errorf("%d:%d: ByteLengthQueuingStrategy requires byte-array chunks (e.g. Uint8Array)", pos.Line, pos.Col)
		}
	}
	hwmRef := "1.0"
	if hwmExpr != nil {
		hv, err := e.emitExpr(hwmExpr)
		if err != nil {
			return "", nil, false, err
		}
		hv = e.coerce(hv, TypeF64)
		hwmRef = hv.Ref
	}
	return hwmRef, sizeExpr, byteLengthStrategy, nil
}

// emitNewReadableStream implements `new ReadableStream<T>(source?, strategy?)`.
func (e *Emitter) emitNewReadableStream(ex *ast.NewReadableStreamExpression) (Value, error) {
	pos := ex.GetPos()
	chunkTy := TypeI64
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
		for _, p := range ol.Properties {
			switch p.Key {
			case "start":
				startExpr = p.Value
			case "pull":
				pullExpr = p.Value
			case "cancel":
				cancelExpr = p.Value
			case "type", "autoAllocateChunkSize":
				return Value{}, fmt.Errorf("%d:%d: byte streams (underlying source '%s') are not supported — use a default stream of Uint8Array chunks", pos.Line, pos.Col, p.Key)
			default:
				return Value{}, fmt.Errorf("%d:%d: unknown underlying source member '%s' (expected start, pull, cancel)", pos.Line, pos.Col, p.Key)
			}
		}
	}

	hwmRef, sizeExpr, byteLengthStrategy, err := e.resolveStreamStrategy(ex.Strategy, chunkTy, pos)
	if err != nil {
		return Value{}, err
	}

	fulfillFn := e.emitStreamFulfillThunk(chunkTy)
	s := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_rs_alloc(double %s, ptr %s)", s, hwmRef, fulfillFn))

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
		e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", env))
		e0 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 0", e0, env))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", userClo.Ref, e0))
		e1 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 1", e1, env))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", s, e1))
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

// emitStreamProperty implements the stream surface's property reads:
// `.locked` (stream), `.desiredSize` (controller), `.closed` (reader).
func (e *Emitter) emitStreamProperty(ex *ast.MemberExpression, objTy Type) (Value, error) {
	ty, ptr, err := e.resolveStreamForCall(ex.Object, ex.GetPos())
	if err != nil {
		return Value{}, err
	}
	e.ensureStreamRuntime()
	switch ex.Property {
	case "locked":
		if !ty.IsReadableStream {
			break
		}
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 11", gep, rstreamStructIR, ptr))
		f := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", f, gep))
		bit := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = and i64 %s, 32", bit, f))
		b := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", b, bit))
		return Value{Ref: b, Ty: TypeBool}, nil
	case "desiredSize":
		if !ty.IsRSController {
			break
		}
		d := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call double @__kml_rs_desired(ptr %s)", d, ptr))
		return Value{Ref: d, Ty: TypeF64}, nil
	case "closed":
		if !ty.IsStreamReader {
			break
		}
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 16", gep, rstreamStructIR, ptr))
		cp := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", cp, gep))
		pt := PromiseOf(TypeVoid)
		pt.PromiseTask = true
		return Value{Ref: cp, Ty: pt}, nil
	}
	return Value{}, fmt.Errorf("%d:%d: property '%s' is not available on this stream value", ex.GetPos().Line, ex.GetPos().Col, ex.Property)
}

// resolveStreamForCall resolves a stream/reader/controller receiver expression
// to its loaded heap pointer — resolveEventEmitterForCall's sibling.
func (e *Emitter) resolveStreamForCall(objExpr ast.Expression, pos ast.Pos) (Type, string, error) {
	if id, ok := objExpr.(*ast.Identifier); ok {
		sym, found := e.lookup(id.Name)
		if found && (sym.Ty.IsReadableStream || sym.Ty.IsStreamReader || sym.Ty.IsRSController) {
			ptr := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", ptr, sym.Ptr))
			return sym.Ty, ptr, nil
		}
	}
	val, err := e.emitExpr(objExpr)
	if err != nil {
		return Type{}, "", err
	}
	if !val.Ty.IsReadableStream && !val.Ty.IsStreamReader && !val.Ty.IsRSController {
		return Type{}, "", fmt.Errorf("%d:%d: value is not a ReadableStream/reader/controller", pos.Line, pos.Col)
	}
	return val.Ty, val.Ref, nil
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
	errPtr := e.buildErrorObj(errorKindIDs["TypeError"], e.internString(msg), e.internString("TypeError"))
	e.emitInstr(fmt.Sprintf("call void @__kml_throw(ptr %s)", errPtr))
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
	ty, ptr, err := e.resolveStreamForCall(objExpr, pos)
	if err != nil {
		return Value{}, err
	}
	e.ensureStreamRuntime()
	chunkTy := TypeI64
	if ty.StreamChunk != nil {
		chunkTy = *ty.StreamChunk
	}

	switch {
	case ty.IsReadableStream:
		switch method {
		case "getReader", "values":
			if method == "getReader" && len(args) > 0 {
				return Value{}, fmt.Errorf("%d:%d: getReader() options (BYOB mode) are not supported", pos.Line, pos.Col)
			}
			ok := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_rs_lock(ptr %s)", ok, ptr))
			e.streamThrowTypeError(ok, "ReadableStream is already locked to a reader")
			return Value{Ref: ptr, Ty: StreamReaderType(chunkTy)}, nil
		case "cancel":
			bits, err := e.streamReasonBits(args, pos)
			if err != nil {
				return Value{}, err
			}
			prom := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_rs_cancel(ptr %s, i64 %s)", prom, ptr, bits))
			pt := PromiseOf(TypeVoid)
			pt.PromiseTask = true
			return Value{Ref: prom, Ty: pt}, nil
		case "pipeTo":
			return e.emitStreamPipeTo(ptr, chunkTy, args, pos)
		case "pipeThrough":
			return e.emitStreamPipeThrough(ptr, chunkTy, args, pos)
		case "tee":
			return e.emitStreamTee(ptr, chunkTy, pos)
		}
		return Value{}, fmt.Errorf("%d:%d: unknown ReadableStream method '%s'", pos.Line, pos.Col, method)

	case ty.IsStreamReader:
		switch method {
		case "read":
			prom := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_rs_read(ptr %s)", prom, ptr))
			pt := PromiseOf(genNextResultType(chunkTy))
			pt.PromiseTask = true
			return Value{Ref: prom, Ty: pt}, nil
		case "releaseLock":
			e.emitInstr(fmt.Sprintf("call void @__kml_rs_unlock(ptr %s)", ptr))
			return Value{Ty: TypeVoid}, nil
		case "cancel":
			bits, err := e.streamReasonBits(args, pos)
			if err != nil {
				return Value{}, err
			}
			prom := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_rs_cancel(ptr %s, i64 %s)", prom, ptr, bits))
			pt := PromiseOf(TypeVoid)
			pt.PromiseTask = true
			return Value{Ref: prom, Ty: pt}, nil
		}
		return Value{}, fmt.Errorf("%d:%d: unknown stream reader method '%s'", pos.Line, pos.Col, method)

	case ty.IsRSController:
		switch method {
		case "enqueue":
			if len(args) != 1 {
				return Value{}, fmt.Errorf("%d:%d: enqueue() expects 1 chunk argument", pos.Line, pos.Col)
			}
			cv, err := e.emitExprWithObjectHint(args[0], chunkTy)
			if err != nil {
				return Value{}, err
			}
			cv = e.coerce(cv, chunkTy)
			v0, v1 := e.streamChunkWords(cv)
			ok := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_rs_enqueue(ptr %s, i64 %s, i64 %s)", ok, ptr, v0, v1))
			e.streamThrowTypeError(ok, "cannot enqueue on a closed or errored ReadableStream")
			return Value{Ty: TypeVoid}, nil
		case "close":
			ok := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_rs_close(ptr %s)", ok, ptr))
			e.streamThrowTypeError(ok, "cannot close an already-closed or errored ReadableStream")
			return Value{Ty: TypeVoid}, nil
		case "error":
			bits, err := e.streamReasonBits(args, pos)
			if err != nil {
				return Value{}, err
			}
			e.emitInstr(fmt.Sprintf("call void @__kml_rs_error(ptr %s, i64 %s)", ptr, bits))
			return Value{Ty: TypeVoid}, nil
		}
		return Value{}, fmt.Errorf("%d:%d: unknown stream controller method '%s'", pos.Line, pos.Col, method)
	}
	return Value{}, fmt.Errorf("%d:%d: unknown stream method '%s'", pos.Line, pos.Col, method)
}

// emitReadableStreamFrom implements `ReadableStream.from(array)` — a closed
// stream pre-filled with the array's elements.
func (e *Emitter) emitReadableStreamFrom(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: ReadableStream.from() expects 1 argument", pos.Line, pos.Col)
	}
	arrTy := e.inferExprType(args[0])
	if !arrTy.IsArray {
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
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_rs_alloc(double 1.0, ptr %s)", s, fulfillFn))

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
	resultTy := genNextResultType(chunkTy)

	if ty.IsReadableStream {
		// Lock like getReader() does; an already-locked stream throws.
		ok := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_rs_lock(ptr %s)", ok, streamVal.Ref))
		e.streamThrowTypeError(ok, "ReadableStream is already locked to a reader")
	}

	isPattern := s.ArrayPattern != nil || s.ObjectPattern != nil
	varPtr := e.freshReg()
	if !isPattern {
		if chunkTy.IsArray {
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
	prom := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_rs_read(ptr %s)", prom, streamVal.Ref))
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
	vIdx, _, _ := resultTy.FieldIndex("value")
	vGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", vGep, resultTy.StructIR(), recB, vIdx))
	loaded := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", loaded, StructFieldIR(chunkTy), vGep, chunkTy.Align()))
	switch {
	case s.ObjectPattern != nil:
		if err := e.unpackObjectPatternInto(loaded, chunkTy, s.ObjectPattern, s.GetPos()); err != nil {
			return err
		}
	case s.ArrayPattern != nil:
		if !chunkTy.IsTuple {
			return fmt.Errorf("%d:%d: cannot array-destructure a stream chunk of non-tuple type", s.GetPos().Line, s.GetPos().Col)
		}
		if err := e.unpackTuplePatternInto(loaded, chunkTy, s.ArrayPattern, s.GetPos()); err != nil {
			return err
		}
	default:
		if chunkTy.IsArray {
			// Point the chunk variable's slot at a fresh header for this chunk.
			chunkHeader := e.boxArrayValue(Value{Ref: loaded, Ty: chunkTy})
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", chunkHeader, varPtr))
		} else {
			e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", StructFieldIR(chunkTy), loaded, varPtr, chunkTy.Align()))
		}
	}
	if err := e.emitStmt(s.Body); err != nil {
		return err
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", incL))
	e.emitLabel(incL)
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(endL)
	if ty.IsReadableStream {
		e.emitInstr(fmt.Sprintf("call void @__kml_rs_unlock(ptr %s)", streamVal.Ref))
	}
	return nil
}
