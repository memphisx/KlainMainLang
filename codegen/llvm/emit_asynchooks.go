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
	if e.programIndependent() {
		// Shared code wraps only while a context is current: the same
		// whether or not the program uses AsyncLocalStorage.
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_als_wrap(ptr %s)", r, closurePtr))
		return r
	}
	cc := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_als_ctx_get()", cc))
	env := e.freshReg()
	e.heapMallocInto(env, "16")
	e.heapStore("ptr", closurePtr, env, 8)
	ccp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 1", ccp, env))
	e.heapStore("ptr", cc, ccp, 8)
	hdr := e.freshReg()
	e.heapMallocInto(hdr, "16")
	e.heapStore("ptr", "@__kml_als_timer_tramp", hdr, 8)
	hep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 1", hep, hdr))
	e.heapStore("ptr", env, hep, 8)
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
