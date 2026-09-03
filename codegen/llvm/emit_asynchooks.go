// emit_asynchooks.go — the async_hooks module's AsyncLocalStorage<T> (TDD-00168).
//
// `new AsyncLocalStorage<T>()` allocates a per-instance record; the instance
// methods operate on the current async context-frame list (runtime_asynchooks.go):
//   - run(store, cb, ...args): push a frame, invoke cb, restore — the store is
//     visible to getStore() for cb's whole dynamic extent, across `await`s.
//   - getStore(): the nearest store for this instance (T's zero value / null
//     when none — this compiler's undefined stand-in; a disabled instance too).
//   - exit(cb, ...args): run cb with this instance's store shadowed to undefined.
//   - enterWith(store): set the store for the rest of the current execution.
//   - disable(): getStore() henceforth returns undefined.
//
// Stage 4 adds static AsyncLocalStorage.bind(fn) (a wrapper of fn's own
// signature that reinstalls the captured context, via a monomorphic trampoline)
// and AsyncResource (capture-at-construction + runInAsyncScope). snapshot()
// stays a clean rejection — its runner is fully generic (arbitrary parameter/
// return types per call site), needing generic first-class closures this
// compiler lacks. The low-level createHook / async-id lifecycle is out of scope
// (a faithful impossibility for a compiled runtime).
package llvm

import (
	"fmt"
	"strings"

	"KlainMainLang/ast"
)

// wrapTimerClosureWithAsyncCtx wraps a timer callback closure so it carries the
// *current* async context to its deferred fire (TDD-00168 Stage 3). It captures
// the context head now (at schedule time) into a { origClosure, capturedCtx }
// env and returns a fresh { @__kml_als_timer_tramp, env } closure header that
// installs the captured context, invokes the original, and restores — so a
// `setTimeout(cb)` scheduled inside `als.run(...)` sees the store when `cb`
// fires. Only applied when the program constructs an AsyncLocalStorage
// (e.programUsesALS); otherwise timers are untouched.
func (e *Emitter) wrapTimerClosureWithAsyncCtx(closurePtr string) string {
	e.ensureAsyncCtxAccessors()
	cc := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_als_ctx_get()", cc))
	env := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", env))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", closurePtr, env))
	ccp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 1", ccp, env))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", cc, ccp))
	hdr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", hdr))
	e.emitInstr(fmt.Sprintf("store ptr @__kml_als_timer_tramp, ptr %s, align 8", hdr))
	hep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 1", hep, hdr))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", env, hep))
	return hdr
}

// programUsesAsyncLocalStorage reports whether the program constructs an
// AsyncLocalStorage anywhere — a whole-program pre-scan (mirroring
// programUsesHTTPUpgrade) run before Pass 2, so timer callbacks can be wrapped
// to carry async context (TDD-00168 Stage 3) even when the `new` sits inside a
// function body emitted before the top-level construction. Conservative: a
// false positive only costs a little dead IR in the timer path, never
// correctness.
func programUsesAsyncLocalStorage(prog *ast.Program) bool {
	found := false
	for _, s := range prog.Body {
		walkStmtForALS(s, &found)
		if found {
			return true
		}
	}
	return false
}

func walkBlockForALS(b *ast.BlockStatement, found *bool) {
	if b == nil || *found {
		return
	}
	for _, s := range b.Body {
		walkStmtForALS(s, found)
		if *found {
			return
		}
	}
}

func walkStmtForALS(s ast.Statement, found *bool) {
	if *found || s == nil {
		return
	}
	switch n := s.(type) {
	case *ast.BlockStatement:
		walkBlockForALS(n, found)
	case *ast.ExpressionStatement:
		walkExprForALS(n.Expr, found)
	case *ast.VarDeclaration:
		walkExprForALS(n.Init, found)
	case *ast.VarDeclarationList:
		for _, d := range n.Decls {
			walkStmtForALS(d, found)
		}
	case *ast.ReturnStatement:
		walkExprForALS(n.Value, found)
	case *ast.ThrowStatement:
		walkExprForALS(n.Argument, found)
	case *ast.IfStatement:
		walkExprForALS(n.Test, found)
		walkStmtForALS(n.Consequent, found)
		walkStmtForALS(n.Alternate, found)
	case *ast.ForStatement:
		walkStmtForALS(n.Init, found)
		walkExprForALS(n.Test, found)
		for _, u := range n.Update {
			walkExprForALS(u, found)
		}
		walkStmtForALS(n.Body, found)
	case *ast.ForOfStatement:
		walkExprForALS(n.Iterable, found)
		walkStmtForALS(n.Body, found)
	case *ast.ForInStatement:
		walkStmtForALS(n.Body, found)
	case *ast.WhileStatement:
		walkExprForALS(n.Test, found)
		walkStmtForALS(n.Body, found)
	case *ast.DoWhileStatement:
		walkStmtForALS(n.Body, found)
		walkExprForALS(n.Test, found)
	case *ast.SwitchStatement:
		walkExprForALS(n.Discriminant, found)
		for _, c := range n.Cases {
			walkExprForALS(c.Test, found)
			for _, cs := range c.Body {
				walkStmtForALS(cs, found)
			}
		}
	case *ast.TryStatement:
		walkBlockForALS(n.Body, found)
		if n.Catch != nil {
			walkBlockForALS(n.Catch.Body, found)
		}
		walkBlockForALS(n.Finally, found)
	case *ast.LabeledStatement:
		walkStmtForALS(n.Body, found)
	case *ast.FunctionDeclaration:
		walkBlockForALS(n.Body, found)
	case *ast.ExportDeclaration:
		walkStmtForALS(n.Decl, found)
	}
}

func walkExprForALS(ex ast.Expression, found *bool) {
	if *found || ex == nil {
		return
	}
	switch n := ex.(type) {
	case *ast.NewExpression:
		if n.ClassName == "AsyncLocalStorage" {
			*found = true
			return
		}
		for _, a := range n.Args {
			walkExprForALS(a, found)
		}
	case *ast.CallExpression:
		walkExprForALS(n.Callee, found)
		for _, a := range n.Args {
			walkExprForALS(a, found)
		}
	case *ast.MemberExpression:
		walkExprForALS(n.Object, found)
	case *ast.IndexExpression:
		walkExprForALS(n.Object, found)
		walkExprForALS(n.Index, found)
	case *ast.BinaryExpression:
		walkExprForALS(n.Left, found)
		walkExprForALS(n.Right, found)
	case *ast.AssignmentExpression:
		walkExprForALS(n.Left, found)
		walkExprForALS(n.Right, found)
	case *ast.ConditionalExpression:
		walkExprForALS(n.Test, found)
		walkExprForALS(n.Consequent, found)
		walkExprForALS(n.Alternate, found)
	case *ast.SequenceExpression:
		for _, e := range n.Exprs {
			walkExprForALS(e, found)
		}
	case *ast.UnaryExpression:
		walkExprForALS(n.Arg, found)
	case *ast.UpdateExpression:
		walkExprForALS(n.Arg, found)
	case *ast.SpreadElement:
		walkExprForALS(n.Arg, found)
	case *ast.AwaitExpression:
		walkExprForALS(n.Argument, found)
	case *ast.YieldExpression:
		walkExprForALS(n.Argument, found)
	case *ast.ArrayLiteral:
		for _, e := range n.Elements {
			walkExprForALS(e, found)
		}
	case *ast.ObjectLiteral:
		for _, p := range n.Properties {
			walkExprForALS(p.KeyExpr, found)
			walkExprForALS(p.Value, found)
		}
	case *ast.TemplateLiteral:
		for _, e := range n.Exprs {
			walkExprForALS(e, found)
		}
	case *ast.ArrowFunction:
		walkExprForALS(n.Body, found)
		walkBlockForALS(n.Block, found)
	case *ast.FunctionExpression:
		walkBlockForALS(n.Body, found)
	}
}

// emitNewAsyncLocalStorage implements `new AsyncLocalStorage<T>()`.
func (e *Emitter) emitNewAsyncLocalStorage(ex *ast.NewExpression) (Value, error) {
	e.ensureAsyncLocalStorageRuntime()
	e.ensureNanBox()
	elem := TypeAny
	if len(ex.TypeArgs) == 1 && ex.TypeArgs[0] != nil {
		elem = e.resolveType(ex.TypeArgs[0])
	}
	if len(ex.Args) != 0 {
		return Value{}, fmt.Errorf("%d:%d: new AsyncLocalStorage() takes no arguments", ex.GetPos().Line, ex.GetPos().Col)
	}
	rec := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_als_new_record()", rec))
	return Value{Ref: rec, Ty: AsyncLocalStorageType(elem)}, nil
}

// alsInstanceId loads an AsyncLocalStorage handle's instance id (record field 0).
func (e *Emitter) alsInstanceId(recRef string) string {
	id := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", id, recRef))
	return id
}

// emitAsyncLocalStorageMethod dispatches an AsyncLocalStorage instance method.
func (e *Emitter) emitAsyncLocalStorageMethod(objExpr ast.Expression, method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	objVal, err := e.emitExpr(objExpr)
	if err != nil {
		return Value{}, err
	}
	elemT := TypeAny
	if objVal.Ty.ElemType != nil {
		elemT = *objVal.Ty.ElemType
	}
	id := e.alsInstanceId(objVal.Ref)

	switch method {
	case "getStore":
		if len(args) != 0 {
			return Value{}, fmt.Errorf("%d:%d: getStore() takes no arguments", pos.Line, pos.Col)
		}
		// undefined when the instance is disabled; else the nearest frame's value.
		dp := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr { i64, i64 }, ptr %s, i32 0, i32 1", dp, objVal.Ref))
		dis := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", dis, dp))
		disB := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", disB, dis))
		look := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_als_lookup(i64 %s)", look, id))
		word := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %d, i64 %s", word, disB, nbUndefined, look))
		return e.coerce(Value{Ref: word, Ty: TypeAny}, elemT), nil

	case "run", "exit":
		return e.emitAlsRunExit(objVal, id, elemT, method, args, pos)

	case "enterWith":
		if len(args) != 1 {
			return Value{}, fmt.Errorf("%d:%d: enterWith(store) takes one argument", pos.Line, pos.Col)
		}
		w, err := e.emitAlsBoxStore(args[0], elemT)
		if err != nil {
			return Value{}, err
		}
		discard := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_als_push(i64 %s, i64 %s)", discard, id, w))
		return Value{Ty: TypeVoid}, nil

	case "disable":
		if len(args) != 0 {
			return Value{}, fmt.Errorf("%d:%d: disable() takes no arguments", pos.Line, pos.Col)
		}
		dp := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr { i64, i64 }, ptr %s, i32 0, i32 1", dp, objVal.Ref))
		e.emitInstr(fmt.Sprintf("store i64 1, ptr %s, align 8", dp))
		return Value{Ty: TypeVoid}, nil

	case "bind", "snapshot":
		return Value{}, fmt.Errorf("%d:%d: AsyncLocalStorage.%s is the static context-capture form and is a staged follow-up — the instance run/getStore/exit/enterWith/disable surface works", pos.Line, pos.Col, method)
	}
	return Value{}, fmt.Errorf("%d:%d: AsyncLocalStorage has no method '%s' (run, getStore, exit, enterWith, disable)", pos.Line, pos.Col, method)
}

// emitAlsBoxStore evaluates a store expression, coerces it to the instance's
// element type, and NaN-boxes it into a single `any` word for a frame slot.
func (e *Emitter) emitAlsBoxStore(storeExpr ast.Expression, elemT Type) (string, error) {
	sv, err := e.emitExpr(storeExpr)
	if err != nil {
		return "", err
	}
	boxed, err := e.emitBoxValue(e.coerce(sv, elemT))
	if err != nil {
		return "", err
	}
	return boxed.Ref, nil
}

// emitAlsRunExit implements run(store, cb, ...args) and exit(cb, ...args): push
// a frame (the store for run, an undefined shadow for exit), invoke the
// callback with the trailing args, then restore the previous head. Returns the
// callback's own result.
func (e *Emitter) emitAlsRunExit(objVal Value, id string, elemT Type, method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	var word string
	var cbExpr ast.Expression
	var callArgs []ast.Expression
	if method == "run" {
		if len(args) < 2 {
			return Value{}, fmt.Errorf("%d:%d: run(store, callback, ...args) takes at least a store and a callback", pos.Line, pos.Col)
		}
		w, err := e.emitAlsBoxStore(args[0], elemT)
		if err != nil {
			return Value{}, err
		}
		word = w
		cbExpr = args[1]
		callArgs = args[2:]
	} else { // exit
		if len(args) < 1 {
			return Value{}, fmt.Errorf("%d:%d: exit(callback, ...args) takes at least a callback", pos.Line, pos.Col)
		}
		word = fmt.Sprintf("%d", nbUndefined)
		cbExpr = args[0]
		callArgs = args[1:]
	}

	old := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_als_push(i64 %s, i64 %s)", old, id, word))

	cbVal, err := e.emitExpr(cbExpr)
	if err != nil {
		return Value{}, err
	}
	if !cbVal.Ty.IsFunc {
		return Value{}, fmt.Errorf("%d:%d: AsyncLocalStorage.%s's callback must be a function value", pos.Line, pos.Col, method)
	}
	result, err := e.emitClosureCallByPtr(cbVal.Ref, cbVal.Ty, callArgs, pos)
	if err != nil {
		return Value{}, err
	}

	// Restore the previous head (pop the frame). V1 does the restore on the
	// normal-return path only; a callback that throws leaves its frame in place
	// (a documented limitation — exception-safe restore is a follow-up).
	e.emitInstr(fmt.Sprintf("call void @__kml_als_ctx_set(ptr %s)", old))
	return result, nil
}

// --- AsyncResource + static bind/snapshot (TDD-00168 Stage 4) ---

// emitNewAsyncResource implements `new AsyncResource(name?, opts?)`: it captures
// the current async context head into a { ptr capturedCtx } record. The name and
// options are accepted and ignored (labels/ids for the low-level tracing API,
// which is out of scope) — only the context capture is modeled.
func (e *Emitter) emitNewAsyncResource(ex *ast.NewExpression) (Value, error) {
	e.ensureAsyncCtxAccessors()
	if len(ex.Args) > 2 {
		return Value{}, fmt.Errorf("%d:%d: new AsyncResource(name?, options?) takes at most 2 arguments", ex.GetPos().Line, ex.GetPos().Col)
	}
	for _, a := range ex.Args { // evaluate for side effects; values unused
		if _, err := e.emitExpr(a); err != nil {
			return Value{}, err
		}
	}
	cc := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_als_ctx_get()", cc))
	rec := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 8)", rec))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", cc, rec))
	return Value{Ref: rec, Ty: AsyncResourceType()}, nil
}

// emitAsyncResourceMethod dispatches an AsyncResource instance method —
// runInAsyncScope(fn, thisArg?, ...args): install the captured context, invoke
// fn with the trailing args, restore, and return fn's result. thisArg is
// accepted but its this-binding is a V1 limitation (closures carry no dynamic
// this here); the common `runInAsyncScope(() => …)` / `(fn, thisArg)` forms work.
func (e *Emitter) emitAsyncResourceMethod(objExpr ast.Expression, method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	objVal, err := e.emitExpr(objExpr)
	if err != nil {
		return Value{}, err
	}
	if method != "runInAsyncScope" {
		return Value{}, fmt.Errorf("%d:%d: AsyncResource has no method '%s' (runInAsyncScope) — emitDestroy/asyncId/triggerAsyncId are out of scope (no async-id lifecycle)", pos.Line, pos.Col, method)
	}
	if len(args) < 1 {
		return Value{}, fmt.Errorf("%d:%d: runInAsyncScope(fn, thisArg?, ...args) needs a callback", pos.Line, pos.Col)
	}
	cc := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", cc, objVal.Ref))
	fnVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	if !fnVal.Ty.IsFunc {
		return Value{}, fmt.Errorf("%d:%d: runInAsyncScope's first argument must be a function", pos.Line, pos.Col)
	}
	var callArgs []ast.Expression
	if len(args) > 2 { // args[1] is thisArg
		callArgs = args[2:]
	}
	saved := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_als_ctx_get()", saved))
	e.emitInstr(fmt.Sprintf("call void @__kml_als_ctx_set(ptr %s)", cc))
	result, err := e.emitClosureCallByPtr(fnVal.Ref, fnVal.Ty, callArgs, pos)
	if err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("call void @__kml_als_ctx_set(ptr %s)", saved))
	return result, nil
}

// emitAsyncLocalStorageStatic dispatches the static AsyncLocalStorage.bind /
// .snapshot forms (the receiver is the bare `AsyncLocalStorage` identifier).
func (e *Emitter) emitAsyncLocalStorageStatic(method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	switch method {
	case "bind":
		return e.emitAsyncLocalStorageBind(args, pos)
	case "snapshot":
		// snapshot() returns a fully generic runner `<R>(fn: (...a) => R, ...a) => R`
		// — a first-class closure whose parameter and return types are only known
		// at each later call site. This compiler has no generic first-class
		// closures (a monomorphic wrapper would have to fix R/args at snapshot()
		// time, which the API forbids), so it is a clean rejection distinct from
		// the ALS feature itself. bind(fn) — whose wrapper takes fn's already-known
		// signature — is supported.
		return Value{}, fmt.Errorf("%d:%d: AsyncLocalStorage.snapshot() needs a generic first-class closure (its runner's parameter/return types vary per call site), which this compiler doesn't have; AsyncLocalStorage.bind(fn) and AsyncResource cover the fixed-signature cases", pos.Line, pos.Col)
	}
	return Value{}, fmt.Errorf("%d:%d: AsyncLocalStorage has no static method '%s' (bind, snapshot)", pos.Line, pos.Col, method)
}

// emitAsyncLocalStorageBind implements static AsyncLocalStorage.bind(fn): it
// captures the current async context now and returns a wrapper closure of fn's
// own type that installs the captured context around each call to fn. Built on
// a monomorphic trampoline (ensureAlsBindTramp) keyed by fn's signature.
func (e *Emitter) emitAsyncLocalStorageBind(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: AsyncLocalStorage.bind(fn) takes one function", pos.Line, pos.Col)
	}
	e.ensureAsyncCtxAccessors()
	fnVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	if !fnVal.Ty.IsFunc {
		return Value{}, fmt.Errorf("%d:%d: AsyncLocalStorage.bind(fn)'s argument must be a function (Node throws ERR_INVALID_ARG_TYPE at runtime; here a non-function is a compile-time type error)", pos.Line, pos.Col)
	}
	trampName, err := e.ensureAlsBindTramp(fnVal.Ty, pos)
	if err != nil {
		return Value{}, err
	}
	// env = { ptr origClosure, ptr capturedCtx }
	cc := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_als_ctx_get()", cc))
	env := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", env))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", fnVal.Ref, env))
	ccp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 1", ccp, env))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", cc, ccp))
	// header = { trampoline, env } — a closure value of fn's own type.
	hdr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", hdr))
	e.emitInstr(fmt.Sprintf("store ptr @%s, ptr %s, align 8", trampName, hdr))
	hep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 1", hep, hdr))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", env, hep))
	return Value{Ref: hdr, Ty: fnVal.Ty}, nil
}

// ensureAlsBindTramp emits (once per distinct signature) the monomorphic
// trampoline behind AsyncLocalStorage.bind for a closure of type fnTy. The
// trampoline has fnTy's exact call ABI — `<ret> (ptr env, params...)` — extracts
// the original closure and captured context from env, installs the context,
// forwards the params to the original, restores, and returns its result. V1
// supports scalar/pointer parameters and returns (an array/rest parameter or an
// array return is rejected — the (ptr,i64) decomposition isn't threaded here).
func (e *Emitter) ensureAlsBindTramp(fnTy Type, pos ast.Pos) (string, error) {
	retIR := "void"
	if fnTy.FuncRetType != nil {
		retIR = fnTy.FuncRetType.IR
	}
	if retIR == "" {
		retIR = "void"
	}
	if fnTy.FuncHasRest {
		return "", fmt.Errorf("%d:%d: AsyncLocalStorage.bind of a rest-parameter function is not supported (V1)", pos.Line, pos.Col)
	}
	key := retIR
	for _, p := range fnTy.FuncParams {
		if p.IsArray || p.IR == "" || p.IR == "void" {
			return "", fmt.Errorf("%d:%d: AsyncLocalStorage.bind of a function with an array/void parameter is not supported (V1)", pos.Line, pos.Col)
		}
		key += "_" + p.IR
	}
	name := "__kml_als_bind_tramp_" + llvmSafeSymbol(key)
	if e.alsBindTramps[key] {
		return name, nil
	}
	e.alsBindTramps[key] = true

	// Parameter decls and forwarded operands.
	params := []string{"ptr %env"}
	fwd := []string{"ptr %ep"}
	for i, p := range fnTy.FuncParams {
		params = append(params, fmt.Sprintf("%s %%p%d", p.IR, i))
		fwd = append(fwd, fmt.Sprintf("%s %%p%d", p.IR, i))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\ndefine %s @%s(%s) {\nentry:\n", retIR, name, strings.Join(params, ", "))
	b.WriteString("  %orig = load ptr, ptr %env, align 8\n")
	b.WriteString("  %ccp = getelementptr { ptr, ptr }, ptr %env, i32 0, i32 1\n")
	b.WriteString("  %cc = load ptr, ptr %ccp, align 8\n")
	b.WriteString("  %saved = call ptr @__kml_als_ctx_get()\n")
	b.WriteString("  call void @__kml_als_ctx_set(ptr %cc)\n")
	b.WriteString("  %fp = load ptr, ptr %orig, align 8\n")
	b.WriteString("  %ep_p = getelementptr { ptr, ptr }, ptr %orig, i32 0, i32 1\n")
	b.WriteString("  %ep = load ptr, ptr %ep_p, align 8\n")
	if retIR == "void" {
		fmt.Fprintf(&b, "  call void %%fp(%s)\n", strings.Join(fwd, ", "))
		b.WriteString("  call void @__kml_als_ctx_set(ptr %saved)\n")
		b.WriteString("  ret void\n}\n")
	} else {
		fmt.Fprintf(&b, "  %%r = call %s %%fp(%s)\n", retIR, strings.Join(fwd, ", "))
		b.WriteString("  call void @__kml_als_ctx_set(ptr %saved)\n")
		fmt.Fprintf(&b, "  ret %s %%r\n}\n", retIR)
	}
	e.emitGlobal(b.String())
	return name, nil
}
