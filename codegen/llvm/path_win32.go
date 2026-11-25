package llvm

import (
	_ "embed"
)

// path_win32.go — Node's `path.win32` flavour (TDD-00178). The algorithms are
// a C port of lib/path.js's win32 object (pathsrc/path_win32.c), linked when
// a win32-flavoured path call names it: every bare `path.X` on a Windows
// host, or an explicit `path.win32.X` on any host. The posix flavour stays in runtime_path.go.

//go:embed pathsrc/path_win32.c
var pathWin32Source string

// PathWin32Source returns the C source implementing the __kml_path_win32_*
// ABI, linked by the symbols the program or the runtime (fserror.c and the
// pool on Windows) names.
func PathWin32Source() string { return pathWin32Source }

// pathFlavor names which of Node's two path flavours a call site resolved to.
type pathFlavor int

const (
	pathPosix pathFlavor = iota
	pathWin32
)
