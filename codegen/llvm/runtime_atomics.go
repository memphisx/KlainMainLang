// runtime_atomics.go — the Atomics.wait/notify runtime (TDD-00099): a
// portable futex substitute. No futex syscall exists on macOS, so waiting is
// an address-keyed linked list of waiter nodes guarded by one PROCESS-WIDE
// (deliberately not thread_local — cross-thread wakeup is the whole point)
// pthread mutex + condition variable. notify marks matching nodes and
// broadcasts; a spurious cond wakeup can never produce a false "ok" because
// only a matching notify sets a node's flag.
//
// Zero-initialized static storage is NOT a valid mutex/cond on Darwin
// (PTHREAD_MUTEX_INITIALIZER carries a nonzero signature; locking a zeroed
// one returns EINVAL — verified by prototype), so both are initialized
// lazily through a cmpxchg-guarded init the wait/notify entry points call.
//
// The runtime lives in atomicssrc/atomics.c (TDD-00240).
//
// The plain atomic operations (Atomics.load/store/add/.../compareExchange)
// need nothing here at all — they lower directly to load atomic /
// store atomic / atomicrmw / cmpxchg in emit_atomics.go.
package llvm

import _ "embed"

//go:embed atomicssrc/atomics.c
var atomicsSource string

// AtomicsSource is the Atomics.wait/notify runtime's C source.
func AtomicsSource() string { return atomicsSource }

// UsesAtomicsRuntime reports whether the program links the Atomics runtime.
func (e *Emitter) UsesAtomicsRuntime() bool { return e.usedAtomicsRuntime }

// ensureAtomicsRuntime declares the Atomics runtime (atomics.c) once.
func (e *Emitter) ensureAtomicsRuntime() {
	if e.usedAtomicsRuntime {
		return
	}
	e.usedAtomicsRuntime = true
	e.emitGlobal(`declare i64 @__kml_atomics_wait(ptr, i32, double)
declare i64 @__kml_atomics_wait64(ptr, i64, double)
declare i64 @__kml_atomics_notify(ptr, i64)`)
}
