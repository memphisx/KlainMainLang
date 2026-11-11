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
	return e.emitStreamDecodeThunkTo(chunkTy, chunkTy)
}

// emitStreamDecodeThunkTo is emitStreamDecodeThunk for a destination whose
// chunks are destTy: each chunk converted to it (a number written to an
// untyped sink is its box).
func (e *Emitter) emitStreamDecodeThunkTo(chunkTy, destTy Type) string {
	resultTy := streamReadResultType(chunkTy)

	restore := e.beginThunkEmit()
	// Demote the (possibly `T | undefined`) value field back to its bare chunk
	// words; on a done read the words are ignored by the pipe/tee machinery.
	chunk := e.loadStreamResultValue("%rec", resultTy, chunkTy)
	if destTy.IR != chunkTy.IR || destTy.IsDynamic() != chunkTy.IsDynamic() {
		chunk = e.convert(chunk, destTy)
	}
	v0, v1 := e.streamChunkWords(chunk)
	dIdx, _, _ := resultTy.FieldIndex("done")
	dGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %%rec, i32 0, i32 %d", dGep, resultTy.StructIR(), dIdx))
	dI1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i1, "+irPtr+" %s, align 1", dI1, dGep))
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

	fn := e.defineContentNamed("@__kml_rs_decode.", "{ i64, i64, i64 }", irPtr+" %rec", body)
	return fn
}

// resolvePipeOptions evaluates a pipeTo/pipeThrough options object (any
// object; unknown members ignored) into a runtime flags register (1
// preventClose · 2 preventAbort · 4 preventCancel) and the abort-signal
// field pointers (or nulls). badSignal is a signal that is no AbortSignal,
// boxed: Node's error for it is the caller's to raise.
func (e *Emitter) resolvePipeOptions(opt ast.Expression, pos ast.Pos) (flagsRef, sigARef, sigRRef string, badSignal *pipeBadSignal, err error) {
	flagsRef, sigARef, sigRRef = "0", "null", "null"
	if opt == nil || isUndefinedLiteral(e, opt) {
		return
	}
	members, _, ok, err := e.streamUnderlying(opt, []string{"preventAbort", "preventCancel", "preventClose", "signal"})
	if err != nil {
		return "", "", "", nil, err
	}
	if !ok {
		return "", "", "", nil, internalAt(pos, "pipe options of no readable shape")
	}
	cur := "0"
	for _, f := range []struct {
		name string
		bit  int
	}{{"preventAbort", 2}, {"preventCancel", 4}, {"preventClose", 1}} {
		x := members[f.name]
		if x == nil {
			continue
		}
		v, err := e.emitExpr(x)
		if err != nil {
			return "", "", "", nil, err
		}
		v = e.convert(v, TypeBool)
		z, sh, nx := e.freshReg(), e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = zext i1 %s to i64", z, v.Ref))
		e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %d", sh, z, f.bit))
		e.emitInstr(fmt.Sprintf("%s = or i64 %s, %s", nx, cur, sh))
		cur = nx
	}
	flagsRef = cur
	if x := members["signal"]; x != nil {
		sv, err := e.emitExpr(x)
		if err != nil {
			return "", "", "", nil, err
		}
		switch {
		case e.isAbortSignalType(sv.Ty):
			// The class's private #aborted flag and #reason (emit_signal.go),
			// by the class's own layout.
			aIdx, _, sigTy, _ := e.classField(sv.Ty, "#aborted")
			rIdx, _, _, _ := e.classField(sv.Ty, "#reason")
			ag := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 %d", ag, sigTy.StructIR(), sv.Ref, aIdx))
			rg := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 %d", rg, sigTy.StructIR(), sv.Ref, rIdx))
			sigARef, sigRRef = ag, rg
		case sv.Ty.IsUndefined() && !sv.Ty.IsDynamic():
		case sv.Ty.IsDynamic():
			// Known at run time: undefined is none, an AbortSignal its
			// fields, anything else bad (the caller's error).
			sigTy, haveSig := e.abortSignalClass()
			aSlot, rSlot := e.freshReg(), e.freshReg()
			e.emitAlloca(fmt.Sprintf("%s = alloca "+irPtr+", align 8", aSlot))
			e.emitAlloca(fmt.Sprintf("%s = alloca "+irPtr+", align 8", rSlot))
			e.emitInstr(fmt.Sprintf("store "+irPtr+" null, "+irPtr+" %s, align 8", aSlot))
			e.emitInstr(fmt.Sprintf("store "+irPtr+" null, "+irPtr+" %s, align 8", rSlot))
			id := e.bindValue(sv, pos)
			badSlot := e.freshReg()
			e.emitAlloca(fmt.Sprintf("%s = alloca i1, align 1", badSlot))
			e.emitInstr(fmt.Sprintf("store i1 false, "+irPtr+" %s, align 1", badSlot))
			isU, err := e.emitExpr(ast.NewBinaryExpression("===", id, ast.NewNullLiteral(true, pos), pos))
			if err != nil {
				return "", "", "", nil, err
			}
			isU = e.convert(isU, TypeBool)
			chkL, doneL := e.freshLabel("pipe.sig.chk"), e.freshLabel("pipe.sig.done")
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isU.Ref, doneL, chkL))
			e.emitLabel(chkL)
			// (A program that names no AbortSignal makes none.)
			isS := Value{Ref: "false", Ty: TypeBool}
			if haveSig {
				if isS, err = e.emitAnyInstanceOfClass(sv, sigTy.ClassName); err != nil {
					return "", "", "", nil, err
				}
				isS = e.convert(isS, TypeBool)
			}
			sigL, badL := e.freshLabel("pipe.sig.ok"), e.freshLabel("pipe.sig.bad")
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isS.Ref, sigL, badL))
			e.emitLabel(sigL)
			if haveSig {
				sp := e.convert(sv, sigTy)
				aIdx, _, st, _ := e.classField(sigTy, "#aborted")
				rIdx, _, _, _ := e.classField(sigTy, "#reason")
				ag, rg := e.freshReg(), e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 %d", ag, st.StructIR(), sp.Ref, aIdx))
				e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 %d", rg, st.StructIR(), sp.Ref, rIdx))
				e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", ag, aSlot))
				e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", rg, rSlot))
				e.emitTerminator("br label %" + doneL)
			} else {
				e.emitTerminator("unreachable")
			}
			e.emitLabel(badL)
			e.emitInstr(fmt.Sprintf("store i1 true, "+irPtr+" %s, align 1", badSlot))
			e.emitTerminator("br label %" + doneL)
			e.emitLabel(doneL)
			la, lr, lb := e.freshReg(), e.freshReg(), e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", la, aSlot))
			e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", lr, rSlot))
			e.emitInstr(fmt.Sprintf("%s = load i1, "+irPtr+" %s, align 1", lb, badSlot))
			sigARef, sigRRef = la, lr
			badSignal = &pipeBadSignal{v: sv, cond: lb}
		default:
			b, err := e.emitBoxValue(sv)
			if err != nil {
				return "", "", "", nil, err
			}
			badSignal = &pipeBadSignal{v: b}
		}
	}
	return
}

// pipeBadSignal is a pipe's signal option that is no AbortSignal (boxed):
// always, or when cond (an i1) holds at run time.
type pipeBadSignal struct {
	v    Value
	cond string
}

// emitThrowBadSignal throws Node's error for a bad signal option: always,
// or where its run-time condition holds.
func (e *Emitter) emitThrowBadSignal(bad *pipeBadSignal) error {
	contL := e.freshLabel("pipe.sigok")
	if bad.cond != "" {
		badL := e.freshLabel("pipe.badsig")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", bad.cond, badL, contL))
		e.emitLabel(badL)
	}
	if err := e.emitThrowArgType(pipeSignalMsg, bad.v); err != nil {
		return err
	}
	e.emitLabel(contL)
	return nil
}

const pipeSignalMsg = `The "options.signal" property must be an instance of AbortSignal.`

// emitArgTypeErrorPtr is Node's ERR_INVALID_ARG_TYPE TypeError for v
// (boxed), prefix its message before the "Received …" part.
func (e *Emitter) emitArgTypeErrorPtr(prefix string, v Value) (string, error) {
	e.ensureDynJSONC()
	e.ensureExceptionHelpers()
	suffix := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_received(i64 %s)", suffix, v.Ref))
	msg, err := e.emitStringConcat(Value{Ref: e.internString(prefix), Ty: TypePtr}, Value{Ref: suffix, Ty: TypePtr})
	if err != nil {
		return "", err
	}
	return e.buildErrorObjWithCode(errorKindIDs["TypeError"], msg.Ref, e.internString("TypeError"), e.internString("ERR_INVALID_ARG_TYPE"), "0.0", "null"), nil
}

// emitThrowArgType throws emitArgTypeErrorPtr's error; the block after it
// is dead.
func (e *Emitter) emitThrowArgType(prefix string, v Value) error {
	errPtr, err := e.emitArgTypeErrorPtr(prefix, v)
	if err != nil {
		return err
	}
	e.excThrow(errPtr)
	e.emitTerminator("unreachable")
	return nil
}

// emitRejectedArgType is a promise rejected with emitArgTypeErrorPtr's
// error.
func (e *Emitter) emitRejectedArgType(prefix string, v Value) (Value, error) {
	e.ensureMicrotasks()
	e.ensureUnhandledRejections()
	errPtr, err := e.emitArgTypeErrorPtr(prefix, v)
	if err != nil {
		return Value{}, err
	}
	bits, p := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = ptrtoint "+irPtr+" %s to i64", bits, errPtr))
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_task_alloc_promise()", p))
	e.emitInstr(fmt.Sprintf("call void @__kml_promise_reject_err("+irPtr+" %s, i64 %s)", p, bits))
	pt := PromiseOf(TypeVoid)
	pt.PromiseTask = true
	return Value{Ref: p, Ty: pt}, nil
}

// emitStreamPipeTo implements `src.pipeTo(dest, options?)`.
func (e *Emitter) emitStreamPipeTo(srcPtr string, chunkTy Type, args []ast.Expression, pos ast.Pos) (Value, error) {
	e.ensureMicrotasks()
	args, err := e.dropArgsPast(args, 2, pos)
	if err != nil {
		return Value{}, err
	}
	e.ensureNanBox()
	e.ensureExceptionHelpers()
	e.ensurePromiseAddReaction()
	e.ensureShapeRuntime()
	e.ensureUnhandledRejections()
	if len(args) == 0 || e.omittedArg(args[0]) || isUndefinedLiteral(e, args[0]) {
		return e.emitRejectedArgType(`The "transform.writable" property must be an instance of WritableStream.`, Value{Ref: fmt.Sprint(nbUndefined), Ty: TypeAny})
	}
	dv, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	var opt ast.Expression
	if len(args) == 2 {
		opt = args[1]
	}
	const destMsg = `The "transform.writable" property must be an instance of WritableStream.`
	var resSlot, doneL string
	// rejectWhere branches to a promise rejected with Node's error for v
	// where cond holds (Node's pipeTo is async), continuing otherwise.
	rejectWhere := func(cond, msg string, v Value) error {
		if resSlot == "" {
			resSlot, doneL = e.freshReg(), e.freshLabel("pipeto.done")
			e.emitAlloca(fmt.Sprintf("%s = alloca "+irPtr+", align 8", resSlot))
		}
		badL, okL := e.freshLabel("pipeto.bad"), e.freshLabel("pipeto.ok")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", cond, badL, okL))
		e.emitLabel(badL)
		rp, err := e.emitRejectedArgType(msg, v)
		if err != nil {
			return err
		}
		e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", rp.Ref, resSlot))
		e.emitTerminator("br label %" + doneL)
		e.emitLabel(okL)
		return nil
	}
	if !dv.Ty.IsWritableStream() {
		b, err := e.emitBoxValue(dv)
		if err != nil {
			return Value{}, err
		}
		if !dv.Ty.IsDynamic() {
			return e.emitRejectedArgType(destMsg, b)
		}
		// Known at run time: the destination checked before the options.
		isW, err := e.emitExpr(ast.NewBinaryExpression("instanceof", e.bindValue(b, pos), e.builtinIdent("WritableStream", pos), pos))
		if err != nil {
			return Value{}, err
		}
		isW = e.convert(isW, TypeBool)
		notW := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = xor i1 %s, true", notW, isW.Ref))
		if err := rejectWhere(notW, destMsg, b); err != nil {
			return Value{}, err
		}
		dv = e.emitUnboxHost(b, WritableStreamType(TypeAny))
	}
	if opt != nil && !isUndefinedLiteral(e, opt) {
		// The options, evaluated once: a primitive other than undefined or
		// null rejects, as Node's validateObject does.
		ov, err := e.emitExpr(opt)
		if err != nil {
			return Value{}, err
		}
		ob, err := e.emitBoxValue(ov)
		if err != nil {
			return Value{}, err
		}
		bx := e.bindValue(ob, pos)
		opt = e.bindValue(ov, pos)
		if ov.Ty.IsDynamic() || ov.Ty.Nullable() || ov.Ty.IsNull() {
			// null reads as no options.
			opt = ast.NewBinaryExpression("??", bx, ast.NewObjectLiteral(nil, pos), pos)
		}
		str := func(s string) ast.Expression { return ast.NewStringLiteral(s, pos) }
		ty := ast.NewUnaryExpression("typeof", true, bx, pos)
		bad := ast.NewBinaryExpression("&&",
			ast.NewBinaryExpression("!==", ty, str("undefined"), pos),
			ast.NewBinaryExpression("&&",
				ast.NewBinaryExpression("!==", ast.NewUnaryExpression("typeof", true, bx, pos), str("object"), pos),
				ast.NewBinaryExpression("!==", ast.NewUnaryExpression("typeof", true, bx, pos), str("function"), pos), pos), pos)
		cv, err := e.emitExpr(bad)
		if err != nil {
			return Value{}, err
		}
		if err := rejectWhere(e.convert(cv, TypeBool).Ref, `The "options" argument must be of type object.`, ob); err != nil {
			return Value{}, err
		}
	}
	flags, sigA, sigR, bad, err := e.resolvePipeOptions(opt, pos)
	if err != nil {
		return Value{}, err
	}
	if bad != nil && bad.cond == "" && resSlot == "" {
		return e.emitRejectedArgType(pipeSignalMsg, bad.v)
	}
	if bad != nil {
		cond := bad.cond
		if cond == "" {
			cond = "true"
		}
		if err := rejectWhere(cond, pipeSignalMsg, bad.v); err != nil {
			return Value{}, err
		}
	}
	// Lock both ends like the spec does.
	l1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_rs_lock("+irPtr+" %s)", l1, srcPtr))
	e.streamThrowTypeError(l1, "Invalid state: The ReadableStream is locked")
	l2 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_ws_lock("+irPtr+" %s)", l2, dv.Ref))
	e.streamThrowTypeError(l2, "Invalid state: The WritableStream is locked")
	destTy := chunkTy
	if dv.Ty.StreamChunk != nil {
		destTy = *dv.Ty.StreamChunk
	}
	decode := e.emitStreamDecodeThunkTo(chunkTy, destTy)
	flags = e.pipeSourceAny(srcPtr, chunkTy, flags)
	prom := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_pipe_to("+irPtr+" %s, "+irPtr+" %s, "+irPtr+" %s, i64 %s, "+irPtr+" %s, "+irPtr+" %s)",
		prom, srcPtr, dv.Ref, decode, flags, sigA, sigR))
	pt := PromiseOf(TypeVoid)
	pt.PromiseTask = true
	if resSlot != "" {
		e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", prom, resSlot))
		e.emitTerminator("br label %" + doneL)
		e.emitLabel(doneL)
		prom = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", prom, resSlot))
	}
	return Value{Ref: prom, Ty: pt}, nil
}

// emitStreamPipeThrough implements `src.pipeThrough(transform, options?)` —
// start the pipe into the transform's writable side, return its readable.
func (e *Emitter) emitStreamPipeThrough(srcPtr string, chunkTy Type, args []ast.Expression, pos ast.Pos) (Value, error) {
	e.ensureMicrotasks()
	args, err := e.dropArgsPast(args, 2, pos)
	if err != nil {
		return Value{}, err
	}
	e.ensureNanBox()
	e.ensureExceptionHelpers()
	e.ensurePromiseAddReaction()
	e.ensureShapeRuntime()
	e.ensureUnhandledRejections()
	if len(args) == 0 || e.omittedArg(args[0]) || isUndefinedLiteral(e, args[0]) {
		// transform.readable of undefined: Node reports it undefined.
		if err := e.emitThrowArgType(`The "transform.readable" property must be an instance of ReadableStream.`, Value{Ref: fmt.Sprint(nbUndefined), Ty: TypeAny}); err != nil {
			return Value{}, err
		}
		e.emitLabel(e.freshLabel("pipethrough.dead"))
		return Value{Ref: fmt.Sprint(nbUndefined), Ty: TypeAny}, nil
	}
	tv, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	var opt ast.Expression
	if len(args) == 2 {
		opt = args[1]
	}
	// The transform's two sides: a native TransformStream's own, or any
	// other value's `readable` and `writable` (a TextEncoderStream, a
	// class of the program's, an object literal), as Node reads them.
	var rPtr, wPtr string
	inTy, outTy := chunkTy, TypeI64
	if tv.Ty.IsTransformStream() {
		rPtr, wPtr = e.transformSides(tv.Ref)
		if tv.Ty.StreamChunk != nil {
			inTy = *tv.Ty.StreamChunk
		}
		if tv.Ty.StreamOut != nil {
			outTy = *tv.Ty.StreamOut
		}
	} else {
		inTy, outTy = TypeAny, TypeAny
		b, err := e.emitBoxValue(tv)
		if err != nil {
			return Value{}, err
		}
		rSlot, wSlot := e.freshReg(), e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca "+irPtr+", align 8", rSlot))
		e.emitAlloca(fmt.Sprintf("%s = alloca "+irPtr+", align 8", wSlot))
		joinL := e.freshLabel("pipethrough.sides")
		if tv.Ty.IsDynamic() {
			// Known at run time: a native TransformStream's own sides.
			isT, err := e.emitExpr(ast.NewBinaryExpression("instanceof", e.bindValue(b, pos), e.builtinIdent("TransformStream", pos), pos))
			if err != nil {
				return Value{}, err
			}
			isT = e.convert(isT, TypeBool)
			natL, genL := e.freshLabel("pipethrough.native"), e.freshLabel("pipethrough.generic")
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isT.Ref, natL, genL))
			e.emitLabel(natL)
			t := e.emitUnboxHost(b, Type{IR: irPtr, Host: hostTransformStream, StreamChunk: ptrTo(TypeAny), StreamOut: ptrTo(TypeAny)})
			r, w := e.transformSides(t.Ref)
			e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", r, rSlot))
			e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", w, wSlot))
			e.emitTerminator(fmt.Sprintf("br label %%%s", joinL))
			e.emitLabel(genL)
		}
		side := func(name, class, msg string, slot string, ty Type) error {
			v, err := e.emitExpr(ast.NewMemberExpression(e.bindValue(tv, pos), name, pos))
			if err != nil {
				return err
			}
			if v, err = e.emitBoxValue(v); err != nil {
				return err
			}
			ok, err := e.emitExpr(ast.NewBinaryExpression("instanceof", e.bindValue(v, pos), e.builtinIdent(class, pos), pos))
			if err != nil {
				return err
			}
			ok = e.convert(ok, TypeBool)
			goodL, badL := e.freshLabel("pipethrough.side"), e.freshLabel("pipethrough.badside")
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", ok.Ref, goodL, badL))
			e.emitLabel(badL)
			if err := e.emitThrowArgType(msg, v); err != nil {
				return err
			}
			e.emitLabel(goodL)
			h := e.emitUnboxHost(v, ty)
			e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", h.Ref, slot))
			return nil
		}
		if err := side("readable", "ReadableStream", `The "transform.readable" property must be an instance of ReadableStream.`, rSlot, ReadableStreamType(TypeAny)); err != nil {
			return Value{}, err
		}
		if err := side("writable", "WritableStream", `The "transform.writable" property must be an instance of WritableStream.`, wSlot, WritableStreamType(TypeAny)); err != nil {
			return Value{}, err
		}
		e.emitTerminator(fmt.Sprintf("br label %%%s", joinL))
		e.emitLabel(joinL)
		rPtr, wPtr = e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", rPtr, rSlot))
		e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", wPtr, wSlot))
	}
	flags, sigA, sigR, bad, err := e.resolvePipeOptions(opt, pos)
	if err != nil {
		return Value{}, err
	}
	if bad != nil {
		// pipeThrough checks its options at once: thrown.
		if err := e.emitThrowBadSignal(bad); err != nil {
			return Value{}, err
		}
	}
	l1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_rs_lock("+irPtr+" %s)", l1, srcPtr))
	e.streamThrowTypeError(l1, "Invalid state: The ReadableStream is locked")
	l2 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_ws_lock("+irPtr+" %s)", l2, wPtr))
	e.streamThrowTypeError(l2, "Invalid state: The WritableStream is locked")
	decode := e.emitStreamDecodeThunkTo(chunkTy, inTy)
	flags = e.pipeSourceAny(srcPtr, chunkTy, flags)
	ign := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_pipe_to("+irPtr+" %s, "+irPtr+" %s, "+irPtr+" %s, i64 %s, "+irPtr+" %s, "+irPtr+" %s)",
		ign, srcPtr, wPtr, decode, flags, sigA, sigR))
	return Value{Ref: rPtr, Ty: ReadableStreamType(outTy)}, nil
}

// pipeSourceAny makes a pipe from a source viewed as ReadableStream<any>
// read it as an `any` reader does: the producer's chunks boxed by the
// stream's own boxFn (a Uint8Array chunk is a box of it, not its raw
// pointer read as one). flags gains the pipe's source-any bit.
func (e *Emitter) pipeSourceAny(srcPtr string, chunkTy Type, flags string) string {
	if !chunkTy.IsDynamic() {
		return flags
	}
	ff := e.emitStreamFulfillThunk(chunkTy)
	e.emitInstr(fmt.Sprintf("call void @__kml_rs_set_any_fulfill("+irPtr+" %s, "+irPtr+" %s)", srcPtr, ff))
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = or i64 %s, 8", r, flags))
	return r
}

// transformSides loads a native TransformStream context's readable (field
// 0) and writable (field 1) streams.
func (e *Emitter) transformSides(ctx string) (string, string) {
	var out [2]string
	for i := range out {
		gep, v := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr { "+irPtr+", "+irPtr+" }, "+irPtr+" %s, i32 0, i32 %d", gep, ctx, i))
		e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", v, gep))
		out[i] = v
	}
	return out[0], out[1]
}

// emitStreamTee implements `src.tee()` — two branch streams over a shared
// one-read-at-a-time context.
func (e *Emitter) emitStreamTee(srcPtr string, chunkTy Type, pos ast.Pos) (Value, error) {
	e.ensureMicrotasks()
	e.ensureNanBox()
	e.ensureExceptionHelpers()
	e.ensurePromiseAddReaction()
	e.ensureShapeRuntime()
	e.ensureUnhandledRejections()
	l1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_rs_lock("+irPtr+" %s)", l1, srcPtr))
	e.streamThrowTypeError(l1, "Invalid state: ReadableStream is locked")

	fulfill := e.emitStreamFulfillThunk(chunkTy)
	decode := e.emitStreamDecodeThunk(chunkTy)
	b1 := e.freshReg()
	e.emitRsAlloc(b1, "1.0", fulfill, chunkTy)
	b2 := e.freshReg()
	e.emitRsAlloc(b2, "1.0", fulfill, chunkTy)

	ctx := e.freshReg()
	e.heapMallocInto(ctx, allocHost, "80")
	storePtrAt := func(base string, idx int, val string) {
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr "+irPtr+", "+irPtr+" %s, i64 %d", gep, base, idx))
		e.heapStore(irPtr, val, gep, 8)
	}
	storePtrAt(ctx, 0, srcPtr)
	storePtrAt(ctx, 1, b1)
	storePtrAt(ctx, 2, b2)
	storePtrAt(ctx, 3, decode)
	storePtrAt(ctx, 9, "null") // the branches' shared cancel promise, made on the first cancel
	for i := 4; i <= 8; i++ {
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i64, "+irPtr+" %s, i64 %d", gep, ctx, i))
		e.heapStore("i64", "0", gep, 8)
	}

	pullClo := e.buildBuiltinClosure("@__kml_tee_pull", ctx)
	e.storeStreamField(b1, 9, pullClo)
	e.storeStreamField(b2, 9, pullClo)
	for i, b := range []string{b1, b2} {
		env := e.freshReg()
		e.heapMallocInto(env, allocClosure, "16")
		g0 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr { "+irPtr+", "+irPtr+" }, "+irPtr+" %s, i32 0, i32 0", g0, env))
		e.heapStore(irPtr, ctx, g0, 8)
		g1 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr { "+irPtr+", "+irPtr+" }, "+irPtr+" %s, i32 0, i32 1", g1, env))
		which := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %d to "+irPtr, which, i+1))
		e.heapStore(irPtr, which, g1, 8)
		e.storeStreamField(b, 10, e.buildBuiltinClosure("@__kml_tee_cancel", env))
		started := e.buildBuiltinClosure("@__kml_rs_started", b)
		e.emitInstr(fmt.Sprintf("call void @__kml_microtask_enqueue("+irPtr+" %s)", started))
	}

	// Result: a two-element array of branch streams.
	arr := e.freshReg()
	e.heapMallocInto(arr, allocElems, "16")
	s0 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr "+irPtr+", "+irPtr+" %s, i64 0", s0, arr))
	e.heapStore(irPtr, b1, s0, 8)
	s1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr "+irPtr+", "+irPtr+" %s, i64 1", s1, arr))
	e.heapStore(irPtr, b2, s1, 8)
	agg1 := e.newArrayHeader(arr, "2")
	return Value{Ref: agg1, Ty: ArrayOf(ReadableStreamType(chunkTy))}, nil
}

// emitNewTransformStream implements
// `new TransformStream<I, O>({transform?, flush?}?, writableStrategy?, readableStrategy?)`.
// emitTransformCtxAlloc allocates a TransformStream's 72-byte context, the
// stream's handle, after the host header word of ty (TDD-00230 P3.3).
func (e *Emitter) emitTransformCtxAlloc(ty Type) string {
	base, ctx := e.freshReg(), e.freshReg()
	e.heapMallocInto(base, allocHost, "80")
	e.heapStore("i64", fmt.Sprint(e.hostHeaderWord(ty)), base, 8)
	e.emitBodyAddr(ctx, base)
	return ctx
}

// compressionTag is a compression TransformStream's class.
// compressionStreamType is a CompressionStream's (or, decompress, a
// DecompressionStream's) handle: a transform of bytes to bytes.
func compressionStreamType(decompress bool) Type {
	u8i, u8o := TypedArrayType("uint8"), TypedArrayType("uint8")
	return Type{IR: irPtr, Host: hostTransformStream, StreamChunk: &u8i, StreamOut: &u8o, HostTag: compressionTag(decompress)}
}

func compressionTag(decompress bool) string {
	if decompress {
		return "DecompressionStream"
	}
	return "CompressionStream"
}

func (e *Emitter) emitNewTransformStream(ex *ast.NewTransformStreamExpression) (Value, error) {
	e.ensureMicrotasks()
	pos := ex.GetPos()
	inTy, outTy := TypeAny, TypeAny // TransformStream<I = any, O = any>
	if ex.InType != nil {
		inTy = e.annotationType(ex.InType)
	}
	if ex.OutType != nil {
		outTy = e.annotationType(ex.OutType)
	}
	e.ensureNanBox()
	e.ensureExceptionHelpers()
	e.ensurePromiseAddReaction()
	e.ensureShapeRuntime()
	e.ensureUnhandledRejections()

	var startExpr, transformExpr, flushExpr ast.Expression
	if ex.Transformer != nil {
		members, _, ok, err := e.streamUnderlying(ex.Transformer, []string{"start", "transform", "flush"})
		if err != nil {
			return Value{}, err
		}
		if !ok {
			return Value{}, internalAt(pos, "an underlying object streamUnderlying did not read")
		}
		startExpr, transformExpr, flushExpr = members["start"], members["transform"], members["flush"]
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
	e.emitRsAlloc(rs, rHwm, fulfill, outTy)
	ws := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_ws_alloc(double %s)", ws, wHwm))

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

	ctx := e.emitTransformCtxAlloc(Type{IR: irPtr, Host: hostTransformStream})
	storeCtxPtr := func(idx int, val string) {
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr "+irPtr+", "+irPtr+" %s, i64 %d", gep, ctx, idx))
		e.heapStore(irPtr, val, gep, 8)
	}
	storeCtxPtr(0, rs)
	storeCtxPtr(1, ws)
	rsCtrlTy := RSControllerType(outTy)
	if startExpr != nil {
		// start(controller), now: the readable side's controller.
		userClo, err := e.streamCallbackClosure(startExpr, []Type{rsCtrlTy}, "start callback", pos)
		if err != nil {
			return Value{}, err
		}
		fp, fpp, ep, epp := e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr { "+irPtr+", "+irPtr+" }, "+irPtr+" %s, i32 0, i32 0", fpp, userClo.Ref))
		e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", fp, fpp))
		e.emitInstr(fmt.Sprintf("%s = getelementptr { "+irPtr+", "+irPtr+" }, "+irPtr+" %s, i32 0, i32 1", epp, userClo.Ref))
		e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", ep, epp))
		args, sig := fmt.Sprintf(irPtr+" %s", ep), irPtr
		if len(userClo.Ty.FuncParams) == 1 {
			args += fmt.Sprintf(", "+irPtr+" %s", rs)
			sig += ", " + irPtr
		}
		if userClo.Ty.FuncRetType != nil && userClo.Ty.FuncRetType.IsDynamic() {
			b := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call i64 (%s) %s(%s)", b, sig, fp, args))
		} else if callbackReturnsPromise(userClo.Ty) {
			p := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" (%s) %s(%s)", p, sig, fp, args))
			e.emitMarkPromiseHandled(p)
		} else {
			e.emitInstr(fmt.Sprintf("call void (%s) %s(%s)", sig, fp, args))
		}
	}
	if transformExpr != nil {
		userClo, err := e.streamCallbackClosure(transformExpr, []Type{inTy, rsCtrlTy}, "transform callback", pos)
		if err != nil {
			return Value{}, err
		}
		if len(userClo.Ty.FuncParams) > 2 {
			return Value{}, internalAt(pos, "transform callback of %d parameters past its adapter", len(userClo.Ty.FuncParams))
		}
		wrap := e.streamWrap(transformExpr, func() string { return e.emitStreamWriteWrap(userClo.Ty, inTy) })
		env := e.freshReg()
		e.heapMallocInto(env, allocClosure, "16")
		g0 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr { "+irPtr+", "+irPtr+" }, "+irPtr+" %s, i32 0, i32 0", g0, env))
		e.heapStore(irPtr, userClo.Ref, g0, 8)
		g1 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr { "+irPtr+", "+irPtr+" }, "+irPtr+" %s, i32 0, i32 1", g1, env))
		e.heapStore(irPtr, rs, g1, 8)
		storeCtxPtr(2, e.buildBuiltinClosure(wrap, env))
	} else {
		if inTy.IR != outTy.IR || inTy.IsArray() != outTy.IsArray() || isNullableScalar(inTy) != isNullableScalar(outTy) {
			// Chunks of one type read as another: each passes through
			// converted, as `chunk as unknown as O` converts.
			storeCtxPtr(2, e.buildBuiltinClosure(e.emitStreamConvertWrap(inTy, outTy), rs))
		} else {
			storeCtxPtr(2, "null")
		}
	}
	if flushExpr != nil {
		userClo, err := e.streamCallbackClosure(flushExpr, []Type{rsCtrlTy}, "flush callback", pos)
		if err != nil {
			return Value{}, err
		}
		if len(userClo.Ty.FuncParams) > 1 {
			return Value{}, internalAt(pos, "flush callback of %d parameters past its adapter", len(userClo.Ty.FuncParams))
		}
		wrap := e.streamWrap(flushExpr, func() string { return e.emitStreamPullWrap(userClo.Ty) })
		env := e.freshReg()
		e.heapMallocInto(env, allocClosure, "16")
		g0 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr { "+irPtr+", "+irPtr+" }, "+irPtr+" %s, i32 0, i32 0", g0, env))
		e.heapStore(irPtr, userClo.Ref, g0, 8)
		g1 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr { "+irPtr+", "+irPtr+" }, "+irPtr+" %s, i32 0, i32 1", g1, env))
		e.heapStore(irPtr, rs, g1, 8)
		storeCtxPtr(3, e.buildBuiltinClosure(wrap, env))
	} else {
		storeCtxPtr(3, "null")
	}
	for i := 4; i <= 6; i++ {
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i64, "+irPtr+" %s, i64 %d", gep, ctx, i))
		e.heapStore("i64", "0", gep, 8)
	}
	storeCtxPtr(7, "null")
	storeCtxPtr(8, "null")

	// Wire the writable's sink and the readable's pull to the ts machinery.
	e.storeWStreamField(ws, 9, e.buildBuiltinClosure("@__kml_ts_sink_write", ctx))
	e.storeWStreamField(ws, 10, e.buildBuiltinClosure("@__kml_ts_sink_close", ctx))
	e.storeWStreamField(ws, 11, e.buildBuiltinClosure("@__kml_ts_sink_abort", ctx))
	e.storeStreamField(rs, 9, e.buildBuiltinClosure("@__kml_ts_pull", ctx))
	e.emitInstr(fmt.Sprintf("call void @__kml_microtask_enqueue("+irPtr+" %s)", e.buildBuiltinClosure("@__kml_rs_started", rs)))
	e.emitInstr(fmt.Sprintf("call void @__kml_microtask_enqueue("+irPtr+" %s)", e.buildBuiltinClosure("@__kml_ws_started", ws)))

	i, o := inTy, outTy
	return Value{Ref: ctx, Ty: Type{IR: irPtr, Host: hostTransformStream, StreamChunk: &i, StreamOut: &o}}, nil
}

// emitTransformStreamSide is a TransformStream's side held in slot (0 its
// readable, 1 its writable), typed ty.
func (e *Emitter) emitTransformStreamSide(ex *ast.MemberExpression, slot int, ty Type) (Value, error) {
	tv, err := e.emitExpr(ex.Object)
	if err != nil {
		return Value{}, err
	}
	gep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { "+irPtr+", "+irPtr+" }, "+irPtr+" %s, i32 0, i32 %d", gep, tv.Ref, slot))
	p := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", p, gep))
	return Value{Ref: p, Ty: ty}, nil
}

// emitLockUsedBodyStream marks stream s disturbed and locked (the state a
// body method leaves its body stream in) when the Response's body is used.
func (e *Emitter) emitLockUsedBodyStream(objVal Value, s string) {
	fIdx, _, _ := objVal.Ty.FieldIndex("__kml_bodyflags")
	flags := e.loadFieldValue(objVal, fIdx, TypeI64)
	u := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = and i64 %s, %d", u, flags.Ref, responseBodyUsed))
	used := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", used, u))
	e.emitLockStreamIf(used, s)
}

// emitLockStreamIf sets stream s's disturbed and locked flags when cond
// holds (s may be null).
func (e *Emitter) emitLockStreamIf(cond, s string) {
	nn := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne "+irPtr+" %s, null", nn, s))
	do := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", do, cond, nn))
	lockL, skipL := e.freshLabel("rs.lockused"), e.freshLabel("rs.lockused.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", do, lockL, skipL))
	e.emitLabel(lockL)
	fp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 11", fp, rstreamStructIR, s))
	f := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", f, fp))
	f2 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = or i64 %s, 48", f2, f))
	e.heapStore("i64", f2, fp, 8)
	e.emitTerminator(fmt.Sprintf("br label %%%s", skipL))
	e.emitLabel(skipL)
}

// emitResponseBodyStream implements `res.body` (TDD-00097 Stage 4): a
// ReadableStream<Uint8Array> fed by libcurl's write callback for a still-live
// transfer (pausing at the high-water mark, unpausing on pull), or a one-chunk
// replay of the buffered body for an already-finished Response. The stream
// is kept in __kml_stream, so every read is the same stream; a Response
// constructed without a body has a null `.body`.
func (e *Emitter) emitResponseBodyStream(ex *ast.MemberExpression) (Value, error) {
	objVal, err := e.emitExpr(ex.Object)
	if err != nil {
		return Value{}, err
	}
	e.ensureFetchAsync()
	e.ensureMicrotasks()
	e.ensureStrHeaderRuntime()
	e.ensureExceptionHelpers()
	e.ensureMicrotasks()
	e.ensureNanBox()
	e.ensurePromiseAddReaction()
	e.ensureShapeRuntime()
	e.ensureUnhandledRejections()
	chunkTy := TypedArrayType("uint8")
	fulfill := e.emitStreamFulfillThunk(chunkTy)
	sIdx, _, _ := objVal.Ty.FieldIndex("__kml_stream")
	sGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 %d", sGep, objVal.Ty.StructIR(), objVal.Ref, sIdx))
	cached := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", cached, sGep))
	fIdx, _, _ := objVal.Ty.FieldIndex("__kml_bodyflags")
	flags := e.loadFieldValue(objVal, fIdx, TypeI64)
	nb := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = and i64 %s, %d", nb, flags.Ref, responseNullBody))
	isNull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", isNull, nb))
	have := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne "+irPtr+" %s, null", have, cached))
	done := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", done, isNull, have))
	fromL := e.freshLabel("resp.body.cached")
	buildL := e.freshLabel("resp.body.build")
	joinL := e.freshLabel("resp.body.join")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", done, fromL, buildL))
	e.emitLabel(fromL)
	e.emitTerminator(fmt.Sprintf("br label %%%s", joinL))
	e.emitLabel(buildL)
	s := e.freshReg()
	e.emitRsAlloc(s, "1.0", fulfill, chunkTy)

	pIdx, _, _ := objVal.Ty.FieldIndex("__kml_pending")
	pGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 %d", pGep, objVal.Ty.StructIR(), objVal.Ref, pIdx))
	pending := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", pending, pGep))

	// The pull closure unpauses the transfer; started immediately via
	// microtask (pull only matters once reads park).
	e.storeStreamField(s, 9, e.buildBuiltinClosure("@__kml_fetch_body_pull", pending))
	started := e.buildBuiltinClosure("@__kml_rs_started", s)
	e.emitInstr(fmt.Sprintf("call void @__kml_microtask_enqueue("+irPtr+" %s)", started))

	bIdx, _, _ := objVal.Ty.FieldIndex("body")
	bVal := e.loadFieldValue(objVal, bIdx, TypePtr)
	lIdx, _, _ := objVal.Ty.FieldIndex("bodyLength")
	lVal := e.loadFieldValue(objVal, lIdx, TypeI64)
	actual := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_fetch_body_stream("+irPtr+" %s, "+irPtr+" %s, "+irPtr+" %s, i64 %s)",
		actual, pending, s, bVal.Ref, lVal.Ref))
	e.heapStore(irPtr, actual, sGep, 8)
	// After a body method the stream is the consumed one: locked, read.
	e.emitLockUsedBodyStream(objVal, actual)
	buildEnd := e.freshLabel("resp.body.built")
	e.emitTerminator(fmt.Sprintf("br label %%%s", buildEnd))
	e.emitLabel(buildEnd)
	e.emitTerminator(fmt.Sprintf("br label %%%s", joinL))
	e.emitLabel(joinL)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = phi "+irPtr+" [ %s, %%%s ], [ %s, %%%s ]", r, cached, fromL, actual, buildEnd))
	return Value{Ref: r, Ty: bodyStreamType()}, nil
}

// bodyStreamType is a Request's or Response's `body`:
// ReadableStream<Uint8Array> | null.
func bodyStreamType() Type {
	t := ReadableStreamType(TypedArrayType("uint8"))
	t.setNullable(true)
	return t
}

// emitNewCompressionStream implements `new CompressionStream(format)` /
// `new DecompressionStream(format)` (TDD-00097 Stage 6): a TransformStream
// value whose transform/flush closures are the native zlib pump
// (runtime_streams_zlib.go).
func (e *Emitter) emitNewCompressionStream(ex *ast.NewCompressionStreamExpression) (Value, error) {
	// windowBits per format; inflate uses 15+32 auto-detection for the two
	// wrapped formats, raw uses -15 on both sides. A format known at compile
	// time is a constant; any other is read at run time, and one that is no
	// CompressionFormat throws Node's TypeError.
	var wb string
	known := map[string][2]int{"gzip": {31, 47}, "deflate": {15, 47}, "deflate-raw": {-15, -15}}
	if lit, ok := ex.Format.(*ast.StringLiteral); ok && known[lit.Value] != [2]int{} {
		w := known[lit.Value][0]
		if ex.Decompress {
			w = known[lit.Value][1]
		}
		wb = fmt.Sprint(w)
	} else {
		fv, err := e.emitExpr(ex.Format)
		if err != nil {
			return Value{}, err
		}
		str, err := e.emitValueToString(fv)
		if err != nil {
			return Value{}, err
		}
		dec := 0
		if ex.Decompress {
			dec = 1
		}
		wb = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_zs_wbits("+irPtr+" %s, i64 %d)", wb, str.Ref, dec))
		bad := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", bad, wb))
		throwL, okL := e.freshLabel("zs.badfmt"), e.freshLabel("zs.fmt")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", bad, throwL, okL))
		e.emitLabel(throwL)
		msg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_zs_format_error("+irPtr+" %s, i64 %d)", msg, str.Ref, dec))
		e.emitThrowCoded("TypeError", "ERR_INVALID_ARG_VALUE", msg)
		e.emitLabel(okL)
	}
	mode := 0
	if ex.Decompress {
		mode = 1
	}

	e.ensureExceptionHelpers()
	e.ensureMicrotasks()
	e.ensureNanBox()
	e.ensurePromiseAddReaction()
	e.ensureShapeRuntime()
	e.ensureUnhandledRejections()
	u8 := TypedArrayType("uint8")
	fulfill := e.emitStreamFulfillThunk(u8)
	rs := e.freshReg()
	e.emitRsAlloc(rs, "0.0", fulfill, u8)
	ws := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_ws_alloc(double 1.0)", ws))

	zctx := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_zs_init(i64 %d, i64 %s)", zctx, mode, wb))
	okReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne "+irPtr+" %s, null", okReg, zctx))
	okI := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = zext i1 %s to i64", okI, okReg))
	e.streamThrowTypeError(okI, "zlib initialization failed")
	// Patch the readable side into the zlib context.
	rGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 1", rGep, zctxStructIR, zctx))
	e.heapStore(irPtr, rs, rGep, 8)

	ctx := e.emitTransformCtxAlloc(Type{IR: irPtr, Host: hostTransformStream, HostTag: compressionTag(ex.Decompress)})
	storeCtxPtr := func(idx int, val string) {
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr "+irPtr+", "+irPtr+" %s, i64 %d", gep, ctx, idx))
		e.heapStore(irPtr, val, gep, 8)
	}
	storeCtxPtr(0, rs)
	storeCtxPtr(1, ws)
	storeCtxPtr(2, e.buildBuiltinClosure("@__kml_zs_transform", zctx))
	storeCtxPtr(3, e.buildBuiltinClosure("@__kml_zs_flush", zctx))
	for i := 4; i <= 6; i++ {
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i64, "+irPtr+" %s, i64 %d", gep, ctx, i))
		e.heapStore("i64", "0", gep, 8)
	}
	storeCtxPtr(7, "null")
	storeCtxPtr(8, "null")

	e.storeWStreamField(ws, 9, e.buildBuiltinClosure("@__kml_ts_sink_write", ctx))
	e.storeWStreamField(ws, 10, e.buildBuiltinClosure("@__kml_ts_sink_close", ctx))
	e.storeWStreamField(ws, 11, e.buildBuiltinClosure("@__kml_ts_sink_abort", ctx))
	e.storeStreamField(rs, 9, e.buildBuiltinClosure("@__kml_ts_pull", ctx))
	e.emitInstr(fmt.Sprintf("call void @__kml_microtask_enqueue("+irPtr+" %s)", e.buildBuiltinClosure("@__kml_rs_started", rs)))
	e.emitInstr(fmt.Sprintf("call void @__kml_microtask_enqueue("+irPtr+" %s)", e.buildBuiltinClosure("@__kml_ws_started", ws)))

	i8in, i8out := u8, u8
	return Value{Ref: ctx, Ty: Type{IR: irPtr, Host: hostTransformStream, StreamChunk: &i8in, StreamOut: &i8out, HostTag: compressionTag(ex.Decompress)}}, nil
}

// ptrTo is a pointer to a copy of t.
func ptrTo(t Type) *Type { return &t }

// emitStreamConvertWrap emits a transform `ptr (ptr %env, i64 %v0, i64
// %v1)` for an identity TransformStream whose chunk types differ: env is
// the readable side, each chunk of inTy is enqueued converted to outTy.
func (e *Emitter) emitStreamConvertWrap(inTy, outTy Type) string {
	restore := e.beginThunkEmit()
	chunk := e.streamChunkFromWords("%v0", "%v1", inTy)
	out := chunk
	if boxed, err := e.emitBoxValue(chunk); err == nil {
		out = e.convert(Value{Ref: boxed.Ref, Ty: TypeAny}, outTy)
	}
	w0, w1 := e.streamChunkWords(out)
	ok := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_rs_enqueue("+irPtr+" %%env, i64 %s, i64 %s)", ok, w0, w1))
	e.emitInstr("ret " + irPtr + " null")
	body := e.allocas.String() + e.body.String()
	restore()
	return e.defineContentNamed("@__kml_ts_convwrap.", irPtr, irPtr+" %env, i64 %v0, i64 %v1", body)
}
