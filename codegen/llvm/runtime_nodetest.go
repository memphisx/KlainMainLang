// runtime_nodetest.go — the `node:test` runner's bookkeeping runtime
// (TDD-00140): pass/fail/skip counters, TAP-shaped per-test lines, the
// module-level after-hook list, and the exit-sink summary that fails the
// process when any test failed. Composes with the mustCall verifier
// (runtime_testmod.go) in the same exit sink.
package llvm

func (e *Emitter) ensureNodeTestRuntime() {
	if e.usedNodeTestRuntime {
		return
	}
	e.usedNodeTestRuntime = true
	e.usedProcessLifecycle = true
	e.ensurePrintf()
	e.ensureExit()
	e.ensureMalloc()
	e.ensureRealloc()
	e.ensureExceptionHelpers()

	e.emitGlobal(`@__kml_ntest_pass_n = internal global i64 0, align 8`)
	e.emitGlobal(`@__kml_ntest_fail_n = internal global i64 0, align 8`)
	e.emitGlobal(`@__kml_ntest_skip_n = internal global i64 0, align 8`)
	// beforeEach/afterEach: one closure slot each (last registration wins, V1).
	e.emitGlobal(`@__kml_ntest_beforeEach = internal global ptr null, align 8`)
	e.emitGlobal(`@__kml_ntest_afterEach = internal global ptr null, align 8`)
	// module-level after() hooks: a growable closure list.
	e.emitGlobal(`@__kml_ntest_afters = internal global ptr null, align 8`)
	e.emitGlobal(`@__kml_ntest_afters_n = internal global i64 0, align 8`)
	e.emitGlobal(`@__kml_ntest_afters_cap = internal global i64 0, align 8`)

	e.emitGlobal(llvmCStrConst("@.kml_ntest_ok", "ok - %s"))
	e.emitGlobal(llvmCStrConst("@.kml_ntest_fail", "not ok - %s: %s"))
	e.emitGlobal(llvmCStrConst("@.kml_ntest_skip", "ok - %s # SKIP"))
	e.emitGlobal(llvmCStrConst("@.kml_ntest_todo", "ok - %s # TODO"))
	e.emitGlobal(llvmCStrConst("@.kml_ntest_sum", "tests %lld, pass %lld, fail %lld, skip %lld"))

	e.emitGlobal(`
define void @__kml_ntest_pass(ptr %name) {
entry:
  %n = load i64, ptr @__kml_ntest_pass_n, align 8
  %n2 = add i64 %n, 1
  store i64 %n2, ptr @__kml_ntest_pass_n, align 8
  %r = call i32 (ptr, ...) @printf(ptr @.kml_ntest_ok, ptr %name)
  ret void
}

define void @__kml_ntest_fail(ptr %name, ptr %msg) {
entry:
  %n = load i64, ptr @__kml_ntest_fail_n, align 8
  %n2 = add i64 %n, 1
  store i64 %n2, ptr @__kml_ntest_fail_n, align 8
  %r = call i32 (ptr, ...) @printf(ptr @.kml_ntest_fail, ptr %name, ptr %msg)
  ret void
}

define void @__kml_ntest_skipped(ptr %name, i1 %todo) {
entry:
  %n = load i64, ptr @__kml_ntest_skip_n, align 8
  %n2 = add i64 %n, 1
  store i64 %n2, ptr @__kml_ntest_skip_n, align 8
  br i1 %todo, label %t, label %s
t:
  %r1 = call i32 (ptr, ...) @printf(ptr @.kml_ntest_todo, ptr %name)
  ret void
s:
  %r2 = call i32 (ptr, ...) @printf(ptr @.kml_ntest_skip, ptr %name)
  ret void
}

; append a {fnptr, envptr} closure header to a growable list rooted at the
; three globals passed by address (shared by the module after()-list and each
; TestContext's own list).
define void @__kml_ntest_list_push(ptr %rootp, ptr %np, ptr %capp, ptr %closure) {
entry:
  %n = load i64, ptr %np, align 8
  %cap = load i64, ptr %capp, align 8
  %full = icmp sge i64 %n, %cap
  br i1 %full, label %grow, label %store
grow:
  %cap2 = mul i64 %cap, 2
  %ge4 = icmp sgt i64 %cap2, 4
  %newcap = select i1 %ge4, i64 %cap2, i64 4
  %old = load ptr, ptr %rootp, align 8
  %bytes = mul i64 %newcap, 8
  %new = call ptr @realloc(ptr %old, i64 %bytes)
  store ptr %new, ptr %rootp, align 8
  store i64 %newcap, ptr %capp, align 8
  br label %store
store:
  %data = load ptr, ptr %rootp, align 8
  %slot = getelementptr ptr, ptr %data, i64 %n
  store ptr %closure, ptr %slot, align 8
  %n2 = add i64 %n, 1
  store i64 %n2, ptr %np, align 8
  ret void
}

; run every closure in a list (zero-arg call), clearing the count.
define void @__kml_ntest_list_run(ptr %rootp, ptr %np) {
entry:
  %n = load i64, ptr %np, align 8
  %data = load ptr, ptr %rootp, align 8
  br label %loop
loop:
  %i = phi i64 [ 0, %entry ], [ %inext, %cont ]
  %done = icmp sge i64 %i, %n
  br i1 %done, label %ret, label %body
body:
  %slot = getelementptr ptr, ptr %data, i64 %i
  %c = load ptr, ptr %slot, align 8
  %fpp = getelementptr { ptr, ptr }, ptr %c, i32 0, i32 0
  %fp = load ptr, ptr %fpp, align 8
  %epp = getelementptr { ptr, ptr }, ptr %c, i32 0, i32 1
  %ep = load ptr, ptr %epp, align 8
  call void %fp(ptr %ep)
  br label %cont
cont:
  %inext = add i64 %i, 1
  br label %loop
ret:
  store i64 0, ptr %np, align 8
  ret void
}

; guarded zero-arg call through a single-closure slot (beforeEach/afterEach).
define void @__kml_ntest_call_slot(ptr %slotp) {
entry:
  %c = load ptr, ptr %slotp, align 8
  %isnull = icmp eq ptr %c, null
  br i1 %isnull, label %ret, label %call
call:
  %fpp = getelementptr { ptr, ptr }, ptr %c, i32 0, i32 0
  %fp = load ptr, ptr %fpp, align 8
  %epp = getelementptr { ptr, ptr }, ptr %c, i32 0, i32 1
  %ep = load ptr, ptr %epp, align 8
  call void %fp(ptr %ep)
  br label %ret
ret:
  ret void
}

; exit-sink summary: run module after() hooks, print totals, and force a
; non-zero exit when any test failed (mirrors __kml_test_verify's posture).
define void @__kml_ntest_summary() {
entry:
  call void @__kml_ntest_list_run(ptr @__kml_ntest_afters, ptr @__kml_ntest_afters_n)
  %p = load i64, ptr @__kml_ntest_pass_n, align 8
  %f = load i64, ptr @__kml_ntest_fail_n, align 8
  %s = load i64, ptr @__kml_ntest_skip_n, align 8
  %pf = add i64 %p, %f
  %tot = add i64 %pf, %s
  %any = icmp sgt i64 %tot, 0
  br i1 %any, label %report, label %ret
report:
  %r = call i32 (ptr, ...) @printf(ptr @.kml_ntest_sum, i64 %tot, i64 %p, i64 %f, i64 %s)
  %bad = icmp sgt i64 %f, 0
  br i1 %bad, label %failexit, label %ret
failexit:
  call void @exit(i32 1)
  unreachable
ret:
  ret void
}`)
}
