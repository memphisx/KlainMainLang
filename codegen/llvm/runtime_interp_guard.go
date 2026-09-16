package llvm

import "fmt"

// ensureNodeInterpFlagGuard emits @__kml_reject_node_interp_flags, a startup
// guard that a compiled binary runs before its program body when the program
// reads process.execPath (see ensureExecPath).
//
// Why this exists: `process.execPath` in a compiled program is the compiled
// binary itself, not a separate Node runtime. A common Node-test idiom re-runs
// the interpreter to evaluate an expression — `spawnSync(process.execPath,
// ['-p', 'http.maxHeaderSize'])` — which under Node launches a fresh `node` in
// print-eval mode (a *different* execution) and exits. A compiled binary has no
// interpreter mode: handed `-p`, it silently ignores the flag and re-runs its
// own program body, which re-hits the same spawn — an unbounded self-fork chain
// (each parent blocked in poll() awaiting a child that never terminates). That
// fork bomb once exhausted the machine's memory during a conformance run.
//
// A compiled binary genuinely cannot act as the Node interpreter (evaluating an
// arbitrary runtime source string is out of scope, the same limit as eval/vm).
// So the faithful thing — and what Node itself does with a flag it rejects — is
// to exit nonzero with a diagnostic instead of pretending the invocation is a
// normal run. That breaks the recursion at the first spawned child and turns
// such tests into honest failures rather than a fork bomb.
//
// The flag set is limited to the flags that unambiguously request interpreter /
// eval mode, to minimize collision with a compiled CLI tool's own arguments.
func (e *Emitter) ensureNodeInterpFlagGuard() {
	if e.usedNodeInterpGuard {
		return
	}
	e.usedNodeInterpGuard = true
	e.ensureStrcmp()
	e.ensureExit()
	e.ensureWriteDecl()

	flags := []struct{ sym, text string }{
		{"@.kml_ni_p", "-p"},
		{"@.kml_ni_print", "--print"},
		{"@.kml_ni_e", "-e"},
		{"@.kml_ni_eval", "--eval"},
		{"@.kml_ni_i", "-i"},
		{"@.kml_ni_intr", "--interactive"},
	}
	for _, f := range flags {
		e.emitGlobal(fmt.Sprintf(`%s = private unnamed_addr constant [%d x i8] c"%s\00"`,
			f.sym, len(f.text)+1, f.text))
	}

	msg := "error: Node interpreter flags (-p/-e/--eval/-i) are not supported by this compiled binary\n"
	msgEsc, msgSize := escapeLLVM(msg) // msgSize counts the trailing NUL
	msgLen := msgSize - 1              // bytes to write to stderr (message without NUL)
	e.emitGlobal(fmt.Sprintf(`@.kml_ni_msg = private unnamed_addr constant [%d x i8] c"%s"`,
		msgSize, msgEsc))

	var b []byte
	w := func(format string, a ...any) { b = append(b, []byte(fmt.Sprintf(format, a...))...) }
	w("\ndefine void @__kml_reject_node_interp_flags(i32 %%argc, ptr %%argv) {\n")
	w("entry:\n  br label %%head\n")
	w("head:\n")
	w("  %%i = phi i32 [1, %%entry], [%%inext, %%cont]\n")
	w("  %%more = icmp slt i32 %%i, %%argc\n")
	w("  br i1 %%more, label %%body, label %%ret\n")
	w("body:\n")
	w("  %%slot = getelementptr ptr, ptr %%argv, i32 %%i\n")
	w("  %%arg = load ptr, ptr %%slot, align 8\n")
	w("  %%argnull = icmp eq ptr %%arg, null\n")
	w("  br i1 %%argnull, label %%cont, label %%chk0\n")
	for i, f := range flags {
		next := "reject"
		nextLabel := fmt.Sprintf("chk%d", i+1)
		if i < len(flags)-1 {
			next = nextLabel
		} else {
			nextLabel = "cont"
		}
		_ = next
		w("chk%d:\n", i)
		w("  %%r%d = call i32 @strcmp(ptr %%arg, ptr %s)\n", i, f.sym)
		w("  %%m%d = icmp eq i32 %%r%d, 0\n", i, i)
		if i < len(flags)-1 {
			w("  br i1 %%m%d, label %%reject, label %%chk%d\n", i, i+1)
		} else {
			w("  br i1 %%m%d, label %%reject, label %%cont\n", i)
		}
		_ = nextLabel
	}
	w("reject:\n")
	w("  call i64 @write(i32 2, ptr @.kml_ni_msg, i64 %d)\n", msgLen)
	w("  call void @exit(i32 9)\n")
	w("  unreachable\n")
	w("cont:\n")
	w("  %%inext = add i32 %%i, 1\n")
	w("  br label %%head\n")
	w("ret:\n  ret void\n}\n")
	e.emitGlobal(string(b))
}
