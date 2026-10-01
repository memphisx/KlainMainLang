// emit_stmts_forof_any.go — `for (const x of v)` where v is a bare any/unknown
// value (ADR-01077): a D1 dynamic array or a static array boxed into `any`.
package llvm

import (
	"fmt"

	"KlainMainLang/ast"
)

// emitForOfAny iterates a bare any/unknown value (ADR-01077), binding the
// loop variable as `any`, through one cursor over two run-time shapes: an
// array (a D1 dynamic array or a boxed static array) steps by index through
// the dynamic member-get, each element read by its own element-kind rules;
// any other value is iterated by the protocol — its `[Symbol.iterator]()`
// (`[Symbol.asyncIterator]()` under `for await`, falling back to the sync
// one, whose values are then awaited), then `next()` until `done` (TDD-00230
// phase 5). A value with neither throws Node's "is not iterable" TypeError.
// Leaving the loop early (`break`, `return`, a throw) closes an iterator
// through its `return()`, as JS's IteratorClose does.
// Perf note: each array element read goes through a numeric-string key; a
// direct per-tag element walk is the documented follow-up.
func (e *Emitter) emitForOfAny(s *ast.ForOfStatement, condL, bodyL, incL, endL string) error {
	iterVal, err := e.emitExpr(s.Iterable)
	if err != nil {
		return err
	}
	return e.emitForOfAnyValue(s, iterVal, condL, bodyL, incL, endL)
}

// emitForOfAnyValue is emitForOfAny over an already-evaluated iterable.
func (e *Emitter) emitForOfAnyValue(s *ast.ForOfStatement, iterVal Value, condL, bodyL, incL, endL string) error {
	iterVal, err := e.emitBoxValue(iterVal)
	if err != nil {
		return err
	}
	// A string iterates its code points: as the array of them.
	iterVal = e.anyStringAsCodePoints(iterVal)
	// A boxed Map, Set, Headers or URLSearchParams: its entries.
	iterVal = Value{Ref: e.emitHostIterValue(iterVal.Ref), Ty: TypeAny}
	e.ensureSprintf()
	e.ensureMalloc()
	pos := s.GetPos()
	anyAt := func(slot string) Value {
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", r, slot))
		return Value{Ref: r, Ty: TypeAny}
	}
	newSlot := func(init string) string {
		r := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", r))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", init, r))
		return r
	}
	// The iterable is evaluated once; keep it in a slot for the loop.
	iterSlot := newSlot(iterVal.Ref)
	idxSlot := newSlot("0")
	// iterBoxSlot holds the protocol iterator (undefined in array mode);
	// syncSlot marks `for await` over a sync iterator (values awaited).
	iterBoxSlot := newSlot(fmt.Sprintf("%d", nbUndefined))
	syncSlot := newSlot("0")

	tag, _ := e.emitUnboxTagPayload(iterVal)
	isDyn := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isDyn, tag, kmlTagDynArray))
	isArr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isArr, tag, kmlTagArray))
	arrMode := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", arrMode, isDyn, isArr))
	arrModeSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i1, align 1", arrModeSlot))
	e.emitInstr(fmt.Sprintf("store i1 %s, ptr %s, align 1", arrMode, arrModeSlot))
	protoL := e.freshLabel("forof.any.proto")
	startL := e.freshLabel("forof.any.start")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", arrMode, startL, protoL))

	// Protocol mode: iterator = iterable[@@asyncIterator | @@iterator]().
	e.emitLabel(protoL)
	notIterable := forOfIterableName(s.Iterable) + " is not iterable"
	if s.Await {
		notIterable = forOfIterableName(s.Iterable) + " is not async iterable"
	}
	callMethod := func(recv Value, fnBox Value) (Value, error) {
		argv := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", argv))
		return e.emitDynFnBoxCallN(fnBox, recv, argv, "0", notIterable, pos)
	}
	methodOf := func(key string) (Value, string) {
		m, _ := e.emitAnyMemberOrUndefined(Value{Ref: iterVal.Ref, Ty: TypeAny}, key)
		mt, _ := e.emitUnboxTagPayload(m)
		isFn := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isFn, mt, kmlTagDynFunc))
		return m, isFn
	}
	var method Value
	if s.Await {
		am, amFn := methodOf("@@asyncIterator")
		asyncL := e.freshLabel("forof.any.async")
		syncL := e.freshLabel("forof.any.sync")
		pickL := e.freshLabel("forof.any.pick")
		mslot := newSlot(am.Ref)
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", amFn, asyncL, syncL))
		e.emitLabel(syncL)
		sm, _ := methodOf("@@iterator")
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", sm.Ref, mslot))
		e.emitInstr(fmt.Sprintf("store i64 1, ptr %s, align 8", syncSlot))
		e.emitTerminator(fmt.Sprintf("br label %%%s", pickL))
		e.emitLabel(asyncL)
		e.emitTerminator(fmt.Sprintf("br label %%%s", pickL))
		e.emitLabel(pickL)
		method = anyAt(mslot)
	} else {
		method, _ = methodOf("@@iterator")
	}
	it, err := callMethod(Value{Ref: iterVal.Ref, Ty: TypeAny}, method)
	if err != nil {
		return err
	}
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", it.Ref, iterBoxSlot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", startL))

	e.emitLabel(startL)
	isPattern := s.ArrayPattern != nil || s.ObjectPattern != nil
	varPtr := e.freshReg()
	if !isPattern {
		e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", varPtr))
		e.define(s.VarName, Symbol{Ptr: varPtr, Ty: TypeAny})
	}
	// Leaving the loop early closes a protocol iterator (IteratorClose).
	closeName := "__kml_forof_any_it_" + e.freshReg()[1:]
	e.define(closeName, Symbol{Ptr: iterBoxSlot, Ty: TypeAny})
	e.pendingFinallys = append(e.pendingFinallys, pendingExit{body: []ast.Statement{iteratorCloseStmt(closeName, pos)}})
	defer func() { e.pendingFinallys = e.pendingFinallys[:len(e.pendingFinallys)-1] }()
	elemSlot := newSlot(fmt.Sprintf("%d", nbUndefined))

	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	e.emitSafepoint()
	am := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i1, ptr %s, align 1", am, arrModeSlot))
	arrCondL := e.freshLabel("forof.any.arrcond")
	protoCondL := e.freshLabel("forof.any.protocond")
	haveL := e.freshLabel("forof.any.have")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", am, arrCondL, protoCondL))

	// Array mode: the length is reloaded each pass — a push inside the body
	// extends the walk, as it does in JS.
	e.emitLabel(arrCondL)
	i := anyAt(idxSlot).Ref
	curIter := anyAt(iterSlot)
	curLen, err := e.emitDynAnyMemberGetNamed(curIter, e.internString("length"), "length", pos)
	if err != nil {
		return err
	}
	_, curPay := e.emitUnboxTagPayload(curLen)
	curD := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = bitcast i64 %s to double", curD, curPay))
	curI := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = fptosi double %s to i64", curI, curD))
	more := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, %s", more, i, curI))
	arrReadL := e.freshLabel("forof.any.arrread")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", more, arrReadL, endL))
	e.emitLabel(arrReadL)
	key := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 24)", key))
	e.emitInstr(fmt.Sprintf("call i32 (ptr, ptr, ...) @sprintf(ptr %s, ptr %s, i64 %s)", key, e.internString("%lld"), i))
	elem, err := e.emitDynAnyMemberGet(anyAt(iterSlot), key, pos)
	if err != nil {
		return err
	}
	if s.Await {
		if elem, err = e.emitAwaitAny(elem); err != nil {
			return err
		}
	}
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", elem.Ref, elemSlot))
	ni := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", ni, anyAt(idxSlot).Ref))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", ni, idxSlot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", haveL))

	// Protocol mode: r = it.next() (awaited under `for await`).
	e.emitLabel(protoCondL)
	itv := anyAt(iterBoxSlot)
	next, err := e.emitDynAnyMemberGetNamed(itv, e.internString("next"), "next", pos)
	if err != nil {
		return err
	}
	r, err := callMethod(itv, next)
	if err != nil {
		return err
	}
	if s.Await {
		if r, err = e.emitAwaitAny(r); err != nil {
			return err
		}
	}
	rslot := newSlot(r.Ref)
	done, err := e.emitDynAnyMemberGetNamed(anyAt(rslot), e.internString("done"), "done", pos)
	if err != nil {
		return err
	}
	isDone := e.toBool(done).Ref
	// A finished iterator needs no close.
	finL := e.freshLabel("forof.any.fin")
	valL := e.freshLabel("forof.any.val")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isDone, finL, valL))
	e.emitLabel(finL)
	e.emitInstr(fmt.Sprintf("store i64 %d, ptr %s, align 8", nbUndefined, iterBoxSlot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", endL))
	e.emitLabel(valL)
	v, err := e.emitDynAnyMemberGetNamed(anyAt(rslot), e.internString("value"), "value", pos)
	if err != nil {
		return err
	}
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", v.Ref, elemSlot))
	if s.Await {
		// A sync iterator under `for await`: its values are awaited.
		sy := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", sy, syncSlot))
		isSync := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", isSync, sy))
		awL := e.freshLabel("forof.any.awaitval")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isSync, awL, haveL))
		e.emitLabel(awL)
		av, err := e.emitAwaitAny(anyAt(elemSlot))
		if err != nil {
			return err
		}
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", av.Ref, elemSlot))
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", haveL))

	e.emitLabel(haveL)
	e.emitTerminator(fmt.Sprintf("br label %%%s", bodyL))
	e.emitLabel(bodyL)
	elemV := anyAt(elemSlot)
	switch {
	case s.ObjectPattern != nil:
		if err := e.unpackObjectPatternInto(elemV.Ref, TypeAny, s.ObjectPattern, pos); err != nil {
			return err
		}
	case s.ArrayPattern != nil:
		if err := e.unpackDynArrayPatternInto(elemV, s.ArrayPattern, pos); err != nil {
			return err
		}
	default:
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", elemV.Ref, varPtr))
	}
	if err := e.emitForOfBody(s); err != nil {
		return err
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", incL))
	e.emitLabel(incL)
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(endL)
	return nil
}

// anyStringAsCodePoints is v, or, when v holds a string, the array of its
// code points (a string's iterator).
func (e *Emitter) anyStringAsCodePoints(v Value) Value {
	tag, _ := e.emitUnboxTagPayload(v)
	isStr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isStr, tag, kmlTagString))
	out := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", out))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", v.Ref, out))
	strL, doneL := e.freshLabel("forof.any.str"), e.freshLabel("forof.any.strdone")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isStr, strL, doneL))
	e.emitLabel(strL)
	s := e.emitUnboxBoxToType(v.Ref, TypePtr)
	arr, err := e.emitBoxValue(e.emitStringToCharArray(s))
	if err == nil {
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", arr.Ref, out))
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", r, out))
	return Value{Ref: r, Ty: TypeAny}
}

// emitAnyMemberOrUndefined reads key off a dynamic value, answering
// undefined — never throwing — where the value has no such member (a
// primitive, null/undefined, a value with no run-time shape).
func (e *Emitter) emitAnyMemberOrUndefined(v Value, key string) (Value, error) {
	tag, payload := e.emitUnboxTagPayload(v)
	res := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", res))
	e.emitInstr(fmt.Sprintf("store i64 %d, ptr %s, align 8", nbUndefined, res))
	objL, dynL, doneL := e.freshLabel("anymem.obj"), e.freshLabel("anymem.dyn"), e.freshLabel("anymem.done")
	isObj := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isObj, tag, kmlTagObject))
	notObjL := e.freshLabel("anymem.notobj")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isObj, objL, notObjL))
	e.emitLabel(objL)
	objp := e.emitIntToPtr(payload)
	sv, _ := e.emitShapeGet(objp, e.internString(key))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", sv, res))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(notObjL)
	isBag := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isBag, tag, kmlTagDynObject))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isBag, dynL, doneL))
	e.emitLabel(dynL)
	e.ensureDynObj()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynobj_get(ptr %s, ptr %s)", r, e.emitIntToPtr(payload), e.internString(key)))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", r, res))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	out := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", out, res))
	return Value{Ref: out, Ty: TypeAny}, nil
}

// iteratorCloseStmt is `if (typeof it.return === "function") it.return()`
// over the iterator bound to name — JS's IteratorClose, run when a loop over
// it is left early. name holds undefined once the iterator is done.
func iteratorCloseStmt(name string, pos ast.Pos) ast.Statement {
	it := ast.NewIdentifier(name, pos)
	isObj := ast.NewBinaryExpression("!==", ast.NewUnaryExpression("typeof", true, it, pos), ast.NewStringLiteral("undefined", pos), pos)
	hasRet := ast.NewBinaryExpression("===", ast.NewUnaryExpression("typeof", true, ast.NewMemberExpression(it, "return", pos), pos), ast.NewStringLiteral("function", pos), pos)
	call := ast.NewExpressionStatement(ast.NewCallExpression(ast.NewMemberExpression(it, "return", pos), nil, pos), pos)
	return ast.NewIfStatement(ast.NewBinaryExpression("&&", isObj, hasRet, pos), ast.NewBlockStatement([]ast.Statement{call}, pos), nil, pos)
}

// forOfIterableName renders the iterable expression for the "is not
// iterable" message the way Node does for the common shapes (`x`, `o.p`,
// `o[k]`); anything else is Node's generic "object".
func forOfIterableName(expr ast.Expression) string {
	switch x := expr.(type) {
	case *ast.Identifier:
		return x.Name
	case *ast.MemberExpression:
		return forOfIterableName(x.Object) + "." + x.Property
	case *ast.IndexExpression:
		return forOfIterableName(x.Object) + "[...]"
	}
	return "object"
}
