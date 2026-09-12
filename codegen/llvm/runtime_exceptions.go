package llvm

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
	// TDD-00202: the thrown value is an unpacked (tag, payload) pair — the logical
	// kmlTag* value plus its payload (a NaN-box payload for primitives; a
	// ptrtoint'd errorObjType pointer for tag kmlTagError=13). `@__kml_thrown`
	// (the ptr above) stays set to the Error object when tag==13, so the
	// internal Error-only catch paths (async/fs) and the uncaught printer keep
	// reading it directly; it is null for a non-Error throw.
	e.emitGlobal(`@__kml_thrown_tag = internal thread_local global i8 13, align 1`)
	e.emitGlobal(`@__kml_thrown_pay = internal thread_local global i64 0, align 8`)
	// align 16: Win64 _setjmp saves XMM registers with aligned stores, so every
	// 512-byte slot (a multiple of 16) must start 16-aligned; 8 faults there.
	e.emitGlobal(`@__kml_jmp_stk = internal thread_local global [64 x [64 x i64]] zeroinitializer, align 16`)
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
	if targetGOOS() == "windows" {
		e.emitGlobal(`declare i32 @_setjmp(ptr, ptr) returns_twice`)
	} else {
		e.emitGlobal(`declare i32 @setjmp(ptr) returns_twice`)
	}
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
	// The unpacked thrown record accessors (TDD-00202): the catch binding loads
	// the tag + payload to reconstruct the caught value.
	e.emitGlobal(`define i8 @__kml_get_thrown_tag() {
  %t = load i8, ptr @__kml_thrown_tag, align 1
  ret i8 %t
}`)
	e.emitGlobal(`define i64 @__kml_get_thrown_pay() {
  %p = load i64, ptr @__kml_thrown_pay, align 8
  ret i64 %p
}`)

	// __kml_throw(ptr errObj) is the Error-object shim kept for the ~41 internal
	// throw sites (assert/fs/encoding/...) and every `throw new Error(...)`:
	// record it as a kmlTagError (13) value and delegate to the core.
	e.emitGlobal(`define void @__kml_throw(ptr %errObj) {
entry:
  %pay = ptrtoint ptr %errObj to i64
  call void @__kml_throw_any(i8 13, i64 %pay)
  unreachable
}`)

	e.emitGlobal(`@.kml_unc_thrown = private unnamed_addr constant [16 x i8] c"[thrown value]\0A\00", align 1`)
	// __kml_caught_unc_msg renders the message an uncaught throw prints: an Error
	// yields its .message; a string is itself; a number is dtoa'd; booleans /
	// null / undefined their literals; any other value a generic placeholder
	// (object stringification at the top level is a minor edge). The returned
	// pointer is NUL-terminated (printed with the "Uncaught: %s" format).
	e.emitGlobal(`@.kml_s_true = private unnamed_addr constant [5 x i8] c"true\00", align 1`)
	e.emitGlobal(`@.kml_s_false = private unnamed_addr constant [6 x i8] c"false\00", align 1`)
	e.emitGlobal(`@.kml_s_null = private unnamed_addr constant [5 x i8] c"null\00", align 1`)
	e.emitGlobal(`@.kml_s_undef = private unnamed_addr constant [10 x i8] c"undefined\00", align 1`)
	e.ensureDtoa()
	e.ensureMalloc()
	e.emitGlobal(`define ptr @__kml_caught_unc_msg(i8 %tag, i64 %pay) {
entry:
  switch i8 %tag, label %other [
    i8 13, label %err
    i8 2, label %str
    i8 1, label %num
    i8 3, label %bool
    i8 4, label %null
    i8 5, label %undef
  ]
err:
  %eo = inttoptr i64 %pay to ptr
  %mp = getelementptr { i64, ptr, ptr }, ptr %eo, i32 0, i32 1
  %m = load ptr, ptr %mp, align 8
  ret ptr %m
str:
  %sp = inttoptr i64 %pay to ptr
  ret ptr %sp
num:
  %d = bitcast i64 %pay to double
  %buf = call ptr @malloc(i64 32)
  call void @__kml_dtoa(ptr %buf, double %d)
  ret ptr %buf
bool:
  %bt = icmp ne i64 %pay, 0
  %bs = select i1 %bt, ptr @.kml_s_true, ptr @.kml_s_false
  ret ptr %bs
null:
  ret ptr @.kml_s_null
undef:
  ret ptr @.kml_s_undef
other:
  ret ptr @.kml_unc_thrown
}`)

	e.emitGlobal(`define void @__kml_throw_any(i8 %tag, i64 %pay) {
entry:
  store i8 %tag, ptr @__kml_thrown_tag, align 1
  store i64 %pay, ptr @__kml_thrown_pay, align 8
  ; Keep @__kml_thrown pointing at the Error object for tag 13 (internal
  ; Error-only catch paths + the uncaught printer read it); null otherwise.
  %isErr = icmp eq i8 %tag, 13
  %errObj = inttoptr i64 %pay to ptr
  %thrownPtr = select i1 %isErr, ptr %errObj, ptr null
  store ptr %thrownPtr, ptr @__kml_thrown, align 8
  %top = load i32, ptr @__kml_jmp_top, align 4
  %iszero = icmp eq i32 %top, 0
  br i1 %iszero, label %uncaught, label %jump
uncaught:
  ; process.on('uncaughtException', ...) hook (passed the Error object, or null
  ; for a non-Error throw): if a listener runs it returns 1 and we skip the
  ; default print — but still exit (the stack has unwound to the top-level
  ; catch-all). Runs the 'exit' listener on the way out, like Node.
  %prochandled = call i1 @__kml_process_uncaught(ptr %thrownPtr)
  br i1 %prochandled, label %procunc, label %defunc
procunc:
  call void @__kml_run_exit_handlers(i64 1)
  call void @exit(i32 1)
  unreachable
defunc:
  %msg = call ptr @__kml_caught_unc_msg(i8 %tag, i64 %pay)
  ; TDD-00098 stage 5: on a worker thread this call does NOT return — it
  ; reports 'error' + exit(1) to the parent and ends only that thread.
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

// setjmpCall returns the IR call that saves a catch frame into buf. On
// mingw-w64 x86-64, C's setjmp is a macro for _setjmp(buf, NULL): the second
// argument is the frame pointer longjmp will SEH-unwind to, and NULL tells
// it to skip unwinding entirely. Calling a bare one-argument setjmp leaves
// that register holding garbage, and the matching longjmp faults with
// STATUS_BAD_STACK (0xC0000028) — every throw/catch test on Windows
// (TDD-00177 Stage 0). Elsewhere it is the libc symbol as before.
func setjmpCall(buf string) string {
	if targetGOOS() == "windows" {
		return "call i32 @_setjmp(ptr " + buf + ", ptr null)"
	}
	return "call i32 @setjmp(ptr " + buf + ")"
}
