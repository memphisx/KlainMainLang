package llvm

import (
	_ "embed"
	"fmt"
	"strconv"
)

// The process runtime (hooks, uptime, cwd, ids, exec path, signals; TDD-00240)
// lives in processsrc/process.c; the natives in runtime_process_native.go are
// declared against it.
//
//go:embed processsrc/process.c
var processSource string

// ProcessSource is the process runtime's C source.
func ProcessSource() string { return processSource }

// ProcessCFlags pass process.c the NaN-box constants; it is compiled the
// same way for every program (DeadStripFlags).
func (e *Emitter) ProcessCFlags() []string {
	return append([]string{"-DKML_NB_UNDEFINED=" + strconv.Itoa(nbUndefined) + "LL", "-DKML_NB_DOUBLE_OFFSET=" + strconv.FormatInt(nbDoubleOffset, 10) + "LL"}, DeadStripFlags(e.exportsRuntime())...)
}

// ensureProcessUptime declares @__kml_proc_uptime_init (called once at main
// start, capturing the monotonic start time) and @__kml_process_uptime() →
// seconds-since-start as a double (process.c).
func (e *Emitter) ensureProcessUptime() {
	if e.usedProcessUptime {
		return
	}
	e.usedProcessUptime = true
	e.emitGlobal(`declare void @__kml_proc_uptime_init()`)
}

// emitProcessLifecycleRuntime emits the process-lifecycle globals and the two
// hook functions __kml_throw / process.exit / main-end call:
//   - @__kml_run_exit_handlers(code): emits the process 'exit' event once
//     (through the hook the process emitter registered, lib/node/
//     internal_process.ts), used at normal program end, process.exit(), and
//     after an uncaughtException.
//   - @__kml_process_uncaught(tag, payload, rejection) -> i1: emits
//     'uncaughtException' with the thrown value and its origin, and returns 1
//     when something listens (so __kml_throw skips its default "Uncaught: ..."
//     print), else 0.
//
// Emitted whenever exceptions or any process-lifecycle surface is used.
func (e *Emitter) emitProcessLifecycleRuntime() {
	if e.fnDecls["__kml_process_uncaught"] {
		return
	}
	e.fnDecls["__kml_process_uncaught"] = true
	e.ensureProcessHooks()
	restore := e.beginDetachedFunc()
	rec := e.emitCaughtRecord("%tag", "%pay")
	box := e.emitCaughtToAny(Value{Ref: rec, Ty: TypeCaught})
	origin := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %%rejection, i64 %s, i64 %s", origin, e.emitNbEncodeDouble("1.0"), e.emitNbEncodeDouble("0.0")))
	has := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_process_hook_call(i32 1, i64 %s, i64 %s)", has, box.Ref, origin))
	e.emitInstr(fmt.Sprintf("ret i1 %s", has))
	body := e.allocas.String() + e.body.String()
	restore()
	e.writeFunc("i1 @__kml_process_uncaught(i8 %tag, i64 %pay, i1 %rejection)", body)
}

// emitCaughtRecord is the { i8 tag, i64 payload } caught-value aggregate
// built from its two words.
func (e *Emitter) emitCaughtRecord(tag, pay string) string {
	r0, r := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue { i8, i64 } undef, i8 %s, 0", r0, tag))
	e.emitInstr(fmt.Sprintf("%s = insertvalue { i8, i64 } %s, i64 %s, 1", r, r0, pay))
	return r
}

// ensureProcessHooks defines the runtime's hooks for the process events it
// raises (process.c) — 0 'exit', 1 'uncaughtException', 2 'unhandledRejection' — and
// lib/native.d.ts's processHook/processUnhook, which the process emitter
// (lib/node/internal_process.ts) registers them with. A hook takes two `any`
// words; @__kml_process_hook_call runs one and reports whether it was set.
func (e *Emitter) ensureProcessHooks() {
	if e.fnDecls["__kml_process_hook_call"] {
		return
	}
	e.fnDecls["__kml_process_hook_call"] = true
	e.usedProcessLifecycle = true
	e.emitGlobal(`declare zeroext i1 @__kml_process_hook_call(i32, i64, i64)`)
}
