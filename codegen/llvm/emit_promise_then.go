// emit_promise_then.go — Promise.prototype.then / .catch / .finally over a task
// promise (TDD-00083 Stage 3, value-chaining added in a follow-on). A reaction is
// a 0-arg closure {runner, env} where the runner is a per-signature,
// type-specialized function that reads the settled source promise's value/reason,
// invokes the callback, and settles the *returned* promise Q with the callback's
// result — so `p.then(f).then(g)` chains. The runner runs as a microtask, so
// ordering matches the spec (after the current synchronous run, before timers).
//
// Chaining model:
//   - `.then(onF, onR?)`: Q resolves to onF(value) on fulfillment; on rejection Q
//     resolves to onR(reason) if onR is present (recovery), else Q rejects with the
//     same reason (propagation). Q's value type is the callback's return type.
//   - `.catch(onR)`: same as `.then(undefined, onR)`.
//   - `.finally(onFin)`: onFin runs for its side effect; Q passes the source's
//     settlement (value or rejection) straight through unchanged.
package llvm

import (
	"KlainMainLang/ast"
	_ "embed"
	"fmt"
	"reflect"
	"strings"
)

// The deferred settle, the finally wait and the fetch bridge live in
// promisesrc/ (TDD-00240).
//
//go:embed promisesrc/defer.c
var promiseDeferSource string

//go:embed promisesrc/finally.c
var promiseFinallySource string

//go:embed promisesrc/fetchdrive.c
var fetchDriveSource string

// PromiseDeferSource is defer.c, behind kml_layout.h.
func PromiseDeferSource() string { return layoutHeader() + promiseDeferSource }

// PromiseFinallySource is finally.c, behind kml_layout.h.
func PromiseFinallySource() string { return layoutHeader() + promiseFinallySource }

// FetchDriveSource is fetchdrive.c, behind kml_layout.h.
func FetchDriveSource() string { return layoutHeader() + fetchDriveSource }

// UsesPromiseDefer reports whether the program links defer.c.
func (e *Emitter) UsesPromiseDefer() bool { return e.fnDecls["__kml_promise_defer_settle"] }

// UsesPromiseFinally reports whether the program links finally.c.
func (e *Emitter) UsesPromiseFinally() bool { return e.fnDecls["__kml_promise_finally_wait"] }

// UsesFetchDrive reports whether the program links fetchdrive.c.
func (e *Emitter) UsesFetchDrive() bool { return e.usedFetchDriveRunner }

// emitRejectCallback emits a `.catch`/onRejected callback, hinting an
// arrow-function parameter to the shared error object shape so `e.message`/
// `e.name` (and `AggregateError`'s `.errors`) work inside it without the caller
// having to annotate the parameter (an `Error`-family annotation resolves to the
// same shape either way). A non-arrow callback (a named function reference) is
// emitted unchanged.
func (e *Emitter) emitRejectCallback(arg ast.Expression) (Value, error) {
	// An `any`/`unknown`-annotated reject parameter is the same "hold anything,
	// access leniently" role as an unannotated one — treat it as the caught-value
	// hint (which yields undefined for a missing member rather than the stricter
	// `any` dynamic-access throw) by clearing the annotation for the emit
	// (TDD-00207). A concrete annotation (string/Error/object) is left to the
	// adapter below.
	restoreParam := e.clearDynamicRejectParam(arg)
	var v Value
	var err error
	switch a := arg.(type) {
	case *ast.ArrowFunction:
		v, err = e.emitArrowFunctionWithHints(a, []Type{TypeCaught})
	case *ast.FunctionExpression:
		v, err = e.emitFunctionExpression(a, []Type{TypeCaught})
	default:
		v, err = e.emitExpr(arg)
	}
	restoreParam()
	if err != nil {
		return Value{}, err
	}
	if c, ok := e.thenCallbackFromAny(v, TypeAny); ok {
		v = c // an annotated-any parameter: the adapter below boxes the reason
	}
	// The .then/.catch runner always calls onR with a { i8, i64 } caught-value
	// reason. If the handler's parameter is annotated (string / Error / any / an
	// object shape) the TypeCaught hint was overridden, so its closure expects a
	// different ABI — wrap it in an adapter that coerces the reason (TDD-00207).
	// A function taking its arguments as a rest: called with the one
	// reason, so its rest holds that reason.
	if v.Ty.IsFunc() && v.Ty.FuncHasRest && len(v.Ty.FuncParams) == 1 {
		ret := TypeVoid
		if v.Ty.FuncRetType != nil {
			ret = *v.Ty.FuncRetType
		}
		if adapted, ok := e.emitBoxedClosureAs(v, FuncType([]Type{TypeCaught}, ret)); ok {
			return adapted, nil
		}
	}
	if v.Ty.IsFunc() && !(len(v.Ty.FuncParams) >= 1 && v.Ty.FuncParams[0].IsCaught()) {
		return e.emitRejectAdapter(v), nil
	}
	return v, nil
}

// emitFulfillCallback emits a `.then` onFulfilled callback, hinting an
// arrow-function parameter with no annotation to the source promise's value type
// (e.g. a fetch chain's `Response`), so `.then(r => r.status)` works without the
// caller annotating `r`. An annotated param, or a non-arrow callback, is emitted
// unchanged.
// closureOfBoxedHandler is a reaction handler the runner cannot call with
// the settled value alone — one held boxed (compiled as a dynamic function)
// or one taking a rest slot (its `arguments`) — as a closure that does: it
// takes the value as param and calls the handler with it. Any other handler
// is returned as is.
func (e *Emitter) closureOfBoxedHandler(v Value, param Type, pos ast.Pos) (Value, error) {
	if v.Ref == "null" || v.Ty.IR == "ptr" && !(v.Ty.IsFunc() && v.Ty.FuncHasRest) {
		return v, nil
	}
	const arg = "__kml_rxn_arg"
	call := ast.NewCallExpression(e.bindValue(v, pos), []ast.Expression{ast.NewIdentifier(arg, pos)}, pos)
	return e.emitArrowFunctionWithHints(ast.NewArrowFunction([]ast.Param{{Name: arg, Pos: pos}}, nil, call, nil, pos), []Type{param})
}

func (e *Emitter) emitFulfillCallback(arg ast.Expression, valueTy Type) (Value, error) {
	if valueTy.IR != "void" && valueTy.IR != "" {
		var v Value
		var err error
		lit := true
		switch fn := arg.(type) {
		case *ast.ArrowFunction:
			v, err = e.emitArrowFunctionWithHints(fn, []Type{valueTy})
		case *ast.FunctionExpression:
			v, err = e.emitFunctionExpression(fn, []Type{valueTy})
		default:
			lit = false
		}
		if lit {
			if err != nil {
				return Value{}, err
			}
			// An annotated parameter (`(buf: ArrayBuffer) => …`) over a
			// promise of `any`: the runner passes the boxed value, which an
			// adapter unboxes to the parameter's type.
			if isUnconstrainedDynamic(valueTy) && v.Ty.IsFunc() && len(v.Ty.FuncParams) >= 1 && !v.Ty.FuncParams[0].IsDynamic() {
				ret := TypeVoid
				if v.Ty.FuncRetType != nil {
					ret = *v.Ty.FuncRetType
				}
				if adapted, ok := e.emitBoxedClosureAs(v, FuncType([]Type{valueTy}, ret)); ok {
					return adapted, nil
				}
			}
			// An annotated object parameter of another layout than the
			// promise's (`(r: { a: any }) => …` over a Promise<{ a: number }>):
			// the value crosses through its box, which re-reads it as the
			// parameter's type.
			if v.Ty.IsFunc() && len(v.Ty.FuncParams) >= 1 && valueTy.IsObject() && v.Ty.FuncParams[0].IsObject() &&
				valueTy.StructIR() != v.Ty.FuncParams[0].StructIR() {
				ret := TypeVoid
				if v.Ty.FuncRetType != nil {
					ret = *v.Ty.FuncRetType
				}
				if adapted, ok := e.emitBoxedClosureAs(v, FuncType([]Type{valueTy}, ret)); ok {
					return adapted, nil
				}
			}
			return v, nil
		}
	}
	v, err := e.emitExpr(arg)
	if err != nil {
		return Value{}, err
	}
	if p := valueTy; p.IR != "void" && p.IR != "" {
		if c, ok := e.thenCallbackFromAny(v, p); ok {
			return c, nil
		}
	} else if c, ok := e.thenCallbackFromAny(v, TypeAny); ok {
		return c, nil
	}
	// A named function whose parameter is `any` over a promise of a
	// concrete type: an adapter boxes the value the runner passes.
	if v.Ty.IsFunc() && !v.Ty.FuncHasRest && len(v.Ty.FuncParams) >= 1 && v.Ty.FuncParams[0].IsDynamic() &&
		valueTy.IR != "void" && valueTy.IR != "" && !valueTy.IsDynamic() {
		ret := TypeVoid
		if v.Ty.FuncRetType != nil {
			ret = *v.Ty.FuncRetType
		}
		if adapted, ok := e.emitBoxedClosureAs(v, FuncType([]Type{valueTy}, ret)); ok {
			return adapted, nil
		}
	}
	// A void source passes undefined as an `any`: a callback taking a
	// parameter of another type (or a rest) reads it through an adapter.
	if v.Ty.IsFunc() && len(v.Ty.FuncParams) >= 1 && (valueTy.IR == "void" || valueTy.IR == "") && (v.Ty.FuncHasRest || !v.Ty.FuncParams[0].IsDynamic()) {
		ret := TypeVoid
		if v.Ty.FuncRetType != nil {
			ret = *v.Ty.FuncRetType
		}
		if adapted, ok := e.emitBoxedClosureAs(v, FuncType([]Type{TypeAny}, ret)); ok {
			return adapted, nil
		}
	}
	// A function taking its arguments as a rest (`(...a) => …`): called
	// with the one value, so its rest holds that value.
	if v.Ty.IsFunc() && v.Ty.FuncHasRest && len(v.Ty.FuncParams) == 1 && valueTy.IR != "void" && valueTy.IR != "" {
		ret := TypeVoid
		if v.Ty.FuncRetType != nil {
			ret = *v.Ty.FuncRetType
		}
		if adapted, ok := e.emitBoxedClosureAs(v, FuncType([]Type{valueTy}, ret)); ok {
			return adapted, nil
		}
	}
	return v, nil
}

// isAbsentCallback reports whether a `.then` argument is a literal `undefined`
// or `null` — JS treats either as "no callback" (pass-through), not a callable.
func isAbsentCallback(arg ast.Expression) bool {
	_, ok := arg.(*ast.NullLiteral)
	return ok
}

// isNonCallableThenArg reports whether a `.then`/`.catch` handler argument is
// not callable — a literal `null`/`undefined`, or a value whose static type is
// a concrete non-function (e.g. `p.then(3, 5)`). Per spec (PerformPromiseThen /
// Promise.prototype.catch) a non-callable handler is treated as undefined
// (pass-through), so it must be lowered to a null callback slot rather than
// storing the raw value as a callback pointer (which emits e.g. a double
// constant into a `ptr` field — invalid IR). `any`/`unknown` arguments may hold
// a function at runtime, so they are never statically dropped here.
func (e *Emitter) isNonCallableThenArg(arg ast.Expression) bool {
	if isAbsentCallback(arg) {
		return true
	}
	if id, ok := arg.(*ast.Identifier); ok && id.Name == "undefined" && !e.isShadowedByLocal(id.Name) {
		return true
	}
	t := e.inferExprType(arg)
	return !t.IsFunc() && !t.IsDynamic()
}

// emitPromiseThen handles a `.then`/`.catch`/`.finally` call on a Promise value.
func (e *Emitter) emitPromiseThen(objExpr ast.Expression, kind string, args []ast.Expression, pos ast.Pos) (Value, error) {
	return e.emitPromiseThenInto(objExpr, kind, args, pos, "")
}

// emitPromiseThenInto is emitPromiseThen settling the pending promise body
// into (a species construction's, TDD-00247) instead of a fresh one, when
// into is not "".
func (e *Emitter) emitPromiseThenInto(objExpr ast.Expression, kind string, args []ast.Expression, pos ast.Pos, into string) (Value, error) {
	pVal, err := e.emitExpr(objExpr)
	if err != nil {
		return Value{}, err
	}
	if !pVal.Ty.IsPromise() {
		// A value only known as `any` here (a method read off a prototype
		// object, called): its own `then`, looked up at run time.
		if pVal.Ty.IsDynamic() && into == "" {
			return e.emitDynAnyMethodCall(pVal, kind, args, pos)
		}
		return Value{}, fmt.Errorf("%d:%d: .%s is only supported on a Promise", pos.Line, pos.Col, kind)
	}
	// A raw fetch()'s Promise<Response> is a still-pending fetch handle, not a
	// task-shaped promise. Bridge it to a *pending* task promise whose settle
	// (drive the fetch, build the Response) is deferred to a queued microtask —
	// so the synchronous script continues immediately and the transport wait
	// happens when the event loop drains, matching JS ordering (ADR-00258's
	// synchronous drive replaced). The !PromiseTask guard is essential: a
	// chained `.finally`/`.then` that itself settles to a Response (e.g.
	// `fetch(u).finally(f).then(g)`) returns a *task* promise that also looks
	// IsResponse — it must not be re-driven as a fetch handle.
	if !pVal.Ty.PromiseTask && pVal.Ty.PromiseType != nil && pVal.Ty.PromiseType.IsResponse() && !pVal.Ty.PromiseResolved {
		pVal = e.emitFetchHandleToPendingPromise(pVal.Ref)
	}
	if !pVal.Ty.PromiseTask {
		return Value{}, fmt.Errorf("%d:%d: .%s is currently supported only on a promise from a may-suspend async function or a fetch (TDD-00083 Stage 3)", pos.Line, pos.Col, kind)
	}
	// An onFulfilled literal taking `any` receives an array value boxed
	// as `any` holds it (`{}[]`'s pointers, read as `any[]`). Only a literal: a
	// passed function's first parameter may be a rest (`console.log`).
	if kind == "then" && len(args) >= 1 && anyAnnotatedFirstParam(args[0]) {
		if ft := e.inferExprType(args[0]); ft.IsFunc() && len(ft.FuncParams) > 0 && ft.FuncParams[0].IsDynamic() {
			if want := PromiseOf(ft.FuncParams[0]); promiseElemsNeedConvert(pVal.Ty, want) {
				pVal = e.convert(pVal, want)
			}
		}
	}
	innerTy := TypeVoid
	if pVal.Ty.PromiseType != nil {
		innerTy = *pVal.Ty.PromiseType
	}
	// Only the promise + microtask machinery is needed here; the fiber scheduler
	// (if the program has may-suspend fns) is pulled in by those fns themselves.
	e.ensurePromiseRuntime()
	e.ensureMicrotasks()

	// Evaluate callbacks into closure pointers ("null" when absent) and decide the
	// returned promise Q's value type U (the type Q settles to).
	onF, onR, onFin := "null", "null", "null"
	var fVal, rVal Value
	var rTy Type
	hasR := false
	finAwait := false
	retTy := TypeVoid
	// A callback returning a thenable class instance resolves Q through it.
	args = append([]ast.Expression(nil), args...)
	for i := range args {
		if i > 1 || e.isNonCallableThenArg(args[i]) {
			continue
		}
		param := innerTy
		if kind == "catch" || i == 1 {
			param = TypeCaught
		}
		if kind == "finally" {
			break
		}
		w, err := e.thenableCallback(args[i], param, pos)
		if err != nil {
			return Value{}, err
		}
		args[i] = w
	}
	switch kind {
	case "then":
		// JS treats a missing/`undefined`/`null` onFulfilled as a pass-through
		// (`p.then().then(g)` hands g the source value; `p.then(undefined, onR)`
		// is the .catch shape) — the runner already has the pass-through block,
		// so only the arity/argument handling lives here.
		if len(args) >= 1 && !e.isNonCallableThenArg(args[0]) {
			v, err := e.emitFulfillCallback(args[0], innerTy)
			if err == nil {
				v, err = e.closureOfBoxedHandler(v, innerTy, pos)
			}
			if err != nil {
				return Value{}, err
			}
			onF, fVal = v.Ref, v
			if t, ok := emittedCallbackReturn(v); ok {
				retTy = t
			} else if t, ok := e.callbackReturnType(args[0], innerTy); ok {
				retTy = t
			}
		} else {
			retTy = innerTy // pass-through keeps the source value type
		}
		if len(args) >= 2 && !e.isNonCallableThenArg(args[1]) {
			v2, err := e.emitRejectCallback(args[1])
			if err == nil {
				v2, err = e.closureOfBoxedHandler(v2, TypeCaught, pos)
			}
			if err != nil {
				return Value{}, err
			}
			onR, rVal = v2.Ref, v2
			if rTy, hasR = emittedCallbackReturn(v2); !hasR {
				rTy, hasR = e.callbackReturnType(args[1], TypeCaught)
			}
		}
	case "catch":
		if len(args) < 1 {
			return Value{}, fmt.Errorf("%d:%d: catch expects 1 argument", pos.Line, pos.Col)
		}
		// A non-callable onRejected (`p.catch(undefined)`, `p.catch(3)`) is
		// treated as undefined: the rejection propagates through unhandled.
		if e.isNonCallableThenArg(args[0]) {
			retTy = innerTy
		} else {
			v, err := e.emitRejectCallback(args[0])
			if err == nil {
				v, err = e.closureOfBoxedHandler(v, TypeCaught, pos)
			}
			if err != nil {
				return Value{}, err
			}
			onR, rVal = v.Ref, v
			retTy = innerTy // the fulfilled path passes the source value through
			if rTy, hasR = emittedCallbackReturn(v); !hasR {
				rTy, hasR = e.callbackReturnType(args[0], TypeCaught)
			}
			if hasR && !thenPathsDiffer(retTy, rTy) {
				retTy = rTy
			}
		}
	case "finally":
		if len(args) < 1 {
			return Value{}, fmt.Errorf("%d:%d: finally expects 1 argument", pos.Line, pos.Col)
		}
		v, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		if v.Ty.IsFunc() && len(v.Ty.FuncParams) > 0 {
			// onFinally is called with no arguments: a function taking some
			// (its own `arguments` included) is called so, from a closure
			// taking none.
			call := ast.NewCallExpression(e.bindValue(v, pos), nil, pos)
			if v, err = e.emitExpr(ast.NewArrowFunction(nil, nil, call, nil, pos)); err != nil {
				return Value{}, err
			}
		}
		onFin = v.Ref
		retTy = innerTy // finally passes the source value through unchanged
		// An onFinally returning a promise is waited for (and its rejection
		// taken), as the spec's thenFinally does.
		if v.Ty.IsFunc() && v.Ty.FuncRetType != nil && v.Ty.FuncRetType.IsPromise() {
			rt := *v.Ty.FuncRetType
			finAwait = rt.PromiseTask || rt.PromiseType == nil || !rt.PromiseType.IsResponse()
		}
	}

	// Q settles to the fulfilled path's value or the rejection handler's: TS
	// types it as their union, which Q holds boxed when the two differ. A
	// pass-through fulfilled path becomes an identity callback that boxes.
	if hasR && thenPathsDiffer(retTy, rTy) {
		if onF == "null" {
			v, err := e.emitFulfillCallback(thenIdentityCallback(pos), innerTy)
			if err != nil {
				return Value{}, err
			}
			fVal = v
		}
		f, okF := e.closureReturningAny(fVal)
		r, okR := e.closureReturningAny(rVal)
		if !okF || !okR {
			return Value{}, fmt.Errorf("%d:%d: .%s whose handlers settle to different types is not supported for these handler types", pos.Line, pos.Col, kind)
		}
		onF, onR, retTy = f.Ref, r.Ref, TypeAny
	}

	// A callback that returns a promise resolves Q *with* that promise: Q settles
	// the way it settles, to its value (flattening — `p.then(() => later())`
	// yields later()'s value, not a promise). A raw fetch handle returned from
	// the callback is bridged to a task promise first, inside the runner.
	adopt := 0
	if retTy.IsPromise() {
		adopt = 1
		if !retTy.PromiseTask && retTy.PromiseType != nil && retTy.PromiseType.IsResponse() && !retTy.PromiseResolved {
			adopt = 2
			e.ensureFetchSlotToPromise()
		}
		e.ensurePromiseAdopt()
	}

	// Q: the returned promise, allocated *pending* so a chained `.then` attaches a
	// reaction that the runner fires when it settles Q.
	q := into
	if q == "" {
		q = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_task_alloc_promise()", q))
	}

	// env = { ptr p, ptr onF, ptr onR, ptr onFin, ptr q, ptr runner }
	env := e.freshReg()
	e.heapMallocInto(env, "48")
	storeEnv := func(idx int, ref string) {
		gp := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr, ptr, ptr, ptr }, ptr %s, i32 0, i32 %d", gp, env, idx))
		e.heapStore("ptr", ref, gp, 8)
	}
	storeEnv(0, pVal.Ref)
	storeEnv(1, onF)
	storeEnv(2, onR)
	storeEnv(3, onFin)
	storeEnv(4, q)

	runner := e.emitThenRunner(innerTy, retTy, adopt, finAwait)
	// The reaction runs the runner under __kml_then_guard, which rejects Q
	// when a handler throws.
	runnerSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 5", runnerSlot, env))
	e.heapStore("ptr", runner, runnerSlot, 8)
	if adopt != 0 {
		// Q's value type is what the returned promise resolves to.
		if retTy.PromiseType != nil {
			retTy = *retTy.PromiseType
		} else {
			retTy = TypeVoid
		}
	}

	// closure { runner, env }
	clo := e.freshReg()
	cfp := e.freshReg()
	cep := e.freshReg()
	e.heapMallocInto(clo, "16")
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 0", cfp, clo))
	e.heapStore("ptr", "@__kml_then_guard", cfp, 8)
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 1", cep, clo))
	e.heapStore("ptr", env, cep, 8)

	e.emitAttachPromiseReaction(pVal.Ref, clo)

	qt := PromiseOf(retTy)
	qt.PromiseTask = true
	return Value{Ref: q, Ty: qt}, nil
}

// emitAttachPromiseReaction registers reaction closure clo on promise prom:
// if the source is already settled, enqueue the closure as a microtask now;
// else push a { closure, next } node onto the promise's reaction list, which
// __kml_promise_drain_reactions fires at settle time. Shared by .then/.catch/
// .finally and the stream finished()/pipeline() callback forms.
func (e *Emitter) emitAttachPromiseReaction(prom, clo string) {
	e.emitMarkPromiseHandled(prom)
	// The reaction runs in the async context it was registered in, as Node's
	// does (TDD-00168): the reaction is a `void (env)` closure like a timer's.
	if e.programUsesALS || e.programIndependent() {
		clo = e.wrapTimerClosureWithAsyncCtx(clo)
	}
	res := e.freshReg()
	resP := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 0", resP, promiseStructIR, prom))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", res, resP))
	settled := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", settled, res))
	nowL := e.freshLabel("then.now")
	laterL := e.freshLabel("then.later")
	doneL := e.freshLabel("then.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", settled, nowL, laterL))
	e.emitLabel(nowL)
	e.emitInstr(fmt.Sprintf("call void @__kml_microtask_enqueue(ptr %s)", clo))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(laterL)
	// node = { closure, next = p.reactions }; p.reactions = node
	node := e.freshReg()
	rxP := e.freshReg()
	oldHead := e.freshReg()
	nodeClo := e.freshReg()
	nodeNext := e.freshReg()
	e.heapMallocInto(node, "16")
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 4", rxP, promiseStructIR, prom))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", oldHead, rxP))
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 0", nodeClo, node))
	e.heapStore("ptr", clo, nodeClo, 8)
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 1", nodeNext, node))
	e.heapStore("ptr", oldHead, nodeNext, 8)
	e.heapStore("ptr", node, rxP, 8)
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
}

// ensureFetchDriveRunner declares @__kml_fetch_drive_run(ptr %env) exactly once —
// the deferred microtask step that bridges a raw fetch handle to a task promise.
// env = { ptr slot, ptr prom }: it drives the fetch (`__kml_await_fetch`, the
// same drive `await` uses), builds the Response, stores it into prom's value
// slot, and settles prom fulfilled via __kml_promise_settle (so reactions
// attached while pending fire, and a parked awaiter wakes). A transport-level
// failure (which `__kml_await_fetch` throws) is caught via setjmp and settles
// prom *rejected* — `fetch(u).catch(e => …)` recovers it; an HTTP 4xx/5xx is a
// fulfilled Response, per WHATWG. The fetch slot is NOT freed: a fetch
// Promise<Response> is a reusable value (TDD-00090) — `const p = fetch(u);
// p.then(f); await p` must still read a live slot.
func (e *Emitter) ensureFetchDriveRunner() {
	if e.usedFetchDriveRunner {
		return
	}
	e.usedFetchDriveRunner = true
	e.ensurePromiseRuntime()
	e.ensureFetchAsync()
	e.ensureAwaitFetchHeaders()
	e.ensureExceptionHelpers()
	e.ensureMalloc()
	e.ensurePromiseSettle()
	e.ensureCalloc()

	e.emitGlobal("declare void @__kml_fetch_drive_run(ptr)")
}

// emitFetchHandleToPendingPromise bridges a raw fetch()'s Promise<Response>
// handle (slotRef points at the malloc'd slot holding the pending curl handle)
// to a *pending* task-shaped promise whose settle is deferred to a queued
// microtask (@__kml_fetch_drive_run) — so `fetch(u).then(f); console.log("x")`
// runs the synchronous script first and the transport wait happens when the
// event loop drains, not at the `.then` call site. The fetch transfer itself is
// already in flight from the fetch() call; only the completion wait is deferred.
func (e *Emitter) emitFetchHandleToPendingPromise(slotRef string) Value {
	// The bridge runs as a coroutine that parks on the fetch (TDD-00223 §3) —
	// see runtime_promise_adopt.go.
	e.ensureFetchSlotToPromise()
	prom := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_fetch_slot_to_promise(ptr %s)", prom, slotRef))
	rt := PromiseOf(ResponseType())
	rt.PromiseTask = true
	return Value{Ref: prom, Ty: rt}
}

// thenValLoadIR returns the IR reconstructing `%val` (of ty.IR) from the source
// promise's `%v0` i64 bits, and the argument-type string for the callback call.
// For a void source there is no argument.
func thenValLoadIR(ty Type) (load, argIR string) {
	// A nullable scalar (`T | undefined`) travels as payload bits in v0 and the
	// presence bit in v1 (storePromiseValue's convention, ADR-01065); the
	// callback takes the `{ i1, T }` aggregate.
	if isNullableScalar(ty) {
		base := ty.withoutNullable()
		pl, _ := thenValLoadIR(base)
		pl = strings.Replace(pl, "%val", "%valpl", 1)
		agg := nullableScalarStorageIR(ty)
		return pl + "  %valpr = trunc i64 %v1 to i1\n" +
			"  %val0 = insertvalue " + agg + " undef, i1 %valpr, 0\n" +
			"  %val = insertvalue " + agg + " %val0, " + base.IR + " %valpl, 1\n", agg
	}
	switch ty.IR {
	case "void", "":
		return "", ""
	case "ptr":
		return "  %val = inttoptr i64 %v0 to ptr\n", "ptr"
	case "i64":
		return "  %val = add i64 %v0, 0\n", "i64"
	case "double":
		return "  %val = bitcast i64 %v0 to double\n", "double"
	default:
		return fmt.Sprintf("  %%val = trunc i64 %%v0 to %s\n", ty.IR), ty.IR
	}
}

// thenStoreResultIR returns IR that stores a callback result register (%rv<sfx>,
// of type retTy) into the returned promise %q's value slots and marks %q
// fulfilled (res = 1). For a void result it stores 0 (undefined). The sfx keeps
// SSA names unique across the two call blocks (fulfill vs reject) of one runner.
func thenStoreResultIR(retTy Type, sfx string) (produce func(callExpr string) string, storeAndSettle string) {
	settle := `  %qres` + sfx + ` = getelementptr ` + promiseStructIR + `, ptr %q, i32 0, i32 0
  store i64 1, ptr %qres` + sfx + `, align 8
`
	v0slot := `  %qv0` + sfx + ` = getelementptr ` + promiseStructIR + `, ptr %q, i32 0, i32 2
`
	// An array result is checked before the IR switch: an array's IR is "ptr"
	// (the header-pointer model), so it would otherwise fall into the `case
	// "ptr"` scalar-pointer arm. The callback returns a header pointer (`ret ptr`,
	// TDD-00213 Stage 3), which v0 holds as loadPromiseValue reads it back.
	if retTy.IsArray() {
		return func(callExpr string) string { return "  %rv" + sfx + " = " + callExpr + "\n" },
			"  %rvpi" + sfx + " = ptrtoint ptr %rv" + sfx + " to i64\n" + v0slot +
				"  store i64 %rvpi" + sfx + ", ptr %qv0" + sfx + ", align 8\n" + settle
	}
	// A nullable-scalar result: payload bits → v0, presence → v1 (ADR-01065).
	if isNullableScalar(retTy) {
		base := retTy.withoutNullable()
		agg := nullableScalarStorageIR(retTy)
		_, baseStore := thenStoreResultIR(base, sfx)
		// baseStore converts `%rv<sfx>` (a bare T) into v0 and settles; feed it
		// the extracted payload under that name and add the presence word.
		return func(callExpr string) string {
				return "  %rvagg" + sfx + " = " + callExpr + "\n" +
					"  %rvpr" + sfx + " = extractvalue " + agg + " %rvagg" + sfx + ", 0\n" +
					"  %rv" + sfx + " = extractvalue " + agg + " %rvagg" + sfx + ", 1\n"
			},
			"  %rvpri" + sfx + " = zext i1 %rvpr" + sfx + " to i64\n" +
				"  %qv1n" + sfx + " = getelementptr " + promiseStructIR + ", ptr %q, i32 0, i32 3\n" +
				"  store i64 %rvpri" + sfx + ", ptr %qv1n" + sfx + ", align 8\n" + baseStore
	}
	switch retTy.IR {
	case "void", "":
		return func(callExpr string) string { return "  " + callExpr + "\n" },
			v0slot + "  store i64 0, ptr %qv0" + sfx + ", align 8\n" + settle
	case "i64":
		return func(callExpr string) string { return "  %rv" + sfx + " = " + callExpr + "\n" },
			v0slot + "  store i64 %rv" + sfx + ", ptr %qv0" + sfx + ", align 8\n" + settle
	case "ptr":
		return func(callExpr string) string { return "  %rv" + sfx + " = " + callExpr + "\n" },
			"  %rvb" + sfx + " = ptrtoint ptr %rv" + sfx + " to i64\n" + v0slot +
				"  store i64 %rvb" + sfx + ", ptr %qv0" + sfx + ", align 8\n" + settle
	case "double":
		return func(callExpr string) string { return "  %rv" + sfx + " = " + callExpr + "\n" },
			"  %rvb" + sfx + " = bitcast double %rv" + sfx + " to i64\n" + v0slot +
				"  store i64 %rvb" + sfx + ", ptr %qv0" + sfx + ", align 8\n" + settle
	default:
		// small ints (i1/i8/i16/i32)
		return func(callExpr string) string { return "  %rv" + sfx + " = " + callExpr + "\n" },
			"  %rvb" + sfx + " = zext " + retTy.IR + " %rv" + sfx + " to i64\n" + v0slot +
				"  store i64 %rvb" + sfx + ", ptr %qv0" + sfx + ", align 8\n" + settle
	}
}

// thenPassThroughIR copies the source promise's settlement (res, v0, v1) straight
// into Q — used by finally and by a missing fulfillment callback. sfx keeps the
// temporaries unique across the two blocks that pass through.
func thenPassThroughIR(sfx string) string {
	return "  %pv0" + sfx + " = load i64, ptr %v0_p, align 8\n" +
		"  %pv1_p" + sfx + " = getelementptr " + promiseStructIR + ", ptr %p, i32 0, i32 3\n" +
		"  %pv1" + sfx + " = load i64, ptr %pv1_p" + sfx + ", align 8\n" +
		"  %qv0pt" + sfx + " = getelementptr " + promiseStructIR + ", ptr %q, i32 0, i32 2\n" +
		"  store i64 %pv0" + sfx + ", ptr %qv0pt" + sfx + ", align 8\n" +
		"  %qv1pt" + sfx + " = getelementptr " + promiseStructIR + ", ptr %q, i32 0, i32 3\n" +
		"  store i64 %pv1" + sfx + ", ptr %qv1pt" + sfx + ", align 8\n" +
		"  %qrespt" + sfx + " = getelementptr " + promiseStructIR + ", ptr %q, i32 0, i32 0\n" +
		"  store i64 %res, ptr %qrespt" + sfx + ", align 8\n"
}

// emitThenRunner emits a reaction runner and returns its symbol; call sites
// with the same types share one. argTy is the source
// promise's value type (the callback argument); retTy is the callback's return
// type (what the returned promise Q settles to). The runner reads the source's
// settled state, invokes the right callback, settles Q with its result (or
// passes the source settlement through for finally / a missing callback), then
// drains Q's own reactions so a chained `.then` fires.
func (e *Emitter) emitThenRunner(argTy, retTy Type, adopt int, finAwait bool) string {
	valLoad, argIR := thenValLoadIR(argTy)
	produceF, storeSettleF := thenStoreResultIR(retTy, "f")
	produceR, storeSettleR := thenStoreResultIR(retTy, "r")

	// Fulfillment callback call expression (onF). A void source passes
	// undefined (its `any` box): a callback of no parameters ignores it, one
	// of other types is adapted from `any` (emitFulfillCallback).
	loadFClosure := "  %ffp_p = getelementptr { ptr, ptr }, ptr %onF, i32 0, i32 0\n" +
		"  %ffp = load ptr, ptr %ffp_p, align 8\n" +
		"  %fep_p = getelementptr { ptr, ptr }, ptr %onF, i32 0, i32 1\n" +
		"  %fep = load ptr, ptr %fep_p, align 8\n"
	var fCall string
	switch {
	case argIR == "":
		fCall = loadFClosure + produceF(fmt.Sprintf("call %s %%ffp(ptr %%fep, i64 %d)", thenCallRetIR(retTy), nbUndefined))
	case argTy.IsArray():
		// An array callback argument is passed by the two-slot array-param ABI
		// (`ptr <header>, i64 <len>` — bindArrayParam): v0 holds the header
		// of the array that settled the source promise (storePromiseValue), so
		// the callback shares it.
		arrValLoad := "  %ahdr = inttoptr i64 %v0 to ptr\n" +
			"  %alenp = getelementptr { ptr, i64 }, ptr %ahdr, i32 0, i32 1\n" +
			"  %alen = load i64, ptr %alenp, align 8\n"
		fCall = arrValLoad + loadFClosure + produceF(fmt.Sprintf("call %s %%ffp(ptr %%fep, ptr %%ahdr, i64 %%alen)", thenCallRetIR(retTy)))
	default:
		fCall = valLoad + loadFClosure + produceF(fmt.Sprintf("call %s %%ffp(ptr %%fep, %s %%val)", thenCallRetIR(retTy), argIR))
	}

	// Rejection callback call (onR) — the reason is a caught value { i8 tag, i64
	// payload } rebuilt from v1 (tag) + v0 (payload) (TDD-00207), so the handler
	// param `e` behaves exactly like a `catch (e)` binding.
	rCall := "  %errtag = trunc i64 %v1 to i8\n" +
		"  %erragg0 = insertvalue { i8, i64 } undef, i8 %errtag, 0\n" +
		"  %erragg = insertvalue { i8, i64 } %erragg0, i64 %v0, 1\n" +
		"  %rfp_p = getelementptr { ptr, ptr }, ptr %onR, i32 0, i32 0\n" +
		"  %rfp = load ptr, ptr %rfp_p, align 8\n" +
		"  %rep_p = getelementptr { ptr, ptr }, ptr %onR, i32 0, i32 1\n" +
		"  %rep = load ptr, ptr %rep_p, align 8\n" +
		produceR(fmt.Sprintf("call %s %%rfp(ptr %%rep, { i8, i64 } %%erragg)", thenCallRetIR(retTy)))

	// Pass-through blocks (finally, and a missing fulfillment callback).
	// finally's result settles two microtasks after its callback, as Node's
	// does: its thenFinally resolves Q with PromiseResolve(onFinally()).then(
	// () => value) — a reaction, then the adoption of that promise.
	e.ensurePromiseDeferSettle()
	finCall := "  call void (ptr) %finfp(ptr %finep)\n"
	if finAwait {
		e.ensurePromiseFinallyWait()
		finCall = "  %finr = call ptr (ptr) %finfp(ptr %finep)\n" +
			"  call void @__kml_promise_finally_wait(ptr %finr, ptr %q, i64 %res, i64 %v0, i64 %v1)\n  ret void\n"
	}
	passThroughD := "  %qv0fd = getelementptr " + promiseStructIR + ", ptr %q, i32 0, i32 2\n" +
		"  store i64 %v0, ptr %qv0fd, align 8\n" +
		"  %qv1fd = getelementptr " + promiseStructIR + ", ptr %q, i32 0, i32 3\n" +
		"  store i64 %v1, ptr %qv1fd, align 8\n" +
		"  call void @__kml_promise_defer_settle(ptr %q, i64 %res, i64 2)\n  ret void\n"
	passThroughF := thenPassThroughIR("pf")
	// Propagate a rejection (no onR): Q rejects with the same reason — both the
	// payload (v0) and the tag (v1) carry over (TDD-00207).
	propReject := `  %qv0_pr = getelementptr ` + promiseStructIR + `, ptr %q, i32 0, i32 2
  store i64 %v0, ptr %qv0_pr, align 8
  %qv1_pr = getelementptr ` + promiseStructIR + `, ptr %q, i32 0, i32 3
  store i64 %v1, ptr %qv1_pr, align 8
  %qres_pr = getelementptr ` + promiseStructIR + `, ptr %q, i32 0, i32 0
  store i64 2, ptr %qres_pr, align 8
`
	drainRet := "  call void @__kml_promise_drain_reactions(ptr %q)\n  ret void\n"
	// The callback branches either settle Q with the returned value and drain, or
	// — when the callback returned a promise — hand Q to the adoption job and
	// leave it pending (draining a pending promise would fire its reactions early).
	dynRet := adopt == 0 && retTy.IsDynamic() && !retTy.IsArray() && StructFieldIR(retTy) == "i64"
	if dynRet {
		e.ensurePromiseResolveAny()
	}
	cbTail := func(storeSettle, sfx string) string {
		if dynRet {
			// A dynamic result may hold a promise: resolve Q with it (adopting).
			return "  call void @__kml_promise_resolve_any(ptr %q, i64 %rv" + sfx + ")\n  ret void\n"
		}
		if adopt == 0 {
			return storeSettle + drainRet
		}
		h := "%rv" + sfx
		pre := ""
		if adopt == 2 {
			pre = "  %rvp" + sfx + " = call ptr @__kml_fetch_slot_to_promise(ptr %rv" + sfx + ")\n"
			h = "%rvp" + sfx
		}
		return pre + "  call void @__kml_promise_adopt(ptr %q, ptr " + h + ")\n  ret void\n"
	}

	// Named by its text (the name left out), as defineContentNamed names.
	const self = "@__kml_then_run"
	text := fmt.Sprintf(`
define void %s(ptr %%env) {
entry:
  %%p_p = getelementptr { ptr, ptr, ptr, ptr, ptr }, ptr %%env, i32 0, i32 0
  %%p = load ptr, ptr %%p_p, align 8
  %%onF_p = getelementptr { ptr, ptr, ptr, ptr, ptr }, ptr %%env, i32 0, i32 1
  %%onF = load ptr, ptr %%onF_p, align 8
  %%onR_p = getelementptr { ptr, ptr, ptr, ptr, ptr }, ptr %%env, i32 0, i32 2
  %%onR = load ptr, ptr %%onR_p, align 8
  %%onFin_p = getelementptr { ptr, ptr, ptr, ptr, ptr }, ptr %%env, i32 0, i32 3
  %%onFin = load ptr, ptr %%onFin_p, align 8
  %%q_p = getelementptr { ptr, ptr, ptr, ptr, ptr }, ptr %%env, i32 0, i32 4
  %%q = load ptr, ptr %%q_p, align 8
  %%res_p = getelementptr %s, ptr %%p, i32 0, i32 0
  %%res = load i64, ptr %%res_p, align 8
  %%v0_p = getelementptr %s, ptr %%p, i32 0, i32 2
  %%v0 = load i64, ptr %%v0_p, align 8
  %%v1_p = getelementptr %s, ptr %%p, i32 0, i32 3
  %%v1 = load i64, ptr %%v1_p, align 8
  %%hasFin = icmp ne ptr %%onFin, null
  br i1 %%hasFin, label %%dofin, label %%branch
dofin:
  %%finfp_p = getelementptr { ptr, ptr }, ptr %%onFin, i32 0, i32 0
  %%finfp = load ptr, ptr %%finfp_p, align 8
  %%finep_p = getelementptr { ptr, ptr }, ptr %%onFin, i32 0, i32 1
  %%finep = load ptr, ptr %%finep_p, align 8
%s%s
branch:
  %%isful = icmp eq i64 %%res, 1
  br i1 %%isful, label %%ful, label %%rej
ful:
  %%hasF = icmp ne ptr %%onF, null
  br i1 %%hasF, label %%callF, label %%passF
callF:
%s%s
passF:
%s%s
rej:
  %%hasR = icmp ne ptr %%onR, null
  br i1 %%hasR, label %%callR, label %%propR
callR:
%s%s
propR:
%s%s
}`, self, promiseStructIR, promiseStructIR, promiseStructIR,
		finCall, passThroughD,
		fCall, cbTail(storeSettleF, "f"),
		passThroughF, drainRet,
		rCall, cbTail(storeSettleR, "r"),
		propReject, drainRet)
	name := contentSymbol(self+".", text)
	if e.contentDefined == nil {
		e.contentDefined = map[string]bool{}
	}
	if !e.contentDefined[name] {
		e.contentDefined[name] = true
		e.emitGlobal(strings.Replace(text, "define void "+self+"(", "define linkonce_odr hidden void "+name+"(", 1))
	}
	return name
}

// thenCallRetIR gives the LLVM return-type token for a `call` to a then/catch
// callback: the empty string maps to "void".
func thenCallRetIR(retTy Type) string {
	if retTy.IR == "" || retTy.IR == "void" {
		return "void"
	}
	if retTy.IsArray() {
		// An array is returned by the header-pointer ABI (`ret ptr`, TDD-00213
		// Stage 3).
		return "ptr"
	}
	if isNullableScalar(retTy) {
		return nullableScalarStorageIR(retTy)
	}
	return retTy.IR
}

// ensurePromiseDeferSettle declares @__kml_promise_defer_settle(q, state,
// hops): settle q with state after hops more microtasks.
func (e *Emitter) ensurePromiseDeferSettle() {
	if e.fnDecls["__kml_promise_defer_settle"] {
		return
	}
	e.fnDecls["__kml_promise_defer_settle"] = true
	e.ensurePromiseSettle()
	e.ensureMicrotasks()
	e.ensureMalloc()
	e.ensureExceptionHelpers()
	e.emitGlobal(`declare void @__kml_promise_defer_settle(ptr, i64, i64)
declare void @__kml_promise_defer_step(ptr)
declare void @__kml_then_guard(ptr)`)
}

// ensurePromiseFinallyWait declares @__kml_promise_finally_wait(r, q, res,
// v0, v1): once onFinally's promise r settles, q takes r's rejection, or
// else the source's settlement (res, v0, v1) — one microtask after r's
// reaction, as the spec's adoption of `r.then(() => value)` does.
func (e *Emitter) ensurePromiseFinallyWait() {
	if e.fnDecls["__kml_promise_finally_wait"] {
		return
	}
	e.fnDecls["__kml_promise_finally_wait"] = true
	e.ensurePromiseDeferSettle()
	e.ensurePromiseAdopt() // @__kml_promise_attach
	e.emitGlobal(`declare void @__kml_promise_finally_wait(ptr, ptr, i64, i64, i64)`)
}

// thenPathsDiffer reports whether a then/catch's fulfilled and rejected paths
// settle to values Q cannot hold in one representation. A promise-returning
// handler (flattened by adoption) keeps its own handling.
func thenPathsDiffer(a, b Type) bool {
	if a.IsPromise() || b.IsPromise() {
		return false
	}
	if a.IsDynamic() && b.IsDynamic() {
		return false
	}
	return !reflect.DeepEqual(a, b) && !sameShortCircuitType(a, b)
}

// thenIdentityCallback is `v => v`, the fulfilled path of a `.catch`.
func thenIdentityCallback(pos ast.Pos) ast.Expression {
	return ast.NewArrowFunction([]ast.Param{{Name: "__kml_v"}}, nil, ast.NewIdentifier("__kml_v", pos), nil, pos)
}

// closureReturningAny is the handler closure v adapted to box its result.
func (e *Emitter) closureReturningAny(v Value) (Value, bool) {
	if !v.Ty.IsFunc() {
		return Value{}, false
	}
	tgt := FuncType(v.Ty.FuncParams, TypeAny)
	if a, ok := e.emitClosureAdapter(v, tgt); ok {
		return a, true
	}
	return e.emitBoxedClosureAs(v, tgt)
}

// thenCallbackFromAny converts a `.then`/`.catch` handler held in an `any`
// to a closure taking param: a value that is not a function at run time is
// the null callback, the pass-through PerformPromiseThen makes of a
// non-callable handler.
func (e *Emitter) thenCallbackFromAny(v Value, param Type) (Value, bool) {
	if !isUnconstrainedDynamic(v.Ty) {
		return Value{}, false
	}
	c, ok := e.emitAnyToClosure(v, FuncType([]Type{param}, TypeAny))
	if !ok {
		return Value{}, false
	}
	tag, _ := e.emitUnboxTagPayload(v)
	isRef, isDyn, isFn, out := e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isRef, tag, kmlTagFuncRef))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isDyn, tag, kmlTagDynFunc))
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", isFn, isRef, isDyn))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr null", out, isFn, c.Ref))
	return Value{Ref: out, Ty: c.Ty}, true
}

// emittedCallbackReturn is the return type of a callback as it was
// compiled (a closure's own FuncRetType, void when it returns nothing):
// the runner must call it by that ABI.
func emittedCallbackReturn(v Value) (Type, bool) {
	if !v.Ty.IsFunc() || v.Ty.FuncRetType == nil {
		return Type{}, false
	}
	if t := *v.Ty.FuncRetType; t.IR != "" {
		return t, true
	}
	return TypeVoid, true
}

// anyAnnotatedFirstParam reports an arrow or function expression whose
// first parameter is written `: any`.
func anyAnnotatedFirstParam(x ast.Expression) bool {
	var ps []ast.Param
	switch f := x.(type) {
	case *ast.ArrowFunction:
		ps = f.Params
	case *ast.FunctionExpression:
		ps = f.Params
	}
	return len(ps) > 0 && !ps[0].Rest && ps[0].Type != nil && ps[0].Type.Name == "any"
}
