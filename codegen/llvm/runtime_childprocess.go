// runtime_childprocess.go — async Node `child_process`: spawn/exec/execFile.
//
// A spawned child is a fork()+execvp() with three pipes (stdin write, stdout
// read, stderr read); the two read ends are made non-blocking and folded into
// the central select() event loop exactly like the Worker message pipes
// (TDD-00098): @__kml_cp_fdset_add adds every live child's read fds,
// @__kml_cp_dispatch drains them after select() and fires the registered
// listeners, @__kml_cp_keepalive holds the loop open while a child is live.
// No-op stubs (emitLoopTaskStubs) stand in when the program never spawns.
// The runtime lives in childprocsrc/childproc.c (TDD-00240); this file keeps
// the handle layout, the declares and the Windows spawn.
//
// One ChildProcess handle (%kml.cp) per spawn, in a process-wide registry.
// Streaming mode (spawn): stdout/stderr 'data'/'end' + 'close'/'exit'
// listeners are stored per-handle and fired from the dispatch. Buffered mode
// (exec/execFile): stdout/stderr accumulate into growable buffers, and a
// single (err, stdout, stderr) callback fires on child exit.
package llvm

import (
	_ "embed"
)

//go:embed childprocsrc/childproc.c
var childProcSource string

// ChildProcSource is the child_process runtime's C source, behind kml_layout.h.
func ChildProcSource() string { return layoutHeader() + childProcSource }

// UsesChildProcRuntime reports whether the program links the child_process
// runtime (childproc.c).
func (e *Emitter) UsesChildProcRuntime() bool { return e.usedChildProcRuntime }

// %kml.cp layout (fields, all 8-byte-slotted except the three i32 fds):
//
//	0 i64 pid
//	1 i32 stdinFd   (write end; -1 after .end())
//	2 i32 stdoutFd  (read end; -1 after EOF)
//	3 i32 stderrFd  (read end; -1 after EOF)
//	4 i64 state     (0 running · 1 reaped, 'exit' fired · 2 closed/finalized)
//	5 i64 exitCode
//	6 ptr stdout 'data' listener   · 7 ptr stdout 'end' listener
//	8 ptr stderr 'data' listener   · 9 ptr stderr 'end' listener
//
// 10 ptr 'close' listener         · 11 ptr 'exit' listener  · 12 ptr 'error'
// 13 i64 mode      (0 streaming spawn · 1 buffered exec)
// 14 ptr stdoutAccum {ptr,i64,i64} · 15 ptr stderrAccum · 16 ptr execCallback
// 17 i32 ipcFd (0 none · >0 open · -1 closed — TDD-00141 fork channel)
// 18 ptr 'message' listener · 19 ptr ipc channel (C-side line buffer)
// 20 i64 spawn errno · 21 i64 recorded .kill() signal (Windows exit shape)
// 22 i64 timeout deadline (absolute monotonic ns, 0 = none — ADR-00764)
// 23 i64 killSignal for the timeout kill (default 15 = SIGTERM)
// 24 i64 unref flag (1 = child.unref()'d — does not keep the loop alive, ADR-00767)
// 25 i64 raw wait status · 26 i64 plain exit code (both set at reap, read by __kml_cp_close)
// 27 i64 maxBuffer (bytes per stream, 0 = unlimited — exec/execFile, ADR-01080)
// 28 i64 maxBuffer hit (0 none · 1 stdout · 2 stderr): the child was killed for it
// Field 20 (i64) is the spawn-failure errno: 0 when the child started, else
// the errno the exec failed with (ENOENT for a missing command).
// __kml_cp_finalize fires 'error' instead of 'exit' when it is set
// (ADR-00754). calloc zeroes it, so an unset field means "started fine".
// Field 21 (i64) is the signal number a `.kill(sig)` recorded, 0 otherwise.
// POSIX recovers a signalled death from the wait status directly (WIFSIGNALED),
// so this field only feeds the Windows `'exit'`/`'close'` `(code, signal)`
// shape, where TerminateProcess leaves no signalled bit to read (TDD-00184).
const cpStructIR = "{ i64, i32, i32, i32, i64, i64, ptr, ptr, ptr, ptr, ptr, ptr, ptr, i64, ptr, ptr, ptr, i32, ptr, ptr, i64, i64, i64, i64, i64, i64, i64, i64, i64 }"

// cpStructBytes is the calloc size for cpStructIR (29 × 8).
const cpStructBytes = 232

// cpSignalEntry maps a Node signal name to the host platform's signal number.
type cpSignalEntry struct {
	num  int
	name string
}
