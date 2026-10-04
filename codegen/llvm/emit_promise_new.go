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
	"strings"
)

// The settle, reject and adoption routines live in promisesrc/settle.c
// (TDD-00240).
//
//go:embed promisesrc/settle.c
var promiseSettleSource string

// PromiseSettleSource is settle.c, behind kml_layout.h.
func PromiseSettleSource() string { return layoutHeader() + promiseSettleSource }

// UsesPromiseSettle reports whether the program links settle.c.
func (e *Emitter) UsesPromiseSettle() bool { return e.usedPromiseSettle }

// ensurePromiseSettle declares @__kml_promise_settle(ptr %p, i64 %state): settle a
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
	e.ensureNanBox()     // @__kml_promise_reject_box
	e.emitGlobal(`declare void @__kml_promise_settle(ptr, i64)
declare void @__kml_promise_reject_with(ptr, i64, i64)
declare void @__kml_promise_reject_err(ptr, i64)
declare void @__kml_promise_reject_from(ptr, ptr)
declare void @__kml_promise_reject_box(ptr, i64)`)
}

// emitRejectPromise rejects promise q with the caught value (tag, an i8 or
// i64 register or literal; pay, i64): the one rejection writer.
func (e *Emitter) emitRejectPromise(q, tag string, tagIsI8 bool, pay string) {
	e.ensurePromiseSettle()
	if tagIsI8 {
		w := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = zext i8 %s to i64", w, tag))
		tag = w
	}
	e.emitInstr(fmt.Sprintf("call void @__kml_promise_reject_with(ptr %s, i64 %s, i64 %s)", q, tag, pay))
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

	valTy := e.newPromiseValueType(ex)

	e.ensurePromiseSettle()
	e.ensureExceptionHelpers()
	e.ensureMalloc()

	p := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_task_alloc_promise()", p))

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
		resolveTy.IsPromiseResolver = true
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
	e.emitGlobal("declare void @__kml_promise_adopt_runner(ptr)")
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
	e.emitMarkPromiseHandled(srcRef)
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
		if isUnconstrainedDynamic(valTy) {
			// resolve(x) with a promise adopts it.
			e.ensurePromiseResolveAny()
			e.emitInstr(fmt.Sprintf("call void @__kml_promise_resolve_any(ptr %%p, i64 %s)", v.Ref))
			e.emitTerminator("ret void")
		} else {
			e.storePromiseValue("%p", v)
		}
	}
	if !e.blockDone {
		e.emitInstr("call void @__kml_promise_settle(ptr %p, i64 1)")
		e.emitInstr("ret void")
	}
	params := "ptr %p"
	if !isVoid {
		if valTy.IsArray {
			params = "ptr %p, ptr %vptr, i64 %vlen"
		} else {
			params = fmt.Sprintf("ptr %%p, %s %%v", StructFieldIR(valTy))
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
	return e.defineContentNamed("@__kml_promise_reject.", "void", "ptr %p, { i8, i64 } %err", e.allocas.String()+e.body.String())
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

// newPromiseValueType is T of `new Promise<T>(executor)`: the type argument,
// else the checker's (a contextual `Promise<number>`), else `any` — the
// value an untyped promise resolves with.
func (e *Emitter) newPromiseValueType(ex *ast.NewExpression) Type {
	if len(ex.TypeArgs) == 1 {
		return e.resolveType(ex.TypeArgs[0])
	}
	if c := e.front(); c != nil {
		t := c.TypeOf(ex)
		if !c.Unanswered(t) && len(t.TypeArgs) == 1 {
			if r, ok := lowerRepr(t.TypeArgs[0]); ok && !r.IsArray && !r.IsObject {
				return r
			}
		}
	}
	return TypeAny
}
