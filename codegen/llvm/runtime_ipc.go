// runtime_ipc.go — the child side of a forked process's IPC channel
// (TDD-00141): its reader (a libuv watcher on POSIX; the __kml_ipcc_*
// select hooks on Windows) plus __kml_ipcc_fd (NODE_CHANNEL_FD, parsed once) and
// __kml_ipcc_send, in ipcsrc/ipcc.c. Wire framing (NDJSON string lines) lives
// in ipcsrc/ipc.c. The parent side is child_process's TypeScript port over the
// pool's process handle (TDD-00231).
package llvm

import (
	_ "embed"
)

//go:embed ipcsrc/ipcc.c
var ipcChildSource string

// IPCChildSource is the child-side channel C (ipcc.c), behind kml_layout.h.
func IPCChildSource() string { return layoutHeader() + ipcChildSource }

// UsesIPCChild reports whether the program links ipcc.c (it reads the
// channel as a forked child).
func (e *Emitter) UsesIPCChild() bool { return e.usedIPCChildRuntime }

// ensureIPCChildRuntime declares the child-side channel (ipcsrc/ipcc.c):
// NODE_CHANNEL_FD parsing, send, and the event-loop hooks that deliver
// 'message' events.
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
	if e.opts.Target.OS() == "windows" {
		e.ensureWorkerFdSetbit()
	}
	e.ensureAtoll()
	e.ensureGetenv()

	e.emitGlobal(`declare i32 @__kml_ipcc_fd()
declare i1 @__kml_ipcc_send(ptr)`)
}

// emitCPRuntimeStubs finalizes the child-side __kml_ipcc_* loop hooks when
// the program never touches the channel: Windows only (POSIX reads it on a
// libuv watcher). Called from program finalization beside
// emitLoopTaskStubs.
func (e *Emitter) emitCPRuntimeStubs() {
	if e.usedHTTP && !e.usedIPCChildRuntime && e.opts.Target.OS() == "windows" {
		e.emitGlobal("define i1 @__kml_ipcc_keepalive() {\nentry:\n  ret i1 0\n}")
		e.emitGlobal("define i1 @__kml_ipcc_fdset_add(ptr %fdset, ptr %maxfd) {\nentry:\n  ret i1 0\n}")
		e.emitGlobal("define void @__kml_ipcc_dispatch() {\nentry:\n  ret void\n}")
	}
}

// ensureNativeIPCChildClaim defines lib/native.d.ts's ipcChildClaim: the
// process emitter's fork channel takes the descriptor over from the
// runtime's own child-side reader (which cluster's worker side still sends
// through), probing it first so NODE_CHANNEL_FD may then be deleted.
func (e *Emitter) ensureNativeIPCChildClaim() {
	if e.fnDecls["__kml_native_ipc_child_claim"] {
		return
	}
	e.fnDecls["__kml_native_ipc_child_claim"] = true
	e.ensureIPCChildRuntime()
	e.emitGlobal("declare void @__kml_native_ipc_child_claim()")
}
