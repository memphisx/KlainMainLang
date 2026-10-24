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

// UsesFsWatch reports whether the program links the fs.watch runtime.
func (e *Emitter) UsesFsWatch() bool { return e.usedFsWatchRuntime }

// ensureFsWatchRuntime links the fs.watch runtime (fswatch.c) once.
func (e *Emitter) ensureFsWatchRuntime() {
	if e.usedFsWatchRuntime {
		return
	}
	e.usedFsWatchRuntime = true
	e.ensureFsThrow()
	e.ensureStrHeaderRuntime()
}

// ensureNativeFsWatch declares the natives lib/node/fs.ts's FSWatcher runs
// on: __kml_native_fs_watch(path, flags, inv, clo) starts a watcher (flags: 1
// recursive, 2 persistent: false) whose every event calls inv(clo, isRename,
// 0) with the file name as the last string, and returns the watcher's handle;
// __kml_native_fs_watch_close(handle) stops it and
// __kml_native_fs_watch_ref(handle, on) refs or unrefs it.
func (e *Emitter) ensureNativeFsWatch() {
	if e.fnDecls["__kml_native_fs_watch"] {
		return
	}
	e.fnDecls["__kml_native_fs_watch"] = true
	e.ensureFsWatchRuntime()
	e.ensureNativePool() // __kml_native_set_last_string lives in klainpool.c
	e.emitGlobal(`declare double @__kml_native_fs_watch(ptr, double, ptr, ptr)
declare void @__kml_native_fs_watch_close(double)
declare void @__kml_native_fs_watch_ref(double, double)`)
}
