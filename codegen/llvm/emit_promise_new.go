// emit_promise_new.go — `new Promise((resolve, reject) => …)`, the executor
// constructor (TDD-00087). The promise is a task-shaped promise; resolve/reject
// are per-site closures capturing it that settle it (and wake any awaiter + fire
// reactions) via @__kml_promise_settle when the executor — or a later callback it
// stashed them into — calls them.
package llvm

import (
	"KlainMainLang/ast"
	_ "embed"
	"fmt"
)

// The settle, reject and adoption routines live in promisesrc/settle.c
// (TDD-00240).
//
//go:embed promisesrc/settle.c
var promiseSettleSource string

// PromiseSettleSource is settle.c, behind kml_layout.h.
func PromiseSettleSource() string { return layoutHeader() + promiseSettleSource }

// emitRejectPromise rejects promise q with the caught value (tag, an i8 or
// i64 register or literal; pay, i64): the one rejection writer.
func (e *Emitter) emitRejectPromise(q, tag string, tagIsI8 bool, pay string) {
	e.ensureMicrotasks()
	e.ensureNanBox()
	e.ensureExceptionHelpers()
	e.ensureUnhandledRejections()
	if tagIsI8 {
		w := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = zext i8 %s to i64", w, tag))
		tag = w
	}
	e.emitInstr(fmt.Sprintf("call void @__kml_promise_reject_with("+irPtr+" %s, i64 %s, i64 %s)", q, tag, pay))
}

// emitNewPromise implements `new Promise<T>((resolve, reject) => …)`. It allocates
// a pending task promise, builds resolve/reject as per-site closures over it, and
// runs the executor immediately with them. Resolution may be synchronous (the
// executor calls resolve/reject before returning) or deferred (it stashes one into
// a later callback, e.g. setTimeout) — either way the promise is a real task
// promise awaitable / chainable like any other.
func (e *Emitter) emitNewPromise(ex *ast.NewExpression) (Value, error) {
	if len(ex.Args) == 0 && e.compatJS() {
		// No executor: the constructor's TypeError (the checker reports
		// TS2554 in the strict lane).
		e.emitThrowTypeError("Promise resolver undefined is not a function")
		e.emitLabel(e.freshLabel("promise.noexec.dead"))
		return Value{Ref: fmt.Sprint(nbUndefined), Ty: TypeAny}, nil
	}
	if len(ex.Args) > 1 {
		// Arguments past the executor are evaluated, and ignored, before
		// the executor runs. A function literal's evaluation is only its
		// closure's creation, so it is emitted where the executor needs it;
		// any other executor is evaluated first, into a binding.
		ex2 := *ex
		ex2.Args = ex.Args[:1]
		switch ex.Args[0].(type) {
		case *ast.ArrowFunction, *ast.FunctionExpression:
		default:
			v, err := e.emitExpr(ex.Args[0])
			if err != nil {
				return Value{}, err
			}
			ex2.Args = []ast.Expression{e.bindValue(v, ex.GetPos())}
		}
		for _, x := range ex.Args[1:] {
			if _, err := e.emitExpr(x); err != nil {
				return Value{}, err
			}
		}
		return e.emitNewPromise(&ex2)
	}
	if len(ex.Args) != 1 {
		return Value{}, internalAt(ex.GetPos(), "new Promise without an executor")
	}

	valTy := e.newPromiseValueType(ex)
	if e.promiseValue == nil {
		e.promiseValue = map[*ast.NewExpression]Type{}
	}
	e.promiseValue[ex] = valTy
	e.ensureMicrotasks()

	e.ensureNanBox()
	e.ensureExceptionHelpers()
	e.ensureUnhandledRejections()

	p := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_task_alloc_promise()", p))
	return e.emitPromiseExecutor(p, valTy, ex.Args[0], ex.GetPos())
}

// emitPromiseExecutor runs a Promise executor (exec) with resolve/reject
// functions settling the pending promise body p, whose value type is valTy:
// `new Promise(exec)`'s, or the body of an instance of a class extending
// Promise (TDD-00247).
func (e *Emitter) emitPromiseExecutor(p string, valTy Type, exec ast.Expression, pos ast.Pos) (Value, error) {
	e.ensureMicrotasks()
	e.ensureNanBox()
	e.ensureExceptionHelpers()
	e.ensureUnhandledRejections()

	// Per-site resolve/reject settle functions + their closure headers over p.
	resolveFn := e.emitResolveThunk(valTy)
	rejectFn := e.emitRejectThunk()

	resolveClo := e.buildBuiltinClosure(resolveFn, p)
	rejectClo := e.buildBuiltinClosure(rejectFn, p)

	// Emit the executor, hinting its two params so `resolve(x)`/`reject(e)` inside
	// it get the right closure signatures even though the arrow leaves them
	// unannotated.
	// A `Promise<void>` executor's `resolve` takes no argument.
	var resolveParams []Type
	if valTy.IR != "void" && valTy.IR != "" {
		resolveParams = []Type{valTy}
	}
	resolveTy := FuncType(resolveParams, TypeVoid)
	// Mark resolve so a call site passing a Promise (`resolve(anotherPromise)`)
	// adopts the thenable instead of coercing a promise to the value type
	// (TDD-00091). A `Promise<void>` resolve takes no argument, so it can't be
	// handed a thenable — leave it unmarked.
	if len(resolveParams) == 1 {
		resolveTy.setKind(kindPromiseResolver, true)
	}
	// reject(reason) carries the real value as a caught-value record ({i8,i64}),
	// not an Error pointer — so `rej(42)`/`rej("x")`/`rej(new Error())` all round-
	// trip to the .catch handler (TDD-00207).
	rejectTy := FuncType([]Type{TypeCaught}, TypeVoid)
	// The executor may be an arrow or function-expression literal (its two params
	// are hinted so `resolve(x)`/`reject(e)` get the right closure signatures even
	// unannotated), or any closure-typed expression already in scope — a variable
	// holding an executor, or a bare top-level-function reference (which emitExpr
	// resolves to a closure value). All closures share the `{fnptr, env}` ABI, so
	// the call below works regardless.
	var execVal Value
	var err error
	// Hints stop at a rest parameter: it collects the arguments past it.
	hints := func(ps []ast.Param) []Type {
		h := []Type{resolveTy, rejectTy}
		for i, p := range ps {
			if p.Rest && i < len(h) {
				return h[:i]
			}
		}
		return h
	}
	restExec := false
	switch fn := exec.(type) {
	case *ast.ArrowFunction:
		restExec = len(hints(fn.Params)) < 2
	case *ast.FunctionExpression:
		restExec = len(hints(fn.Params)) < 2
	}
	switch fn := exec.(type) {
	case *ast.ArrowFunction:
		if restExec {
			execVal, err = e.emitExpr(exec)
			break
		}
		execVal, err = e.emitArrowFunctionWithHints(fn, hints(fn.Params))
	case *ast.FunctionExpression:
		if restExec {
			execVal, err = e.emitExpr(exec)
			break
		}
		execVal, err = e.emitFunctionExpression(fn, hints(fn.Params))
	default:
		execVal, err = e.emitExpr(exec)
		if err == nil && !execVal.Ty.IsFunc() {
			// Not a function (JavaScript; or a box holding one): the
			// constructor's TypeError naming it, unless it is one.
			execVal, err = e.promiseExecutorFromValue(execVal, FuncType([]Type{resolveTy, rejectTy}, TypeVoid))
		}
	}
	if err != nil {
		return Value{}, err
	}
	if execVal.Ty.IsFunc() && (execVal.Ty.FuncHasRest || len(execVal.Ty.FuncParams) > 2) {
		// The runtime passes the two resolving functions alone: an
		// executor with a rest parameter (its `arguments`, too) is called
		// through an adapter, as any call of it packs the rest.
		f := e.bindValue(execVal, pos)
		res, rej := "__kml_exec_res", "__kml_exec_rej"
		call := ast.NewCallExpression(f, []ast.Expression{ast.NewIdentifier(res, pos), ast.NewIdentifier(rej, pos)}, pos)
		ad := ast.NewArrowFunction([]ast.Param{{Name: res}, {Name: rej}}, nil, call, nil, pos)
		if execVal, err = e.emitArrowFunctionWithHints(ad, []Type{resolveTy, rejectTy}); err != nil {
			return Value{}, err
		}
	}

	// An executor held as a value takes its resolving functions at the
	// types it declares (a class extending Promise's `(resolve: (value: T)
	// => void, reject: (reason?: any) => void)`): ours, adapted.
	// (A parameter held in `any` takes the box, passed in the same
	// register a pointer is.)
	adapt := func(ref string, from, to Type) string {
		v := e.convert(Value{Ref: ref, Ty: from}, to)
		if v.Ty.IR == "i64" {
			p := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to "+irPtr, p, v.Ref))
			return p
		}
		return v.Ref
	}
	if ps := execVal.Ty.FuncParams; execVal.Ty.IsFunc() && len(ps) >= 2 {
		resolveClo = adapt(resolveClo, resolveTy, ps[0])
		rejectClo = adapt(rejectClo, rejectTy, ps[1])
	} else if len(ps) == 1 && execVal.Ty.IsFunc() {
		resolveClo = adapt(resolveClo, resolveTy, ps[0])
	}
	// A throw from the executor rejects the promise (settle.c).
	e.declareFn("__kml_promise_run_executor", "declare void @__kml_promise_run_executor("+irPtr+", "+irPtr+", "+irPtr+", "+irPtr+")")
	e.emitInstr(fmt.Sprintf("call void @__kml_promise_run_executor("+irPtr+" %s, "+irPtr+" %s, "+irPtr+" %s, "+irPtr+" %s)", execVal.Ref, resolveClo, rejectClo, p))

	pt := PromiseOf(valTy)
	pt.PromiseTask = true
	return Value{Ref: p, Ty: pt}, nil
}

// emitPromiseAdopt implements `resolve(srcPromise)` thenable adoption (TDD-00091):
// register an adopt reaction on the source promise so the target (the resolve
// closure's env) mirrors the source's settlement. If the source is already
// settled, the reaction is enqueued now; otherwise it is attached to the source's
// reaction list and fires when the source settles — the same reaction/microtask
// mechanism `.then` uses, so ordering matches JS (adoption is a microtask tick).
func (e *Emitter) emitPromiseAdopt(tgtRef, srcRef string) {
	e.ensureMicrotasks()
	e.ensureNanBox()
	e.ensureExceptionHelpers()
	e.ensureUnhandledRejections()
	e.ensureMicrotasks()

	// env = { src, tgt }
	env := e.freshReg()
	envSrc := e.freshReg()
	envTgt := e.freshReg()
	e.heapMallocInto(env, allocClosure, "16")
	e.emitInstr(fmt.Sprintf("%s = getelementptr { "+irPtr+", "+irPtr+" }, "+irPtr+" %s, i32 0, i32 0", envSrc, env))
	e.heapStore(irPtr, srcRef, envSrc, 8)
	e.emitInstr(fmt.Sprintf("%s = getelementptr { "+irPtr+", "+irPtr+" }, "+irPtr+" %s, i32 0, i32 1", envTgt, env))
	e.heapStore(irPtr, tgtRef, envTgt, 8)

	// closure = { adopt_runner, env }
	clo := e.freshReg()
	cfp := e.freshReg()
	cep := e.freshReg()
	e.emitClosureAlloc(clo, "16", "@__kml_promise_adopt_runner")
	e.emitInstr(fmt.Sprintf("%s = getelementptr { "+irPtr+", "+irPtr+" }, "+irPtr+" %s, i32 0, i32 0", cfp, clo))
	e.heapStore(irPtr, "@__kml_promise_adopt_runner", cfp, 8)
	e.emitInstr(fmt.Sprintf("%s = getelementptr { "+irPtr+", "+irPtr+" }, "+irPtr+" %s, i32 0, i32 1", cep, clo))
	e.heapStore(irPtr, env, cep, 8)

	// If src already settled → enqueue now; else attach a reaction node onto
	// src.reactions (the same pattern emitPromiseThen uses).
	e.emitMarkPromiseHandled(srcRef)
	res := e.freshReg()
	resP := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 0", resP, promiseStructIR, srcRef))
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", res, resP))
	settled := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", settled, res))
	nowL := e.freshLabel("adopt.now")
	laterL := e.freshLabel("adopt.later")
	doneL := e.freshLabel("adopt.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", settled, nowL, laterL))
	e.emitLabel(nowL)
	e.emitInstr(fmt.Sprintf("call void @__kml_microtask_enqueue("+irPtr+" %s)", clo))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(laterL)
	node := e.freshReg()
	rxP := e.freshReg()
	oldHead := e.freshReg()
	nodeClo := e.freshReg()
	nodeNext := e.freshReg()
	e.heapMallocInto(node, allocHost, "16")
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 4", rxP, promiseStructIR, srcRef))
	e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", oldHead, rxP))
	e.emitInstr(fmt.Sprintf("%s = getelementptr { "+irPtr+", "+irPtr+" }, "+irPtr+" %s, i32 0, i32 0", nodeClo, node))
	e.heapStore(irPtr, clo, nodeClo, 8)
	e.emitInstr(fmt.Sprintf("%s = getelementptr { "+irPtr+", "+irPtr+" }, "+irPtr+" %s, i32 0, i32 1", nodeNext, node))
	e.heapStore(irPtr, oldHead, nodeNext, 8)
	e.heapStore(irPtr, node, rxP, 8)
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
}

// buildBuiltinClosure builds a `{fnptr, env}` closure header whose env is a raw
// pointer (here the promise), passed as the callee's first argument by the closure
// ABI — no per-variable env cell needed (the settle thunks take the promise
// directly).
func (e *Emitter) buildBuiltinClosure(fn, envPtr string) string {
	hdr := e.freshReg()
	fpp := e.freshReg()
	epp := e.freshReg()
	e.emitClosureAlloc(hdr, "16", fn)
	e.emitInstr(fmt.Sprintf("%s = getelementptr { "+irPtr+", "+irPtr+" }, "+irPtr+" %s, i32 0, i32 0", fpp, hdr))
	e.heapStore(irPtr, fn, fpp, 8)
	e.emitInstr(fmt.Sprintf("%s = getelementptr { "+irPtr+", "+irPtr+" }, "+irPtr+" %s, i32 0, i32 1", epp, hdr))
	e.heapStore(irPtr, envPtr, epp, 8)
	return hdr
}

// emitResolveThunk emits `void (ptr %p, <T> %v)` and returns its symbol:
// store v into the promise's value slot(s) and settle it fulfilled. The
// promise arrives as the closure env (first arg), the resolved value as the
// second.
func (e *Emitter) emitResolveThunk(valTy Type) string {
	// A `Promise<void>` resolves with no value — `resolve()` takes no argument, so
	// the thunk has no `%v` parameter (a `void %v` param is invalid IR).
	isVoid := valTy.IR == "void" || valTy.IR == ""
	restore := e.beginThunkEmit()
	defer restore()
	// First settle wins: bail out before storing the value if already settled, so
	// a later resolve/reject can't overwrite the winning value (the state guard in
	// __kml_promise_settle alone isn't enough — the value store happens here).
	e.emitThunkAlreadySettledGuard()
	if !isVoid {
		v := Value{Ref: "%v", Ty: valTy}
		if valTy.IsArray() {
			// A resolved array value arrives via the closure array-argument ABI
			// (`ptr %vptr` — the {data, len} header — plus `i64 %vlen`), the
			// same shape `emitClosureCall` marshals a `resolve(arr)` call into.
			// The header is the array value storePromiseValue expects.
			v = e.arrayValueFromHeaderReg("%vptr", valTy)
		}
		if isUnconstrainedDynamic(valTy) {
			// resolve(x) with a promise adopts it.
			e.ensurePromiseResolveAny()
			e.emitInstr(fmt.Sprintf("call void @__kml_promise_resolve_any("+irPtr+" %%p, i64 %s)", v.Ref))
			e.emitTerminator("ret void")
		} else {
			e.storePromiseValue("%p", v)
		}
	}
	if !e.blockDone {
		e.emitInstr("call void @__kml_promise_settle(" + irPtr + " %p, i64 1)")
		e.emitInstr("ret void")
	}
	params := irPtr + " %p"
	if !isVoid {
		if valTy.IsArray() {
			params = irPtr + " %p, " + irPtr + " %vptr, i64 %vlen"
		} else {
			params = fmt.Sprintf(irPtr+" %%p, %s %%v", StructFieldIR(valTy))
		}
	}
	return e.defineContentNamed("@__kml_promise_resolve.", "void", params, e.allocas.String()+e.body.String())
}

// emitRejectThunk emits `void (ptr %p, ptr %err)` and returns its symbol:
// store the error pointer bits into the promise's value slot and settle it
// rejected.
func (e *Emitter) emitRejectThunk() string {
	restore := e.beginThunkEmit()
	defer restore()
	e.emitThunkAlreadySettledGuard()
	// %err is the caught-value record: { i8 tag, i64 payload }. Unpack and store
	// into the rejection slots v0 (payload) + v1 (tag) (TDD-00207).
	tag := e.freshReg()
	pay := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue { i8, i64 } %%err, 0", tag))
	e.emitInstr(fmt.Sprintf("%s = extractvalue { i8, i64 } %%err, 1", pay))
	e.emitRejectPromise("%p", tag, true, pay)
	e.emitInstr("ret void")
	return e.defineContentNamed("@__kml_promise_reject.", "void", irPtr+" %p, { i8, i64 } %err", e.allocas.String()+e.body.String())
}

// emitThunkAlreadySettledGuard emits an early `ret void` when %p is already
// settled (state != 0), so only the first resolve/reject writes the value.
func (e *Emitter) emitThunkAlreadySettledGuard() {
	sp := e.freshReg()
	cur := e.freshReg()
	settled := e.freshReg()
	storeL := e.freshLabel("thunk.store")
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %%p, i32 0, i32 0", sp, promiseStructIR))
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", cur, sp))
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", settled, cur))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%thunk.ret, label %%%s", settled, storeL))
	e.emitLabel("thunk.ret")
	e.emitInstr("ret void")
	e.emitLabel(storeL)
}

// beginThunkEmit swaps in fresh alloca/body builders and SSA counters for emitting
// a small standalone helper function, returning a restore closure. The settle
// thunks have no scopes/generator/async state to preserve, so this is the minimal
// subset of emitClosureFunc's own save/restore dance.
func (e *Emitter) beginThunkEmit() func() {
	saved := e.funcBuilder
	e.funcBuilder = &funcBuilder{}
	return func() { e.funcBuilder = saved }
}

// newPromiseValueType is T of `new Promise<T>(executor)`: the type argument,
// else the checker's (a contextual `Promise<number>`), else `any` — the
// value an untyped promise resolves with.
func (e *Emitter) newPromiseValueType(ex *ast.NewExpression) Type {
	if len(ex.TypeArgs) == 1 {
		return e.annotationType(ex.TypeArgs[0])
	}
	if c := e.front(); c != nil {
		t := c.TypeOf(ex)
		if !c.Unanswered(t) && len(t.TypeArgs) == 1 {
			if r, ok := lowerRepr(t.TypeArgs[0]); ok && !r.IsArray() && !r.IsObject() {
				return r
			}
		}
	}
	return TypeAny
}

// promiseExecutorFromValue is new Promise's executor held as a non-closure
// value v: the closure a box holds, or the constructor's TypeError
// (`Promise resolver 5 is not a function`).
func (e *Emitter) promiseExecutorFromValue(v Value, fnTy Type) (Value, error) {
	box := v
	if !v.Ty.IsDynamic() {
		b, err := e.emitBoxValue(v)
		if err != nil {
			return Value{}, err
		}
		box = b
	}
	ty, err := e.emitDynamicTypeof(box)
	if err != nil {
		return Value{}, err
	}
	isFn := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq "+irPtr+" %s, %s", isFn, ty.Ref, e.internString("function")))
	badL, okL := e.freshLabel("promise.exec.bad"), e.freshLabel("promise.exec.ok")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isFn, okL, badL))
	e.emitLabel(badL)
	text, err := e.emitV8ValueText(box)
	if err != nil {
		return Value{}, err
	}
	m1, err := e.emitStringConcat(Value{Ref: e.internString("Promise resolver "), Ty: TypePtr}, text)
	if err != nil {
		return Value{}, err
	}
	msg, err := e.emitStringConcat(m1, Value{Ref: e.internString(" is not a function"), Ty: TypePtr})
	if err != nil {
		return Value{}, err
	}
	e.ensureExceptionHelpers()
	e.excThrow(e.buildErrorObj(errorKindIDs["TypeError"], msg.Ref, e.internString("TypeError")))
	e.emitTerminator("unreachable")
	e.emitLabel(okL)
	if !v.Ty.IsDynamic() {
		// A static non-function never is one: the closure is never read.
		return Value{Ref: "zeroinitializer", Ty: fnTy}, nil
	}
	return e.convert(box, fnTy), nil
}
