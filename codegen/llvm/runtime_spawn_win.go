package llvm

import (
	"fmt"
	"runtime"
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
	if runtime.GOOS != "windows" || e.usedWinSpawn {
		return
	}
	e.usedWinSpawn = true
	e.emitGlobal("declare i32 @__kml_win_spawn(ptr, ptr, ptr, i32, i32, i32, i32, i32)")
}

// cpSpawnForkIR is the region of __kml_cp_spawn between the pipe setup and
// the `parent:` label.
func (e *Emitter) cpSpawnForkIR() string {
	if runtime.GOOS == "windows" {
		e.ensureWinSpawnDecl()
		return `  %kmlverb64 = lshr i64 %mode, 1
  %kmlverb = trunc i64 %kmlverb64 to i32
  %pid = call i32 @__kml_win_spawn(ptr %file, ptr %argv, ptr %cwd, i32 %inr, i32 %outw, i32 %errw, i32 -1, i32 %kmlverb)
  br label %parent
`
	}
	return `  %pid = call i32 @fork()
  %ischild = icmp eq i32 %pid, 0
  br i1 %ischild, label %child, label %parent
child:
  %hascwd = icmp ne ptr %cwd, null
  br i1 %hascwd, label %dochdir, label %setupfds
dochdir:
  call i32 @chdir(ptr %cwd)
  br label %setupfds
setupfds:
  call i32 @dup2(i32 %inr, i32 0)
  call i32 @dup2(i32 %outw, i32 1)
  call i32 @dup2(i32 %errw, i32 2)
  call i32 @close(i32 %inr)
  call i32 @close(i32 %inw)
  call i32 @close(i32 %outr)
  call i32 @close(i32 %outw)
  call i32 @close(i32 %errr)
  call i32 @close(i32 %errw)
  call i32 @execvp(ptr %file, ptr %argv)
  call void @_exit(i32 127)
  unreachable
`
}

// execSyncForkIR is the same region of __kml_exec_file_sync (one pipe, the
// child's stdout).
func (e *Emitter) execSyncForkIR() string {
	if runtime.GOOS == "windows" {
		e.ensureWinSpawnDecl()
		return `  %pid = call i32 @__kml_win_spawn(ptr %file, ptr %argv, ptr %cwd, i32 -1, i32 %writefd, i32 -1, i32 -1, i32 0)
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
	if runtime.GOOS == "windows" {
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
  %pid = call i32 @__kml_win_spawn(ptr %exe, ptr %argv, ptr null, i32 -1, i32 -1, i32 -1, i32 %cfd, i32 0)
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
	if runtime.GOOS == "windows" {
		e.ensureWinSpawnDecl()
		e.ensureUnsetenv()
		return `  %numbuf = alloca [16 x i8], align 1
  %numptr = getelementptr [16 x i8], ptr %numbuf, i32 0, i32 0
  %cfd64 = sext i32 %cfd to i64
  call i32 (ptr, ptr, ...) @sprintf(ptr %numptr, ptr ` + fmtFD + `, i64 %cfd64)
  call i32 @setenv(ptr ` + envFD + `, ptr %numptr, i32 1)
  %pid = call i32 @__kml_win_spawn(ptr %argv0, ptr %argv, ptr null, i32 -1, i32 -1, i32 -1, i32 %cfd, i32 0)
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
	if runtime.GOOS != "windows" {
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
	if runtime.GOOS != "windows" {
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
  %pid = call i32 @__kml_win_spawn(ptr %exe, ptr %argv, ptr null, i32 -1, i32 -1, i32 -1, i32 %lfd, i32 0)
  call i32 @unsetenv(ptr ` + envID + `)
  call i32 @unsetenv(ptr ` + envFD + `)
  br label %parentnext
`
}

// httpListenInheritIR is inserted at the top of __kml_http_bind_and_listen:
// a spawned worker returns the inherited listening fd instead of binding.
func (e *Emitter) httpListenInheritIR() string {
	if runtime.GOOS != "windows" {
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
	if runtime.GOOS != "windows" || e.usedHTTPClusterSeed {
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
	if runtime.GOOS == "windows" {
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
