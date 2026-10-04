package llvm

import (
	"fmt"
)

// Windows process model (TDD-00177 Stage 4). The three fork+exec sites in
// the runtime (child_process spawn, execFileSync, cluster.fork) keep their
// pipe/socketpair plumbing on every host; only the fork→child→exec region
// differs. On Windows that region becomes one call into win32proc.c's
// __kml_win_spawn (CreateProcessW with the pipe ends as the child's stdio),
// which returns the pid the parent path already expects. The helpers below
// return the region's IR text with single '%' (they are inserted through a
// %s verb or concatenation, never re-formatted).

// ensureWinSpawnDecl declares __kml_win_spawn once (Windows only).
func (e *Emitter) ensureWinSpawnDecl() {
	if e.opts.Target.OS() != "windows" || e.usedWinSpawn {
		return
	}
	e.usedWinSpawn = true
	e.emitGlobal("declare i32 @__kml_win_spawn(ptr, ptr, ptr, i32, i32, i32, i32, i32, ptr)")
}

// The http.listen({ workers: N }) cluster (TDD-00025) forks N-1 workers that
// share the already-bound listening socket. On Windows the primary instead
// re-spawns its own executable N-1 times with every listening socket it
// holds inherited (win32proc.c re-homes each at the same fd number before
// the worker's main runs) and the worker id in the environment; a worker's
// __kml_http_bind_and_listen then takes the inherited listener bound to its
// port instead of binding, and its own __kml_http_cluster_fork is a no-op.
// Sharing accept() on the one socket does not distribute on Windows (the
// kernel serves pending accepts most-recent-first, so one process takes
// everything), so connections are dealt round-robin instead, the way Node's
// cluster does there: the primary accepts and hands each connection to the
// next process in turn, itself included (win32io.c, "cluster round-robin").
// The mmap'd close flag has no Windows equivalent yet; it stays null, so
// http.close() in a worker does not reach its siblings there.

// httpClusterEntryIR is inserted at the top of __kml_http_cluster_fork.
func (e *Emitter) httpClusterEntryIR() string {
	if e.opts.Target.OS() != "windows" {
		return ""
	}
	return `  %wid0 = load i64, ptr @__kml_cluster_worker_id, align 8
  %isworker0 = icmp ne i64 %wid0, 0
  br i1 %isworker0, label %wflag, label %primary
wflag:
  ; A re-spawned worker maps the primary's close-flag page (fork would have
  ; inherited the mapping).
  %wmm = call ptr @__kml_win_cluster_flag()
  %wmmfail = icmp eq ptr %wmm, inttoptr (i64 -1 to ptr)
  br i1 %wmmfail, label %done, label %wstore
wstore:
  store ptr %wmm, ptr @__kml_cluster_close_flag, align 8
  br label %done
primary:
`
}

// httpClusterFlagIR allocates the shared close-flag page as %mm (MAP_FAILED on
// failure): an anonymous MAP_SHARED mapping the forked workers inherit, or on
// Windows a named section the re-spawned workers open (win32proc.c).
func (e *Emitter) httpClusterFlagIR() string {
	if e.opts.Target.OS() != "windows" {
		return fmt.Sprintf("  %%mm = call ptr @mmap(ptr null, i64 16, i32 3, i32 %d, i32 -1, i64 0)\n", e.mmapSharedAnonFlags())
	}
	return "  %mm = call ptr @__kml_win_cluster_flag()\n"
}

// httpClusterForkIR replaces the per-worker fork block (doforkw: … up to
// parentnext:) of __kml_http_cluster_fork.
func (e *Emitter) httpClusterForkIR() string {
	if e.opts.Target.OS() != "windows" {
		return `  ; fflush(NULL) before fork() is required, not optional: fork() copies
  ; libc's stdio buffers verbatim, so any console.log output still sitting
  ; unflushed in stdout's buffer (the common case once stdout isn't a TTY)
  ; would otherwise be flushed once per worker.
  call i32 @fflush(ptr null)
  ; The primary's pid, read before fork(): a getppid() in the child could
  ; already name the reaper if the primary died in between.
  %primarypid = call i32 @getpid()
  %pid = call i32 @fork()
  %ischild = icmp eq i32 %pid, 0
  br i1 %ischild, label %child, label %parentnext
child:
  store i64 %i, ptr @__kml_cluster_worker_id, align 8
  store i32 %primarypid, ptr @__kml_cluster_primary_pid, align 4
  br label %done
`
	}
	e.ensureWinSpawnDecl()
	e.ensureMalloc()
	e.ensureSprintf()
	e.ensureSetenvDecl()
	e.ensureUnsetenv()
	idFmt := e.internString("%lld")
	envID := e.internString("KML_CLUSTER_WORKER_ID")
	// Spawn flag 131072 (0x20000) = cluster worker: the platform layer hands it
	// every listening socket this process holds plus a hand-off channel for each
	// (win32proc.c); %lfd, the fork model's one shared listener, is not needed.
	return `  call i32 @fflush(ptr null)
  %idbuf = call ptr @malloc(i64 24)
  call i32 (ptr, ptr, ...) @sprintf(ptr %idbuf, ptr ` + idFmt + `, i64 %i)
  call i32 @setenv(ptr ` + envID + `, ptr %idbuf, i32 1)
  %exe = call ptr @__kml_win_self_exe()
  %argv = load ptr, ptr @__argv_ptr, align 8
  %pid = call i32 @__kml_win_spawn(ptr %exe, ptr %argv, ptr null, i32 -1, i32 -1, i32 -1, i32 -1, i32 131072, ptr null)
  call i32 @unsetenv(ptr ` + envID + `)
  br label %parentnext
`
}

// httpClusterOrphanIR opens the event loop's per-iteration cluster poll (block
// ccpoll, falling through to ccpoll1): a worker does not outlive its primary.
// Node's cluster worker exits when its channel to the primary disconnects
// (process.exit(0) on 'disconnect'); a worker left behind would keep the
// listening port open. A forked worker whose parent is no longer the primary
// has been re-parented, i.e. the primary is gone. On Windows the platform layer
// waits on the primary's process handle instead (win32proc.c), so nothing is
// polled here.
func (e *Emitter) httpClusterOrphanIR() string {
	if e.opts.Target.OS() == "windows" {
		return "  br label %ccpoll1"
	}
	e.ensureExit()
	e.declareFn("getppid", "declare i32 @getppid()")
	return `  %ccprimary = load i32, ptr @__kml_cluster_primary_pid, align 4
  %ccisworker = icmp ne i32 %ccprimary, 0
  br i1 %ccisworker, label %ccparent, label %ccpoll1
ccparent:
  %ccppid = call i32 @getppid()
  %ccorphan = icmp ne i32 %ccppid, %ccprimary
  br i1 %ccorphan, label %ccorphaned, label %ccpoll1
ccorphaned:
  call void @exit(i32 0)
  unreachable`
}

// httpListenInheritIR is inserted at the top of __kml_http_bind_and_listen:
// a spawned worker returns the inherited listening fd instead of binding.
func (e *Emitter) httpListenInheritIR() string {
	if e.opts.Target.OS() != "windows" {
		return ""
	}
	if !e.usedWinInheritedListener {
		e.usedWinInheritedListener = true
		e.emitGlobal("declare i32 @__kml_win_inherited_listener(i32 noundef)")
	}
	return `  %inhfd = call i32 @__kml_win_inherited_listener(i32 %port)
  %noinh = icmp slt i32 %inhfd, 0
  br i1 %noinh, label %fresh, label %inherited
inherited:
  ret i32 %inhfd
fresh:
`
}

// ensureHTTPClusterSeed defines __kml_http_cluster_seed (Windows only): a
// re-spawned worker reads its id from KML_CLUSTER_WORKER_ID at startup, the
// value the fork model would have inherited in memory.
func (e *Emitter) ensureHTTPClusterSeed() {
	if e.opts.Target.OS() != "windows" || e.usedHTTPClusterSeed {
		return
	}
	e.usedHTTPClusterSeed = true
	e.ensureGetenv()
	e.ensureAtoll()
	e.emitGlobal("declare ptr @__kml_win_self_exe()")
	e.emitGlobal("declare ptr @__kml_win_cluster_flag()")
	e.emitGlobal(`
define void @__kml_http_cluster_seed() {
entry:
  %v = call ptr @getenv(ptr ` + e.internString("KML_CLUSTER_WORKER_ID") + `)
  %isnull = icmp eq ptr %v, null
  br i1 %isnull, label %done, label %seed
seed:
  %id = call i64 @atoll(ptr %v)
  store i64 %id, ptr @__kml_cluster_worker_id, align 8
  br label %done
done:
  ret void
}`)
}

// ensureListenFdGlobal declares @__kml_listen_fd once. The HTTP runtime owns
// it; on Windows the cluster module's re-spawn path also reads it (a worker
// inherits the listening socket under that number), so both paths share
// this guard instead of the HTTP runtime's unguarded emit.
func (e *Emitter) ensureListenFdGlobal() {
	if e.usedListenFdGlobal {
		return
	}
	e.usedListenFdGlobal = true
	e.emitGlobal("@__kml_listen_fd = internal thread_local global i32 -1, align 4")
}
