// ipc.go — the child_process.fork IPC channel's embedded C (TDD-00141):
// NDJSON string framing over a socketpair, Node's own `json` serialization
// mode. The C is self-contained (no json-tree dependency); linked only when
// a program forks or touches the child-side channel surface.
package llvm

import _ "embed"

//go:embed ipcsrc/ipc.c
var ipcSource string

// UsesIPC reports whether the program used the fork IPC channel (either
// side), so the driver links the embedded framing C.
func (e *Emitter) UsesIPC() bool { return e.usedIPC }

// IPCSource returns the embedded framing C.
func IPCSource() string { return ipcSource }

// ensureIPCDecls declares the framing C's entry points once and marks the
// source for linking.
func (e *Emitter) ensureIPCDecls() {
	if e.usedIPC {
		return
	}
	e.usedIPC = true
	e.emitGlobal("declare ptr @__kml_ipc_chan_new()")
	e.emitGlobal("declare void @__kml_ipc_feed(ptr, ptr, i64)")
	e.emitGlobal("declare ptr @__kml_ipc_take(ptr)")
	e.emitGlobal("declare i64 @__kml_ipc_send(i64, ptr)")
}
