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
package llvm

// ensureWeakHelpers emits the weak-collection runtime exactly once.
func (e *Emitter) ensureWeakHelpers() {
	if e.usedWeakHelpers {
		return
	}
	e.usedWeakHelpers = true
	e.ensureMalloc()

	// The scanned allocator (cells, head boxes) is plain @malloc in both modes
	// — in -mm=gc the allocator shim redirects it to Boehm's scanned GC_malloc
	// (ADR-00071). The referent-word allocator is unscanned under gc.
	linkAlloc := "@malloc"
	if e.isGCMode() {
		e.emitGlobal("declare ptr @GC_malloc_atomic(i64 noundef)")
		e.emitGlobal("declare i32 @GC_general_register_disappearing_link(ptr noundef, ptr noundef)")
		linkAlloc = "@GC_malloc_atomic"
	}

	// makeLinkCell emits the IR to allocate a one-word link cell holding %obj
	// and (gc only) register it as a disappearing link, leaving the cell pointer
	// in the returned register name. Emitted inline at each construction site.
	// registerLink is the gc-only registration on an existing slot.
	registerLink := func(slot, obj string) string {
		if !e.isGCMode() {
			return ""
		}
		return "  call i32 @GC_general_register_disappearing_link(ptr " + slot + ", ptr " + obj + ")\n"
	}

	// __kml_weak_create() -> ptr : an 8-byte head box, list initially empty.
	e.emitGlobal("" +
		"define ptr @__kml_weak_create() {\n" +
		"entry:\n" +
		"  %h = call ptr @malloc(i64 8)\n" +
		"  store ptr null, ptr %h, align 8\n" +
		"  ret ptr %h\n" +
		"}")

	// __kml_weak_find(head, key) -> ptr : first cell whose live referent == key,
	// or null. A cell whose referent has been collected (link word NULL) never
	// matches a non-null key, so it is skipped.
	e.emitGlobal("" +
		"define ptr @__kml_weak_find(ptr %h, ptr %key) {\n" +
		"entry:\n" +
		"  %cur0 = load ptr, ptr %h, align 8\n" +
		"  br label %loop\n" +
		"loop:\n" +
		"  %cur = phi ptr [ %cur0, %entry ], [ %next, %cont ]\n" +
		"  %isnull = icmp eq ptr %cur, null\n" +
		"  br i1 %isnull, label %none, label %check\n" +
		"check:\n" +
		"  %linkslotp = getelementptr i8, ptr %cur, i64 8\n" +
		"  %linkslot = load ptr, ptr %linkslotp, align 8\n" +
		"  %ref = load ptr, ptr %linkslot, align 8\n" +
		"  %hit = icmp eq ptr %ref, %key\n" +
		"  br i1 %hit, label %found, label %cont\n" +
		"cont:\n" +
		"  %next = load ptr, ptr %cur, align 8\n" +
		"  br label %loop\n" +
		"found:\n" +
		"  ret ptr %cur\n" +
		"none:\n" +
		"  ret ptr null\n" +
		"}")

	// __kml_weak_set(head, key, val) : update an existing cell, else prepend one.
	e.emitGlobal("" +
		"define void @__kml_weak_set(ptr %h, ptr %key, i64 %val) {\n" +
		"entry:\n" +
		"  %found = call ptr @__kml_weak_find(ptr %h, ptr %key)\n" +
		"  %exists = icmp ne ptr %found, null\n" +
		"  br i1 %exists, label %upd, label %new\n" +
		"upd:\n" +
		"  %uvalp = getelementptr i8, ptr %found, i64 16\n" +
		"  store i64 %val, ptr %uvalp, align 8\n" +
		"  ret void\n" +
		"new:\n" +
		"  %linkslot = call ptr " + linkAlloc + "(i64 8)\n" +
		"  store ptr %key, ptr %linkslot, align 8\n" +
		registerLink("%linkslot", "%key") +
		"  %cell = call ptr @malloc(i64 24)\n" +
		"  %oldhead = load ptr, ptr %h, align 8\n" +
		"  store ptr %oldhead, ptr %cell, align 8\n" +
		"  %clinkp = getelementptr i8, ptr %cell, i64 8\n" +
		"  store ptr %linkslot, ptr %clinkp, align 8\n" +
		"  %cvalp = getelementptr i8, ptr %cell, i64 16\n" +
		"  store i64 %val, ptr %cvalp, align 8\n" +
		"  store ptr %cell, ptr %h, align 8\n" +
		"  ret void\n" +
		"}")

	// __kml_weak_get(head, key) -> i64 : the cell's val, or 0 if absent.
	e.emitGlobal("" +
		"define i64 @__kml_weak_get(ptr %h, ptr %key) {\n" +
		"entry:\n" +
		"  %found = call ptr @__kml_weak_find(ptr %h, ptr %key)\n" +
		"  %isnull = icmp eq ptr %found, null\n" +
		"  br i1 %isnull, label %miss, label %hit\n" +
		"hit:\n" +
		"  %valp = getelementptr i8, ptr %found, i64 16\n" +
		"  %v = load i64, ptr %valp, align 8\n" +
		"  ret i64 %v\n" +
		"miss:\n" +
		"  ret i64 0\n" +
		"}")

	// __kml_weak_has(head, key) -> i1
	e.emitGlobal("" +
		"define i1 @__kml_weak_has(ptr %h, ptr %key) {\n" +
		"entry:\n" +
		"  %found = call ptr @__kml_weak_find(ptr %h, ptr %key)\n" +
		"  %present = icmp ne ptr %found, null\n" +
		"  ret i1 %present\n" +
		"}")

	// __kml_weak_delete(head, key) -> i1 : unlink the cell if present. The
	// disappearing-link registration on the removed cell's link word is left
	// as-is — the word becomes unreachable and is collected (gc) or leaked
	// (manual); Boehm tolerates a dangling registration on collected memory.
	e.emitGlobal("" +
		"define i1 @__kml_weak_delete(ptr %h, ptr %key) {\n" +
		"entry:\n" +
		"  %cur0 = load ptr, ptr %h, align 8\n" +
		"  br label %loop\n" +
		"loop:\n" +
		"  %prev = phi ptr [ %h, %entry ], [ %cur, %cont ]\n" +
		"  %cur = phi ptr [ %cur0, %entry ], [ %next, %cont ]\n" +
		"  %isnull = icmp eq ptr %cur, null\n" +
		"  br i1 %isnull, label %none, label %check\n" +
		"check:\n" +
		"  %linkslotp = getelementptr i8, ptr %cur, i64 8\n" +
		"  %linkslot = load ptr, ptr %linkslotp, align 8\n" +
		"  %ref = load ptr, ptr %linkslot, align 8\n" +
		"  %next = load ptr, ptr %cur, align 8\n" +
		"  %hit = icmp eq ptr %ref, %key\n" +
		"  br i1 %hit, label %unlink, label %cont\n" +
		"unlink:\n" +
		// prev's next field is at offset 0 for a cell, and the head box's slot is
		// its first (only) word — so a store at prev+0 works for both.
		"  store ptr %next, ptr %prev, align 8\n" +
		"  ret i1 1\n" +
		"cont:\n" +
		"  br label %loop\n" +
		"none:\n" +
		"  ret i1 0\n" +
		"}")

	// __kml_weakref_create(obj) -> ptr : a bare one-word link cell holding the
	// referent (unscanned + registered under gc), so deref reads NULL once the
	// referent is collected.
	e.emitGlobal("" +
		"define ptr @__kml_weakref_create(ptr %obj) {\n" +
		"entry:\n" +
		"  %box = call ptr " + linkAlloc + "(i64 8)\n" +
		"  store ptr %obj, ptr %box, align 8\n" +
		registerLink("%box", "%obj") +
		"  ret ptr %box\n" +
		"}")

	// __kml_weakref_deref(box) -> ptr : the referent, or null once collected.
	e.emitGlobal("" +
		"define ptr @__kml_weakref_deref(ptr %box) {\n" +
		"entry:\n" +
		"  %v = load ptr, ptr %box, align 8\n" +
		"  ret ptr %v\n" +
		"}")
}
