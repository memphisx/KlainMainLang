package llvm

import _ "embed"

// runtime_sync.go — the `klain:sync` goroutine runtime plumbing (TDD-00143).
//
// klain:sync is an explicitly-non-Node opt-in: a Go-style GMP scheduler with
// cheap goroutines (`go`), CSP channels (`Channel<T>`), and a cooperative
// preempt safepoint, implemented in the embedded C runtime klainsyncsrc/
// klainsync.c and linked only when a program imports the module. A program
// that never imports klain:sync links none of it (usedSync gates the source in
// EmbeddedCSources). Nothing here touches async/await, Promises, or Worker.
//
// The extern C ABI is deliberately tiny; channel elements are a fixed 8-byte
// slot (i64), so send/receive bitcast the element type T through i64.

//go:embed klainsyncsrc/klainsync.c
var syncSource string

// SyncSource returns the embedded GMP-scheduler + channel C runtime.
func SyncSource() string { return syncSource }

// UsesSync reports whether the program uses klain:sync, gating the
// embedded-source link (EmbeddedCSources).
func (e *Emitter) UsesSync() bool { return e.usedSync }

// ensureSyncRuntime links the scheduler: klain:sync's `@link sync`, and a
// program importing it for its safepoints.
func (e *Emitter) ensureSyncRuntime() {
	if e.usedSync {
		return
	}
	e.usedSync = true
	e.emitGlobal(`declare void @klainsync_safepoint()`)
}
