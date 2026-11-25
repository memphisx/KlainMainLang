package llvm

import (
	_ "embed"
	"fmt"
)

//go:embed jmpstacksrc/jmpstack.c
var jmpStackSource string

// JmpStackSource is the setjmp-buffer stack runtime's C source.
func JmpStackSource() string { return layoutHeader() + jmpStackSource }

//go:embed exceptionssrc/exceptions.c
var exceptionsSource string

// ExceptionsSource is the thrown-value and throw-path runtime's C source,
// behind kml_layout.h.
func ExceptionsSource() string { return layoutHeader() + exceptionsSource }

// ExceptionsCFlags is exceptions.c's mode define: on a program with Workers
// the uncaught path ends the thread (@__kml_thread_exit), not the process.
func (e *Emitter) ExceptionsCFlags() []string {
	flags := []string{
		fmt.Sprintf("-DKML_KIND_TYPEERR=%dLL", errorTypeIDStored(errorKindIDs["TypeError"])),
		fmt.Sprintf("-DKML_KIND_RANGEERR=%dLL", errorTypeIDStored(errorKindIDs["RangeError"])),
		fmt.Sprintf("-DKML_NB_UNDEFINED=%dLL", nbUndefined),
	}
	return flags
}

// setjmpCall returns the IR call that saves a catch frame into buf. On
// mingw-w64 x86-64, C's setjmp is a macro for _setjmp(buf, NULL): the second
// argument is the frame pointer longjmp will SEH-unwind to, and NULL tells
// it to skip unwinding entirely. Calling a bare one-argument setjmp leaves
// that register holding garbage, and the matching longjmp faults with
// STATUS_BAD_STACK (0xC0000028) — every throw/catch test on Windows
// (TDD-00177 Stage 0). Elsewhere it is the libc symbol as before.
func (e *Emitter) setjmpCall(buf string) string {
	if e.opts.Target.OS() == "windows" {
		return "call i32 @_setjmp(" + irPtr + " " + buf + ", " + irPtr + " null)"
	}
	return "call i32 @setjmp(" + irPtr + " " + buf + ")"
}
