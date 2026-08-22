// runtime_streams_writable.go — the WHATWG WritableStream state machine
// (TDD-00097 Stage 2). One malloc'd %kml.wstream struct fuses the stream, its
// default controller, and its writer (compile-time retyping, exactly the
// %kml.rstream convention in runtime_streams.go). Every write() enqueues a
// {chunk words, size, per-write promise} entry; a drain loop dequeues one
// entry at a time, invokes the sink's write, and advances on its settlement
// via a promise reaction — sink writes never overlap (the spec's in-flight
// write). Backpressure is the writer.ready promise: re-created pending when
// desiredSize drops to 0 or below, settled fulfilled when it recovers.
//
// %kml.wstream field indices:
//
//	 0 state i64      (0 writable · 1 closed · 2 errored)
//	 1 err i64        stored error ptr bits
//	 2 qData ptr      write FIFO: 32-byte entries { i64 v0, i64 v1, double size, ptr prom }
//	 3 qCap i64
//	 4 qHead i64
//	 5 qLen i64
//	 6 total double
//	 7 hwm double
//	 8 sizeClo ptr    or null (→ 1 per chunk)
//	 9 writeClo ptr   normalized: ptr (env, i64 v0, i64 v1) → promise|null
//	10 closeClo ptr   normalized: ptr (env) → promise|null, or null
//	11 abortClo ptr   normalized: ptr (env, i64 reason) → promise|null, or null
//	12 flags i64      1 started · 2 inFlight · 8 closeRequested · 32 locked
//	13 readyProm ptr  current writer.ready promise
//	14 closedProm ptr backs writer.closed
//	15 closeProm ptr  the promise writer.close() returned (null until requested)
package llvm

import "fmt"

const (
	wstreamStructIR   = "{ i64, i64, ptr, i64, i64, i64, double, double, ptr, ptr, ptr, ptr, i64, ptr, ptr, ptr }"
	wstreamStructSize = 128
)

// ensureWStreamRuntime emits the WritableStream runtime helpers once.
func (e *Emitter) ensureWStreamRuntime() {
	if e.usedWStreamRuntime {
		return
	}
	e.usedWStreamRuntime = true
	e.ensurePromiseSettle()
	e.ensurePromiseAddReaction()
	e.ensureMalloc()
	e.ensureRealloc()
	e.ensureMemmove()

	ws := wstreamStructIR
	p := promiseStructIR

	// __kml_ws_alloc(hwm): a fresh writable stream; ready starts fulfilled
	// (no backpressure while the queue is empty), closed starts pending.
	e.emitGlobal(fmt.Sprintf(`
define ptr @__kml_ws_alloc(double %%hwm) {
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
  %%wr_p = getelementptr %s, ptr %%s, i32 0, i32 9
  store ptr null, ptr %%wr_p, align 8
  %%cl_p = getelementptr %s, ptr %%s, i32 0, i32 10
  store ptr null, ptr %%cl_p, align 8
  %%ab_p = getelementptr %s, ptr %%s, i32 0, i32 11
  store ptr null, ptr %%ab_p, align 8
  %%fl_p = getelementptr %s, ptr %%s, i32 0, i32 12
  store i64 0, ptr %%fl_p, align 8
  %%rdy = call ptr @__kml_task_alloc_promise()
  call void @__kml_promise_settle(ptr %%rdy, i64 1)
  %%rdy_p = getelementptr %s, ptr %%s, i32 0, i32 13
  store ptr %%rdy, ptr %%rdy_p, align 8
  %%cp = call ptr @__kml_task_alloc_promise()
  %%cp_p = getelementptr %s, ptr %%s, i32 0, i32 14
  store ptr %%cp, ptr %%cp_p, align 8
  %%clp_p = getelementptr %s, ptr %%s, i32 0, i32 15
  store ptr null, ptr %%clp_p, align 8
  ret ptr %%s
}`, wstreamStructSize, ws, ws, ws, ws, ws, ws, ws, ws, ws, ws, ws, ws, ws, ws, ws, ws))

	// __kml_ws_qpush: append a {v0,v1,size,prom} entry (compact-then-grow).
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_ws_qpush(ptr %%s, i64 %%v0, i64 %%v1, double %%sz, ptr %%prom) {
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
  %%off = mul i64 %%head, 32
  %%src = getelementptr i8, ptr %%d0, i64 %%off
  %%live = sub i64 %%len, %%head
  %%bytes = mul i64 %%live, 32
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
  %%nbytes = mul i64 %%nc, 32
  %%nd = call ptr @realloc(ptr %%d1, i64 %%nbytes)
  store ptr %%nd, ptr %%qd_p, align 8
  store i64 %%nc, ptr %%qc_p, align 8
  br label %%app
app:
  %%d = load ptr, ptr %%qd_p, align 8
  %%l = load i64, ptr %%ql_p, align 8
  %%eoff = mul i64 %%l, 32
  %%slot = getelementptr i8, ptr %%d, i64 %%eoff
  store i64 %%v0, ptr %%slot, align 8
  %%s1 = getelementptr i8, ptr %%slot, i64 8
  store i64 %%v1, ptr %%s1, align 8
  %%s2 = getelementptr i8, ptr %%slot, i64 16
  store double %%sz, ptr %%s2, align 8
  %%s3 = getelementptr i8, ptr %%slot, i64 24
  store ptr %%prom, ptr %%s3, align 8
  %%nl = add i64 %%l, 1
  store i64 %%nl, ptr %%ql_p, align 8
  %%tot_p = getelementptr %s, ptr %%s, i32 0, i32 6
  %%tot = load double, ptr %%tot_p, align 8
  %%nt = fadd double %%tot, %%sz
  store double %%nt, ptr %%tot_p, align 8
  call void @__kml_ws_update_ready(ptr %%s)
  ret void
}`, ws, ws, ws, ws, ws))

	// __kml_ws_update_ready: re-arm or settle writer.ready to track
	// backpressure (desiredSize ≤ 0 while writable ⇒ pending ready).
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_ws_update_ready(ptr %%s) {
entry:
  %%st_p = getelementptr %s, ptr %%s, i32 0, i32 0
  %%st = load i64, ptr %%st_p, align 8
  %%writable = icmp eq i64 %%st, 0
  br i1 %%writable, label %%ck, label %%ret
ck:
  %%hwm_p = getelementptr %s, ptr %%s, i32 0, i32 7
  %%tot_p = getelementptr %s, ptr %%s, i32 0, i32 6
  %%hwm = load double, ptr %%hwm_p, align 8
  %%tot = load double, ptr %%tot_p, align 8
  %%bp = fcmp ole double %%hwm, %%tot
  %%rdy_p = getelementptr %s, ptr %%s, i32 0, i32 13
  %%rdy = load ptr, ptr %%rdy_p, align 8
  %%rst_p = getelementptr %s, ptr %%rdy, i32 0, i32 0
  %%rst = load i64, ptr %%rst_p, align 8
  %%settled = icmp ne i64 %%rst, 0
  br i1 %%bp, label %%wantPending, label %%wantReady
wantPending:
  br i1 %%settled, label %%rearm, label %%ret
rearm:
  %%np = call ptr @__kml_task_alloc_promise()
  store ptr %%np, ptr %%rdy_p, align 8
  ret void
wantReady:
  br i1 %%settled, label %%ret, label %%fulfill
fulfill:
  call void @__kml_promise_settle(ptr %%rdy, i64 1)
  ret void
ret:
  ret void
}`, ws, ws, ws, ws, p))

	// __kml_ws_advance — the drain loop step: while writable, started, and no
	// write in flight, dequeue one entry and run the sink's write; when the
	// queue empties with close requested, run the sink's close.
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_ws_advance(ptr %%s) {
entry:
  %%st_p = getelementptr %s, ptr %%s, i32 0, i32 0
  %%st = load i64, ptr %%st_p, align 8
  %%writable = icmp eq i64 %%st, 0
  br i1 %%writable, label %%ck1, label %%ret
ck1:
  %%fl_p = getelementptr %s, ptr %%s, i32 0, i32 12
  %%f = load i64, ptr %%fl_p, align 8
  %%startedBit = and i64 %%f, 1
  %%notStarted = icmp eq i64 %%startedBit, 0
  br i1 %%notStarted, label %%ret, label %%ck2
ck2:
  %%ifBit = and i64 %%f, 2
  %%inFlight = icmp ne i64 %%ifBit, 0
  br i1 %%inFlight, label %%ret, label %%ck3
ck3:
  %%qh_p = getelementptr %s, ptr %%s, i32 0, i32 4
  %%ql_p = getelementptr %s, ptr %%s, i32 0, i32 5
  %%qh = load i64, ptr %%qh_p, align 8
  %%ql = load i64, ptr %%ql_p, align 8
  %%have = icmp slt i64 %%qh, %%ql
  br i1 %%have, label %%deq, label %%maybeClose
deq:
  %%qd_p = getelementptr %s, ptr %%s, i32 0, i32 2
  %%d = load ptr, ptr %%qd_p, align 8
  %%off = mul i64 %%qh, 32
  %%slot = getelementptr i8, ptr %%d, i64 %%off
  %%v0 = load i64, ptr %%slot, align 8
  %%s1 = getelementptr i8, ptr %%slot, i64 8
  %%v1 = load i64, ptr %%s1, align 8
  %%s2 = getelementptr i8, ptr %%slot, i64 16
  %%sz = load double, ptr %%s2, align 8
  %%s3 = getelementptr i8, ptr %%slot, i64 24
  %%wprom = load ptr, ptr %%s3, align 8
  %%nh = add i64 %%qh, 1
  %%emptyNow = icmp sge i64 %%nh, %%ql
  br i1 %%emptyNow, label %%resetq, label %%keepq
resetq:
  store i64 0, ptr %%qh_p, align 8
  store i64 0, ptr %%ql_p, align 8
  br label %%dequeued
keepq:
  store i64 %%nh, ptr %%qh_p, align 8
  br label %%dequeued
dequeued:
  %%tot_p = getelementptr %s, ptr %%s, i32 0, i32 6
  %%tot = load double, ptr %%tot_p, align 8
  %%nt = fsub double %%tot, %%sz
  store double %%nt, ptr %%tot_p, align 8
  call void @__kml_ws_update_ready(ptr %%s)
  %%f2 = or i64 %%f, 2
  store i64 %%f2, ptr %%fl_p, align 8
  %%wc_p = getelementptr %s, ptr %%s, i32 0, i32 9
  %%wc = load ptr, ptr %%wc_p, align 8
  %%nowrite = icmp eq ptr %%wc, null
  br i1 %%nowrite, label %%writeSync, label %%invoke
invoke:
  %%wfp_p = getelementptr { ptr, ptr }, ptr %%wc, i32 0, i32 0
  %%wfp = load ptr, ptr %%wfp_p, align 8
  %%wep_p = getelementptr { ptr, ptr }, ptr %%wc, i32 0, i32 1
  %%wep = load ptr, ptr %%wep_p, align 8
  %%wp = call ptr %%wfp(ptr %%wep, i64 %%v0, i64 %%v1)
  %%sync = icmp eq ptr %%wp, null
  br i1 %%sync, label %%writeSync, label %%async
writeSync:
  call void @__kml_promise_settle(ptr %%wprom, i64 1)
  %%f3 = load i64, ptr %%fl_p, align 8
  %%f4 = and i64 %%f3, -3
  store i64 %%f4, ptr %%fl_p, align 8
  call void @__kml_ws_advance(ptr %%s)
  ret void
async:
  %%env = call ptr @malloc(i64 24)
  %%e0 = getelementptr ptr, ptr %%env, i64 0
  store ptr %%wp, ptr %%e0, align 8
  %%e1 = getelementptr ptr, ptr %%env, i64 1
  store ptr %%s, ptr %%e1, align 8
  %%e2 = getelementptr ptr, ptr %%env, i64 2
  store ptr %%wprom, ptr %%e2, align 8
  %%clo = call ptr @malloc(i64 16)
  %%c0 = getelementptr { ptr, ptr }, ptr %%clo, i32 0, i32 0
  store ptr @__kml_ws_write_settled, ptr %%c0, align 8
  %%c1 = getelementptr { ptr, ptr }, ptr %%clo, i32 0, i32 1
  store ptr %%env, ptr %%c1, align 8
  call void @__kml_promise_add_reaction(ptr %%wp, ptr %%clo)
  ret void
maybeClose:
  %%crBit = and i64 %%f, 8
  %%closeReq = icmp ne i64 %%crBit, 0
  br i1 %%closeReq, label %%doClose, label %%ret
doClose:
  %%f5 = or i64 %%f, 2
  store i64 %%f5, ptr %%fl_p, align 8
  %%cc_p = getelementptr %s, ptr %%s, i32 0, i32 10
  %%cc = load ptr, ptr %%cc_p, align 8
  %%noclose = icmp eq ptr %%cc, null
  br i1 %%noclose, label %%closeSync, label %%invokeClose
invokeClose:
  %%cfp_p = getelementptr { ptr, ptr }, ptr %%cc, i32 0, i32 0
  %%cfp = load ptr, ptr %%cfp_p, align 8
  %%cep_p = getelementptr { ptr, ptr }, ptr %%cc, i32 0, i32 1
  %%cep = load ptr, ptr %%cep_p, align 8
  %%cp2 = call ptr %%cfp(ptr %%cep)
  %%csync = icmp eq ptr %%cp2, null
  br i1 %%csync, label %%closeSync, label %%closeAsync
closeSync:
  call void @__kml_ws_finish_close(ptr %%s)
  ret void
closeAsync:
  %%env2 = call ptr @malloc(i64 16)
  %%f0 = getelementptr { ptr, ptr }, ptr %%env2, i32 0, i32 0
  store ptr %%cp2, ptr %%f0, align 8
  %%f1 = getelementptr { ptr, ptr }, ptr %%env2, i32 0, i32 1
  store ptr %%s, ptr %%f1, align 8
  %%clo2 = call ptr @malloc(i64 16)
  %%g0 = getelementptr { ptr, ptr }, ptr %%clo2, i32 0, i32 0
  store ptr @__kml_ws_close_settled, ptr %%g0, align 8
  %%g1 = getelementptr { ptr, ptr }, ptr %%clo2, i32 0, i32 1
  store ptr %%env2, ptr %%g1, align 8
  call void @__kml_promise_add_reaction(ptr %%cp2, ptr %%clo2)
  ret void
ret:
  ret void
}

define void @__kml_ws_write_settled(ptr %%env) {
entry:
  %%e0 = getelementptr ptr, ptr %%env, i64 0
  %%wp = load ptr, ptr %%e0, align 8
  %%e1 = getelementptr ptr, ptr %%env, i64 1
  %%s = load ptr, ptr %%e1, align 8
  %%e2 = getelementptr ptr, ptr %%env, i64 2
  %%wprom = load ptr, ptr %%e2, align 8
  %%pst_p = getelementptr %s, ptr %%wp, i32 0, i32 0
  %%pst = load i64, ptr %%pst_p, align 8
  %%rejected = icmp eq i64 %%pst, 2
  br i1 %%rejected, label %%err, label %%ok
err:
  %%pv0_p = getelementptr %s, ptr %%wp, i32 0, i32 2
  %%pv0 = load i64, ptr %%pv0_p, align 8
  %%wv0_p = getelementptr %s, ptr %%wprom, i32 0, i32 2
  store i64 %%pv0, ptr %%wv0_p, align 8
  call void @__kml_promise_settle(ptr %%wprom, i64 2)
  call void @__kml_ws_error(ptr %%s, i64 %%pv0)
  ret void
ok:
  call void @__kml_promise_settle(ptr %%wprom, i64 1)
  %%fl_p = getelementptr %s, ptr %%s, i32 0, i32 12
  %%f = load i64, ptr %%fl_p, align 8
  %%f2 = and i64 %%f, -3
  store i64 %%f2, ptr %%fl_p, align 8
  call void @__kml_ws_advance(ptr %%s)
  ret void
}

define void @__kml_ws_close_settled(ptr %%env) {
entry:
  %%f0 = getelementptr { ptr, ptr }, ptr %%env, i32 0, i32 0
  %%cp = load ptr, ptr %%f0, align 8
  %%f1 = getelementptr { ptr, ptr }, ptr %%env, i32 0, i32 1
  %%s = load ptr, ptr %%f1, align 8
  %%pst_p = getelementptr %s, ptr %%cp, i32 0, i32 0
  %%pst = load i64, ptr %%pst_p, align 8
  %%rejected = icmp eq i64 %%pst, 2
  br i1 %%rejected, label %%err, label %%ok
err:
  %%pv0_p = getelementptr %s, ptr %%cp, i32 0, i32 2
  %%pv0 = load i64, ptr %%pv0_p, align 8
  call void @__kml_ws_error(ptr %%s, i64 %%pv0)
  ret void
ok:
  call void @__kml_ws_finish_close(ptr %%s)
  ret void
}

define void @__kml_ws_finish_close(ptr %%s) {
entry:
  %%st_p = getelementptr %s, ptr %%s, i32 0, i32 0
  store i64 1, ptr %%st_p, align 8
  %%fl_p = getelementptr %s, ptr %%s, i32 0, i32 12
  %%f = load i64, ptr %%fl_p, align 8
  %%f2 = and i64 %%f, -3
  store i64 %%f2, ptr %%fl_p, align 8
  %%cp_p = getelementptr %s, ptr %%s, i32 0, i32 14
  %%cp = load ptr, ptr %%cp_p, align 8
  call void @__kml_promise_settle(ptr %%cp, i64 1)
  %%clp_p = getelementptr %s, ptr %%s, i32 0, i32 15
  %%clp = load ptr, ptr %%clp_p, align 8
  %%noclp = icmp eq ptr %%clp, null
  br i1 %%noclp, label %%ret, label %%settleClp
settleClp:
  call void @__kml_promise_settle(ptr %%clp, i64 1)
  ret void
ret:
  ret void
}`, ws, ws, ws, ws, ws, ws, ws, ws, p, p, p, ws, p, p, ws, ws, ws, ws))

	// __kml_ws_error: move to errored — reject every queued write promise,
	// the ready and closed promises, and any pending close() promise.
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_ws_error(ptr %%s, i64 %%err) {
entry:
  %%st_p = getelementptr %s, ptr %%s, i32 0, i32 0
  %%st = load i64, ptr %%st_p, align 8
  %%writable = icmp eq i64 %%st, 0
  br i1 %%writable, label %%do, label %%ret
do:
  store i64 2, ptr %%st_p, align 8
  %%err_p = getelementptr %s, ptr %%s, i32 0, i32 1
  store i64 %%err, ptr %%err_p, align 8
  %%qh_p = getelementptr %s, ptr %%s, i32 0, i32 4
  %%ql_p = getelementptr %s, ptr %%s, i32 0, i32 5
  %%qd_p = getelementptr %s, ptr %%s, i32 0, i32 2
  %%d = load ptr, ptr %%qd_p, align 8
  br label %%loop
loop:
  %%qh = load i64, ptr %%qh_p, align 8
  %%ql = load i64, ptr %%ql_p, align 8
  %%have = icmp slt i64 %%qh, %%ql
  br i1 %%have, label %%rejectOne, label %%drained
rejectOne:
  %%off = mul i64 %%qh, 32
  %%slot = getelementptr i8, ptr %%d, i64 %%off
  %%s3 = getelementptr i8, ptr %%slot, i64 24
  %%wprom = load ptr, ptr %%s3, align 8
  %%wv0_p = getelementptr %s, ptr %%wprom, i32 0, i32 2
  store i64 %%err, ptr %%wv0_p, align 8
  call void @__kml_promise_settle(ptr %%wprom, i64 2)
  %%nh = add i64 %%qh, 1
  store i64 %%nh, ptr %%qh_p, align 8
  br label %%loop
drained:
  store i64 0, ptr %%qh_p, align 8
  store i64 0, ptr %%ql_p, align 8
  %%tot_p = getelementptr %s, ptr %%s, i32 0, i32 6
  store double 0.0, ptr %%tot_p, align 8
  %%rdy_p = getelementptr %s, ptr %%s, i32 0, i32 13
  %%rdy = load ptr, ptr %%rdy_p, align 8
  %%rv0_p = getelementptr %s, ptr %%rdy, i32 0, i32 2
  store i64 %%err, ptr %%rv0_p, align 8
  call void @__kml_promise_settle(ptr %%rdy, i64 2)
  %%cp_p = getelementptr %s, ptr %%s, i32 0, i32 14
  %%cp = load ptr, ptr %%cp_p, align 8
  %%cv0_p = getelementptr %s, ptr %%cp, i32 0, i32 2
  store i64 %%err, ptr %%cv0_p, align 8
  call void @__kml_promise_settle(ptr %%cp, i64 2)
  %%clp_p = getelementptr %s, ptr %%s, i32 0, i32 15
  %%clp = load ptr, ptr %%clp_p, align 8
  %%noclp = icmp eq ptr %%clp, null
  br i1 %%noclp, label %%ret, label %%rejClp
rejClp:
  %%lv0_p = getelementptr %s, ptr %%clp, i32 0, i32 2
  store i64 %%err, ptr %%lv0_p, align 8
  call void @__kml_promise_settle(ptr %%clp, i64 2)
  ret void
ret:
  ret void
}`, ws, ws, ws, ws, ws, p, ws, ws, p, ws, p, ws, p))

	// __kml_ws_write(s, v0, v1) → the per-write promise.
	e.emitGlobal(fmt.Sprintf(`
define ptr @__kml_ws_write(ptr %%s, i64 %%v0, i64 %%v1) {
entry:
  %%prom = call ptr @__kml_task_alloc_promise()
  %%st_p = getelementptr %s, ptr %%s, i32 0, i32 0
  %%st = load i64, ptr %%st_p, align 8
  %%errored = icmp eq i64 %%st, 2
  br i1 %%errored, label %%rejErr, label %%ck1
rejErr:
  %%err_p = getelementptr %s, ptr %%s, i32 0, i32 1
  %%err = load i64, ptr %%err_p, align 8
  %%pv0_p = getelementptr %s, ptr %%prom, i32 0, i32 2
  store i64 %%err, ptr %%pv0_p, align 8
  call void @__kml_promise_settle(ptr %%prom, i64 2)
  ret ptr %%prom
ck1:
  %%closed = icmp eq i64 %%st, 1
  %%fl_p = getelementptr %s, ptr %%s, i32 0, i32 12
  %%f = load i64, ptr %%fl_p, align 8
  %%crBit = and i64 %%f, 8
  %%closeReq = icmp ne i64 %%crBit, 0
  %%noWrite = or i1 %%closed, %%closeReq
  br i1 %%noWrite, label %%rejClosed, label %%go
rejClosed:
  ret ptr null
go:
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
  call void @__kml_ws_qpush(ptr %%s, i64 %%v0, i64 %%v1, double %%sz, ptr %%prom)
  call void @__kml_ws_advance(ptr %%s)
  ret ptr %%prom
}`, ws, ws, p, ws, ws))

	// __kml_ws_close(s) → the close promise (null when close is not allowed).
	e.emitGlobal(fmt.Sprintf(`
define ptr @__kml_ws_close(ptr %%s) {
entry:
  %%st_p = getelementptr %s, ptr %%s, i32 0, i32 0
  %%st = load i64, ptr %%st_p, align 8
  %%errored = icmp eq i64 %%st, 2
  br i1 %%errored, label %%rejErr, label %%ck1
rejErr:
  %%prom0 = call ptr @__kml_task_alloc_promise()
  %%err_p = getelementptr %s, ptr %%s, i32 0, i32 1
  %%err = load i64, ptr %%err_p, align 8
  %%pv0_p = getelementptr %s, ptr %%prom0, i32 0, i32 2
  store i64 %%err, ptr %%pv0_p, align 8
  call void @__kml_promise_settle(ptr %%prom0, i64 2)
  ret ptr %%prom0
ck1:
  %%closed = icmp eq i64 %%st, 1
  %%fl_p = getelementptr %s, ptr %%s, i32 0, i32 12
  %%f = load i64, ptr %%fl_p, align 8
  %%crBit = and i64 %%f, 8
  %%closeReq = icmp ne i64 %%crBit, 0
  %%bad = or i1 %%closed, %%closeReq
  br i1 %%bad, label %%no, label %%go
no:
  ret ptr null
go:
  %%prom = call ptr @__kml_task_alloc_promise()
  %%clp_p = getelementptr %s, ptr %%s, i32 0, i32 15
  store ptr %%prom, ptr %%clp_p, align 8
  %%f2 = or i64 %%f, 8
  store i64 %%f2, ptr %%fl_p, align 8
  call void @__kml_ws_advance(ptr %%s)
  ret ptr %%prom
}`, ws, ws, p, ws, ws))

	// __kml_ws_abort(s, reason) → the abort promise: error the stream with
	// the reason, run the sink's abort, fulfill when it completes.
	e.emitGlobal(fmt.Sprintf(`
define ptr @__kml_ws_abort(ptr %%s, i64 %%reason) {
entry:
  %%st_p = getelementptr %s, ptr %%s, i32 0, i32 0
  %%st = load i64, ptr %%st_p, align 8
  %%writable = icmp eq i64 %%st, 0
  br i1 %%writable, label %%do, label %%already
already:
  %%ap = call ptr @__kml_task_alloc_promise()
  call void @__kml_promise_settle(ptr %%ap, i64 1)
  ret ptr %%ap
do:
  call void @__kml_ws_error(ptr %%s, i64 %%reason)
  %%ab_p = getelementptr %s, ptr %%s, i32 0, i32 11
  %%ac = load ptr, ptr %%ab_p, align 8
  %%nocb = icmp eq ptr %%ac, null
  br i1 %%nocb, label %%plain, label %%invoke
invoke:
  %%afp_p = getelementptr { ptr, ptr }, ptr %%ac, i32 0, i32 0
  %%afp = load ptr, ptr %%afp_p, align 8
  %%aep_p = getelementptr { ptr, ptr }, ptr %%ac, i32 0, i32 1
  %%aep = load ptr, ptr %%aep_p, align 8
  %%p2 = call ptr %%afp(ptr %%aep, i64 %%reason)
  %%sync = icmp eq ptr %%p2, null
  br i1 %%sync, label %%plain, label %%prop
prop:
  ret ptr %%p2
plain:
  %%fp2 = call ptr @__kml_task_alloc_promise()
  call void @__kml_promise_settle(ptr %%fp2, i64 1)
  ret ptr %%fp2
}`, ws, ws))

	// __kml_ws_desired / __kml_ws_started / lock / unlock.
	e.emitGlobal(fmt.Sprintf(`
define double @__kml_ws_desired(ptr %%s) {
entry:
  %%st_p = getelementptr %s, ptr %%s, i32 0, i32 0
  %%st = load i64, ptr %%st_p, align 8
  %%writable = icmp eq i64 %%st, 0
  br i1 %%writable, label %%calc, label %%zero
calc:
  %%hwm_p = getelementptr %s, ptr %%s, i32 0, i32 7
  %%tot_p = getelementptr %s, ptr %%s, i32 0, i32 6
  %%hwm = load double, ptr %%hwm_p, align 8
  %%tot = load double, ptr %%tot_p, align 8
  %%d = fsub double %%hwm, %%tot
  ret double %%d
zero:
  ret double 0.0
}

define void @__kml_ws_started(ptr %%s) {
entry:
  %%fl_p = getelementptr %s, ptr %%s, i32 0, i32 12
  %%f = load i64, ptr %%fl_p, align 8
  %%f2 = or i64 %%f, 1
  store i64 %%f2, ptr %%fl_p, align 8
  call void @__kml_ws_advance(ptr %%s)
  ret void
}

define i64 @__kml_ws_lock(ptr %%s) {
entry:
  %%fl_p = getelementptr %s, ptr %%s, i32 0, i32 12
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

define void @__kml_ws_unlock(ptr %%s) {
entry:
  %%fl_p = getelementptr %s, ptr %%s, i32 0, i32 12
  %%f = load i64, ptr %%fl_p, align 8
  %%f2 = and i64 %%f, -33
  store i64 %%f2, ptr %%fl_p, align 8
  ret void
}`, ws, ws, ws, ws, ws, ws))
}
