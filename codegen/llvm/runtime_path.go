// runtime_path.go — the posix flavour of Node's `path` module (emit_path.go)
// normalizes in pathsrc/path.c (TDD-00240); the win32 flavour is the C
// sidecar behind path_win32.go (TDD-00178).
package llvm

import _ "embed"

//go:embed pathsrc/path.c
var pathSource string

// PathSource is the posix path normalizer's C source.
func PathSource() string { return pathSource }

// UsesPathNormalize reports whether the program links the path normalizer.
func (e *Emitter) UsesPathNormalize() bool { return e.usedPathNormalize }
