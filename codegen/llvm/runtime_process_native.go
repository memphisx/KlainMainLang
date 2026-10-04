package llvm

// runtime_process_native.go — lib/native.d.ts's process primitives, the
// counterparts of Node's process binding (src/node_process_methods.cc) that
// lib/node/internal_process_methods.ts builds process.cwd, chdir, uptime,
// hrtime, kill, memoryUsage, umask and the credential reads on. A failing
// call returns -errno; the TypeScript side builds Node's error from it. The
// bodies are in processsrc/process.c; each ensure* here declares its natives.

import "fmt"

// ensureNativeProcessCwd declares processCwd: the working directory.
func (e *Emitter) ensureNativeProcessCwd() {
	if e.fnDecls["__kml_native_process_cwd"] {
		return
	}
	e.fnDecls["__kml_native_process_cwd"] = true
	e.ensureProcessCwd()
	e.emitGlobal("declare ptr @__kml_native_process_cwd()")
}

// ensureNativeProcessChdir declares processChdir: 0, or -errno.
func (e *Emitter) ensureNativeProcessChdir() {
	if e.fnDecls["__kml_native_process_chdir"] {
		return
	}
	e.fnDecls["__kml_native_process_chdir"] = true
	e.emitGlobal("declare double @__kml_native_process_chdir(ptr)")
}

// ensureNativeProcessUptime declares processUptime: seconds since start.
func (e *Emitter) ensureNativeProcessUptime() {
	if e.fnDecls["__kml_native_process_uptime"] {
		return
	}
	e.fnDecls["__kml_native_process_uptime"] = true
	e.ensureProcessUptime()
	e.emitGlobal("declare double @__kml_native_process_uptime()")
}

// ensureNativeProcessHrtime declares processHrtime, which reads the
// monotonic clock into a buffer, and processHrtimeRead(0 seconds,
// 1 nanoseconds), which reads it back: Node's hrtime binding fills a
// shared buffer the same way.
func (e *Emitter) ensureNativeProcessHrtime() {
	if e.fnDecls["__kml_native_process_hrtime"] {
		return
	}
	e.fnDecls["__kml_native_process_hrtime"] = true
	e.emitGlobal(`declare void @__kml_native_process_hrtime()
declare double @__kml_native_process_hrtime_read(double)`)
}

// ensureNativeKillPid declares killPid: kill(pid, sig), 0 or -errno.
func (e *Emitter) ensureNativeKillPid() {
	if e.fnDecls["__kml_native_kill_pid"] {
		return
	}
	e.fnDecls["__kml_native_kill_pid"] = true
	e.emitGlobal("declare double @__kml_native_kill_pid(double, double)")
}

// ensureNativeProcessMemory declares processMemory(0 rss, 1 heapTotal,
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
	e.emitGlobal("declare double @__kml_native_process_memory(double)")
}

// ensureNativeProcessUmask declares processUmask(mask): sets the mask and
// returns the previous one; a negative mask only reads it (set, restore,
// as libuv does).
func (e *Emitter) ensureNativeProcessUmask() {
	if e.fnDecls["__kml_native_process_umask"] {
		return
	}
	e.fnDecls["__kml_native_process_umask"] = true
	e.emitGlobal("declare double @__kml_native_process_umask(double)")
}

// ensureNativeProcessIds declares processId(0 pid, 1 ppid, 2 uid, 3 euid,
// 4 gid, 5 egid). Windows has no credential reads; they are -1 there.
func (e *Emitter) ensureNativeProcessIds() {
	if e.fnDecls["__kml_native_process_id"] {
		return
	}
	e.fnDecls["__kml_native_process_id"] = true
	e.ensureGetpid()
	e.emitGlobal("declare double @__kml_native_process_id(double)")
}

// ensureNativeProcessArgv declares processArgc and processArgv(i): the
// Node-shaped argument vector main stores ([execPath, execPath, …args]).
func (e *Emitter) ensureNativeProcessArgv() {
	if e.fnDecls["__kml_native_process_argc"] {
		return
	}
	e.fnDecls["__kml_native_process_argc"] = true
	e.emitGlobal(`declare double @__kml_native_process_argc()
declare ptr @__kml_native_process_argv0()
declare ptr @__kml_native_process_argv(double)`)
}

// ensureNativeProcessExecPath declares processExecPath: the running
// executable's absolute path.
func (e *Emitter) ensureNativeProcessExecPath() {
	if e.fnDecls["__kml_native_process_exec_path"] {
		return
	}
	e.fnDecls["__kml_native_process_exec_path"] = true
	e.ensureExecPath()
	e.ensureStrHeaderRuntime()
	e.emitGlobal("declare ptr @__kml_native_process_exec_path()")
}

// ensureNativeProcessExit declares processSetExitCode(code), the code the
// program exits with at its end, and processReallyExit(code): the 'exit'
// hook, then exit(3) (Node's process.reallyExit).
func (e *Emitter) ensureNativeProcessExit() {
	if e.fnDecls["__kml_native_process_set_exit_code"] {
		return
	}
	e.fnDecls["__kml_native_process_set_exit_code"] = true
	e.usedProcessLifecycle = true
	e.emitGlobal(`declare void @__kml_native_process_set_exit_code(double)
declare double @__kml_native_process_get_exit_code()
declare void @__kml_native_process_really_exit(double)`)
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

// ensureNativeEnv declares envGet (a variable, or absent), envSet and
// envDelete over the live environment block.
func (e *Emitter) ensureNativeEnv() {
	if e.fnDecls["__kml_native_env_get"] {
		return
	}
	e.fnDecls["__kml_native_env_get"] = true
	e.ensureStrHeaderRuntime()
	e.emitGlobal(`declare zeroext i1 @__kml_native_env_get(ptr, ptr)
declare void @__kml_native_env_set(ptr, ptr)
declare void @__kml_native_env_delete(ptr)`)
}
