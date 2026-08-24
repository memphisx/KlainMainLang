package llvm

import ()

// ensureExceptionHelpers hand-writes @__kml_throw's uncaught-error path
// against errorObjType's layout directly ({ i64 kind, ptr message, ptr name
// } — emit_exceptions.go) rather than through the generic FieldIndex/
// StructIR machinery, since this is raw IR text, not codegen output. If
// errorObjType's field order or count ever changes, the `getelementptr { i64,
// ptr, ptr }, ..., i32 0, i32 1` below must be updated to match, or the
// uncaught-exception printer silently prints garbage instead of the message.
func (e *Emitter) ensureExceptionHelpers() {
	if e.usedExceptionHelpers {
		return
	}
	e.usedExceptionHelpers = true
	e.ensurePrintf()
	e.ensureMalloc()

	e.emitGlobal(`@__kml_thrown  = internal thread_local global ptr null, align 8`)
	e.emitGlobal(`@__kml_jmp_stk = internal thread_local global [64 x [64 x i64]] zeroinitializer, align 8`)
	e.emitGlobal(`@__kml_jmp_top = internal thread_local global i32 0, align 4`)
	// The jmpbuf stack is indirected through @__kml_cur_jmp_stk (default: the
	// thread's own @__kml_jmp_stk) so each coroutine task can swap in its own
	// stack — otherwise two suspended tasks' catch frames would overwrite each
	// other's longjmp targets (TDD-00083 Stage 2, fiber-safe exceptions). Each
	// slot is 64*8 = 512 bytes; push/throw byte-index so a task stack can be
	// smaller. null is the "default stack" sentinel: a thread_local initializer
	// can't take another thread_local's address (it would bake in the TLS
	// template address, not the per-thread one), so the two consumers below
	// resolve null to @__kml_jmp_stk at load time instead.
	e.emitGlobal(`@__kml_cur_jmp_stk = internal thread_local global ptr null, align 8`)
	e.emitGlobal(`@.kml_unc_fmt  = private unnamed_addr constant [14 x i8] c"Uncaught: %s\0A\00", align 1`)
	e.emitGlobal(`declare i32 @setjmp(ptr) returns_twice`)
	e.emitGlobal(`declare void @longjmp(ptr, i32) noreturn`)
	e.ensureExit()

	e.emitGlobal(`define ptr @__kml_push_jmpbuf() {
  %stk0 = load ptr, ptr @__kml_cur_jmp_stk, align 8
  %usedef = icmp eq ptr %stk0, null
  %stk = select i1 %usedef, ptr @__kml_jmp_stk, ptr %stk0
  %top = load i32, ptr @__kml_jmp_top, align 4
  %off = mul i32 %top, 512
  %off64 = zext i32 %off to i64
  %slot = getelementptr i8, ptr %stk, i64 %off64
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
  ; process.on('uncaughtException', ...) hook: if a listener is registered it
  ; runs and this returns 1, and we skip the default print — but still exit
  ; (this exception model has unwound the stack to the top-level catch-all, so
  ; there is no in-progress computation to resume). Runs the 'exit' listener
  ; on the way out, like Node. Returns 0 (no listener) → default behavior.
  %prochandled = call i1 @__kml_process_uncaught(ptr %errObj)
  br i1 %prochandled, label %procunc, label %defunc
procunc:
  call void @__kml_run_exit_handlers(i64 1)
  call void @exit(i32 1)
  unreachable
defunc:
  %msgPtr = getelementptr { i64, ptr, ptr }, ptr %errObj, i32 0, i32 1
  %msg = load ptr, ptr %msgPtr, align 8
  ; TDD-00098 stage 5: on a worker thread this call does NOT return — it
  ; reports 'error' + exit(1) to the parent and ends only that thread. On
  ; the main thread (or without workers, via the no-op stub) it returns and
  ; the process-fatal path below runs as always.
  call void @__kml_worker_uncaught(ptr %msg)
  call i32 (ptr, ...) @printf(ptr @.kml_unc_fmt, ptr %msg)
  call void @exit(i32 1)
  unreachable
jump:
  %newtop = sub i32 %top, 1
  store i32 %newtop, ptr @__kml_jmp_top, align 4
  %stk0 = load ptr, ptr @__kml_cur_jmp_stk, align 8
  %usedef = icmp eq ptr %stk0, null
  %stk = select i1 %usedef, ptr @__kml_jmp_stk, ptr %stk0
  %off = mul i32 %newtop, 512
  %off64 = zext i32 %off to i64
  %slot = getelementptr i8, ptr %stk, i64 %off64
  call void @longjmp(ptr %slot, i32 1)
  unreachable
}`)
}
