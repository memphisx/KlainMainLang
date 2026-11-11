// emit_task_classify.go — the "may-suspend" classification pass (TDD-00083
// Stage 2). An async function is compiled as a coroutine task (runtime_task.go)
// only if it can actually suspend at an await; purely-synchronous async
// functions keep the inlined malloc-slot fast path unchanged, so programs
// without a suspending async fn are byte-for-byte identical.
//
// A function may suspend if its body contains an `await` (any await yields at
// least a microtask tick, TDD-00088) or a `for await…of`, outside nested
// functions. The body is walked with the generated AST traversal, so an await
// in any expression or statement position is found: a missed one would run the
// function synchronously to completion, reordering its output against Node.

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
	for name, fd := range asyncFns {
		if awaitsDirectly(fd.Body) {
			maySuspend[name] = true
		}
	}
	// TDD-00223 §2: every async callable that awaits is a coroutine, and fetch
	// reactions run as coroutines, so the task-aware await paths must be the ones
	// emitted whenever the program can wait at all — not only when a top-level
	// declaration was classified.
	if programMayWait(prog) {
		e.hasMaySuspend = true
	}
	for name := range maySuspend {
		if sig, ok := e.funcs[name]; ok {
			sig.MaySuspend = true
			e.funcs[name] = sig
			e.hasMaySuspend = true
		}
	}
}

// awaitsDirectly reports whether n contains an `await` or a `for await…of`
// that belongs to the enclosing function or module — it never enters a nested
// function or class (whose awaits are their own), n itself included.
func awaitsDirectly(n ast.Node) bool {
	found := false
	ast.Inspect(n, func(c ast.Node) bool {
		switch c := c.(type) {
		case *ast.AwaitExpression:
			found = true
		case *ast.ForOfStatement:
			found = found || c.Await
		case *ast.FunctionDeclaration, *ast.FunctionExpression, *ast.ArrowFunction,
			*ast.ClassDeclaration, *ast.ClassExpression:
			return false
		}
		return !found
	})
	return found
}
