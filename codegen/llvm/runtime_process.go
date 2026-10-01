package llvm

import (
	"fmt"
)

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

// ensureProcessUptime declares the process-start monotonic timestamp global,
// its capture function (@__kml_proc_uptime_init, called once at main start),
// and @__kml_process_uptime() → seconds-since-start as a double.
func (e *Emitter) ensureProcessUptime() {
	if e.usedProcessUptime {
		return
	}
	e.usedProcessUptime = true
	e.ensureClockGettime()
	e.emitGlobal("@__kml_proc_start_ns = internal global i64 0, align 8")
	clk := e.monotonicClockID()
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_proc_uptime_init() {
entry:
  %%ts = alloca { i64, i64 }, align 8
  call i32 @clock_gettime(i32 %s, ptr %%ts)
  %%sp = getelementptr { i64, i64 }, ptr %%ts, i32 0, i32 0
  %%np = getelementptr { i64, i64 }, ptr %%ts, i32 0, i32 1
  %%s = load i64, ptr %%sp, align 8
  %%n = load i64, ptr %%np, align 8
  %%sns = mul i64 %%s, 1000000000
  %%tot = add i64 %%sns, %%n
  store i64 %%tot, ptr @__kml_proc_start_ns, align 8
  ret void
}
define double @__kml_process_uptime() {
entry:
  %%ts = alloca { i64, i64 }, align 8
  call i32 @clock_gettime(i32 %s, ptr %%ts)
  %%sp = getelementptr { i64, i64 }, ptr %%ts, i32 0, i32 0
  %%np = getelementptr { i64, i64 }, ptr %%ts, i32 0, i32 1
  %%s = load i64, ptr %%sp, align 8
  %%n = load i64, ptr %%np, align 8
  %%sns = mul i64 %%s, 1000000000
  %%now = add i64 %%sns, %%n
  %%start = load i64, ptr @__kml_proc_start_ns, align 8
  %%diff = sub i64 %%now, %%start
  %%df = sitofp i64 %%diff to double
  %%secs = fdiv double %%df, 1000000000.0
  ret double %%secs
}`, clk, clk))
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
	e.emitGlobal("@__kml_process_exit_code = internal " + e.isolateTLS() + "global i64 0, align 8")
	e.emitGlobal("@__kml_exit_ran = internal " + e.isolateTLS() + "global i1 0, align 1")
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_run_exit_handlers(i64 %%code) {
entry:
  %%ran = load i1, ptr @__kml_exit_ran, align 1
  br i1 %%ran, label %%done, label %%run
run:
  store i1 1, ptr @__kml_exit_ran, align 1
  %%code_d = sitofp i64 %%code to double
  %%bits = bitcast double %%code_d to i64
  %%enc = add i64 %%bits, %d
  %%r = call i1 @__kml_process_hook_call(i32 0, i64 %%enc, i64 %d)
  br label %%done
done:
  ret void
}`, nbDoubleOffset, nbUndefined))
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
// raises — 0 'exit', 1 'uncaughtException', 2 'unhandledRejection' — and
// lib/native.d.ts's processHook/processUnhook, which the process emitter
// (lib/node/internal_process.ts) registers them with. A hook takes two `any`
// words; @__kml_process_hook_call runs one and reports whether it was set.
func (e *Emitter) ensureProcessHooks() {
	if e.fnDecls["__kml_process_hook_call"] {
		return
	}
	e.fnDecls["__kml_process_hook_call"] = true
	e.usedProcessLifecycle = true
	e.emitGlobal("@__kml_phook_inv = internal " + e.isolateTLS() + "global [4 x ptr] zeroinitializer")
	e.emitGlobal("@__kml_phook_clo = internal " + e.isolateTLS() + "global [4 x ptr] zeroinitializer")
	e.emitGlobal(`
define i1 @__kml_process_hook_call(i32 %which, i64 %a, i64 %b) {
entry:
  %i = zext i32 %which to i64
  %ip = getelementptr [4 x ptr], ptr @__kml_phook_inv, i64 0, i64 %i
  %inv = load ptr, ptr %ip, align 8
  %has = icmp ne ptr %inv, null
  br i1 %has, label %call, label %none
call:
  %cp = getelementptr [4 x ptr], ptr @__kml_phook_clo, i64 0, i64 %i
  %clo = load ptr, ptr %cp, align 8
  call void %inv(ptr %clo, i64 %a, i64 %b)
  ret i1 1
none:
  ret i1 0
}
define void @__kml_native_process_hook(double %which, ptr %inv, ptr %clo) {
entry:
  %i = fptosi double %which to i64
  %ip = getelementptr [4 x ptr], ptr @__kml_phook_inv, i64 0, i64 %i
  %cp = getelementptr [4 x ptr], ptr @__kml_phook_clo, i64 0, i64 %i
  store ptr %clo, ptr %cp, align 8
  store ptr %inv, ptr %ip, align 8
  ret void
}
define void @__kml_native_process_unhook(double %which) {
entry:
  %i = fptosi double %which to i64
  %ip = getelementptr [4 x ptr], ptr @__kml_phook_inv, i64 0, i64 %i
  store ptr null, ptr %ip, align 8
  ret void
}`)
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
	e.ensureFree()
	e.emitGlobal("declare ptr @getcwd(ptr noundef, i64 noundef)")
	e.emitGlobal(`
define ptr @__kml_process_cwd() {
entry:
  ; getcwd's buffer is a C string: copied into a runtime string, whose
  ; length header every string operation reads.
  %r = call ptr @getcwd(ptr null, i64 0)
  %s = call ptr @__kml_str_from_cstr(ptr %r)
  call void @free(ptr %r)
  ret ptr %s
}`)
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
	e.ensureMalloc()
	// A program that reads process.execPath can spawn itself. Guard against the
	// interpreter-flag self-fork bomb (see ensureNodeInterpFlagGuard).
	e.ensureNodeInterpFlagGuard()
	if e.opts.Target.OS() == "darwin" {
		e.declareFn("_NSGetExecutablePath", "declare i32 @_NSGetExecutablePath(ptr noundef, ptr noundef)")
		e.declareFn("realpath", "declare ptr @realpath(ptr noundef, ptr noundef)")
		e.emitGlobal(`
define ptr @__kml_execpath() {
entry:
  %sizep = alloca i32
  store i32 4096, ptr %sizep
  %buf = call ptr @malloc(i64 4096)
  %rc = call i32 @_NSGetExecutablePath(ptr %buf, ptr %sizep)
  %res = call ptr @realpath(ptr %buf, ptr null)
  %isnull = icmp eq ptr %res, null
  br i1 %isnull, label %fallback, label %ok
fallback:
  ret ptr %buf
ok:
  ret ptr %res
}`)
		return
	}
	// Linux and other /proc-bearing systems.
	e.emitGlobal(`@__kml_procself_exe = private unnamed_addr constant [15 x i8] c"/proc/self/exe\00"`)
	e.ensureReadlinkDecl()
	e.emitGlobal(`
define ptr @__kml_execpath() {
entry:
  %buf = call ptr @malloc(i64 4097)
  %n = call i64 @readlink(ptr @__kml_procself_exe, ptr %buf, i64 4096)
  %neg = icmp slt i64 %n, 0
  br i1 %neg, label %fail, label %ok
ok:
  %endp = getelementptr i8, ptr %buf, i64 %n
  store i8 0, ptr %endp
  ret ptr %buf
fail:
  store i8 0, ptr %buf
  ret ptr %buf
}`)
}

func (e *Emitter) ensureGetpid() {
	if e.usedGetpid {
		return
	}
	e.usedGetpid = true
	e.emitGlobal("declare i32 @getpid()")
	e.emitGlobal(`
define i64 @__kml_getpid() {
entry:
  %r = call i32 @getpid()
  %r64 = sext i32 %r to i64
  ret i64 %r64
}`)
}

// signalNumbers is the *target's* signal-name table for process.kill(pid, name)
// (ADR-00728): the names Node's os.constants.signals exposes there, with the
// numbers that platform's kill() takes — Linux's on Linux (and on Windows,
// where the shim accepts Linux numbers; SIGBREAK is libuv's 21), Darwin's on
// macOS. Node rejects a name outside that table with ERR_UNKNOWN_SIGNAL, which
// the compile-time rejection / runtime -1 mirror.
//
// A function, not a package-level var: `e.opts.Target.OS()` answers from the
// `--target` flag, which is parsed long after package initialisation would have
// frozen the table to the *host*. As a var, a macOS→Linux cross-compile
// ([ADR-00813](../../docs/adr/ADR-00813.md)) emitted Darwin's numbers into a
// Linux binary — SIGCHLD 20 for 17, so the child-exit self-pipe
// ([ADR-01023](../../docs/adr/ADR-01023.md)) would have watched a signal the
// kernel never raises.
func (e *Emitter) signalNumbers() map[string]int {
	switch e.opts.Target.OS() {
	case "windows":
		return map[string]int{"SIGHUP": 1, "SIGINT": 2, "SIGILL": 4, "SIGABRT": 6, "SIGFPE": 8, "SIGKILL": 9, "SIGSEGV": 11, "SIGTERM": 15, "SIGBREAK": 21, "SIGWINCH": 28}
	case "darwin":
		return map[string]int{"SIGHUP": 1, "SIGINT": 2, "SIGQUIT": 3, "SIGILL": 4, "SIGTRAP": 5, "SIGABRT": 6, "SIGEMT": 7, "SIGFPE": 8, "SIGKILL": 9, "SIGBUS": 10, "SIGSEGV": 11, "SIGSYS": 12, "SIGPIPE": 13, "SIGALRM": 14, "SIGTERM": 15, "SIGURG": 16, "SIGSTOP": 17, "SIGTSTP": 18, "SIGCONT": 19, "SIGCHLD": 20, "SIGTTIN": 21, "SIGTTOU": 22, "SIGIO": 23, "SIGXCPU": 24, "SIGXFSZ": 25, "SIGVTALRM": 26, "SIGPROF": 27, "SIGWINCH": 28, "SIGINFO": 29, "SIGUSR1": 30, "SIGUSR2": 31}
	default:
		return map[string]int{"SIGHUP": 1, "SIGINT": 2, "SIGQUIT": 3, "SIGILL": 4, "SIGTRAP": 5, "SIGABRT": 6, "SIGBUS": 7, "SIGFPE": 8, "SIGKILL": 9, "SIGUSR1": 10, "SIGSEGV": 11, "SIGUSR2": 12, "SIGPIPE": 13, "SIGALRM": 14, "SIGTERM": 15, "SIGCHLD": 17, "SIGCONT": 18, "SIGSTOP": 19, "SIGTSTP": 20, "SIGTTIN": 21, "SIGTTOU": 22, "SIGURG": 23, "SIGXCPU": 24, "SIGXFSZ": 25, "SIGVTALRM": 26, "SIGPROF": 27, "SIGWINCH": 28, "SIGIO": 29, "SIGPWR": 30, "SIGSYS": 31}
	}
}

// ensureSignalDecl emits the shared libc `signal` declaration exactly once —
// both the process signal-handler runtime and the http reactor's SIGPIPE-ignore
// (TDD-00214) reference @signal, so the decl is guarded independently of either.
func (e *Emitter) ensureSignalDecl() {
	if e.usedSignalDecl {
		return
	}
	e.usedSignalDecl = true
	e.emitGlobal("declare ptr @signal(i32 noundef, ptr noundef)")
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
	e.ensureSignalDecl()
	e.emitGlobal(`
define void @__kml_ignore_sigpipe() {
entry:
  %ign = call ptr @signal(i32 13, ptr inttoptr(i64 1 to ptr))
  ret void
}`)
}

// ensureSignalHandlerRuntime declares the machinery behind a process signal
// listener (TDD-00019; the process emitter starts a watcher per signal). POSIX signal handlers
// must be async-signal-safe: they can interrupt the program at literally any
// instruction, including mid-malloc, mid-longjmp, or mid-swapcontext fiber
// switch (see TDD-00006 on why coroutine/fiber suspension is already this
// fragile around this compiler's setjmp/longjmp exception model). So
// __kml_sig_handler, the only code that ever runs in real signal context,
// does the absolute minimum: one `store volatile` to a flag, nothing else.
// The watcher is only ever invoked later, from ordinary
// control flow at the top of the event loop's own iteration (see
// __kml_event_loop_run in runtime_http.go and __kml_timer_drain in
// emit_timers.go) — by construction, never from signal context itself.
//
// The pending flags are `i8`, always accessed `volatile` — required so
// -O2 can't cache a flag's value across the select() call or eliminate a
// store as dead, the LLVM-IR equivalent of C's mandatory
// `volatile sig_atomic_t` for this exact pattern.
//
// signal(), not sigaction(): sigaction() needs a hand-laid-out
// struct sigaction, and that struct's byte layout differs between Darwin
// and Linux (sa_mask size, presence of sa_restorer) — exactly the class of
// bug already hit once with ucontext_t (ADR-00051). signal()'s C signature
// is two scalars, no struct, identical on both platforms this compiler
// targets. select()/poll() are documented (Linux signal(7), equivalently
// on BSD/Darwin) to always return EINTR on a signal regardless of
// SA_RESTART, so signal()'s restart semantics don't matter for this
// design's correctness.
func (e *Emitter) ensureSignalHandlerRuntime() {
	if e.usedSignalHandler {
		return
	}
	e.usedSignalHandler = true
	e.ensureSignalDecl()
	// A watched signal's pending flag and its watcher (the invoker and closure
	// signalStart registered), by signal number. The handler only sets the
	// flag; the event loop's iteration runs the watcher (__kml_signal_dispatch).
	e.emitGlobal("@__kml_sig_pending = internal thread_local global [65 x i8] zeroinitializer")
	e.emitGlobal("@__kml_sig_inv = internal thread_local global [65 x ptr] zeroinitializer")
	e.emitGlobal("@__kml_sig_clo = internal thread_local global [65 x ptr] zeroinitializer")
	e.emitGlobal(`
define void @__kml_sig_handler(i32 %signum) {
entry:
  %ok = icmp ult i32 %signum, 65
  br i1 %ok, label %set, label %done
set:
  %i = zext i32 %signum to i64
  %p = getelementptr [65 x i8], ptr @__kml_sig_pending, i64 0, i64 %i
  store volatile i8 1, ptr %p, align 1
  br label %done
done:
  ret void
}
define void @__kml_signal_dispatch() {
entry:
  br label %loop
loop:
  %i = phi i64 [ 1, %entry ], [ %inext, %next ]
  %inb = icmp slt i64 %i, 65
  br i1 %inb, label %body, label %done
body:
  %pp = getelementptr [65 x i8], ptr @__kml_sig_pending, i64 0, i64 %i
  %pend = load volatile i8, ptr %pp, align 1
  %isset = icmp ne i8 %pend, 0
  br i1 %isset, label %fire, label %next
fire:
  store volatile i8 0, ptr %pp, align 1
  %ip = getelementptr [65 x ptr], ptr @__kml_sig_inv, i64 0, i64 %i
  %inv = load ptr, ptr %ip, align 8
  %has = icmp ne ptr %inv, null
  br i1 %has, label %call, label %next
call:
  %cp = getelementptr [65 x ptr], ptr @__kml_sig_clo, i64 0, i64 %i
  %clo = load ptr, ptr %cp, align 8
  %d = sitofp i64 %i to double
  call void %inv(ptr %clo, double %d)
  br label %next
next:
  %inext = add i64 %i, 1
  br label %loop
done:
  ret void
}`)
}

// ensureNativeSignal defines lib/native.d.ts's signalStart and signalStop:
// Node's signal_wrap over the handler above. Starting installs the handler
// (a watcher never holds the loop open, as Node unrefs its signal handles);
// stopping restores the default disposition (SIGPIPE's is ignored, as this
// runtime ignores it). A signal the host refuses (SIGKILL, SIGSTOP) is
// -EINVAL. On Windows the signal() shim (win32proc.c) raises SIGINT, SIGHUP,
// SIGBREAK and SIGWINCH; a watcher for another is accepted and never runs,
// as libuv's is.
func (e *Emitter) ensureNativeSignal() {
	if e.fnDecls["__kml_native_signal_start"] {
		return
	}
	e.fnDecls["__kml_native_signal_start"] = true
	e.ensureSignalHandlerRuntime()
	install := `  %old = call ptr @signal(i32 %n, ptr @__kml_sig_handler)
  %bad = icmp eq ptr %old, inttoptr (i64 -1 to ptr)
  br i1 %bad, label %einval, label %ok`
	restore := `  %dfl = select i1 %pipe, ptr inttoptr (i64 1 to ptr), ptr null
  %old = call ptr @signal(i32 %n, ptr %dfl)
  br label %done`
	e.emitGlobal(`
define double @__kml_native_signal_start(double %signo, ptr %inv, ptr %clo) {
entry:
  %n = fptosi double %signo to i32
  %lo = icmp slt i32 %n, 1
  %hi = icmp sgt i32 %n, 64
  %out = or i1 %lo, %hi
  br i1 %out, label %einval, label %range
range:
  %i = zext i32 %n to i64
  %ip = getelementptr [65 x ptr], ptr @__kml_sig_inv, i64 0, i64 %i
  %cp = getelementptr [65 x ptr], ptr @__kml_sig_clo, i64 0, i64 %i
  store ptr %clo, ptr %cp, align 8
  store ptr %inv, ptr %ip, align 8
` + install + `
einval:
  ret double -22.0
ok:
  ret double 0.0
}
define void @__kml_native_signal_stop(double %signo) {
entry:
  %n = fptosi double %signo to i32
  %lo = icmp slt i32 %n, 1
  %hi = icmp sgt i32 %n, 64
  %out = or i1 %lo, %hi
  br i1 %out, label %done, label %range
range:
  %i = zext i32 %n to i64
  %ip = getelementptr [65 x ptr], ptr @__kml_sig_inv, i64 0, i64 %i
  store ptr null, ptr %ip, align 8
  %pipe = icmp eq i32 %n, 13
` + restore + `
done:
  ret void
}`)
}
