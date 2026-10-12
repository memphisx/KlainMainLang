// runtime_loop_turn.go — the loop globals the generated code shares with
// loopsrc/loop.c.
//
// Every wait parks (TDD-00244 Stage 2): a coroutine, an async generator's
// step or a connection fiber yields to the one event loop, and the module
// body with a top-level await is itself a task (TDD-00224). A wait that can
// do neither is a compiler bug and aborts (__kml_wait_outside_coroutine).
// What remains here:
//
//	@__kml_loop_oneshot / _nowait / _idle
//	    A single non-blocking pass of the loop and its idle verdict: a
//	    dynamic-import island's poll export (emit_dynimport.go).
//
//	@.kml_tla_unsettled
//	    Node's warning for a top-level await that can never settle (exit 13).
package llvm

import "strconv"

// noteLoopTurn records that emitted code waits on the event loop, so the end
// of main() runs the full loop.
func (e *Emitter) noteLoopTurn() {
	e.usedLoopTurn = true
	e.usedAwaitTimerDrive = true // @__kml_fetch_pump resolves
}

// ensureLoopTurn declares the loop globals and the shared warning text.
// Called by ensureHTTPRuntime, which links loop.c, where the globals live.
func (e *Emitter) ensureLoopTurn() {
	if e.usedLoopTurnDefs {
		return
	}
	e.usedLoopTurnDefs = true
	e.ensureExit()
	e.ensureWriteDecl()
	e.emitGlobal("@__kml_loop_oneshot = external thread_local global i8")
	e.emitGlobal("@__kml_loop_nowait = external thread_local global i8")
	e.emitGlobal("@__kml_loop_idle = external thread_local global i1")
	msg := tlaUnsettledMsg
	e.emitGlobal(`@.kml_tla_unsettled = private unnamed_addr constant [` + strconv.Itoa(len(msg)) + ` x i8] c"Warning: Detected unsettled top-level await\0A"`)
}
