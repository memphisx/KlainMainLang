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
	_ "embed"
	"fmt"
	"strings"

	"KlainMainLang/ast"
)

// The trampoline and the inline-loop helper live in promisesrc/module.c
// (TDD-00240).
//
//go:embed promisesrc/module.c
var moduleTaskSource string

// ModuleTaskSource is module.c, behind kml_layout.h.
func ModuleTaskSource() string { return layoutHeader() + moduleTaskSource }

// UsesModuleTask reports whether the program links module.c.
func (e *Emitter) UsesModuleTask() bool { return e.usedModuleTaskRuntime }

// ModuleTaskCFlags: module.c restores the GC stack bottom after a swap.
func (e *Emitter) ModuleTaskCFlags() []string {
	if e.isGCMode() {
		return []string{"-DKLAIN_GC=1"}
	}
	return nil
}

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
	// Defined in module.c; the entry program and worker loops read them.
	e.emitGlobal(`@__kml_module_task = external thread_local global ptr, align 8
@__kml_module_wants_loop = external thread_local global i1, align 1
@__kml_module_promise = external thread_local global ptr, align 8
declare void @__kml_module_trampoline()
declare void @__kml_module_run_loop()`)
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
	// The runtime consumes the module's promise (the loop's wait, or the
	// importer polling an island): never an unhandled rejection (ADR-01192).
	e.emitMarkPromiseHandled(prom)
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
