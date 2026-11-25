package llvm

// The Node-interpreter guard: @__kml_reject_node_interp_flags (hooks.c), a
// startup check every compiled binary runs before its program body, armed
// when the program can spawn a process (armInterpGuard): only such a
// program can re-run itself.
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

// armInterpGuard arms the guard from the hooks constructor: the program
// spawns processes, so it may spawn itself.
func (e *Emitter) armInterpGuard() {
	const arm = "  store i8 1, " + irPtr + " @__kml_interp_guard_armed, align 1\n"
	for _, h := range e.hookInits {
		if h == arm {
			return
		}
	}
	e.addHookInit(arm)
}
