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
		e.emitInstr(fmt.Sprintf("%s = ptrtoint "+irPtr+" %s to i64", pi, val.Ref))
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
		e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to "+irPtr, h, v0))
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
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_rs_alloc(double %s, "+irPtr+" %s, "+irPtr+" %s)", dst, hwm, fulfill, box))
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
		e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_rs_read_any("+irPtr+" %s, "+irPtr+" %s)", prom, s, ff))
		return prom
	}
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_rs_read("+irPtr+" %s)", prom, s))
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
	rec := e.freshReg()
	e.emitObjMallocInto(rec, resultTy)
	vIdx, valTy, _ := resultTy.FieldIndex("value")
	vGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 %d", vGep, resultTy.StructIR(), rec, vIdx))
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
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 %d", dGep, resultTy.StructIR(), rec, dIdx))
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
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 %d", vGep, resultTy.StructIR(), recPtr, vIdx))
	if valTy.IsArray() {
		v := e.loadArraySlotAggregate(vGep, chunkTy) // header-ptr slot (TDD-00213 S2)
		return v
	}
	loaded := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, "+irPtr+" %s, align %d", loaded, StructFieldIR(valTy), vGep, chunkTy.Align()))
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
	e.emitInstr("call void @__kml_promise_settle(" + irPtr + " %p, i64 1)")
	e.emitInstr("ret void")
	body := e.allocas.String() + e.body.String()
	restore()

	return e.defineContentNamed("@__kml_rs_fulfill.", "void", irPtr+" %p, i64 %v0, i64 %v1, i64 %done", body)
}

// streamCallbackClosure evaluates an underlying-source callback expression
// (arrow / function expression only — a later invocation needs a real
// closure header) with the given param hints.
func (e *Emitter) streamCallbackClosure(arg ast.Expression, hints []Type, what string, pos ast.Pos) (Value, error) {
	if streamCallbackNeedsAdapter(arg, len(hints)) {
		// Any other function value, or one declaring more parameters than
		// the stream passes: read once, called through an arrow taking
		// what the stream passes (the rest of its parameters undefined).
		fn := arg
		if m, isMem := arg.(*ast.MemberExpression); !isMem || !pureRef(m.Object) {
			var err error
			if fn, err = e.receiverTemp(arg); err != nil {
				return Value{}, err
			}
		} // else a method of the source, called on it (its this)
		params := make([]ast.Param, len(hints))
		args := make([]ast.Expression, len(hints))
		for i := range hints {
			params[i] = ast.Param{Name: fmt.Sprintf("__kml_rs_arg%d", i)}
			args[i] = ast.NewIdentifier(params[i].Name, pos)
		}
		arg = ast.NewArrowFunction(params, nil, ast.NewCallExpression(fn, args, pos), nil, pos)
	}
	var val Value
	var err error
	switch fn := arg.(type) {
	case *ast.ArrowFunction:
		val, err = e.emitArrowFunctionWithHints(fn, hints)
	case *ast.FunctionExpression:
		val, err = e.emitFunctionExpression(fn, hints)
	default:
		return Value{}, internalAt(pos, "a ReadableStream's %s is no function literal after adapting", what)
	}
	if err != nil {
		return Value{}, err
	}
	if !val.Ty.IsFunc() {
		return Value{}, internalAt(pos, "a ReadableStream's %s is no function", what)
	}
	return val, nil
}

// streamCallbackArity is how many arguments the stream passes each
// underlying source or sink callback.
var streamCallbackArity = map[string]int{"start": 1, "pull": 1, "cancel": 1, "write": 2, "close": 0, "abort": 1, "transform": 2, "flush": 1}

// streamUnderlyingDynamic is streamUnderlying for a value held in any: each
// callback is read off it once, now, and the stream is given the spec's
// algorithm over it, PromiseResolve of calling it on the source (or of
// undefined when it is absent). Its type member is read too.
func (e *Emitter) streamUnderlyingDynamic(src ast.Expression, names []string) (map[string]ast.Expression, map[string]bool, bool, error) {
	pos := src.GetPos()
	tmp, err := e.receiverTemp(src)
	if err != nil {
		return nil, nil, false, err
	}
	members := map[string]ast.Expression{}
	for _, n := range names {
		m, err := e.receiverTemp(ast.NewMemberExpression(tmp, n, pos))
		if err != nil {
			return nil, nil, false, err
		}
		undef := ast.NewNullLiteral(true, pos)
		if n == "size" {
			// Node's validateFunction of a present size (ERR_INVALID_ARG_TYPE).
			if err := e.emitStrategySizeCheck(m); err != nil {
				return nil, nil, false, err
			}
			// A strategy's size, called with this undefined; 1 when absent.
			c := ast.NewIdentifier("__kml_rs_chunk", pos)
			members[n] = ast.NewArrowFunction([]ast.Param{{Name: "__kml_rs_chunk"}}, nil,
				ast.NewConditionalExpression(ast.NewBinaryExpression("===", m, undef, pos), ast.NewNumberLiteral("1", pos),
					ast.NewCallExpression(m, []ast.Expression{c}, pos), pos), nil, pos)
			continue
		}
		if _, callback := streamCallbackArity[n]; !callback {
			members[n] = m // a plain member (highWaterMark)
			continue
		}
		params := make([]ast.Param, streamCallbackArity[n])
		args := []ast.Expression{tmp}
		for i := range params {
			params[i] = ast.Param{Name: fmt.Sprintf("__kml_rs_arg%d", i)}
			args = append(args, ast.NewIdentifier(params[i].Name, pos))
		}
		call := ast.NewConditionalExpression(ast.NewBinaryExpression("===", m, undef, pos), undef,
			ast.NewCallExpression(ast.NewMemberExpression(m, "call", pos), args, pos), pos)
		// start is called as it is (its result adopted when a thenable);
		// every other method through Node's `async (...args) =>
		// method.call(obj, ...args)` wrapper, whose promise the stream
		// reacts to as it is.
		fn := ast.NewArrowFunction(params, ast.AnyAnnotation("ts"), call, nil, pos)
		if n != "start" {
			fn.RetType = nil
			fn.IsAsync = true
			if e.streamDirect == nil {
				e.streamDirect = map[ast.Expression]bool{}
			}
			e.streamDirect[fn] = true
		}
		members[n] = fn
	}
	// A source's or sink's type, known only now.
	if slices.Contains(names, "pull") || slices.Contains(names, "write") {
		if err := e.emitStreamTypeCheck(ast.NewMemberExpression(tmp, "type", pos), slices.Contains(names, "pull")); err != nil {
			return nil, nil, false, err
		}
	}
	return members, map[string]bool{}, true, nil
}

// streamCallbackNeedsAdapter reports an underlying-source callback that is
// no arrow or function expression of at most n parameters.
func streamCallbackNeedsAdapter(arg ast.Expression, n int) bool {
	switch fn := arg.(type) {
	case *ast.ArrowFunction:
		return len(fn.Params) > n
	case *ast.FunctionExpression:
		return len(fn.Params) > n
	}
	return true
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
	e.emitInstr(fmt.Sprintf("%s = getelementptr { "+irPtr+", "+irPtr+" }, "+irPtr+" %%env, i32 0, i32 0", up))
	e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", u, up))
	sp := e.freshReg()
	s := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { "+irPtr+", "+irPtr+" }, "+irPtr+" %%env, i32 0, i32 1", sp))
	e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", s, sp))
	fp := e.freshReg()
	fpp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { "+irPtr+", "+irPtr+" }, "+irPtr+" %s, i32 0, i32 0", fpp, u))
	e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", fp, fpp))
	ep := e.freshReg()
	epp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { "+irPtr+", "+irPtr+" }, "+irPtr+" %s, i32 0, i32 1", epp, u))
	e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", ep, epp))
	args := fmt.Sprintf(irPtr+" %s", ep)
	sig := irPtr
	if hasParam {
		args += fmt.Sprintf(", "+irPtr+" %s", s)
		sig += ", " + irPtr
	}
	e.emitStreamCallbackCall(isAsync, sig, fp, args)
	body := e.allocas.String() + e.body.String()
	restore()

	fn := e.defineContentNamed("@__kml_rs_pullwrap.", irPtr, irPtr+" %env", body)
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
	e.emitInstr(fmt.Sprintf("%s = getelementptr { "+irPtr+", "+irPtr+" }, "+irPtr+" %%env, i32 0, i32 0", fpp))
	e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", fp, fpp))
	ep := e.freshReg()
	epp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { "+irPtr+", "+irPtr+" }, "+irPtr+" %%env, i32 0, i32 1", epp))
	e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", ep, epp))
	// The reason, boxed, as the callback's parameter takes it.
	sig, args := irPtr, irPtr+" "+ep
	if len(userTy.FuncParams) == 1 {
		pty := userTy.FuncParams[0]
		r := Value{Ref: "%reason", Ty: TypeAny}
		if !pty.IsDynamic() {
			r = e.unboxArgToParam("%reason", pty)
		}
		sig += ", " + paramIR(pty)
		args += ", " + paramIR(pty) + " " + r.Ref
	}
	e.emitStreamCallbackCall(isAsync, sig, fp, args)
	body := e.allocas.String() + e.body.String()
	restore()

	fn := e.defineContentNamed("@__kml_rs_cancelwrap.", irPtr, irPtr+" %env, i64 %reason", body)
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

	restore := e.beginThunkEmit()
	fp := e.freshReg()
	fpp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { "+irPtr+", "+irPtr+" }, "+irPtr+" %%env, i32 0, i32 0", fpp))
	e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", fp, fpp))
	ep := e.freshReg()
	epp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { "+irPtr+", "+irPtr+" }, "+irPtr+" %%env, i32 0, i32 1", epp))
	e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", ep, epp))
	var args, sig string
	if chunkTy.IsArray() {
		hdr := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %%v0 to "+irPtr, hdr))
		args = fmt.Sprintf(irPtr+" %s, "+irPtr+" %s, i64 %%v1", ep, hdr)
		sig = irPtr + ", " + irPtr + ", i64"
	} else {
		chunk := e.streamChunkFromWords("%v0", "%v1", chunkTy)
		args = fmt.Sprintf(irPtr+" %s, %s %s", ep, chunkTy.IR, chunk.Ref)
		sig = irPtr + ", " + chunkTy.IR
	}
	if retTy.IR == "void" {
		// No result: undefined, whose number is NaN.
		e.emitInstr(fmt.Sprintf("call void (%s) %s(%s)", sig, fp, args))
		e.emitInstr("ret double 0x7FF8000000000000")
		body := e.allocas.String() + e.body.String()
		restore()
		return e.defineContentNamed("@__kml_rs_sizewrap.", "double", irPtr+" %env, i64 %v0, i64 %v1", body), nil
	}
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call %s (%s) %s(%s)", r, retTy.IR, sig, fp, args))
	if !retTy.IsDynamic() && retTy.IR != "i64" && retTy.IR != "double" {
		// Any other result: its number (the algorithm's unrestricted double).
		boxed, err := e.emitBoxValue(Value{Ref: r, Ty: retTy})
		if err != nil {
			restore()
			return "", err
		}
		e.emitInstr(fmt.Sprintf("ret double %s", e.convert(boxed, TypeF64).Ref))
	} else if retTy.IsDynamic() {
		e.emitInstr(fmt.Sprintf("ret double %s", e.convert(Value{Ref: r, Ty: retTy}, TypeF64).Ref))
	} else if retTy.IR == "i64" {
		d := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = sitofp i64 %s to double", d, r))
		e.emitInstr(fmt.Sprintf("ret double %s", d))
	} else {
		e.emitInstr(fmt.Sprintf("ret double %s", r))
	}
	body := e.allocas.String() + e.body.String()
	restore()

	fn := e.defineContentNamed("@__kml_rs_sizewrap.", "double", irPtr+" %env, i64 %v0, i64 %v1", body)
	return fn, nil
}

// emitStreamByteLengthSizeWrap emits the built-in ByteLengthQueuingStrategy
// size: the chunk's element count (its {ptr,i64} length word) — for the byte
// chunks (Uint8Array) it is meant for, that IS the byteLength.
func (e *Emitter) emitStreamByteLengthSizeWrap() string {
	return e.defineContentNamed("@__kml_rs_sizewrap.", "double", irPtr+" %env, i64 %v0, i64 %v1", "  %d = sitofp i64 %v1 to double\n  ret double %d\n")
}

// storeStreamField stores a ptr into the rstream struct field at idx.
func (e *Emitter) storeStreamField(s string, idx int, val string) {
	gep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 %d", gep, rstreamStructIR, s, idx))
	e.heapStore(irPtr, val, gep, 8)
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
			// new XQueuingStrategy(init): init.highWaterMark, required; a
			// ByteLengthQueuingStrategy's size is chunk.byteLength.
			byteLengthStrategy = ne.ClassName == "ByteLengthQueuingStrategy"
			args, err := e.dropArgsPast(ne.Args, 1, pos)
			if err != nil {
				return "", nil, false, err
			}
			if len(args) == 0 {
				e.emitThrowCoded("TypeError", "ERR_INVALID_ARG_TYPE", e.internString(`The "init" argument must be of type object. Received undefined`))
				e.emitLabel(e.freshLabel("strategy.dead"))
				return "1.0", nil, false, nil
			}
			members, _, ok, err := e.streamUnderlying(args[0], []string{"highWaterMark", "size"})
			if err != nil {
				return "", nil, false, err
			}
			if !ok {
				if members, _, _, err = e.streamUnderlyingDynamic(e.boxedOperand(args[0]), []string{"highWaterMark"}); err != nil {
					return "", nil, false, err
				}
			}
			hwmExpr = members["highWaterMark"]
			if hwmExpr == nil {
				e.emitThrowCoded("TypeError", "ERR_MISSING_OPTION", e.internString("init.highWaterMark is required"))
				e.emitLabel(e.freshLabel("strategy.dead"))
				return "1.0", nil, false, nil
			}
			if byteLengthStrategy && !chunkTy.IsArray() {
				byteLengthStrategy = false
				sizeExpr = ast.NewArrowFunction([]ast.Param{{Name: "__kml_rs_chunk"}}, nil,
					ast.NewMemberExpression(ast.NewIdentifier("__kml_rs_chunk", pos), "byteLength", pos), nil, pos)
			}
		} else {
			members, _, ok, err := e.streamUnderlying(strat, []string{"highWaterMark", "size"})
			if err != nil {
				return "", nil, false, err
			}
			if _, lit := strat.(*ast.ObjectLiteral); !lit && e.typeOfExpr(strat).IsClass() {
				ok = false // a class instance's members may be accessors
			}
			if !ok {
				// Members known only at run time (a strategy object held
				// in a binding): read off its box.
				if members, _, _, err = e.streamUnderlyingDynamic(e.boxedOperand(strat), []string{"highWaterMark", "size"}); err != nil {
					return "", nil, false, err
				}
			}
			hwmExpr, sizeExpr = members["highWaterMark"], members["size"]
		}
	}
	hwmRef := "1.0"
	if hwmExpr != nil {
		hv, err := e.emitExpr(hwmExpr)
		if err != nil {
			return "", nil, false, err
		}
		if hv, err = e.emitStreamHighWaterMark(hv); err != nil {
			return "", nil, false, err
		}
		hwmRef = hv.Ref
	}
	return hwmRef, sizeExpr, byteLengthStrategy, nil
}

// boxedOperand is x read as `any`: its value boxed, bound to a temporary.
func (e *Emitter) boxedOperand(x ast.Expression) ast.Expression {
	return ast.NewAsExpression(x, ast.AnyAnnotation("ts"), x.GetPos())
}

// emitStreamHighWaterMark is ExtractHighWaterMark: undefined is the default
// (1), NaN or a negative number Node's RangeError.
func (e *Emitter) emitStreamHighWaterMark(hv Value) (Value, error) {
	if hv.Ty.IsUndefined() && !hv.Ty.IsDynamic() {
		return Value{Ref: "1.0", Ty: TypeF64}, nil
	}
	def := ""
	if hv.Ty.IsDynamic() && hv.Ty.IR == "i64" {
		def = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", def, hv.Ref, nbUndefined))
	}
	f := e.convert(hv, TypeF64)
	bad := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = fcmp ult double %s, 0.0", bad, f.Ref)) // NaN or < 0
	if def != "" {
		nd, b2 := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = xor i1 %s, true", nd, def))
		e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", b2, bad, nd))
		bad = b2
	}
	badL, okL := e.freshLabel("hwm.bad"), e.freshLabel("hwm.ok")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", bad, badL, okL))
	e.emitLabel(badL)
	shown, err := e.emitValueToString(f)
	if err != nil {
		return Value{}, err
	}
	msg, err := e.emitStringConcat(Value{Ref: e.internString("The property 'strategy.highWaterMark' is invalid. Received "), Ty: TypePtr}, shown)
	if err != nil {
		return Value{}, err
	}
	e.emitThrowCoded("RangeError", "ERR_INVALID_ARG_VALUE", msg.Ref)
	e.emitLabel(okL)
	if def == "" {
		return f, nil
	}
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, double 1.0, double %s", r, def, f.Ref))
	return Value{Ref: r, Ty: TypeF64}, nil
}

// emitNewReadableStream implements `new ReadableStream<T>(source?, strategy?)`.
func (e *Emitter) emitNewReadableStream(ex *ast.NewReadableStreamExpression) (Value, error) {
	pos := ex.GetPos()
	chunkTy := TypeAny // ReadableStream<R = any>
	if ex.ChunkType != nil {
		chunkTy = e.annotationType(ex.ChunkType)
	}
	controllerTy := RSControllerType(chunkTy)
	e.ensureExceptionHelpers()
	e.ensureMicrotasks()
	e.ensureNanBox()
	e.ensurePromiseAddReaction()
	e.ensureShapeRuntime()
	e.ensureUnhandledRejections()

	// Destructure the underlying source at compile time.
	var startExpr, pullExpr, cancelExpr ast.Expression
	if ex.Source != nil {
		members, _, ok, err := e.streamUnderlying(ex.Source, []string{"start", "pull", "cancel"})
		if err != nil {
			return Value{}, err
		}
		if !ok {
			return Value{}, internalAt(pos, "an underlying object streamUnderlying did not read")
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
		wrap := e.streamWrap(pullExpr, func() string { return e.emitStreamPullWrap(userClo.Ty) })
		env := e.freshReg()
		e.heapMallocInto(env, allocClosure, "16")
		e0 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr { "+irPtr+", "+irPtr+" }, "+irPtr+" %s, i32 0, i32 0", e0, env))
		e.heapStore(irPtr, userClo.Ref, e0, 8)
		e1 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr { "+irPtr+", "+irPtr+" }, "+irPtr+" %s, i32 0, i32 1", e1, env))
		e.heapStore(irPtr, s, e1, 8)
		e.storeStreamField(s, 9, e.buildBuiltinClosure(wrap, env))
	}

	if cancelExpr != nil {
		userClo, err := e.streamCallbackClosure(cancelExpr, []Type{TypeAny}, "cancel callback", pos)
		if err != nil {
			return Value{}, err
		}
		wrap := e.streamWrap(cancelExpr, func() string { return e.emitStreamCancelWrap(userClo.Ty) })
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
		fp := e.freshReg()
		fpp := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr { "+irPtr+", "+irPtr+" }, "+irPtr+" %s, i32 0, i32 0", fpp, userClo.Ref))
		e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", fp, fpp))
		ep := e.freshReg()
		epp := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr { "+irPtr+", "+irPtr+" }, "+irPtr+" %s, i32 0, i32 1", epp, userClo.Ref))
		e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", ep, epp))
		args := fmt.Sprintf(irPtr+" %s", ep)
		sig := irPtr
		if len(userClo.Ty.FuncParams) == 1 {
			args += fmt.Sprintf(", "+irPtr+" %s", s)
			sig += ", " + irPtr
		}
		if userClo.Ty.FuncRetType != nil && userClo.Ty.FuncRetType.IsDynamic() {
			// A start read at run time: adopted when its result is a thenable.
			b := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call i64 (%s) %s(%s)", b, sig, fp, args))
			p := e.emitStreamStartResult(b)
			e.emitMarkPromiseHandled(p)
			// Bit 1: already the setup's promise, reacted to as it is.
			d := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr i8, "+irPtr+" %s, i64 2", d, p))
			e.declareFn("__kml_rs_start_promise", "declare void @__kml_rs_start_promise("+irPtr+", "+irPtr+")")
			e.emitInstr(fmt.Sprintf("call void @__kml_rs_start_promise("+irPtr+" %s, "+irPtr+" %s)", s, d))
		} else if callbackReturnsPromise(userClo.Ty) {
			p := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" (%s) %s(%s)", p, sig, fp, args))
			e.emitMarkPromiseHandled(p) // the stream reacts to it (uponPromise)
			e.declareFn("__kml_rs_start_promise", "declare void @__kml_rs_start_promise("+irPtr+", "+irPtr+")")
			e.emitInstr(fmt.Sprintf("call void @__kml_rs_start_promise("+irPtr+" %s, "+irPtr+" %s)", s, p))
		} else {
			e.emitInstr(fmt.Sprintf("call void (%s) %s(%s)", sig, fp, args))
			e.emitInstr(fmt.Sprintf("call void @__kml_microtask_enqueue("+irPtr+" %s)", startedClo))
		}
	} else {
		e.emitInstr(fmt.Sprintf("call void @__kml_microtask_enqueue("+irPtr+" %s)", startedClo))
	}

	return Value{Ref: s, Ty: ReadableStreamType(chunkTy)}, nil
}

// newStreamHandle is a reader or writer of the stream at stream: its own
// object (getReader() answers a new one each time, as in Node), the stream
// after its host header word (TDD-00230 P3.3).
func (e *Emitter) newStreamHandle(stream string, ty Type) Value {
	base, h := e.freshReg(), e.freshReg()
	e.heapMallocInto(base, allocHost, "32")
	e.heapStore("i64", fmt.Sprint(e.hostHeaderWord(ty)), base, 8)
	e.emitBodyAddr(h, base)
	e.heapStore(irPtr, stream, h, 8)
	for _, off := range []int{8, 16} {
		p := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i8, "+irPtr+" %s, i64 %d", p, h, off))
		e.heapStore(irPtr, "null", p, 8)
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
	e.emitInstr(fmt.Sprintf("%s = getelementptr "+irPtr+", "+irPtr+" %s, i64 %d", p, h, i))
	e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", v, p))
	return v
}

// emitIfReleased emits bound (the handle's stream is attached) and released
// (its lock was released) as two paths and joins their results, which are
// both ptr-typed or both void.
func (e *Emitter) emitIfReleased(stream string, bound, released func() (Value, error)) (Value, error) {
	isNull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq "+irPtr+" %s, null", isNull, stream))
	relL, boundL, joinL := e.freshLabel("handle.released"), e.freshLabel("handle.bound"), e.freshLabel("handle.join")
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca "+irPtr+", align 8", slot))
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
	isPtr := bv.Ty.IR == irPtr && bv.Ref != ""
	if isPtr {
		e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", bv.Ref, slot))
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", joinL))
	e.emitLabel(relL)
	rv, err := released()
	if err != nil {
		return Value{}, err
	}
	if isPtr && rv.Ref != "" {
		e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", rv.Ref, slot))
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", joinL))
	e.emitLabel(joinL)
	if !isPtr {
		return bv, nil
	}
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", r, slot))
	return Value{Ref: r, Ty: bv.Ty}, nil
}

// emitStreamStateError builds Node's `TypeError [ERR_INVALID_STATE]` with
// msg and returns its bits.
func (e *Emitter) emitStreamStateError(msg string) string {
	errPtr := e.emitStreamStateErrorPtr(msg)
	bits := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = ptrtoint "+irPtr+" %s to i64", bits, errPtr))
	return bits
}

// emitStreamStateErrorBox is emitStreamStateError's error boxed, as a
// stream keeps a reason.
func (e *Emitter) emitStreamStateErrorBox(msg string) string {
	bits := e.emitStreamStateError(msg)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = or i64 %s, 1", r, bits))
	return r
}

// emitStreamStateErrorPtr is emitStreamStateError's error object.
func (e *Emitter) emitStreamStateErrorPtr(msg string) string {
	e.ensureExceptionHelpers()
	return e.buildErrorObjWithCode(errorKindIDs["TypeError"], e.internString(msg), e.internString("TypeError"), e.internString("ERR_INVALID_STATE"), "0.0", "null")
}

// emitRejectedStreamPromise is a Promise rejected with Node's stream state
// error msg.
func (e *Emitter) emitRejectedStreamPromise(msg string, ty Type) Value {
	e.ensureMicrotasks()
	e.ensureNanBox()
	e.ensureExceptionHelpers()
	e.ensureUnhandledRejections()
	bits := e.emitStreamStateError(msg)
	p := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_task_alloc_promise()", p))
	e.emitInstr(fmt.Sprintf("call void @__kml_promise_reject_err("+irPtr+" %s, i64 %s)", p, bits))
	return Value{Ref: p, Ty: ty}
}

// streamOfHandle is the stream a stream-typed value works on: a reader's or
// writer's stream, any other's own pointer.
func (e *Emitter) streamOfHandle(ty Type, ref string) string {
	if !ty.IsStreamReader() && !ty.IsStreamWriter() {
		return ref
	}
	s := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", s, ref))
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
			e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", ptr, sym.Ptr))
			return sym.Ty, ptr, e.streamOfHandle(sym.Ty, ptr), nil
		}
	}
	val, err := e.emitExpr(objExpr)
	if err != nil {
		return Type{}, "", "", err
	}
	if !val.Ty.IsReadableStream() && !val.Ty.IsStreamReader() && !val.Ty.IsRSController() {
		return Type{}, "", "", internalAt(pos, "value is not a ReadableStream/reader/controller")
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
	// The reason as given, boxed (undefined when absent): a stream keeps
	// the value itself, as controller.error(42)'s 42.
	if len(args) == 0 {
		return fmt.Sprint(nbUndefined), nil
	}
	v, err := e.emitExpr(args[0])
	if err != nil {
		return "", err
	}
	boxed, err := e.emitBoxValue(v)
	if err != nil {
		return "", err
	}
	return boxed.Ref, nil
}

// emitStreamMethodCall dispatches stream/reader/controller method calls.
func (e *Emitter) emitStreamMethodCall(objExpr ast.Expression, method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	ty, handle, ptr, err := e.resolveStreamHandle(objExpr, pos)
	if err != nil {
		return Value{}, err
	}
	e.ensureExceptionHelpers()
	e.ensureMicrotasks()
	e.ensureNanBox()
	e.ensurePromiseAddReaction()
	e.ensureShapeRuntime()
	e.ensureUnhandledRejections()
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
	switch {
	case ty.IsReadableStream():
		table = readableStreamMethods
	case ty.IsStreamReader():
		table = streamReaderMethods
	case ty.IsRSController():
		table = rsControllerMethods
	default:
		return Value{}, internalAt(pos, "stream method '%s' on no stream type", method)
	}
	if f, ok := table[method]; ok {
		return f(e, c)
	}
	// No such method: JavaScript's TypeError once the arguments are
	// evaluated (TypeScript's checker reports it first).
	for _, a := range c.args {
		if _, err := e.emitExpr(a); err != nil {
			return Value{}, err
		}
	}
	e.emitThrowTypeError(e.notFunctionMsg(method))
	e.emitLabel(e.freshLabel("stream.nomethod.dead"))
	return Value{Ref: fmt.Sprint(nbUndefined), Ty: TypeAny}, nil
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
		"terminate": func(e *Emitter, c *streamCall) (Value, error) {
			err := e.emitStreamStateErrorBox("Invalid state: TransformStream terminated")
			e.emitInstr(fmt.Sprintf("call void @__kml_rs_terminate("+irPtr+" %s, i64 %s)", c.ptr, err))
			return Value{Ty: TypeVoid}, nil
		},
	}
}

// streamReaderOptions checks getReader's options as Node does, before the
// stream is locked: null or undefined, or an object whose mode is absent,
// is the default reader; "byob" asks for a byte stream, which no stream
// here is; any other mode, or a primitive, is a TypeError.
func (e *Emitter) streamReaderOptions(c *streamCall) error {
	ov, err := e.emitExprWithObjectHint(c.args[0], TypeAny)
	if err != nil {
		return err
	}
	for _, a := range c.args[1:] {
		if _, err := e.emitExpr(a); err != nil {
			return err
		}
	}
	box, err := e.emitBoxValue(ov)
	if err != nil {
		return err
	}
	e.ensureDynJSONC()
	tag, _ := e.emitUnboxTagPayload(box)
	nullish, n1, n2 := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", n1, tag, kmlTagNull))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", n2, tag, kmlTagUndefined))
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", nullish, n1, n2))
	doneL, objCheckL := e.freshLabel("rdopts.done"), e.freshLabel("rdopts.obj")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", nullish, doneL, objCheckL))
	e.emitLabel(objCheckL)
	prim := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ule i8 %s, %d", prim, tag, kmlTagBoolean))
	primL, modeL := e.freshLabel("rdopts.prim"), e.freshLabel("rdopts.mode")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", prim, primL, modeL))
	e.emitLabel(primL)
	suffix := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_received(i64 %s)", suffix, box.Ref))
	msg, err := e.emitStringConcat(Value{Ref: e.internString(`The "options" argument must be of type object.`), Ty: TypePtr}, Value{Ref: suffix, Ty: TypePtr})
	if err != nil {
		return err
	}
	e.emitThrowCoded("TypeError", "ERR_INVALID_ARG_TYPE", msg.Ref)
	e.emitLabel(modeL)
	mode, err := e.emitDynAnyMemberGetNamed(box, e.internString("mode"), "mode", c.pos)
	if err != nil {
		return err
	}
	mode = e.convert(mode, TypeAny)
	absent := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", absent, mode.Ref, nbUndefined))
	badL := e.freshLabel("rdopts.bad")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", absent, doneL, badL))
	e.emitLabel(badL)
	// `${mode}` (its ToString, as Node's validation does) "byob": the
	// stream must be a byte stream.
	text := ast.NewTemplateLiteral([]string{"", ""}, []ast.Expression{e.bindValue(mode, c.pos)}, c.pos)
	isByob, err := e.emitExpr(ast.NewBinaryExpression("===", text, ast.NewStringLiteral("byob", c.pos), c.pos))
	if err != nil {
		return err
	}
	isByob = e.toBool(isByob)
	byobL, otherL := e.freshLabel("rdopts.byob"), e.freshLabel("rdopts.other")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isByob.Ref, byobL, otherL))
	e.emitLabel(byobL)
	sbox, err := e.emitBoxValue(Value{Ref: c.ptr, Ty: ReadableStreamType(c.chunkTy)})
	if err != nil {
		return err
	}
	shown, err := e.emitDynamicInspect(sbox)
	if err != nil {
		return err
	}
	bmsg, err := e.emitStringConcat(Value{Ref: e.internString("The argument 'stream' must be a byte stream. Received "), Ty: TypePtr}, shown)
	if err != nil {
		return err
	}
	e.emitThrowCoded("TypeError", "ERR_INVALID_ARG_VALUE", bmsg.Ref)
	e.emitLabel(otherL)
	mshown, err := e.emitInspectQuoted(mode, "'")
	if err != nil {
		return err
	}
	mmsg, err := e.emitStringConcat(Value{Ref: e.internString("The property 'options.mode' is invalid. Received "), Ty: TypePtr}, mshown)
	if err != nil {
		return err
	}
	e.emitThrowCoded("TypeError", "ERR_INVALID_ARG_VALUE", mmsg.Ref)
	e.emitLabel(doneL)
	return nil
}

func (e *Emitter) streamGetReader(c *streamCall) (Value, error) {
	if c.method == "getReader" && len(c.args) > 0 {
		if err := e.streamReaderOptions(c); err != nil {
			return Value{}, err
		}
	}
	ok := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_rs_lock("+irPtr+" %s)", ok, c.ptr))
	e.streamThrowTypeError(ok, "Invalid state: ReadableStream is locked")
	return e.newStreamHandle(c.ptr, StreamReaderType(c.chunkTy)), nil
}

// streamCancel is a stream's and its reader's cancel(reason).
func (e *Emitter) streamCancel(c *streamCall) (Value, error) {
	// The reason as given, boxed (undefined when absent).
	bits := fmt.Sprint(nbUndefined)
	if len(c.args) > 0 {
		v, err := e.emitExpr(c.args[0])
		if err != nil {
			return Value{}, err
		}
		boxed, err := e.emitBoxValue(v)
		if err != nil {
			return Value{}, err
		}
		bits = boxed.Ref
	}
	prom := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_rs_cancel("+irPtr+" %s, i64 %s)", prom, c.ptr, bits))
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
	closedErr := e.emitStreamStateErrorBox("Invalid state: Reader released")
	readErr := e.emitStreamStateErrorBox("Invalid state: Releasing reader")
	e.emitInstr(fmt.Sprintf("call void @__kml_rs_release("+irPtr+" %s, "+irPtr+" %s, i64 %s, i64 %s)", c.ptr, c.handle, closedErr, readErr))
	e.heapStore(irPtr, "null", c.handle, 8)
	return Value{Ty: TypeVoid}, nil
}

func (e *Emitter) streamEnqueue(c *streamCall) (Value, error) {
	args := c.args
	if len(args) == 0 {
		args = []ast.Expression{ast.NewNullLiteral(true, c.pos)} // enqueue(undefined)
	}
	args, err := e.dropArgsPast(args, 1, c.pos)
	if err != nil {
		return Value{}, err
	}
	cv, err := e.emitExprWithObjectHint(args[0], c.chunkTy)
	if err != nil {
		return Value{}, err
	}
	cv = e.convert(cv, c.chunkTy)
	v0, v1 := e.streamChunkWords(cv)
	ok := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_rs_enqueue("+irPtr+" %s, i64 %s, i64 %s)", ok, c.ptr, v0, v1))
	e.streamThrowTypeError(ok, "cannot enqueue on a closed or errored ReadableStream")
	return Value{Ty: TypeVoid}, nil
}

func (e *Emitter) streamClose(c *streamCall) (Value, error) {
	ok := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_rs_close("+irPtr+" %s)", ok, c.ptr))
	e.streamThrowTypeError(ok, "cannot close an already-closed or errored ReadableStream")
	return Value{Ty: TypeVoid}, nil
}

func (e *Emitter) streamError(c *streamCall) (Value, error) {
	bits, err := e.streamReasonBits(c.args, c.pos)
	if err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("call void @__kml_rs_error("+irPtr+" %s, i64 %s)", c.ptr, bits))
	return Value{Ty: TypeVoid}, nil
}

// emitReadableStreamFrom implements `ReadableStream.from(array)` — a closed
// stream pre-filled with the array's elements.
func (e *Emitter) emitReadableStreamFrom(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) == 0 {
		args = []ast.Expression{ast.NewNullLiteral(true, pos)} // read as undefined
	}
	if len(args) > 1 {
		var err error
		if args, err = e.dropArgsPast(args, 1, pos); err != nil {
			return Value{}, err
		}
	}
	arrTy := e.typeOfExpr(args[0])
	if !arrTy.IsArray() {
		return e.emitReadableStreamFromIterable(args[0], pos)
	}
	elemTy := *arrTy.ElemType
	e.ensureExceptionHelpers()
	e.ensureMicrotasks()
	e.ensureNanBox()
	e.ensurePromiseAddReaction()
	e.ensureShapeRuntime()
	e.ensureUnhandledRejections()

	ptrReg, lenReg, _, err := e.resolveArrayForHOF(args[0], pos)
	if err != nil {
		return Value{}, err
	}
	fulfillFn := e.emitStreamFulfillThunk(elemTy)
	s := e.freshReg()
	e.emitRsAlloc(s, "1.0", fulfillFn, elemTy)

	idxA := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxA))
	e.emitInstr(fmt.Sprintf("store i64 0, "+irPtr+" %s, align 8", idxA))
	condL := e.freshLabel("rs.from.cond")
	bodyL := e.freshLabel("rs.from.body")
	doneL := e.freshLabel("rs.from.done")
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	idx := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", idx, idxA))
	end := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp sge i64 %s, %s", end, idx, lenReg))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", end, doneL, bodyL))
	e.emitLabel(bodyL)
	gep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i64 %s", gep, elemTy.IR, ptrReg, idx))
	elem := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, "+irPtr+" %s, align %d", elem, elemTy.IR, gep, elemTy.Align()))
	v0, v1 := e.streamChunkWords(Value{Ref: elem, Ty: elemTy})
	tmp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_rs_enqueue("+irPtr+" %s, i64 %s, i64 %s)", tmp, s, v0, v1))
	next := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", next, idx))
	e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", next, idxA))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(doneL)
	closed := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_rs_close("+irPtr+" %s)", closed, s))

	return Value{Ref: s, Ty: ReadableStreamType(elemTy)}, nil
}

// emitForAwaitOfStream implements `for await (const chunk of stream)` — the
// stream (or an already-obtained reader) is read chunk-at-a-time, each read()
// promise awaited through the shared task-promise await (microtask-accurate in
// both runtime tiers).
func (e *Emitter) emitForAwaitOfStream(s *ast.ForOfStatement, ty Type, streamVal Value, condL, bodyL, incL, endL string) error {
	e.ensureExceptionHelpers()
	e.ensureMicrotasks()
	e.ensureNanBox()
	e.ensurePromiseAddReaction()
	e.ensureShapeRuntime()
	e.ensureUnhandledRejections()
	chunkTy := TypeI64
	if ty.StreamChunk != nil {
		chunkTy = *ty.StreamChunk
	}
	resultTy := streamReadResultType(chunkTy)
	streamVal.Ref = e.streamOfHandle(ty, streamVal.Ref)

	if ty.IsReadableStream() {
		// Lock like getReader() does; an already-locked stream throws.
		ok := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_rs_lock("+irPtr+" %s)", ok, streamVal.Ref))
		e.streamThrowTypeError(ok, "Invalid state: ReadableStream is locked")
	}

	isPattern := s.ArrayPattern != nil || s.ObjectPattern != nil
	varPtr := e.freshReg()
	if !isPattern {
		if chunkTy.IsArray() {
			// An array-shaped chunk variable is a stable slot holding a pointer
			// to the current chunk's header (object-reference model, TDD-00127).
			e.emitAlloca(fmt.Sprintf("%s = alloca "+irPtr+", align 8", varPtr))
			e.define(s.VarName, Symbol{Ptr: varPtr, Ty: chunkTy})
		} else {
			e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", varPtr, StructFieldIR(chunkTy), chunkTy.Align()))
			e.define(s.VarName, Symbol{Ptr: varPtr, Ty: chunkTy})
		}
	}
	resultAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca "+irPtr+", align 8", resultAlloca))

	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	prom := e.emitRsRead(streamVal.Ref, chunkTy)
	rec, err := e.emitAwaitTaskPromise(prom, resultTy)
	if err != nil {
		return err
	}
	e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", rec.Ref, resultAlloca))
	recR := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", recR, resultAlloca))
	dIdx, _, _ := resultTy.FieldIndex("done")
	dGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 %d", dGep, resultTy.StructIR(), recR, dIdx))
	doneReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i1, "+irPtr+" %s, align 1", doneReg, dGep))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", doneReg, endL, bodyL))

	e.emitLabel(bodyL)
	recB := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", recB, resultAlloca))
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
			return internalAt(s.GetPos(), "cannot array-destructure a stream chunk of non-tuple type")
		}
		if err := e.unpackTuplePatternInto(loaded, chunkTy, s.ArrayPattern, s.GetPos()); err != nil {
			return err
		}
	default:
		if chunkTy.IsArray() {
			// Point the chunk variable's slot at a fresh header for this chunk.
			chunkHeader := e.boxArrayValue(Value{Ref: loaded, Ty: chunkTy})
			e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", chunkHeader, varPtr))
		} else {
			e.emitInstr(fmt.Sprintf("store %s %s, "+irPtr+" %s, align %d", StructFieldIR(chunkTy), loaded, varPtr, chunkTy.Align()))
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
		e.emitInstr(fmt.Sprintf("call void @__kml_rs_unlock("+irPtr+" %s)", streamVal.Ref))
	}
	return nil
}

// The WHATWG stream methods, reached through their `@intrinsic`
// declarations: the readable family's on emitStreamMethodCall, the
// writable family's on emitWStreamMethodCall.
func init() {
	for owner, names := range map[string][]string{
		"ReadableStream":                  {"cancel", "getReader", "pipeThrough", "pipeTo", "tee"},
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

// streamUnderlying resolves an underlying source or sink to its callback
// members by name, and the other keys it has: an object literal's own (any
// other property's value evaluated, as building the object does), or a
// statically typed object's members read off it once evaluated. ok is false
// for a value whose members are known only at run time.
func (e *Emitter) streamUnderlying(src ast.Expression, names []string) (members map[string]ast.Expression, others map[string]bool, ok bool, err error) {
	members, others = map[string]ast.Expression{}, map[string]bool{}
	if ol, lit := src.(*ast.ObjectLiteral); lit {
		for _, p := range ol.Properties {
			if _, spread := p.Value.(*ast.SpreadElement); spread || p.KeyExpr != nil || p.AccessorKind != "" {
				return e.streamUnderlyingBoxed(src, names)
			}
			if slices.Contains(names, p.Key) {
				if p.Key == "size" && !mayBeFunctionLiteral(p.Value) {
					// Not a function: the stream's own check throws, when it runs.
					return e.streamUnderlyingBoxed(src, names)
				}
				members[p.Key] = p.Value
				continue
			}
			others[p.Key] = true
			if p.Key == "type" && (slices.Contains(names, "pull") || slices.Contains(names, "write")) {
				if err := e.emitStreamTypeCheck(p.Value, slices.Contains(names, "pull")); err != nil {
					return nil, nil, false, err
				}
				continue
			}
			if !noEffectExpr(p.Value) {
				if _, err := e.emitExpr(p.Value); err != nil {
					return nil, nil, false, err
				}
			}
		}
		return members, others, true, nil
	}
	ty := e.typeOfExpr(src)
	if ty.IsDynamic() || !ty.IsObject() || ty.IsArray() {
		return e.streamUnderlyingBoxed(src, names)
	}
	tmp, err := e.receiverTemp(src)
	if err != nil {
		return nil, nil, false, err
	}
	for _, f := range ty.VisibleFields() {
		if f.Name == "size" && slices.Contains(names, "size") && !f.Ty.IsFunc() && !f.Ty.IsDynamic() && !f.Ty.IsUndefined() {
			return e.streamUnderlyingBoxed(src, names)
		}
		if slices.Contains(names, f.Name) {
			members[f.Name] = ast.NewMemberExpression(tmp, f.Name, src.GetPos())
		} else {
			others[f.Name] = true
		}
	}
	for _, n := range names {
		if _, has := members[n]; !has && e.classMethodValue(ty, n) {
			members[n] = ast.NewMemberExpression(tmp, n, src.GetPos())
		}
	}
	return members, others, true, nil
}

// streamUnderlyingBoxed is streamUnderlying for a value whose members are
// read when it runs (a spread literal, a union, a primitive): boxed, then
// read as an any's. undefined is no underlying object (the defaults); any
// other primitive is Node's ERR_INVALID_ARG_TYPE for the argument.
func (e *Emitter) streamUnderlyingBoxed(src ast.Expression, names []string) (map[string]ast.Expression, map[string]bool, bool, error) {
	pos := src.GetPos()
	v, err := e.emitExpr(src)
	if err != nil {
		return nil, nil, false, err
	}
	box, err := e.emitBoxValue(v)
	if err != nil {
		return nil, nil, false, err
	}
	role := ""
	switch {
	case slices.Contains(names, "pull"):
		role = "source"
	case slices.Contains(names, "write"):
		role = "sink"
	case slices.Contains(names, "transform"):
		role = "transformer"
	}
	if role != "" {
		tag, _ := e.emitUnboxTagPayload(box)
		prim, isUndef, bad := e.freshReg(), e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ule i8 %s, %d", prim, tag, kmlTagUndefined))
		e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isUndef, tag, kmlTagUndefined))
		notUndef := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = xor i1 %s, true", notUndef, isUndef))
		e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", bad, prim, notUndef))
		badL, okL := e.freshLabel("stream.src.bad"), e.freshLabel("stream.src.ok")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", bad, badL, okL))
		e.emitLabel(badL)
		e.ensureDynJSONC()
		suffix := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_received(i64 %s)", suffix, box.Ref))
		msg, err := e.emitStringConcat(Value{Ref: e.internString(`The "` + role + `" argument must be of type object.`), Ty: TypePtr}, Value{Ref: suffix, Ty: TypePtr})
		if err != nil {
			return nil, nil, false, err
		}
		e.emitThrowCoded("TypeError", "ERR_INVALID_ARG_TYPE", msg.Ref)
		e.emitLabel(okL)
		// undefined: an empty object's members, every one absent.
		empty := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_dynobj_new()", empty))
		eb := e.emitNbTagPtr(empty, kmlTagDynObject)
		sel := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", sel, isUndef, eb, box.Ref))
		box = Value{Ref: sel, Ty: TypeAny}
	}
	return e.streamUnderlyingDynamic(e.bindValue(box, pos), names)
}

// typeMemberMessage is prefix + shown + "'".
func (e *Emitter) typeMemberMessage(prefix string, shown Value) (string, error) {
	m, err := e.emitStringConcat(Value{Ref: e.internString(prefix), Ty: TypePtr}, shown)
	if err != nil {
		return "", err
	}
	m, err = e.emitStringConcat(m, Value{Ref: e.internString("'"), Ty: TypePtr})
	return m.Ref, err
}

// emitStreamTypeCheck checks an underlying source's (readable) or sink's
// type member at run time: Node's errors for one it rejects; a byte
// stream's ("bytes") is not supported, an error rather than a default
// stream in its place.
func (e *Emitter) emitStreamTypeCheck(typeExpr ast.Expression, readable bool) error {
	ty, err := e.emitExpr(typeExpr)
	if err != nil {
		return err
	}
	ty, err = e.emitBoxValue(ty)
	if err != nil {
		return err
	}
	isU := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", isU, ty.Ref, nbUndefined))
	badL, okL := e.freshLabel("rs.type.bad"), e.freshLabel("rs.type.ok")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isU, okL, badL))
	e.emitLabel(badL)
	shown, err := e.emitArgToString(ty)
	if err != nil {
		return err
	}
	if readable {
		isBytes := e.emitStrEq(shown.Ref, e.internString("bytes"))
		bytesL, otherL := e.freshLabel("rs.type.bytes"), e.freshLabel("rs.type.other")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isBytes, bytesL, otherL))
		e.emitLabel(bytesL)
		e.emitThrowTypeError("byte streams (an underlying source's type 'bytes') are not supported")
		e.emitLabel(otherL)
		msg, err := e.typeMemberMessage("The property 'source.type' is invalid. Received '", shown)
		if err != nil {
			return err
		}
		e.emitThrowCoded("TypeError", "ERR_INVALID_ARG_VALUE", msg)
	} else {
		msg, err := e.typeMemberMessage("The argument 'type' is invalid. Received '", shown)
		if err != nil {
			return err
		}
		e.emitThrowCoded("RangeError", "ERR_INVALID_ARG_VALUE", msg)
	}
	e.emitLabel(okL)
	return nil
}

// emitInspectQuoted is the boxed v shown as inspect shows a nested value: a
// string between quote, anything else as emitDynamicInspect renders it.
func (e *Emitter) emitInspectQuoted(v Value, quote string) (Value, error) {
	shown, err := e.emitDynamicInspect(v)
	if err != nil {
		return Value{}, err
	}
	tag, _ := e.emitUnboxTagPayload(v)
	isStr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isStr, tag, kmlTagString))
	q1, err := e.emitStringConcat(Value{Ref: e.internString(quote), Ty: TypePtr}, shown)
	if err != nil {
		return Value{}, err
	}
	q2, err := e.emitStringConcat(q1, Value{Ref: e.internString(quote), Ty: TypePtr})
	if err != nil {
		return Value{}, err
	}
	qs := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, "+irPtr+" %s, "+irPtr+" %s", qs, isStr, q2.Ref, shown.Ref))
	return Value{Ref: qs, Ty: TypePtr}, nil
}

// streamWrap generates a stream callback's wrapper (gen), marking it direct
// when expr is one of streamUnderlyingDynamic's async wrappers.
func (e *Emitter) streamWrap(expr ast.Expression, gen func() string) string {
	saved := e.streamWrapDirect
	e.streamWrapDirect = e.streamDirect[expr]
	defer func() { e.streamWrapDirect = saved }()
	return gen()
}

// emitStreamStartResult is a start callback's result held in any (an
// underlying object's start read at run time) as Node's setup resolves it:
// `new Promise((r) => r(result))` — a thenable adopted through a job, a
// promise adopted, any other value fulfilling it now.
func (e *Emitter) emitStreamStartResult(box string) string {
	e.ensurePromiseDynRuntime()
	q := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_task_alloc_promise()", q))
	e.emitInstr(fmt.Sprintf("call void @__kml_promise_resolve_any("+irPtr+" %s, i64 %s)", q, box))
	return q
}

// optMember is `o?.p`.
func optMember(o ast.Expression, p string, pos ast.Pos) *ast.MemberExpression {
	m := ast.NewMemberExpression(o, p, pos)
	m.Optional = true
	return m
}

// mayBeFunctionLiteral reports an expression that may evaluate to a
// function or undefined: anything but a primitive, object or array literal.
func mayBeFunctionLiteral(x ast.Expression) bool {
	switch v := x.(type) {
	case *ast.StringLiteral, *ast.NumberLiteral, *ast.BooleanLiteral, *ast.TemplateLiteral, *ast.ObjectLiteral, *ast.ArrayLiteral:
		return false
	case *ast.NullLiteral:
		return v.IsUndefined
	}
	return true
}

// emitStrategySizeCheck throws Node's TypeError when a queuing strategy's
// size (the expression m, read once) is present and no function.
func (e *Emitter) emitStrategySizeCheck(m ast.Expression) error {
	v, err := e.emitExpr(m)
	if err != nil {
		return err
	}
	if v, err = e.emitBoxValue(v); err != nil {
		return err
	}
	pos := m.GetPos()
	held := e.anyTemp(v.Ref, pos) // read once: a getter runs once
	tf, err := e.emitExpr(ast.NewBinaryExpression("!==", ast.NewUnaryExpression("typeof", true, held, pos), ast.NewStringLiteral("function", pos), pos))
	if err != nil {
		return err
	}
	isUndef, bad := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, %d", isUndef, v.Ref, nbUndefined))
	e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", bad, isUndef, tf.Ref))
	badL, okL := e.freshLabel("strat.size.bad"), e.freshLabel("strat.size.ok")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", bad, badL, okL))
	e.emitLabel(badL)
	if err := e.emitThrowArgType(`The "strategy.size" property must be of type function.`, v); err != nil {
		return err
	}
	e.emitLabel(okL)
	return nil
}

// streamLockCtors are the reader/writer constructors, each with the stream
// it locks and that stream's acquiring method.
var streamLockCtors = map[string][2]string{
	"ReadableStreamDefaultReader": {"ReadableStream", "getReader"},
	"WritableStreamDefaultWriter": {"WritableStream", "getWriter"},
}

// emitNewStreamLock is `new ReadableStreamDefaultReader(s)` (or the writer):
// s's own acquire (which refuses a locked stream with ERR_INVALID_STATE),
// after Node's ERR_INVALID_ARG_TYPE for an s that is no such stream.
func (e *Emitter) emitNewStreamLock(ex *ast.NewExpression, lock [2]string) (Value, error) {
	pos := ex.GetPos()
	var arg ast.Expression = ast.NewNullLiteral(true, pos)
	if len(ex.Args) > 0 {
		arg = ex.Args[0]
	}
	for _, a := range ex.Args[min(1, len(ex.Args)):] {
		if _, err := e.emitExpr(a); err != nil {
			return Value{}, err
		}
	}
	st := e.typeOfExpr(arg)
	if lock[0] == "ReadableStream" && st.IsReadableStream() && !st.Nullable() || lock[0] == "WritableStream" && st.IsWritableStream() && !st.Nullable() {
		return e.emitExpr(ast.NewCallExpression(ast.NewMemberExpression(arg, lock[1], pos), nil, pos))
	}
	v, err := e.emitExpr(arg)
	if err != nil {
		return Value{}, err
	}
	if v, err = e.emitBoxValue(v); err != nil {
		return Value{}, err
	}
	held := e.anyTemp(v.Ref, pos)
	test, err := e.emitExpr(ast.NewBinaryExpression("instanceof", held, ast.NewIdentifier(lock[0], pos), pos))
	if err != nil {
		return Value{}, err
	}
	badL, okL := e.freshLabel("lock.bad"), e.freshLabel("lock.ok")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", test.Ref, okL, badL))
	e.emitLabel(badL)
	if err := e.emitThrowArgType(`The "stream" argument must be an instance of `+lock[0]+`.`, v); err != nil {
		return Value{}, err
	}
	e.emitLabel(okL)
	typed := ast.NewAsExpression(held, nameAnnotation(lock[0]), pos)
	return e.emitExpr(ast.NewCallExpression(ast.NewMemberExpression(typed, lock[1], pos), nil, pos))
}
