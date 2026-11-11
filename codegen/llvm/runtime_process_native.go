package llvm

// runtime_process_native.go — lib/native.d.ts's process primitives, the
// counterparts of Node's process binding (src/node_process_methods.cc) that
// lib/node/internal_process_methods.ts builds process.cwd, chdir, uptime,
// hrtime, kill, memoryUsage, umask and the credential reads on. A failing
// call returns -errno; the TypeScript side builds Node's error from it. The
// bodies are in processsrc/process.c; each ensure* here declares its natives.

import "fmt"

// ensureNativeProcessExit declares processSetExitCode(code), the code the
// program exits with at its end, and processReallyExit(code): the 'exit'
// hook, then exit(3) (Node's process.reallyExit).
func (e *Emitter) ensureNativeProcessExit() {
	if e.fnDecls["__kml_native_process_set_exit_code"] {
		return
	}
	e.fnDecls["__kml_native_process_set_exit_code"] = true
	e.usedProcessLifecycle = true
}

// ensureNativeProcessVersion defines processVersion(0 version, 1 node,
// 2 v8, 3 klain).
func (e *Emitter) ensureNativeProcessVersion() {
	if e.fnDecls["__kml_native_process_version"] {
		return
	}
	e.fnDecls["__kml_native_process_version"] = true
	restore := e.beginDetachedFunc()
	which := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = fptosi double %%which to i32", which))
	strs := []string{e.internString("v" + nodeCompatVersion), e.internString(nodeCompatVersion), e.internString(nodeCompatV8), e.internString(KlainVersion)}
	labels := make([]string, len(strs))
	for k := range labels {
		labels[k] = e.freshLabel("ver")
	}
	e.emitTerminator(fmt.Sprintf("switch i32 %s, label %%%s [ i32 1, label %%%s i32 2, label %%%s i32 3, label %%%s ]", which, labels[0], labels[1], labels[2], labels[3]))
	for k, l := range labels {
		e.emitLabel(l)
		e.emitTerminator("ret " + irPtr + " " + strs[k])
	}
	body := e.allocas.String() + e.body.String()
	restore()
	e.writeFunc(irPtr+" @__kml_native_process_version(double %which)", body)
}
