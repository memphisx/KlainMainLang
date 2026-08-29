// runtime_streams.go — the WHATWG ReadableStream state machine (TDD-00097
// Stage 1). One malloc'd %kml.rstream struct per stream fuses the stream and
// its default controller: the state (readable/closed/errored), the chunk FIFO
// with its high-water mark + total-size accounting, the pending-read promise
// FIFO, the underlying-source closures, and a per-construction-site "fulfill
// thunk" the compiler emits (emit_streams.go) so the runtime can settle read()
// promises with correctly-typed {value, done} records while staying fully
// type-agnostic itself — chunks travel as two raw i64 words (the same
// marshalling the promise v0/v1 slots use).
//
// Layered on ensurePromiseSettle only (promise struct + settle + microtask
// FIFO): a pure in-memory stream program links neither fibers nor libcurl.
//
// %kml.rstream field indices:
//
//	 0 state i64      (0 readable · 1 closed · 2 errored)
//	 1 err i64        (stored error-object ptr bits)
//	 2 qData ptr      chunk FIFO: 24-byte entries { i64 v0, i64 v1, double size }
//	 3 qCap i64
//	 4 qHead i64      head-index FIFO (compacted on push when full)
//	 5 qLen i64
//	 6 total double   Σ entry sizes
//	 7 hwm double     high-water mark
//	 8 sizeClo ptr    size-algorithm closure {fn,env} or null (→ every chunk is 1)
//	 9 pullClo ptr    normalized pull wrapper closure or null
//	10 cancelClo ptr  normalized cancel wrapper closure or null
//	11 flags i64      1 started · 2 pulling · 4 pullAgain · 8 closeRequested ·
//	                  16 disturbed · 32 locked
//	12 rdData ptr     pending-read FIFO: 8-byte promise ptr entries
//	13 rdCap i64
//	14 rdHead i64
//	15 rdLen i64
//	16 closedProm ptr backs reader.closed
//	17 fulfillFn ptr  void (ptr prom, i64 v0, i64 v1, i64 done)
package llvm

import "fmt"

const (
	rstreamStructIR   = "{ i64, i64, ptr, i64, i64, i64, double, double, ptr, ptr, ptr, i64, ptr, i64, i64, i64, ptr, ptr }"
	rstreamStructSize = 144
)

// ensurePromiseAddReaction emits @__kml_promise_add_reaction(ptr %p, ptr %clo):
// attach a reaction closure to a promise — enqueued as a microtask immediately
// when the promise is already settled, appended to its reaction list otherwise.
// The runtime-side generalization of the attach-or-enqueue pattern
// emitPromiseAdopt/emitPromiseThen emit inline at compile sites.
func (e *Emitter) ensurePromiseAddReaction() {
	if e.usedPromiseAddReaction {
		return
	}
	e.usedPromiseAddReaction = true
	e.ensurePromiseSettle()
	e.ensureMalloc()
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_promise_add_reaction(ptr %%p, ptr %%clo) {
entry:
  %%st_p = getelementptr %s, ptr %%p, i32 0, i32 0
  %%st = load i64, ptr %%st_p, align 8
  %%settled = icmp ne i64 %%st, 0
  br i1 %%settled, label %%now, label %%later
now:
  call void @__kml_microtask_enqueue(ptr %%clo)
  ret void
later:
  %%node = call ptr @malloc(i64 16)
  %%rx_p = getelementptr %s, ptr %%p, i32 0, i32 4
  %%old = load ptr, ptr %%rx_p, align 8
  %%nclo_p = getelementptr { ptr, ptr }, ptr %%node, i32 0, i32 0
  store ptr %%clo, ptr %%nclo_p, align 8
  %%nnext_p = getelementptr { ptr, ptr }, ptr %%node, i32 0, i32 1
  store ptr %%old, ptr %%nnext_p, align 8
  store ptr %%node, ptr %%rx_p, align 8
  ret void
}`, promiseStructIR, promiseStructIR))
}

// ensureStreamRuntime emits the ReadableStream runtime helpers once.
func (e *Emitter) ensureStreamRuntime() {
	if e.usedStreamRuntime {
		return
	}
	e.usedStreamRuntime = true
	e.ensurePromiseSettle()
	e.ensurePromiseAddReaction()
	e.ensureMalloc()
	e.ensureRealloc()
	e.ensureMemmove()

	rs := rstreamStructIR
	p := promiseStructIR

	// __kml_rs_alloc(hwm, fulfillFn): a fresh readable stream, everything else
	// zero; closure fields are stored directly by the construction site.
	e.emitGlobal(fmt.Sprintf(`
define ptr @__kml_rs_alloc(double %%hwm, ptr %%ff) {
entry:
  %%s = call ptr @malloc(i64 %d)
  %%st_p = getelementptr %s, ptr %%s, i32 0, i32 0
  store i64 0, ptr %%st_p, align 8
  %%err_p = getelementptr %s, ptr %%s, i32 0, i32 1
  store i64 0, ptr %%err_p, align 8
  %%qd_p = getelementptr %s, ptr %%s, i32 0, i32 2
  store ptr null, ptr %%qd_p, align 8
  %%qc_p = getelementptr %s, ptr %%s, i32 0, i32 3
  store i64 0, ptr %%qc_p, align 8
  %%qh_p = getelementptr %s, ptr %%s, i32 0, i32 4
  store i64 0, ptr %%qh_p, align 8
  %%ql_p = getelementptr %s, ptr %%s, i32 0, i32 5
  store i64 0, ptr %%ql_p, align 8
  %%tot_p = getelementptr %s, ptr %%s, i32 0, i32 6
  store double 0.0, ptr %%tot_p, align 8
  %%hwm_p = getelementptr %s, ptr %%s, i32 0, i32 7
  store double %%hwm, ptr %%hwm_p, align 8
  %%sz_p = getelementptr %s, ptr %%s, i32 0, i32 8
  store ptr null, ptr %%sz_p, align 8
  %%pu_p = getelementptr %s, ptr %%s, i32 0, i32 9
  store ptr null, ptr %%pu_p, align 8
  %%ca_p = getelementptr %s, ptr %%s, i32 0, i32 10
  store ptr null, ptr %%ca_p, align 8
  %%fl_p = getelementptr %s, ptr %%s, i32 0, i32 11
  store i64 0, ptr %%fl_p, align 8
  %%rd_p = getelementptr %s, ptr %%s, i32 0, i32 12
  store ptr null, ptr %%rd_p, align 8
  %%rc_p = getelementptr %s, ptr %%s, i32 0, i32 13
  store i64 0, ptr %%rc_p, align 8
  %%rh_p = getelementptr %s, ptr %%s, i32 0, i32 14
  store i64 0, ptr %%rh_p, align 8
  %%rl_p = getelementptr %s, ptr %%s, i32 0, i32 15
  store i64 0, ptr %%rl_p, align 8
  %%cp = call ptr @__kml_task_alloc_promise()
  %%cp_p = getelementptr %s, ptr %%s, i32 0, i32 16
  store ptr %%cp, ptr %%cp_p, align 8
  %%ff_p = getelementptr %s, ptr %%s, i32 0, i32 17
  store ptr %%ff, ptr %%ff_p, align 8
  ret ptr %%s
}`, rstreamStructSize, rs, rs, rs, rs, rs, rs, rs, rs, rs, rs, rs, rs, rs, rs, rs, rs, rs, rs))

	// __kml_rs_qpush: append a {v0,v1,size} entry, compacting the head-index
	// FIFO to the front before growing (the runtime_microtask.go pattern, with
	// realloc-doubling from emit_arrays_mutate.go).
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_rs_qpush(ptr %%s, i64 %%v0, i64 %%v1, double %%sz) {
entry:
  %%qd_p = getelementptr %s, ptr %%s, i32 0, i32 2
  %%qc_p = getelementptr %s, ptr %%s, i32 0, i32 3
  %%qh_p = getelementptr %s, ptr %%s, i32 0, i32 4
  %%ql_p = getelementptr %s, ptr %%s, i32 0, i32 5
  %%len = load i64, ptr %%ql_p, align 8
  %%cap = load i64, ptr %%qc_p, align 8
  %%full = icmp sge i64 %%len, %%cap
  br i1 %%full, label %%compact, label %%app
compact:
  %%head = load i64, ptr %%qh_p, align 8
  %%hasHead = icmp sgt i64 %%head, 0
  br i1 %%hasHead, label %%doCompact, label %%grow
doCompact:
  %%d0 = load ptr, ptr %%qd_p, align 8
  %%off = mul i64 %%head, 24
  %%src = getelementptr i8, ptr %%d0, i64 %%off
  %%live = sub i64 %%len, %%head
  %%bytes = mul i64 %%live, 24
  call ptr @memmove(ptr %%d0, ptr %%src, i64 %%bytes)
  store i64 %%live, ptr %%ql_p, align 8
  store i64 0, ptr %%qh_p, align 8
  %%stillFull = icmp sge i64 %%live, %%cap
  br i1 %%stillFull, label %%grow, label %%app
grow:
  %%d1 = load ptr, ptr %%qd_p, align 8
  %%cap2 = mul i64 %%cap, 2
  %%ge8 = icmp sgt i64 %%cap2, 8
  %%nc = select i1 %%ge8, i64 %%cap2, i64 8
  %%nbytes = mul i64 %%nc, 24
  %%nd = call ptr @realloc(ptr %%d1, i64 %%nbytes)
  store ptr %%nd, ptr %%qd_p, align 8
  store i64 %%nc, ptr %%qc_p, align 8
  br label %%app
app:
  %%d = load ptr, ptr %%qd_p, align 8
  %%l = load i64, ptr %%ql_p, align 8
  %%eoff = mul i64 %%l, 24
  %%slot = getelementptr i8, ptr %%d, i64 %%eoff
  store i64 %%v0, ptr %%slot, align 8
  %%s1 = getelementptr i8, ptr %%slot, i64 8
  store i64 %%v1, ptr %%s1, align 8
  %%s2 = getelementptr i8, ptr %%slot, i64 16
  store double %%sz, ptr %%s2, align 8
  %%nl = add i64 %%l, 1
  store i64 %%nl, ptr %%ql_p, align 8
  %%tot_p = getelementptr %s, ptr %%s, i32 0, i32 6
  %%tot = load double, ptr %%tot_p, align 8
  %%nt = fadd double %%tot, %%sz
  store double %%nt, ptr %%tot_p, align 8
  ret void
}`, rs, rs, rs, rs, rs))

	// __kml_rs_qunshift: push a chunk back onto the FRONT of the queue
	// (Readable.unshift — ADR-00485). Reuses the head slot when one is
	// free; otherwise grows (same doubling as qpush) and memmoves the live
	// region forward one slot.
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_rs_qunshift(ptr %%s, i64 %%v0, i64 %%v1, double %%sz) {
entry:
  %%qd_p = getelementptr %s, ptr %%s, i32 0, i32 2
  %%qc_p = getelementptr %s, ptr %%s, i32 0, i32 3
  %%qh_p = getelementptr %s, ptr %%s, i32 0, i32 4
  %%ql_p = getelementptr %s, ptr %%s, i32 0, i32 5
  %%head = load i64, ptr %%qh_p, align 8
  %%hasRoom = icmp sgt i64 %%head, 0
  br i1 %%hasRoom, label %%front, label %%shift
front:
  %%nh = sub i64 %%head, 1
  store i64 %%nh, ptr %%qh_p, align 8
  %%d0 = load ptr, ptr %%qd_p, align 8
  %%off0 = mul i64 %%nh, 24
  %%slot0 = getelementptr i8, ptr %%d0, i64 %%off0
  br label %%write
shift:
  %%len = load i64, ptr %%ql_p, align 8
  %%cap = load i64, ptr %%qc_p, align 8
  %%full = icmp sge i64 %%len, %%cap
  br i1 %%full, label %%grow, label %%mv
grow:
  %%d1 = load ptr, ptr %%qd_p, align 8
  %%cap2 = mul i64 %%cap, 2
  %%ge8 = icmp sgt i64 %%cap2, 8
  %%nc = select i1 %%ge8, i64 %%cap2, i64 8
  %%nbytes = mul i64 %%nc, 24
  %%nd = call ptr @realloc(ptr %%d1, i64 %%nbytes)
  store ptr %%nd, ptr %%qd_p, align 8
  store i64 %%nc, ptr %%qc_p, align 8
  br label %%mv
mv:
  %%d2 = load ptr, ptr %%qd_p, align 8
  %%dst = getelementptr i8, ptr %%d2, i64 24
  %%lb = mul i64 %%len, 24
  call ptr @memmove(ptr %%dst, ptr %%d2, i64 %%lb)
  %%nl = add i64 %%len, 1
  store i64 %%nl, ptr %%ql_p, align 8
  br label %%write2
write2:
  %%d3 = load ptr, ptr %%qd_p, align 8
  br label %%writeM
write:
  br label %%writeM
writeM:
  %%slot = phi ptr [ %%slot0, %%write ], [ %%d3, %%write2 ]
  store i64 %%v0, ptr %%slot, align 8
  %%s1 = getelementptr i8, ptr %%slot, i64 8
  store i64 %%v1, ptr %%s1, align 8
  %%s2 = getelementptr i8, ptr %%slot, i64 16
  store double %%sz, ptr %%s2, align 8
  %%tot_p = getelementptr %s, ptr %%s, i32 0, i32 6
  %%tot = load double, ptr %%tot_p, align 8
  %%nt = fadd double %%tot, %%sz
  store double %%nt, ptr %%tot_p, align 8
  ret void
}`, rs, rs, rs, rs, rs))

	// __kml_rs_qpop: dequeue the head entry (caller guarantees non-empty) and
	// subtract its size from the running total.
	e.emitGlobal(fmt.Sprintf(`
define { i64, i64, double } @__kml_rs_qpop(ptr %%s) {
entry:
  %%qd_p = getelementptr %s, ptr %%s, i32 0, i32 2
  %%qh_p = getelementptr %s, ptr %%s, i32 0, i32 4
  %%ql_p = getelementptr %s, ptr %%s, i32 0, i32 5
  %%d = load ptr, ptr %%qd_p, align 8
  %%head = load i64, ptr %%qh_p, align 8
  %%off = mul i64 %%head, 24
  %%slot = getelementptr i8, ptr %%d, i64 %%off
  %%v0 = load i64, ptr %%slot, align 8
  %%s1 = getelementptr i8, ptr %%slot, i64 8
  %%v1 = load i64, ptr %%s1, align 8
  %%s2 = getelementptr i8, ptr %%slot, i64 16
  %%sz = load double, ptr %%s2, align 8
  %%nh = add i64 %%head, 1
  %%len = load i64, ptr %%ql_p, align 8
  %%empty = icmp sge i64 %%nh, %%len
  br i1 %%empty, label %%reset, label %%keep
reset:
  store i64 0, ptr %%qh_p, align 8
  store i64 0, ptr %%ql_p, align 8
  br label %%out
keep:
  store i64 %%nh, ptr %%qh_p, align 8
  br label %%out
out:
  %%tot_p = getelementptr %s, ptr %%s, i32 0, i32 6
  %%tot = load double, ptr %%tot_p, align 8
  %%nt = fsub double %%tot, %%sz
  store double %%nt, ptr %%tot_p, align 8
  %%r0 = insertvalue { i64, i64, double } undef, i64 %%v0, 0
  %%r1 = insertvalue { i64, i64, double } %%r0, i64 %%v1, 1
  %%r2 = insertvalue { i64, i64, double } %%r1, double %%sz, 2
  ret { i64, i64, double } %%r2
}`, rs, rs, rs, rs))

	// __kml_rs_tryread(s): the synchronous Readable.read() core (ADR-00484)
	// — {has, v0, v1}: pops one queued chunk if the queue is non-empty,
	// {0,0,0} otherwise (the caller renders that as null/zero).
	e.emitGlobal(fmt.Sprintf(`
define { i64, i64, i64 } @__kml_rs_tryread(ptr %%s) {
entry:
  %%ql_p = getelementptr %s, ptr %%s, i32 0, i32 5
  %%len = load i64, ptr %%ql_p, align 8
  %%qh_p = getelementptr %s, ptr %%s, i32 0, i32 4
  %%head = load i64, ptr %%qh_p, align 8
  %%avail = icmp sgt i64 %%len, %%head
  br i1 %%avail, label %%pop, label %%empty
pop:
  %%c = call { i64, i64, double } @__kml_rs_qpop(ptr %%s)
  %%v0 = extractvalue { i64, i64, double } %%c, 0
  %%v1 = extractvalue { i64, i64, double } %%c, 1
  %%r0 = insertvalue { i64, i64, i64 } undef, i64 1, 0
  %%r1 = insertvalue { i64, i64, i64 } %%r0, i64 %%v0, 1
  %%r2 = insertvalue { i64, i64, i64 } %%r1, i64 %%v1, 2
  ret { i64, i64, i64 } %%r2
empty:
  ret { i64, i64, i64 } zeroinitializer
}`, rs, rs))

	// __kml_rs_rdpush / __kml_rs_rdpop: the pending-read promise FIFO (8-byte
	// ptr entries, same compact-then-grow discipline as the chunk queue).
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_rs_rdpush(ptr %%s, ptr %%prom) {
entry:
  %%rd_p = getelementptr %s, ptr %%s, i32 0, i32 12
  %%rc_p = getelementptr %s, ptr %%s, i32 0, i32 13
  %%rh_p = getelementptr %s, ptr %%s, i32 0, i32 14
  %%rl_p = getelementptr %s, ptr %%s, i32 0, i32 15
  %%len = load i64, ptr %%rl_p, align 8
  %%cap = load i64, ptr %%rc_p, align 8
  %%full = icmp sge i64 %%len, %%cap
  br i1 %%full, label %%compact, label %%app
compact:
  %%head = load i64, ptr %%rh_p, align 8
  %%hasHead = icmp sgt i64 %%head, 0
  br i1 %%hasHead, label %%doCompact, label %%grow
doCompact:
  %%d0 = load ptr, ptr %%rd_p, align 8
  %%src = getelementptr ptr, ptr %%d0, i64 %%head
  %%live = sub i64 %%len, %%head
  %%bytes = mul i64 %%live, 8
  call ptr @memmove(ptr %%d0, ptr %%src, i64 %%bytes)
  store i64 %%live, ptr %%rl_p, align 8
  store i64 0, ptr %%rh_p, align 8
  %%stillFull = icmp sge i64 %%live, %%cap
  br i1 %%stillFull, label %%grow, label %%app
grow:
  %%d1 = load ptr, ptr %%rd_p, align 8
  %%cap2 = mul i64 %%cap, 2
  %%ge8 = icmp sgt i64 %%cap2, 8
  %%nc = select i1 %%ge8, i64 %%cap2, i64 8
  %%nbytes = mul i64 %%nc, 8
  %%nd = call ptr @realloc(ptr %%d1, i64 %%nbytes)
  store ptr %%nd, ptr %%rd_p, align 8
  store i64 %%nc, ptr %%rc_p, align 8
  br label %%app
app:
  %%d = load ptr, ptr %%rd_p, align 8
  %%l = load i64, ptr %%rl_p, align 8
  %%slot = getelementptr ptr, ptr %%d, i64 %%l
  store ptr %%prom, ptr %%slot, align 8
  %%nl = add i64 %%l, 1
  store i64 %%nl, ptr %%rl_p, align 8
  ret void
}

define ptr @__kml_rs_rdpop(ptr %%s) {
entry:
  %%rd_p = getelementptr %s, ptr %%s, i32 0, i32 12
  %%rh_p = getelementptr %s, ptr %%s, i32 0, i32 14
  %%rl_p = getelementptr %s, ptr %%s, i32 0, i32 15
  %%head = load i64, ptr %%rh_p, align 8
  %%len = load i64, ptr %%rl_p, align 8
  %%have = icmp slt i64 %%head, %%len
  br i1 %%have, label %%pop, label %%none
none:
  ret ptr null
pop:
  %%d = load ptr, ptr %%rd_p, align 8
  %%slot = getelementptr ptr, ptr %%d, i64 %%head
  %%prom = load ptr, ptr %%slot, align 8
  %%nh = add i64 %%head, 1
  %%empty = icmp sge i64 %%nh, %%len
  br i1 %%empty, label %%reset, label %%keep
reset:
  store i64 0, ptr %%rh_p, align 8
  store i64 0, ptr %%rl_p, align 8
  ret ptr %%prom
keep:
  store i64 %%nh, ptr %%rh_p, align 8
  ret ptr %%prom
}`, rs, rs, rs, rs, rs, rs, rs))

	// __kml_rs_pull_if_needed — the spec's CallPullIfNeeded: shouldPull ⇔
	// readable && started && !closeRequested && (a read is pending || the queue
	// is under its high-water mark). Re-entrant pulls coalesce via the
	// pulling/pullAgain flags; an async pull's settlement re-runs the check via
	// a __kml_rs_pull_settled reaction.
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_rs_pull_if_needed(ptr %%s) {
entry:
  %%pu_p = getelementptr %s, ptr %%s, i32 0, i32 9
  %%pc = load ptr, ptr %%pu_p, align 8
  %%nopull = icmp eq ptr %%pc, null
  br i1 %%nopull, label %%ret, label %%ck1
ck1:
  %%st_p = getelementptr %s, ptr %%s, i32 0, i32 0
  %%st = load i64, ptr %%st_p, align 8
  %%notReadable = icmp ne i64 %%st, 0
  br i1 %%notReadable, label %%ret, label %%ck2
ck2:
  %%fl_p = getelementptr %s, ptr %%s, i32 0, i32 11
  %%f = load i64, ptr %%fl_p, align 8
  %%startedBit = and i64 %%f, 1
  %%notStarted = icmp eq i64 %%startedBit, 0
  br i1 %%notStarted, label %%ret, label %%ck3
ck3:
  %%crBit = and i64 %%f, 8
  %%closeReq = icmp ne i64 %%crBit, 0
  br i1 %%closeReq, label %%ret, label %%ck4
ck4:
  %%rh_p = getelementptr %s, ptr %%s, i32 0, i32 14
  %%rl_p = getelementptr %s, ptr %%s, i32 0, i32 15
  %%rh = load i64, ptr %%rh_p, align 8
  %%rl = load i64, ptr %%rl_p, align 8
  %%rdPending = icmp slt i64 %%rh, %%rl
  %%hwm_p = getelementptr %s, ptr %%s, i32 0, i32 7
  %%tot_p = getelementptr %s, ptr %%s, i32 0, i32 6
  %%hwm = load double, ptr %%hwm_p, align 8
  %%tot = load double, ptr %%tot_p, align 8
  %%wantMore = fcmp ogt double %%hwm, %%tot
  %%should = or i1 %%rdPending, %%wantMore
  br i1 %%should, label %%ck5, label %%ret
ck5:
  %%pullingBit = and i64 %%f, 2
  %%isPulling = icmp ne i64 %%pullingBit, 0
  br i1 %%isPulling, label %%again, label %%doPull
again:
  %%fa = or i64 %%f, 4
  store i64 %%fa, ptr %%fl_p, align 8
  ret void
doPull:
  %%fp2 = or i64 %%f, 2
  store i64 %%fp2, ptr %%fl_p, align 8
  %%pfp_p = getelementptr { ptr, ptr }, ptr %%pc, i32 0, i32 0
  %%pfp = load ptr, ptr %%pfp_p, align 8
  %%pep_p = getelementptr { ptr, ptr }, ptr %%pc, i32 0, i32 1
  %%pep = load ptr, ptr %%pep_p, align 8
  %%p = call ptr %%pfp(ptr %%pep)
  %%sync = icmp eq ptr %%p, null
  br i1 %%sync, label %%syncDone, label %%async
syncDone:
  call void @__kml_rs_pull_done(ptr %%s)
  ret void
async:
  %%env = call ptr @malloc(i64 16)
  %%e0 = getelementptr { ptr, ptr }, ptr %%env, i32 0, i32 0
  store ptr %%p, ptr %%e0, align 8
  %%e1 = getelementptr { ptr, ptr }, ptr %%env, i32 0, i32 1
  store ptr %%s, ptr %%e1, align 8
  %%clo = call ptr @malloc(i64 16)
  %%c0 = getelementptr { ptr, ptr }, ptr %%clo, i32 0, i32 0
  store ptr @__kml_rs_pull_settled, ptr %%c0, align 8
  %%c1 = getelementptr { ptr, ptr }, ptr %%clo, i32 0, i32 1
  store ptr %%env, ptr %%c1, align 8
  call void @__kml_promise_add_reaction(ptr %%p, ptr %%clo)
  ret void
ret:
  ret void
}

define void @__kml_rs_pull_done(ptr %%s) {
entry:
  %%fl_p = getelementptr %s, ptr %%s, i32 0, i32 11
  %%f = load i64, ptr %%fl_p, align 8
  %%f2 = and i64 %%f, -3
  %%againBit = and i64 %%f, 4
  %%again = icmp ne i64 %%againBit, 0
  br i1 %%again, label %%repull, label %%done
repull:
  %%f3 = and i64 %%f2, -5
  store i64 %%f3, ptr %%fl_p, align 8
  call void @__kml_rs_pull_if_needed(ptr %%s)
  ret void
done:
  store i64 %%f2, ptr %%fl_p, align 8
  ret void
}

define void @__kml_rs_pull_settled(ptr %%env) {
entry:
  %%e0 = getelementptr { ptr, ptr }, ptr %%env, i32 0, i32 0
  %%p = load ptr, ptr %%e0, align 8
  %%e1 = getelementptr { ptr, ptr }, ptr %%env, i32 0, i32 1
  %%s = load ptr, ptr %%e1, align 8
  %%st_p = getelementptr %s, ptr %%p, i32 0, i32 0
  %%st = load i64, ptr %%st_p, align 8
  %%rejected = icmp eq i64 %%st, 2
  br i1 %%rejected, label %%err, label %%fin
err:
  %%v0_p = getelementptr %s, ptr %%p, i32 0, i32 2
  %%v0 = load i64, ptr %%v0_p, align 8
  call void @__kml_rs_error(ptr %%s, i64 %%v0)
  br label %%fin
fin:
  call void @__kml_rs_pull_done(ptr %%s)
  ret void
}

define void @__kml_rs_started(ptr %%s) {
entry:
  %%fl_p = getelementptr %s, ptr %%s, i32 0, i32 11
  %%f = load i64, ptr %%fl_p, align 8
  %%f2 = or i64 %%f, 1
  store i64 %%f2, ptr %%fl_p, align 8
  call void @__kml_rs_pull_if_needed(ptr %%s)
  ret void
}`, rs, rs, rs, rs, rs, rs, rs, rs, p, p, rs))

	// __kml_rs_enqueue: controller.enqueue's core. Returns 1, or 0 when the
	// stream is no longer readable / close was already requested (the compile
	// site turns 0 into a thrown TypeError). A pending read is fulfilled
	// directly (the queue is necessarily empty then); otherwise the chunk is
	// queued with its size (the size algorithm closure, or 1 per chunk).
	e.emitGlobal(fmt.Sprintf(`
define i64 @__kml_rs_enqueue(ptr %%s, i64 %%v0, i64 %%v1) {
entry:
  %%st_p = getelementptr %s, ptr %%s, i32 0, i32 0
  %%st = load i64, ptr %%st_p, align 8
  %%notReadable = icmp ne i64 %%st, 0
  br i1 %%notReadable, label %%no, label %%ck
ck:
  %%fl_p = getelementptr %s, ptr %%s, i32 0, i32 11
  %%f = load i64, ptr %%fl_p, align 8
  %%crBit = and i64 %%f, 8
  %%closeReq = icmp ne i64 %%crBit, 0
  br i1 %%closeReq, label %%no, label %%go
no:
  ret i64 0
go:
  %%prom = call ptr @__kml_rs_rdpop(ptr %%s)
  %%direct = icmp ne ptr %%prom, null
  br i1 %%direct, label %%fulfill, label %%queue
fulfill:
  %%ff_p = getelementptr %s, ptr %%s, i32 0, i32 17
  %%ff = load ptr, ptr %%ff_p, align 8
  call void %%ff(ptr %%prom, i64 %%v0, i64 %%v1, i64 0)
  br label %%pull
queue:
  %%sz_p = getelementptr %s, ptr %%s, i32 0, i32 8
  %%sc = load ptr, ptr %%sz_p, align 8
  %%nosize = icmp eq ptr %%sc, null
  br i1 %%nosize, label %%one, label %%calc
one:
  br label %%push
calc:
  %%sfp_p = getelementptr { ptr, ptr }, ptr %%sc, i32 0, i32 0
  %%sfp = load ptr, ptr %%sfp_p, align 8
  %%sep_p = getelementptr { ptr, ptr }, ptr %%sc, i32 0, i32 1
  %%sep = load ptr, ptr %%sep_p, align 8
  %%csz = call double %%sfp(ptr %%sep, i64 %%v0, i64 %%v1)
  br label %%push
push:
  %%sz = phi double [ 1.0, %%one ], [ %%csz, %%calc ]
  call void @__kml_rs_qpush(ptr %%s, i64 %%v0, i64 %%v1, double %%sz)
  br label %%pull
pull:
  call void @__kml_rs_pull_if_needed(ptr %%s)
  ret i64 1
}`, rs, rs, rs, rs))

	// __kml_rs_finalize_close: transition to closed — resolve every pending
	// read with {done: true} and settle the closed promise fulfilled.
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_rs_finalize_close(ptr %%s) {
entry:
  %%st_p = getelementptr %s, ptr %%s, i32 0, i32 0
  %%st = load i64, ptr %%st_p, align 8
  %%notReadable = icmp ne i64 %%st, 0
  br i1 %%notReadable, label %%ret, label %%do
do:
  store i64 1, ptr %%st_p, align 8
  %%ff_p = getelementptr %s, ptr %%s, i32 0, i32 17
  %%ff = load ptr, ptr %%ff_p, align 8
  br label %%loop
loop:
  %%prom = call ptr @__kml_rs_rdpop(ptr %%s)
  %%done = icmp eq ptr %%prom, null
  br i1 %%done, label %%closed, label %%resolve
resolve:
  call void %%ff(ptr %%prom, i64 0, i64 0, i64 1)
  br label %%loop
closed:
  %%cp_p = getelementptr %s, ptr %%s, i32 0, i32 16
  %%cp = load ptr, ptr %%cp_p, align 8
  call void @__kml_promise_settle(ptr %%cp, i64 1)
  ret void
ret:
  ret void
}`, rs, rs, rs))

	// __kml_rs_close: controller.close(). Returns 1, or 0 when not allowed
	// (already closed/errored or close already requested).
	e.emitGlobal(fmt.Sprintf(`
define i64 @__kml_rs_close(ptr %%s) {
entry:
  %%st_p = getelementptr %s, ptr %%s, i32 0, i32 0
  %%st = load i64, ptr %%st_p, align 8
  %%notReadable = icmp ne i64 %%st, 0
  br i1 %%notReadable, label %%no, label %%ck
ck:
  %%fl_p = getelementptr %s, ptr %%s, i32 0, i32 11
  %%f = load i64, ptr %%fl_p, align 8
  %%crBit = and i64 %%f, 8
  %%closeReq = icmp ne i64 %%crBit, 0
  br i1 %%closeReq, label %%no, label %%go
no:
  ret i64 0
go:
  %%f2 = or i64 %%f, 8
  store i64 %%f2, ptr %%fl_p, align 8
  %%qh_p = getelementptr %s, ptr %%s, i32 0, i32 4
  %%ql_p = getelementptr %s, ptr %%s, i32 0, i32 5
  %%qh = load i64, ptr %%qh_p, align 8
  %%ql = load i64, ptr %%ql_p, align 8
  %%empty = icmp sge i64 %%qh, %%ql
  br i1 %%empty, label %%fin, label %%later
fin:
  call void @__kml_rs_finalize_close(ptr %%s)
  ret i64 1
later:
  ret i64 1
}`, rs, rs, rs, rs))

	// __kml_rs_error: controller.error(e) / a rejected pull. Drops the queue,
	// rejects every pending read and the closed promise with the stored error.
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_rs_error(ptr %%s, i64 %%err) {
entry:
  %%st_p = getelementptr %s, ptr %%s, i32 0, i32 0
  %%st = load i64, ptr %%st_p, align 8
  %%notReadable = icmp ne i64 %%st, 0
  br i1 %%notReadable, label %%ret, label %%do
do:
  store i64 2, ptr %%st_p, align 8
  %%err_p = getelementptr %s, ptr %%s, i32 0, i32 1
  store i64 %%err, ptr %%err_p, align 8
  %%qh_p = getelementptr %s, ptr %%s, i32 0, i32 4
  store i64 0, ptr %%qh_p, align 8
  %%ql_p = getelementptr %s, ptr %%s, i32 0, i32 5
  store i64 0, ptr %%ql_p, align 8
  %%tot_p = getelementptr %s, ptr %%s, i32 0, i32 6
  store double 0.0, ptr %%tot_p, align 8
  br label %%loop
loop:
  %%prom = call ptr @__kml_rs_rdpop(ptr %%s)
  %%done = icmp eq ptr %%prom, null
  br i1 %%done, label %%closedProm, label %%reject
reject:
  %%v0_p = getelementptr %s, ptr %%prom, i32 0, i32 2
  store i64 %%err, ptr %%v0_p, align 8
  call void @__kml_promise_settle(ptr %%prom, i64 2)
  br label %%loop
closedProm:
  %%cp_p = getelementptr %s, ptr %%s, i32 0, i32 16
  %%cp = load ptr, ptr %%cp_p, align 8
  %%cv0_p = getelementptr %s, ptr %%cp, i32 0, i32 2
  store i64 %%err, ptr %%cv0_p, align 8
  call void @__kml_promise_settle(ptr %%cp, i64 2)
  ret void
ret:
  ret void
}`, rs, rs, rs, rs, rs, p, rs, p))

	// __kml_rs_read: reader.read()'s core — returns the read promise.
	e.emitGlobal(fmt.Sprintf(`
define ptr @__kml_rs_read(ptr %%s) {
entry:
  %%fl_p = getelementptr %s, ptr %%s, i32 0, i32 11
  %%f = load i64, ptr %%fl_p, align 8
  %%fd = or i64 %%f, 16
  store i64 %%fd, ptr %%fl_p, align 8
  %%st_p = getelementptr %s, ptr %%s, i32 0, i32 0
  %%st = load i64, ptr %%st_p, align 8
  %%isErr = icmp eq i64 %%st, 2
  br i1 %%isErr, label %%errored, label %%ck1
errored:
  %%ep = call ptr @__kml_task_alloc_promise()
  %%err_p = getelementptr %s, ptr %%s, i32 0, i32 1
  %%err = load i64, ptr %%err_p, align 8
  %%ev0_p = getelementptr %s, ptr %%ep, i32 0, i32 2
  store i64 %%err, ptr %%ev0_p, align 8
  call void @__kml_promise_settle(ptr %%ep, i64 2)
  ret ptr %%ep
ck1:
  %%isClosed = icmp eq i64 %%st, 1
  br i1 %%isClosed, label %%closed, label %%readable
closed:
  %%cpm = call ptr @__kml_task_alloc_promise()
  %%ff0_p = getelementptr %s, ptr %%s, i32 0, i32 17
  %%ff0 = load ptr, ptr %%ff0_p, align 8
  call void %%ff0(ptr %%cpm, i64 0, i64 0, i64 1)
  ret ptr %%cpm
readable:
  %%qh_p = getelementptr %s, ptr %%s, i32 0, i32 4
  %%ql_p = getelementptr %s, ptr %%s, i32 0, i32 5
  %%qh = load i64, ptr %%qh_p, align 8
  %%ql = load i64, ptr %%ql_p, align 8
  %%have = icmp slt i64 %%qh, %%ql
  br i1 %%have, label %%deq, label %%park
deq:
  %%e = call { i64, i64, double } @__kml_rs_qpop(ptr %%s)
  %%v0 = extractvalue { i64, i64, double } %%e, 0
  %%v1 = extractvalue { i64, i64, double } %%e, 1
  %%pm = call ptr @__kml_task_alloc_promise()
  %%ff_p = getelementptr %s, ptr %%s, i32 0, i32 17
  %%ff = load ptr, ptr %%ff_p, align 8
  call void %%ff(ptr %%pm, i64 %%v0, i64 %%v1, i64 0)
  %%f1 = load i64, ptr %%fl_p, align 8
  %%crBit = and i64 %%f1, 8
  %%closeReq = icmp ne i64 %%crBit, 0
  %%qh2 = load i64, ptr %%qh_p, align 8
  %%ql2 = load i64, ptr %%ql_p, align 8
  %%nowEmpty = icmp sge i64 %%qh2, %%ql2
  %%finish = and i1 %%closeReq, %%nowEmpty
  br i1 %%finish, label %%fin, label %%pull
fin:
  call void @__kml_rs_finalize_close(ptr %%s)
  ret ptr %%pm
pull:
  call void @__kml_rs_pull_if_needed(ptr %%s)
  ret ptr %%pm
park:
  %%pp = call ptr @__kml_task_alloc_promise()
  call void @__kml_rs_rdpush(ptr %%s, ptr %%pp)
  call void @__kml_rs_pull_if_needed(ptr %%s)
  ret ptr %%pp
}`, rs, rs, rs, p, rs, rs, rs, rs))

	// __kml_rs_desired: controller.desiredSize (hwm − total while readable;
	// 0 once closed/errored — JS's null-when-errored is a documented caveat).
	e.emitGlobal(fmt.Sprintf(`
define double @__kml_rs_desired(ptr %%s) {
entry:
  %%st_p = getelementptr %s, ptr %%s, i32 0, i32 0
  %%st = load i64, ptr %%st_p, align 8
  %%readable = icmp eq i64 %%st, 0
  br i1 %%readable, label %%calc, label %%zero
calc:
  %%hwm_p = getelementptr %s, ptr %%s, i32 0, i32 7
  %%tot_p = getelementptr %s, ptr %%s, i32 0, i32 6
  %%hwm = load double, ptr %%hwm_p, align 8
  %%tot = load double, ptr %%tot_p, align 8
  %%d = fsub double %%hwm, %%tot
  ret double %%d
zero:
  ret double 0.0
}`, rs, rs, rs))

	// __kml_rs_cancel: stream/reader cancel(reason) — drop the queue, close,
	// invoke the underlying source's cancel callback; the returned promise is
	// the callback's own promise when it yields one, else already-fulfilled.
	e.emitGlobal(fmt.Sprintf(`
define ptr @__kml_rs_cancel(ptr %%s, i64 %%reason) {
entry:
  %%fl_p = getelementptr %s, ptr %%s, i32 0, i32 11
  %%f = load i64, ptr %%fl_p, align 8
  %%fd = or i64 %%f, 16
  store i64 %%fd, ptr %%fl_p, align 8
  %%st_p = getelementptr %s, ptr %%s, i32 0, i32 0
  %%st = load i64, ptr %%st_p, align 8
  %%isErr = icmp eq i64 %%st, 2
  br i1 %%isErr, label %%errored, label %%ck1
errored:
  %%ep = call ptr @__kml_task_alloc_promise()
  %%err_p = getelementptr %s, ptr %%s, i32 0, i32 1
  %%err = load i64, ptr %%err_p, align 8
  %%ev0_p = getelementptr %s, ptr %%ep, i32 0, i32 2
  store i64 %%err, ptr %%ev0_p, align 8
  call void @__kml_promise_settle(ptr %%ep, i64 2)
  ret ptr %%ep
ck1:
  %%isClosed = icmp eq i64 %%st, 1
  br i1 %%isClosed, label %%already, label %%do
already:
  %%ap = call ptr @__kml_task_alloc_promise()
  call void @__kml_promise_settle(ptr %%ap, i64 1)
  ret ptr %%ap
do:
  %%qh_p = getelementptr %s, ptr %%s, i32 0, i32 4
  store i64 0, ptr %%qh_p, align 8
  %%ql_p = getelementptr %s, ptr %%s, i32 0, i32 5
  store i64 0, ptr %%ql_p, align 8
  %%tot_p = getelementptr %s, ptr %%s, i32 0, i32 6
  store double 0.0, ptr %%tot_p, align 8
  call void @__kml_rs_finalize_close(ptr %%s)
  %%ca_p = getelementptr %s, ptr %%s, i32 0, i32 10
  %%cc = load ptr, ptr %%ca_p, align 8
  %%nocb = icmp eq ptr %%cc, null
  br i1 %%nocb, label %%plain, label %%invoke
invoke:
  %%cfp_p = getelementptr { ptr, ptr }, ptr %%cc, i32 0, i32 0
  %%cfp = load ptr, ptr %%cfp_p, align 8
  %%cep_p = getelementptr { ptr, ptr }, ptr %%cc, i32 0, i32 1
  %%cep = load ptr, ptr %%cep_p, align 8
  %%p = call ptr %%cfp(ptr %%cep, i64 %%reason)
  %%sync = icmp eq ptr %%p, null
  br i1 %%sync, label %%plain, label %%prop
prop:
  ret ptr %%p
plain:
  %%fp2 = call ptr @__kml_task_alloc_promise()
  call void @__kml_promise_settle(ptr %%fp2, i64 1)
  ret ptr %%fp2
}`, rs, rs, rs, p, rs, rs, rs, rs))

	// __kml_rs_lock / __kml_rs_unlock: the reader lock (getReader/releaseLock).
	e.emitGlobal(fmt.Sprintf(`
define i64 @__kml_rs_lock(ptr %%s) {
entry:
  %%fl_p = getelementptr %s, ptr %%s, i32 0, i32 11
  %%f = load i64, ptr %%fl_p, align 8
  %%lockedBit = and i64 %%f, 32
  %%locked = icmp ne i64 %%lockedBit, 0
  br i1 %%locked, label %%no, label %%yes
no:
  ret i64 0
yes:
  %%f2 = or i64 %%f, 32
  store i64 %%f2, ptr %%fl_p, align 8
  ret i64 1
}

define void @__kml_rs_unlock(ptr %%s) {
entry:
  %%fl_p = getelementptr %s, ptr %%s, i32 0, i32 11
  %%f = load i64, ptr %%fl_p, align 8
  %%f2 = and i64 %%f, -33
  store i64 %%f2, ptr %%fl_p, align 8
  ret void
}`, rs, rs))
}
