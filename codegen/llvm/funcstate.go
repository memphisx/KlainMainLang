// funcstate.go — the per-function emitter state a nested function body must
// not inherit (TDD-00230 P3.5 splits it out of the Emitter).
package llvm

import (
	"strings"

	"KlainMainLang/ast"
)

// funcContext is the language context of the function being emitted: its
// scopes, return and promise types, async, generator and constructor state,
// and the loop, label, cleanup and finally stacks. Emitter embeds it; a
// function emitted with none of the enclosing one's context swaps it.
type funcContext struct {
	scopes               []scope
	currentGenerator     *generatorEmitCtx
	currentRetType       Type // return type of the function being emitted
	currentCtorClass     string
	inModuleTask         bool     // emitting @__kml_module_body
	sawAwait             bool     // set by emitAwait/for-await while a function body is emitted; saved/restored per body
	breakStack           []string // end labels for enclosing loops / switch
	continueStack        []string // continue-target labels for enclosing loops
	pendingFinallys      []pendingExit
	breakFinallyDepth    []int
	continueFinallyDepth []int
	pendingFrees         []pendingFree
	breakFreeScope       []int
	continueFreeScope    []int
	namedLabelStack      []namedLabel // labeled break/continue targets, innermost last
	isAsync              bool
	coroHdl              string // register holding the malloc'd promise slot
	asyncPromiseReg      string // non-suspending async fn: the settled task promise it returns (TDD-00084 Part A)
	asyncCatchLabel      string // non-suspending async fn: the catch-and-reject block label
	currentPromiseTy     Type   // T in Promise<T>; void if Promise<void>
	coroRetLabel         string // label for the async-return block
	asyncAdoptSlot       string
}

// funcBuilder is the state of the function being emitted: its entry-block
// allocas and its body (merged at the end), the register and label
// counters, and whether the current block has its terminator. Emitter
// embeds it, so a nested function's emission swaps one pointer.
type funcBuilder struct {
	allocas   strings.Builder
	body      strings.Builder
	regCtr    int
	labelCtr  int
	blockDone bool
	// frame is the function's shadow-call-stack record (frames.go), and
	// frameOld the top it replaced, which every return restores; "" for a
	// function without one. A helper emitted meanwhile has its own builder,
	// and so none.
	frame    string
	frameOld string
	// frameFile is the source file the frame names.
	frameFile string
	// sitePos are the enclosing calls' positions, innermost last.
	sitePos []ast.Pos
}

// beginDetachedFunc starts the body of a function emitted while another is
// being emitted (a synthesized adapter, a dynamic function): a fresh,
// synchronous function body with its own scope, its return type `any` —
// none of the enclosing function's async, generator, loop or cleanup state —
// restored by the returned func.
func (e *Emitter) beginDetachedFunc() func() {
	restore := e.beginThunkEmit()
	saved := e.funcContext
	e.funcContext = &funcContext{currentRetType: TypeAny}
	// Its registers and labels are numbered from zero (a fresh builder), as
	// a declared function's are, so a helper's text does not depend on how
	// much the program emitted before it: its unit is cached by that text.
	e.pushScope()
	return func() {
		e.funcContext = saved
		restore()
	}
}

// writeFunc writes a finished function to the module: its definition
// header (`internal i64 @f(ptr %env)`), then its entry block's body, the
// allocas first. Every function the emitter builds goes out through here,
// the one place its prologue and epilogue are written.
func (e *Emitter) writeFunc(header, body string) {
	e.functions.WriteString("\ndefine " + header + " {\nentry:\n")
	e.functions.WriteString(body)
	e.functions.WriteString("}\n")
}
