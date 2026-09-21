// runtime_childprocess_exit.go — child exit as an event (TDD-00223 §5).
//
// The event loop reaps children with waitpid(WNOHANG) every iteration
// (__kml_cp_reap); what this file supplies is the *wake*: the loop must leave
// select() the moment a child ends, even when the child's stdio is silent,
// closed, or held open by a grandchild. libuv's mechanism on each platform:
//
//   - POSIX: a SIGCHLD handler writes one byte to a non-blocking self-pipe
//     whose read end sits in the loop's read set. A self-pipe rather than the
//     EINTR that select() returns on a signal: a signal landing between the
//     loop's last check and its select() call interrupts nothing, and the
//     exit would go unnoticed until some unrelated wake.
//   - Windows: a registered wait on the process handle posts a packet to the
//     reactor's completion port (win32proc.c __kml_win_child_watch).
//
// Hooks the child_process runtime calls:
//
//	__kml_cp_watch_init()        before a spawn — POSIX installs pipe+handler
//	__kml_cp_watch_pid(i32 pid)  after a spawn — Windows registers the wait
//	__kml_cp_wake_fdset_add(fdset, maxfd) -> i1
//	                             adds the self-pipe; true once after a new
//	                             registration, forcing one non-blocking pass so
//	                             a child that died before the watch existed is
//	                             still reaped at once
//	__kml_cp_wake_drain()        empties the self-pipe
package llvm

import "fmt"

func (e *Emitter) ensureCPExitWake() {
	if e.usedCPExitWake {
		return
	}
	e.usedCPExitWake = true
	e.ensureWorkerFdSetbit()
	// Set by __kml_cp_register: the next fdset_add reports "don't block".
	e.emitGlobal("@__kml_cp_kick = internal global i1 false, align 1")
	if targetGOOS() == "windows" {
		e.emitGlobal("declare void @__kml_win_child_watch(i32 noundef)")
		e.emitGlobal(`
define void @__kml_cp_watch_init() {
  ret void
}
define void @__kml_cp_watch_pid(i32 %pid) {
  call void @__kml_win_child_watch(i32 %pid)
  ret void
}
define i1 @__kml_cp_wake_fdset_add(ptr %fdset, ptr %maxfd) {
  %k = load i1, ptr @__kml_cp_kick, align 1
  store i1 false, ptr @__kml_cp_kick, align 1
  ret i1 %k
}
define void @__kml_cp_wake_drain() {
  ret void
}`)
		return
	}
	e.ensureSignalDecl()
	e.ensureGetpid()
	e.ensureReadDecl()
	e.ensureWriteDecl()
	e.ensureFcntlDecl()
	e.ensureErrnoAccessor()
	sigchld := signalNumbers()["SIGCHLD"]
	// The pipe belongs to the process that created it. A forked cluster worker
	// inherits both ends and the handler; were it to read the parent's pipe it
	// would swallow the parent's wake bytes, so every use is gated on the owner
	// pid and a worker that spawns children makes its own pipe.
	e.emitGlobal("@__kml_cp_wake_r = internal global i32 -1, align 4")
	e.emitGlobal("@__kml_cp_wake_w = internal global i32 -1, align 4")
	e.emitGlobal("@__kml_cp_wake_owner = internal global i32 0, align 4")
	e.emitGlobal(`@.kml_cp_wake_byte = private unnamed_addr constant [1 x i8] c"x"`)
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_cp_sigchld(i32 %%sig) {
entry:
  %%owner = load volatile i32, ptr @__kml_cp_wake_owner, align 4
  %%me = call i32 @getpid()
  %%mine = icmp eq i32 %%owner, %%me
  br i1 %%mine, label %%wake, label %%ret
wake:
  ; async-signal-safe: getpid and write only, errno preserved for the
  ; interrupted code
  %%ep = call ptr @%[1]s()
  %%saved = load i32, ptr %%ep, align 4
  %%w = load volatile i32, ptr @__kml_cp_wake_w, align 4
  %%n = call i64 @write(i32 %%w, ptr @.kml_cp_wake_byte, i64 1)
  store i32 %%saved, ptr %%ep, align 4
  br label %%ret
ret:
  ret void
}

define void @__kml_cp_watch_init() {
entry:
  %%owner = load i32, ptr @__kml_cp_wake_owner, align 4
  %%me = call i32 @getpid()
  %%have = icmp eq i32 %%owner, %%me
  br i1 %%have, label %%ret, label %%make
make:
  %%fds = alloca [2 x i32], align 4
  %%rc = call i32 @pipe(ptr %%fds)
  %%ok = icmp eq i32 %%rc, 0
  br i1 %%ok, label %%setup, label %%ret
setup:
  %%r_p = getelementptr [2 x i32], ptr %%fds, i32 0, i32 0
  %%w_p = getelementptr [2 x i32], ptr %%fds, i32 0, i32 1
  %%r = load i32, ptr %%r_p, align 4
  %%w = load i32, ptr %%w_p, align 4
  ; both ends non-blocking (a full pipe must never stall the handler; an
  ; empty one must never stall the drain) and close-on-exec
  %%rfl = call i32 (i32, i32, ...) @fcntl(i32 %%r, i32 3)
  %%rfl2 = or i32 %%rfl, %[2]d
  call i32 (i32, i32, ...) @fcntl(i32 %%r, i32 4, i32 %%rfl2)
  %%wfl = call i32 (i32, i32, ...) @fcntl(i32 %%w, i32 3)
  %%wfl2 = or i32 %%wfl, %[2]d
  call i32 (i32, i32, ...) @fcntl(i32 %%w, i32 4, i32 %%wfl2)
  call i32 (i32, i32, ...) @fcntl(i32 %%r, i32 2, i32 1)
  call i32 (i32, i32, ...) @fcntl(i32 %%w, i32 2, i32 1)
  store volatile i32 %%r, ptr @__kml_cp_wake_r, align 4
  store volatile i32 %%w, ptr @__kml_cp_wake_w, align 4
  store volatile i32 %%me, ptr @__kml_cp_wake_owner, align 4
  %%old = call ptr @signal(i32 %[3]d, ptr @__kml_cp_sigchld)
  br label %%ret
ret:
  ret void
}

define void @__kml_cp_watch_pid(i32 %%pid) {
  ret void
}

define i1 @__kml_cp_wake_fdset_add(ptr %%fdset, ptr %%maxfd) {
entry:
  %%k = load i1, ptr @__kml_cp_kick, align 1
  store i1 false, ptr @__kml_cp_kick, align 1
  %%owner = load i32, ptr @__kml_cp_wake_owner, align 4
  %%me = call i32 @getpid()
  %%mine = icmp eq i32 %%owner, %%me
  br i1 %%mine, label %%add, label %%ret
add:
  %%r = load i32, ptr @__kml_cp_wake_r, align 4
  call void @__kml_worker_fd_setbit(i32 %%r, ptr %%fdset, ptr %%maxfd)
  br label %%ret
ret:
  ret i1 %%k
}

define void @__kml_cp_wake_drain() {
entry:
  %%owner = load i32, ptr @__kml_cp_wake_owner, align 4
  %%me = call i32 @getpid()
  %%mine = icmp eq i32 %%owner, %%me
  br i1 %%mine, label %%loop, label %%ret
loop:
  %%buf = alloca [64 x i8], align 1
  br label %%rd
rd:
  %%r = load i32, ptr @__kml_cp_wake_r, align 4
  %%n = call i64 @read(i32 %%r, ptr %%buf, i64 64)
  %%more = icmp eq i64 %%n, 64
  br i1 %%more, label %%rd, label %%ret
ret:
  ret void
}`, errnoAccessor(), httpNonblockFlag(), sigchld))
}
