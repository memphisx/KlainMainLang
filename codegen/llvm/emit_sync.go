package llvm

// emit_sync.go — klain:sync (TDD-00143) is lib/node/klain_sync.ts over the
// goroutine scheduler (klainsync.c). The compiler's own part is the
// preemption: a program importing klain:sync gets a cooperative safepoint at
// every function entry and loop back-edge.

// emitSafepoint emits a cooperative preempt check (TDD-00143 Stage 2) — a call
// into the runtime that yields the goroutine iff sysmon has flagged it. Emitted
// at function entry and loop back-edges, but only when the program imports
// klain:sync (usesSyncProgram); a non-klain program emits nothing here. A no-op
// after a terminator (the dead-code path drops it, which is fine).
func (e *Emitter) emitSafepoint() {
	if !e.usesSyncProgram || e.blockDone {
		return
	}
	e.emitInstr("call void @klainsync_safepoint()")
}
