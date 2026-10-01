package llvm

// runtime_process_native.go — lib/native.d.ts's process primitives, the
// counterparts of Node's process binding (src/node_process_methods.cc) that
// lib/node/internal_process_methods.ts builds process.cwd, chdir, uptime,
// hrtime, kill, memoryUsage, umask and the credential reads on. A failing
// call returns -errno; the TypeScript side builds Node's error from it.

import "fmt"

// ensureNativeProcessCwd defines processCwd: the working directory.
func (e *Emitter) ensureNativeProcessCwd() {
	if e.fnDecls["__kml_native_process_cwd"] {
		return
	}
	e.fnDecls["__kml_native_process_cwd"] = true
	e.ensureProcessCwd()
	e.emitGlobal(`
define ptr @__kml_native_process_cwd() {
entry:
  %r = call ptr @__kml_process_cwd()
  ret ptr %r
}`)
}

// ensureNativeProcessChdir defines processChdir: 0, or -errno.
func (e *Emitter) ensureNativeProcessChdir() {
	if e.fnDecls["__kml_native_process_chdir"] {
		return
	}
	e.fnDecls["__kml_native_process_chdir"] = true
	e.ensureChdirDecl()
	e.ensureErrnoAccessor()
	e.emitGlobal(fmt.Sprintf(`
define double @__kml_native_process_chdir(ptr %%path) {
entry:
  %%r = call i32 @chdir(ptr %%path)
  %%failed = icmp ne i32 %%r, 0
  br i1 %%failed, label %%fail, label %%ok
fail:
  %%ep = call ptr @%s()
  %%ev = load i32, ptr %%ep, align 4
  %%neg = sub i32 0, %%ev
  %%d = sitofp i32 %%neg to double
  ret double %%d
ok:
  ret double 0.0
}`, e.errnoAccessor()))
}

// ensureNativeProcessUptime defines processUptime: seconds since start.
func (e *Emitter) ensureNativeProcessUptime() {
	if e.fnDecls["__kml_native_process_uptime"] {
		return
	}
	e.fnDecls["__kml_native_process_uptime"] = true
	e.ensureProcessUptime()
	e.emitGlobal(`
define double @__kml_native_process_uptime() {
entry:
  %r = call double @__kml_process_uptime()
  ret double %r
}`)
}

// ensureNativeProcessHrtime defines processHrtime, which reads the
// monotonic clock into a buffer, and processHrtimeRead(0 seconds,
// 1 nanoseconds), which reads it back: Node's hrtime binding fills a
// shared buffer the same way.
func (e *Emitter) ensureNativeProcessHrtime() {
	if e.fnDecls["__kml_native_process_hrtime"] {
		return
	}
	e.fnDecls["__kml_native_process_hrtime"] = true
	e.ensureClockGettime()
	e.emitGlobal("@__kml_hrtime_buf = internal global [2 x i64] zeroinitializer, align 8")
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_native_process_hrtime() {
entry:
  call i32 @clock_gettime(i32 %s, ptr @__kml_hrtime_buf)
  ret void
}
define double @__kml_native_process_hrtime_read(double %%which) {
entry:
  %%i = fptosi double %%which to i32
  %%p = getelementptr [2 x i64], ptr @__kml_hrtime_buf, i32 0, i32 %%i
  %%v = load i64, ptr %%p, align 8
  %%d = sitofp i64 %%v to double
  ret double %%d
}`, e.monotonicClockID()))
}

// ensureNativeKillPid defines killPid: kill(pid, sig), 0 or -errno.
func (e *Emitter) ensureNativeKillPid() {
	if e.fnDecls["__kml_native_kill_pid"] {
		return
	}
	e.fnDecls["__kml_native_kill_pid"] = true
	e.ensureCPKill()
	e.ensureErrnoAccessor()
	e.emitGlobal(fmt.Sprintf(`
define double @__kml_native_kill_pid(double %%pid, double %%sig) {
entry:
  %%p = fptosi double %%pid to i32
  %%s = fptosi double %%sig to i32
  %%r = call i32 @kill(i32 %%p, i32 %%s)
  %%failed = icmp ne i32 %%r, 0
  br i1 %%failed, label %%fail, label %%ok
fail:
  %%ep = call ptr @%s()
  %%ev = load i32, ptr %%ep, align 4
  %%neg = sub i32 0, %%ev
  %%d = sitofp i32 %%neg to double
  ret double %%d
ok:
  ret double 0.0
}`, e.errnoAccessor()))
}

// ensureNativeProcessMemory defines processMemory(0 rss, 1 heapTotal,
// 2 heapUsed): see ProcMemSource for what the heap numbers measure.
func (e *Emitter) ensureNativeProcessMemory() {
	if e.fnDecls["__kml_native_process_memory"] {
		return
	}
	e.fnDecls["__kml_native_process_memory"] = true
	e.ensureCurrentRSS()
	if !e.usedHeapStats {
		e.usedHeapStats = true
		e.emitGlobal("declare i64 @__kml_heap_total_bytes()")
		e.emitGlobal("declare i64 @__kml_heap_used_bytes()")
	}
	e.emitGlobal(`
define double @__kml_native_process_memory(double %which) {
entry:
  %i = fptosi double %which to i32
  switch i32 %i, label %rss [ i32 1, label %total
                               i32 2, label %used ]
rss:
  %a = call i64 @__kml_current_rss_bytes()
  %ad = uitofp i64 %a to double
  ret double %ad
total:
  %b = call i64 @__kml_heap_total_bytes()
  %bd = uitofp i64 %b to double
  ret double %bd
used:
  %c = call i64 @__kml_heap_used_bytes()
  %cd = uitofp i64 %c to double
  ret double %cd
}`)
}

// ensureNativeProcessUmask defines processUmask(mask): sets the mask and
// returns the previous one; a negative mask only reads it (set, restore,
// as libuv does).
func (e *Emitter) ensureNativeProcessUmask() {
	if e.fnDecls["__kml_native_process_umask"] {
		return
	}
	e.fnDecls["__kml_native_process_umask"] = true
	e.declareFn(e.umaskSymbol(), fmt.Sprintf("declare i32 @%s(i32 noundef)", e.umaskSymbol()))
	e.emitGlobal(fmt.Sprintf(`
define double @__kml_native_process_umask(double %%mask) {
entry:
  %%read = fcmp olt double %%mask, 0.0
  br i1 %%read, label %%query, label %%set
query:
  %%old = call i32 @%[1]s(i32 0)
  call i32 @%[1]s(i32 %%old)
  %%od = uitofp i32 %%old to double
  ret double %%od
set:
  %%m = fptosi double %%mask to i32
  %%prev = call i32 @%[1]s(i32 %%m)
  %%pd = uitofp i32 %%prev to double
  ret double %%pd
}`, e.umaskSymbol()))
}

// ensureNativeProcessIds defines processId(0 pid, 1 ppid, 2 uid, 3 euid,
// 4 gid, 5 egid). Windows has no credential reads; they are -1 there.
func (e *Emitter) ensureNativeProcessIds() {
	if e.fnDecls["__kml_native_process_id"] {
		return
	}
	e.fnDecls["__kml_native_process_id"] = true
	e.ensureGetpid()
	if e.opts.Target.OS() == "windows" {
		e.emitGlobal(`
define double @__kml_native_process_id(double %which) {
entry:
  %i = fptosi double %which to i32
  %ispid = icmp eq i32 %i, 0
  br i1 %ispid, label %pid, label %none
pid:
  %p = call i64 @__kml_getpid()
  %pd = sitofp i64 %p to double
  ret double %pd
none:
  ret double -1.0
}`)
		return
	}
	e.declareFn("getppid", "declare i32 @getppid()")
	if !e.usedProcessGetID {
		e.usedProcessGetID = true
		e.emitGlobal("declare i32 @getuid()")
		e.emitGlobal("declare i32 @geteuid()")
		e.emitGlobal("declare i32 @getgid()")
		e.emitGlobal("declare i32 @getegid()")
	}
	e.emitGlobal(`
define double @__kml_native_process_id(double %which) {
entry:
  %i = fptosi double %which to i32
  switch i32 %i, label %pid [ i32 1, label %ppid
                              i32 2, label %uid
                              i32 3, label %euid
                              i32 4, label %gid
                              i32 5, label %egid ]
pid:
  %p = call i64 @__kml_getpid()
  %pd = sitofp i64 %p to double
  ret double %pd
ppid:
  %a = call i32 @getppid()
  %ad = sitofp i32 %a to double
  ret double %ad
uid:
  %b = call i32 @getuid()
  %bd = uitofp i32 %b to double
  ret double %bd
euid:
  %c = call i32 @geteuid()
  %cd = uitofp i32 %c to double
  ret double %cd
gid:
  %d = call i32 @getgid()
  %dd = uitofp i32 %d to double
  ret double %dd
egid:
  %f = call i32 @getegid()
  %fd = uitofp i32 %f to double
  ret double %fd
}`)
}

// ensureNativeProcessArgv defines processArgc and processArgv(i): the
// Node-shaped argument vector main stores ([execPath, execPath, …args]).
func (e *Emitter) ensureNativeProcessArgv() {
	if e.fnDecls["__kml_native_process_argc"] {
		return
	}
	e.fnDecls["__kml_native_process_argc"] = true
	e.emitGlobal(`
define double @__kml_native_process_argc() {
entry:
  %n = load i64, ptr @__process_argv_len, align 8
  %d = sitofp i64 %n to double
  ret double %d
}
define ptr @__kml_native_process_argv0() {
entry:
  %data = load ptr, ptr @__argv_ptr, align 8
  %s = load ptr, ptr %data, align 8
  ret ptr %s
}
define ptr @__kml_native_process_argv(double %i) {
entry:
  %n = fptosi double %i to i64
  %data = load ptr, ptr @__process_argv_ptr, align 8
  %p = getelementptr ptr, ptr %data, i64 %n
  %s = load ptr, ptr %p, align 8
  ret ptr %s
}`)
}

// ensureNativeProcessExecPath defines processExecPath: the running
// executable's absolute path.
func (e *Emitter) ensureNativeProcessExecPath() {
	if e.fnDecls["__kml_native_process_exec_path"] {
		return
	}
	e.fnDecls["__kml_native_process_exec_path"] = true
	e.ensureExecPath()
	e.ensureStrHeaderRuntime()
	e.emitGlobal(`
define ptr @__kml_native_process_exec_path() {
entry:
  %r = call ptr @__kml_execpath()
  %s = call ptr @__kml_str_from_cstr(ptr %r)
  ret ptr %s
}`)
}

// ensureNativeProcessExit defines processSetExitCode(code), the code the
// program exits with at its end, and processReallyExit(code): the 'exit'
// hook, then exit(3) (Node's process.reallyExit).
func (e *Emitter) ensureNativeProcessExit() {
	if e.fnDecls["__kml_native_process_set_exit_code"] {
		return
	}
	e.fnDecls["__kml_native_process_set_exit_code"] = true
	e.usedProcessLifecycle = true
	e.ensureExit()
	e.emitGlobal(`
define void @__kml_native_process_set_exit_code(double %code) {
entry:
  %c = fptosi double %code to i64
  store i64 %c, ptr @__kml_process_exit_code, align 8
  ret void
}
define double @__kml_native_process_get_exit_code() {
entry:
  %c = load i64, ptr @__kml_process_exit_code, align 8
  %d = sitofp i64 %c to double
  ret double %d
}
define void @__kml_native_process_really_exit(double %code) {
entry:
  %c = fptosi double %code to i64
  store i64 %c, ptr @__kml_process_exit_code, align 8
  call void @__kml_run_exit_handlers(i64 %c)
  %final = load i64, ptr @__kml_process_exit_code, align 8
  %c32 = trunc i64 %final to i32
  ` + e.exitCall("%c32") + `
  unreachable
}`)
}

// ensureNativeProcessVersion defines processVersion(0 version, 1 node,
// 2 v8, 3 klain).
func (e *Emitter) ensureNativeProcessVersion() {
	if e.fnDecls["__kml_native_process_version"] {
		return
	}
	e.fnDecls["__kml_native_process_version"] = true
	e.emitGlobal(fmt.Sprintf(`
define ptr @__kml_native_process_version(double %%which) {
entry:
  %%i = fptosi double %%which to i32
  switch i32 %%i, label %%version [ i32 1, label %%node
                                    i32 2, label %%v8
                                    i32 3, label %%klain ]
version:
  ret ptr %s
node:
  ret ptr %s
v8:
  ret ptr %s
klain:
  ret ptr %s
}`, e.internString("v"+nodeCompatVersion), e.internString(nodeCompatVersion), e.internString(nodeCompatV8), e.internString(KlainVersion)))
}

// ensureNativeEnv defines envGet (a variable, or absent), envSet and
// envDelete over the live environment block.
func (e *Emitter) ensureNativeEnv() {
	if e.fnDecls["__kml_native_env_get"] {
		return
	}
	e.fnDecls["__kml_native_env_get"] = true
	e.ensureGetenv()
	e.ensureSetenvDecl()
	e.ensureUnsetenv()
	e.ensureStrHeaderRuntime()
	e.emitGlobal(`
define zeroext i1 @__kml_native_env_get(ptr %key, ptr %out) {
entry:
  %raw = call ptr @getenv(ptr %key)
  %absent = icmp eq ptr %raw, null
  br i1 %absent, label %none, label %some
none:
  ret i1 0
some:
  %s = call ptr @__kml_str_from_cstr(ptr %raw)
  store ptr %s, ptr %out, align 8
  ret i1 1
}
define void @__kml_native_env_set(ptr %key, ptr %value) {
entry:
  call i32 @setenv(ptr %key, ptr %value, i32 1)
  ret void
}
define void @__kml_native_env_delete(ptr %key) {
entry:
  call i32 @unsetenv(ptr %key)
  ret void
}`)
}

// umaskSymbol is the C runtime's umask: _umask on Windows.
func (e *Emitter) umaskSymbol() string {
	if e.opts.Target.OS() == "windows" {
		return "_umask"
	}
	return "umask"
}
