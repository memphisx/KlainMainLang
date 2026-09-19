// runtime_cluster.go — Node `cluster` module (TDD-00105).
//
// cluster.fork() forks the current process and, in the child, re-execs the
// program's own executable with KML_CLUSTER_WORKER_ID=<n> in the environment.
// The re-exec'd worker runs main() from the top with fresh state — matching
// Node's "a worker runs the entry file from the top" semantics, avoiding the
// fork bomb a plain fork() would cause inside the primary's `for` loop, and
// sidestepping the fiber-safety constraint (the child image is replaced before
// any event-loop/fiber state exists). Worker identity is unified with the
// http.listen({workers}) machinery: @__kml_cluster_worker_id (declared by
// ensureHTTPClusterFork) is seeded from the env at startup for a re-exec'd
// worker, and cluster.isPrimary/isWorker read it. Workers each bind the port
// independently via SO_REUSEPORT (runtime_http.go).
package llvm

import (
	"fmt"
)

// clusterWorkerIR: a Worker handle returned by cluster.fork() — { i64 id,
// i64 pid, ptr cp }. Field 0 is the worker id, field 1 the child pid, field
// 2 the ChildProcess handle carrying the worker's IPC channel (TDD-00141
// reuse): worker.send/.on('message'/'exit'/...) route through it.
const clusterWorkerIR = "{ i64, i64, ptr }"

func (e *Emitter) ensureClusterRuntime() {
	if e.usedClusterRuntime {
		return
	}
	e.usedClusterRuntime = true
	e.ensureHTTPClusterFork() // declares @__kml_cluster_worker_id + fork()
	e.ensureCPForkRuntime()   // socketpair + __kml_cp_wrap_ipc (worker IPC channel)
	if targetGOOS() == "windows" {
		e.ensureUnsetenv() // clusterForkIR clears the worker env vars after spawning
	}
	e.ensureMalloc()
	e.ensureRealloc()
	e.ensureMemset()
	e.ensureFflushDecl()
	e.ensureSprintf()
	e.ensureGetenv()

	e.ensureSetenvDecl()
	e.ensureAtoiDecl()
	e.ensureExecvDecl()
	e.ensureExitRawDecl()
	e.ensureWaitpidDecl()
	if targetGOOS() == "darwin" {
		e.emitGlobal("declare i32 @_NSGetExecutablePath(ptr noundef, ptr noundef)")
	} else {
		e.ensureReadlinkDecl()
	}

	e.ensureMicrotasks()       // cluster.on('online') relays fire as queued microtasks
	e.ensureIPCChildRuntime()  // worker-side listening announcement (__kml_ipcc_*)
	e.ensureChildProcRuntime() // the reap hook global lives with __kml_cp_finalize

	e.emitGlobal("@__kml_cluster_next_id = internal global i64 1, align 8")
	e.emitGlobal("@__kml_cluster_pids = internal global ptr null, align 8")
	e.emitGlobal("@__kml_cluster_pid_len = internal global i64 0, align 8")
	e.emitGlobal("@__kml_cluster_pid_cap = internal global i64 0, align 8")
	// Parallel Worker-handle table (same len/cap as the pid table): the live
	// registry behind cluster.workers / cluster.disconnect() and the
	// cluster-level event relays.
	e.emitGlobal("@__kml_cluster_wrks = internal global ptr null, align 8")
	// setupPrimary settings (ADR: exec is a genuine impossibility under the
	// re-exec-self model and is rejected at compile time; args and silent are
	// honored): args = a NULL-terminated C-string vector for worker argv
	// (replacing the inherited argv tail), silent = pipe the workers' stdio
	// to the Worker handle instead of inheriting.
	e.emitGlobal("@__kml_cluster_setup_args = internal global ptr null, align 8")
	e.emitGlobal("@__kml_cluster_setup_args_len = internal global i64 0, align 8")
	e.emitGlobal("@__kml_cluster_setup_silent = internal global i64 0, align 8")
	// Cluster-level relay registration (one listener per event, the module's
	// standing convention): the compiled adapter fn + the user closure header.
	// Set by cluster.on(...); read at fork time (exit/msg arm each new worker)
	// or at hook-fire time (disc/listening go through the cp hooks below).
	for _, evt := range []string{"exit", "msg", "fork", "online", "disc", "listening"} {
		e.emitGlobal(fmt.Sprintf("@__kml_cluster_relay_%s_fn = internal global ptr null, align 8", evt))
		e.emitGlobal(fmt.Sprintf("@__kml_cluster_relay_%s_hdr = internal global ptr null, align 8", evt))
	}

	envName := e.internString("KML_CLUSTER_WORKER_ID")
	chanEnvName := e.internString("NODE_CHANNEL_FD")
	idFmt := e.internString("%lld")

	// __kml_cluster_seed_id(): a re-exec'd worker carries its id in the env;
	// read it into @__kml_cluster_worker_id so isPrimary/isWorker/workerId work.
	// The primary (env unset) leaves the global at 0. Called first thing in
	// main. Also arms the cross-runtime hooks in every process: the worker's
	// listening announcement, and the primary's reap/disconnect/control hooks
	// (each a no-op on the side that never fires it).
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_cluster_seed_id() {
entry:
  store ptr @__kml_cluster_announce_listening, ptr @__kml_http_listening_announce, align 8
  store ptr @__kml_cluster_on_reap, ptr @__kml_cp_reaped_hook, align 8
  store ptr @__kml_cluster_on_disc, ptr @__kml_cp_disc_hook, align 8
  store ptr @__kml_cluster_on_ctrl, ptr @__kml_cp_ctrl_hook, align 8
  %%v = call ptr @getenv(ptr %s)
  %%isnull = icmp eq ptr %%v, null
  br i1 %%isnull, label %%done, label %%seed
seed:
  %%id32 = call i32 @atoi(ptr %%v)
  %%id64 = sext i32 %%id32 to i64
  store i64 %%id64, ptr @__kml_cluster_worker_id, align 8
  br label %%done
done:
  ret void
}`, envName))

	// __kml_cluster_self_exe(): path to the running executable, for re-exec.
	if targetGOOS() == "darwin" {
		e.emitGlobal(`
define ptr @__kml_cluster_self_exe() {
entry:
  %buf = call ptr @malloc(i64 4096)
  %szslot = alloca i32, align 4
  store i32 4096, ptr %szslot, align 4
  call i32 @_NSGetExecutablePath(ptr %buf, ptr %szslot)
  ret ptr %buf
}`)
	} else {
		procSelf := e.internString("/proc/self/exe")
		e.emitGlobal(fmt.Sprintf(`
define ptr @__kml_cluster_self_exe() {
entry:
  %%buf = call ptr @malloc(i64 4096)
  %%n = call i64 @readlink(ptr %s, ptr %%buf, i64 4095)
  %%bad = icmp slt i64 %%n, 0
  br i1 %%bad, label %%zero, label %%term
zero:
  store i8 0, ptr %%buf, align 1
  ret ptr %%buf
term:
  %%endp = getelementptr i8, ptr %%buf, i64 %%n
  store i8 0, ptr %%endp, align 1
  ret ptr %%buf
}`, procSelf))
	}

	// __kml_cluster_register(pid, w): append to the primary's parallel
	// worker-pid / Worker-handle tables (shared len/cap).
	e.emitGlobal(`
define void @__kml_cluster_register(i64 %pid, ptr %w) {
entry:
  %len = load i64, ptr @__kml_cluster_pid_len, align 8
  %cap = load i64, ptr @__kml_cluster_pid_cap, align 8
  %full = icmp sge i64 %len, %cap
  br i1 %full, label %grow, label %store
grow:
  %cap2 = mul i64 %cap, 2
  %atleast4 = icmp sgt i64 %cap2, 4
  %newcap = select i1 %atleast4, i64 %cap2, i64 4
  %olddata = load ptr, ptr @__kml_cluster_pids, align 8
  %bytes = mul i64 %newcap, 8
  %newdata = call ptr @realloc(ptr %olddata, i64 %bytes)
  store ptr %newdata, ptr @__kml_cluster_pids, align 8
  %oldw = load ptr, ptr @__kml_cluster_wrks, align 8
  %neww = call ptr @realloc(ptr %oldw, i64 %bytes)
  store ptr %neww, ptr @__kml_cluster_wrks, align 8
  store i64 %newcap, ptr @__kml_cluster_pid_cap, align 8
  br label %store
store:
  %data = load ptr, ptr @__kml_cluster_pids, align 8
  %slot = getelementptr i64, ptr %data, i64 %len
  store i64 %pid, ptr %slot, align 8
  %wdata = load ptr, ptr @__kml_cluster_wrks, align 8
  %wslot = getelementptr ptr, ptr %wdata, i64 %len
  store ptr %w, ptr %wslot, align 8
  %newlen = add i64 %len, 1
  store i64 %newlen, ptr @__kml_cluster_pid_len, align 8
  ret void
}`)

	// __kml_cluster_attach_exit / _msg(w, fn, hdr): arm one worker with a
	// cluster-level relay — the listener slot (cp field 11 'exit' / 18
	// 'message') gets a closure header { adapterFn, envPair } whose envPair
	// carries { userClosureHdr, worker } so the adapter can pass the Worker
	// as the listener's first argument. One listener per event: a
	// cluster-level relay and a worker.on(...) listener on the same worker
	// overwrite each other (documented).
	for _, ev := range []struct{ name, field string }{{"exit", "11"}, {"msg", "18"}} {
		e.emitGlobal(fmt.Sprintf(`
define void @__kml_cluster_attach_%s(ptr %%w, ptr %%fn, ptr %%hdr) {
entry:
  %%pair = call ptr @malloc(i64 16)
  %%ph = getelementptr { ptr, ptr }, ptr %%pair, i32 0, i32 0
  store ptr %%hdr, ptr %%ph, align 8
  %%pw = getelementptr { ptr, ptr }, ptr %%pair, i32 0, i32 1
  store ptr %%w, ptr %%pw, align 8
  %%lhdr = call ptr @malloc(i64 16)
  %%lf = getelementptr { ptr, ptr }, ptr %%lhdr, i32 0, i32 0
  store ptr %%fn, ptr %%lf, align 8
  %%le = getelementptr { ptr, ptr }, ptr %%lhdr, i32 0, i32 1
  store ptr %%pair, ptr %%le, align 8
  %%cpslot = getelementptr %s, ptr %%w, i32 0, i32 2
  %%cp = load ptr, ptr %%cpslot, align 8
  %%fslot = getelementptr %s, ptr %%cp, i32 0, i32 %s
  call void @__kml_cp_listener_append(ptr %%fslot, ptr %%lhdr)
  ret void
}`, ev.name, clusterWorkerIR, cpStructIR, ev.field))

		// __kml_cluster_install_*(): arm every already-forked worker (the
		// cluster.on(...) call site ran after the fork loop).
		e.emitGlobal(fmt.Sprintf(`
define void @__kml_cluster_install_%s() {
entry:
  %%fn = load ptr, ptr @__kml_cluster_relay_%s_fn, align 8
  %%hdr = load ptr, ptr @__kml_cluster_relay_%s_hdr, align 8
  %%i = alloca i64, align 8
  store i64 0, ptr %%i, align 8
  br label %%loop
loop:
  %%iv = load i64, ptr %%i, align 8
  %%len = load i64, ptr @__kml_cluster_pid_len, align 8
  %%inb = icmp slt i64 %%iv, %%len
  br i1 %%inb, label %%body, label %%done
body:
  %%wdata = load ptr, ptr @__kml_cluster_wrks, align 8
  %%wslot = getelementptr ptr, ptr %%wdata, i64 %%iv
  %%w = load ptr, ptr %%wslot, align 8
  call void @__kml_cluster_attach_%s(ptr %%w, ptr %%fn, ptr %%hdr)
  %%inext = add i64 %%iv, 1
  store i64 %%inext, ptr %%i, align 8
  br label %%loop
done:
  ret void
}`, ev.name, ev.name, ev.name, ev.name))
	}

	// __kml_cluster_worker_by_cp(cp): the registered Worker handle whose
	// embedded ChildProcess is cp, or null.
	e.emitGlobal(fmt.Sprintf(`
define ptr @__kml_cluster_worker_by_cp(ptr %%cp) {
entry:
  %%i = alloca i64, align 8
  store i64 0, ptr %%i, align 8
  br label %%loop
loop:
  %%iv = load i64, ptr %%i, align 8
  %%len = load i64, ptr @__kml_cluster_pid_len, align 8
  %%inb = icmp slt i64 %%iv, %%len
  br i1 %%inb, label %%body, label %%miss
body:
  %%wdata = load ptr, ptr @__kml_cluster_wrks, align 8
  %%wslot = getelementptr ptr, ptr %%wdata, i64 %%iv
  %%w = load ptr, ptr %%wslot, align 8
  %%cpslot = getelementptr %s, ptr %%w, i32 0, i32 2
  %%wcp = load ptr, ptr %%cpslot, align 8
  %%same = icmp eq ptr %%wcp, %%cp
  br i1 %%same, label %%hit, label %%next
hit:
  ret ptr %%w
next:
  %%inext = add i64 %%iv, 1
  store i64 %%inext, ptr %%i, align 8
  br label %%loop
miss:
  ret ptr null
}`, clusterWorkerIR))

	// __kml_cluster_worker_by_id(id): the live Worker with .id == id, or null
	// — cluster.workers[id], Node's id-keyed lookup.
	e.emitGlobal(fmt.Sprintf(`
define ptr @__kml_cluster_worker_by_id(i64 %%id) {
entry:
  %%i = alloca i64, align 8
  store i64 0, ptr %%i, align 8
  br label %%loop
loop:
  %%iv = load i64, ptr %%i, align 8
  %%len = load i64, ptr @__kml_cluster_pid_len, align 8
  %%inb = icmp slt i64 %%iv, %%len
  br i1 %%inb, label %%body, label %%miss
body:
  %%wdata = load ptr, ptr @__kml_cluster_wrks, align 8
  %%wslot = getelementptr ptr, ptr %%wdata, i64 %%iv
  %%w = load ptr, ptr %%wslot, align 8
  %%idslot = getelementptr %s, ptr %%w, i32 0, i32 0
  %%wid = load i64, ptr %%idslot, align 8
  %%same = icmp eq i64 %%wid, %%id
  br i1 %%same, label %%hit, label %%next
hit:
  ret ptr %%w
next:
  %%inext = add i64 %%iv, 1
  store i64 %%inext, ptr %%i, align 8
  br label %%loop
miss:
  ret ptr null
}`, clusterWorkerIR))

	// __kml_cluster_on_reap(cp) — the cp post-reap hook: drop the exited
	// worker from the registry (compact shift, order preserved), so
	// cluster.workers lists only live workers, like Node's delete.
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_cluster_on_reap(ptr %%cp) {
entry:
  %%i = alloca i64, align 8
  store i64 0, ptr %%i, align 8
  br label %%loop
loop:
  %%iv = load i64, ptr %%i, align 8
  %%len = load i64, ptr @__kml_cluster_pid_len, align 8
  %%inb = icmp slt i64 %%iv, %%len
  br i1 %%inb, label %%body, label %%done
body:
  %%wdata = load ptr, ptr @__kml_cluster_wrks, align 8
  %%wslot = getelementptr ptr, ptr %%wdata, i64 %%iv
  %%w = load ptr, ptr %%wslot, align 8
  %%cpslot = getelementptr %s, ptr %%w, i32 0, i32 2
  %%wcp = load ptr, ptr %%cpslot, align 8
  %%same = icmp eq ptr %%wcp, %%cp
  br i1 %%same, label %%remove, label %%next
next:
  %%inext = add i64 %%iv, 1
  store i64 %%inext, ptr %%i, align 8
  br label %%loop
remove:
  %%j = alloca i64, align 8
  store i64 %%iv, ptr %%j, align 8
  br label %%shift
shift:
  %%jv = load i64, ptr %%j, align 8
  %%len2 = load i64, ptr @__kml_cluster_pid_len, align 8
  %%last = sub i64 %%len2, 1
  %%more = icmp slt i64 %%jv, %%last
  br i1 %%more, label %%copy, label %%shrink
copy:
  %%jn = add i64 %%jv, 1
  %%wd = load ptr, ptr @__kml_cluster_wrks, align 8
  %%src_w = getelementptr ptr, ptr %%wd, i64 %%jn
  %%v_w = load ptr, ptr %%src_w, align 8
  %%dst_w = getelementptr ptr, ptr %%wd, i64 %%jv
  store ptr %%v_w, ptr %%dst_w, align 8
  %%pd = load ptr, ptr @__kml_cluster_pids, align 8
  %%src_p = getelementptr i64, ptr %%pd, i64 %%jn
  %%v_p = load i64, ptr %%src_p, align 8
  %%dst_p = getelementptr i64, ptr %%pd, i64 %%jv
  store i64 %%v_p, ptr %%dst_p, align 8
  store i64 %%jn, ptr %%j, align 8
  br label %%shift
shrink:
  store i64 %%last, ptr @__kml_cluster_pid_len, align 8
  br label %%done
done:
  ret void
}`, clusterWorkerIR))

	// __kml_cluster_on_disc(cp) — the cp channel-closed hook: fire the
	// cluster-level 'disconnect' relay with the Worker.
	e.emitGlobal(`
define void @__kml_cluster_on_disc(ptr %cp) {
entry:
  %fn = load ptr, ptr @__kml_cluster_relay_disc_fn, align 8
  %set = icmp ne ptr %fn, null
  br i1 %set, label %findw, label %ret
findw:
  %w = call ptr @__kml_cluster_worker_by_cp(ptr %cp)
  %hasw = icmp ne ptr %w, null
  br i1 %hasw, label %fire, label %ret
fire:
  %hdr = load ptr, ptr @__kml_cluster_relay_disc_hdr, align 8
  %pair = call ptr @malloc(i64 16)
  %ph = getelementptr { ptr, ptr }, ptr %pair, i32 0, i32 0
  store ptr %hdr, ptr %ph, align 8
  %pw = getelementptr { ptr, ptr }, ptr %pair, i32 0, i32 1
  store ptr %w, ptr %pw, align 8
  call void %fn(ptr %pair)
  br label %ret
ret:
  ret void
}`)

	// __kml_cluster_on_ctrl(cp, msg) — the cp control-line hook: a worker's
	// "__kml:listening:<port>" announcement fires the cluster-level
	// 'listening' relay with the Worker and the port.
	lstPrefix := e.internString("__kml:listening:")
	e.ensureStrncmp()
	e.ensureAtoll()
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_cluster_on_ctrl(ptr %%cp, ptr %%msg) {
entry:
  %%cmp = call i32 @strncmp(ptr %%msg, ptr %s, i64 16)
  %%islst = icmp eq i32 %%cmp, 0
  br i1 %%islst, label %%lst, label %%ret
lst:
  %%fn = load ptr, ptr @__kml_cluster_relay_listening_fn, align 8
  %%set = icmp ne ptr %%fn, null
  br i1 %%set, label %%findw, label %%ret
findw:
  %%w = call ptr @__kml_cluster_worker_by_cp(ptr %%cp)
  %%hasw = icmp ne ptr %%w, null
  br i1 %%hasw, label %%fire, label %%ret
fire:
  %%portp = getelementptr i8, ptr %%msg, i64 16
  %%port = call i64 @atoll(ptr %%portp)
  %%hdr = load ptr, ptr @__kml_cluster_relay_listening_hdr, align 8
  %%pair = call ptr @malloc(i64 16)
  %%ph = getelementptr { ptr, ptr }, ptr %%pair, i32 0, i32 0
  store ptr %%hdr, ptr %%ph, align 8
  %%pw = getelementptr { ptr, ptr }, ptr %%pair, i32 0, i32 1
  store ptr %%w, ptr %%pw, align 8
  call void %%fn(ptr %%pair, i64 %%port)
  br label %%ret
ret:
  ret void
}`, lstPrefix))

	// __kml_cluster_announce_listening(port) — worker side, armed into
	// @__kml_http_listening_announce by the startup seed: tell the primary
	// this worker's server bound.
	e.ensureListeningAnnounceHook()
	lstFmt := e.internString("__kml:listening:%lld")
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_cluster_announce_listening(i32 %%port) {
entry:
  %%fd = call i32 @__kml_ipcc_fd()
  %%has = icmp sgt i32 %%fd, 0
  br i1 %%has, label %%send, label %%ret
send:
  %%buf = call ptr @malloc(i64 32)
  %%port64 = sext i32 %%port to i64
  call i32 (ptr, ptr, ...) @sprintf(ptr %%buf, ptr %s, i64 %%port64)
  call i1 @__kml_ipcc_send(ptr %%buf)
  br label %%ret
ret:
  ret void
}`, lstFmt))

	// __kml_cluster_disconnect_all(): cluster.disconnect() — close every
	// worker's IPC channel (each worker's channel-read loop then winds down,
	// like Node's per-worker disconnect).
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_cluster_disconnect_all() {
entry:
  %%i = alloca i64, align 8
  store i64 0, ptr %%i, align 8
  br label %%loop
loop:
  %%iv = load i64, ptr %%i, align 8
  %%len = load i64, ptr @__kml_cluster_pid_len, align 8
  %%inb = icmp slt i64 %%iv, %%len
  br i1 %%inb, label %%body, label %%done
body:
  %%wdata = load ptr, ptr @__kml_cluster_wrks, align 8
  %%wslot = getelementptr ptr, ptr %%wdata, i64 %%iv
  %%w = load ptr, ptr %%wslot, align 8
  %%cpslot = getelementptr %s, ptr %%w, i32 0, i32 2
  %%cp = load ptr, ptr %%cpslot, align 8
  call void @__kml_cp_disconnect(ptr %%cp)
  %%inext = add i64 %%iv, 1
  store i64 %%inext, ptr %%i, align 8
  br label %%loop
done:
  ret void
}`, clusterWorkerIR))

	// __kml_cluster_fork(): fork; the child re-execs the executable as a worker
	// (KML_CLUSTER_WORKER_ID set), the parent registers the child and returns a
	// Worker { id, pid }.
	e.emitGlobal(fmt.Sprintf(`
define ptr @__kml_cluster_fork() {
entry:
  %%id = load i64, ptr @__kml_cluster_next_id, align 8
  call i32 @fflush(ptr null)
  ; the worker's IPC channel (TDD-00141): NODE_CHANNEL_FD, like fork()
  %%sv = alloca [2 x i32], align 4
  %%svp = getelementptr [2 x i32], ptr %%sv, i32 0, i32 0
  call i32 @socketpair(i32 1, i32 1, i32 0, ptr %%svp)
  %%p0_p = getelementptr [2 x i32], ptr %%sv, i32 0, i32 0
  %%p1_p = getelementptr [2 x i32], ptr %%sv, i32 0, i32 1
  %%pfd = load i32, ptr %%p0_p, align 4
  %%cfd = load i32, ptr %%p1_p, align 4
%s%sparent:
  call i32 @close(i32 %%cfd)
  %%pid64 = sext i32 %%pid to i64
  %%next = add i64 %%id, 1
  store i64 %%next, ptr @__kml_cluster_next_id, align 8
  %%cp = call ptr @__kml_cp_wrap_ipc(i64 %%pid64, i32 %%pfd)
%s  %%w = call ptr @malloc(i64 24)
  %%wid = getelementptr %s, ptr %%w, i32 0, i32 0
  store i64 %%id, ptr %%wid, align 8
  %%wpid = getelementptr %s, ptr %%w, i32 0, i32 1
  store i64 %%pid64, ptr %%wpid, align 8
  %%wcp = getelementptr %s, ptr %%w, i32 0, i32 2
  store ptr %%cp, ptr %%wcp, align 8
  call void @__kml_cluster_register(i64 %%pid64, ptr %%w)
  ; arm the cluster-level relays registered before this fork
  %%xfn = load ptr, ptr @__kml_cluster_relay_exit_fn, align 8
  %%xset = icmp ne ptr %%xfn, null
  br i1 %%xset, label %%armx, label %%aftx
armx:
  %%xhdr = load ptr, ptr @__kml_cluster_relay_exit_hdr, align 8
  call void @__kml_cluster_attach_exit(ptr %%w, ptr %%xfn, ptr %%xhdr)
  br label %%aftx
aftx:
  %%mfn = load ptr, ptr @__kml_cluster_relay_msg_fn, align 8
  %%mset = icmp ne ptr %%mfn, null
  br i1 %%mset, label %%armm, label %%aftm
armm:
  %%mhdr = load ptr, ptr @__kml_cluster_relay_msg_hdr, align 8
  call void @__kml_cluster_attach_msg(ptr %%w, ptr %%mfn, ptr %%mhdr)
  br label %%aftm
aftm:
  ; cluster.on('fork') fires synchronously for a fork after registration
  %%ffn = load ptr, ptr @__kml_cluster_relay_fork_fn, align 8
  %%fset = icmp ne ptr %%ffn, null
  br i1 %%fset, label %%firef, label %%aftf
firef:
  %%fhdr = load ptr, ptr @__kml_cluster_relay_fork_hdr, align 8
  call void %%ffn(ptr %%fhdr, ptr %%w)
  br label %%aftf
aftf:
  ; cluster.on('online') fires from a queued microtask (the worker is exec'd
  ; by then) — same convention as worker.on('online')
  %%ofn = load ptr, ptr @__kml_cluster_relay_online_fn, align 8
  %%oset = icmp ne ptr %%ofn, null
  br i1 %%oset, label %%fireo, label %%afto
fireo:
  %%ohdr = load ptr, ptr @__kml_cluster_relay_online_hdr, align 8
  %%opair = call ptr @malloc(i64 16)
  %%oph = getelementptr { ptr, ptr }, ptr %%opair, i32 0, i32 0
  store ptr %%ohdr, ptr %%oph, align 8
  %%opw = getelementptr { ptr, ptr }, ptr %%opair, i32 0, i32 1
  store ptr %%w, ptr %%opw, align 8
  %%omt = call ptr @malloc(i64 16)
  %%omf = getelementptr { ptr, ptr }, ptr %%omt, i32 0, i32 0
  store ptr %%ofn, ptr %%omf, align 8
  %%ome = getelementptr { ptr, ptr }, ptr %%omt, i32 0, i32 1
  store ptr %%opair, ptr %%ome, align 8
  call void @__kml_microtask_enqueue(ptr %%omt)
  br label %%afto
afto:
  ret ptr %%w
}`, clusterSilentEntryIR(), e.clusterForkIR(idFmt, envName, idFmt, chanEnvName), clusterSilentParentIR(), clusterWorkerIR, clusterWorkerIR, clusterWorkerIR))

	// __kml_cluster_wait_all(): the primary blocks until every forked worker
	// exits (keeping the primary alive while workers serve, like Node). A
	// worker process has an empty table, so this is a no-op there.
	e.emitGlobal(`
define void @__kml_cluster_wait_all() {
entry:
  %st = alloca i32, align 4
  %i = alloca i64, align 8
  store i64 0, ptr %i, align 8
  br label %loop
loop:
  %iv = load i64, ptr %i, align 8
  %len = load i64, ptr @__kml_cluster_pid_len, align 8
  %inb = icmp slt i64 %iv, %len
  br i1 %inb, label %body, label %done
body:
  %data = load ptr, ptr @__kml_cluster_pids, align 8
  %slot = getelementptr i64, ptr %data, i64 %iv
  %pid64 = load i64, ptr %slot, align 8
  %pid32 = trunc i64 %pid64 to i32
  call i32 @waitpid(i32 %pid32, ptr %st, i32 0)
  %inext = add i64 %iv, 1
  store i64 %inext, ptr %i, align 8
  br label %loop
done:
  ret void
}`)
}

// clusterSilentEntryIR is the entry-block region of __kml_cluster_fork that
// prepares the setupPrimary({ silent: true }) stdio pipes BEFORE the fork —
// the parent needs the read ends, the child the write ends. POSIX only; the
// Windows spawn path lands with the platform lane (TDD-00177).
func clusterSilentEntryIR() string {
	if targetGOOS() == "windows" {
		return ""
	}
	return `  %silent = load i64, ptr @__kml_cluster_setup_silent, align 8
  %dosilent = icmp ne i64 %silent, 0
  %outp = alloca [2 x i32], align 4
  %errp = alloca [2 x i32], align 4
  %op0 = getelementptr [2 x i32], ptr %outp, i32 0, i32 0
  %op1 = getelementptr [2 x i32], ptr %outp, i32 0, i32 1
  %ep0 = getelementptr [2 x i32], ptr %errp, i32 0, i32 0
  %ep1 = getelementptr [2 x i32], ptr %errp, i32 0, i32 1
  store i32 -1, ptr %op0, align 4
  store i32 -1, ptr %op1, align 4
  store i32 -1, ptr %ep0, align 4
  store i32 -1, ptr %ep1, align 4
  br i1 %dosilent, label %mkpipes, label %pipesdone
mkpipes:
  call i32 @pipe(ptr %outp)
  call i32 @pipe(ptr %errp)
  br label %pipesdone
pipesdone:
  %out_r = load i32, ptr %op0, align 4
  %out_w = load i32, ptr %op1, align 4
  %err_r = load i32, ptr %ep0, align 4
  %err_w = load i32, ptr %ep1, align 4
`
}

// clusterSilentParentIR is the post-wrap parent region: hand the pipes' read
// ends (non-blocking) to the Worker's ChildProcess handle — the event loop
// then streams them like a spawned child's stdio ('data' listeners on
// worker.process.stdout/stderr) — and drop the write ends.
func clusterSilentParentIR() string {
	if targetGOOS() == "windows" {
		return ""
	}
	nonblock := httpNonblockFlag()
	return fmt.Sprintf(`  br i1 %%dosilent, label %%pwire, label %%pnowire
pwire:
  call i32 @close(i32 %%out_w)
  call i32 @close(i32 %%err_w)
  %%ofl = call i32 (i32, i32, ...) @fcntl(i32 %%out_r, i32 3)
  %%ofln = or i32 %%ofl, %d
  call i32 (i32, i32, ...) @fcntl(i32 %%out_r, i32 4, i32 %%ofln)
  %%efl = call i32 (i32, i32, ...) @fcntl(i32 %%err_r, i32 3)
  %%efln = or i32 %%efl, %d
  call i32 (i32, i32, ...) @fcntl(i32 %%err_r, i32 4, i32 %%efln)
  %%csout_p = getelementptr %s, ptr %%cp, i32 0, i32 2
  store i32 %%out_r, ptr %%csout_p, align 4
  %%cserr_p = getelementptr %s, ptr %%cp, i32 0, i32 3
  store i32 %%err_r, ptr %%cserr_p, align 4
  br label %%pnowire
pnowire:
`, nonblock, nonblock, cpStructIR, cpStructIR)
}
