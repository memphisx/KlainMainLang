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
