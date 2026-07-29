package llvm

import ()

func (e *Emitter) ensureExceptionHelpers() {
	if e.usedExceptionHelpers {
		return
	}
	e.usedExceptionHelpers = true
	e.ensurePrintf()
	e.ensureMalloc()

	e.emitGlobal(`@__kml_thrown  = internal global ptr null, align 8`)
	e.emitGlobal(`@__kml_jmp_stk = internal global [64 x [64 x i64]] zeroinitializer, align 8`)
	e.emitGlobal(`@__kml_jmp_top = internal global i32 0, align 4`)
	e.emitGlobal(`@.kml_unc_fmt  = private unnamed_addr constant [14 x i8] c"Uncaught: %s\0A\00", align 1`)
	e.emitGlobal(`declare i32 @setjmp(ptr) returns_twice`)
	e.emitGlobal(`declare void @longjmp(ptr, i32) noreturn`)
	e.ensureExit()

	e.emitGlobal(`define ptr @__kml_push_jmpbuf() {
  %top = load i32, ptr @__kml_jmp_top, align 4
  %slot = getelementptr [64 x [64 x i64]], ptr @__kml_jmp_stk, i32 0, i32 %top
  %newtop = add i32 %top, 1
  store i32 %newtop, ptr @__kml_jmp_top, align 4
  ret ptr %slot
}`)

	e.emitGlobal(`define void @__kml_pop_jmpbuf() {
  %top = load i32, ptr @__kml_jmp_top, align 4
  %newtop = sub i32 %top, 1
  store i32 %newtop, ptr @__kml_jmp_top, align 4
  ret void
}`)

	e.emitGlobal(`define ptr @__kml_get_thrown() {
  %v = load ptr, ptr @__kml_thrown, align 8
  ret ptr %v
}`)

	e.emitGlobal(`define void @__kml_throw(ptr %errObj) {
entry:
  store ptr %errObj, ptr @__kml_thrown, align 8
  %top = load i32, ptr @__kml_jmp_top, align 4
  %iszero = icmp eq i32 %top, 0
  br i1 %iszero, label %uncaught, label %jump
uncaught:
  %msgPtr = getelementptr { ptr }, ptr %errObj, i32 0, i32 0
  %msg = load ptr, ptr %msgPtr, align 8
  call i32 (ptr, ...) @printf(ptr @.kml_unc_fmt, ptr %msg)
  call void @exit(i32 1)
  unreachable
jump:
  %newtop = sub i32 %top, 1
  store i32 %newtop, ptr @__kml_jmp_top, align 4
  %slot = getelementptr [64 x [64 x i64]], ptr @__kml_jmp_stk, i32 0, i32 %newtop
  call void @longjmp(ptr %slot, i32 1)
  unreachable
}`)
}
