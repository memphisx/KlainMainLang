// runtime_finalization.go — FinalizationRegistry runtime (TDD-00163).
//
// One pending-callback path, several death signals, chosen by -mm:
//
//   - -mm=manual (default): Memory.free(ptr) looks the pointer up in the
//     process-global registration list (__kml_finreg_onfree, called from the
//     compiled free chokepoint) and enqueues each matching registration's
//     cleanup callback; anything still live at exit is flushed by an atexit
//     hook — deterministic destructors plus an exit sweep. With
//     -finalizers=report the atexit hook first prints one leak line per
//     surviving registration (its held value + the .register() call site).
//   - -mm=gc (Boehm): each register() call also registers a real Boehm
//     finalizer (GC_register_finalizer); when the collector proves the target
//     unreachable it fires __kml_finreg_gc_cb, which only enqueues — never
//     calls into user code from collector context.
//
// Death signals enqueue, they never execute: the callback (wrapped by a
// per-construction-site trampoline that unboxes the held value back to its
// static type) rides the existing microtask FIFO, so it runs at the same
// spec-reachable drain points .then reactions do — end of the top-level
// script, each scheduler step, each fired timer — and at the atexit flush.
//
// Registration cells live on one process-global (per-thread) singly linked
// list; each cell is { ptr next, ptr registry, ptr target, i64 held,
// ptr token, i64 line, i64 col, i64 dead, ptr gcNext } (72 bytes, all
// naturally aligned 8-byte fields). `dead` marks unregistered/already-fired
// cells (cells are never unlinked — same never-compact reasoning as the
// timer queue's -1 sentinel). Under gc, `target` is left null (a scanned
// strong reference would keep the target alive forever, defeating the
// finalizer) and `gcNext` chains earlier registrations on the same target,
// since GC_register_finalizer replaces — and hands back — the previous
// finalizer's client data.
package llvm

import (
	"fmt"
	"strings"
)

// ensureGCInvokeFinalizers declares Boehm's GC_invoke_finalizers exactly once
// (TDD-00163 Stage 3): Boehm only *queues* a dead object's finalizer at
// collection time and runs the queue lazily from later allocation points, so
// gc() and the exit flush call it explicitly to make firing observable.
func (e *Emitter) ensureGCInvokeFinalizers() {
	if e.declaredGCInvokeFin {
		return
	}
	e.declaredGCInvokeFin = true
	e.emitGlobal("declare i32 @GC_invoke_finalizers()")
}

// ensureAtexitDecl declares C atexit exactly once (shared between the h2c
// flush hook and the finalization-registry exit flush).
func (e *Emitter) ensureAtexitDecl() {
	if e.declaredAtexit {
		return
	}
	e.declaredAtexit = true
	e.emitGlobal("declare i32 @atexit(ptr)")
}

// ensureFinalizationHelpers emits the FinalizationRegistry runtime once.
func (e *Emitter) ensureFinalizationHelpers() {
	if e.usedFinRegHelpers {
		return
	}
	e.usedFinRegHelpers = true
	e.ensureMalloc()
	e.ensureMicrotasks()
	e.ensureAtexitDecl()
	e.ensurePrintf()

	// The registration-list head is thread_local under manual mode (matching
	// the timer/microtask singletons; V1 is same-thread anyway) but a plain
	// global under gc: Boehm does not scan TLS blocks as static roots (they
	// are dyld-allocated on macOS), so a TLS-only-reachable cell would be
	// collected out from under its own pending finalizer (verified crash).
	tls := "thread_local "
	if e.isGCMode() {
		tls = ""
	}
	e.emitGlobal("@__kml_finreg_all = internal " + tls + "global ptr null, align 8")
	e.emitGlobal("@__kml_finreg_atexit_done = internal global i8 0, align 1")

	// __kml_finreg_create(closure, trampoline, reportFn) -> ptr: the 24-byte
	// registry record; the first construction process-wide arms the atexit
	// flush.
	e.emitGlobal(`
define ptr @__kml_finreg_create(ptr %cl, ptr %tramp, ptr %rep) {
entry:
  %r = call ptr @malloc(i64 24)
  store ptr %cl, ptr %r, align 8
  %t_p = getelementptr i8, ptr %r, i64 8
  store ptr %tramp, ptr %t_p, align 8
  %rep_p = getelementptr i8, ptr %r, i64 16
  store ptr %rep, ptr %rep_p, align 8
  %done = load i8, ptr @__kml_finreg_atexit_done, align 1
  %isdone = icmp ne i8 %done, 0
  br i1 %isdone, label %out, label %arm
arm:
  store i8 1, ptr @__kml_finreg_atexit_done, align 1
  %rc = call i32 @atexit(ptr @__kml_finreg_atexit)
  br label %out
out:
  ret ptr %r
}`)

	// __kml_finreg_register(reg, target, held, token, line, col): prepend a
	// cell to the global registration list (plus, under gc, the real Boehm
	// finalizer registration — chaining any previous finalizer's cell).
	gcRegister := ""
	targetStore := "  store ptr %target, ptr %tgt_p, align 8\n"
	if e.isGCMode() {
		e.emitGlobal("declare void @GC_register_finalizer(ptr, ptr, ptr, ptr, ptr)")
		// A scanned strong target reference would keep the target alive; the
		// gc death signal is the finalizer itself, so the cell stores null.
		targetStore = "  store ptr null, ptr %tgt_p, align 8\n"
		gcRegister = `  %ofn = alloca ptr, align 8
  %ocd = alloca ptr, align 8
  store ptr null, ptr %ocd, align 8
  call void @GC_register_finalizer(ptr %target, ptr @__kml_finreg_gc_cb, ptr %c, ptr %ofn, ptr %ocd)
  %prevcd = load ptr, ptr %ocd, align 8
  store ptr %prevcd, ptr %gcn_p, align 8
`
	}
	e.emitGlobal(`
define void @__kml_finreg_register(ptr %reg, ptr %target, i64 %held, ptr %token, i64 %line, i64 %col) {
entry:
  %c = call ptr @malloc(i64 72)
  %head = load ptr, ptr @__kml_finreg_all, align 8
  store ptr %head, ptr %c, align 8
  %reg_p = getelementptr i8, ptr %c, i64 8
  store ptr %reg, ptr %reg_p, align 8
  %tgt_p = getelementptr i8, ptr %c, i64 16
` + targetStore + `  %held_p = getelementptr i8, ptr %c, i64 24
  store i64 %held, ptr %held_p, align 8
  %tok_p = getelementptr i8, ptr %c, i64 32
  store ptr %token, ptr %tok_p, align 8
  %line_p = getelementptr i8, ptr %c, i64 40
  store i64 %line, ptr %line_p, align 8
  %col_p = getelementptr i8, ptr %c, i64 48
  store i64 %col, ptr %col_p, align 8
  %dead_p = getelementptr i8, ptr %c, i64 56
  store i64 0, ptr %dead_p, align 8
  %gcn_p = getelementptr i8, ptr %c, i64 64
  store ptr null, ptr %gcn_p, align 8
  store ptr %c, ptr @__kml_finreg_all, align 8
` + gcRegister + `  ret void
}`)

	// __kml_finreg_unregister(reg, token) -> i1: mark every live cell of this
	// registry made with this token dead; true if any existed.
	e.emitGlobal(`
define i1 @__kml_finreg_unregister(ptr %reg, ptr %token) {
entry:
  %foundp = alloca i8, align 1
  %curp = alloca ptr, align 8
  store i8 0, ptr %foundp, align 1
  %tnull = icmp eq ptr %token, null
  br i1 %tnull, label %done, label %start
start:
  %h = load ptr, ptr @__kml_finreg_all, align 8
  store ptr %h, ptr %curp, align 8
  br label %loop
loop:
  %cur = load ptr, ptr %curp, align 8
  %isnull = icmp eq ptr %cur, null
  br i1 %isnull, label %done, label %check
check:
  %reg_p = getelementptr i8, ptr %cur, i64 8
  %creg = load ptr, ptr %reg_p, align 8
  %tok_p = getelementptr i8, ptr %cur, i64 32
  %ctok = load ptr, ptr %tok_p, align 8
  %dead_p = getelementptr i8, ptr %cur, i64 56
  %cdead = load i64, ptr %dead_p, align 8
  %regm = icmp eq ptr %creg, %reg
  %tokm = icmp eq ptr %ctok, %token
  %live = icmp eq i64 %cdead, 0
  %m1 = and i1 %regm, %tokm
  %m = and i1 %m1, %live
  br i1 %m, label %kill, label %next
kill:
  store i64 1, ptr %dead_p, align 8
  store i8 1, ptr %foundp, align 1
  br label %next
next:
  %n = load ptr, ptr %cur, align 8
  store ptr %n, ptr %curp, align 8
  br label %loop
done:
  %f = load i8, ptr %foundp, align 1
  %fb = icmp ne i8 %f, 0
  ret i1 %fb
}`)

	// __kml_finreg_enqueue_cell(cell): mark dead and push the cleanup
	// callback — {trampoline, closure, held} behind a generic 0-arg thunk —
	// onto the microtask FIFO. The single execution path every death signal
	// funnels into. Under gc the env/header are GC_malloc_uncollectable'd:
	// between enqueue and drain they are reachable only through the microtask
	// FIFO's thread_local buffer pointer, which Boehm does not scan (same TLS
	// root hole as the registration-list head above).
	envAlloc := "@malloc"
	if e.isGCMode() {
		e.emitGlobal("declare ptr @GC_malloc_uncollectable(i64)")
		envAlloc = "@GC_malloc_uncollectable"
	}
	e.emitGlobal(strings.ReplaceAll(`
define void @__kml_finreg_enqueue_cell(ptr %cell) {
entry:
  %dead_p = getelementptr i8, ptr %cell, i64 56
  store i64 1, ptr %dead_p, align 8
  %reg_p = getelementptr i8, ptr %cell, i64 8
  %reg = load ptr, ptr %reg_p, align 8
  %cl = load ptr, ptr %reg, align 8
  %tr_p = getelementptr i8, ptr %reg, i64 8
  %tr = load ptr, ptr %tr_p, align 8
  %held_p = getelementptr i8, ptr %cell, i64 24
  %held = load i64, ptr %held_p, align 8
  %env = call ptr ENVALLOC(i64 24)
  store ptr %tr, ptr %env, align 8
  %ecl_p = getelementptr i8, ptr %env, i64 8
  store ptr %cl, ptr %ecl_p, align 8
  %eh_p = getelementptr i8, ptr %env, i64 16
  store i64 %held, ptr %eh_p, align 8
  %hdr = call ptr ENVALLOC(i64 16)
  store ptr @__kml_finreg_thunk, ptr %hdr, align 8
  %he_p = getelementptr i8, ptr %hdr, i64 8
  store ptr %env, ptr %he_p, align 8
  call void @__kml_microtask_enqueue(ptr %hdr)
  ret void
}`, "ENVALLOC", envAlloc))

	e.emitGlobal(`
define void @__kml_finreg_thunk(ptr %env) {
entry:
  %tr = load ptr, ptr %env, align 8
  %cl_p = getelementptr i8, ptr %env, i64 8
  %cl = load ptr, ptr %cl_p, align 8
  %h_p = getelementptr i8, ptr %env, i64 16
  %h = load i64, ptr %h_p, align 8
  call void %tr(ptr %cl, i64 %h)
  ret void
}`)

	if e.isGCMode() {
		// Boehm finalizer: collector-invoked with the newest cell for the
		// collected target; walk the same-target chain, enqueue live ones.
		e.emitGlobal(`
define void @__kml_finreg_gc_cb(ptr %obj, ptr %cd) {
entry:
  %curp = alloca ptr, align 8
  store ptr %cd, ptr %curp, align 8
  br label %loop
loop:
  %cur = load ptr, ptr %curp, align 8
  %isnull = icmp eq ptr %cur, null
  br i1 %isnull, label %done, label %check
check:
  %dead_p = getelementptr i8, ptr %cur, i64 56
  %cdead = load i64, ptr %dead_p, align 8
  %live = icmp eq i64 %cdead, 0
  br i1 %live, label %fire, label %next
fire:
  call void @__kml_finreg_enqueue_cell(ptr %cur)
  br label %next
next:
  %gcn_p = getelementptr i8, ptr %cur, i64 64
  %n = load ptr, ptr %gcn_p, align 8
  store ptr %n, ptr %curp, align 8
  br label %loop
done:
  ret void
}`)
	} else {
		// Manual mode: the Memory.free chokepoint calls this with the pointer
		// about to be freed; every live registration targeting it fires.
		e.emitGlobal(`
define void @__kml_finreg_onfree(ptr %p) {
entry:
  %pnull = icmp eq ptr %p, null
  br i1 %pnull, label %done, label %start
start:
  %curp = alloca ptr, align 8
  %h = load ptr, ptr @__kml_finreg_all, align 8
  store ptr %h, ptr %curp, align 8
  br label %loop
loop:
  %cur = load ptr, ptr %curp, align 8
  %isnull = icmp eq ptr %cur, null
  br i1 %isnull, label %done, label %check
check:
  %tgt_p = getelementptr i8, ptr %cur, i64 16
  %ctgt = load ptr, ptr %tgt_p, align 8
  %dead_p = getelementptr i8, ptr %cur, i64 56
  %cdead = load i64, ptr %dead_p, align 8
  %tgtm = icmp eq ptr %ctgt, %p
  %live = icmp eq i64 %cdead, 0
  %m = and i1 %tgtm, %live
  br i1 %m, label %fire, label %next
fire:
  call void @__kml_finreg_enqueue_cell(ptr %cur)
  br label %next
next:
  %n = load ptr, ptr %cur, align 8
  store ptr %n, ptr %curp, align 8
  br label %loop
done:
  ret void
}`)
	}

	e.emitFinRegAtexit()
}

// emitFinRegAtexit emits the exit flush. Manual mode: (report only) count and
// print one line per still-live registration, then enqueue every survivor;
// both modes finish by draining the microtask FIFO so anything pending —
// survivors and callbacks enqueued after the last event-loop turn — runs.
func (e *Emitter) emitFinRegAtexit() {
	reportBlock := "  br label %flushstart\n"
	if e.finalizersMode == "report" && !e.isGCMode() {
		hdr := e.internString("[finalizers] leak: %lld registration(s) never freed\n")
		reportBlock = fmt.Sprintf(`  %%cntp = alloca i64, align 8
  %%ccurp = alloca ptr, align 8
  store i64 0, ptr %%cntp, align 8
  %%ch = load ptr, ptr @__kml_finreg_all, align 8
  store ptr %%ch, ptr %%ccurp, align 8
  br label %%cloop
cloop:
  %%ccur = load ptr, ptr %%ccurp, align 8
  %%cnull = icmp eq ptr %%ccur, null
  br i1 %%cnull, label %%cdone, label %%ccheck
ccheck:
  %%cdead_p = getelementptr i8, ptr %%ccur, i64 56
  %%cdead = load i64, ptr %%cdead_p, align 8
  %%clive = icmp eq i64 %%cdead, 0
  br i1 %%clive, label %%ccount, label %%cnext
ccount:
  %%cn = load i64, ptr %%cntp, align 8
  %%cn1 = add i64 %%cn, 1
  store i64 %%cn1, ptr %%cntp, align 8
  br label %%cnext
cnext:
  %%cnx = load ptr, ptr %%ccur, align 8
  store ptr %%cnx, ptr %%ccurp, align 8
  br label %%cloop
cdone:
  %%total = load i64, ptr %%cntp, align 8
  %%haveleaks = icmp sgt i64 %%total, 0
  br i1 %%haveleaks, label %%phdr, label %%flushstart
phdr:
  %%prc = call i32 (ptr, ...) @printf(ptr %s, i64 %%total)
  %%rcurp = alloca ptr, align 8
  %%rh = load ptr, ptr @__kml_finreg_all, align 8
  store ptr %%rh, ptr %%rcurp, align 8
  br label %%rloop
rloop:
  %%rcur = load ptr, ptr %%rcurp, align 8
  %%rnull = icmp eq ptr %%rcur, null
  br i1 %%rnull, label %%flushstart, label %%rcheck
rcheck:
  %%rdead_p = getelementptr i8, ptr %%rcur, i64 56
  %%rdead = load i64, ptr %%rdead_p, align 8
  %%rlive = icmp eq i64 %%rdead, 0
  br i1 %%rlive, label %%rprint, label %%rnext
rprint:
  %%rreg_p = getelementptr i8, ptr %%rcur, i64 8
  %%rreg = load ptr, ptr %%rreg_p, align 8
  %%rrep_p = getelementptr i8, ptr %%rreg, i64 16
  %%rrep = load ptr, ptr %%rrep_p, align 8
  %%rheld_p = getelementptr i8, ptr %%rcur, i64 24
  %%rheld = load i64, ptr %%rheld_p, align 8
  %%rline_p = getelementptr i8, ptr %%rcur, i64 40
  %%rline = load i64, ptr %%rline_p, align 8
  %%rcol_p = getelementptr i8, ptr %%rcur, i64 48
  %%rcol = load i64, ptr %%rcol_p, align 8
  call void %%rrep(i64 %%rheld, i64 %%rline, i64 %%rcol)
  br label %%rnext
rnext:
  %%rnx = load ptr, ptr %%rcur, align 8
  store ptr %%rnx, ptr %%rcurp, align 8
  br label %%rloop
`, hdr)
	}

	flushBlock := `flushstart:
  br label %drain
`
	if e.isGCMode() {
		// Run any queued-but-not-yet-invoked Boehm finalizers so their
		// enqueued callbacks make this exit drain.
		e.ensureGCInvokeFinalizers()
		flushBlock = `flushstart:
  %ifc = call i32 @GC_invoke_finalizers()
  br label %drain
`
	}
	if !e.isGCMode() {
		// Survivor flush: everything never freed still gets its one cleanup.
		flushBlock = `flushstart:
  %fcurp = alloca ptr, align 8
  %fh = load ptr, ptr @__kml_finreg_all, align 8
  store ptr %fh, ptr %fcurp, align 8
  br label %floop
floop:
  %fcur = load ptr, ptr %fcurp, align 8
  %fnull = icmp eq ptr %fcur, null
  br i1 %fnull, label %drain, label %fcheck
fcheck:
  %fdead_p = getelementptr i8, ptr %fcur, i64 56
  %fdead = load i64, ptr %fdead_p, align 8
  %flive = icmp eq i64 %fdead, 0
  br i1 %flive, label %ffire, label %fnext
ffire:
  call void @__kml_finreg_enqueue_cell(ptr %fcur)
  br label %fnext
fnext:
  %fnx = load ptr, ptr %fcur, align 8
  store ptr %fnx, ptr %fcurp, align 8
  br label %floop
`
	}

	e.emitGlobal(`
define void @__kml_finreg_atexit() {
entry:
` + reportBlock + flushBlock + `drain:
  call void @__kml_drain_microtasks()
  ret void
}`)
}
