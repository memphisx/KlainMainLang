package llvm

import _ "embed"

// runtime_sync.go — the `klain:sync` goroutine runtime plumbing (TDD-00143).
//
// klain:sync is an explicitly-non-Node opt-in: a Go-style GMP scheduler with
// cheap goroutines (`go`), CSP channels (`Channel<T>`), and a cooperative
// preempt safepoint, implemented in the embedded C runtime klainsyncsrc/
// klainsync.c and linked only when a program imports the module. A program
// that never imports klain:sync links none of it (the unit is linked when the
// program names it). Nothing here touches async/await, Promises, or Worker.
//
// The extern C ABI is deliberately tiny; channel elements are a fixed 8-byte
// slot (i64), so send/receive bitcast the element type T through i64.

//go:embed klainsyncsrc/klainsync.c
var syncSource string

// SyncSource returns the embedded GMP-scheduler + channel C runtime.
func SyncSource() string { return syncSource }

// UsesSync reports whether the program imports klain:sync (its goroutine
// threads change how the thread pool registers with the collector).
func (e *Emitter) UsesSync() bool { return e.usesSyncProgram }
