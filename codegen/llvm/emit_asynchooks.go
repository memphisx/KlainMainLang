// emit_asynchooks.go — async context propagation for async_hooks
// (lib/node/async_hooks.ts, TDD-00168): the context frame a timer callback
// was scheduled under is reinstalled when it fires.
package llvm

import (
	"fmt"

	"KlainMainLang/ast"
)

// wrapTimerClosureWithAsyncCtx wraps a timer callback closure so it carries the
// *current* async context to its deferred fire (TDD-00168 Stage 3). It captures
// the context head now (at schedule time) into a { origClosure, capturedCtx }
// env and returns a fresh { @__kml_als_timer_tramp, env } closure header that
// installs the captured context, invokes the original, and restores — so a
// `setTimeout(cb)` scheduled inside `als.run(...)` sees the store when `cb`
// fires. Only applied when the program has the async_hooks module
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

// programConstructsClass reports whether the program contains a `new <cls>`
// anywhere — the whole-program pre-scan behind the FinalizationRegistry
// Memory.free hook (TDD-00163 Stage 2).
func programConstructsClass(prog *ast.Program, cls string) bool {
	found := false
	ast.Inspect(prog, func(n ast.Node) bool {
		if ne, ok := n.(*ast.NewExpression); ok && ne.ClassName == cls {
			found = true
		}
		return !found
	})
	return found
}
