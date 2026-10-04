// runtime_weak.go — C-runtime helpers for WeakMap/WeakSet/WeakRef (TDD-00112).
//
// One interface, two backings, chosen by -mm (the same runtime_chan.go pattern):
//
//   - -mm=manual (default): everything is plain malloc'd and never freed — a
//     weak reference is indistinguishable from a strong one here, since nothing
//     is ever collected ("leak by design"). No disappearing-link registration.
//   - -mm=gc (Boehm): the *referent-holding word* is GC_malloc_atomic'd (an
//     UNSCANNED allocation) and registered via
//     GC_general_register_disappearing_link. This is the crux: Boehm traces a
//     normal GC_malloc'd object's words as potential pointers, so a referent
//     stored in scanned memory would be kept alive and the link would never
//     fire. Storing it in an unscanned atomic word makes the disappearing link
//     the sole reference mechanism — Boehm nulls it when the referent is
//     collected. (Verified against a standalone Boehm C repro before wiring.)
//
// Layout. A weak map/set is a one-word head box holding the head of a singly
// linked list of cells { ptr next; ptr link; i64 val } (24 bytes, scanned):
//   - next keeps the list alive (a normal traced pointer).
//   - link points to a separate ONE-word "link cell" holding the referent —
//     malloc'd in manual mode, GC_malloc_atomic'd (unscanned) + registered as a
//     disappearing link in gc mode. A live entry's referent is *cell->link; a
//     collected referent reads back NULL there.
//   - val is the WeakMap value (unused for WeakSet).
//
// WeakRef is just a bare link cell; deref() loads it.
//
// A linked list (not a growable array) keeps every link cell's address stable,
// which the by-address disappearing-link registration requires.
//
// The runtime lives in weaksrc/weak.c (TDD-00240); KML_GC selects the -mm=gc
// backing.
package llvm

import _ "embed"

//go:embed weaksrc/weak.c
var weakSource string

// WeakSource is the weak-collection runtime's C source; under -mm=gc it is
// compiled with the disappearing-link backing (KML_GC).
func (e *Emitter) WeakSource() string {
	src := weakSource
	if e.usedWeakNatives {
		// The WeakRef natives (lib/node/kml_weakref.ts) and their kept
		// objects, which the microtask runtime releases.
		src = "#define KML_WEAK_NATIVES 1\n" + src
	}
	if e.isGCMode() {
		return "#define KML_GC 1\n" + src
	}
	return src
}

// UsesWeakHelpers reports whether the program links the weak runtime.
func (e *Emitter) UsesWeakHelpers() bool { return e.usedWeakHelpers }

// ensureWeakHelpers declares the weak-collection runtime (weak.c) once.
func (e *Emitter) ensureWeakHelpers() {
	if e.usedWeakHelpers {
		return
	}
	e.usedWeakHelpers = true
	e.emitGlobal(`declare ptr @__kml_weak_create()
declare void @__kml_weak_set(ptr, ptr, i64)
declare i64 @__kml_weak_get(ptr, ptr)
declare zeroext i1 @__kml_weak_has(ptr, ptr)
declare zeroext i1 @__kml_weak_delete(ptr, ptr)
declare ptr @__kml_weakref_create(ptr)
declare ptr @__kml_weakref_deref(ptr)`)
}

// ensureWeakNatives links the weak runtime for a WeakRef held in TypeScript
// (lib/node/kml_weakref.ts): its kept objects are released at the microtask
// checkpoint, so the microtask runtime comes along.
func (e *Emitter) ensureWeakNatives() {
	e.usedWeakNatives = true
	e.ensureWeakHelpers()
	e.ensureMicrotasks()
}
