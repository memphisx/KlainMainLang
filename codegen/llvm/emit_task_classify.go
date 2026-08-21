// emit_task_classify.go — the "may-suspend" classification pass (TDD-00083
// Stage 2). An async function is compiled as a coroutine task (runtime_task.go)
// only if it can actually suspend at an await; purely-synchronous async
// functions keep the inlined malloc-slot fast path unchanged, so programs
// without a suspending async fn are byte-for-byte identical.
//
// A function may suspend if its body contains an `await` whose argument is a
// fetch call, a Promise combinator (`Promise.all`/`.any`/`.race`/`.allSettled`),
// or a call to another may-suspend function — computed to a fixed point over the
// async call graph. The analysis is a safe *under*-approximation: an await it
// fails to recognize just leaves the function on the current synchronous path
// (correct, merely non-concurrent), never wrong.

package llvm

import "KlainMainLang/ast"

// classifyAsyncSuspension marks e.funcs[name].MaySuspend for every top-level
// async function that can suspend. Run after registerFunctions.
func (e *Emitter) classifyAsyncSuspension(prog *ast.Program) {
	asyncFns := map[string]*ast.FunctionDeclaration{}
	for _, s := range prog.Body {
		fd, ok := s.(*ast.FunctionDeclaration)
		if ok && fd.IsAsync && !fd.IsGenerator && len(fd.TypeParams) == 0 && fd.Body != nil {
			asyncFns[fd.Name] = fd
		}
	}
	maySuspend := map[string]bool{}
	for changed := true; changed; {
		changed = false
		for name, fd := range asyncFns {
			if maySuspend[name] {
				continue
			}
			if stmtsSuspend(fd.Body.Body, maySuspend) {
				maySuspend[name] = true
				changed = true
			}
		}
	}
	for name := range maySuspend {
		if sig, ok := e.funcs[name]; ok {
			sig.MaySuspend = true
			e.funcs[name] = sig
			e.hasMaySuspend = true
		}
	}
}

// ensureCurrentTaskGlobal declares @__kml_current_task (the running-task pointer,
// null on the main/scheduler context). Declared by both the task runtime and the
// fetch runtime so __kml_await_fetch can reference it even in a program that
// uses fetch without any may-suspend async function (where it stays null).
func (e *Emitter) ensureCurrentTaskGlobal() {
	if e.usedCurrentTaskGlobal {
		return
	}
	e.usedCurrentTaskGlobal = true
	e.emitGlobal("@__kml_current_task = internal global ptr null, align 8")
}

// isSuspendingAwaitArg reports whether awaiting arg suspends: a fetch call, a
// Promise combinator, or a call to a may-suspend function.
func isSuspendingAwaitArg(arg ast.Expression, maySuspend map[string]bool) bool {
	ce, ok := arg.(*ast.CallExpression)
	if !ok {
		return false
	}
	switch callee := ce.Callee.(type) {
	case *ast.Identifier:
		return callee.Name == "fetch" || maySuspend[callee.Name]
	case *ast.MemberExpression:
		if obj, ok := callee.Object.(*ast.Identifier); ok && obj.Name == "Promise" {
			switch callee.Property {
			case "all", "any", "race", "allSettled":
				// A combinator only actually suspends if one of its members is a
				// fetch / may-suspend call. Over non-suspending async results it
				// resolves synchronously, so it must NOT force the whole program
				// onto the fiber/libcurl path (TDD-00084 Part A). An array literal
				// we can inspect precisely; anything else (a variable built
				// elsewhere) we conservatively treat as suspending.
				if len(ce.Args) >= 1 {
					if arr, ok := ce.Args[0].(*ast.ArrayLiteral); ok {
						for _, el := range arr.Elements {
							if isSuspendingAwaitArg(el, maySuspend) {
								return true
							}
						}
						return false
					}
				}
				return true
			}
		}
	}
	return false
}

func stmtsSuspend(stmts []ast.Statement, ms map[string]bool) bool {
	for _, s := range stmts {
		if stmtSuspend(s, ms) {
			return true
		}
	}
	return false
}

func blockSuspend(b *ast.BlockStatement, ms map[string]bool) bool {
	return b != nil && stmtsSuspend(b.Body, ms)
}

func stmtSuspend(s ast.Statement, ms map[string]bool) bool {
	switch st := s.(type) {
	case *ast.BlockStatement:
		return stmtsSuspend(st.Body, ms)
	case *ast.VarDeclaration:
		return exprSuspend(st.Init, ms)
	case *ast.ExpressionStatement:
		return exprSuspend(st.Expr, ms)
	case *ast.ReturnStatement:
		return exprSuspend(st.Value, ms)
	case *ast.IfStatement:
		return exprSuspend(st.Test, ms) || blockSuspend(st.Consequent, ms) || stmtSuspend(st.Alternate, ms)
	case *ast.ForStatement:
		return stmtSuspend(st.Init, ms) || exprSuspend(st.Test, ms) || exprsSuspend(st.Update, ms) || blockSuspend(st.Body, ms)
	case *ast.WhileStatement:
		return exprSuspend(st.Test, ms) || blockSuspend(st.Body, ms)
	case *ast.DoWhileStatement:
		return exprSuspend(st.Test, ms) || blockSuspend(st.Body, ms)
	case *ast.ForOfStatement:
		return exprSuspend(st.Iterable, ms) || blockSuspend(st.Body, ms)
	case *ast.ForInStatement:
		return exprSuspend(st.Object, ms) || blockSuspend(st.Body, ms)
	case *ast.TryStatement:
		return blockSuspend(st.Body, ms) || tryHandlerSuspend(st, ms)
	case *ast.SwitchStatement:
		if exprSuspend(st.Discriminant, ms) {
			return true
		}
		for _, c := range st.Cases {
			if exprSuspend(c.Test, ms) || stmtsSuspend(c.Body, ms) {
				return true
			}
		}
	}
	return false
}

func exprsSuspend(exprs []ast.Expression, ms map[string]bool) bool {
	for _, ex := range exprs {
		if exprSuspend(ex, ms) {
			return true
		}
	}
	return false
}

func exprSuspend(ex ast.Expression, ms map[string]bool) bool {
	switch e := ex.(type) {
	case nil:
		return false
	case *ast.AwaitExpression:
		// Any `await` suspends: even awaiting an already-settled promise yields a
		// microtask tick (TDD-00088), so the enclosing async fn must run as a task
		// (fiber) to have a suspension point. (The argument may itself suspend too,
		// but a bare `await settledPromise` is enough on its own now.)
		return true
	case *ast.CallExpression:
		if exprSuspend(e.Callee, ms) {
			return true
		}
		return exprsSuspend(e.Args, ms)
	case *ast.MemberExpression:
		return exprSuspend(e.Object, ms)
	case *ast.BinaryExpression:
		return exprSuspend(e.Left, ms) || exprSuspend(e.Right, ms)
	case *ast.ConditionalExpression:
		return exprSuspend(e.Test, ms) || exprSuspend(e.Consequent, ms) || exprSuspend(e.Alternate, ms)
	case *ast.AssignmentExpression:
		return exprSuspend(e.Left, ms) || exprSuspend(e.Right, ms)
	}
	return false
}

func tryHandlerSuspend(st *ast.TryStatement, ms map[string]bool) bool {
	if st.Catch != nil && blockSuspend(st.Catch.Body, ms) {
		return true
	}
	return blockSuspend(st.Finally, ms)
}
