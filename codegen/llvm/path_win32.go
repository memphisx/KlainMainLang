package llvm

import (
	_ "embed"
)

// path_win32.go — Node's `path.win32` flavour (TDD-00178). The algorithms are
// a C port of lib/path.js's win32 object (pathsrc/path_win32.c), compiled
// alongside the program (the json_parse.c pattern) whenever a win32-flavoured
// path call is emitted: every bare `path.X` on a Windows host, or an explicit
// `path.win32.X` on any host. The posix flavour stays in runtime_path.go.

//go:embed pathsrc/path_win32.c
var pathWin32Source string

// PathWin32Source returns the C source implementing the __kml_path_win32_*
// ABI; main.go writes it next to the .ll and compiles it when UsesPathWin32()
// is set (libc only, no library to locate).
func PathWin32Source() string { return pathWin32Source }

// UsesPathWin32 reports whether any win32-flavoured path call reached codegen.
func (e *Emitter) UsesPathWin32() bool { return e.usesPathWin32 }

// pathFlavor names which of Node's two path flavours a call site resolved to.
type pathFlavor int

const (
	pathPosix pathFlavor = iota
	pathWin32
)

// hostPathFlavor is what a bare `path.X` means on this host: Node's `path` is
// `path.win32` on Windows and `path.posix` everywhere else. A compile-time
// switch, like nodePlatformName() — this compiler builds for the host only.
func (e *Emitter) hostPathFlavor() pathFlavor {
	if e.opts.Target.OS() == "windows" {
		return pathWin32
	}
	return pathPosix
}

// ensurePathWin32 declares the sidecar's ABI once and marks the program as
// needing path_win32.c compiled in.
func (e *Emitter) ensurePathWin32() {
	e.usesPathWin32 = true
	if e.declaredPathWin32 {
		return
	}
	e.declaredPathWin32 = true
	e.emitGlobal("declare ptr @__kml_path_win32_join(i64, ptr)")
	e.emitGlobal("declare ptr @__kml_path_win32_resolve(i64, ptr, ptr, i32)")
	e.emitGlobal("declare ptr @__kml_path_win32_dirname(ptr)")
	e.emitGlobal("declare ptr @__kml_path_win32_basename(ptr, ptr)")
	e.emitGlobal("declare ptr @__kml_path_win32_extname(ptr)")
	e.emitGlobal("declare i32 @__kml_path_win32_is_absolute(ptr)")
	e.emitGlobal("declare void @__kml_path_win32_parse(ptr, ptr, ptr, ptr, ptr, ptr)")
	e.emitGlobal("declare ptr @__kml_path_win32_format(ptr, ptr, ptr, ptr, ptr)")
	// url.pathToFileURL / url.fileURLToPath, Windows halves (ADR-00722).
	e.emitGlobal("declare ptr @__kml_path_win32_to_file_url(ptr, ptr, ptr)")
	e.emitGlobal("declare ptr @__kml_path_win32_from_file_url(ptr, ptr, ptr)")
	e.emitGlobal("declare ptr @__kml_path_win32_split_file_host(ptr, ptr)")
	e.emitGlobal("declare ptr @__kml_path_win32_file_url_pathname(ptr)")
	// normalize / relative / toNamespacedPath, both flavours (ADR-00723): the
	// posix trio lives in the same sidecar since it shares normalizeString.
	e.emitGlobal("declare ptr @__kml_path_win32_normalize(ptr)")
	e.emitGlobal("declare ptr @__kml_path_win32_relative(ptr, ptr, ptr, i32)")
	e.emitGlobal("declare ptr @__kml_path_win32_to_namespaced_path(ptr, ptr, i32)")
	e.emitGlobal("declare ptr @__kml_path_posix_normalize(ptr)")
	e.emitGlobal("declare ptr @__kml_path_posix_resolve(i64, ptr, ptr)")
	e.emitGlobal("declare ptr @__kml_path_posix_relative(ptr, ptr, ptr)")
	e.emitGlobal("declare ptr @__kml_path_posix_format(ptr, ptr, ptr, ptr, ptr)")
}
