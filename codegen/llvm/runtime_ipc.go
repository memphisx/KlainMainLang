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

// ensureIPCChildRuntime declares the child-side channel (ipcsrc/ipcc.c):
// NODE_CHANNEL_FD parsing, send, and the event-loop hooks that deliver
// 'message' events.
func (e *Emitter) ensureIPCChildRuntime() {
	if e.usedIPCChildRuntime {
		return
	}
	e.usedIPCChildRuntime = true
	e.ensureStrHeaderRuntime()

}
