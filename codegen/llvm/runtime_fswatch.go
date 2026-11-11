// runtime_fswatch.go — fs.watch (TDD-00181): a native, event-driven file
// watcher in the event loop: each watcher is a libuv uv_fs_event_t
// (TDD-00244 Stage 4). The runtime and the natives lib/node/fs.ts's
// FSWatcher runs on live in fssrc/fswatch.c (TDD-00240).
package llvm

import _ "embed"

//go:embed fssrc/fswatch.c
var fsWatchSource string

// FsWatchSource is the fs.watch runtime's C source.
func FsWatchSource() string { return fsWatchSource }

// ensureFsWatchRuntime links the fs.watch runtime (fswatch.c) once.
func (e *Emitter) ensureFsWatchRuntime() {
	if e.usedFsWatchRuntime {
		return
	}
	e.usedFsWatchRuntime = true
	e.ensureFsThrow()
	e.ensureStrHeaderRuntime()
}
