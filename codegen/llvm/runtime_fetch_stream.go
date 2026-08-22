// runtime_fetch_stream.go — TDD-00097 Stage 4: `Response.body` as a
// ReadableStream<Uint8Array> fed straight from libcurl's write callback.
//
// The shared write callback (runtime_fetch.go) calls two hooks that are
// no-op stubs until this file's real definitions are emitted (a program that
// never touches `.body` keeps the fully-buffered behavior byte-for-byte):
//
//	__kml_fetch_body_write(pending, chunk, total) → 0 buffer · 1 consumed ·
//	  2 pause (CURL_WRITEFUNC_PAUSE, the chunk is redelivered on unpause —
//	  chosen when the stream's queue is at its high-water mark with no read
//	  pending, so the queue stays bounded)
//	__kml_fetch_body_on_done(pending) → close (or error, on a transfer-level
//	  failure) the activated stream when the transfer completes
//
// The stream's pull closure is __kml_fetch_body_pull(pending): clear the
// paused flag and curl_easy_pause(CONT) so the transfer resumes — the event
// loop / await drive's regular multi_perform then delivers the next chunk.
package llvm

import "fmt"

// ensureFetchBodyStream emits the real streaming hooks.
func (e *Emitter) ensureFetchBodyStream() {
	if e.usedFetchBodyStream {
		return
	}
	e.usedFetchBodyStream = true
	e.ensureStreamRuntime()
	e.ensureAwaitFetchHeaders()
	e.ensureMemcpy()
	errName := e.internString("Error")

	pendIR := "{ ptr, ptr, i64, i64, i64, ptr, i64, ptr, i64 }"
	e.emitGlobal("declare i32 @curl_easy_pause(ptr noundef, i32 noundef)")
	e.emitGlobal(fmt.Sprintf(`
define i64 @__kml_fetch_body_write(ptr %%pending, ptr %%chunk, i64 %%total) {
entry:
  %%bs_p = getelementptr %s, ptr %%pending, i32 0, i32 7
  %%bs = load ptr, ptr %%bs_p, align 8
  %%nostream = icmp eq ptr %%bs, null
  br i1 %%nostream, label %%buffer, label %%ck
buffer:
  ret i64 0
ck:
  %%st_p = getelementptr %s, ptr %%bs, i32 0, i32 0
  %%st = load i64, ptr %%st_p, align 8
  %%notReadable = icmp ne i64 %%st, 0
  br i1 %%notReadable, label %%drop, label %%ckroom
drop:
  ret i64 1
ckroom:
  %%d = call double @__kml_rs_desired(ptr %%bs)
  %%hasRoom = fcmp ogt double %%d, 0.0
  %%rh_p = getelementptr %s, ptr %%bs, i32 0, i32 14
  %%rl_p = getelementptr %s, ptr %%bs, i32 0, i32 15
  %%rh = load i64, ptr %%rh_p, align 8
  %%rl = load i64, ptr %%rl_p, align 8
  %%rdPending = icmp slt i64 %%rh, %%rl
  %%accept = or i1 %%hasRoom, %%rdPending
  br i1 %%accept, label %%enq, label %%pause
pause:
  %%pa_p = getelementptr %s, ptr %%pending, i32 0, i32 8
  store i64 1, ptr %%pa_p, align 8
  ret i64 2
enq:
  %%copy = call ptr @malloc(i64 %%total)
  call ptr @memcpy(ptr %%copy, ptr %%chunk, i64 %%total)
  %%bits = ptrtoint ptr %%copy to i64
  %%ign = call i64 @__kml_rs_enqueue(ptr %%bs, i64 %%bits, i64 %%total)
  ret i64 1
}

define void @__kml_fetch_body_on_done(ptr %%pending) {
entry:
  %%bs_p = getelementptr %s, ptr %%pending, i32 0, i32 7
  %%bs = load ptr, ptr %%bs_p, align 8
  %%nostream = icmp eq ptr %%bs, null
  br i1 %%nostream, label %%ret, label %%ck
ck:
  %%result_p = getelementptr %s, ptr %%pending, i32 0, i32 4
  %%result = load i64, ptr %%result_p, align 8
  %%failed = icmp ne i64 %%result, 0
  br i1 %%failed, label %%err, label %%close
err:
  %%result32 = trunc i64 %%result to i32
  %%errstr = call ptr @curl_easy_strerror(i32 %%result32)
  %%eo = call ptr @malloc(i64 24)
  %%eo_kind = getelementptr { i64, ptr, ptr }, ptr %%eo, i32 0, i32 0
  store i64 0, ptr %%eo_kind, align 8
  %%eo_msg = getelementptr { i64, ptr, ptr }, ptr %%eo, i32 0, i32 1
  store ptr %%errstr, ptr %%eo_msg, align 8
  %%eo_name = getelementptr { i64, ptr, ptr }, ptr %%eo, i32 0, i32 2
  store ptr %s, ptr %%eo_name, align 8
  %%ebits = ptrtoint ptr %%eo to i64
  call void @__kml_rs_error(ptr %%bs, i64 %%ebits)
  ret void
close:
  %%ign = call i64 @__kml_rs_close(ptr %%bs)
  ret void
ret:
  ret void
}

define ptr @__kml_fetch_body_pull(ptr %%pending) {
entry:
  %%pa_p = getelementptr %s, ptr %%pending, i32 0, i32 8
  %%pa = load i64, ptr %%pa_p, align 8
  %%isPaused = icmp ne i64 %%pa, 0
  br i1 %%isPaused, label %%unpause, label %%ret
unpause:
  store i64 0, ptr %%pa_p, align 8
  %%done_p = getelementptr %s, ptr %%pending, i32 0, i32 2
  %%done = load i64, ptr %%done_p, align 8
  %%isdone = icmp ne i64 %%done, 0
  br i1 %%isdone, label %%ret, label %%cont
cont:
  %%easy_p = getelementptr %s, ptr %%pending, i32 0, i32 0
  %%easy = load ptr, ptr %%easy_p, align 8
  %%ign = call i32 @curl_easy_pause(ptr %%easy, i32 0)
  %%ign2 = call i1 @__kml_fetch_pump()
  br label %%ret
ret:
  ret ptr null
}

define ptr @__kml_fetch_body_stream(ptr %%pending, ptr %%s, ptr %%bodyPtr, i64 %%bodyLen) {
entry:
  ; Combinator-built / already-finished Responses arrive with pending == null:
  ; replay the buffered body as one chunk and close.
  %%nopend = icmp eq ptr %%pending, null
  br i1 %%nopend, label %%replaybody, label %%live
replaybody:
  %%hasBody = icmp sgt i64 %%bodyLen, 0
  br i1 %%hasBody, label %%enqbody, label %%closebody
enqbody:
  %%bbits = ptrtoint ptr %%bodyPtr to i64
  %%ign0 = call i64 @__kml_rs_enqueue(ptr %%s, i64 %%bbits, i64 %%bodyLen)
  br label %%closebody
closebody:
  %%ign1 = call i64 @__kml_rs_close(ptr %%s)
  ret ptr %%s
live:
  %%bs_p = getelementptr %s, ptr %%pending, i32 0, i32 7
  %%existing = load ptr, ptr %%bs_p, align 8
  %%have = icmp ne ptr %%existing, null
  br i1 %%have, label %%reuse, label %%activate
reuse:
  ret ptr %%existing
activate:
  store ptr %%s, ptr %%bs_p, align 8
  ; Flush whatever arrived before activation as the first chunk.
  %%buf_p = getelementptr %s, ptr %%pending, i32 0, i32 1
  %%buf = load ptr, ptr %%buf_p, align 8
  %%blen_p = getelementptr { ptr, i64, i64 }, ptr %%buf, i32 0, i32 1
  %%blen = load i64, ptr %%blen_p, align 8
  %%hasPre = icmp sgt i64 %%blen, 0
  br i1 %%hasPre, label %%flush, label %%ckdone
flush:
  %%bdata_p = getelementptr { ptr, i64, i64 }, ptr %%buf, i32 0, i32 0
  %%bdata = load ptr, ptr %%bdata_p, align 8
  %%fbits = ptrtoint ptr %%bdata to i64
  %%ign2 = call i64 @__kml_rs_enqueue(ptr %%s, i64 %%fbits, i64 %%blen)
  br label %%ckdone
ckdone:
  %%done_p = getelementptr %s, ptr %%pending, i32 0, i32 2
  %%done = load i64, ptr %%done_p, align 8
  %%isdone = icmp ne i64 %%done, 0
  br i1 %%isdone, label %%finish, label %%ret
finish:
  call void @__kml_fetch_body_on_done(ptr %%pending)
  ret ptr %%s
ret:
  ret ptr %%s
}`, pendIR, rstreamStructIR, rstreamStructIR, rstreamStructIR, pendIR, pendIR, pendIR, errName, pendIR, pendIR, pendIR, pendIR, pendIR, pendIR))
}
