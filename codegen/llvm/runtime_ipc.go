// runtime_ipc.go — child_process.fork's self-fork spawn and both ends of its
// IPC channel (TDD-00141). Wire framing (NDJSON string lines) lives in the
// embedded C (ipcsrc/ipc.c); the fd plumbing is C too (TDD-00240):
//
//   - parent (ipcsrc/cpipc.c): __kml_cp_wrap_ipc, __kml_cp_ipc_drain
//     (event-loop drain of a handle's channel, firing its 'message'
//     listeners), __kml_cp_send / __kml_cp_disconnect.
//   - __kml_cp_fork (socketpair, fork, re-exec of the current binary with
//     NODE_CHANNEL_FD set) stays IR here: its fork/spawn region differs per
//     OS (runtime_spawn_win.go).
//   - child (ipcsrc/ipcc.c): the __kml_ipcc_* event-loop hooks
//     (keepalive/fdset_add/dispatch) plus __kml_ipcc_fd (NODE_CHANNEL_FD,
//     parsed once), __kml_ipcc_send.
//
// A plain-spawn program never links the channel C: __kml_cp_dispatch calls
// @__kml_cp_ipc_drain unconditionally, so a no-op stub stands in when fork
// was never used (emitCPRuntimeStubs, called from program finalization).
package llvm

import (
	_ "embed"
)

//go:embed ipcsrc/cpipc.c
var cpIPCSource string

//go:embed ipcsrc/ipcc.c
var ipcChildSource string

// CPIPCSource is the parent-side channel C (cpipc.c), behind kml_layout.h.
func CPIPCSource() string { return layoutHeader() + cpIPCSource }

// IPCChildSource is the child-side channel C (ipcc.c), behind kml_layout.h.
func IPCChildSource() string { return layoutHeader() + ipcChildSource }

// UsesCPIPC reports whether the program links cpipc.c (it forks).
func (e *Emitter) UsesCPIPC() bool { return e.usedCPForkRuntime }

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
	e.ensureWorkerFdSetbit()
	e.ensureAtoll()
	e.ensureGetenv()

	e.emitGlobal(`declare i32 @__kml_ipcc_fd()
declare i1 @__kml_ipcc_send(ptr)
declare i1 @__kml_ipcc_keepalive()
declare i1 @__kml_ipcc_fdset_add(ptr, ptr)
declare void @__kml_ipcc_dispatch()`)
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
