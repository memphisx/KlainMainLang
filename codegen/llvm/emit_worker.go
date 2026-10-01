// emit_worker.go — a worker module's entry: the modules a Worker evaluates on
// its own thread (every builtin module, the user modules it imports, then
// its own top level), emitted into `define void @__kml_worker_entry_<i>()`,
// and the registry that maps the module's path to it at start-up
// (klainpool.c's workerSpawn looks the path up). The Worker surface itself is
// lib/node/worker_threads.ts.
package llvm

import (
	"fmt"
	"strings"

	"KlainMainLang/ast"
)

// emitWorkerModules emits every worker module's entry function, using the
// same builder-swap seam emitClosureFunc uses, and records the start-up
// registration main runs.
func (e *Emitter) emitWorkerModules(prog *ast.Program) error {
	if len(prog.WorkerModules) == 0 {
		return nil
	}
	e.ensureWorkerRuntime()
	var reg strings.Builder
	anyModuleTask := false
	for i, wm := range prog.WorkerModules {
		symbol := fmt.Sprintf("__kml_worker_entry_%d", i)
		savedAllocas := e.allocas
		savedBody := e.body
		savedRegCtr := e.regCtr
		savedLabelCtr := e.labelCtr
		savedScopes := e.scopes
		savedRetType := e.currentRetType
		savedBlockDone := e.blockDone
		e.allocas = strings.Builder{}
		e.body = strings.Builder{}
		e.regCtr = 0
		e.labelCtr = 0
		e.scopes = nil
		e.currentRetType = TypeVoid
		e.blockDone = false
		e.pushScope()
		e.currentWorkerMod = wm.Path
		// A worker module with a top-level await takes the entry program's
		// shape on its own thread (TDD-00224 Stage 2): its statements become
		// the body of a module task, and the entry symbol only spawns that
		// task; @__kml_worker_run_loop then resumes it from the reactions it
		// parks in, and ends the worker with code 13 if the module promise
		// never settled.
		moduleTask := stmtsHaveTopLevelAwait(wm.Body)
		savedInModuleTask := e.inModuleTask
		e.inModuleTask = moduleTask

		var emitErr error
		for _, stmt := range append(append([]ast.Statement{}, wm.Deps...), wm.Body...) {
			if emitErr = e.emitStmt(stmt); emitErr != nil {
				break
			}
		}
		if emitErr == nil {
			e.emitTerminator("ret void")
			if moduleTask {
				e.functions.WriteString(fmt.Sprintf("\ndefine internal void @%s_body(ptr %%__kml_module_args) {\nentry:\n", symbol))
				e.functions.WriteString(e.allocas.String())
				e.functions.WriteString(e.body.String())
				e.functions.WriteString("}\n")
				e.ensureModuleTaskRuntime()
				e.functions.WriteString(fmt.Sprintf(`
define void @%s() {
entry:
  %%p = call ptr @__kml_task_alloc_promise()
  store ptr %%p, ptr @__kml_module_promise, align 8
  %%t = call ptr @__kml_spawn_task_ex(ptr @%s_body, ptr null, ptr %%p, i64 %d, ptr @__kml_module_trampoline)
  ret void
}
`, symbol, symbol, e.moduleTaskStackBytes()))
			} else {
				e.functions.WriteString(fmt.Sprintf("\ndefine void @%s() {\nentry:\n", symbol))
				e.functions.WriteString(e.allocas.String())
				e.functions.WriteString(e.body.String())
				e.functions.WriteString("}\n")
			}
		}

		e.inModuleTask = savedInModuleTask
		e.currentWorkerMod = ""
		e.allocas = savedAllocas
		e.body = savedBody
		e.regCtr = savedRegCtr
		e.labelCtr = savedLabelCtr
		e.scopes = savedScopes
		e.currentRetType = savedRetType
		e.blockDone = savedBlockDone
		if emitErr != nil {
			return fmt.Errorf("worker module %s: %w", wm.Path, emitErr)
		}
		if moduleTask {
			anyModuleTask = true
		}
		fmt.Fprintf(&reg, "  call void @__kml_worker_register(ptr %s, ptr @%s, ptr @__kml_worker_thread)\n", e.internString(wm.Path), symbol)
	}
	e.workerRegistry = reg.String()
	e.emitWorkerRunLoop(anyModuleTask)
	return nil
}

// emitWorkerRunLoop defines @__kml_worker_run_loop() -> i64, what a worker
// thread runs after its modules evaluate: the event loop, then the exit code
// the parent hears (process.exitCode). With a worker module task it is
// main()'s shape from TDD-00224: run the loop; if the module task parked
// asking for an inline loop run (@__kml_module_run_loop), resume it and run
// the loop again; when the loop finally returns with the module promise
// still pending, print Node's warning and end with code 13.
func (e *Emitter) emitWorkerRunLoop(anyModuleTask bool) {
	code, pre := "0", ""
	if e.usedProcessLifecycle {
		pre = "  %code = load i64, ptr @__kml_process_exit_code, align 8\n"
		code = "%code"
	}
	if !anyModuleTask {
		e.emitGlobal(fmt.Sprintf("define i64 @__kml_worker_run_loop() {\nentry:\n  call void @__kml_event_loop_run()\n%s  ret i64 %s\n}", pre, code))
		return
	}
	e.ensureModuleTaskRuntime()
	e.ensureWriteDecl()
	e.ensureLoopTurn() // owns @.kml_tla_unsettled
	e.emitGlobal(fmt.Sprintf(`define i64 @__kml_worker_run_loop() {
entry:
  br label %%loop
loop:
  call void @__kml_event_loop_run()
  %%wants = load i1, ptr @__kml_module_wants_loop, align 1
  br i1 %%wants, label %%resume, label %%check
resume:
  store i1 false, ptr @__kml_module_wants_loop, align 1
  %%mt = load ptr, ptr @__kml_module_task, align 8
  call void @__kml_task_resume(ptr %%mt)
  br label %%loop
check:
  %%prom = load ptr, ptr @__kml_module_promise, align 8
  %%hasprom = icmp ne ptr %%prom, null
  br i1 %%hasprom, label %%state, label %%done
state:
  %%st_p = getelementptr %s, ptr %%prom, i32 0, i32 0
  %%st = load i64, ptr %%st_p, align 8
  %%pending = icmp eq i64 %%st, 0
  br i1 %%pending, label %%unsettled, label %%done
unsettled:
  %%w = call i64 @write(i32 2, ptr @.kml_tla_unsettled, i64 %d)
  ret i64 13
done:
%s  ret i64 %s
}`, promiseStructIR, len(tlaUnsettledMsg), pre, code))
}

func stringLiteralArg(args []ast.Expression, idx int, what string, pos ast.Pos) (string, error) {
	if idx >= len(args) {
		return "", fmt.Errorf("%d:%d: %s requires a string-literal event name", pos.Line, pos.Col, what)
	}
	lit, ok := args[idx].(*ast.StringLiteral)
	if !ok {
		return "", fmt.Errorf("%d:%d: %s requires a string-literal event name", pos.Line, pos.Col, what)
	}
	return lit.Value, nil
}
