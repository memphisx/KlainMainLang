// runtime_test.go — the `test` builtin's exit-time verification runtime
// (TDD-00122). mustCall/mustCallAtLeast/mustNotCall each allocate an i64 counter
// and register a {counterPtr, min, max, msgPtr} expectation here; the wrapper
// closure (emit_test.go) bumps the counter on every invocation. At process exit
// __kml_test_verify walks the registry and, on the first unmet expectation,
// prints a diagnostic and exits non-zero. The verifier is invoked from the
// existing process exit sink (runtime_process.go's __kml_run_exit_handlers),
// after any user process.on('exit') handler.
package llvm

import (
	"fmt"
	"strings"
)

// llvmCStrConst renders a `<sym> = private constant [N x i8] c"..."` global for a
// printf format string, appending a trailing newline + NUL and computing N (and
// the \XX escaping) from the actual bytes — so the length is never hand-counted.
func llvmCStrConst(sym, text string) string {
	raw := text + "\n\x00"
	var b strings.Builder
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if c == '\\' || c == '"' || c < 0x20 || c >= 0x7f {
			b.WriteString(fmt.Sprintf("\\%02X", c))
		} else {
			b.WriteByte(c)
		}
	}
	return fmt.Sprintf("%s = private unnamed_addr constant [%d x i8] c\"%s\", align 1", sym, len(raw), b.String())
}

// ensureTestRuntime emits (once) the expectation registry, __kml_test_register,
// and __kml_test_verify. Sets usedProcessLifecycle so the exit sink exists and
// calls the verifier.
func (e *Emitter) ensureTestRuntime() {
	if e.usedTestRuntime {
		return
	}
	e.usedTestRuntime = true
	e.usedProcessLifecycle = true
	e.ensurePrintf()
	e.ensureExit()

	// A fixed-capacity registry — 4096 expectations is far beyond any real test
	// file; an overflow silently drops (bounded, never a crash). Each entry is
	// {counterPtr, min, max, msgPtr}; max == -1 means "no upper bound"
	// (mustCallAtLeast).
	e.emitGlobal(`@__kml_test_reg = internal global [4096 x { ptr, i64, i64, ptr }] zeroinitializer, align 8`)
	e.emitGlobal(`@__kml_test_n = internal global i64 0, align 8`)
	e.emitGlobal(llvmCStrConst("@.kml_test_fail", "test: %s called %lld time(s), expected %lld..%lld"))
	e.emitGlobal(llvmCStrConst("@.kml_test_atleast", "test: %s called %lld time(s), expected >=%lld"))

	e.emitGlobal(`
define void @__kml_test_register(ptr %cp, i64 %min, i64 %max, ptr %msg) {
entry:
  %n = load i64, ptr @__kml_test_n, align 8
  %full = icmp sge i64 %n, 4096
  br i1 %full, label %done, label %store
store:
  %e0 = getelementptr [4096 x { ptr, i64, i64, ptr }], ptr @__kml_test_reg, i64 0, i64 %n, i32 0
  store ptr %cp, ptr %e0, align 8
  %e1 = getelementptr [4096 x { ptr, i64, i64, ptr }], ptr @__kml_test_reg, i64 0, i64 %n, i32 1
  store i64 %min, ptr %e1, align 8
  %e2 = getelementptr [4096 x { ptr, i64, i64, ptr }], ptr @__kml_test_reg, i64 0, i64 %n, i32 2
  store i64 %max, ptr %e2, align 8
  %e3 = getelementptr [4096 x { ptr, i64, i64, ptr }], ptr @__kml_test_reg, i64 0, i64 %n, i32 3
  store ptr %msg, ptr %e3, align 8
  %n1 = add i64 %n, 1
  store i64 %n1, ptr @__kml_test_n, align 8
  br label %done
done:
  ret void
}`)

	e.emitGlobal(`
define void @__kml_test_verify() {
entry:
  %n = load i64, ptr @__kml_test_n, align 8
  br label %loop
loop:
  %i = phi i64 [ 0, %entry ], [ %inext, %cont ]
  %atend = icmp sge i64 %i, %n
  br i1 %atend, label %ret, label %body
body:
  %cpp = getelementptr [4096 x { ptr, i64, i64, ptr }], ptr @__kml_test_reg, i64 0, i64 %i, i32 0
  %cp = load ptr, ptr %cpp, align 8
  %count = load i64, ptr %cp, align 8
  %minp = getelementptr [4096 x { ptr, i64, i64, ptr }], ptr @__kml_test_reg, i64 0, i64 %i, i32 1
  %min = load i64, ptr %minp, align 8
  %maxp = getelementptr [4096 x { ptr, i64, i64, ptr }], ptr @__kml_test_reg, i64 0, i64 %i, i32 2
  %max = load i64, ptr %maxp, align 8
  %msgp = getelementptr [4096 x { ptr, i64, i64, ptr }], ptr @__kml_test_reg, i64 0, i64 %i, i32 3
  %msg = load ptr, ptr %msgp, align 8
  %tooFew = icmp slt i64 %count, %min
  %hasMax = icmp sge i64 %max, 0
  %overMax = icmp sgt i64 %count, %max
  %tooMany = and i1 %hasMax, %overMax
  %bad = or i1 %tooFew, %tooMany
  br i1 %bad, label %fail, label %cont
fail:
  br i1 %hasMax, label %failbounded, label %failatleast
failbounded:
  call i32 (ptr, ...) @printf(ptr @.kml_test_fail, ptr %msg, i64 %count, i64 %min, i64 %max)
  br label %doexit
failatleast:
  call i32 (ptr, ...) @printf(ptr @.kml_test_atleast, ptr %msg, i64 %count, i64 %min)
  br label %doexit
doexit:
  call void @exit(i32 1)
  unreachable
cont:
  %inext = add i64 %i, 1
  br label %loop
ret:
  ret void
}`)
}
