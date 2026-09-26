package llvm

import (
	"fmt"
	"sort"
	"strings"
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
//   - @__kml_run_exit_handlers(code): runs the registered 'exit' listener once
//     (a no-op when none is set), used at normal program end, process.exit(),
//     and after an uncaughtException.
//   - @__kml_process_uncaught(err) -> i1: runs the registered
//     'uncaughtException' listener and returns 1 if one was set (so __kml_throw
//     skips its default "Uncaught: ..." print+exit), else 0.
//
// Both no-op naturally when their handler global is null, so this single
// definition serves whether or not a listener was registered. Emitted whenever
// exceptions or any process-lifecycle surface is used.
func (e *Emitter) emitProcessLifecycleRuntime() {
	e.emitGlobal("@__kml_process_exit_code = internal global i64 0, align 8")
	e.emitGlobal("@__kml_exit_handler = internal global ptr null, align 8")
	e.emitGlobal("@__kml_uncaught_handler = internal global ptr null, align 8")
	e.emitGlobal("@__kml_exit_ran = internal global i1 0, align 1")
	// The test-module's exit-time verifier (TDD-00122) runs once, in the guarded
	// `run` path, AFTER any user `process.on('exit')` handler — so a user
	// handler's own assertions still surface, and a failed mustCall expectation
	// exits the process non-zero from here.
	testVerify := ""
	if e.usedNodeTestRuntime {
		// The node:test summary runs first (module after() hooks + totals +
		// nonzero exit on failures, TDD-00140), then the mustCall verifier.
		testVerify += "  call void @__kml_ntest_summary()\n"
	}
	if e.usedTestRuntime {
		testVerify += "  call void @__kml_test_verify()\n"
	}
	e.emitGlobal(`
define void @__kml_run_exit_handlers(i64 %code) {
entry:
  %ran = load i1, ptr @__kml_exit_ran, align 1
  br i1 %ran, label %done, label %run
run:
  store i1 1, ptr @__kml_exit_ran, align 1
  %h = load ptr, ptr @__kml_exit_handler, align 8
  %has = icmp ne ptr %h, null
  br i1 %has, label %fire, label %post
fire:
  %fp_p = getelementptr { ptr, ptr }, ptr %h, i32 0, i32 0
  %fp = load ptr, ptr %fp_p, align 8
  %ep_p = getelementptr { ptr, ptr }, ptr %h, i32 0, i32 1
  %ep = load ptr, ptr %ep_p, align 8
  %code_d = sitofp i64 %code to double
  call void %fp(ptr %ep, double %code_d)
  br label %post
post:
` + testVerify + `  br label %done
done:
  ret void
}
define i1 @__kml_process_uncaught(ptr %err) {
entry:
  %h = load ptr, ptr @__kml_uncaught_handler, align 8
  %has = icmp ne ptr %h, null
  br i1 %has, label %fire, label %no
fire:
  %fp_p = getelementptr { ptr, ptr }, ptr %h, i32 0, i32 0
  %fp = load ptr, ptr %fp_p, align 8
  %ep_p = getelementptr { ptr, ptr }, ptr %h, i32 0, i32 1
  %ep = load ptr, ptr %ep_p, align 8
  call void %fp(ptr %ep, ptr %err)
  ret i1 1
no:
  ret i1 0
}`)
}

// ensureProcessHrtime declares @__kml_process_hrtime() → a malloc'd { i64 sec,
// i64 nsec } (the [seconds, nanoseconds] tuple), and @__kml_process_hrtime_ns()
// → total nanoseconds as an i64 (for hrtime.bigint).
func (e *Emitter) ensureProcessHrtime() {
	if e.usedProcessHrtime {
		return
	}
	e.usedProcessHrtime = true
	e.ensureClockGettime()
	e.ensureMalloc()
	clk := e.monotonicClockID()
	e.emitGlobal(fmt.Sprintf(`
define ptr @__kml_process_hrtime() {
entry:
  %%ts = alloca { i64, i64 }, align 8
  call i32 @clock_gettime(i32 %s, ptr %%ts)
  %%sp = getelementptr { i64, i64 }, ptr %%ts, i32 0, i32 0
  %%np = getelementptr { i64, i64 }, ptr %%ts, i32 0, i32 1
  %%s = load i64, ptr %%sp, align 8
  %%n = load i64, ptr %%np, align 8
  %%t = call ptr @malloc(i64 16)
  %%t0 = getelementptr { i64, i64 }, ptr %%t, i32 0, i32 0
  store i64 %%s, ptr %%t0, align 8
  %%t1 = getelementptr { i64, i64 }, ptr %%t, i32 0, i32 1
  store i64 %%n, ptr %%t1, align 8
  ret ptr %%t
}
define i64 @__kml_process_hrtime_ns() {
entry:
  %%ts = alloca { i64, i64 }, align 8
  call i32 @clock_gettime(i32 %s, ptr %%ts)
  %%sp = getelementptr { i64, i64 }, ptr %%ts, i32 0, i32 0
  %%np = getelementptr { i64, i64 }, ptr %%ts, i32 0, i32 1
  %%s = load i64, ptr %%sp, align 8
  %%n = load i64, ptr %%np, align 8
  %%sns = mul i64 %%s, 1000000000
  %%tot = add i64 %%sns, %%n
  ret i64 %%tot
}`, clk, clk))
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
	e.emitGlobal("declare ptr @getcwd(ptr noundef, i64 noundef)")
	e.emitGlobal(`
define ptr @__kml_process_cwd() {
entry:
  %r = call ptr @getcwd(ptr null, i64 0)
  ret ptr %r
}`)
}

// ensureProcessChdir declares __kml_process_chdir: changes the current
// working directory via POSIX chdir(), throwing the same "<opDesc> '<path>':
// <strerror>" Error shape fs's own failures already use (ensureFsThrow is
// generic over any path-taking operation, not fs-specific in what it needs).
func (e *Emitter) ensureProcessChdir() {
	if e.usedProcessChdir {
		return
	}
	e.usedProcessChdir = true
	e.ensureFsThrow()
	e.ensureChdirDecl()
	opDescPtr := e.internString("cannot change directory to")
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_process_chdir(ptr %%path) {
entry:
  %%r = call i32 @chdir(ptr %%path)
  %%failed = icmp ne i32 %%r, 0
  br i1 %%failed, label %%fail, label %%ok

fail:
  call void @__kml_fs_throw(ptr %s, ptr %s, ptr %%path)
  unreachable

ok:
  ret void
}`, opDescPtr, e.internString("chdir")))
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
		e.emitGlobal("declare i32 @_NSGetExecutablePath(ptr, ptr)")
		e.emitGlobal("declare ptr @realpath(ptr, ptr)")
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

// ensureProcessKill declares __kml_process_kill: sends a signal to a process
// via POSIX kill(), throwing a catchable Error built from strerror(errno) on
// failure (e.g. ESRCH for "no such process") — the same "surface a real OS
// failure as a catchable Error" convention as everywhere else, just with a
// numeric pid/signal in the message instead of a path.
func (e *Emitter) ensureProcessKill() {
	if e.usedProcessKill {
		return
	}
	e.usedProcessKill = true
	e.ensureMalloc()
	e.ensureStrlen()
	e.ensureSprintf()
	e.ensureStrHeaderRuntime() // error .message must be headered for concat/=== (TDD-00120)
	e.ensureExceptionHelpers()
	e.ensureErrnoAccessor()
	e.ensureStrerror()
	e.ensureCalloc()
	e.ensureErrnoCode()
	accessor := e.errnoAccessor()
	e.ensureCPKill() // single owner of `declare i32 @kill` (shared with child.kill)
	fmtPtr := e.internString("kill(pid=%lld, signal=%lld): %s")
	killErrNamePtr := e.internString("Error")
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_process_kill(i64 %%pid, i64 %%sig) {
entry:
  %%pid32 = trunc i64 %%pid to i32
  %%sig32 = trunc i64 %%sig to i32
  %%r = call i32 @kill(i32 %%pid32, i32 %%sig32)
  %%failed = icmp ne i32 %%r, 0
  br i1 %%failed, label %%fail, label %%ok

fail:
  %%errno_ptr = call ptr @%[1]s()
  %%errno_val = load i32, ptr %%errno_ptr, align 4
  %%errmsg = call ptr @strerror(i32 %%errno_val)
  %%errlen = call i64 @strlen(ptr %%errmsg)
  %%bufsize = add i64 %%errlen, 48
  %%buf = call ptr @__kml_str_alloc(i64 %%bufsize)
  call i32 (ptr, ptr, ...) @sprintf(ptr %%buf, ptr %[2]s, i64 %%pid, i64 %%sig, ptr %%errmsg)
  call void @__kml_str_finalize(ptr %%buf)
  ; The full error shape, zero-filled: a catch handler reads .code/.errno/
  ; .syscall off this object, so it must be the whole struct — and those three
  ; are what Node's kill error carries (code 'ESRCH', syscall 'kill').
  %%errobj = call ptr @calloc(i64 1, i64 %[4]d)
  %%errobj.kind = getelementptr %[5]s, ptr %%errobj, i32 0, i32 0
  store i64 281474976710656, ptr %%errobj.kind, align 8
  %%errobj.msg = getelementptr %[5]s, ptr %%errobj, i32 0, i32 1
  store ptr %%buf, ptr %%errobj.msg, align 8
  %%errobj.name = getelementptr %[5]s, ptr %%errobj, i32 0, i32 2
  store ptr %[3]s, ptr %%errobj.name, align 8
  %%code = call ptr @__kml_errno_code(i32 %%errno_val)
  %%errobj.code = getelementptr %[5]s, ptr %%errobj, i32 0, i32 3
  store ptr %%code, ptr %%errobj.code, align 8
  %%errno_d = sitofp i32 %%errno_val to double
  %%errobj.errcode = getelementptr %[5]s, ptr %%errobj, i32 0, i32 4
  store double %%errno_d, ptr %%errobj.errcode, align 8
  %%errobj.errstr = getelementptr %[5]s, ptr %%errobj, i32 0, i32 5
  store ptr %%errmsg, ptr %%errobj.errstr, align 8
  %%errobj.syscall = getelementptr %[5]s, ptr %%errobj, i32 0, i32 6
  store ptr %[6]s, ptr %%errobj.syscall, align 8
  %%uv = call i32 @__kml_uv_errno(i32 %%errno_val)
  %%uvd = sitofp i32 %%uv to double
  %%errobj.errno = getelementptr %[5]s, ptr %%errobj, i32 0, i32 8
  store double %%uvd, ptr %%errobj.errno, align 8
  call void @__kml_throw(ptr %%errobj)
  unreachable

ok:
  ret void
}`, accessor, fmtPtr, killErrNamePtr, errorObjType.StructSize(), errorObjType.StructIR(), e.internString("kill")))
}

// ensureSignalHandlerRuntime declares the shared machinery behind
// process.on('SIGINT'/'SIGTERM', handler) — TDD-00019. POSIX signal handlers
// must be async-signal-safe: they can interrupt the program at literally any
// instruction, including mid-malloc, mid-longjmp, or mid-swapcontext fiber
// switch (see TDD-00006 on why coroutine/fiber suspension is already this
// fragile around this compiler's setjmp/longjmp exception model). So
// __kml_sig_handler, the only code that ever runs in real signal context,
// does the absolute minimum: one `store volatile` to a flag, nothing else.
// The registered TS closure is only ever invoked later, from ordinary
// control flow at the top of the event loop's own iteration (see
// __kml_event_loop_run in runtime_http.go and __kml_timer_drain in
// emit_timers.go) — by construction, never from signal context itself.
//
// Both pending flags are `i8`, always accessed `volatile` — required so
// -O2 can't cache a flag's value across the select() call or eliminate a
// store as dead, the LLVM-IR equivalent of C's mandatory
// `volatile sig_atomic_t` for this exact pattern. Both closure globals hold
// a `ptr` to a {funcPtr, envPtr} closure header (null = unregistered).
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

// ensureSignalFromName defines __kml_signal_from_name(ptr name) -> i64: the
// runtime lookup for a non-literal signal name in process.kill, over the same
// target table; -1 for an unknown name (kill() then fails with EINVAL and the
// existing throw path reports it).
func (e *Emitter) ensureSignalFromName() {
	if e.usedSignalFromName {
		return
	}
	e.usedSignalFromName = true
	e.ensureStrcmp()
	table := e.signalNumbers()
	names := make([]string, 0, len(table))
	for n := range table {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString("\ndefine i64 @__kml_signal_from_name(ptr %name) {\nentry:\n  br label %c0\n")
	for i, n := range names {
		ptr := e.internString(n)
		fmt.Fprintf(&b, "c%d:\n  %%r%d = call i32 @strcmp(ptr %%name, ptr %s)\n  %%eq%d = icmp eq i32 %%r%d, 0\n  br i1 %%eq%d, label %%hit%d, label %%c%d\nhit%d:\n  ret i64 %d\n", i, i, ptr, i, i, i, i, i+1, i, table[n])
	}
	fmt.Fprintf(&b, "c%d:\n  ret i64 -1\n}", len(names))
	e.emitGlobal(b.String())
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

func (e *Emitter) ensureSignalHandlerRuntime() {
	if e.usedSignalHandler {
		return
	}
	e.usedSignalHandler = true
	e.ensureSignalDecl()
	e.emitGlobal("@__kml_sigint_pending = internal thread_local global i8 0")
	e.emitGlobal("@__kml_sigterm_pending = internal thread_local global i8 0")
	// TDD-00031: SIGWINCH (terminal resize) is a third literal on the same
	// flag/closure machinery — SIGWINCH is signal 28 on both Linux and Darwin.
	e.emitGlobal("@__kml_sigwinch_pending = internal thread_local global i8 0")
	// SIGBREAK (ADR-00728): Ctrl+Break at a Windows console, libuv's signal
	// 21 there; a listener is accepted on every host, as in Node, and only a
	// Windows build ever sets the flag (ensureSignalRegisteredSigbreak).
	e.emitGlobal("@__kml_sigbreak_pending = internal thread_local global i8 0")
	e.emitGlobal("@__kml_sigint_closure = internal thread_local global ptr null")
	e.emitGlobal("@__kml_sigterm_closure = internal thread_local global ptr null")
	e.emitGlobal("@__kml_sigwinch_closure = internal thread_local global ptr null")
	e.emitGlobal("@__kml_sigbreak_closure = internal thread_local global ptr null")
	e.emitGlobal(`
define void @__kml_sig_handler(i32 %signum) {
entry:
  %isint = icmp eq i32 %signum, 2
  br i1 %isint, label %setint, label %checkterm

setint:
  store volatile i8 1, ptr @__kml_sigint_pending
  ret void

checkterm:
  %isterm = icmp eq i32 %signum, 15
  br i1 %isterm, label %setterm, label %checkwinch

setterm:
  store volatile i8 1, ptr @__kml_sigterm_pending
  ret void

checkwinch:
  %iswinch = icmp eq i32 %signum, 28
  br i1 %iswinch, label %setwinch, label %checkbreak

setwinch:
  store volatile i8 1, ptr @__kml_sigwinch_pending
  ret void

checkbreak:
  %isbreak = icmp eq i32 %signum, 21
  br i1 %isbreak, label %setbreak, label %done

setbreak:
  store volatile i8 1, ptr @__kml_sigbreak_pending
  ret void

done:
  ret void
}`)
}

// ensureSignalRegisteredSigbreak installs __kml_sig_handler for SIGBREAK
// (Ctrl+Break, libuv's signal 21) on Windows only — on POSIX hosts 21 is
// SIGTTIN and Node never raises SIGBREAK there, so the listener is merely
// accepted (ADR-00728).
func (e *Emitter) ensureSignalRegisteredSigbreak() {
	if e.usedSignalSigbreak {
		return
	}
	e.usedSignalSigbreak = true
	e.ensureSignalHandlerRuntime()
	if e.opts.Target.OS() == "windows" {
		e.emitInstr("call ptr @signal(i32 21, ptr @__kml_sig_handler)")
	}
}

// ensureSignalRegisteredSigint / ensureSignalRegisteredSigterm each call
// signal() exactly once per compiled binary (idempotent, matching every
// other ensure*() in this compiler) to install __kml_sig_handler for that
// one signal — called from emitProcessOn (emit_process.go) the first time
// process.on('SIGINT'/'SIGTERM', ...) is compiled for that signal name. A
// program that never calls process.on for a given signal never calls
// signal() for it either, so that signal's OS-level disposition stays the
// untouched default (SIG_DFL — terminates immediately), exactly matching
// this compiler's pre-existing behavior with zero overhead.
func (e *Emitter) ensureSignalRegisteredSigint() {
	if e.usedSignalSigint {
		return
	}
	e.usedSignalSigint = true
	e.ensureSignalHandlerRuntime()
	e.emitInstr("call ptr @signal(i32 2, ptr @__kml_sig_handler)")
}

func (e *Emitter) ensureSignalRegisteredSigterm() {
	if e.usedSignalSigterm {
		return
	}
	e.usedSignalSigterm = true
	e.ensureSignalHandlerRuntime()
	e.emitInstr("call ptr @signal(i32 15, ptr @__kml_sig_handler)")
}

// ensureSignalRegisteredSigwinch installs __kml_sig_handler for SIGWINCH
// (terminal resize, signal 28 on both Linux and Darwin) — TDD-00031. Same
// idempotent, register-only-if-used posture as the SIGINT/SIGTERM helpers.
func (e *Emitter) ensureSignalRegisteredSigwinch() {
	if e.usedSignalSigwinch {
		return
	}
	e.usedSignalSigwinch = true
	e.ensureSignalHandlerRuntime()
	e.emitInstr("call ptr @signal(i32 28, ptr @__kml_sig_handler)")
}
