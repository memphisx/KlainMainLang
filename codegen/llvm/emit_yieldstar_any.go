package llvm

import (
	"fmt"

	"KlainMainLang/ast"
)

// emitYieldStarAny is `yield* operand` over any iterable the static arms do
// not cover (a plain object, a value held in `any`), by the spec's
// algorithm on boxed values: GetIterator (in an async generator
// @@asyncIterator, else @@iterator — a sync iterator inside an async
// generator has its values awaited), then each resumption of the outer
// generator forwarded to the iterator's next, throw or return, until a
// result is done. A done next/throw result is the expression's value; a
// done return result returns from the outer generator. An iterator without
// throw is closed and a TypeError thrown; one without return lets the
// return through.
func (e *Emitter) emitYieldStarAny(gctx *generatorEmitCtx, ex *ast.YieldExpression) (Value, error) {
	pos := ex.GetPos()
	async := gctx.genTy.GeneratorIsAsync
	e.asyncGenStepCtr++
	n := e.asyncGenStepCtr
	slot := func(name string, init string) (string, string) {
		full := fmt.Sprintf("__kml_ys%d_%s", n, name)
		p := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", p))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", init, p))
		e.define(full, Symbol{Ptr: p, Ty: TypeAny})
		return full, p
	}
	id := func(name string) ast.Expression { return ast.NewIdentifier(name, pos) }
	store := func(p string, v Value) error {
		b, err := e.emitBoxValue(v)
		if err != nil {
			return err
		}
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", b.Ref, p))
		return nil
	}
	eval := func(x ast.Expression) (Value, error) {
		v, err := e.emitExpr(x)
		if err != nil {
			return Value{}, err
		}
		return e.emitBoxValue(v)
	}
	load := func(p string) Value {
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", r, p))
		return Value{Ref: r, Ty: TypeAny}
	}
	isNullish := func(v Value) string {
		tag, _ := e.emitUnboxTagPayload(v)
		a, b, r := e.freshReg(), e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", a, tag, kmlTagNull))
		e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", b, tag, kmlTagUndefined))
		e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", r, a, b))
		return r
	}
	await := func(x ast.Expression) (Value, error) {
		v, err := e.emitAwait(ast.NewAwaitExpression(x, pos))
		if err != nil {
			return Value{}, err
		}
		return e.emitBoxValue(v)
	}

	src, err := eval(ex.Argument)
	if err != nil {
		return Value{}, err
	}
	srcName, _ := slot("src", src.Ref)
	itName, itP := slot("it", fmt.Sprint(nbUndefined))
	syncName, syncP := slot("sync", "0")
	_ = syncName
	symbol := func(name string) ast.Expression {
		return ast.NewIndexExpression(id(srcName), ast.NewMemberExpression(id("Symbol"), name, pos), pos)
	}

	// GetIterator.
	if async {
		am, err := eval(symbol("asyncIterator"))
		if err != nil {
			return Value{}, err
		}
		amName, amP := slot("am", am.Ref)
		_ = amName
		syncL, asyncL, gotL := e.freshLabel("ystar.any.sync"), e.freshLabel("ystar.any.async"), e.freshLabel("ystar.any.got")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNullish(am), syncL, asyncL))
		e.emitLabel(syncL)
		sm, err := eval(symbol("iterator"))
		if err != nil {
			return Value{}, err
		}
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", sm.Ref, amP))
		e.emitInstr(fmt.Sprintf("store i64 1, ptr %s, align 8", syncP))
		e.emitTerminator(fmt.Sprintf("br label %%%s", gotL))
		e.emitLabel(asyncL)
		e.emitTerminator(fmt.Sprintf("br label %%%s", gotL))
		e.emitLabel(gotL)
		it, err := eval(ast.NewCallExpression(ast.NewMemberExpression(id(amName), "call", pos), []ast.Expression{id(srcName)}, pos))
		if err != nil {
			return Value{}, err
		}
		if err := store(itP, it); err != nil {
			return Value{}, err
		}
	} else {
		it, err := eval(ast.NewCallExpression(ast.NewMemberExpression(ast.NewIndexExpression(id(srcName), ast.NewMemberExpression(id("Symbol"), "iterator", pos), pos), "call", pos), []ast.Expression{id(srcName)}, pos))
		if err != nil {
			return Value{}, err
		}
		if err := store(itP, it); err != nil {
			return Value{}, err
		}
	}
	notObjMsg := "Result of the Symbol.iterator method is not an object"
	if async {
		notObjMsg = "Result of the Symbol.asyncIterator method is not an object"
	}
	e.emitAnyObjectCheck(load(itP), notObjMsg)
	next, err := eval(ast.NewMemberExpression(id(itName), "next", pos))
	if err != nil {
		return Value{}, err
	}
	nextName, _ := slot("next", next.Ref)
	recvName, recvP := slot("recv", fmt.Sprint(nbUndefined))
	modeP := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", modeP))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", modeP))
	kindP := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", kindP))
	resName, resP := slot("res", fmt.Sprint(nbUndefined))
	outP := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", outP))

	loopL := e.freshLabel("ystar.any.loop")
	nextL := e.freshLabel("ystar.any.next")
	throwL := e.freshLabel("ystar.any.throw")
	retL := e.freshLabel("ystar.any.ret")
	gotL := e.freshLabel("ystar.any.res")
	endL := e.freshLabel("ystar.any.end")
	e.emitTerminator(fmt.Sprintf("br label %%%s", loopL))

	e.emitLabel(loopL)
	mode := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", mode, modeP))
	e.emitTerminator(fmt.Sprintf("switch i64 %s, label %%%s [ i64 1, label %%%s i64 2, label %%%s ]", mode, nextL, throwL, retL))

	// returnFrom completes the outer generator with v (its finallys first).
	returnFrom := func(v Value) error {
		if async {
			name, _ := slot(fmt.Sprintf("rv%d", e.asyncGenStepCtr), v.Ref)
			e.asyncGenStepCtr++
			aw, err := await(id(name))
			if err != nil {
				return err
			}
			v = aw
		}
		rv := e.convert(v, gctx.elemTy)
		e.storeGeneratorField(gctx.genObjReg, gctx.genTy, GeneratorHasRetField, "i1", "1")
		if err := e.emitPendingFinallys(); err != nil {
			return err
		}
		e.emitGeneratorSwapToCaller(gctx, rv, true)
		e.emitTerminator("ret void")
		return nil
	}

	// next: r = next.call(it, recv).
	e.emitLabel(nextL)
	r, err := eval(ast.NewCallExpression(ast.NewMemberExpression(id(nextName), "call", pos), []ast.Expression{id(itName), id(recvName)}, pos))
	if err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", r.Ref, resP))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", kindP))
	e.emitTerminator(fmt.Sprintf("br label %%%s", gotL))

	// throw: it.throw ? r = it.throw(recv) : close it, TypeError.
	e.emitLabel(throwL)
	tm, err := eval(ast.NewMemberExpression(id(itName), "throw", pos))
	if err != nil {
		return Value{}, err
	}
	tmName, _ := slot("throwm", tm.Ref)
	haveT, noT := e.freshLabel("ystar.any.hasthrow"), e.freshLabel("ystar.any.nothrow")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNullish(tm), noT, haveT))
	e.emitLabel(noT)
	rm0, err := eval(ast.NewMemberExpression(id(itName), "return", pos))
	if err != nil {
		return Value{}, err
	}
	rm0Name, _ := slot("closem", rm0.Ref)
	closeL, typeErrL := e.freshLabel("ystar.any.close"), e.freshLabel("ystar.any.typeerr")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNullish(rm0), typeErrL, closeL))
	e.emitLabel(closeL)
	cr, err := eval(ast.NewCallExpression(ast.NewMemberExpression(id(rm0Name), "call", pos), []ast.Expression{id(itName)}, pos))
	if err != nil {
		return Value{}, err
	}
	if async {
		crName, _ := slot("closer", cr.Ref)
		if cr, err = await(id(crName)); err != nil {
			return Value{}, err
		}
	}
	e.emitAnyObjectCheck(cr, "Iterator result is not an object")
	e.emitTerminator(fmt.Sprintf("br label %%%s", typeErrL))
	e.emitLabel(typeErrL)
	e.emitThrowTypeError("The iterator does not provide a 'throw' method.")
	e.emitTerminator("unreachable")
	e.emitLabel(haveT)
	r, err = eval(ast.NewCallExpression(ast.NewMemberExpression(id(tmName), "call", pos), []ast.Expression{id(itName), id(recvName)}, pos))
	if err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", r.Ref, resP))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", kindP))
	e.emitTerminator(fmt.Sprintf("br label %%%s", gotL))

	// return: it.return ? r = it.return(recv) : return recv from the outer.
	e.emitLabel(retL)
	rm, err := eval(ast.NewMemberExpression(id(itName), "return", pos))
	if err != nil {
		return Value{}, err
	}
	rmName, _ := slot("retm", rm.Ref)
	haveR, noR := e.freshLabel("ystar.any.hasret"), e.freshLabel("ystar.any.noret")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNullish(rm), noR, haveR))
	e.emitLabel(noR)
	if err := returnFrom(load(recvP)); err != nil {
		return Value{}, err
	}
	e.emitLabel(haveR)
	r, err = eval(ast.NewCallExpression(ast.NewMemberExpression(id(rmName), "call", pos), []ast.Expression{id(itName), id(recvName)}, pos))
	if err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", r.Ref, resP))
	e.emitInstr(fmt.Sprintf("store i64 1, ptr %s, align 8", kindP))
	e.emitTerminator(fmt.Sprintf("br label %%%s", gotL))

	// A result: awaited (an async iterator's promise), an object, done or not.
	e.emitLabel(gotL)
	if async {
		syncNow := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", syncNow, syncP))
		isSync := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", isSync, syncNow))
		awL, chkL := e.freshLabel("ystar.any.await"), e.freshLabel("ystar.any.check")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isSync, chkL, awL))
		e.emitLabel(awL)
		aw, err := await(id(resName))
		if err != nil {
			return Value{}, err
		}
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", aw.Ref, resP))
		e.emitTerminator(fmt.Sprintf("br label %%%s", chkL))
		e.emitLabel(chkL)
	}
	e.emitAnyObjectCheck(load(resP), "Iterator result is not an object")
	done, err := e.emitExpr(ast.NewUnaryExpression("!", true, ast.NewUnaryExpression("!", true, ast.NewMemberExpression(id(resName), "done", pos), pos), pos))
	if err != nil {
		return Value{}, err
	}
	doneB := e.convert(done, TypeBool)
	val, err := eval(ast.NewMemberExpression(id(resName), "value", pos))
	if err != nil {
		return Value{}, err
	}
	valName, valP := slot("val", val.Ref)
	if async {
		// A sync iterator's value is awaited (CreateAsyncFromSyncIterator).
		syncNow := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", syncNow, syncP))
		isSync := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", isSync, syncNow))
		awL, keepL := e.freshLabel("ystar.any.awv"), e.freshLabel("ystar.any.keepv")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isSync, awL, keepL))
		e.emitLabel(awL)
		aw, err := await(id(valName))
		if err != nil {
			return Value{}, err
		}
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", aw.Ref, valP))
		e.emitTerminator(fmt.Sprintf("br label %%%s", keepL))
		e.emitLabel(keepL)
	}
	doneL, yieldL := e.freshLabel("ystar.any.done"), e.freshLabel("ystar.any.yield")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", doneB.Ref, doneL, yieldL))

	e.emitLabel(doneL)
	kind := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", kind, kindP))
	isRet := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 1", isRet, kind))
	fromRetL, valueL := e.freshLabel("ystar.any.fromret"), e.freshLabel("ystar.any.value")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isRet, fromRetL, valueL))
	e.emitLabel(fromRetL)
	if err := returnFrom(load(valP)); err != nil {
		return Value{}, err
	}
	e.emitLabel(valueL)
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", load(valP).Ref, outP))
	e.emitTerminator(fmt.Sprintf("br label %%%s", endL))

	// Not done: yield the value, then record how the outer was resumed.
	e.emitLabel(yieldL)
	e.emitGeneratorSwapToCaller(gctx, e.convert(load(valP), gctx.elemTy), false)
	rmode := e.loadGeneratorField(gctx.genObjReg, gctx.genTy, GeneratorResumeModeField)
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", rmode.Ref, modeP))
	isThrow := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 1", isThrow, rmode.Ref))
	sent := e.loadGeneratorField(gctx.genObjReg, gctx.genTy, GeneratorSentField)
	thrown := e.loadGeneratorField(gctx.genObjReg, gctx.genTy, GeneratorThrownField)
	// A next/return resumption leaves no thrown cell: read it only on throw.
	thrownCell := e.freshReg()
	e.ensureZeroCell()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr @__kml_zero_cell", thrownCell, isThrow, thrown.Ref))
	tbox := e.thrownCellAny(thrownCell)
	recv := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", recv, isThrow, tbox.Ref, sent.Ref))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", recv, recvP))
	e.emitTerminator(fmt.Sprintf("br label %%%s", loopL))

	e.emitLabel(endL)
	return load(outP), nil
}

// emitAnyObjectCheck throws a TypeError with msg unless v is an object (not
// a number, string, boolean, null or undefined).
func (e *Emitter) emitAnyObjectCheck(v Value, msg string) {
	tag, _ := e.emitUnboxTagPayload(v)
	prim := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ule i8 %s, %d", prim, tag, kmlTagUndefined))
	badL, okL := e.freshLabel("anyobj.bad"), e.freshLabel("anyobj.ok")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", prim, badL, okL))
	e.emitLabel(badL)
	e.emitThrowTypeError(msg)
	e.emitTerminator("unreachable")
	e.emitLabel(okL)
}

// yieldStarIsDynamic reports a `yield*` operand of type t that
// emitYieldStar hands to emitYieldStarAny: not a generator, not a builtin
// collection a sync generator walks, not a class with an async iterator.
func (e *Emitter) yieldStarIsDynamic(t Type, async bool) bool {
	if t.IsGenerator() {
		return false
	}
	if !async && !t.Nullable() && (t.IsArray() || t.IsSet() || t.IsMap() || isForOfStringTy(t)) {
		return false
	}
	if async && t.IsClass() {
		if info, ok := e.classes[t.ClassName]; ok {
			if _, ok := info.MethodSigs[asyncIteratorMethodName]; ok {
				return false
			}
		}
	}
	return true
}
