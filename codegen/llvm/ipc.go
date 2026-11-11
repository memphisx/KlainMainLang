// ipc.go — the child_process.fork IPC channel's embedded C (TDD-00141):
// NDJSON string framing over a socketpair, Node's own `json` serialization
// mode. The C is self-contained (no json-tree dependency); linked only when
// a program forks or touches the child-side channel surface.
package llvm

import _ "embed"

//go:embed ipcsrc/ipc.c
var ipcSource string

// IPCSource returns the embedded framing C.
func IPCSource() string { return ipcSource }
