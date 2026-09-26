// emit_module_task.go — a module with a top-level `await` runs as a coroutine
// (TDD-00224).
//
// The continuation of `await p` is a reaction on p, ordered among p's other
// reactions. Inside an async function that holds by construction: the await
// parks the coroutine as a reaction in the promise's own list. Module top-level
// code used to wait on the main stack instead, so its continuation ran out of
// order. For a program with a top-level await the module body is now emitted as
// `@__kml_module_body` and run as a task:
//
//	main():  prologue
//	         P = pending promise; spawn the module task   — runs to its first park
//	         the one event loop                           — resumes it like any task
//	         P still pending → unsettled top-level await (warning, exit 13)
//	         epilogue
//
// The module task differs from an async function's task in two ways: its stack
// is the size of a process main stack (top-level recursion depth must not
// regress to a coroutine's 256 KiB), and its trampoline has no catch-all — an
// uncaught top-level throw reaches the process-level uncaught path with the
// task's jmpbuf stack empty, exactly as it does from main().
//
// A program without a top-level await is emitted exactly as before.
package llvm

import (
	"fmt"
	"strings"

	"KlainMainLang/ast"
)

// tlaUnsettledMsg is what Node prints (first line) for a module whose top-level
// await can never settle.
const tlaUnsettledMsg = "Warning: Detected unsettled top-level await\n"

// moduleTaskStackBytes is the module task's stack: the platform's main-thread
// default, which is what top-level code ran on before it became a task.
func (e *Emitter) moduleTaskStackBytes() int {
	if e.opts.Target.OS() == "windows" {
		// the mingw-w64 linker's default stack reserve (the PE header's
		// SizeOfStackReserve of every executable this compiler produces)
		return 2 * 1024 * 1024
	}
	return 8 * 1024 * 1024
}

// programHasTopLevelAwait reports whether an `await` / `for await` occurs whose
// nearest enclosing function is the module itself.
func programHasTopLevelAwait(prog *ast.Program) bool {
	return stmtsHaveTopLevelAwait(prog.Body)
}

// stmtsHaveTopLevelAwait is programHasTopLevelAwait over a module's statement
// list — the entry program's body or a worker module's (TDD-00224 Stage 2).
func stmtsHaveTopLevelAwait(body []ast.Statement) bool {
	for _, st := range body {
		if awaitsDirectly(st) {
			return true
		}
	}
	return false
}

// ensureModuleTaskRuntime emits the module task's trampoline and the helper a
// blocking event-loop call in module code goes through.
func (e *Emitter) ensureModuleTaskRuntime() {
	if e.usedModuleTaskRuntime {
		return
	}
	e.usedModuleTaskRuntime = true
	e.ensureTaskRuntime()
	e.emitGlobal("@__kml_module_task = internal thread_local global ptr null, align 8")
	e.emitGlobal("@__kml_module_wants_loop = internal thread_local global i1 false, align 1")
	// The module promise of a worker module task, for the worker thread's
	// unsettled check (the entry program keeps its promise in a main() register).
	e.emitGlobal("@__kml_module_promise = internal thread_local global ptr null, align 8")

	// No catch-all (unlike @__kml_task_trampoline): the task's jmpbuf stack is
	// empty at entry, so an uncaught throw takes the process-level uncaught path.
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_module_trampoline() {
entry:
  %%t = load ptr, ptr @__kml_task_launching, align 8
  store ptr %%t, ptr @__kml_module_task, align 8
  %%fn_p = getelementptr %s, ptr %%t, i32 0, i32 %d
  %%fn = load ptr, ptr %%fn_p, align 8
  %%args_p = getelementptr %s, ptr %%t, i32 0, i32 %d
  %%args = load ptr, ptr %%args_p, align 8
  call void %%fn(ptr %%args)
  call void @__kml_task_finish(ptr %%t)
  ret void
}`, taskStructIR, taskFn, taskStructIR, taskArgs))

	// @__kml_module_run_loop(): a call that runs the event loop inline (the
	// blocking http.listen). The loop may only run on the main stack — on a
	// coroutine's stack it would switch contexts out from under itself — so the
	// module task asks main() to run it and parks; main() resumes the task when
	// that run of the loop returns. Anywhere else it is the plain inline run.
	gcRestore := ""
	if e.isGCMode() {
		gcRestore = "\n  call void @__kml_task_gc_restore()"
	}
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_module_run_loop() {
entry:
  %%ct = load ptr, ptr @__kml_current_task, align 8
  %%mt = load ptr, ptr @__kml_module_task, align 8
  %%has = icmp ne ptr %%ct, null
  %%same = icmp eq ptr %%ct, %%mt
  %%onmod = and i1 %%has, %%same
  br i1 %%onmod, label %%park, label %%inline
inline:
  call void @__kml_event_loop_run()
  ret void
park:
  store i1 true, ptr @__kml_module_wants_loop, align 1
  %%st_p = getelementptr %s, ptr %%ct, i32 0, i32 %d
  store i64 3, ptr %%st_p, align 8
  %%sjt_p = getelementptr %s, ptr %%ct, i32 0, i32 %d
  %%curtop = load i32, ptr @__kml_jmp_top, align 4
  %%curtop64 = zext i32 %%curtop to i64
  store i64 %%curtop64, ptr %%sjt_p, align 8
  %%rc_p = getelementptr %s, ptr %%ct, i32 0, i32 %d
  %%rc = load ptr, ptr %%rc_p, align 8
  %%ctx_p = getelementptr %s, ptr %%ct, i32 0, i32 %d
  %%ctx = load ptr, ptr %%ctx_p, align 8
  %%sw = call i32 @swapcontext(ptr %%ctx, ptr %%rc)%s
  ret void
}`, taskStructIR, taskState, taskStructIR, taskSavedJmpTop, taskStructIR, taskResumerCtx, taskStructIR, taskCtx, gcRestore))
}

// moduleBodySplit holds main()'s builders while the module body is emitted into
// its own pair.
type moduleBodySplit struct {
	allocas, body strings.Builder
	blockDone     bool
}

// beginModuleBody switches emission from main() to @__kml_module_body. Called
// after main()'s prologue, before the first top-level statement.
func (e *Emitter) beginModuleBody() *moduleBodySplit {
	s := &moduleBodySplit{blockDone: e.blockDone}
	s.allocas.WriteString(e.allocas.String())
	s.body.WriteString(e.body.String())
	e.allocas = strings.Builder{}
	e.body = strings.Builder{}
	e.blockDone = false
	e.inModuleTask = true
	return s
}

// endModuleBody writes @__kml_module_body from the current builders, switches
// back to main(), and emits the spawn. It returns the module promise register.
func (e *Emitter) endModuleBody(s *moduleBodySplit) string {
	e.emitTerminator("ret void")
	e.inModuleTask = false
	e.functions.WriteString("\ndefine internal void @__kml_module_body(ptr %__kml_module_args) {\nentry:\n")
	e.functions.WriteString(e.allocas.String())
	e.functions.WriteString(e.body.String())
	e.functions.WriteString("}\n")
	e.allocas = strings.Builder{}
	e.body = strings.Builder{}
	e.allocas.WriteString(s.allocas.String())
	e.body.WriteString(s.body.String())
	e.blockDone = s.blockDone

	e.ensureModuleTaskRuntime()
	prom := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_task_alloc_promise()", prom))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr @__kml_module_promise, align 8", prom))
	// An island's top-level throw rejects its module promise — and with it the
	// importer's import() (TDD-00225) — so its task keeps the ordinary
	// trampoline's catch-all; the entry program's throw is an uncaught exception.
	tramp := "@__kml_module_trampoline"
	if e.islandHash != "" {
		tramp = "@__kml_task_trampoline"
	}
	t := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_spawn_task_ex(ptr @__kml_module_body, ptr null, ptr %s, i64 %d, ptr %s)",
		t, prom, e.moduleTaskStackBytes(), tramp))
	return prom
}

// emitModuleLoopResume closes main()'s loop around the event loop: when the loop
// returned because the module task asked for an inline run of it
// (@__kml_module_run_loop), resume the task and run the loop again.
func (e *Emitter) emitModuleLoopResume(loopL string) {
	wants := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i1, ptr @__kml_module_wants_loop, align 1", wants))
	resumeL := e.freshLabel("mod.resume")
	doneL := e.freshLabel("mod.loopdone")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", wants, resumeL, doneL))
	e.emitLabel(resumeL)
	e.emitInstr("store i1 false, ptr @__kml_module_wants_loop, align 1")
	mt := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr @__kml_module_task, align 8", mt))
	e.emitInstr(fmt.Sprintf("call void @__kml_task_resume(ptr %s)", mt))
	e.emitTerminator(fmt.Sprintf("br label %%%s", loopL))
	e.emitLabel(doneL)
}

// emitModuleUnsettledCheck ends the process the way Node ends a module whose
// top-level await never settled: the loop returned with the module promise still
// pending, so nothing alive can settle it — a warning, `process.on('exit')`
// listeners with code 13, exit code 13.
func (e *Emitter) emitModuleUnsettledCheck(prom string) {
	e.ensureExit()
	e.ensureWriteDecl()
	sp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 0", sp, promiseStructIR, prom))
	st := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", st, sp))
	pending := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", pending, st))
	unsL := e.freshLabel("mod.unsettled")
	okL := e.freshLabel("mod.settled")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", pending, unsL, okL))
	e.emitLabel(unsL)
	w := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @write(i32 2, ptr @.kml_tla_unsettled, i64 %d)", w, len(tlaUnsettledMsg)))
	if e.usedProcessLifecycle {
		e.emitInstr("store i64 13, ptr @__kml_process_exit_code, align 8")
		e.emitInstr("call void @__kml_run_exit_handlers(i64 13)")
	}
	e.emitInstr("call void @exit(i32 13)")
	e.emitTerminator("unreachable")
	e.emitLabel(okL)
}
