package llvm

import _ "embed"

// runtime_native.go — the native primitives the builtin modules written in
// TypeScript call (lib/native.d.ts, TDD-00231): the ones the runtime defines
// outside the pool. The asynchronous fs operations live in the pool
// (threadpoolsrc/klainpool.c), which calls each one's callback on the loop
// thread.

//go:embed nativesrc/native.c
var nativeSource string

// NativeSource is the native primitives' C source.
func NativeSource() string { return nativeSource }

//go:embed nativesrc/entrypath.c
var entryPathSource string

// NativeCFlags: native.c is compiled the same way for every program; the
// link drops what a program does not use (DeadStripFlags).
func (e *Emitter) NativeCFlags() []string { return DeadStripFlags(e.exportsRuntime()) }
