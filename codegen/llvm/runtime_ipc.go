// runtime_ipc.go — child_process.fork's self-fork spawn and both ends of its
// IPC channel (TDD-00141). Wire framing (NDJSON string lines) lives in the
// embedded C (ipcsrc/ipc.c); this file is the fd plumbing:
//
//   - parent: __kml_cp_fork (socketpair + fork + re-exec of the current
//     binary with NODE_CHANNEL_FD set), __kml_cp_ipc_drain (event-loop
//     drain of a handle's channel, firing its 'message' listener),
//     __kml_cp_send / __kml_cp_disconnect.
//   - child: the __kml_ipcc_* event-loop hooks (keepalive/fdset_add/
//     dispatch) plus __kml_ipcc_fd (NODE_CHANNEL_FD, parsed once),
//     __kml_ipcc_send, and the single-slot process.on('message') listener.
//
// A plain-spawn program never links the framing C: __kml_cp_dispatch calls
// @__kml_cp_ipc_drain unconditionally, so a no-op stub stands in when fork
// was never used (emitCPRuntimeStubs, called from program finalization).
package llvm

import "fmt"

// ensureCPForkRuntime emits the parent-side fork machinery.
func (e *Emitter) ensureCPForkRuntime() {
	if e.usedCPForkRuntime {
		return
	}
	e.usedCPForkRuntime = true
	e.ensureChildProcRuntime()
	e.ensureIPCDecls()
	e.ensureSprintf()
	e.ensureStrHeaderRuntime()
	e.ensureSetenvDecl()
	e.emitGlobal("declare i32 @socketpair(i32 noundef, i32 noundef, i32 noundef, ptr noundef)")
	e.ensureExecvDecl()

	cp := cpStructIR
	nonblock := httpNonblockFlag()
	chanEnv := e.internString("NODE_CHANNEL_FD")

	// __kml_cp_fork(argsdata, argslen): socketpair + fork; the child dups
	// nothing (stdio is inherited, Node fork's default), sets NODE_CHANNEL_FD
	// to its socket end and re-execs the current executable (argv[0]) with
	// the extra args appended — restarting the program from main() the way
	// Node restarts the forked module. The parent gets a ChildProcess handle
	// whose stdio fds are -1 (none) and whose ipcFd joins the select() loop.
	e.emitGlobal(fmt.Sprintf(`
define ptr @__kml_cp_fork(ptr %%argsdata, i64 %%argslen) {
entry:
  ; Node's forked-child argv layout is [exe, modulePath, ...args] — extra
  ; args start at argv[2]. The self-fork's "module path" slot is the binary
  ; path again, so process.argv[2]-based child branching lines up.
  %%argv0data = load ptr, ptr @__argv_ptr, align 8
  %%argv0 = load ptr, ptr %%argv0data, align 8
  %%argvlen = add i64 %%argslen, 3
  %%argvbytes = mul i64 %%argvlen, 8
  %%argv = call ptr @malloc(i64 %%argvbytes)
  store ptr %%argv0, ptr %%argv, align 8
  %%slot1 = getelementptr ptr, ptr %%argv, i64 1
  store ptr %%argv0, ptr %%slot1, align 8
  %%argvoff2 = getelementptr ptr, ptr %%argv, i64 2
  %%hasargs = icmp sgt i64 %%argslen, 0
  br i1 %%hasargs, label %%copyargs, label %%setnull
copyargs:
  %%copybytes = mul i64 %%argslen, 8
  call ptr @memcpy(ptr %%argvoff2, ptr %%argsdata, i64 %%copybytes)
  br label %%setnull
setnull:
  %%nullidx = add i64 %%argslen, 2
  %%nullslot = getelementptr ptr, ptr %%argv, i64 %%nullidx
  store ptr null, ptr %%nullslot, align 8

  %%sv = alloca [2 x i32], align 4
  %%svp = getelementptr [2 x i32], ptr %%sv, i32 0, i32 0
  call i32 @socketpair(i32 1, i32 1, i32 0, ptr %%svp)
  %%p0_p = getelementptr [2 x i32], ptr %%sv, i32 0, i32 0
  %%p1_p = getelementptr [2 x i32], ptr %%sv, i32 0, i32 1
  %%pfd = load i32, ptr %%p0_p, align 4
  %%cfd = load i32, ptr %%p1_p, align 4

  %%pid = call i32 @fork()
  %%ischild = icmp eq i32 %%pid, 0
  br i1 %%ischild, label %%child, label %%parent
child:
  call i32 @close(i32 %%pfd)
  %%numbuf = alloca [16 x i8], align 1
  %%numptr = getelementptr [16 x i8], ptr %%numbuf, i32 0, i32 0
  %%cfd64 = sext i32 %%cfd to i64
  call i32 (ptr, ptr, ...) @sprintf(ptr %%numptr, ptr %s, i64 %%cfd64)
  call i32 @setenv(ptr %s, ptr %%numptr, i32 1)
  call i32 @execv(ptr %%argv0, ptr %%argv)
  call void @_exit(i32 127)
  unreachable
parent:
  call i32 @close(i32 %%cfd)
  %%pid64 = zext i32 %%pid to i64
  %%cp = call ptr @__kml_cp_wrap_ipc(i64 %%pid64, i32 %%pfd)
  ret ptr %%cp
}`, e.internString("%lld"), chanEnv))

	// __kml_cp_wrap_ipc(pid, fd): build + register a ChildProcess handle
	// around an already-created IPC socket (parent end): inherited stdio (no
	// pipes), fd made non-blocking, a fresh line-buffer channel. Shared by
	// __kml_cp_fork and cluster.fork (runtime_cluster.go).
	e.emitGlobal(fmt.Sprintf(`
define ptr @__kml_cp_wrap_ipc(i64 %%pid64, i32 %%pfd) {
entry:
  %%fl = call i32 (i32, i32, ...) @fcntl(i32 %%pfd, i32 3)
  %%fln = or i32 %%fl, %d
  call i32 (i32, i32, ...) @fcntl(i32 %%pfd, i32 4, i32 %%fln)
  %%cp = call ptr @calloc(i64 1, i64 160)
  %%pid_p = getelementptr %s, ptr %%cp, i32 0, i32 0
  store i64 %%pid64, ptr %%pid_p, align 8
  ; stdio is inherited — no pipes on the handle
  %%sin_p = getelementptr %s, ptr %%cp, i32 0, i32 1
  store i32 -1, ptr %%sin_p, align 4
  %%sout_p = getelementptr %s, ptr %%cp, i32 0, i32 2
  store i32 -1, ptr %%sout_p, align 4
  %%serr_p = getelementptr %s, ptr %%cp, i32 0, i32 3
  store i32 -1, ptr %%serr_p, align 4
  %%ipc_p = getelementptr %s, ptr %%cp, i32 0, i32 17
  store i32 %%pfd, ptr %%ipc_p, align 4
  %%chanv = call ptr @__kml_ipc_chan_new()
  %%chan_p = getelementptr %s, ptr %%cp, i32 0, i32 19
  store ptr %%chanv, ptr %%chan_p, align 8
  call void @__kml_cp_register(ptr %%cp)
  ret ptr %%cp
}`, nonblock, cp, cp, cp, cp, cp, cp))

	// __kml_cp_ipc_drain(cp): read the channel until EAGAIN/EOF, feed the
	// C-side line buffer, then fire the 'message' listener once per decoded
	// line. EOF closes the fd (state -1) so finalize can proceed.
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_cp_ipc_drain(ptr %%cp) {
entry:
  %%chunk = alloca [4096 x i8], align 1
  %%chunkptr = getelementptr [4096 x i8], ptr %%chunk, i32 0, i32 0
  %%ipc_p = getelementptr %s, ptr %%cp, i32 0, i32 17
  %%chan_p = getelementptr %s, ptr %%cp, i32 0, i32 19
  %%chanv = load ptr, ptr %%chan_p, align 8
  br label %%loop
loop:
  %%fd = load i32, ptr %%ipc_p, align 4
  %%open = icmp sgt i32 %%fd, 0
  br i1 %%open, label %%doread, label %%deliver
doread:
  %%n = call i64 @read(i32 %%fd, ptr %%chunkptr, i64 4096)
  %%hasdata = icmp sgt i64 %%n, 0
  br i1 %%hasdata, label %%feed, label %%ckeof
feed:
  call void @__kml_ipc_feed(ptr %%chanv, ptr %%chunkptr, i64 %%n)
  br label %%loop
ckeof:
  %%iseof = icmp eq i64 %%n, 0
  br i1 %%iseof, label %%oneof, label %%deliver
oneof:
  call i32 @close(i32 %%fd)
  store i32 -1, ptr %%ipc_p, align 4
  br label %%deliver
deliver:
  %%haschan = icmp ne ptr %%chanv, null
  br i1 %%haschan, label %%take, label %%ret
take:
  %%msg = call ptr @__kml_ipc_take(ptr %%chanv)
  %%hasmsg = icmp ne ptr %%msg, null
  br i1 %%hasmsg, label %%fire, label %%ret
fire:
  %%mL_p = getelementptr %s, ptr %%cp, i32 0, i32 18
  %%mL = load ptr, ptr %%mL_p, align 8
  %%hasL = icmp ne ptr %%mL, null
  br i1 %%hasL, label %%docall, label %%freemsg
docall:
  %%mlen = call i64 @strlen(ptr %%msg)
  %%mstr = call ptr @__kml_str_alloc(i64 %%mlen)
  call ptr @memcpy(ptr %%mstr, ptr %%msg, i64 %%mlen)
  %%mnul = getelementptr i8, ptr %%mstr, i64 %%mlen
  store i8 0, ptr %%mnul, align 1
  %%fp_p = getelementptr { ptr, ptr }, ptr %%mL, i32 0, i32 0
  %%fp = load ptr, ptr %%fp_p, align 8
  %%ep_p = getelementptr { ptr, ptr }, ptr %%mL, i32 0, i32 1
  %%ep = load ptr, ptr %%ep_p, align 8
  call void %%fp(ptr %%ep, ptr %%mstr)
  br label %%freemsg
freemsg:
  call void @free(ptr %%msg)
  br label %%take
ret:
  ret void
}`, cp, cp, cp))

	// __kml_cp_send(cp, s): JSON-quote + newline + write. false once closed.
	e.emitGlobal(fmt.Sprintf(`
define i1 @__kml_cp_send(ptr %%cp, ptr %%s) {
entry:
  %%ipc_p = getelementptr %s, ptr %%cp, i32 0, i32 17
  %%fd = load i32, ptr %%ipc_p, align 4
  %%open = icmp sgt i32 %%fd, 0
  br i1 %%open, label %%wr, label %%no
wr:
  %%fd64 = sext i32 %%fd to i64
  %%ok = call i64 @__kml_ipc_send(i64 %%fd64, ptr %%s)
  %%okb = icmp ne i64 %%ok, 0
  ret i1 %%okb
no:
  ret i1 0
}
define void @__kml_cp_disconnect(ptr %%cp) {
entry:
  %%ipc_p = getelementptr %s, ptr %%cp, i32 0, i32 17
  %%fd = load i32, ptr %%ipc_p, align 4
  %%open = icmp sgt i32 %%fd, 0
  br i1 %%open, label %%cl, label %%ret
cl:
  call i32 @close(i32 %%fd)
  store i32 -1, ptr %%ipc_p, align 4
  br label %%ret
ret:
  ret void
}`, cp, cp))
}

// ensureIPCChildRuntime emits the child-side channel: NODE_CHANNEL_FD
// parsing, send, and the event-loop hooks that deliver 'message' events.
func (e *Emitter) ensureIPCChildRuntime() {
	if e.usedIPCChildRuntime {
		return
	}
	e.usedIPCChildRuntime = true
	e.ensureIPCDecls()
	e.ensureMalloc()
	e.ensureFree()
	e.ensureMemcpy()
	e.ensureStrlen()
	e.ensureStrHeaderRuntime()
	e.ensureReadDecl()
	e.ensureCloseDecl()
	e.ensureFcntlDecl()
	e.ensureWorkerFdSetbit()
	e.ensureAtoll()
	e.ensureGetenv()

	// -2 = not yet probed · -1 = no channel · >0 = the channel fd
	e.emitGlobal("@__kml_ipcc_fd_g = internal global i32 -2, align 4")
	e.emitGlobal("@__kml_ipcc_chan = internal global ptr null, align 8")
	e.emitGlobal("@__kml_ipcc_msg_listener = internal global ptr null, align 8")
	chanEnv := e.internString("NODE_CHANNEL_FD")
	nonblock := httpNonblockFlag()

	e.emitGlobal(fmt.Sprintf(`
define i32 @__kml_ipcc_fd() {
entry:
  %%cur = load i32, ptr @__kml_ipcc_fd_g, align 4
  %%probed = icmp ne i32 %%cur, -2
  br i1 %%probed, label %%have, label %%probe
have:
  ret i32 %%cur
probe:
  %%env = call ptr @getenv(ptr %s)
  %%hasenv = icmp ne ptr %%env, null
  br i1 %%hasenv, label %%parse, label %%none
parse:
  %%v = call i64 @atoll(ptr %%env)
  %%fd = trunc i64 %%v to i32
  %%valid = icmp sgt i32 %%fd, 0
  br i1 %%valid, label %%init, label %%none
init:
  ; nonblocking + a line buffer, ready for the event loop
  %%fl = call i32 (i32, i32, ...) @fcntl(i32 %%fd, i32 3)
  %%fln = or i32 %%fl, %d
  call i32 (i32, i32, ...) @fcntl(i32 %%fd, i32 4, i32 %%fln)
  %%chanv = call ptr @__kml_ipc_chan_new()
  store ptr %%chanv, ptr @__kml_ipcc_chan, align 8
  store i32 %%fd, ptr @__kml_ipcc_fd_g, align 4
  ret i32 %%fd
none:
  store i32 -1, ptr @__kml_ipcc_fd_g, align 4
  ret i32 -1
}`, chanEnv, nonblock))

	e.emitGlobal(`
define i1 @__kml_ipcc_send(ptr %s) {
entry:
  %fd = call i32 @__kml_ipcc_fd()
  %open = icmp sgt i32 %fd, 0
  br i1 %open, label %wr, label %no
wr:
  %fd64 = sext i32 %fd to i64
  %ok = call i64 @__kml_ipc_send(i64 %fd64, ptr %s)
  %okb = icmp ne i64 %ok, 0
  ret i1 %okb
no:
  ret i1 0
}
define void @__kml_ipcc_disconnect() {
entry:
  %fd = load i32, ptr @__kml_ipcc_fd_g, align 4
  %open = icmp sgt i32 %fd, 0
  br i1 %open, label %cl, label %ret
cl:
  call i32 @close(i32 %fd)
  store i32 -1, ptr @__kml_ipcc_fd_g, align 4
  br label %ret
ret:
  ret void
}
define i1 @__kml_ipcc_keepalive() {
entry:
  ; hold the loop open while the channel is open AND a listener is armed —
  ; Node refs the loop for an open IPC channel with a message listener.
  %fd = load i32, ptr @__kml_ipcc_fd_g, align 4
  %open = icmp sgt i32 %fd, 0
  %l = load ptr, ptr @__kml_ipcc_msg_listener, align 8
  %hasl = icmp ne ptr %l, null
  %keep = and i1 %open, %hasl
  ret i1 %keep
}
define i1 @__kml_ipcc_fdset_add(ptr %fdset, ptr %maxfd) {
entry:
  %fd = load i32, ptr @__kml_ipcc_fd_g, align 4
  %open = icmp sgt i32 %fd, 0
  br i1 %open, label %add, label %ret
add:
  call void @__kml_worker_fd_setbit(i32 %fd, ptr %fdset, ptr %maxfd)
  br label %ret
ret:
  ret i1 0
}
define void @__kml_ipcc_dispatch() {
entry:
  %chunk = alloca [4096 x i8], align 1
  %chunkptr = getelementptr [4096 x i8], ptr %chunk, i32 0, i32 0
  %chanv = load ptr, ptr @__kml_ipcc_chan, align 8
  br label %loop
loop:
  %fd = load i32, ptr @__kml_ipcc_fd_g, align 4
  %open = icmp sgt i32 %fd, 0
  br i1 %open, label %doread, label %deliver
doread:
  %n = call i64 @read(i32 %fd, ptr %chunkptr, i64 4096)
  %hasdata = icmp sgt i64 %n, 0
  br i1 %hasdata, label %feed, label %ckeof
feed:
  call void @__kml_ipc_feed(ptr %chanv, ptr %chunkptr, i64 %n)
  br label %loop
ckeof:
  %iseof = icmp eq i64 %n, 0
  br i1 %iseof, label %oneof, label %deliver
oneof:
  call i32 @close(i32 %fd)
  store i32 -1, ptr @__kml_ipcc_fd_g, align 4
  br label %deliver
deliver:
  %haschan = icmp ne ptr %chanv, null
  br i1 %haschan, label %take, label %ret
take:
  %msg = call ptr @__kml_ipc_take(ptr %chanv)
  %hasmsg = icmp ne ptr %msg, null
  br i1 %hasmsg, label %fire, label %ret
fire:
  %mL = load ptr, ptr @__kml_ipcc_msg_listener, align 8
  %hasL = icmp ne ptr %mL, null
  br i1 %hasL, label %docall, label %freemsg
docall:
  %mlen = call i64 @strlen(ptr %msg)
  %mstr = call ptr @__kml_str_alloc(i64 %mlen)
  call ptr @memcpy(ptr %mstr, ptr %msg, i64 %mlen)
  %mnul = getelementptr i8, ptr %mstr, i64 %mlen
  store i8 0, ptr %mnul, align 1
  %fp_p = getelementptr { ptr, ptr }, ptr %mL, i32 0, i32 0
  %fp = load ptr, ptr %fp_p, align 8
  %ep_p = getelementptr { ptr, ptr }, ptr %mL, i32 0, i32 1
  %ep = load ptr, ptr %ep_p, align 8
  call void %fp(ptr %ep, ptr %mstr)
  br label %freemsg
freemsg:
  call void @free(ptr %msg)
  br label %take
ret:
  ret void
}`)
}

// emitCPRuntimeStubs finalizes the fork/IPC symbols other runtimes reference
// unconditionally: a no-op @__kml_cp_ipc_drain when child_process is present
// without fork, and the child-side __kml_ipcc_* loop hooks when the program
// never touches the channel. Called from program finalization beside
// emitLoopTaskStubs.
func (e *Emitter) emitCPRuntimeStubs() {
	if e.usedChildProcRuntime && !e.usedCPForkRuntime {
		e.emitGlobal("define void @__kml_cp_ipc_drain(ptr %cp) {\nentry:\n  ret void\n}")
	}
	if e.usedHTTP && !e.usedIPCChildRuntime {
		e.emitGlobal("define i1 @__kml_ipcc_keepalive() {\nentry:\n  ret i1 0\n}")
		e.emitGlobal("define i1 @__kml_ipcc_fdset_add(ptr %fdset, ptr %maxfd) {\nentry:\n  ret i1 0\n}")
		e.emitGlobal("define void @__kml_ipcc_dispatch() {\nentry:\n  ret void\n}")
	}
}
