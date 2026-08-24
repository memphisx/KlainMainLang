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
	"runtime"
)

// clusterWorkerIR: a Worker handle returned by cluster.fork() — { i64 id,
// i64 pid }. Field 0 is the worker id, field 1 the child pid.
const clusterWorkerIR = "{ i64, i64 }"

func (e *Emitter) ensureClusterRuntime() {
	if e.usedClusterRuntime {
		return
	}
	e.usedClusterRuntime = true
	e.ensureHTTPClusterFork() // declares @__kml_cluster_worker_id + fork()
	e.ensureMalloc()
	e.ensureRealloc()
	e.ensureMemset()
	e.ensureFflushDecl()
	e.ensureSprintf()
	e.ensureGetenv()

	e.ensureSetenvDecl()
	e.emitGlobal("declare i32 @atoi(ptr noundef)")
	e.emitGlobal("declare i32 @execv(ptr noundef, ptr noundef)")
	e.emitGlobal("declare void @_exit(i32 noundef) noreturn")
	e.ensureWaitpidDecl()
	if runtime.GOOS == "darwin" {
		e.emitGlobal("declare i32 @_NSGetExecutablePath(ptr noundef, ptr noundef)")
	} else {
		e.emitGlobal("declare i64 @readlink(ptr noundef, ptr noundef, i64 noundef)")
	}

	e.emitGlobal("@__kml_cluster_next_id = internal global i64 1, align 8")
	e.emitGlobal("@__kml_cluster_pids = internal global ptr null, align 8")
	e.emitGlobal("@__kml_cluster_pid_len = internal global i64 0, align 8")
	e.emitGlobal("@__kml_cluster_pid_cap = internal global i64 0, align 8")

	envName := e.internString("KML_CLUSTER_WORKER_ID")
	idFmt := e.internString("%lld")

	// __kml_cluster_seed_id(): a re-exec'd worker carries its id in the env;
	// read it into @__kml_cluster_worker_id so isPrimary/isWorker/workerId work.
	// The primary (env unset) leaves the global at 0. Called first thing in main.
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_cluster_seed_id() {
entry:
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
	if runtime.GOOS == "darwin" {
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

	// __kml_cluster_register_pid(pid): append to the primary's worker-pid table.
	e.emitGlobal(`
define void @__kml_cluster_register_pid(i64 %pid) {
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
  store i64 %newcap, ptr @__kml_cluster_pid_cap, align 8
  br label %store
store:
  %data = load ptr, ptr @__kml_cluster_pids, align 8
  %slot = getelementptr i64, ptr %data, i64 %len
  store i64 %pid, ptr %slot, align 8
  %newlen = add i64 %len, 1
  store i64 %newlen, ptr @__kml_cluster_pid_len, align 8
  ret void
}`)

	// __kml_cluster_fork(): fork; the child re-execs the executable as a worker
	// (KML_CLUSTER_WORKER_ID set), the parent registers the child and returns a
	// Worker { id, pid }.
	e.emitGlobal(fmt.Sprintf(`
define ptr @__kml_cluster_fork() {
entry:
  %%id = load i64, ptr @__kml_cluster_next_id, align 8
  call i32 @fflush(ptr null)
  %%pid = call i32 @fork()
  %%ischild = icmp eq i32 %%pid, 0
  br i1 %%ischild, label %%child, label %%parent
child:
  %%idbuf = call ptr @malloc(i64 24)
  call i32 (ptr, ptr, ...) @sprintf(ptr %%idbuf, ptr %s, i64 %%id)
  call i32 @setenv(ptr %s, ptr %%idbuf, i32 1)
  %%exe = call ptr @__kml_cluster_self_exe()
  %%argv = load ptr, ptr @__argv_ptr, align 8
  call i32 @execv(ptr %%exe, ptr %%argv)
  call void @_exit(i32 127)
  unreachable
parent:
  %%pid64 = sext i32 %%pid to i64
  call void @__kml_cluster_register_pid(i64 %%pid64)
  %%next = add i64 %%id, 1
  store i64 %%next, ptr @__kml_cluster_next_id, align 8
  %%w = call ptr @malloc(i64 16)
  %%wid = getelementptr %s, ptr %%w, i32 0, i32 0
  store i64 %%id, ptr %%wid, align 8
  %%wpid = getelementptr %s, ptr %%w, i32 0, i32 1
  store i64 %%pid64, ptr %%wpid, align 8
  ret ptr %%w
}`, idFmt, envName, clusterWorkerIR, clusterWorkerIR))

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
