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

// UsesErrno reports whether the program links the errno tables.
func (e *Emitter) UsesErrno() bool { return e.usedErrnoCode || e.usedErrnoDesc }

// UsesFsError reports whether the program links the fs error builder.
func (e *Emitter) UsesFsError() bool { return e.usedFsThrow }

// ensureErrnoCode declares __kml_errno_code(i32 errno) -> ptr: the Node
// error-code NAME string (`ENOENT`, `EISDIR`, ...) that `err.code` exposes, or
// null for an unmapped value; and __kml_uv_errno(i32) -> i32, the value Node
// reports as `err.errno`.
func (e *Emitter) ensureErrnoCode() {
	if e.usedErrnoCode {
		return
	}
	e.usedErrnoCode = true
	e.emitGlobal(`declare ptr @__kml_errno_code(i32)
declare i32 @__kml_uv_errno(i32)`)
}

// ensureErrnoDesc declares __kml_errno_desc(i32 errno) -> ptr: the libuv
// canonical description Node's fs `.message` embeds ("no such file or
// directory", ...), or null when the code isn't in the table (the caller then
// falls back to strerror) (ADR-01000).
func (e *Emitter) ensureErrnoDesc() {
	if e.usedErrnoDesc {
		return
	}
	e.usedErrnoDesc = true
	e.emitGlobal("declare ptr @__kml_errno_desc(i32)")
}

// ensureFsThrow declares the fs error builders (fserror.c):
// __kml_fs_errmsg assembles Node's exact message, __kml_fs_error_new builds
// the Error object for an errno, __kml_fs_throw2/__kml_fs_throw throw it for
// the current errno through @__kml_throw (the same "let a real OS-level
// failure surface as a catchable Error" approach as ADR-00021). `opdesc`
// stays in the ABI (every call site passes its verb) but no longer appears
// in the message. __kml_fs_error_new is a thin IR wrapper: the error's name
// is one symbol per constructor name, compared by address, which only the
// emitter's string table can name.
func (e *Emitter) ensureFsThrow() {
	if e.usedFsThrow {
		return
	}
	e.usedFsThrow = true
	e.ensureStrHeaderRuntime() // error .message must be headered for concat/=== (TDD-00120)
	e.ensureExceptionHelpers()
	e.ensureErrnoCode()
	e.ensureErrnoDesc()
	e.emitGlobal(`declare ptr @__kml_fs_errmsg(ptr, ptr, ptr, ptr, ptr)
declare ptr @__kml_fs_error_new_named(i32, ptr, ptr, ptr, ptr)
declare void @__kml_fs_throw2(ptr, ptr, ptr, ptr)
declare void @__kml_fs_throw(ptr, ptr, ptr)`)
	e.emitGlobal(fmt.Sprintf(`define ptr @__kml_fs_error_new(i32 %%errno_val, ptr %%syscall, ptr %%path, ptr %%dest) {
entry:
  %%e = call ptr @__kml_fs_error_new_named(i32 %%errno_val, ptr %%syscall, ptr %%path, ptr %%dest, ptr %s)
  ret ptr %%e
}`, e.internString("Error")))
}
