// emit_worker.go — a worker module's entry: the modules a Worker evaluates on
// its own thread (every builtin module, the user modules it imports, then
// its own top level), emitted into `void @__kml_worker_entry_<i>()`,
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
		restoreBuilder := e.beginThunkEmit()
		savedScopes := e.scopes
		savedRetType := e.currentRetType
		e.scopes = nil
		e.currentRetType = TypeVoid
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
		emitErr = e.emitLibInitCalls(append(append([]ast.Statement{}, wm.Deps...), wm.Body...), e.emitStmt)
		if emitErr == nil {
			e.emitTerminator("ret void")
			if moduleTask {
				e.writeFunc(fmt.Sprintf("internal void @%s_body("+irPtr+" %%__kml_module_args)", symbol), e.allocas.String()+e.body.String())
				e.ensureTaskRuntime()
				e.writeFunc("void @"+symbol+"()", fmt.Sprintf(`  %%p = call `+irPtr+` @__kml_task_alloc_promise()
  store `+irPtr+` %%p, `+irPtr+` @__kml_module_promise, align 8
  %%t = call `+irPtr+` @__kml_spawn_task_ex(`+irPtr+` @%s_body, `+irPtr+` null, `+irPtr+` %%p, i64 %d, `+irPtr+` @__kml_module_trampoline)
  ret void
`, symbol, e.moduleTaskStackBytes()))
			} else {
				e.writeFunc(fmt.Sprintf("void @%s()", symbol), e.allocas.String()+e.body.String())
			}
		}

		e.inModuleTask = savedInModuleTask
		e.currentWorkerMod = ""
		restoreBuilder()
		e.scopes = savedScopes
		e.currentRetType = savedRetType
		if emitErr != nil {
			return fmt.Errorf("worker module %s: %w", wm.Path, emitErr)
		}
		if moduleTask {
			anyModuleTask = true
		}
		fmt.Fprintf(&reg, "  call void @__kml_worker_register("+irPtr+" %s, "+irPtr+" @%s, "+irPtr+" @__kml_worker_thread)\n", e.internString(wm.Path), symbol)
	}
	e.workerRegistry = reg.String()
	e.emitWorkerRunLoop(anyModuleTask)
	return nil
}

// emitWorkerRunLoop defines @__kml_worker_run_loop() -> i64, what a worker
// thread runs after its modules evaluate: the event loop, then the exit code
// the parent hears (process.exitCode). With a worker module task it is
// main()'s shape from TDD-00224: run the loop; if the module task parked
// asking for an inline loop run (__kml_module_park_for_loop), resume it and run
// the loop again; when the loop finally returns with the module promise
// still pending, print Node's warning and end with code 13.
func (e *Emitter) emitWorkerRunLoop(anyModuleTask bool) {
	code, pre := "0", ""
	if e.usedProcessLifecycle {
		pre = "  %code = load i64, " + irPtr + " @__kml_process_exit_code, align 8\n"
		code = "%code"
	}
	if !anyModuleTask {
		e.writeFunc("i64 @__kml_worker_run_loop()", fmt.Sprintf("  call void @__kml_event_loop_run()\n%s  ret i64 %s\n", pre, code))
		return
	}
	e.ensureTaskRuntime()
	e.ensureLoopTurn() // owns @.kml_tla_unsettled
	e.writeFunc("i64 @__kml_worker_run_loop()", fmt.Sprintf(`  br label %%loop
loop:
  call void @__kml_event_loop_run()
  %%wants = load i1, `+irPtr+` @__kml_module_wants_loop, align 1
  br i1 %%wants, label %%resume, label %%check
resume:
  store i1 false, `+irPtr+` @__kml_module_wants_loop, align 1
  %%mt = load `+irPtr+`, `+irPtr+` @__kml_module_task, align 8
  call void @__kml_task_resume(`+irPtr+` %%mt)
  br label %%loop
check:
  %%prom = load `+irPtr+`, `+irPtr+` @__kml_module_promise, align 8
  %%hasprom = icmp ne `+irPtr+` %%prom, null
  br i1 %%hasprom, label %%state, label %%done
state:
  %%st_p = getelementptr %s, `+irPtr+` %%prom, i32 0, i32 0
  %%st = load i64, `+irPtr+` %%st_p, align 8
  %%pending = icmp eq i64 %%st, 0
  br i1 %%pending, label %%unsettled, label %%done
unsettled:
  %%w = call i64 @write(i32 2, `+irPtr+` @.kml_tla_unsettled, i64 %d)
  ret i64 13
done:
%s  ret i64 %s
`, promiseStructIR, len(tlaUnsettledMsg), pre, code))
}
