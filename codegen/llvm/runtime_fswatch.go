// runtime_fswatch.go — fs.watch (TDD-00181): a native, event-driven file
// watcher folded into the select() event loop via the standard hook trio
// (__kml_fswatch_keepalive / _fdset_add / _dispatch), matching worker/cp/
// readline. The backends (Linux inotify, macOS kqueue, Windows
// ReadDirectoryChangesW behind a wakeup socket) and the natives
// lib/node/fs.ts's FSWatcher runs on live in fssrc/fswatch.c, one #if branch
// per platform (TDD-00240); the hooks and __kml_fs_watch keep their symbols.
package llvm

import _ "embed"

//go:embed fssrc/fswatch.c
var fsWatchSource string

// FsWatchSource is the fs.watch runtime's C source.
func FsWatchSource() string { return fsWatchSource }

// UsesFsWatch reports whether the program links the fs.watch runtime.
func (e *Emitter) UsesFsWatch() bool { return e.usedFsWatchRuntime }

// ensureFsWatchRuntime declares the fs.watch runtime (fswatch.c) once. A
// program without it gets the loop's no-op hook stubs (runtime_task.go).
func (e *Emitter) ensureFsWatchRuntime() {
	if e.usedFsWatchRuntime {
		return
	}
	e.usedFsWatchRuntime = true
	e.ensureFsThrow()
	e.ensureStrHeaderRuntime()
	e.emitGlobal(`declare ptr @__kml_fs_watch(ptr)
declare zeroext i1 @__kml_fswatch_keepalive()
declare zeroext i1 @__kml_fswatch_fdset_add(ptr, ptr)
declare void @__kml_fswatch_dispatch()
declare void @__kml_fswatch_on(ptr, i64, ptr)
declare void @__kml_fswatch_close(ptr)`)
}

// ensureNativeFsWatch declares the natives lib/node/fs.ts's FSWatcher runs
// on: __kml_native_fs_watch(path, inv, clo) starts a watcher whose every
// event calls inv(clo, isRename, 0) with the file name as the last string,
// and returns the watcher's handle; __kml_native_fs_watch_close(handle)
// stops it.
func (e *Emitter) ensureNativeFsWatch() {
	if e.fnDecls["__kml_native_fs_watch"] {
		return
	}
	e.fnDecls["__kml_native_fs_watch"] = true
	e.ensureFsWatchRuntime()
	e.ensureNativePool() // __kml_native_set_last_string lives in klainpool.c
	e.emitGlobal(`declare double @__kml_native_fs_watch(ptr, ptr, ptr)
declare void @__kml_native_fs_watch_close(double)`)
}
