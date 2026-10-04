package llvm

// Node http client — `http.get(url, cb)` / `http.request(...)` (TDD-00138
// Stage 1). Built on the existing libcurl `fetch` primitive (`__kml_fetch_async`
// + `__kml_await_fetch`), exposed through Node's callback/event surface: the
// callback receives an `IncomingMessage` (`res.statusCode`, `res.on('data'|
// 'end')`). V1 delivers the response body as a single 'data' chunk then 'end',
// synchronously right after the callback registers its listeners.

import (
	"fmt"
)

// ensureHTTPClientReactions emits the http-client completion-reaction registry
// (TDD-00138 Stage 2): a { pending, closure } list an async `http.get` registers
// into, fired by the event loop after each fetch-message drain when the
// pending's transfer is done. This is what lets `http.get` call the program's
// own in-process `http.listen` server without deadlocking — it registers a
// reaction and returns, and the already-running loop drives both the server and
// the client transfer, then fires the callback. `__kml_httpc_drive` is the
// no-event-loop fallback (busy-pump curl + fire until a specific pending done).
func (e *Emitter) ensureHTTPClientReactions() {
	if e.usedHTTPClientReactions {
		return
	}
	e.usedHTTPClientReactions = true
	e.ensureMalloc()
	e.ensureRealloc()
	e.ensureFetch() // @__kml_curl_multi, curl_multi_perform, __kml_curl_drain_messages
	e.emitGlobal("declare i32 @curl_multi_wait(ptr noundef, ptr noundef, i32 noundef, i32 noundef, ptr noundef)")
	e.emitGlobal(`
@__kml_httpc_data = internal global ptr null, align 8
@__kml_httpc_len = internal global i64 0, align 8
@__kml_httpc_cap = internal global i64 0, align 8

; register a { ptr pending, ptr closure } pair (16-byte entries).
define void @__kml_httpc_register(ptr %pending, ptr %closure) {
entry:
  %len = load i64, ptr @__kml_httpc_len, align 8
  %cap = load i64, ptr @__kml_httpc_cap, align 8
  %full = icmp sge i64 %len, %cap
  br i1 %full, label %grow, label %store
grow:
  %cap2 = mul i64 %cap, 2
  %ge4 = icmp sgt i64 %cap2, 4
  %newcap = select i1 %ge4, i64 %cap2, i64 4
  %old = load ptr, ptr @__kml_httpc_data, align 8
  %bytes = mul i64 %newcap, 16
  %new = call ptr @realloc(ptr %old, i64 %bytes)
  store ptr %new, ptr @__kml_httpc_data, align 8
  store i64 %newcap, ptr @__kml_httpc_cap, align 8
  br label %store
store:
  %data = load ptr, ptr @__kml_httpc_data, align 8
  %slot = getelementptr { ptr, ptr }, ptr %data, i64 %len
  %pp = getelementptr { ptr, ptr }, ptr %slot, i32 0, i32 0
  store ptr %pending, ptr %pp, align 8
  %cp = getelementptr { ptr, ptr }, ptr %slot, i32 0, i32 1
  store ptr %closure, ptr %cp, align 8
  %newlen = add i64 %len, 1
  store i64 %newlen, ptr @__kml_httpc_len, align 8
  ret void
}

; fire every registered reaction whose pending transfer is done (pending field
; 2 == done flag), then clear its slot (pending=null) so it fires at most once.
define void @__kml_httpc_fire_ready() {
entry:
  %len = load i64, ptr @__kml_httpc_len, align 8
  %data = load ptr, ptr @__kml_httpc_data, align 8
  br label %loop
loop:
  %i = phi i64 [ 0, %entry ], [ %inext, %cont ]
  %done = icmp sge i64 %i, %len
  br i1 %done, label %ret, label %body
body:
  %slot = getelementptr { ptr, ptr }, ptr %data, i64 %i
  %pp = getelementptr { ptr, ptr }, ptr %slot, i32 0, i32 0
  %pending = load ptr, ptr %pp, align 8
  %isnull = icmp eq ptr %pending, null
  br i1 %isnull, label %cont, label %check
check:
  %dp = getelementptr { ptr, ptr, i64, i64, i64 }, ptr %pending, i32 0, i32 2
  %d = load i64, ptr %dp, align 8
  %rdy = icmp ne i64 %d, 0
  br i1 %rdy, label %fire, label %cont
fire:
  %cp = getelementptr { ptr, ptr }, ptr %slot, i32 0, i32 1
  %closure = load ptr, ptr %cp, align 8
  store ptr null, ptr %pp, align 8
  %fpp = getelementptr { ptr, ptr }, ptr %closure, i32 0, i32 0
  %fp = load ptr, ptr %fpp, align 8
  %epp = getelementptr { ptr, ptr }, ptr %closure, i32 0, i32 1
  %ep = load ptr, ptr %epp, align 8
  call void %fp(ptr %ep)
  br label %cont
cont:
  %inext = add i64 %i, 1
  br label %loop
ret:
  ret void
}

; drive: pump curl + drain + fire until the given pending is done. Used when
; http.get runs with no event loop (no http.listen) to service the request.
; Between pumps it waits on curl's own sockets (curl_multi_wait, up to 100 ms)
; instead of spinning, and its one out-parameter slot lives in the entry block:
; an alloca in the loop grew the stack on every pass, and a transfer that
; takes long to fail — a refused connect retries for ~2 s on Windows, a slow
; TLS error — spun until the stack overflowed (0xC00000FD).
define void @__kml_httpc_drive(ptr %pending) {
entry:
  %rp = alloca i32, align 4
  br label %loop
loop:
  %dp = getelementptr { ptr, ptr, i64, i64, i64 }, ptr %pending, i32 0, i32 2
  %d = load i64, ptr %dp, align 8
  %isdone = icmp ne i64 %d, 0
  br i1 %isdone, label %fireleft, label %pump
pump:
  %cm = load ptr, ptr @__kml_curl_multi, align 8
  call i32 @curl_multi_perform(ptr %cm, ptr %rp)
  call void @__kml_curl_drain_messages()
  call void @__kml_httpc_fire_ready()
  %d2 = load i64, ptr %dp, align 8
  %isdone2 = icmp ne i64 %d2, 0
  br i1 %isdone2, label %fireleft, label %wait
wait:
  call i32 @curl_multi_wait(ptr %cm, ptr null, i32 0, i32 100, ptr null)
  br label %loop
fireleft:
  ; the pending is done; fire_ready above (or here) delivered its reaction.
  call void @__kml_httpc_fire_ready()
  ret void
}

; flush: pump curl until EVERY registered reaction has fired. Installed into
; @__kml_httpc_flush_hook and invoked after the event loop exits, so a
; server.close() from inside a request handler can't strand a client response
; whose transfer completes as the loop winds down.
define void @__kml_httpc_flush() {
entry:
  %rp = alloca i32, align 4
  br label %scan
scan:
  %len = load i64, ptr @__kml_httpc_len, align 8
  %data = load ptr, ptr @__kml_httpc_data, align 8
  br label %loop
loop:
  %i = phi i64 [ 0, %scan ], [ %inext, %cont ]
  %done = icmp sge i64 %i, %len
  br i1 %done, label %ret, label %body
body:
  %slot = getelementptr { ptr, ptr }, ptr %data, i64 %i
  %pp = getelementptr { ptr, ptr }, ptr %slot, i32 0, i32 0
  %pending = load ptr, ptr %pp, align 8
  %isnull = icmp eq ptr %pending, null
  br i1 %isnull, label %cont, label %haswork
cont:
  %inext = add i64 %i, 1
  br label %loop
haswork:
  %cm = load ptr, ptr @__kml_curl_multi, align 8
  call i32 @curl_multi_perform(ptr %cm, ptr %rp)
  call void @__kml_curl_drain_messages()
  call void @__kml_httpc_fire_ready()
  ; wait on curl's sockets before rescanning, rather than spinning (see drive)
  %running = load i32, ptr %rp, align 4
  %busy = icmp sgt i32 %running, 0
  br i1 %busy, label %waitmore, label %scan
waitmore:
  call i32 @curl_multi_wait(ptr %cm, ptr null, i32 0, i32 100, ptr null)
  br label %scan
ret:
  ret void
}`)
	e.ensureHTTPCFlushHook()
}

// ensureHTTPCFlushHook defines the post-event-loop flush hook global exactly
// once. The client runtime stores @__kml_httpc_flush into it at each
// http.get/request site; the listen emitters call through it (null-guarded)
// after @__kml_event_loop_run returns. Split from ensureHTTPClientReactions so
// server-only programs can reference the (null) hook without dragging curl in.
func (e *Emitter) ensureHTTPCFlushHook() {
	if e.usedHTTPCFlushHook {
		return
	}
	e.usedHTTPCFlushHook = true
	e.emitGlobal("@__kml_httpc_flush_hook = internal global ptr null, align 8")
}

// emitPostLoopFlush emits the null-guarded indirect call through the flush
// hook — placed immediately after every @__kml_event_loop_run call site.
func (e *Emitter) emitPostLoopFlush() {
	e.ensureHTTPCFlushHook()
	h := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr @__kml_httpc_flush_hook, align 8", h))
	isnull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isnull, h))
	skipL := e.freshLabel("postloop.skip")
	callL := e.freshLabel("postloop.flush")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isnull, skipL, callL))
	e.emitLabel(callL)
	e.emitInstr(fmt.Sprintf("call void %s()", h))
	e.emitTerminator(fmt.Sprintf("br label %%%s", skipL))
	e.emitLabel(skipL)
}
