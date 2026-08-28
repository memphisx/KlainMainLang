package llvm

import (
	"fmt"
	"runtime"
)

// stdinGlobalName returns the actual external symbol backing C's `stdin`
// macro on whatever OS is running this compiler right now (and will
// therefore also run clang moments later). Verified directly rather than
// guessed: on Darwin, `stdin` expands (via the preprocessor) to `__stdinp`,
// a differently-named global `FILE*` — not literally "stdin" at the link
// level at all. glibc (Linux) exposes it as the plain symbol `stdin`
// itself, a long-stable convention. The same class of platform check as
// errnoAccessor/monotonicClockID.
func stdinGlobalName() string {
	if runtime.GOOS == "darwin" {
		return "__stdinp"
	}
	return "stdin"
}

// ensureReadLineSync declares __kml_read_line_sync: reads one line from
// stdin via POSIX getline() (handles arbitrarily long lines, unlike a
// fixed-size fgets buffer), strips a trailing "\n" (and a preceding "\r",
// for input from CRLF-terminated sources), and returns null at EOF — the
// same "possibly-null string, check with ?? or an explicit comparison"
// convention already used for process.env (emit_process.go).
func (e *Emitter) ensureReadLineSync() {
	if e.usedReadLineSync {
		return
	}
	e.usedReadLineSync = true
	e.ensureStrlen()
	e.ensureStrHeaderRuntime()
	e.ensureFree()
	stdinName := stdinGlobalName()
	e.emitGlobal(fmt.Sprintf("@%s = external global ptr", stdinName))
	e.emitGlobal("declare i64 @getline(ptr noundef, ptr noundef, ptr noundef)")
	e.emitGlobal(fmt.Sprintf(`
define ptr @__kml_read_line_sync() {
entry:
  %%lineptr = alloca ptr, align 8
  %%n = alloca i64, align 8
  store ptr null, ptr %%lineptr, align 8
  store i64 0, ptr %%n, align 8
  %%stdinval = load ptr, ptr @%s, align 8
  %%r = call i64 @getline(ptr %%lineptr, ptr %%n, ptr %%stdinval)
  %%iseof = icmp slt i64 %%r, 0
  br i1 %%iseof, label %%eof, label %%ok

eof:
  ret ptr null

ok:
  %%buf = load ptr, ptr %%lineptr, align 8
  %%len = call i64 @strlen(ptr %%buf)
  %%haslen = icmp sgt i64 %%len, 0
  br i1 %%haslen, label %%checknl, label %%done

checknl:
  %%lastidx = sub i64 %%len, 1
  %%lastp = getelementptr i8, ptr %%buf, i64 %%lastidx
  %%lastch = load i8, ptr %%lastp, align 1
  %%isnl = icmp eq i8 %%lastch, 10
  br i1 %%isnl, label %%stripnl, label %%done

stripnl:
  store i8 0, ptr %%lastp, align 1
  %%haslen2 = icmp sgt i64 %%lastidx, 0
  br i1 %%haslen2, label %%checkcr, label %%done

checkcr:
  %%cridx = sub i64 %%lastidx, 1
  %%crp = getelementptr i8, ptr %%buf, i64 %%cridx
  %%crch = load i8, ptr %%crp, align 1
  %%iscr = icmp eq i8 %%crch, 13
  br i1 %%iscr, label %%stripcr, label %%done

stripcr:
  store i8 0, ptr %%crp, align 1
  br label %%done

done:
  %%bufh = call ptr @__kml_str_from_cstr(ptr %%buf)
  call void @free(ptr %%buf)
  ret ptr %%bufh
}`, stdinName))
}

// ensureExecFileSync declares __kml_exec_file_sync: fork()s a child process,
// execvp()s it with argv = [file, ...args], captures the child's stdout via
// a pipe into a malloc'd, null-terminated string (grown via realloc
// doubling — the same growable-{ptr,i64,i64}-buffer shape __kml_fetch's
// curl write callback already uses), and waitpid()s for it to finish.
//
// V1 scope, narrowed the same way every other builtin here started narrow:
// stderr is inherited (visible on the terminal live, not captured —
// capturing both streams at once without deadlocking needs select()/poll()
// over two pipes, real complexity for a first pass); a non-zero exit status
// or a signal death throws a plain Error via the existing __kml_throw
// mechanism (same as fs's and fetch's failure paths), not a rich error
// object with .status/.stdout/.stderr fields like real Node's.
//
// The wait-status decoding (low 7 bits == 0 means "exited normally", exit
// code in bits 8-15; otherwise the low 7 bits are the killing signal) is
// the traditional Unix wait-status encoding, valid on both Linux and
// Darwin/BSD, and exhaustive here since waitpid is called with no WUNTRACED
// flag — a child can only ever be reported as exited or signaled, never
// stopped, so there's no third case to get wrong.
func (e *Emitter) ensureExecFileSync() {
	if e.usedExecFileSync {
		return
	}
	e.usedExecFileSync = true
	e.ensureMalloc()
	e.ensureRealloc()
	e.ensureMemcpy()
	e.ensureStrlen()
	e.ensureSprintf()
	e.ensureExceptionHelpers()
	e.ensureStrHeaderRuntime() // TDD-00120: header-copy the returned stdout string

	e.emitGlobal("declare i32 @pipe(ptr noundef)")
	e.ensureForkDecl()
	e.emitGlobal("declare i32 @dup2(i32 noundef, i32 noundef)")
	e.ensureCloseDecl()
	e.emitGlobal("declare i32 @execvp(ptr noundef, ptr noundef)")
	e.emitGlobal("declare void @_exit(i32 noundef) noreturn")
	e.ensureReadDecl()
	e.ensureWaitpidDecl()

	fmtExit := e.internString("Command failed with exit code %d: %s")
	fmtSig := e.internString("Command was terminated by signal %d: %s")
	errNamePtr := e.internString("Error")

	part1 := `
define ptr @__kml_exec_file_sync(ptr %file, ptr %argsdata, i64 %argslen) {
entry:
  %argvlen = add i64 %argslen, 2
  %argvbytes = mul i64 %argvlen, 8
  %argv = call ptr @malloc(i64 %argvbytes)
  store ptr %file, ptr %argv, align 8
  %argvoff1 = getelementptr ptr, ptr %argv, i64 1
  %hasargs = icmp sgt i64 %argslen, 0
  br i1 %hasargs, label %copyargs, label %setnull

copyargs:
  %copybytes = mul i64 %argslen, 8
  call ptr @memcpy(ptr %argvoff1, ptr %argsdata, i64 %copybytes)
  br label %setnull

setnull:
  %nullidx = add i64 %argslen, 1
  %nullslot = getelementptr ptr, ptr %argv, i64 %nullidx
  store ptr null, ptr %nullslot, align 8

  %pipefd = alloca [2 x i32], align 4
  %pipeptr = getelementptr [2 x i32], ptr %pipefd, i32 0, i32 0
  %piperes = call i32 @pipe(ptr %pipeptr)
  %readfdp = getelementptr [2 x i32], ptr %pipefd, i32 0, i32 0
  %writefdp = getelementptr [2 x i32], ptr %pipefd, i32 0, i32 1
  %readfd = load i32, ptr %readfdp, align 4
  %writefd = load i32, ptr %writefdp, align 4

  %pid = call i32 @fork()
  %ischild = icmp eq i32 %pid, 0
  br i1 %ischild, label %child, label %parent

child:
  call i32 @close(i32 %readfd)
  call i32 @dup2(i32 %writefd, i32 1)
  call i32 @close(i32 %writefd)
  call i32 @execvp(ptr %file, ptr %argv)
  call void @_exit(i32 127)
  unreachable

parent:
  call i32 @close(i32 %writefd)
  %bufslot = call ptr @malloc(i64 24)
  %data_p = getelementptr { ptr, i64, i64 }, ptr %bufslot, i32 0, i32 0
  %len_p = getelementptr { ptr, i64, i64 }, ptr %bufslot, i32 0, i32 1
  %cap_p = getelementptr { ptr, i64, i64 }, ptr %bufslot, i32 0, i32 2
  store ptr null, ptr %data_p, align 8
  store i64 0, ptr %len_p, align 8
  store i64 0, ptr %cap_p, align 8
  %chunk = alloca [4096 x i8], align 1
  %chunkptr = getelementptr [4096 x i8], ptr %chunk, i32 0, i32 0
  br label %readloop

readloop:
  %n = call i64 @read(i32 %readfd, ptr %chunkptr, i64 4096)
  %hasdata = icmp sgt i64 %n, 0
  br i1 %hasdata, label %append, label %readdone

append:
  %curdata = load ptr, ptr %data_p, align 8
  %curlen = load i64, ptr %len_p, align 8
  %curcap = load i64, ptr %cap_p, align 8
  %needed = add i64 %curlen, %n
  %neededp1 = add i64 %needed, 1
  %needgrow = icmp sgt i64 %neededp1, %curcap
  br i1 %needgrow, label %grow, label %copy

grow:
  %cap2 = mul i64 %curcap, 2
  %pick1 = icmp sgt i64 %neededp1, %cap2
  %newcap_a = select i1 %pick1, i64 %neededp1, i64 %cap2
  %atleast64 = icmp sgt i64 %newcap_a, 64
  %newcap = select i1 %atleast64, i64 %newcap_a, i64 64
  %newdata = call ptr @realloc(ptr %curdata, i64 %newcap)
  store ptr %newdata, ptr %data_p, align 8
  store i64 %newcap, ptr %cap_p, align 8
  br label %copy

copy:
  %dataNow = load ptr, ptr %data_p, align 8
  %destptr = getelementptr i8, ptr %dataNow, i64 %curlen
  call ptr @memcpy(ptr %destptr, ptr %chunkptr, i64 %n)
  %newlen = add i64 %curlen, %n
  store i64 %newlen, ptr %len_p, align 8
  %termptr = getelementptr i8, ptr %dataNow, i64 %newlen
  store i8 0, ptr %termptr, align 1
  br label %readloop

readdone:
  call i32 @close(i32 %readfd)
  %statusslot = alloca i32, align 4
  store i32 0, ptr %statusslot, align 4
  call i32 @waitpid(i32 %pid, ptr %statusslot, i32 0)
  %status = load i32, ptr %statusslot, align 4
  %lowbyte = and i32 %status, 127
  %exitednormally = icmp eq i32 %lowbyte, 0
  br i1 %exitednormally, label %checkexitcode, label %signaled

checkexitcode:
  %exitcode = lshr i32 %status, 8
  %exitcode8 = and i32 %exitcode, 255
  %failed = icmp ne i32 %exitcode8, 0
  br i1 %failed, label %throwexit, label %success

throwexit:
  %msgbuf1len = call i64 @strlen(ptr %file)
  %msgbuf1size = add i64 %msgbuf1len, 64
  %msgbuf1 = call ptr @__kml_str_alloc(i64 %msgbuf1size)
  call i32 (ptr, ptr, ...) @sprintf(ptr %msgbuf1, ptr `

	part2 := `, i32 %exitcode8, ptr %file)
  call void @__kml_str_finalize(ptr %msgbuf1)
  %errobj1 = call ptr @malloc(i64 24)
  %errobj1.kind = getelementptr { i64, ptr, ptr }, ptr %errobj1, i32 0, i32 0
  store i64 0, ptr %errobj1.kind, align 8
  %errobj1.msg = getelementptr { i64, ptr, ptr }, ptr %errobj1, i32 0, i32 1
  store ptr %msgbuf1, ptr %errobj1.msg, align 8
  %errobj1.name = getelementptr { i64, ptr, ptr }, ptr %errobj1, i32 0, i32 2
  store ptr ` + errNamePtr + `, ptr %errobj1.name, align 8
  call void @__kml_throw(ptr %errobj1)
  unreachable

signaled:
  %sig = and i32 %status, 127
  %msgbuf2len = call i64 @strlen(ptr %file)
  %msgbuf2size = add i64 %msgbuf2len, 64
  %msgbuf2 = call ptr @__kml_str_alloc(i64 %msgbuf2size)
  call i32 (ptr, ptr, ...) @sprintf(ptr %msgbuf2, ptr `

	part3 := `, i32 %sig, ptr %file)
  call void @__kml_str_finalize(ptr %msgbuf2)
  %errobj2 = call ptr @malloc(i64 24)
  %errobj2.kind = getelementptr { i64, ptr, ptr }, ptr %errobj2, i32 0, i32 0
  store i64 0, ptr %errobj2.kind, align 8
  %errobj2.msg = getelementptr { i64, ptr, ptr }, ptr %errobj2, i32 0, i32 1
  store ptr %msgbuf2, ptr %errobj2.msg, align 8
  %errobj2.name = getelementptr { i64, ptr, ptr }, ptr %errobj2, i32 0, i32 2
  store ptr ` + errNamePtr + `, ptr %errobj2.name, align 8
  call void @__kml_throw(ptr %errobj2)
  unreachable

success:
  %finaldata = load ptr, ptr %data_p, align 8
  %isnull = icmp eq ptr %finaldata, null
  br i1 %isnull, label %emptyresult, label %havebody

emptyresult:
  %emptystr = call ptr @malloc(i64 1)
  store i8 0, ptr %emptystr, align 1
  br label %done

havebody:
  br label %done

done:
  %result = phi ptr [ %emptystr, %emptyresult ], [ %finaldata, %havebody ]
  %resulth = call ptr @__kml_str_from_cstr(ptr %result)
  ret ptr %resulth
}`

	e.emitGlobal(part1 + fmtExit + part2 + fmtSig + part3)
}

// nodePlatformName maps the Go compiler's own runtime.GOOS to the string
// Node's process.platform would report on that host — a pure compile-time
// mapping, no runtime code at all, following the same "check the Go
// compiler's own OS, since it also runs clang moments later" reasoning as
// errnoAccessor/monotonicClockID/stdinGlobalName.

func nodePlatformName() string {
	switch runtime.GOOS {
	case "windows":
		return "win32"
	default:
		return runtime.GOOS // "darwin", "linux", "freebsd", etc. already match Node's own strings
	}
}

// nodeArchName maps Go's GOARCH to Node's process.arch strings (amd64 → x64,
// 386 → ia32); arm64/arm/ppc64/s390x already match. This compiler builds for
// the host arch, so the value is a compile-time constant.
func nodeArchName() string {
	switch runtime.GOARCH {
	case "amd64":
		return "x64"
	case "386":
		return "ia32"
	default:
		return runtime.GOARCH // "arm64", "arm", "ppc64", "s390x", ... already match Node
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
	clk := monotonicClockID()
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
	clk := monotonicClockID()
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
	e.emitGlobal("declare i32 @chdir(ptr noundef)")
	opDescPtr := e.internString("cannot change directory to")
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_process_chdir(ptr %%path) {
entry:
  %%r = call i32 @chdir(ptr %%path)
  %%failed = icmp ne i32 %%r, 0
  br i1 %%failed, label %%fail, label %%ok

fail:
  call void @__kml_fs_throw(ptr %s, ptr %%path)
  unreachable

ok:
  ret void
}`, opDescPtr))
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
	if runtime.GOOS == "darwin" {
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
	e.emitGlobal("declare i64 @readlink(ptr, ptr, i64)")
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
	accessor := errnoAccessor()
	e.emitGlobal("declare i32 @kill(i32 noundef, i32 noundef)")
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
  %%errno_ptr = call ptr @%s()
  %%errno_val = load i32, ptr %%errno_ptr, align 4
  %%errmsg = call ptr @strerror(i32 %%errno_val)
  %%errlen = call i64 @strlen(ptr %%errmsg)
  %%bufsize = add i64 %%errlen, 48
  %%buf = call ptr @__kml_str_alloc(i64 %%bufsize)
  call i32 (ptr, ptr, ...) @sprintf(ptr %%buf, ptr %s, i64 %%pid, i64 %%sig, ptr %%errmsg)
  call void @__kml_str_finalize(ptr %%buf)
  %%errobj = call ptr @malloc(i64 24)
  %%errobj.kind = getelementptr { i64, ptr, ptr }, ptr %%errobj, i32 0, i32 0
  store i64 0, ptr %%errobj.kind, align 8
  %%errobj.msg = getelementptr { i64, ptr, ptr }, ptr %%errobj, i32 0, i32 1
  store ptr %%buf, ptr %%errobj.msg, align 8
  %%errobj.name = getelementptr { i64, ptr, ptr }, ptr %%errobj, i32 0, i32 2
  store ptr %s, ptr %%errobj.name, align 8
  call void @__kml_throw(ptr %%errobj)
  unreachable

ok:
  ret void
}`, accessor, fmtPtr, killErrNamePtr))
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
func (e *Emitter) ensureSignalHandlerRuntime() {
	if e.usedSignalHandler {
		return
	}
	e.usedSignalHandler = true
	e.emitGlobal("declare ptr @signal(i32 noundef, ptr noundef)")
	e.emitGlobal("@__kml_sigint_pending = internal thread_local global i8 0")
	e.emitGlobal("@__kml_sigterm_pending = internal thread_local global i8 0")
	e.emitGlobal("@__kml_sigint_closure = internal thread_local global ptr null")
	e.emitGlobal("@__kml_sigterm_closure = internal thread_local global ptr null")
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
  br i1 %isterm, label %setterm, label %done

setterm:
  store volatile i8 1, ptr @__kml_sigterm_pending
  ret void

done:
  ret void
}`)
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
