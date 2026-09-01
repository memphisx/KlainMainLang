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

// UsesSync reports whether the program used klain:sync (spawned a goroutine or
// constructed a channel), gating the embedded-source link (EmbeddedCSources).
func (e *Emitter) UsesSync() bool { return e.usedSync }

// ensureSyncRuntime declares the klainsync extern C ABI exactly once and marks
// the runtime as used (so its C source is compiled and linked). Call it before
// emitting any call into the runtime.
func (e *Emitter) ensureSyncRuntime() {
	if e.usedSync {
		return
	}
	e.usedSync = true

	// go(fn, env): spawn a goroutine running fn(env) — the two halves of a
	// closure header {fnptr, envptr}.
	e.emitGlobal(`declare void @klainsync_go(ptr, ptr)`)
	// Channel<T> ABI. Elements are a fixed 8-byte i64 slot.
	e.emitGlobal(`declare ptr @klainsync_chan_new(i64)`)
	e.emitGlobal(`declare void @klainsync_chan_send(ptr, i64)`)
	// recv(ch, ok*) -> value; *ok is 1 for a real value, 0 for a drained
	// closed channel (value is then the zero slot).
	e.emitGlobal(`declare i64 @klainsync_chan_recv(ptr, ptr)`)
	e.emitGlobal(`declare void @klainsync_chan_close(ptr)`)
	// select(cases*, n, has_default) -> chosen case index (or -1 for default).
	e.emitGlobal(`declare i32 @klainsync_select(ptr, i32, i32)`)
	// The cooperative preempt safepoint (function entry in Stage 1).
	e.emitGlobal(`declare void @klainsync_safepoint()`)
}
