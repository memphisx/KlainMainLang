// funcstate.go — the per-function emitter state a nested function body must
// not inherit (TDD-00230 P3.5 splits it out of the Emitter).
package llvm

import "KlainMainLang/ast"

// beginDetachedFunc starts the body of a function emitted while another is
// being emitted (a synthesized adapter, a dynamic function): a fresh,
// synchronous function body with its own scope, its return type `any` — none of the enclosing function's async,
// generator, loop or cleanup state — restored by the returned func.
func (e *Emitter) beginDetachedFunc() func() {
	restore := e.beginThunkEmit()
	type saved struct {
		scopes                                           []scope
		ret, promTy                                      Type
		isAsync, inModuleTask, sawAwait, httpHandler     bool
		coroHdl, asyncProm, asyncCatch, coroRet, ctorCls string
		gen                                              *generatorEmitCtx
		brk, cont                                        []string
		labels                                           []namedLabel
		frees                                            []pendingFree
		brkFree, contFree, brkFin, contFin               []int
		finallys                                         []pendingExit
		handlerNode                                      ast.Node
	}
	sv := saved{e.scopes, e.currentRetType, e.currentPromiseTy, e.isAsync, e.inModuleTask, e.sawAwait, e.emittingHTTPHandler,
		e.coroHdl, e.asyncPromiseReg, e.asyncCatchLabel, e.coroRetLabel, e.currentCtorClass, e.currentGenerator,
		e.breakStack, e.continueStack, e.namedLabelStack, e.pendingFrees, e.breakFreeScope, e.continueFreeScope,
		e.breakFinallyDepth, e.continueFinallyDepth, e.pendingFinallys, e.httpHandlerNode}
	e.scopes = nil
	e.currentRetType = TypeAny
	e.currentPromiseTy = Type{}
	e.isAsync, e.inModuleTask, e.sawAwait, e.emittingHTTPHandler = false, false, false, false
	e.coroHdl, e.asyncPromiseReg, e.asyncCatchLabel, e.coroRetLabel, e.currentCtorClass = "", "", "", "", ""
	e.currentGenerator = nil
	e.breakStack, e.continueStack, e.namedLabelStack = nil, nil, nil
	e.pendingFrees, e.breakFreeScope, e.continueFreeScope = nil, nil, nil
	e.breakFinallyDepth, e.continueFinallyDepth, e.pendingFinallys = nil, nil, nil
	e.httpHandlerNode = nil
	adoptSlot := e.asyncAdoptSlot
	e.asyncAdoptSlot = ""
	// Its registers and labels are numbered from zero, as a declared
	// function's are, so a helper's text does not depend on how much the
	// program emitted before it: its unit is cached by that text.
	savedReg, savedLabel := e.regCtr, e.labelCtr
	e.regCtr, e.labelCtr = 0, 0
	e.pushScope()
	return func() {
		e.regCtr, e.labelCtr = savedReg, savedLabel
		e.scopes, e.currentRetType, e.currentPromiseTy = sv.scopes, sv.ret, sv.promTy
		e.isAsync, e.inModuleTask, e.sawAwait, e.emittingHTTPHandler = sv.isAsync, sv.inModuleTask, sv.sawAwait, sv.httpHandler
		e.coroHdl, e.asyncPromiseReg, e.asyncCatchLabel, e.coroRetLabel, e.currentCtorClass = sv.coroHdl, sv.asyncProm, sv.asyncCatch, sv.coroRet, sv.ctorCls
		e.currentGenerator = sv.gen
		e.breakStack, e.continueStack, e.namedLabelStack = sv.brk, sv.cont, sv.labels
		e.pendingFrees, e.breakFreeScope, e.continueFreeScope = sv.frees, sv.brkFree, sv.contFree
		e.breakFinallyDepth, e.continueFinallyDepth, e.pendingFinallys = sv.brkFin, sv.contFin, sv.finallys
		e.httpHandlerNode = sv.handlerNode
		e.asyncAdoptSlot = adoptSlot
		restore()
	}
}
