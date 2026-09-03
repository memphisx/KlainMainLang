// emit_promise_new.go — `new Promise((resolve, reject) => …)`, the executor
// constructor (TDD-00087). The promise is a task-shaped promise; resolve/reject
// are per-site closures capturing it that settle it (and wake any awaiter + fire
// reactions) via @__kml_promise_settle when the executor — or a later callback it
// stashed them into — calls them.
package llvm

import (
	"KlainMainLang/ast"
	"fmt"
	"strings"
)

// ensurePromiseSettle emits @__kml_promise_settle(ptr %p, i64 %state): settle a
// bare promise (state 1 fulfilled / 2 rejected — the value is already in v0/v1),
// waking a parked awaiter and enqueuing its reactions. The first settle wins; a
// later resolve/reject is a no-op. Factored from __kml_task_finish's body, which
// only settles a promise derived from a task.
func (e *Emitter) ensurePromiseSettle() {
	if e.usedPromiseSettle {
		return
	}
	e.usedPromiseSettle = true
	e.ensurePromiseRuntime()
	e.ensureMicrotasks() // @__kml_promise_drain_reactions
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_promise_settle(ptr %%p, i64 %%state) {
entry:
  %%res_p = getelementptr %s, ptr %%p, i32 0, i32 0
  %%cur = load i64, ptr %%res_p, align 8
  %%settled = icmp ne i64 %%cur, 0
  br i1 %%settled, label %%ret, label %%do
do:
  store i64 %%state, ptr %%res_p, align 8
  %%w_p = getelementptr %s, ptr %%p, i32 0, i32 1
  %%w = load ptr, ptr %%w_p, align 8
  %%haswaiter = icmp ne ptr %%w, null
  br i1 %%haswaiter, label %%wake, label %%drain
wake:
  %%pp_p = getelementptr %s, ptr %%w, i32 0, i32 %d
  store ptr null, ptr %%pp_p, align 8
  br label %%drain
drain:
  call void @__kml_promise_drain_reactions(ptr %%p)
  ret void
ret:
  ret void
}`, promiseStructIR, promiseStructIR, taskStructIR, taskPendingProm))
}

// emitNewPromise implements `new Promise<T>((resolve, reject) => …)`. It allocates
// a pending task promise, builds resolve/reject as per-site closures over it, and
// runs the executor immediately with them. Resolution may be synchronous (the
// executor calls resolve/reject before returning) or deferred (it stashes one into
// a later callback, e.g. setTimeout) — either way the promise is a real task
// promise awaitable / chainable like any other.
func (e *Emitter) emitNewPromise(ex *ast.NewExpression) (Value, error) {
	if len(ex.Args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: new Promise expects a single executor argument", ex.GetPos().Line, ex.GetPos().Col)
	}

	// T = the resolved value type (new Promise<T>), defaulting to number.
	valTy := TypeI64
	if len(ex.TypeArgs) == 1 {
		valTy = e.resolveType(ex.TypeArgs[0])
	}

	e.ensurePromiseSettle()
	e.ensureExceptionHelpers()
	e.ensureMalloc()

	p := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_task_alloc_promise()", p))

	// Per-site resolve/reject settle functions + their closure headers over p.
	e.newPromiseCtr++
	resolveFn := fmt.Sprintf("@__kml_promise_resolve_%d", e.newPromiseCtr)
	rejectFn := fmt.Sprintf("@__kml_promise_reject_%d", e.newPromiseCtr)
	e.emitResolveThunk(resolveFn, valTy)
	e.emitRejectThunk(rejectFn)

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
		resolveTy.IsPromiseResolver = true
	}
	rejectTy := FuncType([]Type{errorObjType}, TypeVoid)
	// The executor may be an arrow or function-expression literal (its two params
	// are hinted so `resolve(x)`/`reject(e)` get the right closure signatures even
	// unannotated), or any closure-typed expression already in scope — a variable
	// holding an executor, or a bare top-level-function reference (which emitExpr
	// resolves to a closure value). All closures share the `{fnptr, env}` ABI, so
	// the call below works regardless.
	var execVal Value
	var err error
	switch fn := ex.Args[0].(type) {
	case *ast.ArrowFunction:
		execVal, err = e.emitArrowFunctionWithHints(fn, []Type{resolveTy, rejectTy})
	case *ast.FunctionExpression:
		execVal, err = e.emitFunctionExpression(fn, []Type{resolveTy, rejectTy})
	default:
		execVal, err = e.emitExpr(ex.Args[0])
		if err == nil && !execVal.Ty.IsFunc {
			return Value{}, fmt.Errorf("%d:%d: new Promise's executor must be a function (arrow, function expression, or a closure-typed value)", ex.GetPos().Line, ex.GetPos().Col)
		}
	}
	if err != nil {
		return Value{}, err
	}

	execTy := FuncType([]Type{resolveTy, rejectTy}, TypeVoid)
	e.emitClosureCallValues(execVal.Ref, execTy, []Value{
		{Ref: resolveClo, Ty: resolveTy},
		{Ref: rejectClo, Ty: rejectTy},
	})

	pt := PromiseOf(valTy)
	pt.PromiseTask = true
	return Value{Ref: p, Ty: pt}, nil
}

// ensurePromiseAdoptRunner emits @__kml_promise_adopt_runner(ptr %env) once — the
// microtask that performs thenable adoption (TDD-00091). Its env is a { ptr src,
// ptr tgt } pair: when the adopted source promise settles, this copies the
// source's settlement (state + value/reason bits) into the target and settles it,
// so `resolve(src)` makes the outer promise mirror `src`. First-settle-wins on the
// target is honored (a prior resolve/reject already having settled it wins).
func (e *Emitter) ensurePromiseAdoptRunner() {
	if e.usedPromiseAdoptRunner {
		return
	}
	e.usedPromiseAdoptRunner = true
	e.ensurePromiseSettle()
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_promise_adopt_runner(ptr %%env) {
entry:
  %%src_p = getelementptr { ptr, ptr }, ptr %%env, i32 0, i32 0
  %%src = load ptr, ptr %%src_p, align 8
  %%tgt_p = getelementptr { ptr, ptr }, ptr %%env, i32 0, i32 1
  %%tgt = load ptr, ptr %%tgt_p, align 8
  %%ts_p = getelementptr %s, ptr %%tgt, i32 0, i32 0
  %%ts = load i64, ptr %%ts_p, align 8
  %%already = icmp ne i64 %%ts, 0
  br i1 %%already, label %%ret, label %%do
do:
  %%as_p = getelementptr %s, ptr %%src, i32 0, i32 0
  %%as = load i64, ptr %%as_p, align 8
  %%av0_p = getelementptr %s, ptr %%src, i32 0, i32 2
  %%av0 = load i64, ptr %%av0_p, align 8
  %%av1_p = getelementptr %s, ptr %%src, i32 0, i32 3
  %%av1 = load i64, ptr %%av1_p, align 8
  %%tv0_p = getelementptr %s, ptr %%tgt, i32 0, i32 2
  store i64 %%av0, ptr %%tv0_p, align 8
  %%tv1_p = getelementptr %s, ptr %%tgt, i32 0, i32 3
  store i64 %%av1, ptr %%tv1_p, align 8
  call void @__kml_promise_settle(ptr %%tgt, i64 %%as)
  ret void
ret:
  ret void
}`, promiseStructIR, promiseStructIR, promiseStructIR, promiseStructIR, promiseStructIR, promiseStructIR))
}

// emitPromiseAdopt implements `resolve(srcPromise)` thenable adoption (TDD-00091):
// register an adopt reaction on the source promise so the target (the resolve
// closure's env) mirrors the source's settlement. If the source is already
// settled, the reaction is enqueued now; otherwise it is attached to the source's
// reaction list and fires when the source settles — the same reaction/microtask
// mechanism `.then` uses, so ordering matches JS (adoption is a microtask tick).
func (e *Emitter) emitPromiseAdopt(tgtRef, srcRef string) {
	e.ensurePromiseAdoptRunner()
	e.ensureMicrotasks()
	e.ensureMalloc()

	// env = { src, tgt }
	env := e.freshReg()
	envSrc := e.freshReg()
	envTgt := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", env))
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 0", envSrc, env))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", srcRef, envSrc))
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 1", envTgt, env))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", tgtRef, envTgt))

	// closure = { adopt_runner, env }
	clo := e.freshReg()
	cfp := e.freshReg()
	cep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", clo))
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 0", cfp, clo))
	e.emitInstr(fmt.Sprintf("store ptr @__kml_promise_adopt_runner, ptr %s, align 8", cfp))
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 1", cep, clo))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", env, cep))

	// If src already settled → enqueue now; else attach a reaction node onto
	// src.reactions (the same pattern emitPromiseThen uses).
	res := e.freshReg()
	resP := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 0", resP, promiseStructIR, srcRef))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", res, resP))
	settled := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", settled, res))
	nowL := e.freshLabel("adopt.now")
	laterL := e.freshLabel("adopt.later")
	doneL := e.freshLabel("adopt.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", settled, nowL, laterL))
	e.emitLabel(nowL)
	e.emitInstr(fmt.Sprintf("call void @__kml_microtask_enqueue(ptr %s)", clo))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(laterL)
	node := e.freshReg()
	rxP := e.freshReg()
	oldHead := e.freshReg()
	nodeClo := e.freshReg()
	nodeNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", node))
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 4", rxP, promiseStructIR, srcRef))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", oldHead, rxP))
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 0", nodeClo, node))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", clo, nodeClo))
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 1", nodeNext, node))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", oldHead, nodeNext))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", node, rxP))
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
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", hdr))
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 0", fpp, hdr))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", fn, fpp))
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 1", epp, hdr))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", envPtr, epp))
	return hdr
}

// emitClosureCallValues invokes a closure with already-evaluated Value arguments
// (the executor call, whose args are the resolve/reject closure headers). A
// narrow by-value counterpart to emitClosureCallByPtr, for scalar/ptr params and
// a void return — the only shapes the executor needs.
func (e *Emitter) emitClosureCallValues(closurePtr string, ty Type, argVals []Value) {
	fpSlot := e.freshReg()
	fpVal := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 0", fpSlot, closurePtr))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", fpVal, fpSlot))
	epSlot := e.freshReg()
	epVal := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 1", epSlot, closurePtr))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", epVal, epSlot))

	argParts := []string{"ptr " + epVal}
	tyParts := []string{"ptr"}
	for i, av := range argVals {
		pty := ty.FuncParams[i]
		v := e.coerce(av, pty)
		argParts = append(argParts, fmt.Sprintf("%s %s", pty.IR, v.Ref))
		tyParts = append(tyParts, pty.IR)
	}
	e.emitInstr(fmt.Sprintf("call void (%s) %s(%s)", strings.Join(tyParts, ", "), fpVal, strings.Join(argParts, ", ")))
}

// emitResolveThunk emits `void @fn(ptr %p, <T> %v)`: store v into the promise's
// value slot(s) and settle it fulfilled. The promise arrives as the closure env
// (first arg), the resolved value as the second.
func (e *Emitter) emitResolveThunk(fn string, valTy Type) {
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
		if valTy.IsArray {
			// A resolved array value arrives via the closure array-argument ABI
			// (`ptr %vptr` — the boxed {data, len} header — plus `i64 %vlen`), the
			// same shape `emitClosureCall` marshals a `resolve(arr)` call into. Load
			// the data pointer out of the box and rebuild the {ptr, i64} aggregate
			// storePromiseValue expects. (Previously the thunk declared a single
			// `{ptr,i64} %v` param, so it read the *box* pointer as the data pointer
			// — corrupting every `Promise<T[]>`.)
			dp := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %%vptr, align 8", dp))
			a0 := e.freshReg()
			a1 := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = insertvalue { ptr, i64 } undef, ptr %s, 0", a0, dp))
			e.emitInstr(fmt.Sprintf("%s = insertvalue { ptr, i64 } %s, i64 %%vlen, 1", a1, a0))
			v = Value{Ref: a1, Ty: valTy}
		}
		e.storePromiseValue("%p", v)
	}
	e.emitInstr("call void @__kml_promise_settle(ptr %p, i64 1)")
	e.emitInstr("ret void")
	params := "ptr %p"
	if !isVoid {
		if valTy.IsArray {
			params = "ptr %p, ptr %vptr, i64 %vlen"
		} else {
			params = fmt.Sprintf("ptr %%p, %s %%v", StructFieldIR(valTy))
		}
	}
	e.functions.WriteString(fmt.Sprintf("\ndefine void %s(%s) {\nentry:\n", fn, params))
	e.functions.WriteString(e.allocas.String())
	e.functions.WriteString(e.body.String())
	e.functions.WriteString("}\n")
}

// emitRejectThunk emits `void @fn(ptr %p, ptr %err)`: store the error pointer bits
// into the promise's value slot and settle it rejected.
func (e *Emitter) emitRejectThunk(fn string) {
	restore := e.beginThunkEmit()
	defer restore()
	e.emitThunkAlreadySettledGuard()
	bits := e.freshReg()
	v0 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = ptrtoint ptr %%err to i64", bits))
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %%p, i32 0, i32 2", v0, promiseStructIR))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", bits, v0))
	e.emitInstr("call void @__kml_promise_settle(ptr %p, i64 2)")
	e.emitInstr("ret void")
	e.functions.WriteString(fmt.Sprintf("\ndefine void %s(ptr %%p, ptr %%err) {\nentry:\n", fn))
	e.functions.WriteString(e.allocas.String())
	e.functions.WriteString(e.body.String())
	e.functions.WriteString("}\n")
}

// emitThunkAlreadySettledGuard emits an early `ret void` when %p is already
// settled (state != 0), so only the first resolve/reject writes the value.
func (e *Emitter) emitThunkAlreadySettledGuard() {
	sp := e.freshReg()
	cur := e.freshReg()
	settled := e.freshReg()
	storeL := e.freshLabel("thunk.store")
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %%p, i32 0, i32 0", sp, promiseStructIR))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", cur, sp))
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
	savedAllocas := e.allocas
	savedBody := e.body
	savedRegCtr := e.regCtr
	savedLabelCtr := e.labelCtr
	savedBlockDone := e.blockDone
	e.allocas = strings.Builder{}
	e.body = strings.Builder{}
	e.regCtr = 0
	e.labelCtr = 0
	e.blockDone = false
	return func() {
		e.allocas = savedAllocas
		e.body = savedBody
		e.regCtr = savedRegCtr
		e.labelCtr = savedLabelCtr
		e.blockDone = savedBlockDone
	}
}
