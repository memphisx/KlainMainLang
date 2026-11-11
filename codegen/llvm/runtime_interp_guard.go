package llvm

// ensureNodeInterpFlagGuard declares @__kml_reject_node_interp_flags (hooks.c), a startup
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
	// hooks.c defines it.
}
