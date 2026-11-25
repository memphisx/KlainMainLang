package llvm

import (
	_ "embed"
)

// The fs error runtime lives in C (TDD-00240): fssrc/errno.c maps an errno to
// Node's error-code name, libuv's description and libuv's errno (one table
// per target, built from the target's own errno constants; Windows uses the
// shim's Linux numbering, TDD-00177), and fssrc/fserror.c builds the Error an
// fs syscall failure raises (ADR-00768/00770/01000).

//go:embed fssrc/errno.c
var errnoSource string

//go:embed fssrc/fserror.c
var fsErrorSource string

// ErrnoSource is the errno tables' C source.
func ErrnoSource() string { return errnoSource }

// FsErrorSource is the fs error builder's C source, behind kml_layout.h.
func FsErrorSource() string { return layoutHeader() + fsErrorSource }
