// runtime_http_reqbody.go — TDD-00097 Stage 5b: streaming http request
// bodies. When a program calls `req.stream()`, the dispatcher hands the
// handler its request at headers-complete and body bytes flow through a
// per-request body context instead of being fully buffered first (lifting
// the 10 MiB request cap for bodies):
//
//	%kml.reqbody (48 B): 0 fd i64 · 1 remaining i64 (bytes still on the
//	wire) · 2 bodyBuf ptr (the full-Content-Length buffer `req.body`
//	points at) · 3 filled i64 (bytes already in bodyBuf) · 4 stream ptr
//	(the rstream once `req.stream()` was called, else null) · 5 active i64
//	(registered in the pump scan).
//
// Three feeders share the context:
//   - __kml_reqbody_pull — the stream's pull hook: read a chunk from the
//     socket, yielding the connection fiber on EAGAIN (the event loop's
//     readable-scan resumes it, exactly like the request-read phase's own
//     yield) so other connections keep progressing.
//   - __kml_reqbody_pump — called each event-loop iteration for streams
//     consumed *outside* the fiber (e.g. `body: req.stream()` echoed into a
//     chunked response, whose writer runs on microtask reactions).
//   - __kml_reqbody_drain — `.body`/`.bodyBytes()` access: complete bodyBuf
//     in place (it is pre-allocated at full Content-Length), fiber-yielding
//     the same way.
package llvm

import "fmt"

const reqbodyStructIR = "{ i64, i64, ptr, i64, ptr, i64 }"

func (e *Emitter) ensureReqBodyRuntime() {
	if e.usedReqBodyRuntime {
		return
	}
	e.usedReqBodyRuntime = true
	e.ensureStreamRuntime()
	e.ensureMalloc()
	e.ensureRealloc()
	e.ensureMemcpy()
	e.ensureErrnoAccessor()
	e.ensureCurrentTaskGlobal()
	e.ensureExceptionHelpers() // __kml_reqbody_drain throws a TypeError when disturbed

	rb := reqbodyStructIR
	rs := rstreamStructIR
	earlyEndMsg := e.internString("unexpected end of request body")
	errName := e.internString("Error")
	// TDD-00097 Stage 5b follow-up (ADR-00362): reading req.body/.bodyBytes()
	// after req.stream() has consumed the body throws, matching the WHATWG
	// "body already disturbed" TypeError rather than silently returning the
	// pre-stream prefix.
	disturbedMsg := e.internString("request body already consumed by req.stream()")
	typeErrName := e.internString("TypeError")

	e.emitGlobal("@__kml_reqbody_data = internal thread_local global ptr null, align 8")
	e.emitGlobal("@__kml_reqbody_len = internal thread_local global i64 0, align 8")
	e.emitGlobal("@__kml_reqbody_cap = internal thread_local global i64 0, align 8")

	// __kml_reqbody_register: add a context to the pump scan.
	e.emitGlobal(`
define void @__kml_reqbody_register(ptr %ctx) {
entry:
  %len = load i64, ptr @__kml_reqbody_len, align 8
  %cap = load i64, ptr @__kml_reqbody_cap, align 8
  %need = add i64 %len, 1
  %grow = icmp sgt i64 %need, %cap
  br i1 %grow, label %dogrow, label %app
dogrow:
  %data = load ptr, ptr @__kml_reqbody_data, align 8
  %cap2 = mul i64 %cap, 2
  %ge8 = icmp sgt i64 %cap2, 8
  %nc = select i1 %ge8, i64 %cap2, i64 8
  %bytes = mul i64 %nc, 8
  %nd = call ptr @realloc(ptr %data, i64 %bytes)
  store ptr %nd, ptr @__kml_reqbody_data, align 8
  store i64 %nc, ptr @__kml_reqbody_cap, align 8
  br label %app
app:
  %d = load ptr, ptr @__kml_reqbody_data, align 8
  %slot = getelementptr ptr, ptr %d, i64 %len
  store ptr %ctx, ptr %slot, align 8
  %nl = add i64 %len, 1
  store i64 %nl, ptr @__kml_reqbody_len, align 8
  ret void
}`)

	// __kml_reqbody_finish(ctx, err): close (or error) the stream and
	// deactivate the context.
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_reqbody_finish(ptr %%ctx, i64 %%err) {
entry:
  %%act_p = getelementptr %s, ptr %%ctx, i32 0, i32 5
  store i64 0, ptr %%act_p, align 8
  %%st_p = getelementptr %s, ptr %%ctx, i32 0, i32 4
  %%stream = load ptr, ptr %%st_p, align 8
  %%nostream = icmp eq ptr %%stream, null
  br i1 %%nostream, label %%ret, label %%ck
ck:
  %%failed = icmp ne i64 %%err, 0
  br i1 %%failed, label %%doerr, label %%doclose
doerr:
  call void @__kml_rs_error(ptr %%stream, i64 %%err)
  ret void
doclose:
  %%ign = call i64 @__kml_rs_close(ptr %%stream)
  ret void
ret:
  ret void
}`, rb, rb))

	// __kml_reqbody_read_step(ctx) -> i64: one read attempt into the stream.
	// 1 = made progress (or finished), 0 = EAGAIN (no data right now),
	// -1 = finished/error (stream closed or errored).
	e.emitGlobal(fmt.Sprintf(`
define i64 @__kml_reqbody_read_step(ptr %%ctx) {
entry:
  %%rem_p = getelementptr %s, ptr %%ctx, i32 0, i32 1
  %%rem = load i64, ptr %%rem_p, align 8
  %%done = icmp sle i64 %%rem, 0
  br i1 %%done, label %%finish, label %%readit
finish:
  call void @__kml_reqbody_finish(ptr %%ctx, i64 0)
  ret i64 -1
readit:
  %%fd_p = getelementptr %s, ptr %%ctx, i32 0, i32 0
  %%fd64 = load i64, ptr %%fd_p, align 8
  %%fd = trunc i64 %%fd64 to i32
  %%capBig = icmp sgt i64 %%rem, 65536
  %%n0 = select i1 %%capBig, i64 65536, i64 %%rem
  %%buf = call ptr @malloc(i64 %%n0)
  %%n = call i64 @read(i32 %%fd, ptr %%buf, i64 %%n0)
  %%got = icmp sgt i64 %%n, 0
  br i1 %%got, label %%enq, label %%ckerr
enq:
  ; Decrement remaining BEFORE the enqueue: rs_enqueue re-enters this
  ; function through the stream's pull hook, and a stale post-enqueue store
  ; here would clobber every nested decrement (found as ~450 KB of request
  ; body silently unaccounted on a 12 MiB upload). Reload after the enqueue
  ; for the completion check, for the same reason.
  %%nrem0 = sub i64 %%rem, %%n
  store i64 %%nrem0, ptr %%rem_p, align 8
  %%st_p = getelementptr %s, ptr %%ctx, i32 0, i32 4
  %%stream = load ptr, ptr %%st_p, align 8
  %%bits = ptrtoint ptr %%buf to i64
  %%ign = call i64 @__kml_rs_enqueue(ptr %%stream, i64 %%bits, i64 %%n)
  %%nrem = load i64, ptr %%rem_p, align 8
  %%allDone = icmp sle i64 %%nrem, 0
  br i1 %%allDone, label %%finish2, label %%progress
finish2:
  call void @__kml_reqbody_finish(ptr %%ctx, i64 0)
  ret i64 1
progress:
  ret i64 1
ckerr:
  %%isZero = icmp eq i64 %%n, 0
  br i1 %%isZero, label %%earlyend, label %%ckagain
ckagain:
  %%errnoPtr = call ptr @%s()
  %%errnoVal = load i32, ptr %%errnoPtr, align 4
  %%isEagain = icmp eq i32 %%errnoVal, %d
  br i1 %%isEagain, label %%again, label %%earlyend
again:
  ret i64 0
earlyend:
  %%eo = call ptr @malloc(i64 24)
  %%eo_kind = getelementptr { i64, ptr, ptr }, ptr %%eo, i32 0, i32 0
  store i64 0, ptr %%eo_kind, align 8
  %%eo_msg = getelementptr { i64, ptr, ptr }, ptr %%eo, i32 0, i32 1
  store ptr %s, ptr %%eo_msg, align 8
  %%eo_name = getelementptr { i64, ptr, ptr }, ptr %%eo, i32 0, i32 2
  store ptr %s, ptr %%eo_name, align 8
  %%ebits = ptrtoint ptr %%eo to i64
  call void @__kml_reqbody_finish(ptr %%ctx, i64 %%ebits)
  ret i64 -1
}`, rb, rb, rb, errnoAccessor(), httpEagainErrno(), earlyEndMsg, errName))

	// __kml_reqbody_yield_fiber(): park the current connection fiber until
	// the event loop's readable-scan resumes it — the same slot/swapcontext
	// pattern the request-read phase uses (recomputing the slot each time,
	// since the connection array may be realloc-moved while suspended).
	e.emitGlobal(`
define void @__kml_reqbody_yield_fiber() {
entry:
  %idx = load i64, ptr @__kml_current_conn_idx, align 8
  %data = load ptr, ptr @__kml_conn_data, align 8
  %slot = getelementptr { i64, ptr, ptr, ptr, ptr }, ptr %data, i64 %idx
  %ctx_p = getelementptr { i64, ptr, ptr, ptr, ptr }, ptr %slot, i32 0, i32 1
  %ctx = load ptr, ptr %ctx_p, align 8
  %ign = call i32 @swapcontext(ptr %ctx, ptr @__kml_main_ctx)
  ret void
}`)

	// __kml_reqbody_pull — the stream's pull hook. On a connection fiber it
	// loops read → yield-on-EAGAIN until at least one chunk lands (so a
	// fiber-side `for await` always makes progress); elsewhere a single
	// attempt (the pump keeps loop-context consumers fed).
	e.emitGlobal(fmt.Sprintf(`
define ptr @__kml_reqbody_pull(ptr %%ctx) {
entry:
  %%act_p = getelementptr %s, ptr %%ctx, i32 0, i32 5
  %%act = load i64, ptr %%act_p, align 8
  %%inactive = icmp eq i64 %%act, 0
  br i1 %%inactive, label %%ret, label %%tryread
tryread:
  %%r = call i64 @__kml_reqbody_read_step(ptr %%ctx)
  %%eagain = icmp eq i64 %%r, 0
  br i1 %%eagain, label %%ckfiber, label %%ret
ckfiber:
  ; Yield only when actually running on the connection fiber's own stack —
  ; a coroutine task spawned from the handler still sees the connection's
  ; @__kml_current_conn_idx, but yielding through the conn slot from a task
  ; stack would clobber the fiber's saved context (found as a wild-jump
  ; crash on multi-chunk uploads). On a task, return EAGAIN: the parked
  ; read is fed by the event loop's pump.
  %%ct = load ptr, ptr @__kml_current_task, align 8
  %%ontask = icmp ne ptr %%ct, null
  br i1 %%ontask, label %%ret, label %%ckconn
ckconn:
  %%idx = load i64, ptr @__kml_current_conn_idx, align 8
  %%onfiber = icmp sge i64 %%idx, 0
  br i1 %%onfiber, label %%yield, label %%ret
yield:
  call void @__kml_reqbody_yield_fiber()
  br label %%tryread
ret:
  ret ptr null
}`, rb))

	// __kml_reqbody_pump() — one scan over the registered contexts: feed any
	// active stream whose queue has room (or a parked read) with whatever
	// bytes are available right now.
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_reqbody_pump() {
entry:
  %%len = load i64, ptr @__kml_reqbody_len, align 8
  %%data = load ptr, ptr @__kml_reqbody_data, align 8
  br label %%cond
cond:
  %%i = phi i64 [ 0, %%entry ], [ %%inext, %%next ]
  %%go = icmp slt i64 %%i, %%len
  br i1 %%go, label %%body, label %%done
body:
  %%slot = getelementptr ptr, ptr %%data, i64 %%i
  %%ctx = load ptr, ptr %%slot, align 8
  %%act_p = getelementptr %s, ptr %%ctx, i32 0, i32 5
  %%act = load i64, ptr %%act_p, align 8
  %%isact = icmp ne i64 %%act, 0
  br i1 %%isact, label %%ckwant, label %%next
ckwant:
  %%st_p = getelementptr %s, ptr %%ctx, i32 0, i32 4
  %%stream = load ptr, ptr %%st_p, align 8
  %%nostream = icmp eq ptr %%stream, null
  br i1 %%nostream, label %%next, label %%ckroom
ckroom:
  %%d = call double @__kml_rs_desired(ptr %%stream)
  %%hasRoom = fcmp ogt double %%d, 0.0
  %%rh_p = getelementptr %s, ptr %%stream, i32 0, i32 14
  %%rl_p = getelementptr %s, ptr %%stream, i32 0, i32 15
  %%rh = load i64, ptr %%rh_p, align 8
  %%rl = load i64, ptr %%rl_p, align 8
  %%rdPending = icmp slt i64 %%rh, %%rl
  %%want = or i1 %%hasRoom, %%rdPending
  br i1 %%want, label %%feed, label %%next
feed:
  %%ign = call i64 @__kml_reqbody_read_step(ptr %%ctx)
  br label %%next
next:
  %%inext = add i64 %%i, 1
  br label %%cond
done:
  ret void
}`, rb, rb, rs, rs))

	// __kml_reqbody_want() -> i1: does any active context still want bytes?
	// Folded into the event loop's select() timeout so a loop-context
	// consumer (e.g. a request body echoed into a chunked response) keeps
	// being fed even with no other wakeups pending.
	e.emitGlobal(fmt.Sprintf(`
define i1 @__kml_reqbody_want() {
entry:
  %%len = load i64, ptr @__kml_reqbody_len, align 8
  %%data = load ptr, ptr @__kml_reqbody_data, align 8
  br label %%cond
cond:
  %%i = phi i64 [ 0, %%entry ], [ %%inext, %%next ]
  %%go = icmp slt i64 %%i, %%len
  br i1 %%go, label %%body, label %%no
body:
  %%slot = getelementptr ptr, ptr %%data, i64 %%i
  %%ctx = load ptr, ptr %%slot, align 8
  %%act_p = getelementptr %s, ptr %%ctx, i32 0, i32 5
  %%act = load i64, ptr %%act_p, align 8
  %%isact = icmp ne i64 %%act, 0
  br i1 %%isact, label %%ckstream, label %%next
ckstream:
  %%st_p = getelementptr %s, ptr %%ctx, i32 0, i32 4
  %%stream = load ptr, ptr %%st_p, align 8
  %%nostream = icmp eq ptr %%stream, null
  br i1 %%nostream, label %%next, label %%ckroom
ckroom:
  %%d = call double @__kml_rs_desired(ptr %%stream)
  %%hasRoom = fcmp ogt double %%d, 0.0
  %%rh_p = getelementptr %s, ptr %%stream, i32 0, i32 14
  %%rl_p = getelementptr %s, ptr %%stream, i32 0, i32 15
  %%rh = load i64, ptr %%rh_p, align 8
  %%rl = load i64, ptr %%rl_p, align 8
  %%rdPending = icmp slt i64 %%rh, %%rl
  %%want = or i1 %%hasRoom, %%rdPending
  br i1 %%want, label %%yes, label %%next
next:
  %%inext = add i64 %%i, 1
  br label %%cond
yes:
  ret i1 1
no:
  ret i1 0
}`, rb, rb, rs, rs))

	// __kml_reqbody_stream(ctx, s) -> ptr: activate (or return the existing)
	// body stream: flush the already-buffered prefix as the first chunk,
	// close immediately when nothing remains, register for the pump.
	e.emitGlobal(fmt.Sprintf(`
define ptr @__kml_reqbody_stream(ptr %%ctx, ptr %%s) {
entry:
  %%st_p = getelementptr %s, ptr %%ctx, i32 0, i32 4
  %%existing = load ptr, ptr %%st_p, align 8
  %%have = icmp ne ptr %%existing, null
  br i1 %%have, label %%reuse, label %%activate
reuse:
  ret ptr %%existing
activate:
  store ptr %%s, ptr %%st_p, align 8
  %%filled_p = getelementptr %s, ptr %%ctx, i32 0, i32 3
  %%filled = load i64, ptr %%filled_p, align 8
  %%hasPre = icmp sgt i64 %%filled, 0
  br i1 %%hasPre, label %%flush, label %%ckdone
flush:
  %%buf_p = getelementptr %s, ptr %%ctx, i32 0, i32 2
  %%buf = load ptr, ptr %%buf_p, align 8
  %%bits = ptrtoint ptr %%buf to i64
  %%ign = call i64 @__kml_rs_enqueue(ptr %%s, i64 %%bits, i64 %%filled)
  br label %%ckdone
ckdone:
  %%rem_p = getelementptr %s, ptr %%ctx, i32 0, i32 1
  %%rem = load i64, ptr %%rem_p, align 8
  %%alldone = icmp sle i64 %%rem, 0
  br i1 %%alldone, label %%closenow, label %%register
closenow:
  %%ign2 = call i64 @__kml_rs_close(ptr %%s)
  ret ptr %%s
register:
  %%act_p = getelementptr %s, ptr %%ctx, i32 0, i32 5
  store i64 1, ptr %%act_p, align 8
  call void @__kml_reqbody_register(ptr %%ctx)
  ret ptr %%s
}`, rb, rb, rb, rb, rb))

	// __kml_reqbody_drain(ctx): complete bodyBuf in place for the buffered
	// accessors (`.body`/`.bodyBytes()`): read the remaining bytes straight
	// into the pre-allocated full-Content-Length buffer, fiber-yielding on
	// EAGAIN. A no-op once nothing remains or when the body went to a stream
	// (documented caveat: `.body` after `.stream()` is the pre-stream prefix).
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_reqbody_drain(ptr %%ctx) {
entry:
  %%noctx = icmp eq ptr %%ctx, null
  br i1 %%noctx, label %%ret, label %%ckstream
ckstream:
  %%st_p = getelementptr %s, ptr %%ctx, i32 0, i32 4
  %%stream = load ptr, ptr %%st_p, align 8
  %%streamed = icmp ne ptr %%stream, null
  br i1 %%streamed, label %%disturbed, label %%loop
loop:
  %%rem_p = getelementptr %s, ptr %%ctx, i32 0, i32 1
  %%rem = load i64, ptr %%rem_p, align 8
  %%done = icmp sle i64 %%rem, 0
  br i1 %%done, label %%ret, label %%readit
readit:
  %%fd_p = getelementptr %s, ptr %%ctx, i32 0, i32 0
  %%fd64 = load i64, ptr %%fd_p, align 8
  %%fd = trunc i64 %%fd64 to i32
  %%buf_p = getelementptr %s, ptr %%ctx, i32 0, i32 2
  %%buf = load ptr, ptr %%buf_p, align 8
  %%filled_p = getelementptr %s, ptr %%ctx, i32 0, i32 3
  %%filled = load i64, ptr %%filled_p, align 8
  %%dst = getelementptr i8, ptr %%buf, i64 %%filled
  %%n = call i64 @read(i32 %%fd, ptr %%dst, i64 %%rem)
  %%got = icmp sgt i64 %%n, 0
  br i1 %%got, label %%adv, label %%ckerr
adv:
  %%nf = add i64 %%filled, %%n
  store i64 %%nf, ptr %%filled_p, align 8
  %%nrem = sub i64 %%rem, %%n
  store i64 %%nrem, ptr %%rem_p, align 8
  %%term = getelementptr i8, ptr %%buf, i64 %%nf
  store i8 0, ptr %%term, align 1
  br label %%loop
ckerr:
  %%isZero = icmp eq i64 %%n, 0
  br i1 %%isZero, label %%ret, label %%ckagain
ckagain:
  %%errnoPtr = call ptr @%s()
  %%errnoVal = load i32, ptr %%errnoPtr, align 4
  %%isEagain = icmp eq i32 %%errnoVal, %d
  br i1 %%isEagain, label %%wait, label %%ret
wait:
  %%ct = load ptr, ptr @__kml_current_task, align 8
  %%ontask = icmp ne ptr %%ct, null
  br i1 %%ontask, label %%loop, label %%ckconn
ckconn:
  %%idx = load i64, ptr @__kml_current_conn_idx, align 8
  %%onfiber = icmp sge i64 %%idx, 0
  br i1 %%onfiber, label %%yield, label %%loop
yield:
  call void @__kml_reqbody_yield_fiber()
  br label %%loop
disturbed:
  %%do = call ptr @malloc(i64 24)
  %%do_kind = getelementptr { i64, ptr, ptr }, ptr %%do, i32 0, i32 0
  store i64 0, ptr %%do_kind, align 8
  %%do_msg = getelementptr { i64, ptr, ptr }, ptr %%do, i32 0, i32 1
  store ptr %s, ptr %%do_msg, align 8
  %%do_name = getelementptr { i64, ptr, ptr }, ptr %%do, i32 0, i32 2
  store ptr %s, ptr %%do_name, align 8
  call void @__kml_throw(ptr %%do)
  unreachable
ret:
  ret void
}`, rb, rb, rb, rb, rb, errnoAccessor(), httpEagainErrno(), disturbedMsg, typeErrName))
}
