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
	if targetGOOS() != "windows" || e.usedWinSpawn {
		return
	}
	e.usedWinSpawn = true
	e.emitGlobal("declare i32 @__kml_win_spawn(ptr, ptr, ptr, i32, i32, i32, i32, i32, ptr)")
}

// cpSpawnForkIR is the region of __kml_cp_spawn between the pipe setup and
// the `parent:` label.
func (e *Emitter) cpSpawnForkIR() string {
	if targetGOOS() == "windows" {
		e.ensureWinSpawnDecl()
		return `  %kmlverb64 = lshr i64 %mode, 1
  %kmlverb = trunc i64 %kmlverb64 to i32
  ; Per-fd stdio (mode bits 4-9, ADR-00766): the pipe end for pipe (0), -1 for
  ; inherit (1, the shim uses our std handle), -2 for ignore (2, the shim opens
  ; NUL). __kml_win_spawn's inheritable_std reads the sentinels.
  %wsm_in0 = lshr i64 %mode, 4
  %wsm_in = and i64 %wsm_in0, 3
  %wsm_out0 = lshr i64 %mode, 6
  %wsm_out = and i64 %wsm_out0, 3
  %wsm_err0 = lshr i64 %mode, 8
  %wsm_err = and i64 %wsm_err0, 3
  %win_ign = icmp eq i64 %wsm_in, 2
  %win_i0 = select i1 %win_ign, i32 -2, i32 -1
  %win_pipe = icmp eq i64 %wsm_in, 0
  %win_in = select i1 %win_pipe, i32 %inr, i32 %win_i0
  %wout_ign = icmp eq i64 %wsm_out, 2
  %wout_o0 = select i1 %wout_ign, i32 -2, i32 -1
  %wout_pipe = icmp eq i64 %wsm_out, 0
  %win_out = select i1 %wout_pipe, i32 %outw, i32 %wout_o0
  %werr_ign = icmp eq i64 %wsm_err, 2
  %werr_e0 = select i1 %werr_ign, i32 -2, i32 -1
  %werr_pipe = icmp eq i64 %wsm_err, 0
  %win_err = select i1 %werr_pipe, i32 %errw, i32 %werr_e0
  %pid = call i32 @__kml_win_spawn(ptr %file, ptr %argv, ptr %cwd, i32 %win_in, i32 %win_out, i32 %win_err, i32 -1, i32 %kmlverb, ptr %env)
  br label %parent
`
	}
	return `  %pid = call i32 @fork()
  %ischild = icmp eq i32 %pid, 0
  br i1 %ischild, label %child, label %parent
child:
  ; detached (mode bit 3): a new session/group leader so the child can outlive
  ; the parent and isn't in its process group (ADR-00765).
  %isdetached = and i64 %mode, 8
  %dodetach = icmp ne i64 %isdetached, 0
  br i1 %dodetach, label %dosetsid, label %afterdetach
dosetsid:
  call i32 @setsid()
  br label %afterdetach
afterdetach:
  %hascwd = icmp ne ptr %cwd, null
  br i1 %hascwd, label %dochdir, label %setupfds
dochdir:
  call i32 @chdir(ptr %cwd)
  br label %setupfds
setupfds:
  ; Per-fd stdio (mode bits 4-9, ADR-00766): pipe (0) dup2's the pipe end;
  ; inherit (1) leaves the inherited parent fd; ignore (2) dup2's /dev/null.
  ; /dev/null (RDWR) is opened once when any fd is ignore, closed before exec.
  %sm_in0 = lshr i64 %mode, 4
  %sm_in = and i64 %sm_in0, 3
  %sm_out0 = lshr i64 %mode, 6
  %sm_out = and i64 %sm_out0, 3
  %sm_err0 = lshr i64 %mode, 8
  %sm_err = and i64 %sm_err0, 3
  %ign_in = icmp eq i64 %sm_in, 2
  %ign_out = icmp eq i64 %sm_out, 2
  %ign_err = icmp eq i64 %sm_err, 2
  %ign_a = or i1 %ign_in, %ign_out
  %ign_any = or i1 %ign_a, %ign_err
  br i1 %ign_any, label %opendn, label %afterdn
opendn:
  %dn0 = call i32 (ptr, i32, ...) @open(ptr @.kml_cp_devnull, i32 2)
  br label %afterdn
afterdn:
  %dn = phi i32 [ %dn0, %opendn ], [ -1, %setupfds ]
  %si0 = select i1 %ign_in, i32 %dn, i32 -1
  %pipe_in = icmp eq i64 %sm_in, 0
  %si = select i1 %pipe_in, i32 %inr, i32 %si0
  %doi = icmp sge i32 %si, 0
  br i1 %doi, label %dupi, label %afti
dupi:
  call i32 @dup2(i32 %si, i32 0)
  br label %afti
afti:
  %so0 = select i1 %ign_out, i32 %dn, i32 -1
  %pipe_out = icmp eq i64 %sm_out, 0
  %so = select i1 %pipe_out, i32 %outw, i32 %so0
  %doo = icmp sge i32 %so, 0
  br i1 %doo, label %dupo, label %afto
dupo:
  call i32 @dup2(i32 %so, i32 1)
  br label %afto
afto:
  %se0 = select i1 %ign_err, i32 %dn, i32 -1
  %pipe_err = icmp eq i64 %sm_err, 0
  %se = select i1 %pipe_err, i32 %errw, i32 %se0
  %doe = icmp sge i32 %se, 0
  br i1 %doe, label %dupe, label %afte
dupe:
  call i32 @dup2(i32 %se, i32 2)
  br label %afte
afte:
  %hasdn = icmp sge i32 %dn, 0
  br i1 %hasdn, label %closedn, label %afterclosedn
closedn:
  call i32 @close(i32 %dn)
  br label %afterclosedn
afterclosedn:
  call i32 @close(i32 %inr)
  call i32 @close(i32 %inw)
  call i32 @close(i32 %outr)
  call i32 @close(i32 %outw)
  call i32 @close(i32 %errr)
  call i32 @close(i32 %errw)
  ; A custom env fully replaces the child's: point the global environ at it
  ; before exec so execvp inherits it (ADR-00762). No env → keep ours.
  %hasenv = icmp ne ptr %env, null
  br i1 %hasenv, label %setenv, label %doexec
setenv:
  store ptr %env, ptr @environ, align 8
  br label %doexec
doexec:
  call i32 @execvp(ptr %file, ptr %argv)
  ; execvp only returns on failure — report errno up the CLOEXEC status pipe
  ; (%spw, from cpSpawnStatusSetupIR) so the parent can fire 'error' (ADR-00754).
  %spawnerrslot = alloca i32, align 4
  %spawnerrptr = call ptr @` + errnoAccessor() + `()
  %spawnerrv = load i32, ptr %spawnerrptr, align 4
  store i32 %spawnerrv, ptr %spawnerrslot, align 4
  call i64 @write(i32 %spw, ptr %spawnerrslot, i64 4)
  call void @_exit(i32 127)
  unreachable
`
}

// cpSpawnStatusSetupIR sets up the spawn-failure signal used by __kml_cp_spawn.
// POSIX: a pipe whose write end is close-on-exec, so a successful execvp
// closes it (parent reads EOF) and a failed one leaves the child's errno in
// it (ADR-00754). Windows has no fork/exec, so nothing is set up here — the
// detection reads __kml_win_spawn's failure flag instead.
func (e *Emitter) cpSpawnStatusSetupIR() string {
	if targetGOOS() == "windows" {
		return ""
	}
	// F_SETFD = 2, FD_CLOEXEC = 1.
	return `  %statuspipe = alloca [2 x i32], align 4
  call i32 @pipe(ptr %statuspipe)
  %spr_p = getelementptr [2 x i32], ptr %statuspipe, i32 0, i32 0
  %spw_p = getelementptr [2 x i32], ptr %statuspipe, i32 0, i32 1
  %spr = load i32, ptr %spr_p, align 4
  %spw = load i32, ptr %spw_p, align 4
  call i32 (i32, i32, ...) @fcntl(i32 %spw, i32 2, i32 1)
  call i32 (i32, i32, ...) @fcntl(i32 %spr, i32 2, i32 1)
`
}

// cpSpawnFailDetectIR computes the spawn-failure errno and stores it into the
// ChildProcess handle's field 20. POSIX reads the CLOEXEC status pipe (a
// short read of 4 bytes = the child's errno; EOF = success). Windows asks
// __kml_win_spawn_failed, which returns the errno a failed CreateProcessW
// mapped to (0 on success). cp is the struct-type string.
func (e *Emitter) cpSpawnFailDetectIR(cp string) string {
	if targetGOOS() == "windows" {
		return `  %sf_e = call i32 @__kml_win_spawn_failed(i32 %pid)
  %sf_e64 = sext i32 %sf_e to i64
  %sf20_p = getelementptr ` + cp + `, ptr %cp, i32 0, i32 20
  store i64 %sf_e64, ptr %sf20_p, align 8
`
	}
	return `  call i32 @close(i32 %spw)
  %sf_slot = alloca i32, align 4
  store i32 0, ptr %sf_slot, align 4
  %sf_n = call i64 @read(i32 %spr, ptr %sf_slot, i64 4)
  call i32 @close(i32 %spr)
  %sf_got = icmp sgt i64 %sf_n, 0
  %sf_err = load i32, ptr %sf_slot, align 4
  %sf_e32 = select i1 %sf_got, i32 %sf_err, i32 0
  %sf_e64 = sext i32 %sf_e32 to i64
  %sf20_p = getelementptr ` + cp + `, ptr %cp, i32 0, i32 20
  store i64 %sf_e64, ptr %sf20_p, align 8
`
}

// execSyncForkIR is the same region of __kml_exec_file_sync (one pipe, the
// child's stdout).
func (e *Emitter) execSyncForkIR() string {
	if targetGOOS() == "windows" {
		e.ensureWinSpawnDecl()
		return `  %pid = call i32 @__kml_win_spawn(ptr %file, ptr %argv, ptr %cwd, i32 -1, i32 %writefd, i32 -1, i32 -1, i32 0, ptr null)
  br label %parent
`
	}
	return `  %pid = call i32 @fork()
  %ischild = icmp eq i32 %pid, 0
  br i1 %ischild, label %child, label %parent

child:
  call i32 @close(i32 %readfd)
  call i32 @dup2(i32 %writefd, i32 1)
  call i32 @close(i32 %writefd)
  %hascwd = icmp ne ptr %cwd, null
  br i1 %hascwd, label %dochdir, label %doexec
dochdir:
  call i32 @chdir(ptr %cwd)
  br label %doexec
doexec:
  call i32 @execvp(ptr %file, ptr %argv)
  call void @_exit(i32 127)
  unreachable
`
}

// clusterForkIR is the region of __kml_cluster_fork. On Windows the primary
// sets the worker's environment itself, spawns its own executable with the
// child end of the IPC socket pair inherited (win32proc.c re-homes it at
// the advertised fd number before the worker's main runs), then clears the
// two variables so nothing else it spawns inherits them. fmtID/envID/
// fmtFD/envFD are the interned-constant references the template already
// holds for the "%d" formats and the two variable names.
func (e *Emitter) clusterForkIR(fmtID, envID, fmtFD, envFD string) string {
	if targetGOOS() == "windows" {
		e.ensureWinSpawnDecl()
		return `  %idbuf = call ptr @malloc(i64 24)
  call i32 (ptr, ptr, ...) @sprintf(ptr %idbuf, ptr ` + fmtID + `, i64 %id)
  call i32 @setenv(ptr ` + envID + `, ptr %idbuf, i32 1)
  %fdbuf = call ptr @malloc(i64 24)
  %cfd64 = sext i32 %cfd to i64
  call i32 (ptr, ptr, ...) @sprintf(ptr %fdbuf, ptr ` + fmtFD + `, i64 %cfd64)
  call i32 @setenv(ptr ` + envFD + `, ptr %fdbuf, i32 1)
  %exe = call ptr @__kml_cluster_self_exe()
  %argv = load ptr, ptr @__argv_ptr, align 8
  %pid = call i32 @__kml_win_spawn(ptr %exe, ptr %argv, ptr null, i32 -1, i32 -1, i32 -1, i32 %cfd, i32 0, ptr null)
  call i32 @unsetenv(ptr ` + envID + `)
  call i32 @unsetenv(ptr ` + envFD + `)
  br label %parent
`
	}
	return `  %pid = call i32 @fork()
  %ischild = icmp eq i32 %pid, 0
  br i1 %ischild, label %child, label %parent
child:
  call i32 @close(i32 %pfd)
  %idbuf = call ptr @malloc(i64 24)
  call i32 (ptr, ptr, ...) @sprintf(ptr %idbuf, ptr ` + fmtID + `, i64 %id)
  call i32 @setenv(ptr ` + envID + `, ptr %idbuf, i32 1)
  %fdbuf = call ptr @malloc(i64 24)
  %cfd64 = sext i32 %cfd to i64
  call i32 (ptr, ptr, ...) @sprintf(ptr %fdbuf, ptr ` + fmtFD + `, i64 %cfd64)
  call i32 @setenv(ptr ` + envFD + `, ptr %fdbuf, i32 1)
  %exe = call ptr @__kml_cluster_self_exe()
  %argv = load ptr, ptr @__argv_ptr, align 8
  call i32 @execv(ptr %exe, ptr %argv)
  call void @_exit(i32 127)
  unreachable
`
}

// cpForkIR is the region of __kml_cp_fork (child_process.fork): like
// clusterForkIR but with a single channel-fd variable and argv0 as the
// program.
func (e *Emitter) cpForkIR(fmtFD, envFD string) string {
	if targetGOOS() == "windows" {
		e.ensureWinSpawnDecl()
		e.ensureUnsetenv()
		return `  %numbuf = alloca [16 x i8], align 1
  %numptr = getelementptr [16 x i8], ptr %numbuf, i32 0, i32 0
  %cfd64 = sext i32 %cfd to i64
  call i32 (ptr, ptr, ...) @sprintf(ptr %numptr, ptr ` + fmtFD + `, i64 %cfd64)
  call i32 @setenv(ptr ` + envFD + `, ptr %numptr, i32 1)
  %pid = call i32 @__kml_win_spawn(ptr %argv0, ptr %argv, ptr null, i32 -1, i32 -1, i32 -1, i32 %cfd, i32 0, ptr null)
  call i32 @unsetenv(ptr ` + envFD + `)
  br label %parent
`
	}
	return `  %pid = call i32 @fork()
  %ischild = icmp eq i32 %pid, 0
  br i1 %ischild, label %child, label %parent
child:
  call i32 @close(i32 %pfd)
  %numbuf = alloca [16 x i8], align 1
  %numptr = getelementptr [16 x i8], ptr %numbuf, i32 0, i32 0
  %cfd64 = sext i32 %cfd to i64
  call i32 (ptr, ptr, ...) @sprintf(ptr %numptr, ptr ` + fmtFD + `, i64 %cfd64)
  call i32 @setenv(ptr ` + envFD + `, ptr %numptr, i32 1)
  call i32 @execv(ptr %argv0, ptr %argv)
  call void @_exit(i32 127)
  unreachable
`
}

// The http.listen({ workers: N }) cluster (TDD-00025) forks N-1 workers that
// share the already-bound listening socket. On Windows the primary instead
// re-spawns its own executable N-1 times with the listening socket
// inherited (win32proc.c re-homes it at the same fd number before the
// worker's main runs) and the worker id in the environment; a worker's
// __kml_http_bind_and_listen then adopts that fd instead of binding, and
// its own __kml_http_cluster_fork is a no-op. Every worker accepts on the
// one shared socket, which is the same SCHED_NONE distribution the fork
// model has on Linux — Node's Windows round-robin (primary accepts and hands
// connections over) is recorded in TDD-00177 as the remaining gap. The
// mmap'd close flag has no Windows equivalent yet; it stays null, so
// http.close() in a worker does not reach its siblings there.

// httpClusterEntryIR is inserted at the top of __kml_http_cluster_fork.
func (e *Emitter) httpClusterEntryIR() string {
	if targetGOOS() != "windows" {
		return ""
	}
	return `  %wid0 = load i64, ptr @__kml_cluster_worker_id, align 8
  %isworker0 = icmp ne i64 %wid0, 0
  br i1 %isworker0, label %done, label %primary
primary:
`
}

// httpClusterForkIR replaces the per-worker fork block (doforkw: … up to
// parentnext:) of __kml_http_cluster_fork.
func (e *Emitter) httpClusterForkIR() string {
	if targetGOOS() != "windows" {
		return `  ; fflush(NULL) before fork() is required, not optional: fork() copies
  ; libc's stdio buffers verbatim, so any console.log output still sitting
  ; unflushed in stdout's buffer (the common case once stdout isn't a TTY)
  ; would otherwise be flushed once per worker.
  call i32 @fflush(ptr null)
  %pid = call i32 @fork()
  %ischild = icmp eq i32 %pid, 0
  br i1 %ischild, label %child, label %parentnext
child:
  store i64 %i, ptr @__kml_cluster_worker_id, align 8
  br label %done
`
	}
	e.ensureListenFdGlobal()
	e.ensureWinSpawnDecl()
	e.ensureMalloc()
	e.ensureSprintf()
	e.ensureSetenvDecl()
	e.ensureUnsetenv()
	idFmt := e.internString("%lld")
	envID := e.internString("KML_CLUSTER_WORKER_ID")
	envFD := e.internString("KML_HTTP_LISTEN_FD")
	return `  call i32 @fflush(ptr null)
  %lfd = load i32, ptr @__kml_listen_fd, align 4
  %lfd64 = sext i32 %lfd to i64
  %idbuf = call ptr @malloc(i64 24)
  call i32 (ptr, ptr, ...) @sprintf(ptr %idbuf, ptr ` + idFmt + `, i64 %i)
  call i32 @setenv(ptr ` + envID + `, ptr %idbuf, i32 1)
  %fdbuf = call ptr @malloc(i64 24)
  call i32 (ptr, ptr, ...) @sprintf(ptr %fdbuf, ptr ` + idFmt + `, i64 %lfd64)
  call i32 @setenv(ptr ` + envFD + `, ptr %fdbuf, i32 1)
  %exe = call ptr @__kml_win_self_exe()
  %argv = load ptr, ptr @__argv_ptr, align 8
  %pid = call i32 @__kml_win_spawn(ptr %exe, ptr %argv, ptr null, i32 -1, i32 -1, i32 -1, i32 %lfd, i32 0, ptr null)
  call i32 @unsetenv(ptr ` + envID + `)
  call i32 @unsetenv(ptr ` + envFD + `)
  br label %parentnext
`
}

// httpListenInheritIR is inserted at the top of __kml_http_bind_and_listen:
// a spawned worker returns the inherited listening fd instead of binding.
func (e *Emitter) httpListenInheritIR() string {
	if targetGOOS() != "windows" {
		return ""
	}
	e.ensureGetenv()
	e.ensureAtoll()
	envFD := e.internString("KML_HTTP_LISTEN_FD")
	return `  %inh = call ptr @getenv(ptr ` + envFD + `)
  %noinh = icmp eq ptr %inh, null
  br i1 %noinh, label %fresh, label %inherited
inherited:
  %inh64 = call i64 @atoll(ptr %inh)
  %inhfd = trunc i64 %inh64 to i32
  ret i32 %inhfd
fresh:
`
}

// ensureHTTPClusterSeed defines __kml_http_cluster_seed (Windows only): a
// re-spawned worker reads its id from KML_CLUSTER_WORKER_ID at startup, the
// value the fork model would have inherited in memory.
func (e *Emitter) ensureHTTPClusterSeed() {
	if targetGOOS() != "windows" || e.usedHTTPClusterSeed {
		return
	}
	e.usedHTTPClusterSeed = true
	e.ensureGetenv()
	e.ensureAtoll()
	e.emitGlobal("declare ptr @__kml_win_self_exe()")
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

// emitShellArgv builds the (file, argv, argc) for a `shell: true` /
// exec()-style command: `/bin/sh -c cmd` on POSIX, `cmd.exe /d /s /c cmd`
// on Windows — the form Node uses there (libuv hands it to CreateProcess
// as a raw command line; /s keeps cmd.exe's handling of a quoted command
// predictable).
func (e *Emitter) emitShellArgv(cmdRef string) (fileRef, argvPtr, argsLen string) {
	e.ensureMalloc()
	argvPtr = e.freshReg()
	if targetGOOS() == "windows" {
		e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 32)", argvPtr))
		for i, a := range []string{"/d", "/s", "/c"} {
			s := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %d", s, argvPtr, i))
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", e.internString(a), s))
		}
		s3 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 3", s3, argvPtr))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", cmdRef, s3))
		// Node honours ComSpec for the shell (CRT getenv is case-insensitive
		// on Windows); cmd.exe only as the fallback (ADR-00740).
		e.ensureGetenv()
		comspec := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @getenv(ptr %s)", comspec, e.internString("COMSPEC")))
		noComspec := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", noComspec, comspec))
		fileSel := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", fileSel, noComspec, e.internString("cmd.exe"), comspec))
		return fileSel, argvPtr, "4"
	}
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", argvPtr))
	s0 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 0", s0, argvPtr))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", e.internString("-c"), s0))
	s1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 1", s1, argvPtr))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", cmdRef, s1))
	return e.internString("/bin/sh"), argvPtr, "2"
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
