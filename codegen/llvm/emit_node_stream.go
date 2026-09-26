// emit_node_stream.go — the runtime stream handles behind fs.createReadStream/
// createWriteStream and the HTTP server's request and response: their
// 'data'/'end'/'error'/'close'/'finish'/'drain' events, write/end/pause/
// resume/.pipe(), and the wrapping of a web ReadableStream. The `stream`
// module itself is TypeScript (lib/node/stream.ts); these handles move onto
// it with the fs and http modules.
package llvm

import (
	"fmt"

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
		// The user closure's array param expects a header pointer (TDD-00127).
		hdr := e.newArrayHeader(cp, "%v1")
		e.emitInstr(fmt.Sprintf("call void (ptr, ptr, i64) %s(ptr %s, ptr %s, i64 %%v1)", fp, ep, hdr))
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

// streamWriteExtras parses the optional trailing arguments of a Node
// writable.write(chunk[, encoding][, callback]) / end([chunk][, encoding]
// [, callback]) call — everything after the chunk. `encoding` must be the
// 'utf8'/'utf-8' string literal (chunks are strings already, ADR-00449/00483);
// `callback` is a () => void resolved to a function pointer (same restriction
// as process.nextTick, since err is null on success). Returns the callback
// pointer, or "" when no callback was given.
func (e *Emitter) streamWriteExtras(extras []ast.Expression, fnName string, pos ast.Pos) (string, error) {
	if len(extras) == 0 {
		return "", nil
	}
	isUtf8 := func(a ast.Expression) bool {
		sl, ok := a.(*ast.StringLiteral)
		return ok && (sl.Value == "utf8" || sl.Value == "utf-8")
	}
	// A leading string argument is the encoding (must be 'utf8'); a leading
	// function argument is the callback.
	if _, ok := extras[0].(*ast.StringLiteral); ok {
		if !isUtf8(extras[0]) {
			return "", fmt.Errorf("%d:%d: %s() supports only the 'utf8' encoding (chunks are strings already)", pos.Line, pos.Col, fnName)
		}
		extras = extras[1:]
	}
	switch len(extras) {
	case 0:
		return "", nil
	case 1:
		return e.timerCallbackPtr(extras[0], fnName+" callback", pos)
	default:
		return "", fmt.Errorf("%d:%d: %s() takes a chunk[, encoding][, callback]", pos.Line, pos.Col, fnName)
	}
}

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
		if len(args) < 1 {
			return Value{}, fmt.Errorf("%d:%d: write() takes a chunk[, encoding][, callback]", pos.Line, pos.Col)
		}
		// Node: write(chunk[, encoding][, callback]). encoding must be 'utf8'
		// (chunks are strings already, ADR-00449/00483); callback fires once the
		// chunk is handled — scheduled onto the microtask queue like a Node write
		// callback's next-tick delivery. () => void only (same restriction as
		// process.nextTick / timers; err is null on success, so a zero-arg
		// callback observes nothing lost).
		cbPtr, err := e.streamWriteExtras(args[1:], "write", pos)
		if err != nil {
			return Value{}, err
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
		if cbPtr != "" {
			e.ensureMicrotasks()
			e.emitInstr(fmt.Sprintf("call void @__kml_microtask_enqueue(ptr %s)", cbPtr))
		}
		b := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", b, bI))
		return Value{Ref: b, Ty: TypeBool}, nil

	case "end":
		if !ty.IsNodeWritable {
			return Value{}, fmt.Errorf("%d:%d: end() requires a Writable", pos.Line, pos.Col)
		}
		// Node: end([chunk][, encoding][, callback]). The callback is the
		// 'finish' listener — fired after the stream flushes; scheduled onto the
		// microtask queue after close (chunks flush synchronously here). A
		// trailing function argument is that callback; a remaining string literal
		// after the chunk is the encoding (utf8 only).
		rest := args
		var cbPtr string
		if len(rest) > 0 && e.inferExprType(rest[len(rest)-1]).IsFunc {
			p, err := e.timerCallbackPtr(rest[len(rest)-1], "end callback", pos)
			if err != nil {
				return Value{}, err
			}
			cbPtr = p
			rest = rest[:len(rest)-1]
		}
		if len(rest) == 2 {
			sl, ok := rest[1].(*ast.StringLiteral)
			if !ok || (sl.Value != "utf8" && sl.Value != "utf-8") {
				return Value{}, fmt.Errorf("%d:%d: end() supports only the 'utf8' encoding (chunks are strings already)", pos.Line, pos.Col)
			}
			rest = rest[:1]
		}
		if len(rest) > 1 {
			return Value{}, fmt.Errorf("%d:%d: end() takes a final chunk[, encoding][, callback]", pos.Line, pos.Col)
		}
		ws := e.nodeStreamSide(ptr, 1)
		if len(rest) == 1 {
			cv, err := e.emitExprWithObjectHint(rest[0], inTy)
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
		if cbPtr != "" {
			e.ensureMicrotasks()
			e.emitInstr(fmt.Sprintf("call void @__kml_microtask_enqueue(ptr %s)", cbPtr))
		}
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
		// TDD-00195 Stage 2: req.pipe(res) — an http server response is a Writable
		// sink. Start chunked streaming (send the head, build the framing sink),
		// then pipe into its WHATWG writable exactly as any other destination.
		if dv.Ty.IsServerResponse {
			e.ensureResStreamRuntime()
			e.emitResBegin(dv.Ref)
			srt := ServerResponseType()
			wsIdx, _, _ := srt.FieldIndex("__kml_wsink")
			wsGep := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", wsGep, srt.StructIR(), dv.Ref, wsIdx))
			resSink := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", resSink, wsGep))
			e.ensureStreamPipeRuntime()
			rsrc := e.nodeStreamSide(ptr, 0)
			decode := e.emitStreamDecodeThunk(outTy)
			ign := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_pipe_to(ptr %s, ptr %s, ptr %s, i64 0, ptr null, ptr null)", ign, rsrc, resSink, decode))
			return dv, nil
		}
		if dv.Ty.IsClass {
			// A Writable of the `stream` module (an fs WriteStream, a user's
			// subclass): Readable.pipe's own loop — write each chunk, pause
			// until 'drain' when write() asks, end() at the end.
			return dv, e.emitPipeToStreamClass(ty, ptr, outTy, dv, pos)
		}
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

// emitPipeToStreamClass pipes a runtime Readable handle into an instance of
// a `stream` module class, as Readable.prototype.pipe does: each 'data'
// chunk is written, the source pauses when write() returns false until the
// destination's 'drain', and its 'end' ends the destination.
func (e *Emitter) emitPipeToStreamClass(srcTy Type, srcPtr string, chunkTy Type, dst Value, pos ast.Pos) error {
	bind := func(prefix string, ref string, ty Type) string {
		slot := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", ref, slot))
		name := fmt.Sprintf("%s_%s", prefix, slot[1:])
		e.define(name, Symbol{Ptr: slot, Ty: ty})
		return name
	}
	src := bind("__kml_pipe_src", srcPtr, srcTy)
	dstName := bind("__kml_pipe_dst", dst.Ref, dst.Ty)
	id := func(n string) ast.Expression { return ast.NewIdentifier(n, pos) }
	call := func(obj ast.Expression, m string, args ...ast.Expression) ast.Expression {
		return ast.NewCallExpression(ast.NewMemberExpression(obj, m, pos), args, pos)
	}
	stmt := func(x ast.Expression) ast.Statement { return ast.NewExpressionStatement(x, pos) }
	arrow := func(params []ast.Param, body ...ast.Statement) ast.Expression {
		return ast.NewArrowFunction(params, nil, nil, ast.NewBlockStatement(body, pos), pos)
	}
	var chunkAnn *ast.TypeAnnotation
	switch {
	case chunkTy.IsTypedArray:
		chunkAnn = &ast.TypeAnnotation{Name: "Uint8Array", Source: "ts"}
	case isStringTy(chunkTy):
		chunkAnn = &ast.TypeAnnotation{Name: "string", Source: "ts"}
	default:
		chunkAnn = &ast.TypeAnnotation{Name: "any", Source: "ts"}
	}
	onData := arrow([]ast.Param{{Name: "__kml_chunk", Type: chunkAnn}},
		ast.NewIfStatement(
			ast.NewUnaryExpression("!", true, call(id(dstName), "write", id("__kml_chunk")), pos),
			ast.NewBlockStatement([]ast.Statement{
				stmt(call(id(src), "pause")),
				stmt(call(id(dstName), "once", ast.NewStringLiteral("drain", pos), arrow(nil, stmt(call(id(src), "resume"))))),
			}, pos), nil, pos))
	onEnd := arrow(nil, stmt(call(id(dstName), "end")))
	for _, st := range []ast.Statement{
		stmt(call(id(src), "on", ast.NewStringLiteral("data", pos), onData)),
		stmt(call(id(src), "on", ast.NewStringLiteral("end", pos), onEnd)),
	} {
		if err := e.emitStmt(st); err != nil {
			return err
		}
	}
	return nil
}
