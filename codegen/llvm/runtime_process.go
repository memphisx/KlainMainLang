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

// processNativeKeys are the fnDecls entries (runtime_process_native.go,
// ensureNativeSignal) whose use links process.c.
var processNativeKeys = []string{
	"__kml_native_process_cwd", "__kml_native_process_chdir", "__kml_native_process_uptime",
	"__kml_native_process_hrtime", "__kml_native_kill_pid", "__kml_native_process_memory",
	"__kml_native_process_umask", "__kml_native_process_id", "__kml_native_process_argc",
	"__kml_native_process_exec_path", "__kml_native_process_set_exit_code", "__kml_native_env_get",
	"__kml_native_signal_start", "__kml_process_hook_call",
}

// UsesProcessRuntime reports whether the program links process.c.
func (e *Emitter) UsesProcessRuntime() bool {
	if e.usedProcessLifecycle || e.usedProcessUptime || e.usedProcessCwd || e.usedExecPath ||
		e.usedGetpid || e.usedSigpipeIgnored || e.usedSignalHandler {
		return true
	}
	for _, k := range processNativeKeys {
		if e.fnDecls[k] {
			return true
		}
	}
	return false
}

// ProcessCFlags pass process.c the NaN-box constants; it is compiled the
// same way for every program (DeadStripFlags).
func (e *Emitter) ProcessCFlags() []string {
	return append([]string{"-DKML_NB_UNDEFINED=" + strconv.Itoa(nbUndefined) + "LL", "-DKML_NB_DOUBLE_OFFSET=" + strconv.FormatInt(nbDoubleOffset, 10) + "LL"}, DeadStripFlags(e.exportsRuntime())...)
}

// nodePlatformName maps the Go compiler's own runtime.GOOS to the string
// Node's process.platform would report on that host — a pure compile-time
// mapping, no runtime code at all, following the same "check the Go
// compiler's own OS, since it also runs clang moments later" reasoning as
// errnoAccessor/monotonicClockID.

func (e *Emitter) nodePlatformName() string {
	switch e.opts.Target.OS() {
	case "windows":
		return "win32"
	default:
		return e.opts.Target.OS() // "darwin", "linux", "freebsd", etc. already match Node's own strings
	}
}

// nodeArchName maps Go's GOARCH to Node's process.arch strings (amd64 → x64,
// 386 → ia32); arm64/arm/ppc64/s390x already match. This compiler builds for
// the host arch, so the value is a compile-time constant.
func (e *Emitter) nodeArchName() string {
	switch e.opts.Target.Arch() {
	case "amd64":
		return "x64"
	case "386":
		return "ia32"
	default:
		return e.opts.Target.Arch() // "arm64", "arm", "ppc64", "s390x", ... already match Node
	}
}

// ensureProcessUptime declares @__kml_proc_uptime_init (called once at main
// start, capturing the monotonic start time) and @__kml_process_uptime() →
// seconds-since-start as a double (process.c).
func (e *Emitter) ensureProcessUptime() {
	if e.usedProcessUptime {
		return
	}
	e.usedProcessUptime = true
	e.emitGlobal(`declare void @__kml_proc_uptime_init()
declare double @__kml_process_uptime()`)
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
	e.ensureProcessHooks()
	e.emitGlobal(`@__kml_process_exit_code = external thread_local global i64, align 8
declare void @__kml_run_exit_handlers(i64)`)
	restore := e.beginDetachedFunc()
	box := e.emitCaughtToAny(Value{Ref: "%rec", Ty: TypeCaught})
	origin := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %%rejection, i64 %s, i64 %s", origin, e.emitNbEncodeDouble("1.0"), e.emitNbEncodeDouble("0.0")))
	has := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_process_hook_call(i32 1, i64 %s, i64 %s)", has, box.Ref, origin))
	e.emitInstr(fmt.Sprintf("ret i1 %s", has))
	body := e.allocas.String() + e.body.String()
	restore()
	e.functions.WriteString(fmt.Sprintf(`
define i1 @__kml_process_uncaught(i8 %%tag, i64 %%pay, i1 %%rejection) {
entry:
  %%rec0 = insertvalue { i8, i64 } undef, i8 %%tag, 0
  %%rec = insertvalue { i8, i64 } %%rec0, i64 %%pay, 1
%s}
`, body))
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
	e.emitGlobal(`declare zeroext i1 @__kml_process_hook_call(i32, i64, i64)
declare void @__kml_native_process_hook(double, ptr, ptr)
declare void @__kml_native_process_unhook(double)`)
}

// ensureProcessCwd declares __kml_process_cwd: the current working directory
// via POSIX getcwd(NULL, 0) — the glibc/BSD extension where a NULL buffer
// tells getcwd to malloc a buffer sized exactly as needed itself, avoiding
// the usual "grow a fixed buffer until it fits" loop entirely. Verified
// directly (not assumed) that this auto-allocating form is supported on
// both platforms this compiler targets before relying on it.
func (e *Emitter) ensureProcessCwd() {
	if e.usedProcessCwd {
		return
	}
	e.usedProcessCwd = true
	e.ensureStrHeaderRuntime()
	e.emitGlobal("declare ptr @__kml_process_cwd()")
}

// ensureGetpid declares __kml_getpid: the current process ID via POSIX
// getpid(), sign-extended from the C int it actually returns to this
// compiler's i64 number representation.
// ensureExecPath emits `ptr @__kml_execpath()` returning the absolute,
// symlink-resolved path of the running executable — what Node's
// process.execPath guarantees (always absolute), which a raw argv[0] (possibly
// `./bin` or a bare PATH name) is not. Platform-specific, host-only (this
// compiler doesn't cross-compile): macOS uses `_NSGetExecutablePath` +
// `realpath`, Linux reads `/proc/self/exe`.
func (e *Emitter) ensureExecPath() {
	if e.usedExecPath {
		return
	}
	e.usedExecPath = true
	// A program that reads process.execPath can spawn itself. Guard against the
	// interpreter-flag self-fork bomb (see ensureNodeInterpFlagGuard).
	e.ensureNodeInterpFlagGuard()
	e.emitGlobal("declare ptr @__kml_execpath()")
}

func (e *Emitter) ensureGetpid() {
	if e.usedGetpid {
		return
	}
	e.usedGetpid = true
	// The cluster fork region reads getpid() directly.
	e.emitGlobal("declare i32 @getpid()")
	e.emitGlobal("declare i64 @__kml_getpid()")
}

// ensureSigpipeIgnored emits __kml_ignore_sigpipe, which sets SIGPIPE (signal 13
// on both Linux and Darwin) to SIG_IGN so a client disconnecting mid-write
// unwinds the connection via a write() EPIPE rather than killing the whole
// server process (TDD-00214). Global disposition is deliberate and one-shot;
// unlike SIGINT/SIGTERM there is no per-signal user handler to reconcile.
func (e *Emitter) ensureSigpipeIgnored() {
	if e.usedSigpipeIgnored {
		return
	}
	e.usedSigpipeIgnored = true
	e.emitGlobal("declare void @__kml_ignore_sigpipe()")
}

// ensureSignalHandlerRuntime links process.c's signal watchers (TDD-00019;
// the process emitter starts one per signal). On POSIX a watcher runs from
// its uv_signal_t callback; on Windows a signal only marks itself pending
// and runs from the top of the event loop's next pass, never from signal
// context.
func (e *Emitter) ensureSignalHandlerRuntime() {
	e.usedSignalHandler = true
}

// ensureNativeSignal defines lib/native.d.ts's signalStart and signalStop:
// Node's signal_wrap. On POSIX a watcher is an unref'd uv_signal_t on the
// event loop (loop.c), so a watcher never holds the loop open; stopping
// restores the default disposition (SIGPIPE's is ignored, as this runtime
// ignores it). A signal the host refuses (SIGKILL, SIGSTOP) is -EINVAL. On
// Windows the handler above stays until TDD-00244 Stage 6: the signal() shim
// (win32proc.c) raises SIGINT, SIGHUP, SIGBREAK and SIGWINCH; a watcher for
// another is accepted and never runs, as libuv's is.
func (e *Emitter) ensureNativeSignal() {
	if e.fnDecls["__kml_native_signal_start"] {
		return
	}
	e.fnDecls["__kml_native_signal_start"] = true
	e.ensureSignalHandlerRuntime()
	e.ensureHTTPRuntime() // loop.c holds the uv_signal_t handles
	e.emitGlobal(`declare double @__kml_native_signal_start(double, ptr, ptr)
declare void @__kml_native_signal_stop(double)`)
}
