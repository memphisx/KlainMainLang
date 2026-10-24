package llvm

import "fmt"

// exc_prims.go — the one lowering of a catch frame (TDD-00230 P3.3b). A
// try, an async body, a generator body and every runtime-internal catch
// open a handler with excEnter and close it with excLeave; a throw unwinds
// to the innermost open handler (__kml_throw, which pops it). Today that
// is setjmp/longjmp over the jmpbuf stack; WebAssembly exception handling
// replaces exactly these two. TestExceptionLoweringIsOnePrimitive keeps
// every other file off the raw calls.

// excEnter opens a catch frame and branches: to catchL when a throw
// unwinds to it, to tryL now.
func (e *Emitter) excEnter(catchL, tryL string) {
	jb, sj, threw := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_push_jmpbuf()", jb))
	e.emitInstr(fmt.Sprintf("%s = %s", sj, e.setjmpCall(jb)))
	e.emitInstr(fmt.Sprintf("%s = icmp ne i32 %s, 0", threw, sj))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", threw, catchL, tryL))
}

// excLeave closes the innermost catch frame on a path that leaves its body
// without a throw.
func (e *Emitter) excLeave() {
	e.emitInstr("call void @__kml_pop_jmpbuf()")
}

// excThrow throws the error object err to the innermost open catch frame.
func (e *Emitter) excThrow(err string) {
	e.emitInstr(fmt.Sprintf("call void @__kml_throw(ptr %s)", err))
}

// excThrowAny throws a value of any kind, as its tag and payload.
func (e *Emitter) excThrowAny(tag, pay string) {
	e.emitInstr(fmt.Sprintf("call void @__kml_throw_any(i8 %s, i64 %s)", tag, pay))
}

// fiberSwitch suspends the running fiber, saving it into from, and resumes
// the one saved in to: the one lowering of `await`/`yield` suspension and
// resumption (stackful ucontext fibers today; a stackless state machine or
// Asyncify on WebAssembly replaces it). It returns the switch's result
// register.
func (e *Emitter) fiberSwitch(from, to string) string {
	// Each side keeps its own shadow call stack (KML_SWAPCONTEXT, kml_heap.h).
	e.ensureFrames()
	top, base := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", top, e.frameSlot("top")))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", base, e.frameSlot("base")))
	e.declareFn("__kml_frame_park", "declare void @__kml_frame_park(ptr, ptr)")
	e.emitInstr(fmt.Sprintf("call void @__kml_frame_park(ptr %s, ptr %s)", base, top))
	rc := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @swapcontext(ptr %s, ptr %s)", rc, from, to))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", top, e.frameSlot("top")))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", base, e.frameSlot("base")))
	e.ensureFrameCut()
	e.emitInstr(fmt.Sprintf("call void @__kml_frame_cut(ptr %s)", base))
	return rc
}
