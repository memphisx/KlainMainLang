package llvm

import (
	_ "embed"
	"fmt"
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

// ensureFsThrow declares the fs error builders (fserror.c):
// __kml_fs_errmsg assembles Node's exact message, __kml_fs_error_new builds
// the Error object for an errno, __kml_fs_throw2/__kml_fs_throw throw it for
// the current errno through @__kml_throw (the same "let a real OS-level
// failure surface as a catchable Error" approach as ADR-00021). `opdesc`
// stays in the ABI (every call site passes its verb) but no longer appears
// in the message. The error's name is compared by address, so the program
// hands its interned "Error" to the C through hooks.c.
func (e *Emitter) ensureFsThrow() {
	if e.usedFsThrow {
		return
	}
	e.usedFsThrow = true
	e.ensureStrHeaderRuntime() // error .message must be headered for concat/=== (TDD-00120)
	e.ensureExceptionHelpers()
	// The error's name, which fserror.c reads from hooks.c.
	e.addHookInit(fmt.Sprintf("  store "+irPtr+" %s, "+irPtr+" @__kml_error_name, align 8\n", e.internString("Error")))
}
