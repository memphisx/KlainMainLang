package llvm

import _ "embed"

//go:embed jmpstacksrc/jmpstack.c
var jmpStackSource string

// JmpStackSource is the setjmp-buffer stack runtime's C source.
func JmpStackSource() string { return layoutHeader() + jmpStackSource }

//go:embed exceptionssrc/exceptions.c
var exceptionsSource string

// ExceptionsSource is the thrown-value and throw-path runtime's C source,
// behind kml_layout.h.
func ExceptionsSource() string { return layoutHeader() + exceptionsSource }

// UsesExceptions reports whether the program links the throw-path runtime.
func (e *Emitter) UsesExceptions() bool { return e.usedExceptionHelpers }

// ExceptionsCFlags is exceptions.c's mode define: on a program with Workers
// the uncaught path ends the thread (@__kml_thread_exit), not the process.
func (e *Emitter) ExceptionsCFlags() []string {
	if e.hasWorkers {
		return []string{"-DKML_WORKERS=1"}
	}
	return nil
}

// ensureExceptionHelpers declares the throw path (exceptionssrc/exceptions.c:
// the thrown value, @__kml_throw/@__kml_throw_any, the caught-tag and
// uncaught-message helpers) once, plus the setjmp/longjmp the try frames use.
func (e *Emitter) ensureExceptionHelpers() {
	if e.usedExceptionHelpers {
		return
	}
	e.usedExceptionHelpers = true
	e.ensurePrintf()
	e.ensureMalloc()

	// The setjmp buffers try frames unwind through (jmpstacksrc/jmpstack.c,
	// TDD-00240): __kml_cur_jmp_stk is the current stack (null: the thread's
	// own), swapped by each coroutine task and generator with its own so two
	// suspended bodies never overwrite each other's longjmp targets
	// (TDD-00083 Stage 2); __kml_jmp_top is its depth.
	e.usedJmpStack = true
	e.emitGlobal(`@__kml_cur_jmp_stk = external thread_local global ptr, align 8
@__kml_jmp_top = external thread_local global i32, align 4
declare ptr @__kml_push_jmpbuf()
declare void @__kml_pop_jmpbuf()
declare ptr @__kml_jmp_unwind_slot()
declare ptr @__kml_jmp_stack_new()
declare void @__kml_jmp_stack_free(ptr)`)
	// The uncaught-error line (emit_unhandled.go prints it too).
	e.emitGlobal(`@.kml_unc_fmt  = private unnamed_addr constant [14 x i8] c"Uncaught: %s\0A\00", align 1`)
	if e.opts.Target.OS() == "windows" {
		e.emitGlobal(`declare i32 @_setjmp(ptr, ptr) returns_twice`)
	} else {
		e.emitGlobal(`declare i32 @setjmp(ptr) returns_twice`)
	}
	e.emitGlobal(`declare void @longjmp(ptr, i32) noreturn`)
	e.ensureExit()
	e.ensureDtoa() // the uncaught printer dtoa's a thrown number

	// The unpacked thrown record (TDD-00202): the catch binding loads the tag
	// and payload to reconstruct the caught value.
	e.emitGlobal(`declare ptr @__kml_get_thrown()
declare i8 @__kml_get_thrown_tag()
declare i64 @__kml_get_thrown_pay()
declare void @__kml_throw(ptr)
declare void @__kml_throw_any(i8, i64)
declare i8 @__kml_caught_tag(i8, i64)
declare ptr @__kml_caught_unc_msg(i8, i64)`)
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
		return "call i32 @_setjmp(ptr " + buf + ", ptr null)"
	}
	return "call i32 @setjmp(ptr " + buf + ")"
}
