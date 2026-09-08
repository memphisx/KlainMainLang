// runtime_childprocess.go — async Node `child_process`: spawn/exec/execFile.
//
// A spawned child is a fork()+execvp() with three pipes (stdin write, stdout
// read, stderr read); the two read ends are made non-blocking and folded into
// the central select() event loop exactly like the Worker message pipes
// (TDD-00098): @__kml_cp_fdset_add adds every live child's read fds,
// @__kml_cp_dispatch drains them after select() and fires the registered
// listeners, @__kml_cp_keepalive holds the loop open while a child is live.
// No-op stubs (emitLoopTaskStubs) stand in when the program never spawns.
//
// One ChildProcess handle (%kml.cp) per spawn, in a process-wide registry.
// Streaming mode (spawn): stdout/stderr 'data'/'end' + 'close'/'exit'
// listeners are stored per-handle and fired from the dispatch. Buffered mode
// (exec/execFile): stdout/stderr accumulate into growable buffers, and a
// single (err, stdout, stderr) callback fires on child exit.
package llvm

import (
	"fmt"
	"runtime"
	"strings"
)

// %kml.cp layout (fields, all 8-byte-slotted except the three i32 fds):
//
//	0 i64 pid
//	1 i32 stdinFd   (write end; -1 after .end())
//	2 i32 stdoutFd  (read end; -1 after EOF)
//	3 i32 stderrFd  (read end; -1 after EOF)
//	4 i64 state     (0 running · 2 reaped+finalized)
//	5 i64 exitCode
//	6 ptr stdout 'data' listener   · 7 ptr stdout 'end' listener
//	8 ptr stderr 'data' listener   · 9 ptr stderr 'end' listener
//
// 10 ptr 'close' listener         · 11 ptr 'exit' listener  · 12 ptr 'error'
// 13 i64 mode      (0 streaming spawn · 1 buffered exec)
// 14 ptr stdoutAccum {ptr,i64,i64} · 15 ptr stderrAccum · 16 ptr execCallback
// 17 i32 ipcFd (0 none · >0 open · -1 closed — TDD-00141 fork channel)
// 18 ptr 'message' listener · 19 ptr ipc channel (C-side line buffer)
// 20 i64 spawn errno · 21 i64 recorded .kill() signal (Windows exit shape)
// 22 i64 timeout deadline (absolute monotonic ns, 0 = none — ADR-00764)
// 23 i64 killSignal for the timeout kill (default 15 = SIGTERM)
// 24 i64 unref flag (1 = child.unref()'d — does not keep the loop alive, ADR-00767)
// Field 20 (i64) is the spawn-failure errno: 0 when the child started, else
// the errno the exec failed with (ENOENT for a missing command).
// __kml_cp_finalize fires 'error' instead of 'exit' when it is set
// (ADR-00754). calloc zeroes it, so an unset field means "started fine".
// Field 21 (i64) is the signal number a `.kill(sig)` recorded, 0 otherwise.
// POSIX recovers a signalled death from the wait status directly (WIFSIGNALED),
// so this field only feeds the Windows `'exit'`/`'close'` `(code, signal)`
// shape, where TerminateProcess leaves no signalled bit to read (TDD-00184).
const cpStructIR = "{ i64, i32, i32, i32, i64, i64, ptr, ptr, ptr, ptr, ptr, ptr, ptr, i64, ptr, ptr, ptr, i32, ptr, ptr, i64, i64, i64, i64, i64 }"

// cpStructBytes is the calloc size for cpStructIR (25 × 8).
const cpStructBytes = 200

func (e *Emitter) ensureChildProcRuntime() {
	if e.usedChildProcRuntime {
		return
	}
	e.usedChildProcRuntime = true
	e.ensureStrHeaderRuntime()
	e.ensureMalloc()
	e.ensureCalloc()
	e.ensureRealloc()
	e.ensureFree()
	e.ensureMemcpy()
	e.ensureStrlen()
	e.ensureExceptionHelpers()
	e.ensureWorkerFdSetbit() // shared @__kml_worker_fd_setbit
	e.ensureTimerRuntime()   // @__kml_monotonic_ns for the spawn `timeout` deadline
	e.ensureCPKill()         // @kill — the timeout fire and child.kill share it

	e.emitGlobal("declare i32 @pipe(ptr noundef)")
	e.ensureForkDecl()
	e.emitGlobal("declare i32 @dup2(i32 noundef, i32 noundef)")
	e.ensureCloseDecl()
	e.ensureExecvpDecl()
	e.ensureExitRawDecl()
	e.ensureChdirDecl()
	e.ensureReadDecl()
	e.ensureWriteDecl()
	e.ensureWaitpidDecl()
	e.ensureFcntlDecl()
	e.ensureErrnoAccessor() // the spawn-fail status pipe reports the child's errno
	if runtime.GOOS == "windows" {
		// win32proc.c exposes CreateProcessW's failure as a Linux-ABI errno
		// (0 on success) so the spawn 'error' event fires (ADR-00754).
		e.emitGlobal("declare i32 @__kml_win_spawn_failed(i32 noundef)")
		// Full 32-bit exit code; the POSIX wait word carries only 8 (ADR-00759).
		e.emitGlobal("declare i32 @__kml_win_exit_code(i32 noundef)")
	} else {
		// A custom spawn env replaces the child's by pointing environ at it
		// before execvp (ADR-00762). Windows passes the block to CreateProcessW.
		e.emitGlobal("@environ = external global ptr")
		// detached: setsid() in the forked child (ADR-00765). Windows uses
		// DETACHED_PROCESS in the shim instead.
		e.emitGlobal("declare i32 @setsid()")
		// stdio: 'ignore' dup2's /dev/null onto the fd in the child (ADR-00766).
		e.ensureOpenDecl()
		e.emitGlobal(`@.kml_cp_devnull = private unnamed_addr constant [10 x i8] c"/dev/null\00"`)
	}

	e.emitGlobal("@__kml_cp_data = internal global ptr null, align 8")
	e.emitGlobal("@__kml_cp_len = internal global i64 0, align 8")
	e.emitGlobal("@__kml_cp_cap = internal global i64 0, align 8")

	cp := cpStructIR
	nonblock := httpNonblockFlag()
	errName := e.internString("Error")

	// __kml_cp_accum(accum {ptr,i64,i64}*, src, n): append n bytes, keeping a
	// trailing NUL so the buffer doubles as a C string (exec's stdout/stderr).
	e.emitGlobal(`
define void @__kml_cp_accum(ptr %acc, ptr %src, i64 %n) {
entry:
  %data_p = getelementptr { ptr, i64, i64 }, ptr %acc, i32 0, i32 0
  %len_p = getelementptr { ptr, i64, i64 }, ptr %acc, i32 0, i32 1
  %cap_p = getelementptr { ptr, i64, i64 }, ptr %acc, i32 0, i32 2
  %curlen = load i64, ptr %len_p, align 8
  %curcap = load i64, ptr %cap_p, align 8
  %curdata = load ptr, ptr %data_p, align 8
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
  call ptr @memcpy(ptr %destptr, ptr %src, i64 %n)
  %newlen = add i64 %curlen, %n
  store i64 %newlen, ptr %len_p, align 8
  %termptr = getelementptr i8, ptr %dataNow, i64 %newlen
  store i8 0, ptr %termptr, align 1
  ret void
}`)

	// __kml_cp_drain(cp, fdslot i32*, dataL, endL, accum, mode): read the fd
	// until EAGAIN/EOF. On data: fire dataL (streaming) or append (buffered).
	// On EOF: close, set fd -1, fire endL (streaming).
	e.emitGlobal(`
define void @__kml_cp_drain(ptr %cp, ptr %fdslot, ptr %dataL, ptr %endL, ptr %accum, i64 %mode) {
entry:
  %chunk = alloca [4096 x i8], align 1
  %chunkptr = getelementptr [4096 x i8], ptr %chunk, i32 0, i32 0
  br label %loop
loop:
  %fd = load i32, ptr %fdslot, align 4
  %closed = icmp slt i32 %fd, 0
  br i1 %closed, label %ret, label %doread
doread:
  %n = call i64 @read(i32 %fd, ptr %chunkptr, i64 4096)
  %hasdata = icmp sgt i64 %n, 0
  br i1 %hasdata, label %ondata, label %ckeof
ondata:
  ; mode is a bitmask (bit 0 = buffered exec, bit 1 = shell/verbatim spawn,
  ; ADR-00740) — streaming iff the buffered bit is clear.
  %modebuf = and i64 %mode, 1
  %streaming = icmp eq i64 %modebuf, 0
  br i1 %streaming, label %fire, label %append
fire:
  %hasL = icmp ne ptr %dataL, null
  br i1 %hasL, label %docall, label %loop
docall:
  %buf = call ptr @__kml_str_alloc(i64 %n)
  call ptr @memcpy(ptr %buf, ptr %chunkptr, i64 %n)
  %bufnul = getelementptr i8, ptr %buf, i64 %n
  store i8 0, ptr %bufnul, align 1
  %dfp_p = getelementptr { ptr, ptr }, ptr %dataL, i32 0, i32 0
  %dfp = load ptr, ptr %dfp_p, align 8
  %dep_p = getelementptr { ptr, ptr }, ptr %dataL, i32 0, i32 1
  %dep = load ptr, ptr %dep_p, align 8
  call void %dfp(ptr %dep, ptr %buf, i64 %n)
  br label %loop
append:
  call void @__kml_cp_accum(ptr %accum, ptr %chunkptr, i64 %n)
  br label %loop
ckeof:
  %iseof = icmp eq i64 %n, 0
  br i1 %iseof, label %oneof, label %ret
oneof:
  call i32 @close(i32 %fd)
  store i32 -1, ptr %fdslot, align 4
  %modebuf2 = and i64 %mode, 1
  %streaming2 = icmp eq i64 %modebuf2, 0
  %hasEnd = icmp ne ptr %endL, null
  %fireEnd = and i1 %streaming2, %hasEnd
  br i1 %fireEnd, label %callend, label %ret
callend:
  %efp_p = getelementptr { ptr, ptr }, ptr %endL, i32 0, i32 0
  %efp = load ptr, ptr %efp_p, align 8
  %eep_p = getelementptr { ptr, ptr }, ptr %endL, i32 0, i32 1
  %eep = load ptr, ptr %eep_p, align 8
  call void %efp(ptr %eep)
  br label %ret
ret:
  ret void
}`)

	// __kml_cp_finalize(cp): both stdio pipes are at EOF — reap (WNOHANG) and,
	// once reaped, store the exit code and fire the terminal listeners /
	// buffered callback, then mark the handle finalized (state 2).
	finalizeIR := fmt.Sprintf(`
define void @__kml_cp_finalize(ptr %%cp) {
entry:
  %%pid_p = getelementptr %s, ptr %%cp, i32 0, i32 0
  %%pid64 = load i64, ptr %%pid_p, align 8
  %%pid = trunc i64 %%pid64 to i32
  %%stslot = alloca i32, align 4
  store i32 0, ptr %%stslot, align 4
  %%r = call i32 @waitpid(i32 %%pid, ptr %%stslot, i32 1)
  %%reaped = icmp eq i32 %%r, %%pid
  br i1 %%reaped, label %%doreap, label %%ret
doreap:
  %%st = load i32, ptr %%stslot, align 4
  %%low = and i32 %%st, 127
  %%normal = icmp eq i32 %%low, 0
  br i1 %%normal, label %%exited, label %%signaled
exited:
  %%c0 = lshr i32 %%st, 8
  %%code = and i32 %%c0, 255
  br label %%store
signaled:
  %%sigcode = add i32 %%low, 128
  br label %%store
store:
  %%codev = phi i32 [ %%code, %%exited ], [ %%sigcode, %%signaled ]
  ; The plain exit code (0 on a signalled death) drives the streaming
  ; 'exit'/'close' (code, signal) shape; %%codev keeps the folded 128+sig
  ; value the buffered exec path / field-5 exitCode still expect (TDD-00184).
  %%plaincode = phi i32 [ %%code, %%exited ], [ 0, %%signaled ]
  %%code64 = zext i32 %%codev to i64
  %%ec_p = getelementptr %s, ptr %%cp, i32 0, i32 5
  store i64 %%code64, ptr %%ec_p, align 8
  ; ---- streaming 'exit'/'close' (code, signal) shape (TDD-00184) ----
  ; __kml_cp_event_flags decides present/signum per platform: POSIX from the
  ; wait status (WIFSIGNALED/WTERMSIG), Windows from the recorded .kill() signal
  ; (field 21) since TerminateProcess leaves no signalled bit. code is null
  ; (present=false) means the listener sees 0, or null if it typed number|null.
  %%ks_p = getelementptr %s, ptr %%cp, i32 0, i32 21
  %%ks = load i64, ptr %%ks_p, align 8
  %%evflags = call { i1, i32 } @__kml_cp_event_flags(i32 %%st, i64 %%ks)
  %%evpresent = extractvalue { i1, i32 } %%evflags, 0
  %%evsig = extractvalue { i1, i32 } %%evflags, 1
  %%evsig64 = zext i32 %%evsig to i64
  %%evsigname = call ptr @__kml_cp_signal_name(i64 %%evsig64)
  %%plaincode64 = zext i32 %%plaincode to i64
  %%evcode_sel = select i1 %%evpresent, i64 %%plaincode64, i64 0
  %%evcode_d = sitofp i64 %%evcode_sel to double
  %%st_p = getelementptr %s, ptr %%cp, i32 0, i32 4
  store i64 2, ptr %%st_p, align 8
  %%mode_p = getelementptr %s, ptr %%cp, i32 0, i32 13
  %%mode = load i64, ptr %%mode_p, align 8
  %%modebuf = and i64 %%mode, 1
  %%buffered = icmp ne i64 %%modebuf, 0
  br i1 %%buffered, label %%bufcb, label %%streamcb
streamcb:
  ; A failed *spawn* (field 20 != 0) emits 'error' (with an Error) and 'close',
  ; but not 'exit' — Node's ChildProcess semantics (ADR-00754).
  %%sf20_p = getelementptr %s, ptr %%cp, i32 0, i32 20
  %%sf20 = load i64, ptr %%sf20_p, align 8
  %%spawnfailed = icmp ne i64 %%sf20, 0
  br i1 %%spawnfailed, label %%errevt, label %%normexit
errevt:
  %%errL_p = getelementptr %s, ptr %%cp, i32 0, i32 12
  %%errL = load ptr, ptr %%errL_p, align 8
  %%hasErr = icmp ne ptr %%errL, null
  br i1 %%hasErr, label %%callerr, label %%aftexit
callerr:
  %%serrobj = call ptr @__kml_cp_spawn_errobj(i64 %%sf20)
  %%efp3_p = getelementptr { ptr, ptr }, ptr %%errL, i32 0, i32 0
  %%efp3 = load ptr, ptr %%efp3_p, align 8
  %%eep3_p = getelementptr { ptr, ptr }, ptr %%errL, i32 0, i32 1
  %%eep3 = load ptr, ptr %%eep3_p, align 8
  call void %%efp3(ptr %%eep3, ptr %%serrobj)
  br label %%aftexit
normexit:
  ; fire 'exit'(code, signal) then 'close'(code, signal) — the stored listener
  ; is a fixed-ABI adapter void(ptr env, i1 present, double code, ptr signal)
  ; that forwards to the user closure with its own arity (TDD-00184).
  %%exitL_p = getelementptr %s, ptr %%cp, i32 0, i32 11
  %%exitL = load ptr, ptr %%exitL_p, align 8
  %%hasExit = icmp ne ptr %%exitL, null
  br i1 %%hasExit, label %%callexit, label %%aftexit
callexit:
  %%xfp_p = getelementptr { ptr, ptr }, ptr %%exitL, i32 0, i32 0
  %%xfp = load ptr, ptr %%xfp_p, align 8
  %%xep_p = getelementptr { ptr, ptr }, ptr %%exitL, i32 0, i32 1
  %%xep = load ptr, ptr %%xep_p, align 8
  call void %%xfp(ptr %%xep, i1 %%evpresent, double %%evcode_d, ptr %%evsigname)
  br label %%aftexit
aftexit:
  %%closeL_p = getelementptr %s, ptr %%cp, i32 0, i32 10
  %%closeL = load ptr, ptr %%closeL_p, align 8
  %%hasClose = icmp ne ptr %%closeL, null
  br i1 %%hasClose, label %%callclose, label %%ret
callclose:
  %%cfp_p = getelementptr { ptr, ptr }, ptr %%closeL, i32 0, i32 0
  %%cfp = load ptr, ptr %%cfp_p, align 8
  %%cep_p = getelementptr { ptr, ptr }, ptr %%closeL, i32 0, i32 1
  %%cep = load ptr, ptr %%cep_p, align 8
  call void %%cfp(ptr %%cep, i1 %%evpresent, double %%evcode_d, ptr %%evsigname)
  br label %%ret
bufcb:
  %%cb_p = getelementptr %s, ptr %%cp, i32 0, i32 16
  %%cb = load ptr, ptr %%cb_p, align 8
  %%hasCb = icmp ne ptr %%cb, null
  br i1 %%hasCb, label %%docb, label %%ret
docb:
  ; stdout / stderr strings from the accumulators ("" if never allocated data)
  %%so_p = getelementptr %s, ptr %%cp, i32 0, i32 14
  %%so = load ptr, ptr %%so_p, align 8
  %%sostr = call ptr @__kml_cp_accum_str(ptr %%so)
  %%se_p = getelementptr %s, ptr %%cp, i32 0, i32 15
  %%se = load ptr, ptr %%se_p, align 8
  %%sestr = call ptr @__kml_cp_accum_str(ptr %%se)
  ; err: null on success, else an Error object
  %%failed = icmp ne i64 %%code64, 0
  br i1 %%failed, label %%mkerr, label %%callcb
mkerr:
  %%emsg = call ptr @__kml_cp_exec_errmsg(i64 %%code64)
  %%eobj = call ptr @malloc(i64 24)
  %%ek = getelementptr { i64, ptr, ptr }, ptr %%eobj, i32 0, i32 0
  store i64 0, ptr %%ek, align 8
  %%em = getelementptr { i64, ptr, ptr }, ptr %%eobj, i32 0, i32 1
  store ptr %%emsg, ptr %%em, align 8
  %%en = getelementptr { i64, ptr, ptr }, ptr %%eobj, i32 0, i32 2
  store ptr %s, ptr %%en, align 8
  br label %%callcb
callcb:
  %%errv = phi ptr [ null, %%docb ], [ %%eobj, %%mkerr ]
  %%bfp_p = getelementptr { ptr, ptr }, ptr %%cb, i32 0, i32 0
  %%bfp = load ptr, ptr %%bfp_p, align 8
  %%bep_p = getelementptr { ptr, ptr }, ptr %%cb, i32 0, i32 1
  %%bep = load ptr, ptr %%bep_p, align 8
  call void %%bfp(ptr %%bep, ptr %%errv, ptr %%sostr, ptr %%sestr)
  br label %%ret
ret:
  ret void
}`, cp, cp, cp, cp, cp, cp, cp, cp, cp, cp, cp, cp, errName)
	if runtime.GOOS == "windows" {
		// Windows exit codes are full 32-bit; recover the wide value the POSIX
		// 8-bit wait status dropped, falling back for foreign pids (ADR-00759).
		finalizeIR = strings.Replace(finalizeIR,
			`exited:
  %c0 = lshr i32 %st, 8
  %code = and i32 %c0, 255
  br label %store`,
			`exited:
  %c0 = lshr i32 %st, 8
  %fb = and i32 %c0, 255
  %wc = call i32 @__kml_win_exit_code(i32 %pid)
  %wok = icmp sge i32 %wc, 0
  %code = select i1 %wok, i32 %wc, i32 %fb
  br label %store`, 1)
	}
	e.emitGlobal(finalizeIR)

	e.emitCPEventFlags()
	e.emitCPSignalName()

	// small helpers for the buffered path
	emptyStr := e.internString("")
	e.emitGlobal(fmt.Sprintf(`
define ptr @__kml_cp_accum_str(ptr %%acc) {
entry:
  %%isnull = icmp eq ptr %%acc, null
  br i1 %%isnull, label %%empty, label %%chk
chk:
  %%d_p = getelementptr { ptr, i64, i64 }, ptr %%acc, i32 0, i32 0
  %%d = load ptr, ptr %%d_p, align 8
  %%dnull = icmp eq ptr %%d, null
  br i1 %%dnull, label %%empty, label %%ret
empty:
  ret ptr %s
ret:
  ; TDD-00120: return a length-prefixed copy of the accumulator's raw bytes
  ; (len field, i32 1) so binary-safe consumers (.split/=== read ptr-8) work.
  %%len_p = getelementptr { ptr, i64, i64 }, ptr %%acc, i32 0, i32 1
  %%lenv = load i64, ptr %%len_p, align 8
  %%hdr = call ptr @__kml_str_alloc(i64 %%lenv)
  call ptr @memcpy(ptr %%hdr, ptr %%d, i64 %%lenv)
  %%nul = getelementptr i8, ptr %%hdr, i64 %%lenv
  store i8 0, ptr %%nul, align 1
  ret ptr %%hdr
}`, emptyStr))

	// __kml_cp_exec_errmsg(code): "Command failed with exit code N"
	fmtExec := e.internString("Command failed with exit code %lld")
	e.ensureSprintf()
	e.emitGlobal(fmt.Sprintf(`
define ptr @__kml_cp_exec_errmsg(i64 %%code) {
entry:
  %%buf = call ptr @__kml_str_alloc(i64 64)
  call i32 (ptr, ptr, ...) @sprintf(ptr %%buf, ptr %s, i64 %%code)
  call void @__kml_str_finalize(ptr %%buf)
  ret ptr %%buf
}`, fmtExec))

	// __kml_cp_spawn_errobj(errno): builds the Error a failed *spawn* emits
	// through the 'error' event (ADR-00754) — message "spawn <reason>" from
	// strerror(errno), a full errorObjType. Carries the Node error props
	// `err.code` (`ENOENT` for a missing command — the canonical spawn-failure
	// idiom `e.code === 'ENOENT'`), `err.errno` (the negative libuv-style errno,
	// `-2` on POSIX — negating the platform errno is faithful on both Linux and
	// macOS; each reports its own raw number, as Node does), and `err.errstr`.
	// `err.syscall`/`err.path` stay null — Node's `spawn <file>` syscall needs
	// the command string, not threaded here (the message shape is likewise
	// unchanged, ADR-00754). The `__kml_errno_code` map is built per-platform
	// from Go's syscall constants, so the code string is correct where the raw
	// number differs Linux↔macOS (EAGAIN 11 vs 35, …).
	e.ensureStrerror()
	e.ensureErrnoCode()
	fmtSpawn := e.internString("spawn %s")
	spawnErrName := e.internString("Error")
	eIR := errorObjType.StructIR()
	e.emitGlobal(fmt.Sprintf(`
define ptr @__kml_cp_spawn_errobj(i64 %%errno) {
entry:
  %%e32 = trunc i64 %%errno to i32
  %%reason = call ptr @strerror(i32 %%e32)
  %%buf = call ptr @__kml_str_alloc(i64 128)
  call i32 (ptr, ptr, ...) @sprintf(ptr %%buf, ptr %s, ptr %%reason)
  call void @__kml_str_finalize(ptr %%buf)
  %%code = call ptr @__kml_errno_code(i32 %%e32)
  %%errno_pos = sitofp i32 %%e32 to double
  %%errno_neg32 = sub i32 0, %%e32
  %%errno_neg = sitofp i32 %%errno_neg32 to double
  %%obj = call ptr @malloc(i64 %d)
  %%k = getelementptr %s, ptr %%obj, i32 0, i32 0
  store i64 0, ptr %%k, align 8
  %%m = getelementptr %s, ptr %%obj, i32 0, i32 1
  store ptr %%buf, ptr %%m, align 8
  %%nm = getelementptr %s, ptr %%obj, i32 0, i32 2
  store ptr %s, ptr %%nm, align 8
  %%c = getelementptr %s, ptr %%obj, i32 0, i32 3
  store ptr %%code, ptr %%c, align 8
  %%ec = getelementptr %s, ptr %%obj, i32 0, i32 4
  store double %%errno_pos, ptr %%ec, align 8
  %%es = getelementptr %s, ptr %%obj, i32 0, i32 5
  store ptr %%reason, ptr %%es, align 8
  %%sc = getelementptr %s, ptr %%obj, i32 0, i32 6
  store ptr null, ptr %%sc, align 8
  %%pa = getelementptr %s, ptr %%obj, i32 0, i32 7
  store ptr null, ptr %%pa, align 8
  %%en = getelementptr %s, ptr %%obj, i32 0, i32 8
  store double %%errno_neg, ptr %%en, align 8
  ret ptr %%obj
}`, fmtSpawn, errorObjType.StructSize(), eIR, eIR, eIR, spawnErrName, eIR, eIR, eIR, eIR, eIR, eIR))

	// __kml_cp_dispatch(): drain + finalize every live child. Called by the
	// event loop after select().
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_cp_dispatch() {
entry:
  %%len = load i64, ptr @__kml_cp_len, align 8
  %%data = load ptr, ptr @__kml_cp_data, align 8
  %%i = alloca i64, align 8
  store i64 0, ptr %%i, align 8
  br label %%loop
loop:
  %%iv = load i64, ptr %%i, align 8
  %%inb = icmp slt i64 %%iv, %%len
  br i1 %%inb, label %%body, label %%done
body:
  %%slot = getelementptr ptr, ptr %%data, i64 %%iv
  %%cp = load ptr, ptr %%slot, align 8
  %%st_p = getelementptr %s, ptr %%cp, i32 0, i32 4
  %%st = load i64, ptr %%st_p, align 8
  %%live = icmp slt i64 %%st, 2
  br i1 %%live, label %%drain, label %%next
drain:
  ; spawn timeout: kill the child once its deadline passes (ADR-00764). The
  ; kill's signal is recorded in field 21 so the subsequent reap fires the
  ; faithful ('exit', null, '<signal>') the (code, signal) shape reports.
  %%tdl_p = getelementptr %s, ptr %%cp, i32 0, i32 22
  %%tdl = load i64, ptr %%tdl_p, align 8
  %%hastdl = icmp ne i64 %%tdl, 0
  br i1 %%hastdl, label %%tchk, label %%tafter
tchk:
  %%nowtt = call i64 @__kml_monotonic_ns()
  %%tdue = icmp sge i64 %%nowtt, %%tdl
  br i1 %%tdue, label %%tfire, label %%tafter
tfire:
  store i64 0, ptr %%tdl_p, align 8
  %%tpid_p = getelementptr %s, ptr %%cp, i32 0, i32 0
  %%tpid = load i64, ptr %%tpid_p, align 8
  %%tpid32 = trunc i64 %%tpid to i32
  %%tks_p = getelementptr %s, ptr %%cp, i32 0, i32 23
  %%tks = load i64, ptr %%tks_p, align 8
  %%tks32 = trunc i64 %%tks to i32
  %%trk_p = getelementptr %s, ptr %%cp, i32 0, i32 21
  store i64 %%tks, ptr %%trk_p, align 8
  call i32 @kill(i32 %%tpid32, i32 %%tks32)
  br label %%tafter
tafter:
  %%mode_p = getelementptr %s, ptr %%cp, i32 0, i32 13
  %%mode = load i64, ptr %%mode_p, align 8
  %%fd2 = getelementptr %s, ptr %%cp, i32 0, i32 2
  %%d6_p = getelementptr %s, ptr %%cp, i32 0, i32 6
  %%d6 = load ptr, ptr %%d6_p, align 8
  %%e7_p = getelementptr %s, ptr %%cp, i32 0, i32 7
  %%e7 = load ptr, ptr %%e7_p, align 8
  %%a14_p = getelementptr %s, ptr %%cp, i32 0, i32 14
  %%a14 = load ptr, ptr %%a14_p, align 8
  call void @__kml_cp_drain(ptr %%cp, ptr %%fd2, ptr %%d6, ptr %%e7, ptr %%a14, i64 %%mode)
  %%fd3 = getelementptr %s, ptr %%cp, i32 0, i32 3
  %%d8_p = getelementptr %s, ptr %%cp, i32 0, i32 8
  %%d8 = load ptr, ptr %%d8_p, align 8
  %%e9_p = getelementptr %s, ptr %%cp, i32 0, i32 9
  %%e9 = load ptr, ptr %%e9_p, align 8
  %%a15_p = getelementptr %s, ptr %%cp, i32 0, i32 15
  %%a15 = load ptr, ptr %%a15_p, align 8
  call void @__kml_cp_drain(ptr %%cp, ptr %%fd3, ptr %%d8, ptr %%e9, ptr %%a15, i64 %%mode)
  call void @__kml_cp_ipc_drain(ptr %%cp)
  %%ofd = load i32, ptr %%fd2, align 4
  %%efd = load i32, ptr %%fd3, align 4
  %%oclosed = icmp slt i32 %%ofd, 0
  %%eclosed = icmp slt i32 %%efd, 0
  %%botheof0 = and i1 %%oclosed, %%eclosed
  ; a fork child with an open IPC channel is not finalizable yet
  %%ipc_p = getelementptr %s, ptr %%cp, i32 0, i32 17
  %%ipcfd = load i32, ptr %%ipc_p, align 4
  %%ipcopen = icmp sgt i32 %%ipcfd, 0
  %%ipcdone = xor i1 %%ipcopen, 1
  %%botheof = and i1 %%botheof0, %%ipcdone
  br i1 %%botheof, label %%fin, label %%next
fin:
  call void @__kml_cp_finalize(ptr %%cp)
  br label %%next
next:
  %%inext = add i64 %%iv, 1
  store i64 %%inext, ptr %%i, align 8
  br label %%loop
done:
  ret void
}`, cp, cp, cp, cp, cp, cp, cp, cp, cp, cp, cp, cp, cp, cp, cp))

	// __kml_cp_next_timeout_ns(): the soonest spawn-`timeout` deadline (absolute
	// monotonic ns) among live children, or 0 if none — folded into the event
	// loop's select() wait so a silent slow child is still killed on time
	// (ADR-00764; the same shape as __kml_eventsource_next_reconnect_ms).
	e.emitGlobal(fmt.Sprintf(`
define i64 @__kml_cp_next_timeout_ns() {
entry:
  %%len = load i64, ptr @__kml_cp_len, align 8
  %%data = load ptr, ptr @__kml_cp_data, align 8
  %%best = alloca i64, align 8
  store i64 0, ptr %%best, align 8
  %%i = alloca i64, align 8
  store i64 0, ptr %%i, align 8
  br label %%loop
loop:
  %%iv = load i64, ptr %%i, align 8
  %%inb = icmp slt i64 %%iv, %%len
  br i1 %%inb, label %%body, label %%done
body:
  %%slot = getelementptr ptr, ptr %%data, i64 %%iv
  %%cp = load ptr, ptr %%slot, align 8
  %%st_p = getelementptr %s, ptr %%cp, i32 0, i32 4
  %%st = load i64, ptr %%st_p, align 8
  %%live = icmp slt i64 %%st, 2
  br i1 %%live, label %%chk, label %%next
chk:
  %%dl_p = getelementptr %s, ptr %%cp, i32 0, i32 22
  %%dl = load i64, ptr %%dl_p, align 8
  %%has = icmp ne i64 %%dl, 0
  br i1 %%has, label %%consider, label %%next
consider:
  %%cur = load i64, ptr %%best, align 8
  %%none = icmp eq i64 %%cur, 0
  %%sooner = icmp slt i64 %%dl, %%cur
  %%take = or i1 %%none, %%sooner
  br i1 %%take, label %%takeit, label %%next
takeit:
  store i64 %%dl, ptr %%best, align 8
  br label %%next
next:
  %%inext = add i64 %%iv, 1
  store i64 %%inext, ptr %%i, align 8
  br label %%loop
done:
  %%r = load i64, ptr %%best, align 8
  ret i64 %%r
}`, cp, cp))

	// __kml_cp_fdset_add(fdset, maxfd): add every live child's read fds; force
	// a zero select() timeout when a child has both pipes at EOF but is not
	// yet reaped (so dispatch finalizes it promptly).
	e.emitGlobal(fmt.Sprintf(`
define i1 @__kml_cp_fdset_add(ptr %%fdset, ptr %%maxfd) {
entry:
  %%len = load i64, ptr @__kml_cp_len, align 8
  %%data = load ptr, ptr @__kml_cp_data, align 8
  %%force = alloca i1, align 1
  store i1 0, ptr %%force, align 1
  %%i = alloca i64, align 8
  store i64 0, ptr %%i, align 8
  br label %%loop
loop:
  %%iv = load i64, ptr %%i, align 8
  %%inb = icmp slt i64 %%iv, %%len
  br i1 %%inb, label %%body, label %%done
body:
  %%slot = getelementptr ptr, ptr %%data, i64 %%iv
  %%cp = load ptr, ptr %%slot, align 8
  %%st_p = getelementptr %s, ptr %%cp, i32 0, i32 4
  %%st = load i64, ptr %%st_p, align 8
  %%live = icmp slt i64 %%st, 2
  br i1 %%live, label %%chkfds, label %%next
chkfds:
  %%fd2_p = getelementptr %s, ptr %%cp, i32 0, i32 2
  %%ofd = load i32, ptr %%fd2_p, align 4
  %%oopen = icmp sge i32 %%ofd, 0
  br i1 %%oopen, label %%addo, label %%chke
addo:
  call void @__kml_worker_fd_setbit(i32 %%ofd, ptr %%fdset, ptr %%maxfd)
  br label %%chke
chke:
  %%fd3_p = getelementptr %s, ptr %%cp, i32 0, i32 3
  %%efd = load i32, ptr %%fd3_p, align 4
  %%eopen = icmp sge i32 %%efd, 0
  br i1 %%eopen, label %%adde, label %%chki
adde:
  call void @__kml_worker_fd_setbit(i32 %%efd, ptr %%fdset, ptr %%maxfd)
  br label %%chki
chki:
  %%ipc_p = getelementptr %s, ptr %%cp, i32 0, i32 17
  %%ipcfd = load i32, ptr %%ipc_p, align 4
  %%ipcopen = icmp sgt i32 %%ipcfd, 0
  br i1 %%ipcopen, label %%addi, label %%chkforce
addi:
  call void @__kml_worker_fd_setbit(i32 %%ipcfd, ptr %%fdset, ptr %%maxfd)
  br label %%chkforce
chkforce:
  %%obad = icmp slt i32 %%ofd, 0
  %%ebad = icmp slt i32 %%efd, 0
  %%both0 = and i1 %%obad, %%ebad
  %%ipcdone = xor i1 %%ipcopen, 1
  %%both = and i1 %%both0, %%ipcdone
  br i1 %%both, label %%setforce, label %%next
setforce:
  store i1 1, ptr %%force, align 1
  br label %%next
next:
  %%inext = add i64 %%iv, 1
  store i64 %%inext, ptr %%i, align 8
  br label %%loop
done:
  %%f = load i1, ptr %%force, align 1
  ret i1 %%f
}`, cp, cp, cp, cp))

	// __kml_cp_keepalive(): true while any child handle is not yet finalized.
	e.emitGlobal(fmt.Sprintf(`
define i1 @__kml_cp_keepalive() {
entry:
  %%len = load i64, ptr @__kml_cp_len, align 8
  %%data = load ptr, ptr @__kml_cp_data, align 8
  %%i = alloca i64, align 8
  store i64 0, ptr %%i, align 8
  br label %%loop
loop:
  %%iv = load i64, ptr %%i, align 8
  %%inb = icmp slt i64 %%iv, %%len
  br i1 %%inb, label %%body, label %%no
body:
  %%slot = getelementptr ptr, ptr %%data, i64 %%iv
  %%cp = load ptr, ptr %%slot, align 8
  %%st_p = getelementptr %s, ptr %%cp, i32 0, i32 4
  %%st = load i64, ptr %%st_p, align 8
  %%live = icmp slt i64 %%st, 2
  br i1 %%live, label %%chkref, label %%next
chkref:
  ; an unref()'d child (field 24) does not hold the loop open (ADR-00767).
  %%ur_p = getelementptr %s, ptr %%cp, i32 0, i32 24
  %%ur = load i64, ptr %%ur_p, align 8
  %%refd = icmp eq i64 %%ur, 0
  br i1 %%refd, label %%yes, label %%next
next:
  %%inext = add i64 %%iv, 1
  store i64 %%inext, ptr %%i, align 8
  br label %%loop
yes:
  ret i1 1
no:
  ret i1 0
}`, cp, cp))

	// __kml_cp_register(cp): append to the process-wide handle registry.
	e.emitGlobal(`
define void @__kml_cp_register(ptr %cp) {
entry:
  %len = load i64, ptr @__kml_cp_len, align 8
  %cap = load i64, ptr @__kml_cp_cap, align 8
  %full = icmp sge i64 %len, %cap
  br i1 %full, label %grow, label %store
grow:
  %cap2 = mul i64 %cap, 2
  %atleast4 = icmp sgt i64 %cap2, 4
  %newcap = select i1 %atleast4, i64 %cap2, i64 4
  %olddata = load ptr, ptr @__kml_cp_data, align 8
  %bytes = mul i64 %newcap, 8
  %newdata = call ptr @realloc(ptr %olddata, i64 %bytes)
  store ptr %newdata, ptr @__kml_cp_data, align 8
  store i64 %newcap, ptr @__kml_cp_cap, align 8
  br label %store
store:
  %data = load ptr, ptr @__kml_cp_data, align 8
  %slot = getelementptr ptr, ptr %data, i64 %len
  store ptr %cp, ptr %slot, align 8
  %newlen = add i64 %len, 1
  store i64 %newlen, ptr @__kml_cp_len, align 8
  ret void
}`)

	// __kml_cp_spawn(file, argsdata, argslen, mode): fork+exec with three
	// pipes; returns the ChildProcess handle. The two read fds are made
	// non-blocking; buffered mode pre-allocates the accumulators.
	e.emitGlobal(fmt.Sprintf(`
define ptr @__kml_cp_spawn(ptr %%file, ptr %%argsdata, i64 %%argslen, i64 %%mode, ptr %%cwd, ptr %%env, i64 %%timeout_ms, i64 %%killsig) {
entry:
  %%argvlen = add i64 %%argslen, 2
  %%argvbytes = mul i64 %%argvlen, 8
  %%argv = call ptr @malloc(i64 %%argvbytes)
  store ptr %%file, ptr %%argv, align 8
  %%argvoff1 = getelementptr ptr, ptr %%argv, i64 1
  %%hasargs = icmp sgt i64 %%argslen, 0
  br i1 %%hasargs, label %%copyargs, label %%setnull
copyargs:
  %%copybytes = mul i64 %%argslen, 8
  call ptr @memcpy(ptr %%argvoff1, ptr %%argsdata, i64 %%copybytes)
  br label %%setnull
setnull:
  %%nullidx = add i64 %%argslen, 1
  %%nullslot = getelementptr ptr, ptr %%argv, i64 %%nullidx
  store ptr null, ptr %%nullslot, align 8

  %%inpipe = alloca [2 x i32], align 4
  %%outpipe = alloca [2 x i32], align 4
  %%errpipe = alloca [2 x i32], align 4
  call i32 @pipe(ptr %%inpipe)
  call i32 @pipe(ptr %%outpipe)
  call i32 @pipe(ptr %%errpipe)
  %%inr_p = getelementptr [2 x i32], ptr %%inpipe, i32 0, i32 0
  %%inw_p = getelementptr [2 x i32], ptr %%inpipe, i32 0, i32 1
  %%outr_p = getelementptr [2 x i32], ptr %%outpipe, i32 0, i32 0
  %%outw_p = getelementptr [2 x i32], ptr %%outpipe, i32 0, i32 1
  %%errr_p = getelementptr [2 x i32], ptr %%errpipe, i32 0, i32 0
  %%errw_p = getelementptr [2 x i32], ptr %%errpipe, i32 0, i32 1
  %%inr = load i32, ptr %%inr_p, align 4
  %%inw = load i32, ptr %%inw_p, align 4
  %%outr = load i32, ptr %%outr_p, align 4
  %%outw = load i32, ptr %%outw_p, align 4
  %%errr = load i32, ptr %%errr_p, align 4
  %%errw = load i32, ptr %%errw_p, align 4
%s
%sparent:
  ; Per-fd stdio (mode bits 4-9, ADR-00766): pipe (0) keeps the pipe as today;
  ; inherit (1)/ignore (2) close the unused ends and store -1 so the drain and
  ; child.stdin.write paths skip that fd (the child got the inherited fd or
  ; /dev/null instead — see cpSpawnForkIR).
  %%psm_in0 = lshr i64 %%mode, 4
  %%psm_in = and i64 %%psm_in0, 3
  %%psm_out0 = lshr i64 %%mode, 6
  %%psm_out = and i64 %%psm_out0, 3
  %%psm_err0 = lshr i64 %%mode, 8
  %%psm_err = and i64 %%psm_err0, 3
  call i32 @close(i32 %%inr)
  %%pin_pipe = icmp eq i64 %%psm_in, 0
  br i1 %%pin_pipe, label %%in_keep, label %%in_close
in_close:
  call i32 @close(i32 %%inw)
  br label %%in_done
in_keep:
  br label %%in_done
in_done:
  %%inw_val = phi i32 [ %%inw, %%in_keep ], [ -1, %%in_close ]
  %%pout_pipe = icmp eq i64 %%psm_out, 0
  br i1 %%pout_pipe, label %%out_pipe, label %%out_close
out_pipe:
  call i32 @close(i32 %%outw)
  %%ofl = call i32 (i32, i32, ...) @fcntl(i32 %%outr, i32 3)
  %%ofln = or i32 %%ofl, %d
  call i32 (i32, i32, ...) @fcntl(i32 %%outr, i32 4, i32 %%ofln)
  br label %%out_done
out_close:
  call i32 @close(i32 %%outw)
  call i32 @close(i32 %%outr)
  br label %%out_done
out_done:
  %%outr_val = phi i32 [ %%outr, %%out_pipe ], [ -1, %%out_close ]
  %%perr_pipe = icmp eq i64 %%psm_err, 0
  br i1 %%perr_pipe, label %%err_pipe, label %%err_close
err_pipe:
  call i32 @close(i32 %%errw)
  %%efl = call i32 (i32, i32, ...) @fcntl(i32 %%errr, i32 3)
  %%efln = or i32 %%efl, %d
  call i32 (i32, i32, ...) @fcntl(i32 %%errr, i32 4, i32 %%efln)
  br label %%err_done
err_close:
  call i32 @close(i32 %%errw)
  call i32 @close(i32 %%errr)
  br label %%err_done
err_done:
  %%errr_val = phi i32 [ %%errr, %%err_pipe ], [ -1, %%err_close ]

  %%cp = call ptr @calloc(i64 1, i64 200)
  %%pid_p = getelementptr %s, ptr %%cp, i32 0, i32 0
  %%pid64 = zext i32 %%pid to i64
  store i64 %%pid64, ptr %%pid_p, align 8
  %%sin_p = getelementptr %s, ptr %%cp, i32 0, i32 1
  store i32 %%inw_val, ptr %%sin_p, align 4
  %%sout_p = getelementptr %s, ptr %%cp, i32 0, i32 2
  store i32 %%outr_val, ptr %%sout_p, align 4
  %%serr_p = getelementptr %s, ptr %%cp, i32 0, i32 3
  store i32 %%errr_val, ptr %%serr_p, align 4
  %%mode_p = getelementptr %s, ptr %%cp, i32 0, i32 13
  store i64 %%mode, ptr %%mode_p, align 8
  ; spawn timeout: store the absolute deadline (now + timeout_ms) and the
  ; kill signal so the event-loop dispatch can kill a slow child (ADR-00764).
  ; timeout_ms 0 → no deadline.
  %%hasto = icmp ne i64 %%timeout_ms, 0
  %%tonow = call i64 @__kml_monotonic_ns()
  %%toms_ns = mul i64 %%timeout_ms, 1000000
  %%todl0 = add i64 %%tonow, %%toms_ns
  %%todl = select i1 %%hasto, i64 %%todl0, i64 0
  %%todl_p = getelementptr %s, ptr %%cp, i32 0, i32 22
  store i64 %%todl, ptr %%todl_p, align 8
  %%toks_p = getelementptr %s, ptr %%cp, i32 0, i32 23
  store i64 %%killsig, ptr %%toks_p, align 8
  %%modebufa = and i64 %%mode, 1
  %%buffered = icmp ne i64 %%modebufa, 0
  br i1 %%buffered, label %%allocbufs, label %%reg
allocbufs:
  %%oacc = call ptr @calloc(i64 1, i64 24)
  %%oacc_p = getelementptr %s, ptr %%cp, i32 0, i32 14
  store ptr %%oacc, ptr %%oacc_p, align 8
  %%eacc = call ptr @calloc(i64 1, i64 24)
  %%eacc_p = getelementptr %s, ptr %%cp, i32 0, i32 15
  store ptr %%eacc, ptr %%eacc_p, align 8
  br label %%reg
reg:
%s
  call void @__kml_cp_register(ptr %%cp)
  ret ptr %%cp
}`, e.cpSpawnStatusSetupIR(), e.cpSpawnForkIR(), nonblock, nonblock, cp, cp, cp, cp, cp, cp, cp, cp, cp, e.cpSpawnFailDetectIR(cp)))

	// stdin write / end
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_cp_stdin_write(ptr %%cp, ptr %%data, i64 %%n) {
entry:
  %%fd_p = getelementptr %s, ptr %%cp, i32 0, i32 1
  %%fd = load i32, ptr %%fd_p, align 4
  %%open = icmp sge i32 %%fd, 0
  br i1 %%open, label %%wr, label %%ret
wr:
  call i64 @write(i32 %%fd, ptr %%data, i64 %%n)
  br label %%ret
ret:
  ret void
}
define void @__kml_cp_stdin_end(ptr %%cp) {
entry:
  %%fd_p = getelementptr %s, ptr %%cp, i32 0, i32 1
  %%fd = load i32, ptr %%fd_p, align 4
  %%open = icmp sge i32 %%fd, 0
  br i1 %%open, label %%cl, label %%ret
cl:
  call i32 @close(i32 %%fd)
  store i32 -1, ptr %%fd_p, align 4
  br label %%ret
ret:
  ret void
}`, cp, cp))
}

// emitCPEventFlags emits __kml_cp_event_flags(i32 status, i64 killsig) which
// decides the streaming 'exit'/'close' (code, signal) shape's present-bit and
// signal number (TDD-00184). POSIX reads the wait status directly — WIFSIGNALED
// is (status & 0x7f) != 0 and WTERMSIG is those low 7 bits. Windows has no
// signalled bit (TerminateProcess just sets an exit code), so it uses the signal
// a .kill() recorded (field 21); a normally-exited child recorded none (0).
// Returned as a register aggregate { i1 present, i32 signum } — present=true and
// signum=0 for a normal exit, present=false and signum=<sig> for a signalled one.
func (e *Emitter) emitCPEventFlags() {
	if runtime.GOOS == "windows" {
		e.emitGlobal(`
define { i1, i32 } @__kml_cp_event_flags(i32 %status, i64 %killsig) {
entry:
  %present = icmp eq i64 %killsig, 0
  %sig32 = trunc i64 %killsig to i32
  %a = insertvalue { i1, i32 } undef, i1 %present, 0
  %b = insertvalue { i1, i32 } %a, i32 %sig32, 1
  ret { i1, i32 } %b
}`)
		return
	}
	e.emitGlobal(`
define { i1, i32 } @__kml_cp_event_flags(i32 %status, i64 %killsig) {
entry:
  %low = and i32 %status, 127
  %present = icmp eq i32 %low, 0
  %signum = select i1 %present, i32 0, i32 %low
  %a = insertvalue { i1, i32 } undef, i1 %present, 0
  %b = insertvalue { i1, i32 } %a, i32 %signum, 1
  ret { i1, i32 } %b
}`)
}

// emitCPSignalName emits __kml_cp_signal_name(i64 n) → the Node signal-name
// string for signal number n, or null for an unmapped number (TDD-00184). The
// numbering is the host platform's (native compile: the binary runs where it was
// built) — signals 1–6, 8, 9, 11, 13–15 share numbers across Linux/macOS, while
// SIGBUS/SIGUSR1/SIGUSR2 differ, so those three are resolved per GOOS. Windows'
// synthetic signals reuse the Linux-style numbers our .kill() records.
func (e *Emitter) emitCPSignalName() {
	var cases, arms strings.Builder
	for _, s := range cpSignalTable() {
		lbl := fmt.Sprintf("s%d", s.num)
		cases.WriteString(fmt.Sprintf("    i64 %d, label %%%s\n", s.num, lbl))
		arms.WriteString(fmt.Sprintf("%s:\n  ret ptr %s\n", lbl, e.internString(s.name)))	}
	e.emitGlobal(fmt.Sprintf(`
define ptr @__kml_cp_signal_name(i64 %%n) {
entry:
  switch i64 %%n, label %%none [
%s  ]
%snone:
  ret ptr null
}`, cases.String(), arms.String()))
}

// cpSignalEntry maps a Node signal name to the host platform's signal number.
type cpSignalEntry struct {
	num  int
	name string
}

// cpSignalTable returns the number↔name mapping for the signals a child is
// realistically terminated by, using the host platform's numbering (native
// compile: the binary runs where it was built). Signals 1–6, 8, 9, 11, 13–15
// share numbers across Linux/macOS; SIGBUS/SIGUSR1/SIGUSR2 differ, so those are
// resolved per GOOS. Windows reuses the Linux-style numbers our .kill() records.
// Shared by __kml_cp_signal_name (number→name for the exit event) and
// child.kill (name→number). TDD-00184.
func cpSignalTable() []cpSignalEntry {
	t := []cpSignalEntry{
		{1, "SIGHUP"}, {2, "SIGINT"}, {3, "SIGQUIT"}, {4, "SIGILL"},
		{5, "SIGTRAP"}, {6, "SIGABRT"}, {8, "SIGFPE"}, {9, "SIGKILL"},
		{11, "SIGSEGV"}, {13, "SIGPIPE"}, {14, "SIGALRM"}, {15, "SIGTERM"},
	}
	if runtime.GOOS == "darwin" {
		t = append(t, cpSignalEntry{10, "SIGBUS"}, cpSignalEntry{30, "SIGUSR1"}, cpSignalEntry{31, "SIGUSR2"})
	} else {
		t = append(t, cpSignalEntry{7, "SIGBUS"}, cpSignalEntry{10, "SIGUSR1"}, cpSignalEntry{12, "SIGUSR2"})
	}
	return t
}

// cpSignalNumber resolves a Node signal name (e.g. "SIGTERM") to the host's
// signal number for child.kill(signal). TDD-00184.
func cpSignalNumber(name string) (int, bool) {
	for _, s := range cpSignalTable() {
		if s.name == name {
			return s.num, true
		}
	}
	return 0, false
}
