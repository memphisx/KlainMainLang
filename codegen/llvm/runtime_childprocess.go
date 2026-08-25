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

import "fmt"

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
const cpStructIR = "{ i64, i32, i32, i32, i64, i64, ptr, ptr, ptr, ptr, ptr, ptr, ptr, i64, ptr, ptr, ptr }"

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

	e.emitGlobal("declare i32 @pipe(ptr noundef)")
	e.ensureForkDecl()
	e.emitGlobal("declare i32 @dup2(i32 noundef, i32 noundef)")
	e.ensureCloseDecl()
	e.emitGlobal("declare i32 @execvp(ptr noundef, ptr noundef)")
	e.emitGlobal("declare void @_exit(i32 noundef) noreturn")
	e.ensureReadDecl()
	e.ensureWriteDecl()
	e.ensureWaitpidDecl()
	e.ensureFcntlDecl()

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
  %streaming = icmp eq i64 %mode, 0
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
  %streaming2 = icmp eq i64 %mode, 0
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
	e.emitGlobal(fmt.Sprintf(`
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
  %%code64 = zext i32 %%codev to i64
  %%ec_p = getelementptr %s, ptr %%cp, i32 0, i32 5
  store i64 %%code64, ptr %%ec_p, align 8
  %%st_p = getelementptr %s, ptr %%cp, i32 0, i32 4
  store i64 2, ptr %%st_p, align 8
  %%mode_p = getelementptr %s, ptr %%cp, i32 0, i32 13
  %%mode = load i64, ptr %%mode_p, align 8
  %%buffered = icmp ne i64 %%mode, 0
  br i1 %%buffered, label %%bufcb, label %%streamcb
streamcb:
  ; fire 'exit'(code) then 'close'(code)
  %%exitL_p = getelementptr %s, ptr %%cp, i32 0, i32 11
  %%exitL = load ptr, ptr %%exitL_p, align 8
  %%hasExit = icmp ne ptr %%exitL, null
  br i1 %%hasExit, label %%callexit, label %%aftexit
callexit:
  %%xfp_p = getelementptr { ptr, ptr }, ptr %%exitL, i32 0, i32 0
  %%xfp = load ptr, ptr %%xfp_p, align 8
  %%xep_p = getelementptr { ptr, ptr }, ptr %%exitL, i32 0, i32 1
  %%xep = load ptr, ptr %%xep_p, align 8
  call void %%xfp(ptr %%xep, i64 %%code64)
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
  call void %%cfp(ptr %%cep, i64 %%code64)
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
}`, cp, cp, cp, cp, cp, cp, cp, cp, cp, errName))

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
  %%ofd = load i32, ptr %%fd2, align 4
  %%efd = load i32, ptr %%fd3, align 4
  %%oclosed = icmp slt i32 %%ofd, 0
  %%eclosed = icmp slt i32 %%efd, 0
  %%botheof = and i1 %%oclosed, %%eclosed
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
}`, cp, cp, cp, cp, cp, cp, cp, cp, cp, cp))

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
  br i1 %%eopen, label %%adde, label %%chkforce
adde:
  call void @__kml_worker_fd_setbit(i32 %%efd, ptr %%fdset, ptr %%maxfd)
  br label %%chkforce
chkforce:
  %%obad = icmp slt i32 %%ofd, 0
  %%ebad = icmp slt i32 %%efd, 0
  %%both = and i1 %%obad, %%ebad
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
}`, cp, cp, cp))

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
  br i1 %%live, label %%yes, label %%next
next:
  %%inext = add i64 %%iv, 1
  store i64 %%inext, ptr %%i, align 8
  br label %%loop
yes:
  ret i1 1
no:
  ret i1 0
}`, cp))

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
define ptr @__kml_cp_spawn(ptr %%file, ptr %%argsdata, i64 %%argslen, i64 %%mode) {
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

  %%pid = call i32 @fork()
  %%ischild = icmp eq i32 %%pid, 0
  br i1 %%ischild, label %%child, label %%parent
child:
  call i32 @dup2(i32 %%inr, i32 0)
  call i32 @dup2(i32 %%outw, i32 1)
  call i32 @dup2(i32 %%errw, i32 2)
  call i32 @close(i32 %%inr)
  call i32 @close(i32 %%inw)
  call i32 @close(i32 %%outr)
  call i32 @close(i32 %%outw)
  call i32 @close(i32 %%errr)
  call i32 @close(i32 %%errw)
  call i32 @execvp(ptr %%file, ptr %%argv)
  call void @_exit(i32 127)
  unreachable
parent:
  call i32 @close(i32 %%inr)
  call i32 @close(i32 %%outw)
  call i32 @close(i32 %%errw)
  ; make the two read ends non-blocking
  %%ofl = call i32 (i32, i32, ...) @fcntl(i32 %%outr, i32 3)
  %%ofln = or i32 %%ofl, %d
  call i32 (i32, i32, ...) @fcntl(i32 %%outr, i32 4, i32 %%ofln)
  %%efl = call i32 (i32, i32, ...) @fcntl(i32 %%errr, i32 3)
  %%efln = or i32 %%efl, %d
  call i32 (i32, i32, ...) @fcntl(i32 %%errr, i32 4, i32 %%efln)

  %%cp = call ptr @calloc(i64 1, i64 136)
  %%pid_p = getelementptr %s, ptr %%cp, i32 0, i32 0
  %%pid64 = zext i32 %%pid to i64
  store i64 %%pid64, ptr %%pid_p, align 8
  %%sin_p = getelementptr %s, ptr %%cp, i32 0, i32 1
  store i32 %%inw, ptr %%sin_p, align 4
  %%sout_p = getelementptr %s, ptr %%cp, i32 0, i32 2
  store i32 %%outr, ptr %%sout_p, align 4
  %%serr_p = getelementptr %s, ptr %%cp, i32 0, i32 3
  store i32 %%errr, ptr %%serr_p, align 4
  %%mode_p = getelementptr %s, ptr %%cp, i32 0, i32 13
  store i64 %%mode, ptr %%mode_p, align 8
  %%buffered = icmp ne i64 %%mode, 0
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
  call void @__kml_cp_register(ptr %%cp)
  ret ptr %%cp
}`, nonblock, nonblock, cp, cp, cp, cp, cp, cp, cp))

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
