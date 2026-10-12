package llvm

import (
	"KlainMainLang/ast"
	"KlainMainLang/sema"
	"fmt"
)

// emit_thenable.go — a class instance with a `then` method is a thenable:
// `await x`, `Promise.resolve(x)` and the Promise combinators adopt it as
// the spec's PromiseResolve does, through a job that calls `x.then(resolve,
// reject)` on a fresh promise.

// thenableClass is the `then` signature of t when t is an instance of a
// class declaring (or inheriting) a `then` method, or an object whose `then`
// member is a function.
func (e *Emitter) thenableClass(t Type) (FuncSig, bool) {
	if t.IsDynamic || t.IsPromise || t.Nullable {
		return FuncSig{}, false
	}
	if t.IsObject && !t.IsClass {
		// An object whose `then` member is a function (`{ then(res) {…} }`).
		if _, ft, ok := t.FieldIndex("then"); ok && ft.IsFunc && !ft.IsDynamic {
			return FuncSig{ParamTypes: ft.FuncParams, RetType: TypeVoid}, true
		}
		return FuncSig{}, false
	}
	if !t.IsClass {
		return FuncSig{}, false
	}
	info, ok := e.classes[t.ClassName]
	if !ok {
		return FuncSig{}, false
	}
	if sig, ok := info.MethodSigs["then"]; ok {
		return sig, true
	}
	if info.HostBase != nil && info.HostBase.IsPromise {
		// A class extending Promise: its constructor is not %Promise%, so
		// resolution goes through its `then` (Promise's, unless overridden
		// on the way).
		el := e.promiseSubclassValueType(t)
		for _, anc := range info.AncestorChain {
			if ai, ok := e.classes[anc]; ok {
				if sig, ok := ai.MethodSigs["then"]; ok {
					return sig, true
				}
			}
		}
		return FuncSig{ParamTypes: []Type{FuncType([]Type{el}, TypeAny), FuncType([]Type{TypeCaught}, TypeAny)}, RetType: *info.HostBase}, true
	}
	for _, anc := range info.AncestorChain {
		if ai, ok := e.classes[anc]; ok {
			if sig, ok := ai.MethodSigs["then"]; ok {
				return sig, true
			}
		}
	}
	return FuncSig{}, false
}

// thenableValueType is the type a thenable's `then` fulfills with: its
// first callback's first parameter (TypeScript's Awaited), else `any`.
func thenableValueType(sig FuncSig) Type {
	if len(sig.ParamTypes) == 0 {
		return TypeAny
	}
	cb := sig.ParamTypes[0]
	if cb.IsFunc && len(cb.FuncParams) > 0 {
		return cb.FuncParams[0]
	}
	return TypeAny
}

// emitThenableToPromise is PromiseResolve(%Promise%, v) for a thenable class
// instance v: a pending promise, settled by the resolve and reject functions
// a queued job passes to `v.then`; a throw from `then` rejects it.
func (e *Emitter) emitThenableToPromise(v Value, sig FuncSig, pos ast.Pos) (Value, error) {
	valTy := thenableValueType(sig)
	e.ensurePromiseSettle()
	e.ensureExceptionHelpers()
	e.ensureMalloc()
	p := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_task_alloc_promise()", p))
	var resolveParams []Type
	if valTy.IR != "void" && valTy.IR != "" {
		resolveParams = []Type{valTy}
	}
	resolveTy := FuncType(resolveParams, TypeVoid)
	if len(resolveParams) == 1 {
		resolveTy.IsPromiseResolver = true
	}
	rejectTy := FuncType([]Type{TypeCaught}, TypeVoid)
	res := e.bindValue(Value{Ref: e.buildBuiltinClosure(e.emitResolveThunk(valTy), p), Ty: resolveTy}, pos)
	rej := e.bindValue(Value{Ref: e.buildBuiltinClosure(e.emitRejectThunk(), p), Ty: rejectTy}, pos)
	recv := e.bindValue(v, pos)
	// queueMicrotask(() => { try { recv.then(res, rej) } catch (err) { rej(err) } })
	// A `then` declaring fewer parameters ignores the rest, as JavaScript
	// does.
	// (A class method's arity is exact; an object's `then` value is called
	// with both, as the member type of a mixed array may be another
	// element's.)
	args := []ast.Expression{res, rej}
	if n := len(sig.ParamTypes); n < len(args) && v.Ty.IsClass {
		args = args[:n]
	}
	call := ast.NewCallExpression(ast.NewMemberExpression(recv, "then", pos), args, pos)
	errName := "__kml_thenable_err"
	catch := &ast.CatchClause{Param: errName, Body: ast.NewBlockStatement([]ast.Statement{
		ast.NewExpressionStatement(ast.NewCallExpression(rej, []ast.Expression{ast.NewIdentifier(errName, pos)}, pos), pos),
	}, pos), Pos: pos}
	try := ast.NewTryStatement(ast.NewBlockStatement([]ast.Statement{ast.NewExpressionStatement(call, pos)}, pos), catch, nil, pos)
	job := ast.NewArrowFunction(nil, nil, nil, ast.NewBlockStatement([]ast.Statement{try}, pos), pos)
	wrap := &ast.Program{Body: []ast.Statement{ast.NewExpressionStatement(job, pos)}}
	if err := sema.Prepare(wrap); err != nil {
		return Value{}, err
	}
	if _, err := e.emitQueueMicrotask([]ast.Expression{wrap.Body[0].(*ast.ExpressionStatement).Expr}, pos); err != nil {
		return Value{}, err
	}
	pt := PromiseOf(valTy)
	pt.PromiseTask = true
	return Value{Ref: p, Ty: pt}, nil
}

// thenableResolveName is the internal function synthesized source calls for
// PromiseResolve(%Promise%, x): `__kml_thenable_resolve(x)` is a promise
// for x, a thenable class instance adopted through its `then`.
const thenableResolveName = "__kml_thenable_resolve"

func isThenableResolveCall(ex *ast.CallExpression) bool {
	id, ok := ex.Callee.(*ast.Identifier)
	return ok && id.Name == thenableResolveName && len(ex.Args) == 1
}

// emitThenableResolveCall emits `__kml_thenable_resolve(x)`.
func (e *Emitter) emitThenableResolveCall(ex *ast.CallExpression) (Value, error) {
	v, err := e.emitExpr(ex.Args[0])
	if err != nil {
		return Value{}, err
	}
	if v.Ty.IsPromise {
		return v, nil
	}
	if sig, ok := e.thenableClass(v.Ty); ok {
		return e.emitThenableToPromise(v, sig, ex.GetPos())
	}
	return e.emitPromiseResolve([]ast.Expression{e.bindValue(v, ex.GetPos())}, ex.GetPos(), Type{})
}

// thenableCallback is a then/catch callback returning a thenable class
// instance, wrapped to return the promise adopting it, so the reaction
// adopts its settlement as it does a returned promise's; arg otherwise.
func (e *Emitter) thenableCallback(arg ast.Expression, param Type, pos ast.Pos) (ast.Expression, error) {
	rt, ok := e.callbackReturnType(arg, param)
	if !ok {
		return arg, nil
	}
	if _, ok := e.thenableClass(rt); !ok {
		return arg, nil
	}
	fn, err := e.emitExpr(arg)
	if err != nil {
		return nil, err
	}
	if !fn.Ty.IsFunc {
		return arg, nil
	}
	bound := e.bindValue(fn, pos)
	var params []ast.Param
	var callArgs []ast.Expression
	for i := range fn.Ty.FuncParams {
		name := fmt.Sprintf("__kml_cb%d", i)
		params = append(params, ast.Param{Name: name})
		callArgs = append(callArgs, ast.NewIdentifier(name, pos))
	}
	body := ast.NewCallExpression(ast.NewIdentifier(thenableResolveName, pos), []ast.Expression{ast.NewCallExpression(bound, callArgs, pos)}, pos)
	return ast.NewArrowFunction(params, nil, body, nil, pos), nil
}

// isThenableExpr reports an expression whose value is a thenable class
// instance.
func (e *Emitter) isThenableExpr(x ast.Expression) bool {
	_, ok := e.thenableClass(e.inferExprType(x))
	return ok
}

// awaitedRet is an async function's settled value type for a returned value
// of type t: a thenable class instance's fulfillment type, else t.
func (e *Emitter) awaitedRet(t Type) Type {
	if sig, ok := e.thenableClass(t); ok {
		return thenableValueType(sig)
	}
	if t.IsDynamic && len(t.UnionMembers) > 0 {
		// `return p` of a promise in an async function settles with what p
		// settles to (Awaited): its member is p's value type, none for a
		// promise that never fulfills.
		var ms []Type
		seen := map[string]bool{}
		changed := false
		for _, m := range t.UnionMembers {
			if m.IsPromise {
				changed = true
				if m.PromiseType == nil || m.PromiseType.IR == "" || m.PromiseType.IR == "void" || m.PromiseType.IsNever {
					continue
				}
				m = *m.PromiseType
			}
			if k := sameReprKey(m); !seen[k] {
				seen[k] = true
				ms = append(ms, m)
			}
		}
		switch {
		case !changed:
		case len(ms) == 0:
			return TypeAny
		case len(ms) == 1:
			return ms[0]
		default:
			t.UnionMembers = ms
		}
	}
	return t
}

// emitFreePromise frees a task promise: its allocation starts at the header
// word before its body (TDD-00247).
func (e *Emitter) emitFreePromise(p string) {
	e.ensureFree()
	base := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 -8", base, p))
	e.emitInstr(fmt.Sprintf("call void @free(ptr %s)", base))
}

// queueMicrotaskName is the internal function synthesized source calls to
// queue a job: `__kml_queue_microtask(fn)`.
const queueMicrotaskName = "__kml_queue_microtask"

// isThenableType reports a thenable class instance or object type.
func (e *Emitter) isThenableType(t Type) bool {
	_, ok := e.thenableClass(t)
	return ok
}

// thenConstName is the internal function synthesized source calls for
// `p.then(() => v)` (or `p.then(() => { throw v })` when its third argument
// is true): `__kml_then_const(p, v, throws)`, v bound where the call runs.
const thenConstName = "__kml_then_const"

func isThenConstCall(ex *ast.CallExpression) bool {
	id, ok := ex.Callee.(*ast.Identifier)
	return ok && id.Name == thenConstName && len(ex.Args) == 3
}

// thenConstCall is the `p.then(callback)` a `__kml_then_const` call means,
// its callback returning (or throwing) the bound value.
func (e *Emitter) thenConstCall(ex *ast.CallExpression, bound ast.Expression) ast.Expression {
	pos := ex.GetPos()
	cb := ast.NewArrowFunction(nil, nil, bound, nil, pos)
	if b, ok := ex.Args[2].(*ast.BooleanLiteral); ok && b.Value {
		cb = ast.NewArrowFunction(nil, nil, nil, ast.NewBlockStatement([]ast.Statement{ast.NewThrowStatement(bound, pos)}, pos), pos)
	}
	return ast.NewCallExpression(ast.NewMemberExpression(ex.Args[0], "then", pos), []ast.Expression{cb}, pos)
}

// emitThenConstCall emits `__kml_then_const(p, v, throws)`.
func (e *Emitter) emitThenConstCall(ex *ast.CallExpression) (Value, error) {
	v, err := e.emitExpr(ex.Args[1])
	if err != nil {
		return Value{}, err
	}
	return e.emitExpr(e.thenConstCall(ex, e.bindValue(v, ex.GetPos())))
}

// synthTypeAnnotation is an annotation naming codegen type t exactly, for a
// parameter of synthesized source, which no source annotation can spell (a
// caught value) and whose inferred type a nested inference may not see.
func (e *Emitter) synthTypeAnnotation(t Type) *ast.TypeAnnotation {
	if e.synthTypes == nil {
		e.synthTypes = map[string]Type{}
	}
	name := fmt.Sprintf("__kml_synth_t%d", len(e.synthTypes))
	e.synthTypes[name] = t
	return &ast.TypeAnnotation{Name: name}
}

// resolveThenName is the internal function a class's synthesized Promise
// combinator calls per member: `__kml_resolve_then(C, x, onF, onR)` is
// `C.resolve(x).then(onF, onR)`, x itself when its constructor is C (a
// check at run time for a boxed x).
const resolveThenName = "__kml_resolve_then"

func isResolveThenCall(ex *ast.CallExpression) bool {
	id, ok := ex.Callee.(*ast.Identifier)
	return ok && id.Name == resolveThenName && len(ex.Args) == 4
}

// emitResolveThenCall emits `__kml_resolve_then(C, x, onF, onR)`.
func (e *Emitter) emitResolveThenCall(ex *ast.CallExpression) (Value, error) {
	pos := ex.GetPos()
	cls := ex.Args[0].(*ast.Identifier)
	xv, err := e.emitExpr(ex.Args[1])
	if err != nil {
		return Value{}, err
	}
	x := e.bindValue(xv, pos)
	call := func(f ast.Expression, a ...ast.Expression) *ast.CallExpression {
		return ast.NewCallExpression(f, a, pos)
	}
	viaResolve := func() error {
		_, err := e.emitExpr(call(ast.NewMemberExpression(call(ast.NewMemberExpression(cls, "resolve", pos), x), "then", pos), ex.Args[2], ex.Args[3]))
		return err
	}
	info, ok := e.classes[cls.Name]
	if !ok || !isUnconstrainedDynamic(xv.Ty) && !(xv.Ty.IsDynamic && len(xv.Ty.UnionMembers) > 0) {
		return Value{Ty: TypeVoid}, viaResolve()
	}
	// A boxed x: its own `then` when it is a C (its header word is C's tag).
	tag, pay := e.emitUnboxTagPayload(xv)
	isObj := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isObj, tag, kmlTagObject))
	chkL, ownL, resolveL, doneL := e.freshLabel("rt.chk"), e.freshLabel("rt.own"), e.freshLabel("rt.resolve"), e.freshLabel("rt.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isObj, chkL, resolveL))
	e.emitLabel(chkL)
	ptr, hdr, isC := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", ptr, pay))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", hdr, ptr))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", isC, hdr, info.TagID))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isC, ownL, resolveL))
	e.emitLabel(ownL)
	// The box as `any` (a union's box is one), its `then` found at run time.
	xAny := e.bindValue(Value{Ref: xv.Ref, Ty: TypeAny}, pos)
	if _, err := e.emitExpr(call(ast.NewMemberExpression(xAny, "then", pos), ex.Args[2], ex.Args[3])); err != nil {
		return Value{}, err
	}
	if !e.blockDone {
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	}
	e.emitLabel(resolveL)
	if err := viaResolve(); err != nil {
		return Value{}, err
	}
	if !e.blockDone {
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	}
	e.emitLabel(doneL)
	return Value{Ty: TypeVoid}, nil
}

// sameReprKey tells two types apart where their values are stored apart:
// the union tag (a string and an object are both pointers) and the
// structural key.
func sameReprKey(t Type) string {
	return unionMemberTag(t) + "|" + typeMergeKey(t, 0)
}

// thenableResolveCallType is the type of a `__kml_thenable_resolve(x)` call.
func (e *Emitter) thenableResolveCallType(ex *ast.CallExpression) (Type, bool) {
	if !isThenableResolveCall(ex) {
		return Type{}, false
	}
	t := e.inferExprType(ex.Args[0])
	if t.IsPromise {
		return t, true
	}
	if sig, ok := e.thenableClass(t); ok {
		t = thenableValueType(sig)
	}
	pt := PromiseOf(t)
	pt.PromiseTask = true
	return pt, true
}

// thenConstCallType is a `__kml_then_const` call's type.
func (e *Emitter) thenConstCallType(ex *ast.CallExpression) (Type, bool) {
	if !isThenConstCall(ex) {
		return Type{}, false
	}
	t := e.inferExprType(e.thenConstCall(ex, ex.Args[1]))
	return t, true
}
