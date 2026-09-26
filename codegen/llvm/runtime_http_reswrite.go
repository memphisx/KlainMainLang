// runtime_http_reswrite.go — TDD-00195 Stage 2: the incremental `res` Writable.
//
// A server `res` is a ServerResponseType value object whose {status, body,
// headers} the dispatcher normally flushes AFTER the handler returns (one
// buffered write, Content-Length framed). This file makes the *first* res.write
// (or a req.pipe(res)) take the connection fd over for chunked transfer instead:
// __kml_res_begin sends the chunked head and builds a WHATWG writable
// (__kml_ws_alloc) whose sink frames each chunk straight to the socket, so
// res.write streams mid-handler. res.end closes the sink (terminal chunk); the
// dispatcher tail then re-arms the connection (keep-alive) or retires it,
// reading __kml_streaming / __kml_keepalive off the response — see
// buildHTTPDispatcher's streamed-response branch.
//
// The sink write/close are the same {fp,env} closure shape __kml_ws_advance
// invokes, so req.pipe(res) reuses __kml_pipe_to unchanged with res.__kml_wsink
// as its destination writable. Writes are blocking (O_NONBLOCK cleared for the
// stream's lifetime, restored on keep-alive re-arm), matching the buffered path
// and the returned-body %kml.hws writer — a slow client blocks this connection's
// fiber rather than applying socket-level backpressure (a server-wide property,
// not a res-specific gap).
package llvm

import (
	"fmt"
	"strings"
)

func (e *Emitter) ensureResStreamRuntime() {
	if e.usedResStreamRuntime {
		return
	}
	e.usedResStreamRuntime = true
	e.ensureMalloc()
	e.ensureFree()
	e.ensureStrlen()
	e.ensureRealloc()
	e.ensureMemcpy()
	e.ensureMemmove()
	e.ensureSprintf()
	e.ensureHTTPStreamRuntime()     // __kml_http_send_stream_head + fcntl/write decls
	e.ensureHTTPSerializeHeaders()  // __kml_http_serialize_headers
	e.ensureHTTPKeepAliveDecision() // __kml_http_keepalive_decision
	e.ensureWStreamRuntime()        // __kml_ws_alloc / _write / _close / _started
	e.ensureConnPokeGlobal()
	e.ensureErrnoAccessor() // EAGAIN detection in the park-aware write helper

	srt := ServerResponseType()
	idx := func(f string) int { i, _, _ := srt.FieldIndex(f); return i }
	res := srt.StructIR()

	chunkFmt := e.internString("%llx\r\n")
	crlf := e.internString("\r\n")
	terminator := e.internString("0\r\n\r\n")

	tmpl := `
; __kml_res_write_all writes all %len bytes of %buf to %fd, handling short
; writes and — on a non-blocking socket (plain HTTP) — EAGAIN by parking THIS
; connection fiber on socket writability (TDD-00195 Stage 2 backpressure): it
; marks the fd in @__kml_conn_writepark and swapcontexts back to the reactor,
; which adds the fd to select()'s write fd_set and resumes the fiber when the
; kernel send buffer drains. A slow consumer thus parks one fiber instead of
; blocking the whole reactor in a synchronous write(). Over a blocking socket
; (HTTPS, where res_begin leaves O_NONBLOCK cleared) write() never returns
; EAGAIN, so the park path is inert and the loop is a plain blocking write.
define void @__kml_res_write_all(i32 %fd, ptr %buf, i64 %len) {
entry:
  %offp = alloca i64, align 8
  store i64 0, ptr %offp, align 8
  br label %loop
loop:
  %off = load i64, ptr %offp, align 8
  %rem = sub i64 %len, %off
  %more = icmp sgt i64 %rem, 0
  br i1 %more, label %dowrite, label %done
dowrite:
  %p = getelementptr i8, ptr %buf, i64 %off
  %n = call i64 @write(i32 %fd, ptr %p, i64 %rem)
  %wrote = icmp sgt i64 %n, 0
  br i1 %wrote, label %advance, label %checkerr
advance:
  %noff = add i64 %off, %n
  store i64 %noff, ptr %offp, align 8
  br label %loop
checkerr:
  %ep = call ptr @ERRNOFN()
  %ev = load i32, ptr %ep, align 4
  %eag = icmp eq i32 %ev, EAGAINNO
  br i1 %eag, label %parkchk, label %done
parkchk:
  ; Only a connection fiber can yield; off one (unreachable for res writes) a
  ; blocking retry is the safe fallback rather than a hang.
  %idx = load i64, ptr @__kml_current_conn_idx, align 8
  %onfiber = icmp sge i64 %idx, 0
  br i1 %onfiber, label %park, label %loop
park:
  ; Mark this fd write-parked (event loop adds it to the write fd_set), then
  ; yield to the reactor via swapcontext.
  %fd64 = sext i32 %fd to i64
  %div8 = sdiv i64 %fd64, 8
  %mod8 = srem i64 %fd64, 8
  %bp = getelementptr i8, ptr @__kml_conn_writepark, i64 %div8
  %mod8b = trunc i64 %mod8 to i8
  %mask = shl i8 1, %mod8b
  %ob = load i8, ptr %bp, align 1
  %nb = or i8 %ob, %mask
  store i8 %nb, ptr %bp, align 1
  %cd = load ptr, ptr @__kml_conn_data, align 8
  %si = load i64, ptr @__kml_current_conn_idx, align 8
  %slot = getelementptr { i64, ptr, ptr, ptr, ptr }, ptr %cd, i64 %si
  %ctxp = getelementptr { i64, ptr, ptr, ptr, ptr }, ptr %slot, i32 0, i32 1
  %ctx = load ptr, ptr %ctxp, align 8
  %sw = call i32 @swapcontext(ptr %ctx, ptr @__kml_main_ctx)
  ; Resumed: clear the park bit (re-set on the next iteration if still EAGAIN),
  ; then retry. @__kml_conn_writepark is a fixed global, so %bp/%mask survive.
  %ob2 = load i8, ptr %bp, align 1
  %clr = xor i8 %mask, -1
  %nb2 = and i8 %ob2, %clr
  store i8 %nb2, ptr %bp, align 1
  br label %loop
done:
  ret void
}

define void @__kml_res_begin(ptr %res, i1 %allowKA) {
entry:
  %stp = getelementptr RES, ptr %res, i32 0, i32 STREAMING
  %st = load i64, ptr %stp, align 8
  %begun = icmp ne i64 %st, 0
  br i1 %begun, label %done, label %go
go:
  %fdp = getelementptr RES, ptr %res, i32 0, i32 FD
  %fd64 = load i64, ptr %fdp, align 8
  %fd = trunc i64 %fd64 to i32
  %statusp = getelementptr RES, ptr %res, i32 0, i32 STATUS
  %status = load i64, ptr %statusp, align 8
  %rhp = getelementptr RES, ptr %res, i32 0, i32 REQHEADERS
  %reqh = load ptr, ptr %rhp, align 8
  %hp = getelementptr RES, ptr %res, i32 0, i32 HEADERS
  %hmap = load ptr, ptr %hp, align 8
  %ser = call ptr @__kml_http_serialize_headers(ptr %hmap)
  %kadec = call i1 @__kml_http_keepalive_decision(ptr %reqh, ptr %ser)
  %ka = and i1 %kadec, %allowKA
  %ka64 = zext i1 %ka to i64
  %kap = getelementptr RES, ptr %res, i32 0, i32 KEEPALIVE
  store i64 %ka64, ptr %kap, align 8
  ; Send the (tiny) chunked head over a blocking fd so it can't partial-write:
  ; clear O_NONBLOCK, send, then re-enable it (the injected block below) for the
  ; chunk-write lifetime on plain HTTP so slow-consumer chunk writes park
  ; instead of blocking the reactor (empty on HTTPS, chunk writes stay blocking).
  %fl = call i32 (i32, i32, ...) @fcntl(i32 %fd, i32 3)
  %flnb = and i32 %fl, NBCLEAR
  %ign0 = call i32 (i32, i32, ...) @fcntl(i32 %fd, i32 4, i32 %flnb)
  call void @__kml_http_send_stream_head(i32 %fd, i64 %status, ptr %ser, i1 %ka)
  RENONBLOCK
  %sink = call ptr @__kml_ws_alloc(double 6.5536e+04)
  %wclo = call ptr @malloc(i64 16)
  %wc0 = getelementptr { ptr, ptr }, ptr %wclo, i32 0, i32 0
  store ptr @__kml_res_sink_write, ptr %wc0, align 8
  %wc1 = getelementptr { ptr, ptr }, ptr %wclo, i32 0, i32 1
  store ptr %res, ptr %wc1, align 8
  %wf = getelementptr WS, ptr %sink, i32 0, i32 9
  store ptr %wclo, ptr %wf, align 8
  %cclo = call ptr @malloc(i64 16)
  %cc0 = getelementptr { ptr, ptr }, ptr %cclo, i32 0, i32 0
  store ptr @__kml_res_sink_close, ptr %cc0, align 8
  %cc1 = getelementptr { ptr, ptr }, ptr %cclo, i32 0, i32 1
  store ptr %res, ptr %cc1, align 8
  %cf = getelementptr WS, ptr %sink, i32 0, i32 10
  store ptr %cclo, ptr %cf, align 8
  %skp = getelementptr RES, ptr %res, i32 0, i32 WSINK
  store ptr %sink, ptr %skp, align 8
  store i64 1, ptr %stp, align 8
  call void @__kml_ws_started(ptr %sink)
  ret void
done:
  ret void
}

; The pipe sink frames its chunk into the SAME per-response output queue a direct
; res.write uses (res.OUTQ) and then blocking-drains it (__kml_res_flush parks on
; EAGAIN). Routing both paths through one queue keeps their bytes correctly
; ordered when a handler interleaves raw res.write with req.pipe(res) on one
; response (TDD-00195 Stage 2) — draining the whole queue in append order sends
; any earlier direct-write bytes before this pipe chunk. Flushing here (rather
; than returning after enqueue) preserves the pipe's drain-before-next-chunk
; contract (Layer A backpressure).
define ptr @__kml_res_sink_write(ptr %res, i64 %v0, i64 %v1) {
entry:
  %empty = icmp sle i64 %v1, 0
  br i1 %empty, label %skip, label %write
write:
  %data = inttoptr i64 %v0 to ptr
  %hdrbuf = call ptr @malloc(i64 32)
  %hn = call i32 (ptr, ptr, ...) @sprintf(ptr %hdrbuf, ptr CHUNKFMT, i64 %v1)
  %hn64 = sext i32 %hn to i64
  call void @__kml_res_qappend(ptr %res, ptr %hdrbuf, i64 %hn64)
  call void @free(ptr %hdrbuf)
  call void @__kml_res_qappend(ptr %res, ptr %data, i64 %v1)
  call void @__kml_res_qappend(ptr %res, ptr CRLF, i64 2)
  call void @__kml_res_flush(ptr %res)
  ret ptr null
skip:
  ret ptr null
}

define ptr @__kml_res_sink_close(ptr %res) {
entry:
  ; Drain any bytes a direct res.write left queued before the terminal chunk, so
  ; res.write + req.pipe(res) interleaved on one response close in order.
  call void @__kml_res_flush(ptr %res)
  %fdp = getelementptr RES, ptr %res, i32 0, i32 FD
  %fd64 = load i64, ptr %fdp, align 8
  %fd = trunc i64 %fd64 to i32
  call void @__kml_res_write_all(i32 %fd, ptr TERMINATOR, i64 5)
  ; Ensure O_NONBLOCK is set so a keep-alive re-arm's select-driven read loop
  ; yields correctly (plain HTTP already left it set for chunk writes; HTTPS
  ; needs it re-enabled here); harmless when the fd is about to be closed.
  %rfl = call i32 (i32, i32, ...) @fcntl(i32 %fd, i32 3)
  %rflnb = or i32 %rfl, NBSET
  %rign = call i32 (i32, i32, ...) @fcntl(i32 %fd, i32 4, i32 %rflnb)
  %stp = getelementptr RES, ptr %res, i32 0, i32 STREAMING
  store i64 2, ptr %stp, align 8
  %ep = getelementptr RES, ptr %res, i32 0, i32 ENDED
  store i64 1, ptr %ep, align 8
  store i8 1, ptr @__kml_conn_poke, align 1
  ret ptr null
}

; ────────────────────────────────────────────────────────────────────────────
; TDD-00214 — observable backpressure for DIRECT res.write. Unlike the sink path
; above (which req.pipe(res) drives, parking inline via __kml_res_write_all),
; a direct res.write frames its chunk into a per-response output queue
; (res.OUTQ, a { ptr buf, i64 head, i64 len, i64 cap, i64 needDrain }) and
; returns queued<=highWaterMark instead of blocking. The dispatcher-tail park
; loop and res.end drive __kml_res_drain, which flushes the queue (parking on
; EAGAIN, resumed on socket writability) and fires the 'drain' listener on this
; fiber once the queue empties after a write returned false.
; ────────────────────────────────────────────────────────────────────────────

; Ensure res.OUTQ exists, compact any drained prefix, then append %n bytes of
; %src to the unsent tail (growing the buffer geometrically).
define void @__kml_res_qappend(ptr %res, ptr %src, i64 %n) {
entry:
  %z = icmp sle i64 %n, 0
  br i1 %z, label %ret, label %ensure
ensure:
  %qp = getelementptr RES, ptr %res, i32 0, i32 OUTQ
  %q0 = load ptr, ptr %qp, align 8
  %noq = icmp eq ptr %q0, null
  br i1 %noq, label %alloc, label %have
alloc:
  %nq = call ptr @malloc(i64 40)
  %a_buf = getelementptr QST, ptr %nq, i32 0, i32 0
  store ptr null, ptr %a_buf, align 8
  %a_head = getelementptr QST, ptr %nq, i32 0, i32 1
  store i64 0, ptr %a_head, align 8
  %a_len = getelementptr QST, ptr %nq, i32 0, i32 2
  store i64 0, ptr %a_len, align 8
  %a_cap = getelementptr QST, ptr %nq, i32 0, i32 3
  store i64 0, ptr %a_cap, align 8
  %a_nd = getelementptr QST, ptr %nq, i32 0, i32 4
  store i64 0, ptr %a_nd, align 8
  store ptr %nq, ptr %qp, align 8
  br label %have
have:
  %q = load ptr, ptr %qp, align 8
  %bufp = getelementptr QST, ptr %q, i32 0, i32 0
  %headp = getelementptr QST, ptr %q, i32 0, i32 1
  %lenp = getelementptr QST, ptr %q, i32 0, i32 2
  %capp = getelementptr QST, ptr %q, i32 0, i32 3
  %head = load i64, ptr %headp, align 8
  %haveHead = icmp sgt i64 %head, 0
  br i1 %haveHead, label %compact, label %aftercompact
compact:
  %clen = load i64, ptr %lenp, align 8
  %rem = sub i64 %clen, %head
  %cbuf = load ptr, ptr %bufp, align 8
  %csrc = getelementptr i8, ptr %cbuf, i64 %head
  %mm = call ptr @memmove(ptr %cbuf, ptr %csrc, i64 %rem)
  store i64 0, ptr %headp, align 8
  store i64 %rem, ptr %lenp, align 8
  br label %aftercompact
aftercompact:
  %len = load i64, ptr %lenp, align 8
  %cap = load i64, ptr %capp, align 8
  %need = add i64 %len, %n
  %fits = icmp sle i64 %need, %cap
  br i1 %fits, label %copy, label %grow
grow:
  %c2 = mul i64 %cap, 2
  %useC2 = icmp sgt i64 %c2, %need
  %m1 = select i1 %useC2, i64 %c2, i64 %need
  %minok = icmp sgt i64 %m1, 4096
  %newcap = select i1 %minok, i64 %m1, i64 4096
  %oldbuf = load ptr, ptr %bufp, align 8
  %newbuf = call ptr @realloc(ptr %oldbuf, i64 %newcap)
  store ptr %newbuf, ptr %bufp, align 8
  store i64 %newcap, ptr %capp, align 8
  br label %copy
copy:
  %dbuf = load ptr, ptr %bufp, align 8
  %dlen = load i64, ptr %lenp, align 8
  %dst = getelementptr i8, ptr %dbuf, i64 %dlen
  %cp = call ptr @memcpy(ptr %dst, ptr %src, i64 %n)
  %nlen = add i64 %dlen, %n
  store i64 %nlen, ptr %lenp, align 8
  br label %ret
ret:
  ret void
}

; Best-effort non-blocking flush of the queue head; stops (leaving bytes queued)
; the moment write() would block or errors. Never parks. Resets to empty on a
; full flush.
define void @__kml_res_drain_try(ptr %res) {
entry:
  %qp = getelementptr RES, ptr %res, i32 0, i32 OUTQ
  %q = load ptr, ptr %qp, align 8
  %noq = icmp eq ptr %q, null
  br i1 %noq, label %ret, label %go
go:
  %fdp = getelementptr RES, ptr %res, i32 0, i32 FD
  %fd64 = load i64, ptr %fdp, align 8
  %fd = trunc i64 %fd64 to i32
  %bufp = getelementptr QST, ptr %q, i32 0, i32 0
  %headp = getelementptr QST, ptr %q, i32 0, i32 1
  %lenp = getelementptr QST, ptr %q, i32 0, i32 2
  br label %loop
loop:
  %head = load i64, ptr %headp, align 8
  %len = load i64, ptr %lenp, align 8
  %rem = sub i64 %len, %head
  %more = icmp sgt i64 %rem, 0
  br i1 %more, label %dowrite, label %empty
dowrite:
  %buf = load ptr, ptr %bufp, align 8
  %p = getelementptr i8, ptr %buf, i64 %head
  %n = call i64 @write(i32 %fd, ptr %p, i64 %rem)
  %wrote = icmp sgt i64 %n, 0
  br i1 %wrote, label %advance, label %ret
advance:
  %nh = add i64 %head, %n
  store i64 %nh, ptr %headp, align 8
  br label %loop
empty:
  store i64 0, ptr %headp, align 8
  store i64 0, ptr %lenp, align 8
  br label %ret
ret:
  ret void
}

; __kml_res_flush drains the whole queue, parking THIS connection fiber on
; socket writability at each EAGAIN (identical mechanism to __kml_res_write_all)
; and resetting to empty on success. On a terminal error (EPIPE — client gone)
; it drops the queue and forces the response ended (streaming=2) so the park
; loop exits and the connection retires. It does NOT fire 'drain' — res.end uses
; it to push queued bytes before the terminal chunk without a re-entrant listener.
define void @__kml_res_flush(ptr %res) {
entry:
  %qp = getelementptr RES, ptr %res, i32 0, i32 OUTQ
  %q = load ptr, ptr %qp, align 8
  %noq = icmp eq ptr %q, null
  br i1 %noq, label %ret, label %go
go:
  %fdp = getelementptr RES, ptr %res, i32 0, i32 FD
  %fd64 = load i64, ptr %fdp, align 8
  %fd = trunc i64 %fd64 to i32
  %bufp = getelementptr QST, ptr %q, i32 0, i32 0
  %headp = getelementptr QST, ptr %q, i32 0, i32 1
  %lenp = getelementptr QST, ptr %q, i32 0, i32 2
  br label %loop
loop:
  %head = load i64, ptr %headp, align 8
  %len = load i64, ptr %lenp, align 8
  %rem = sub i64 %len, %head
  %more = icmp sgt i64 %rem, 0
  br i1 %more, label %dowrite, label %drained
dowrite:
  %buf = load ptr, ptr %bufp, align 8
  %p = getelementptr i8, ptr %buf, i64 %head
  %n = call i64 @write(i32 %fd, ptr %p, i64 %rem)
  %ok = icmp sgt i64 %n, 0
  br i1 %ok, label %advance, label %checkerr
advance:
  %nh = add i64 %head, %n
  store i64 %nh, ptr %headp, align 8
  br label %loop
checkerr:
  %ep = call ptr @ERRNOFN()
  %ev = load i32, ptr %ep, align 4
  %eag = icmp eq i32 %ev, EAGAINNO
  br i1 %eag, label %parkchk, label %giveup
giveup:
  ; EPIPE / other terminal write error: the client is gone. Drop the queue and
  ; mark the response ended (streaming=2) so the dispatcher retires the fiber.
  store i64 0, ptr %headp, align 8
  store i64 0, ptr %lenp, align 8
  %gstp = getelementptr RES, ptr %res, i32 0, i32 STREAMING
  store i64 2, ptr %gstp, align 8
  %gep = getelementptr RES, ptr %res, i32 0, i32 ENDED
  store i64 1, ptr %gep, align 8
  ret void
parkchk:
  %cidx = load i64, ptr @__kml_current_conn_idx, align 8
  %onfiber = icmp sge i64 %cidx, 0
  br i1 %onfiber, label %park, label %loop
park:
  %pfd64 = sext i32 %fd to i64
  %div8 = sdiv i64 %pfd64, 8
  %mod8 = srem i64 %pfd64, 8
  %bp = getelementptr i8, ptr @__kml_conn_writepark, i64 %div8
  %mod8b = trunc i64 %mod8 to i8
  %mask = shl i8 1, %mod8b
  %ob = load i8, ptr %bp, align 1
  %nb = or i8 %ob, %mask
  store i8 %nb, ptr %bp, align 1
  %cd = load ptr, ptr @__kml_conn_data, align 8
  %si = load i64, ptr @__kml_current_conn_idx, align 8
  %slot = getelementptr { i64, ptr, ptr, ptr, ptr }, ptr %cd, i64 %si
  %ctxp = getelementptr { i64, ptr, ptr, ptr, ptr }, ptr %slot, i32 0, i32 1
  %ctx = load ptr, ptr %ctxp, align 8
  %sw = call i32 @swapcontext(ptr %ctx, ptr @__kml_main_ctx)
  %ob2 = load i8, ptr %bp, align 1
  %clr = xor i8 %mask, -1
  %nb2 = and i8 %ob2, %clr
  store i8 %nb2, ptr %bp, align 1
  br label %loop
drained:
  store i64 0, ptr %headp, align 8
  store i64 0, ptr %lenp, align 8
  ret void
ret:
  ret void
}

; __kml_res_drain (dispatcher-tail park loop) flushes the queue, then — once it
; empties after a write returned false (needDrain) and the response is not yet
; ended — fires the registered 'drain' listener on THIS fiber. The listener may
; res.write again (re-queued bytes are flushed by looping back, which re-parks
; on writability with the bit correctly set) or res.end (sets ended → the loop
; stops firing). Splitting flush from fire keeps res.end's own flush from
; re-entrantly firing 'drain' after the response has ended.
define void @__kml_res_drain(ptr %res) {
entry:
  %qp = getelementptr RES, ptr %res, i32 0, i32 OUTQ
  %q = load ptr, ptr %qp, align 8
  %noq = icmp eq ptr %q, null
  br i1 %noq, label %ret, label %loop
loop:
  call void @__kml_res_flush(ptr %res)
  ; res.end (or an EPIPE give-up) reached: stop — do not fire 'drain' after end.
  %ep = getelementptr RES, ptr %res, i32 0, i32 ENDED
  %ended = load i64, ptr %ep, align 8
  %isended = icmp ne i64 %ended, 0
  br i1 %isended, label %ret, label %chknd
chknd:
  %ndp = getelementptr QST, ptr %q, i32 0, i32 4
  %nd = load i64, ptr %ndp, align 8
  %needit = icmp ne i64 %nd, 0
  br i1 %needit, label %firedrain, label %ret
firedrain:
  store i64 0, ptr %ndp, align 8
  %cbp = getelementptr RES, ptr %res, i32 0, i32 DRAINCB
  %cb = load ptr, ptr %cbp, align 8
  %nocb = icmp eq ptr %cb, null
  br i1 %nocb, label %ret, label %invoke
invoke:
  %oncep = getelementptr RES, ptr %res, i32 0, i32 DRAINONCE
  %once = load i64, ptr %oncep, align 8
  %isonce = icmp ne i64 %once, 0
  br i1 %isonce, label %clearonce, label %docall
clearonce:
  ; res.once('drain'): unregister BEFORE invoking so a re-register inside the
  ; listener (the canonical write→false→once('drain') loop) survives.
  store ptr null, ptr %cbp, align 8
  store i64 0, ptr %oncep, align 8
  br label %docall
docall:
  %fp_p = getelementptr { ptr, ptr }, ptr %cb, i32 0, i32 0
  %fp = load ptr, ptr %fp_p, align 8
  %env_p = getelementptr { ptr, ptr }, ptr %cb, i32 0, i32 1
  %env = load ptr, ptr %env_p, align 8
  call void (ptr) %fp(ptr %env)
  ; Flush whatever the listener enqueued (re-parking with the write bit set) and
  ; re-check for another drain cycle.
  br label %loop
ret:
  ret void
}

; Frame one chunk into the queue (chunked transfer-encoding: <hexlen>CRLF data
; CRLF), attempt an immediate non-blocking flush, and return whether the still-
; queued size is at or below highWaterMark (Node's res.write backpressure bool).
; Crossing above the mark arms needDrain so the eventual empty fires 'drain'.
define i1 @__kml_res_qwrite(ptr %res, ptr %data, i64 %len) {
entry:
  %empty = icmp sle i64 %len, 0
  br i1 %empty, label %retTrue, label %frame
frame:
  %hdrbuf = call ptr @malloc(i64 32)
  %hn = call i32 (ptr, ptr, ...) @sprintf(ptr %hdrbuf, ptr CHUNKFMT, i64 %len)
  %hn64 = sext i32 %hn to i64
  call void @__kml_res_qappend(ptr %res, ptr %hdrbuf, i64 %hn64)
  call void @free(ptr %hdrbuf)
  call void @__kml_res_qappend(ptr %res, ptr %data, i64 %len)
  call void @__kml_res_qappend(ptr %res, ptr CRLF, i64 2)
  call void @__kml_res_drain_try(ptr %res)
  %qp = getelementptr RES, ptr %res, i32 0, i32 OUTQ
  %q = load ptr, ptr %qp, align 8
  %headp = getelementptr QST, ptr %q, i32 0, i32 1
  %lenp = getelementptr QST, ptr %q, i32 0, i32 2
  %head = load i64, ptr %headp, align 8
  %qlen = load i64, ptr %lenp, align 8
  %queued = sub i64 %qlen, %head
  ; ADR-00983: read the per-response backpressure threshold (createServer's
  ; { highWaterMark } option, or 16384 by default) rather than a baked constant.
  %hwmp = getelementptr RES, ptr %res, i32 0, i32 HWMIDX
  %hwm = load i64, ptr %hwmp, align 8
  %over = icmp sgt i64 %queued, %hwm
  br i1 %over, label %setnd, label %retq
setnd:
  %ndp = getelementptr QST, ptr %q, i32 0, i32 4
  store i64 1, ptr %ndp, align 8
  br label %retq
retq:
  %ok = icmp sle i64 %queued, %hwm
  ret i1 %ok
retTrue:
  ret i1 1
}`

	// After the (tiny, blocking) head send, re-enable O_NONBLOCK so slow-consumer
	// chunk writes park on writability rather than blocking the reactor. This
	// applies to HTTPS too (TDD-00195 Stage 2): the TLS chunk writes route
	// through __kml_http_conn_send_nb → __kml_tls_write_nb, which reports SSL
	// WANT as EAGAIN so __kml_res_write_all / _flush / _drain_try park exactly as
	// on plain HTTP.
	renonblock := fmt.Sprintf(
		"%%rebfl = call i32 (i32, i32, ...) @fcntl(i32 %%fd, i32 3)\n"+
			"  %%rebnb = or i32 %%rebfl, %d\n"+
			"  %%rebign = call i32 (i32, i32, ...) @fcntl(i32 %%fd, i32 4, i32 %%rebnb)", e.httpNonblockFlag())

	// Order matters: strings.NewReplacer uses argument order at each position, so
	// every token that is a prefix of another (WS ⊂ WSINK, HEADERS-shaped names)
	// must be listed AFTER the longer one. Longest-first is the safe rule.
	repl := strings.NewReplacer(
		"RENONBLOCK", renonblock,
		"ERRNOFN", e.errnoAccessor(), "EAGAINNO", fmt.Sprintf("%d", e.httpEagainErrno()),
		"REQHEADERS", fmt.Sprintf("%d", idx("__kml_reqheaders")),
		"KEEPALIVE", fmt.Sprintf("%d", idx("__kml_keepalive")),
		"DRAINONCE", fmt.Sprintf("%d", idx("__kml_drain_once")),
		"DRAINCB", fmt.Sprintf("%d", idx("__kml_drain_cb")),
		"STREAMING", fmt.Sprintf("%d", idx("__kml_streaming")),
		"OUTQ", fmt.Sprintf("%d", idx("__kml_outq")),
		"QST", "{ ptr, i64, i64, i64, i64 }",
		"HWMIDX", fmt.Sprintf("%d", idx("__kml_hwm")),
		"TERMINATOR", terminator,
		"CHUNKFMT", chunkFmt,
		"NBCLEAR", fmt.Sprintf("%d", ^e.httpNonblockFlag()),
		"NBSET", fmt.Sprintf("%d", e.httpNonblockFlag()),
		"HEADERS", fmt.Sprintf("%d", idx("headers")),
		"STATUS", fmt.Sprintf("%d", idx("status")),
		"WSINK", fmt.Sprintf("%d", idx("__kml_wsink")),
		"ENDED", fmt.Sprintf("%d", idx("ended")),
		"CRLF", crlf,
		"RES", res,
		"WS", wstreamStructIR,
		"FD", fmt.Sprintf("%d", idx("__kml_fd")),
	)
	block := repl.Replace(tmpl)

	// Over HTTPS/1.1 the fd is TLS-wrapped: route chunk writes through the
	// non-blocking SSL-aware shim (a plain fd falls through to raw write() inside
	// it) so a slow TLS consumer parks the fiber on EAGAIN instead of blocking
	// the reactor inside SSL_write's poll() — TDD-00195 Stage 2 backpressure.
	if e.usedHTTPS1Server {
		e.emitHTTPSConnShims()
		block = strings.ReplaceAll(block, "call i64 @write(i32 %fd,", "call i64 @__kml_http_conn_send_nb(i32 %fd,")
	}
	e.emitGlobal(block)
}
