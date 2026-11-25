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
	"os"
	"regexp"
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

// moduleBodySplit holds main()'s builders while the module body is emitted into
// its own pair.
type moduleBodySplit struct {
	allocas, body strings.Builder
	blockDone     bool
	// main()'s shadow-call-stack state; the body's fiber has its own.
	frame, frameOld string
	sitePos         []ast.Pos
}

// beginModuleBody switches emission from main() to @__kml_module_body. Called
// after main()'s prologue, before the first top-level statement.
func (e *Emitter) beginModuleBody() *moduleBodySplit {
	s := &moduleBodySplit{blockDone: e.blockDone, frame: e.frame, frameOld: e.frameOld, sitePos: e.sitePos}
	e.frame, e.frameOld, e.sitePos = "", "", nil
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
	e.writeFunc("internal void @__kml_module_body("+irPtr+" %__kml_module_args)", e.allocas.String()+e.body.String())
	e.allocas = strings.Builder{}
	e.body = strings.Builder{}
	e.allocas.WriteString(s.allocas.String())
	e.body.WriteString(s.body.String())
	e.blockDone = s.blockDone
	e.frame, e.frameOld, e.sitePos = s.frame, s.frameOld, s.sitePos

	e.ensureUnhandledRejections()
	prom := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_task_alloc_promise()", prom))
	e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" @__kml_module_promise, align 8", prom))
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
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_spawn_task_ex("+irPtr+" @__kml_module_body, "+irPtr+" null, "+irPtr+" %s, i64 %d, "+irPtr+" %s)",
		t, prom, e.moduleTaskStackBytes(), tramp))
	return prom
}

// emitModuleLoopResume closes main()'s loop around the event loop: when the loop
// returned because the module task asked for an inline run of it
// (__kml_module_park_for_loop), resume the task and run the loop again.
func (e *Emitter) emitModuleLoopResume(loopL string) {
	wants := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i1, "+irPtr+" @__kml_module_wants_loop, align 1", wants))
	resumeL := e.freshLabel("mod.resume")
	doneL := e.freshLabel("mod.loopdone")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", wants, resumeL, doneL))
	e.emitLabel(resumeL)
	e.emitInstr("store i1 false, " + irPtr + " @__kml_module_wants_loop, align 1")
	mt := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" @__kml_module_task, align 8", mt))
	e.emitInstr(fmt.Sprintf("call void @__kml_task_resume("+irPtr+" %s)", mt))
	e.emitTerminator(fmt.Sprintf("br label %%%s", loopL))
	e.emitLabel(doneL)
}

// emitModuleUnsettledCheck ends the process the way Node ends a module whose
// top-level await never settled: the loop returned with the module promise still
// pending, so nothing alive can settle it — a warning, `process.on('exit')`
// listeners with code 13, exit code 13.
func (e *Emitter) emitModuleUnsettledCheck(prom string) {
	sp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 0", sp, promiseStructIR, prom))
	st := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", st, sp))
	pending := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", pending, st))
	unsL := e.freshLabel("mod.unsettled")
	okL := e.freshLabel("mod.settled")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", pending, unsL, okL))
	e.emitLabel(unsL)
	e.emitTLAWarning()
	e.emitInstr("call void @__kml_main_set_exit(i32 13)")
	e.emitInstr("call void @exit(i32 13)")
	e.emitTerminator("unreachable")
	e.emitLabel(okL)
}

// noteTLASite records, before a top-level await suspends the module task,
// which await it is: Node's unsettled warning names the await the module is
// parked at, with its line of source and a caret under it (under a `for
// await` loop's binding). It reports whether it recorded one.
func (e *Emitter) noteTLASite(pos ast.Pos, forAwait bool) bool {
	if !e.inModuleTask || e.isAsync || e.currentGenerator != nil || e.frameFile == "" || pos.Line <= 0 {
		return false
	}
	msg, ok := tlaSiteMsg(e.frameFile, pos, forAwait)
	if !ok {
		return false
	}
	if len(e.tlaSites) == 0 {
		e.emitGlobal("@__kml_tla_site = internal" + irGlobalAS + " global i64 -1, align 8")
	}
	e.tlaSites = append(e.tlaSites, msg)
	e.emitInstr(fmt.Sprintf("store i64 %d, "+irPtr+" @__kml_tla_site, align 8", len(e.tlaSites)-1))
	return true
}

// restoreTLALoopSite makes the enclosing top-level `for await` the parked
// site again once an await in its body has resumed: its next step waits
// next.
func (e *Emitter) restoreTLALoopSite() {
	if n := len(e.tlaLoopSites); n > 0 {
		e.emitInstr(fmt.Sprintf("store i64 %d, "+irPtr+" @__kml_tla_site, align 8", e.tlaLoopSites[n-1]))
	}
}

// forAwaitBinding matches a `for await (` head up to the loop's binding.
var forAwaitBinding = regexp.MustCompile(`^for\s+await\s*\(\s*(?:(?:const|let|var)\s+)?`)

// tlaSiteMsg is Node's warning for a module parked at the await at pos in
// file: its URL and line, the line, and a caret under the await (under the
// binding of a `for await` loop at pos).
func tlaSiteMsg(file string, pos ast.Pos, forAwait bool) (string, bool) {
	src, err := os.ReadFile(file)
	if err != nil {
		return "", false
	}
	lines := strings.Split(string(src), "\n")
	if pos.Line > len(lines) {
		return "", false
	}
	line := strings.TrimSuffix(lines[pos.Line-1], "\r")
	col := max(pos.Col-1, 0)
	if col > len(line) {
		return "", false
	}
	if forAwait {
		m := forAwaitBinding.FindString(line[col:])
		if m == "" {
			return "", false
		}
		col += len(m)
	}
	abs, err := realPath(file)
	if err != nil {
		return "", false
	}
	return fmt.Sprintf("Warning: Detected unsettled top-level await at %s:%d\n%s\n%s^\n\n\n\n", fileURL(abs), pos.Line, line, strings.Repeat(" ", col)), true
}

// emitTLAWarning writes the unsettled warning: the recorded await's, or
// the bare first line when none was recorded (a `for await`).
func (e *Emitter) emitTLAWarning() {
	if len(e.tlaSites) == 0 {
		e.emitInstr(fmt.Sprintf("call i64 @write(i32 2, "+irPtr+" @.kml_tla_unsettled, i64 %d)", len(tlaUnsettledMsg)))
		return
	}
	var rows []string
	for _, m := range e.tlaSites {
		rows = append(rows, fmt.Sprintf("{ "+irPtr+", i64 } { "+irPtr+" %s, i64 %d }", e.internString(m), len(m)))
	}
	rows = append(rows, fmt.Sprintf("{ "+irPtr+", i64 } { "+irPtr+" @.kml_tla_unsettled, i64 %d }", len(tlaUnsettledMsg)))
	n := len(rows)
	e.emitGlobal(fmt.Sprintf("@__kml_tla_msgs = private unnamed_addr"+irGlobalAS+" constant [%d x { "+irPtr+", i64 }] [%s]", n, strings.Join(rows, ", ")))
	site, idx := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" @__kml_tla_site, align 8", site))
	neg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 0", neg, site))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %d, i64 %s", idx, neg, n-1, site))
	pp, lp, p, l := e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr [%d x { "+irPtr+", i64 }], "+irPtr+" @__kml_tla_msgs, i64 0, i64 %s, i32 0", pp, n, idx))
	e.emitInstr(fmt.Sprintf("%s = getelementptr [%d x { "+irPtr+", i64 }], "+irPtr+" @__kml_tla_msgs, i64 0, i64 %s, i32 1", lp, n, idx))
	e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", p, pp))
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", l, lp))
	e.emitInstr(fmt.Sprintf("call i64 @write(i32 2, "+irPtr+" %s, i64 %s)", p, l))
}
