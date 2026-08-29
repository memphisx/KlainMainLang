package llvm

import (
	"fmt"
)

// ensureFetch declares the curl_easy_* primitives and __kml_curl_write_cb
// shared by every fetch call, sync or async. It no longer declares a
// __kml_fetch function of its own (ADR-00095 removed it): the blocking,
// single-transfer implementation that symbol used to name predates
// ADR-00050's non-blocking multi-interface rewrite of fetch() and had been
// dead — reachable from no live call path — ever since, confirmed by grep
// before deletion. Numeric CURLOPT_*/CURLINFO_* values below were verified
// directly against curl.h rather than trusted from memory (CURLOPT_URL=10002,
// CURLOPT_WRITEFUNCTION=20011, CURLOPT_WRITEDATA=10001,
// CURLOPT_FOLLOWLOCATION=52, CURLOPT_TIMEOUT=13, CURLOPT_NOSIGNAL=99 — curl's
// own ABI policy freezes these permanently, so hardcoding them here (rather
// than needing curl.h at KML-compile time) is safe long-term, not just
// today).
func (e *Emitter) ensureFetch() {
	if e.usedFetch {
		return
	}
	e.usedFetch = true
	e.requireLink("curl")
	e.ensureMalloc()
	e.ensureRealloc()
	e.ensureMemcpy()
	e.ensureStrHeaderRuntime() // error .message must be headered for concat/=== (TDD-00120)
	e.ensureExceptionHelpers()

	e.emitGlobal("declare void @curl_global_init(i64 noundef)")
	e.emitGlobal("declare ptr @curl_easy_init()")
	e.emitGlobal("declare i32 @curl_easy_setopt(ptr noundef, i32 noundef, ...)")
	e.emitGlobal("declare i32 @curl_easy_perform(ptr noundef)")
	e.emitGlobal("declare i32 @curl_easy_getinfo(ptr noundef, i32 noundef, ...)")
	e.emitGlobal("declare void @curl_easy_cleanup(ptr noundef)")
	e.emitGlobal("declare ptr @curl_easy_strerror(i32 noundef)")
	e.emitGlobal("@__kml_curl_inited = internal global i1 0, align 1")

	// Write callback: libcurl calls this (possibly many times, once per
	// chunk) as the response body streams in. userdata is a ptr to a
	// { ptr data, i64 len, i64 cap } growable buffer this function owns —
	// grown via realloc (doubling, floor 64 bytes), always kept
	// null-terminated so the final body can be handed around as a plain
	// KML string with no extra bookkeeping.
	e.emitGlobal(`
define i64 @__kml_curl_write_cb(ptr %chunk, i64 %size, i64 %nmemb, ptr %ud) {
entry:
  %total = mul i64 %size, %nmemb
  ; TDD-00097 Stage 4: slot 3 of the (extended, 32-byte) buffer struct holds
  ; a backpointer to the pending-fetch struct (null on the blocking path).
  ; When the fetch has an activated body stream, the hook consumes or pauses
  ; the chunk; a 0 return means "not streaming — buffer as before".
  %pend_p = getelementptr ptr, ptr %ud, i64 3
  %pend = load ptr, ptr %pend_p, align 8
  %nopend = icmp eq ptr %pend, null
  br i1 %nopend, label %buffer, label %markhdrs
markhdrs:
  %hd_p = getelementptr { ptr, ptr, i64, i64, i64, ptr, i64, ptr, i64 }, ptr %pend, i32 0, i32 6
  store i64 1, ptr %hd_p, align 8
  %hook = call i64 @__kml_fetch_body_write(ptr %pend, ptr %chunk, i64 %total)
  %consumed = icmp eq i64 %hook, 1
  br i1 %consumed, label %retok, label %ckpause
retok:
  ret i64 %total
ckpause:
  %paused = icmp eq i64 %hook, 2
  br i1 %paused, label %retpause, label %buffer
retpause:
  ret i64 268435457
buffer:
  %data_p = getelementptr { ptr, i64, i64 }, ptr %ud, i32 0, i32 0
  %len_p = getelementptr { ptr, i64, i64 }, ptr %ud, i32 0, i32 1
  %cap_p = getelementptr { ptr, i64, i64 }, ptr %ud, i32 0, i32 2
  %curdata = load ptr, ptr %data_p, align 8
  %curlen = load i64, ptr %len_p, align 8
  %curcap = load i64, ptr %cap_p, align 8
  %needed = add i64 %curlen, %total
  %neededp1 = add i64 %needed, 1
  %needgrow = icmp sgt i64 %neededp1, %curcap
  br i1 %needgrow, label %grow, label %copy

grow:
  %cap2 = mul i64 %curcap, 2
  %pick1 = icmp sgt i64 %neededp1, %cap2
  %newcap_a = select i1 %pick1, i64 %neededp1, i64 %cap2
  %atleast64 = icmp sgt i64 %newcap_a, 64
  %newcap = select i1 %atleast64, i64 %newcap_a, i64 64
  %newdata = call ptr @realloc(ptr %curdata, i64 %newcap)
  store ptr %newdata, ptr %data_p, align 8
  store i64 %newcap, ptr %cap_p, align 8
  br label %copy

copy:
  %dataNow = load ptr, ptr %data_p, align 8
  %destptr = getelementptr i8, ptr %dataNow, i64 %curlen
  call ptr @memcpy(ptr %destptr, ptr %chunk, i64 %total)
  %newlen = add i64 %curlen, %total
  store i64 %newlen, ptr %len_p, align 8
  %termptr = getelementptr i8, ptr %dataNow, i64 %newlen
  store i8 0, ptr %termptr, align 1
  ret i64 %total
}`)
}

// ensureFetchAsync declares everything a real, non-blocking `await
// fetch(...)` needs (ADR-00050, TDD-00006 Part 2's second real slice, on
// top of ADR-00049's fiber/event-loop mechanism): libcurl's multi
// interface, driven by the same select() loop http.listen already uses, so
// a fetch awaited from inside a connection-handler fiber yields instead of
// blocking the whole process, letting other connections' fibers (and their
// own concurrent fetches) keep making progress.
//
// Numeric CURLOPT_*/CURLINFO_* values not already used by ensureFetch were
// verified directly against curl.h/multi.h on this machine (both are
// present locally), the same "never trust from memory" standard the
// existing blocking fetch's own constants already document:
// CURLOPT_PRIVATE=10103 (CURLOPTTYPE_OBJECTPOINT=10000 + 103),
// CURLINFO_PRIVATE=1048597 (CURLINFO_STRING=0x100000 + 21),
// CURLMSG_DONE=1 (CURLMSG_NONE=0 is the first, unused enum value).
// ADR-00074/TDD-00017 added three more, verified the same way:
// CURLOPT_POSTFIELDS=10015 (CURLOPTTYPE_OBJECTPOINT=10000 + 15),
// CURLOPT_HTTPHEADER=10023 (CURLOPTTYPE_SLISTPOINT=10000 + 23),
// CURLOPT_CUSTOMREQUEST=10036 (CURLOPTTYPE_STRINGPOINT=10000 + 36).
//
// A pending fetch is a malloc'd { ptr easy, ptr buf, i64 done, i64
// httpStatus, i64 curlResult } (40 bytes, every field ptr/i64 — no padding
// ambiguity, same convention the timer queue and connection array already
// follow). buf is the same { ptr, i64, i64 } growable write-buffer
// ensureFetch's own write callback already fills — reused as-is, no new
// callback needed.
//
//	__kml_fetch_async(ptr url, ptr method, ptr headers, ptr body) -> ptr
//	  Creates the easy handle (identical setopts to the blocking __kml_fetch:
//	  URL, write callback/data, follow-location, timeout, nosignal), lazily
//	  creates the one global CURLM multi handle on first use, attaches the
//	  pending struct to the easy handle via CURLOPT_PRIVATE (so a later
//	  curl_multi_info_read can match a completed transfer back to it),
//	  curl_multi_add_handle()s it, and calls curl_multi_perform() once to
//	  kick the transfer off. Returns immediately — never blocks. method/
//	  headers/body (ADR-00074, TDD-00017) are each nullable — a null skips
//	  the corresponding CURLOPT_CUSTOMREQUEST/CURLOPT_HTTPHEADER/
//	  CURLOPT_POSTFIELDS setopt entirely, so a plain fetch(url) call site
//	  (which always passes all three as null) behaves exactly as before.
//	  headers, when non-null, is a struct curl_slist* built by emit_fetch.go
//	  from a Map<string,string> via ensureCurlSlist's curl_slist_append.
//	__kml_curl_drain_messages()
//	  Drains curl_multi_info_read()'s completed-transfer queue. For each
//	  CURLMSG_DONE message: retrieves the pending struct via
//	  CURLINFO_PRIVATE, records the HTTP status and CURLcode result into
//	  it, removes+cleans up the easy handle, and sets done=1. Shared by the
//	  event loop (called after every select() wake) and __kml_await_fetch's
//	  own busy-spin fallback path below.
//	__kml_await_fetch(ptr pending) -> { i64 status, ptr body, i64 bodyLen }
//	  Loops until pending->done: if running inside a connection fiber
//	  (@__kml_current_conn_idx >= 0), parks this specific fiber (stores
//	  `pending` into its own connection-array entry's pendingFetch field,
//	  swapcontext back to @__kml_main_ctx — the event loop's resume-scan
//	  already checks this field, see runtime.go's __kml_event_loop_run) and
//	  clears pendingFetch back to null once resumed; otherwise (top-level
//	  code, no event loop/fiber context to yield into) busy-spins by
//	  calling curl_multi_perform + draining messages directly in a tight
//	  loop — behaviorally equivalent to a blocking wait (nothing else could
//	  run concurrently in that case anyway), just implemented via repeated
//	  small multi-interface calls instead of one call to curl_easy_perform.
//	  Once done, throws a catchable Error on a transfer-level failure
//	  (identical shape to __kml_fetch's own neterror path) or returns the
//	  final status/body.
func (e *Emitter) ensureFetchAsync() {
	if e.usedFetchAsync {
		return
	}
	e.usedFetchAsync = true
	e.ensureFetch()
	e.ensureStrHeaderRuntime() // error .message must be headered for concat/=== (TDD-00120)
	e.ensureFiberRuntime()
	e.ensureCurrentTaskGlobal() // @__kml_current_task, read by the may-suspend task-park path below
	e.ensureExceptionHelpers()
	e.ensureSignalAborted() // __kml_signal_aborted, used by the await abort check (TDD-00081)

	// Task-park path (TDD-00083 Stage 2): when a fetch is awaited inside a
	// coroutine task (not a connection fiber), park the task on the fetch and
	// swap to its resumer, so the scheduler can drive other tasks concurrently.
	// Only emitted when the program actually has a may-suspend async fn; without
	// it, maybeyield keeps its original connection-fiber-or-busyspin behavior
	// byte-for-byte.
	// NOTE: these are Sprintf *arguments* (%s), not part of the format string, so
	// LLVM's '%' is written as a single '%' here, not '%%'.
	awfTaskCheck := "\n  br label %maybeconn"
	awfTaskYield := ""
	if e.hasMaySuspend {
		awfGCRestore := ""
		if e.isGCMode() {
			awfGCRestore = "\n  call void @__kml_task_gc_restore()"
		}
		awfTaskCheck = "\n  %awf_task = load ptr, ptr @__kml_current_task, align 8\n  %awf_ontask = icmp ne ptr %awf_task, null\n  br i1 %awf_ontask, label %taskyield, label %maybeconn"
		awfTaskYield = "\ntaskyield:" +
			"\n  %ty_pf_p = getelementptr " + taskStructIR + ", ptr %awf_task, i32 0, i32 " + fmt.Sprintf("%d", taskPendingFetch) + "\n  store ptr %pending, ptr %ty_pf_p, align 8" +
			"\n  %ty_st_p = getelementptr " + taskStructIR + ", ptr %awf_task, i32 0, i32 " + fmt.Sprintf("%d", taskState) + "\n  store i64 1, ptr %ty_st_p, align 8" +
			"\n  %ty_rc_p = getelementptr " + taskStructIR + ", ptr %awf_task, i32 0, i32 " + fmt.Sprintf("%d", taskResumerCtx) + "\n  %ty_rc = load ptr, ptr %ty_rc_p, align 8" +
			"\n  %ty_ctx_p = getelementptr " + taskStructIR + ", ptr %awf_task, i32 0, i32 " + fmt.Sprintf("%d", taskCtx) + "\n  %ty_ctx = load ptr, ptr %ty_ctx_p, align 8" +
			"\n  %ty_sjt_p = getelementptr " + taskStructIR + ", ptr %awf_task, i32 0, i32 " + fmt.Sprintf("%d", taskSavedJmpTop) + "\n  %ty_top = load i32, ptr @__kml_jmp_top, align 4\n  %ty_top64 = zext i32 %ty_top to i64\n  store i64 %ty_top64, ptr %ty_sjt_p, align 8" +
			"\n  %ty_sw = call i32 @swapcontext(ptr %ty_ctx, ptr %ty_rc)" + awfGCRestore +
			"\n  br label %checkloop"
	}
	errNamePtr := e.internString("Error")
	abortNamePtr := e.internString("AbortError")
	abortMsgPtr := e.internString("The operation was aborted")
	timeoutNamePtr := e.internString("TimeoutError")
	timeoutMsgPtr := e.internString("The operation timed out")
	domExcKind := errorKindIDs["DOMException"]

	e.emitGlobal("declare ptr @curl_multi_init()")
	e.emitGlobal("declare i32 @curl_multi_add_handle(ptr noundef, ptr noundef)")
	e.emitGlobal("declare i32 @curl_multi_remove_handle(ptr noundef, ptr noundef)")
	e.emitGlobal("declare i32 @curl_multi_fdset(ptr noundef, ptr noundef, ptr noundef, ptr noundef, ptr noundef)")
	e.emitGlobal("declare i32 @curl_multi_timeout(ptr noundef, ptr noundef)")
	e.emitGlobal("declare i32 @curl_multi_perform(ptr noundef, ptr noundef)")
	e.emitGlobal("declare ptr @curl_multi_info_read(ptr noundef, ptr noundef)")
	e.emitGlobal("@__kml_curl_multi = internal thread_local global ptr null, align 8")

	e.emitGlobal(`
define ptr @__kml_fetch_async(ptr %url, ptr %method, ptr %headers, ptr %body, ptr %signal) {
entry:
  %inited = load i1, ptr @__kml_curl_inited, align 1
  br i1 %inited, label %skipinit, label %doinit

doinit:
  call void @curl_global_init(i64 3)
  store i1 1, ptr @__kml_curl_inited, align 1
  br label %skipinit

skipinit:
  %multi = load ptr, ptr @__kml_curl_multi, align 8
  %needmulti = icmp eq ptr %multi, null
  br i1 %needmulti, label %initmulti, label %havemulti

initmulti:
  %newmulti = call ptr @curl_multi_init()
  store ptr %newmulti, ptr @__kml_curl_multi, align 8
  br label %havemulti

havemulti:
  %multi2 = load ptr, ptr @__kml_curl_multi, align 8

  %buf = call ptr @malloc(i64 40)
  %buf_data_p = getelementptr { ptr, i64, i64 }, ptr %buf, i32 0, i32 0
  %buf_len_p = getelementptr { ptr, i64, i64 }, ptr %buf, i32 0, i32 1
  %buf_cap_p = getelementptr { ptr, i64, i64 }, ptr %buf, i32 0, i32 2
  store ptr null, ptr %buf_data_p, align 8
  store i64 0, ptr %buf_len_p, align 8
  store i64 0, ptr %buf_cap_p, align 8
  %buf_pend_p = getelementptr ptr, ptr %buf, i64 3
  store ptr null, ptr %buf_pend_p, align 8
  ; header-capture buffer (ADR-00490): same growable {data,len,cap} shape,
  ; fed by the same write callback via HEADERFUNCTION/HEADERDATA
  ; (CURLOPT 20079/10029 — frozen curl ABI values), reachable from the
  ; body buffer's slot 4 so the Response can parse it lazily.
  %hbuf = call ptr @malloc(i64 24)
  %hb_d = getelementptr { ptr, i64, i64 }, ptr %hbuf, i32 0, i32 0
  %hb_l = getelementptr { ptr, i64, i64 }, ptr %hbuf, i32 0, i32 1
  %hb_c = getelementptr { ptr, i64, i64 }, ptr %hbuf, i32 0, i32 2
  store ptr null, ptr %hb_d, align 8
  store i64 0, ptr %hb_l, align 8
  store i64 0, ptr %hb_c, align 8
  %buf_hb_p = getelementptr ptr, ptr %buf, i64 4
  store ptr %hbuf, ptr %buf_hb_p, align 8

  %curl = call ptr @curl_easy_init()
  call i32 (ptr, i32, ...) @curl_easy_setopt(ptr %curl, i32 10002, ptr %url)
  call i32 (ptr, i32, ...) @curl_easy_setopt(ptr %curl, i32 20011, ptr @__kml_curl_write_cb)
  call i32 (ptr, i32, ...) @curl_easy_setopt(ptr %curl, i32 10001, ptr %buf)
  call i32 (ptr, i32, ...) @curl_easy_setopt(ptr %curl, i32 20079, ptr @__kml_curl_write_cb)
  call i32 (ptr, i32, ...) @curl_easy_setopt(ptr %curl, i32 10029, ptr %hbuf)
  call i32 (ptr, i32, ...) @curl_easy_setopt(ptr %curl, i32 52, i64 1)
  call i32 (ptr, i32, ...) @curl_easy_setopt(ptr %curl, i32 13, i64 30)
  call i32 (ptr, i32, ...) @curl_easy_setopt(ptr %curl, i32 99, i64 1)
  ; CURLOPT_HTTP_VERSION (84) = CURL_HTTP_VERSION_2TLS (4): negotiate HTTP/2 over
  ; TLS via ALPN, HTTP/1.1 over cleartext. libcurl does ALPN+HPACK internally and
  ; transparently falls back to 1.1; a libcurl built without nghttp2 rejects this
  ; option (return code ignored, as elsewhere) and proceeds as 1.1.
  call i32 (ptr, i32, ...) @curl_easy_setopt(ptr %curl, i32 84, i64 4)

  %hasmethod = icmp ne ptr %method, null
  br i1 %hasmethod, label %setmethod, label %skipmethod

setmethod:
  call i32 (ptr, i32, ...) @curl_easy_setopt(ptr %curl, i32 10036, ptr %method)
  br label %skipmethod

skipmethod:
  %hasheaders = icmp ne ptr %headers, null
  br i1 %hasheaders, label %setheaders, label %skipheaders

setheaders:
  call i32 (ptr, i32, ...) @curl_easy_setopt(ptr %curl, i32 10023, ptr %headers)
  br label %skipheaders

skipheaders:
  %hasbody = icmp ne ptr %body, null
  br i1 %hasbody, label %setbody, label %skipbody

setbody:
  call i32 (ptr, i32, ...) @curl_easy_setopt(ptr %curl, i32 10015, ptr %body)
  br label %skipbody

skipbody:
  %pending = call ptr @malloc(i64 72)
  %p_easy = getelementptr { ptr, ptr, i64, i64, i64 }, ptr %pending, i32 0, i32 0
  store ptr %curl, ptr %p_easy, align 8
  %p_buf = getelementptr { ptr, ptr, i64, i64, i64 }, ptr %pending, i32 0, i32 1
  store ptr %buf, ptr %p_buf, align 8
  %p_done = getelementptr { ptr, ptr, i64, i64, i64 }, ptr %pending, i32 0, i32 2
  store i64 0, ptr %p_done, align 8
  %p_status = getelementptr { ptr, ptr, i64, i64, i64 }, ptr %pending, i32 0, i32 3
  store i64 0, ptr %p_status, align 8
  %p_result = getelementptr { ptr, ptr, i64, i64, i64 }, ptr %pending, i32 0, i32 4
  store i64 0, ptr %p_result, align 8
  %p_signal = getelementptr { ptr, ptr, i64, i64, i64, ptr }, ptr %pending, i32 0, i32 5
  store ptr %signal, ptr %p_signal, align 8
  %p_hdrs = getelementptr { ptr, ptr, i64, i64, i64, ptr, i64, ptr, i64 }, ptr %pending, i32 0, i32 6
  store i64 0, ptr %p_hdrs, align 8
  %p_bstream = getelementptr { ptr, ptr, i64, i64, i64, ptr, i64, ptr, i64 }, ptr %pending, i32 0, i32 7
  store ptr null, ptr %p_bstream, align 8
  %p_paused = getelementptr { ptr, ptr, i64, i64, i64, ptr, i64, ptr, i64 }, ptr %pending, i32 0, i32 8
  store i64 0, ptr %p_paused, align 8
  store ptr %pending, ptr %buf_pend_p, align 8

  call i32 (ptr, i32, ...) @curl_easy_setopt(ptr %curl, i32 10103, ptr %pending)
  call i32 @curl_multi_add_handle(ptr %multi2, ptr %curl)
  %runningp = alloca i32, align 4
  call i32 @curl_multi_perform(ptr %multi2, ptr %runningp)

  ret ptr %pending
}`)

	e.emitGlobal(`
define void @__kml_curl_drain_messages() {
entry:
  %multi = load ptr, ptr @__kml_curl_multi, align 8
  %msgsleft = alloca i32, align 4
  %privslot = alloca ptr, align 8
  %statusslot = alloca i64, align 8
  br label %drainloop

drainloop:
  %msg = call ptr @curl_multi_info_read(ptr %multi, ptr %msgsleft)
  %isnull = icmp eq ptr %msg, null
  br i1 %isnull, label %done, label %havemsg

havemsg:
  %msgtype_p = getelementptr i8, ptr %msg, i64 0
  %msgtype = load i32, ptr %msgtype_p, align 4
  %isdone = icmp eq i32 %msgtype, 1
  br i1 %isdone, label %handledone, label %drainloop

handledone:
  %easyh_p = getelementptr i8, ptr %msg, i64 8
  %easyh = load ptr, ptr %easyh_p, align 8
  %result_p = getelementptr i8, ptr %msg, i64 16
  %result32 = load i32, ptr %result_p, align 4
  %result64 = sext i32 %result32 to i64

  call i32 (ptr, i32, ...) @curl_easy_getinfo(ptr %easyh, i32 1048597, ptr %privslot)
  %pending = load ptr, ptr %privslot, align 8

  store i64 0, ptr %statusslot, align 8
  call i32 (ptr, i32, ...) @curl_easy_getinfo(ptr %easyh, i32 2097154, ptr %statusslot)
  %status = load i64, ptr %statusslot, align 8

  %p_status2 = getelementptr { ptr, ptr, i64, i64, i64 }, ptr %pending, i32 0, i32 3
  store i64 %status, ptr %p_status2, align 8
  %p_result2 = getelementptr { ptr, ptr, i64, i64, i64 }, ptr %pending, i32 0, i32 4
  store i64 %result64, ptr %p_result2, align 8

  call i32 @curl_multi_remove_handle(ptr %multi, ptr %easyh)
  call void @curl_easy_cleanup(ptr %easyh)

  %p_done2 = getelementptr { ptr, ptr, i64, i64, i64 }, ptr %pending, i32 0, i32 2
  store i64 1, ptr %p_done2, align 8
  call void @__kml_fetch_body_on_done(ptr %pending)

  br label %drainloop

done:
  ret void
}`)

	// __kml_fetch_pump: one multi_perform + message drain, reporting whether
	// transfers are still running — the no-fiber await drive's hook
	// (TDD-00097 Stage 4). A no-op stub is emitted at finalize when fetch is
	// entirely unused.
	e.emitGlobal(`
define i1 @__kml_fetch_pump() {
entry:
  %multi = load ptr, ptr @__kml_curl_multi, align 8
  %nomulti = icmp eq ptr %multi, null
  br i1 %nomulti, label %idle, label %pump
idle:
  ret i1 0
pump:
  %runningp = alloca i32, align 4
  store i32 0, ptr %runningp, align 4
  call i32 @curl_multi_perform(ptr %multi, ptr %runningp)
  call void @__kml_curl_drain_messages()
  %running = load i32, ptr %runningp, align 4
  %active = icmp sgt i32 %running, 0
  ret i1 %active
}`)

	// __kml_pending_finish: a pure extraction of what used to be
	// __kml_await_fetch's own finish/neterror/ok/emptybody/havebody/retdone
	// blocks (ADR-00073) — throws a catchable Error on a transfer-level
	// failure, otherwise returns the final status/body. Behavior-preserving:
	// __kml_await_fetch below is now just "loop until done, then tail-call
	// this." Split out so Promise.all/.race (emit_promise.go) can reuse the
	// exact same finish logic per member of a group of pending fetches,
	// without duplicating it.
	e.emitGlobal(`
define { i64, ptr, i64 } @__kml_pending_finish(ptr %pending) {
entry:
  %result_p = getelementptr { ptr, ptr, i64, i64, i64 }, ptr %pending, i32 0, i32 4
  %result = load i64, ptr %result_p, align 8
  %failed = icmp ne i64 %result, 0
  br i1 %failed, label %neterror, label %ok

neterror:
  %result32b = trunc i64 %result to i32
  %errstr = call ptr @curl_easy_strerror(i32 %result32b)
  %errstr_hdr = call ptr @__kml_str_from_cstr(ptr %errstr)
  %errobj = call ptr @malloc(i64 24)
  %errobj.kind = getelementptr { i64, ptr, ptr }, ptr %errobj, i32 0, i32 0
  store i64 0, ptr %errobj.kind, align 8
  %errobj.msg = getelementptr { i64, ptr, ptr }, ptr %errobj, i32 0, i32 1
  store ptr %errstr_hdr, ptr %errobj.msg, align 8
  %errobj.name = getelementptr { i64, ptr, ptr }, ptr %errobj, i32 0, i32 2
  store ptr ` + errNamePtr + `, ptr %errobj.name, align 8
  call void @__kml_throw(ptr %errobj)
  unreachable

ok:
  %status_p = getelementptr { ptr, ptr, i64, i64, i64 }, ptr %pending, i32 0, i32 3
  %status = load i64, ptr %status_p, align 8
  %buf_p = getelementptr { ptr, ptr, i64, i64, i64 }, ptr %pending, i32 0, i32 1
  %buf = load ptr, ptr %buf_p, align 8
  %bodyptr_p = getelementptr { ptr, i64, i64 }, ptr %buf, i32 0, i32 0
  %bodyptr = load ptr, ptr %bodyptr_p, align 8
  %bodylen_p = getelementptr { ptr, i64, i64 }, ptr %buf, i32 0, i32 1
  %bodylen = load i64, ptr %bodylen_p, align 8

  %isnullbody = icmp eq ptr %bodyptr, null
  br i1 %isnullbody, label %emptybody, label %havebody

emptybody:
  %emptystr = call ptr @malloc(i64 1)
  store i8 0, ptr %emptystr, align 1
  br label %retdone

havebody:
  br label %retdone

retdone:
  %bodyfinal = phi ptr [ %emptystr, %emptybody ], [ %bodyptr, %havebody ]
  %bodylenfinal = phi i64 [ 0, %emptybody ], [ %bodylen, %havebody ]
  %r1 = insertvalue { i64, ptr, i64 } undef, i64 %status, 0
  %r2 = insertvalue { i64, ptr, i64 } %r1, ptr %bodyfinal, 1
  %r3 = insertvalue { i64, ptr, i64 } %r2, i64 %bodylenfinal, 2
  ret { i64, ptr, i64 } %r3
}`)

	e.emitGlobal(fmt.Sprintf(`
define { i64, ptr, i64 } @__kml_await_fetch(ptr %%pending) {
entry:
  %%runningp = alloca i32, align 4
  %%sig_p = getelementptr { ptr, ptr, i64, i64, i64, ptr }, ptr %%pending, i32 0, i32 5
  %%sig = load ptr, ptr %%sig_p, align 8
  br label %%checkloop

checkloop:
  ; Done is checked *before* the abort signal so a re-await of an
  ; already-finished fetch (a Promise<Response> is a reusable value —
  ; TDD-00090) goes straight to the non-destructive finish and never
  ; re-enters the abort teardown on its already-cleaned easy handle (a
  ; second curl_easy_cleanup would be a double free). A still-pending
  ; transfer falls through to the per-spin abort check below.
  %%done_p = getelementptr { ptr, ptr, i64, i64, i64 }, ptr %%pending, i32 0, i32 2
  %%done = load i64, ptr %%done_p, align 8
  %%isdone = icmp ne i64 %%done, 0
  br i1 %%isdone, label %%finish, label %%chksignal

chksignal:
  ; AbortSignal check each spin (TDD-00081 Stage 3c): the aborted flag or an
  ; elapsed AbortSignal.timeout deadline tears the transfer down and throws,
  ; so a mid-flight abort or a timeout cancels the in-flight request.
  %%isaborted = call i1 @__kml_signal_aborted(ptr %%sig)
  br i1 %%isaborted, label %%doabort, label %%maybeyield

doabort:
  %%ab_easy_p = getelementptr { ptr, ptr, i64, i64, i64 }, ptr %%pending, i32 0, i32 0
  %%ab_easy = load ptr, ptr %%ab_easy_p, align 8
  %%ab_multi = load ptr, ptr @__kml_curl_multi, align 8
  call i32 @curl_multi_remove_handle(ptr %%ab_multi, ptr %%ab_easy)
  call void @curl_easy_cleanup(ptr %%ab_easy)
  ; A non-zero deadline means this signal came from AbortSignal.timeout, whose
  ; abort is a "TimeoutError" DOMException; a manual controller.abort() (no
  ; deadline) is an "AbortError" DOMException. Both carry the DOMException kind
  ; tag so 'e instanceof DOMException' (and, since DOMException inherits Error,
  ; 'e instanceof Error') both hold in the catch handler.
  %%ab_dl_p = getelementptr { i1, ptr, ptr, i64 }, ptr %%sig, i32 0, i32 3
  %%ab_dl = load i64, ptr %%ab_dl_p, align 8
  %%ab_istimeout = icmp ne i64 %%ab_dl, 0
  %%ab_name = select i1 %%ab_istimeout, ptr %s, ptr %s
  %%ab_msg = select i1 %%ab_istimeout, ptr %s, ptr %s
  %%aberr = call ptr @malloc(i64 24)
  %%aberr.kind = getelementptr { i64, ptr, ptr }, ptr %%aberr, i32 0, i32 0
  store i64 %d, ptr %%aberr.kind, align 8
  %%aberr.msg = getelementptr { i64, ptr, ptr }, ptr %%aberr, i32 0, i32 1
  store ptr %%ab_msg, ptr %%aberr.msg, align 8
  %%aberr.name = getelementptr { i64, ptr, ptr }, ptr %%aberr, i32 0, i32 2
  store ptr %%ab_name, ptr %%aberr.name, align 8
  call void @__kml_throw(ptr %%aberr)
  unreachable

maybeyield:%s

maybeconn:
  %%curidx = load i64, ptr @__kml_current_conn_idx, align 8
  %%onfiber = icmp sge i64 %%curidx, 0
  br i1 %%onfiber, label %%doyield, label %%busyspin

doyield:
  %%conndata = load ptr, ptr @__kml_conn_data, align 8
  %%selfslot = getelementptr { i64, ptr, ptr, ptr, ptr }, ptr %%conndata, i64 %%curidx
  %%pf_p = getelementptr { i64, ptr, ptr, ptr, ptr }, ptr %%selfslot, i32 0, i32 3
  store ptr %%pending, ptr %%pf_p, align 8
  %%ctx_p = getelementptr { i64, ptr, ptr, ptr, ptr }, ptr %%selfslot, i32 0, i32 1
  %%ctxptr = load ptr, ptr %%ctx_p, align 8
  call i32 @swapcontext(ptr %%ctxptr, ptr @__kml_main_ctx)
  store ptr null, ptr %%pf_p, align 8
  br label %%checkloop

busyspin:
  %%multi = load ptr, ptr @__kml_curl_multi, align 8
  call i32 @curl_multi_perform(ptr %%multi, ptr %%runningp)
  call void @__kml_curl_drain_messages()
  br label %%checkloop%s

finish:
  %%raw = call { i64, ptr, i64 } @__kml_pending_finish(ptr %%pending)
  ret { i64, ptr, i64 } %%raw
}`, timeoutNamePtr, abortNamePtr, timeoutMsgPtr, abortMsgPtr, domExcKind, awfTaskCheck, awfTaskYield))
}

// ensureAwaitFetchHeaders emits @__kml_await_fetch_headers (TDD-00097
// Stage 4): drive the multi loop until the response's headers have arrived
// (the first write-callback invocation) or the transfer is done, then return
// the HTTP status — the resolve-at-headers point `await fetch(...)` now uses,
// so `.body` can stream the rest. A busy-spin (multi_perform + drain) in
// every context: the headers phase is short, and body consumption afterwards
// runs on promise reactions. Also emits @__kml_fetch_pump, the no-fiber await
// drive's hook to keep in-flight transfers progressing (a real definition
// here; a no-op stub is emitted at finalize when fetch is unused).
func (e *Emitter) ensureAwaitFetchHeaders() {
	if e.usedAwaitFetchHeaders {
		return
	}
	e.usedAwaitFetchHeaders = true
	e.ensureFetchAsync()
	e.ensureMicrotasks()
	// Task-park path (mirrors __kml_await_fetch's): a coroutine task awaiting
	// headers parks on the fetch so the scheduler/event loop can run others
	// (incl. this process's own http accept loop — a self-fetch deadlocked on
	// the busy-spin before this). Emitted only under hasMaySuspend.
	hdrTaskCheck := "\n  br label %hmaybeconn"
	hdrTaskYield := ""
	if e.hasMaySuspend {
		hdrGCRestore := ""
		if e.isGCMode() {
			hdrGCRestore = "\n  call void @__kml_task_gc_restore()"
		}
		hdrTaskCheck = "\n  %h_task = load ptr, ptr @__kml_current_task, align 8\n  %h_ontask = icmp ne ptr %h_task, null\n  br i1 %h_ontask, label %htaskyield, label %hmaybeconn"
		hdrTaskYield = "\nhtaskyield:" +
			"\n  %h_pf_p = getelementptr " + taskStructIR + ", ptr %h_task, i32 0, i32 " + fmt.Sprintf("%d", taskPendingFetch) + "\n  store ptr %pending, ptr %h_pf_p, align 8" +
			"\n  %h_st_p = getelementptr " + taskStructIR + ", ptr %h_task, i32 0, i32 " + fmt.Sprintf("%d", taskState) + "\n  store i64 1, ptr %h_st_p, align 8" +
			"\n  %h_rc_p = getelementptr " + taskStructIR + ", ptr %h_task, i32 0, i32 " + fmt.Sprintf("%d", taskResumerCtx) + "\n  %h_rc = load ptr, ptr %h_rc_p, align 8" +
			"\n  %h_ctx_p = getelementptr " + taskStructIR + ", ptr %h_task, i32 0, i32 " + fmt.Sprintf("%d", taskCtx) + "\n  %h_ctx = load ptr, ptr %h_ctx_p, align 8" +
			"\n  %h_sjt_p = getelementptr " + taskStructIR + ", ptr %h_task, i32 0, i32 " + fmt.Sprintf("%d", taskSavedJmpTop) + "\n  %h_top = load i32, ptr @__kml_jmp_top, align 4\n  %h_top64 = zext i32 %h_top to i64\n  store i64 %h_top64, ptr %h_sjt_p, align 8" +
			"\n  %h_sw = call i32 @swapcontext(ptr %h_ctx, ptr %h_rc)" + hdrGCRestore +
			"\n  br label %checkloop"
	}
	e.emitGlobal(`
define i64 @__kml_await_fetch_headers(ptr %pending) {
entry:
  %runningp = alloca i32, align 4
  %statusslot = alloca i64, align 8
  ; A signal-carrying fetch keeps the full await (abort/timeout teardown
  ; machinery lives there) and so resolves at completion, not headers.
  %sig_p = getelementptr { ptr, ptr, i64, i64, i64, ptr }, ptr %pending, i32 0, i32 5
  %sig = load ptr, ptr %sig_p, align 8
  %hassig = icmp ne ptr %sig, null
  br i1 %hassig, label %fullawait, label %checkloop
fullawait:
  %raw = call { i64, ptr, i64 } @__kml_await_fetch(ptr %pending)
  %sst = extractvalue { i64, ptr, i64 } %raw, 0
  ret i64 %sst
checkloop:
  %done_p = getelementptr { ptr, ptr, i64, i64, i64 }, ptr %pending, i32 0, i32 2
  %done = load i64, ptr %done_p, align 8
  %isdone = icmp ne i64 %done, 0
  br i1 %isdone, label %fromdone, label %ckhdrs
ckhdrs:
  %hd_p = getelementptr { ptr, ptr, i64, i64, i64, ptr, i64, ptr, i64 }, ptr %pending, i32 0, i32 6
  %hd = load i64, ptr %hd_p, align 8
  %havehdrs = icmp ne i64 %hd, 0
  br i1 %havehdrs, label %fromeasy, label %parkcheck
parkcheck:` + hdrTaskCheck + hdrTaskYield + `
hmaybeconn:
  br label %maybeconn
maybeconn:
  ; Inside an http.listen connection fiber, park on the fetch like the full
  ; await does — the event loop resumes on headersDone or done, so other
  ; connections keep making progress during a slow upstream's header wait.
  %curidx = load i64, ptr @__kml_current_conn_idx, align 8
  %onfiber = icmp sge i64 %curidx, 0
  br i1 %onfiber, label %doyield, label %spin
doyield:
  %conndata = load ptr, ptr @__kml_conn_data, align 8
  %selfslot = getelementptr { i64, ptr, ptr, ptr, ptr }, ptr %conndata, i64 %curidx
  %ypf_p = getelementptr { i64, ptr, ptr, ptr, ptr }, ptr %selfslot, i32 0, i32 3
  store ptr %pending, ptr %ypf_p, align 8
  %yctx_p = getelementptr { i64, ptr, ptr, ptr, ptr }, ptr %selfslot, i32 0, i32 1
  %yctx = load ptr, ptr %yctx_p, align 8
  call i32 @swapcontext(ptr %yctx, ptr @__kml_main_ctx)
  store ptr null, ptr %ypf_p, align 8
  br label %checkloop
spin:
  %multi = load ptr, ptr @__kml_curl_multi, align 8
  call i32 @curl_multi_perform(ptr %multi, ptr %runningp)
  call void @__kml_curl_drain_messages()
  ; Keep microtask ordering intact while waiting on headers — a queued
  ; .then drive (ADR-00280) fires here, exactly as it did under the old
  ; resolve-at-completion await.
  call void @__kml_drain_microtasks()
  br label %checkloop
fromeasy:
  %easy_p = getelementptr { ptr, ptr, i64, i64, i64 }, ptr %pending, i32 0, i32 0
  %easy = load ptr, ptr %easy_p, align 8
  store i64 0, ptr %statusslot, align 8
  call i32 (ptr, i32, ...) @curl_easy_getinfo(ptr %easy, i32 2097154, ptr %statusslot)
  %st1 = load i64, ptr %statusslot, align 8
  ret i64 %st1
fromdone:
  ; A transfer-level failure throws via the shared finish path (which also
  ; handles the empty-body normalization we don't need here).
  %result_p = getelementptr { ptr, ptr, i64, i64, i64 }, ptr %pending, i32 0, i32 4
  %result = load i64, ptr %result_p, align 8
  %failed = icmp ne i64 %result, 0
  br i1 %failed, label %finishthrow, label %okdone
finishthrow:
  %ignored = call { i64, ptr, i64 } @__kml_pending_finish(ptr %pending)
  br label %okdone
okdone:
  %status_p = getelementptr { ptr, ptr, i64, i64, i64 }, ptr %pending, i32 0, i32 3
  %st2 = load i64, ptr %status_p, align 8
  ret i64 %st2
}
`)
}

// ensureCurlSlist declares curl_slist_append (ADR-00074/TDD-00017) —
// emit_fetch.go's buildFetchHeaderList calls it once per Map<string,string>
// entry to build the linked list CURLOPT_HTTPHEADER expects. No
// curl_slist_free_all declared/called: like every other fetch-related
// allocation (the pending struct, the response buffer, the Response object
// itself), the built list is never freed in manual mode — consistent with
// this feature area's already-documented "everything leaks, -mm=gc is the
// opt-in fix" characteristic (TDD-00001), not a new gap. -mm=gc's Boehm
// collector couldn't reclaim it anyway: curl_slist_append is libcurl's own
// internal malloc, not this compiler's GC-instrumented one.
func (e *Emitter) ensureCurlSlist() {
	if e.usedCurlSlist {
		return
	}
	e.usedCurlSlist = true
	e.requireLink("curl")
	e.emitGlobal("declare ptr @curl_slist_append(ptr noundef, ptr noundef)")
}

// ensurePromiseCombinators declares the group-wait primitives
// Promise.all/.race/.allSettled (emit_promise.go, ADR-00073) use to wait on
// N pending fetches at once, rather than the one-at-a-time
// __kml_await_fetch above. A "group" is a malloc'd { ptr membersArr, i64
// count, i64 mode } (24 bytes): membersArr is a malloc'd ptr[count] of
// individual pending-fetch handles (the exact same 40-byte struct
// __kml_fetch_async already produces — a group never wraps or duplicates
// them, just points at the same ones the calling code already dereferenced
// from each Promise<Response> slot); mode 0 = wait for every member done
// (.all/.allSettled), 1 = wait for the first (.race). Lazily emitted only
// when a Promise combinator call over an Array<Promise<Response>> actually
// needs it — same one-time-emission pattern as ensureFetchAsync itself.
func (e *Emitter) ensurePromiseCombinators() {
	if e.usedPromiseCombinators {
		return
	}
	e.usedPromiseCombinators = true
	e.ensureFetchAsync()
	e.ensureMalloc()

	// __kml_group_satisfied(ptr group) -> i1: mode 0 (all) is satisfied once every
	// member's done flag is set; mode 1 (race) as soon as any member is done; mode
	// 2 (any, TDD-00084 Part C) as soon as any member is done *and transport-
	// succeeded* (result == 0), or once every member is done (all failed → the
	// caller throws an AggregateError). Called by __kml_await_group_wait's poll
	// loop below and by the event loop's rcheckgroup resume-scan.
	e.emitGlobal(`
define i1 @__kml_group_satisfied(ptr %group) {
entry:
  %members_p = getelementptr { ptr, i64, i64 }, ptr %group, i32 0, i32 0
  %members = load ptr, ptr %members_p, align 8
  %count_p = getelementptr { ptr, i64, i64 }, ptr %group, i32 0, i32 1
  %count = load i64, ptr %count_p, align 8
  %mode_p = getelementptr { ptr, i64, i64 }, ptr %group, i32 0, i32 2
  %mode = load i64, ptr %mode_p, align 8
  %allmode = icmp eq i64 %mode, 0
  %anymode = icmp eq i64 %mode, 2
  br label %loop

loop:
  %i = phi i64 [ 0, %entry ], [ %inext, %nextmerge ]
  %anynotdone = phi i1 [ false, %entry ], [ %anynotdone2, %nextmerge ]
  %reachedend = icmp sge i64 %i, %count
  br i1 %reachedend, label %loopend, label %body

body:
  %member_p = getelementptr ptr, ptr %members, i64 %i
  %member = load ptr, ptr %member_p, align 8
  %done_p = getelementptr { ptr, ptr, i64, i64, i64 }, ptr %member, i32 0, i32 2
  %done = load i64, ptr %done_p, align 8
  %isdone = icmp ne i64 %done, 0
  br i1 %anymode, label %anybody, label %notany

notany:
  br i1 %allmode, label %checkall, label %checkrace

checkall:
  br i1 %isdone, label %next_keep, label %notsatisfied

checkrace:
  br i1 %isdone, label %satisfied, label %next_keep

anybody:
  br i1 %isdone, label %anydone, label %next_notdone

anydone:
  %result_p = getelementptr { ptr, ptr, i64, i64, i64 }, ptr %member, i32 0, i32 4
  %result = load i64, ptr %result_p, align 8
  %success = icmp eq i64 %result, 0
  br i1 %success, label %satisfied, label %next_keep

satisfied:
  ret i1 1

notsatisfied:
  ret i1 0

next_keep:
  br label %nextmerge

next_notdone:
  br label %nextmerge

nextmerge:
  %anynotdone2 = phi i1 [ %anynotdone, %next_keep ], [ true, %next_notdone ]
  %inext = add i64 %i, 1
  br label %loop

loopend:
  %notanynotdone = xor i1 %anynotdone, true
  %anyresult = select i1 %anymode, i1 %notanynotdone, i1 0
  %result_final = select i1 %allmode, i1 1, i1 %anyresult
  ret i1 %result_final
}`)

	// __kml_first_done_index(ptr group) -> i64: only used by .race, after
	// __kml_await_group_wait (mode=1) has already returned, so at least one
	// member is guaranteed done. Returns -1 if somehow called without that
	// precondition holding (defensive, never expected in practice).
	e.emitGlobal(`
define i64 @__kml_first_done_index(ptr %group) {
entry:
  %members_p = getelementptr { ptr, i64, i64 }, ptr %group, i32 0, i32 0
  %members = load ptr, ptr %members_p, align 8
  %count_p = getelementptr { ptr, i64, i64 }, ptr %group, i32 0, i32 1
  %count = load i64, ptr %count_p, align 8
  br label %loop

loop:
  %i = phi i64 [ 0, %entry ], [ %inext, %next ]
  %reachedend = icmp sge i64 %i, %count
  br i1 %reachedend, label %notfound, label %body

body:
  %member_p = getelementptr ptr, ptr %members, i64 %i
  %member = load ptr, ptr %member_p, align 8
  %done_p = getelementptr { ptr, ptr, i64, i64, i64 }, ptr %member, i32 0, i32 2
  %done = load i64, ptr %done_p, align 8
  %isdone = icmp ne i64 %done, 0
  br i1 %isdone, label %found, label %next

found:
  ret i64 %i

next:
  %inext = add i64 %i, 1
  br label %loop

notfound:
  ret i64 -1
}`)

	// __kml_first_success_index(ptr group) -> i64: for Promise.any over fetches
	// (TDD-00084 Part C) — the first member that is done and transport-succeeded
	// (result == 0), or -1 if none succeeded (all failed → the caller throws an
	// AggregateError). Called after __kml_await_group_wait (mode = 2) returns.
	e.emitGlobal(`
define i64 @__kml_first_success_index(ptr %group) {
entry:
  %members_p = getelementptr { ptr, i64, i64 }, ptr %group, i32 0, i32 0
  %members = load ptr, ptr %members_p, align 8
  %count_p = getelementptr { ptr, i64, i64 }, ptr %group, i32 0, i32 1
  %count = load i64, ptr %count_p, align 8
  br label %loop
loop:
  %i = phi i64 [ 0, %entry ], [ %inext, %next ]
  %reached = icmp sge i64 %i, %count
  br i1 %reached, label %notfound, label %body
body:
  %member_p = getelementptr ptr, ptr %members, i64 %i
  %member = load ptr, ptr %member_p, align 8
  %done_p = getelementptr { ptr, ptr, i64, i64, i64 }, ptr %member, i32 0, i32 2
  %done = load i64, ptr %done_p, align 8
  %isdone = icmp ne i64 %done, 0
  br i1 %isdone, label %chkres, label %next
chkres:
  %result_p = getelementptr { ptr, ptr, i64, i64, i64 }, ptr %member, i32 0, i32 4
  %result = load i64, ptr %result_p, align 8
  %success = icmp eq i64 %result, 0
  br i1 %success, label %found, label %next
found:
  ret i64 %i
next:
  %inext = add i64 %i, 1
  br label %loop
notfound:
  ret i64 -1
}`)

	// __kml_group_throw_aggregate(ptr group): Promise.any's all-rejected path
	// (TDD-00084 Part C) — build an AggregateError whose `.errors` are one Error
	// per member carrying that fetch's transport-failure message, then throw it.
	{
		e.ensureExceptionHelpers()
		errName := e.internString("Error")
		aggName := e.internString("AggregateError")
		aggMsg := e.internString("All promises were rejected")
		aggID := errorKindIDs["AggregateError"]
		e.emitGlobal(fmt.Sprintf(`
define void @__kml_group_throw_aggregate(ptr %%group) {
entry:
  %%members_p = getelementptr { ptr, i64, i64 }, ptr %%group, i32 0, i32 0
  %%members = load ptr, ptr %%members_p, align 8
  %%count_p = getelementptr { ptr, i64, i64 }, ptr %%group, i32 0, i32 1
  %%count = load i64, ptr %%count_p, align 8
  %%bytes = mul i64 %%count, 8
  %%errArr = call ptr @malloc(i64 %%bytes)
  br label %%loop
loop:
  %%i = phi i64 [ 0, %%entry ], [ %%inext, %%cont ]
  %%go = icmp slt i64 %%i, %%count
  br i1 %%go, label %%body, label %%done
body:
  %%mg = getelementptr ptr, ptr %%members, i64 %%i
  %%m = load ptr, ptr %%mg, align 8
  %%res_p = getelementptr { ptr, ptr, i64, i64, i64 }, ptr %%m, i32 0, i32 4
  %%res = load i64, ptr %%res_p, align 8
  %%res32 = trunc i64 %%res to i32
  %%errstr = call ptr @curl_easy_strerror(i32 %%res32)
  %%errstr_hdr = call ptr @__kml_str_from_cstr(ptr %%errstr)
  %%eo = call ptr @malloc(i64 24)
  %%eo_k = getelementptr { i64, ptr, ptr }, ptr %%eo, i32 0, i32 0
  store i64 0, ptr %%eo_k, align 8
  %%eo_m = getelementptr { i64, ptr, ptr }, ptr %%eo, i32 0, i32 1
  store ptr %%errstr_hdr, ptr %%eo_m, align 8
  %%eo_n = getelementptr { i64, ptr, ptr }, ptr %%eo, i32 0, i32 2
  store ptr %s, ptr %%eo_n, align 8
  %%dst = getelementptr ptr, ptr %%errArr, i64 %%i
  store ptr %%eo, ptr %%dst, align 8
  br label %%cont
cont:
  %%inext = add i64 %%i, 1
  br label %%loop
done:
  %%agg = call ptr @malloc(i64 %d)
  %%a_k = getelementptr %s, ptr %%agg, i32 0, i32 0
  store i64 %d, ptr %%a_k, align 8
  %%a_m = getelementptr %s, ptr %%agg, i32 0, i32 1
  store ptr %s, ptr %%a_m, align 8
  %%a_n = getelementptr %s, ptr %%agg, i32 0, i32 2
  store ptr %s, ptr %%a_n, align 8
  %%a_d = getelementptr %s, ptr %%agg, i32 0, i32 3
  store ptr %%errArr, ptr %%a_d, align 8
  %%a_l = getelementptr %s, ptr %%agg, i32 0, i32 4
  store i64 %%count, ptr %%a_l, align 8
  call void @__kml_throw(ptr %%agg)
  unreachable
}`, errName, aggregateErrorStructSize, aggregateErrorStructIR, aggID,
			aggregateErrorStructIR, aggMsg, aggregateErrorStructIR, aggName, aggregateErrorStructIR, aggregateErrorStructIR))
	}

	e.ensurePendingFinishSettled()

	// __kml_await_group_wait(ptr group) -> void: structural clone of
	// __kml_await_fetch's checkloop/maybeyield/doyield/busyspin shape above,
	// just polling __kml_group_satisfied instead of a single pending's own
	// done flag, and parking on the connection's pendingGroup field (index
	// 4) instead of pendingFetch (index 3) when yielding. No return value —
	// the caller already holds %group and re-derives whatever it needs
	// (winner index via __kml_first_done_index, per-member results via
	// __kml_pending_finish/__kml_pending_finish_settled) once this returns.
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_await_group_wait(ptr %%group) {
entry:
  %%runningp = alloca i32, align 4
  br label %%checkloop

checkloop:
  %%sat = call i1 @__kml_group_satisfied(ptr %%group)
  br i1 %%sat, label %%finish, label %%maybeyield

maybeyield:
  %%curidx = load i64, ptr @__kml_current_conn_idx, align 8
  %%onfiber = icmp sge i64 %%curidx, 0
  br i1 %%onfiber, label %%doyield, label %%busyspin

doyield:
  %%conndata = load ptr, ptr @__kml_conn_data, align 8
  %%selfslot = getelementptr { i64, ptr, ptr, ptr, ptr }, ptr %%conndata, i64 %%curidx
  %%pg_p = getelementptr { i64, ptr, ptr, ptr, ptr }, ptr %%selfslot, i32 0, i32 4
  store ptr %%group, ptr %%pg_p, align 8
  %%ctx_p = getelementptr { i64, ptr, ptr, ptr, ptr }, ptr %%selfslot, i32 0, i32 1
  %%ctxptr = load ptr, ptr %%ctx_p, align 8
  call i32 @swapcontext(ptr %%ctxptr, ptr @__kml_main_ctx)
  store ptr null, ptr %%pg_p, align 8
  br label %%checkloop

busyspin:
  %%multi = load ptr, ptr @__kml_curl_multi, align 8
  call i32 @curl_multi_perform(ptr %%multi, ptr %%runningp)
  call void @__kml_curl_drain_messages()
  br label %%checkloop

finish:
  ret void
}`))
}

// ensurePendingFinishSettled declares __kml_pending_finish_settled(ptr
// pending) -> {i1 failed, i64 status, ptr body, ptr reasonMsg, i64
// bodyLen}: a non-throwing sibling of __kml_pending_finish. Originally
// built only for Promise.allSettled (which by definition must not abort on
// an individual member's transport failure) — factored out of
// ensurePromiseCombinators into its own ensure*() so XMLHttpRequest.send()
// (TDD-00040, emit_xhr.go's ensureFetchAwaitSettled below) can reuse it
// too without pulling in the group-wait machinery a program using XHR but
// never Promise.all/.race/.allSettled has no use for. On failure,
// reasonMsg is the same curl_easy_strerror() string __kml_pending_finish's
// neterror block already throws with — the caller decides what to do with
// it (emit_promise.go wraps it into a real Error object for
// .allSettled; emit_xhr.go's send() surfaces it by firing .onerror
// instead). bodyLen is 0 on failure (no body was ever received).
func (e *Emitter) ensurePendingFinishSettled() {
	if e.usedPendingFinishSettled {
		return
	}
	e.usedPendingFinishSettled = true
	e.ensureFetch()

	e.emitGlobal(`
define { i1, i64, ptr, ptr, i64 } @__kml_pending_finish_settled(ptr %pending) {
entry:
  %result_p = getelementptr { ptr, ptr, i64, i64, i64 }, ptr %pending, i32 0, i32 4
  %result = load i64, ptr %result_p, align 8
  %failed = icmp ne i64 %result, 0
  br i1 %failed, label %neterror, label %ok

neterror:
  %result32b = trunc i64 %result to i32
  %errstr = call ptr @curl_easy_strerror(i32 %result32b)
  %errstr_hdr = call ptr @__kml_str_from_cstr(ptr %errstr)
  %rf1 = insertvalue { i1, i64, ptr, ptr, i64 } undef, i1 1, 0
  %rf2 = insertvalue { i1, i64, ptr, ptr, i64 } %rf1, i64 0, 1
  %rf3 = insertvalue { i1, i64, ptr, ptr, i64 } %rf2, ptr null, 2
  %rf4 = insertvalue { i1, i64, ptr, ptr, i64 } %rf3, ptr %errstr_hdr, 3
  %rf5 = insertvalue { i1, i64, ptr, ptr, i64 } %rf4, i64 0, 4
  ret { i1, i64, ptr, ptr, i64 } %rf5

ok:
  %status_p = getelementptr { ptr, ptr, i64, i64, i64 }, ptr %pending, i32 0, i32 3
  %status = load i64, ptr %status_p, align 8
  %buf_p = getelementptr { ptr, ptr, i64, i64, i64 }, ptr %pending, i32 0, i32 1
  %buf = load ptr, ptr %buf_p, align 8
  %bodyptr_p = getelementptr { ptr, i64, i64 }, ptr %buf, i32 0, i32 0
  %bodyptr = load ptr, ptr %bodyptr_p, align 8
  %bodylen_p = getelementptr { ptr, i64, i64 }, ptr %buf, i32 0, i32 1
  %bodylen = load i64, ptr %bodylen_p, align 8

  %isnullbody = icmp eq ptr %bodyptr, null
  br i1 %isnullbody, label %emptybody, label %havebody

emptybody:
  %emptystr = call ptr @malloc(i64 1)
  store i8 0, ptr %emptystr, align 1
  br label %retdone

havebody:
  br label %retdone

retdone:
  %bodyfinal = phi ptr [ %emptystr, %emptybody ], [ %bodyptr, %havebody ]
  %bodylenfinal = phi i64 [ 0, %emptybody ], [ %bodylen, %havebody ]
  %ro1 = insertvalue { i1, i64, ptr, ptr, i64 } undef, i1 0, 0
  %ro2 = insertvalue { i1, i64, ptr, ptr, i64 } %ro1, i64 %status, 1
  %ro3 = insertvalue { i1, i64, ptr, ptr, i64 } %ro2, ptr %bodyfinal, 2
  %ro4 = insertvalue { i1, i64, ptr, ptr, i64 } %ro3, ptr null, 3
  %ro5 = insertvalue { i1, i64, ptr, ptr, i64 } %ro4, i64 %bodylenfinal, 4
  ret { i1, i64, ptr, ptr, i64 } %ro5
}`)
}

// ensureFetchAwaitSettled declares __kml_await_fetch_settled(ptr pending)
// -> {i1, i64, ptr, ptr, i64} (TDD-00040): a structural clone of
// __kml_await_fetch's own checkloop/maybeyield/doyield/busyspin loop above
// — same fiber-yield-if-possible, busy-spin-otherwise waiting behavior,
// including yielding a connection-handler fiber exactly like `await
// fetch(...)` already does — except it finishes via
// __kml_pending_finish_settled instead of the throwing
// __kml_pending_finish. This is XMLHttpRequest.send()'s entire transfer
// mechanism (emit_xhr.go): send() looks synchronous from TS code, but is
// built on the exact same non-blocking primitive fetch() itself uses, and
// never throws on a network failure (real XMLHttpRequest doesn't either —
// it fires .onerror instead, which is exactly what the {i1 failed, ...}
// result lets emit_xhr.go's codegen branch on directly).
func (e *Emitter) ensureFetchAwaitSettled() {
	if e.usedFetchAwaitSettled {
		return
	}
	e.usedFetchAwaitSettled = true
	e.ensureFetchAsync()
	e.ensurePendingFinishSettled()

	e.emitGlobal(fmt.Sprintf(`
define { i1, i64, ptr, ptr, i64 } @__kml_await_fetch_settled(ptr %%pending) {
entry:
  %%runningp = alloca i32, align 4
  br label %%checkloop

checkloop:
  %%done_p = getelementptr { ptr, ptr, i64, i64, i64 }, ptr %%pending, i32 0, i32 2
  %%done = load i64, ptr %%done_p, align 8
  %%isdone = icmp ne i64 %%done, 0
  br i1 %%isdone, label %%finish, label %%maybeyield

maybeyield:
  %%curidx = load i64, ptr @__kml_current_conn_idx, align 8
  %%onfiber = icmp sge i64 %%curidx, 0
  br i1 %%onfiber, label %%doyield, label %%busyspin

doyield:
  %%conndata = load ptr, ptr @__kml_conn_data, align 8
  %%selfslot = getelementptr { i64, ptr, ptr, ptr, ptr }, ptr %%conndata, i64 %%curidx
  %%pf_p = getelementptr { i64, ptr, ptr, ptr, ptr }, ptr %%selfslot, i32 0, i32 3
  store ptr %%pending, ptr %%pf_p, align 8
  %%ctx_p = getelementptr { i64, ptr, ptr, ptr, ptr }, ptr %%selfslot, i32 0, i32 1
  %%ctxptr = load ptr, ptr %%ctx_p, align 8
  call i32 @swapcontext(ptr %%ctxptr, ptr @__kml_main_ctx)
  store ptr null, ptr %%pf_p, align 8
  br label %%checkloop

busyspin:
  %%multi = load ptr, ptr @__kml_curl_multi, align 8
  call i32 @curl_multi_perform(ptr %%multi, ptr %%runningp)
  call void @__kml_curl_drain_messages()
  br label %%checkloop

finish:
  %%raw = call { i1, i64, ptr, ptr, i64 } @__kml_pending_finish_settled(ptr %%pending)
  ret { i1, i64, ptr, ptr, i64 } %%raw
}`))
}


// ensureFetchHeadersMap declares __kml_fetch_headers_map (ADR-00490):
// lazily parses a Response's captured raw header text (the hbuf reachable
// from the body buffer's slot 4) into a Map<string,string> with
// **lowercased** keys, matching the Fetch spec's Headers case rule. Status
// lines and blank lines are skipped; values are left-trimmed of spaces. A
// null pending (combinator-built Response) yields an empty map.
func (e *Emitter) ensureFetchHeadersMap() {
	if e.usedFetchHeadersMap {
		return
	}
	e.usedFetchHeadersMap = true
	e.ensureFetch()
	e.ensureMapStrHelpers()
	e.ensureStrHeaderRuntime()
	e.ensureMalloc()
	e.ensureMemcpy()
	e.emitGlobal(`
define ptr @__kml_fetch_headers_map(ptr %pending) {
entry:
  %map = call ptr @__kml_map_str_create()
  %pnull = icmp eq ptr %pending, null
  br i1 %pnull, label %ret, label %getbuf
getbuf:
  %bufp = getelementptr { ptr, ptr, i64, i64, i64 }, ptr %pending, i32 0, i32 1
  %buf = load ptr, ptr %bufp, align 8
  %bnull = icmp eq ptr %buf, null
  br i1 %bnull, label %ret, label %gethb
gethb:
  %hbpp = getelementptr ptr, ptr %buf, i64 4
  %hbuf = load ptr, ptr %hbpp, align 8
  %hnull = icmp eq ptr %hbuf, null
  br i1 %hnull, label %ret, label %getraw
getraw:
  %rawp = getelementptr { ptr, i64, i64 }, ptr %hbuf, i32 0, i32 0
  %raw = load ptr, ptr %rawp, align 8
  %rnull = icmp eq ptr %raw, null
  br i1 %rnull, label %ret, label %outer
outer:
  %line = phi ptr [ %raw, %getraw ], [ %nextline, %advance ]
  %c0 = load i8, ptr %line, align 1
  %atend = icmp eq i8 %c0, 0
  br i1 %atend, label %ret, label %inner
inner:
  %p = phi ptr [ %line, %outer ], [ %pn, %icont ]
  %c = load i8, ptr %p, align 1
  switch i8 %c, label %icont [
    i8 0, label %ret
    i8 13, label %noheader
    i8 10, label %noheader
    i8 58, label %colon
  ]
icont:
  %pn = getelementptr i8, ptr %p, i64 1
  br label %inner
colon:
  %klen_p = ptrtoint ptr %p to i64
  %kstart = ptrtoint ptr %line to i64
  %klen = sub i64 %klen_p, %kstart
  %kz = icmp eq i64 %klen, 0
  br i1 %kz, label %noheader, label %copykey
copykey:
  %klen1 = add i64 %klen, 1
  %kbuf = call ptr @malloc(i64 %klen1)
  br label %kloop
kloop:
  %ki = phi i64 [ 0, %copykey ], [ %kin, %kstore ]
  %kdone = icmp sge i64 %ki, %klen
  br i1 %kdone, label %kfin, label %kbody
kbody:
  %ksrc = getelementptr i8, ptr %line, i64 %ki
  %kc = load i8, ptr %ksrc, align 1
  %isUp1 = icmp sge i8 %kc, 65
  %isUp2 = icmp sle i8 %kc, 90
  %isUp = and i1 %isUp1, %isUp2
  %low = add i8 %kc, 32
  %kcl = select i1 %isUp, i8 %low, i8 %kc
  br label %kstore
kstore:
  %kdst = getelementptr i8, ptr %kbuf, i64 %ki
  store i8 %kcl, ptr %kdst, align 1
  %kin = add i64 %ki, 1
  br label %kloop
kfin:
  %kterm = getelementptr i8, ptr %kbuf, i64 %klen
  store i8 0, ptr %kterm, align 1
  br label %vskip
vskip:
  %v = phi ptr [ %p, %kfin ], [ %vn, %vskipnext ]
  %vn = getelementptr i8, ptr %v, i64 1
  %vc = load i8, ptr %vn, align 1
  %issp = icmp eq i8 %vc, 32
  br i1 %issp, label %vskipnext, label %vstartb
vskipnext:
  br label %vskip
vstartb:
  br label %vscan
vscan:
  %q = phi ptr [ %vn, %vstartb ], [ %qn, %vcont ]
  %qc = load i8, ptr %q, align 1
  %qend1 = icmp eq i8 %qc, 13
  %qend2 = icmp eq i8 %qc, 10
  %qend3 = icmp eq i8 %qc, 0
  %qe12 = or i1 %qend1, %qend2
  %qend = or i1 %qe12, %qend3
  br i1 %qend, label %vfin, label %vcont
vcont:
  %qn = getelementptr i8, ptr %q, i64 1
  br label %vscan
vfin:
  %vi_end = ptrtoint ptr %q to i64
  %vi_start = ptrtoint ptr %vn to i64
  %vlen = sub i64 %vi_end, %vi_start
  %vlen1 = add i64 %vlen, 1
  %vbuf = call ptr @malloc(i64 %vlen1)
  %ign2 = call ptr @memcpy(ptr %vbuf, ptr %vn, i64 %vlen)
  %vterm = getelementptr i8, ptr %vbuf, i64 %vlen
  store i8 0, ptr %vterm, align 1
  %kh = call ptr @__kml_str_from_cstr(ptr %kbuf)
  %vh = call ptr @__kml_str_from_cstr(ptr %vbuf)
  %vhi = ptrtoint ptr %vh to i64
  call void @__kml_map_str_set(ptr %map, ptr %kh, i64 %vhi)
  br label %skipnl0
skipnl0:
  br label %skipnl
noheader:
  br label %skipnl
skipnl:
  %sp0 = phi ptr [ %q, %skipnl0 ], [ %p, %noheader ], [ %spn, %spcont ]
  %sc = load i8, ptr %sp0, align 1
  %isnl1 = icmp eq i8 %sc, 13
  %isnl2 = icmp eq i8 %sc, 10
  %isnl = or i1 %isnl1, %isnl2
  br i1 %isnl, label %spcont, label %advance
spcont:
  %spn = getelementptr i8, ptr %sp0, i64 1
  br label %skipnl
advance:
  %nextline = phi ptr [ %sp0, %skipnl ]
  br label %outer
ret:
  ret ptr %map
}`)
}


// ensureXHRHeadersAll declares __kml_xhr_headers_all (ADR-00490):
// serializes a parsed response-header map into the "name: value\r\n"
// concatenation getAllResponseHeaders() returns, in stored (arrival)
// order. A null map (send() not yet DONE, or a network failure) yields
// the empty string, matching the spec's "" return for those states.
func (e *Emitter) ensureXHRHeadersAll() {
	if e.usedXHRHeadersAll {
		return
	}
	e.usedXHRHeadersAll = true
	e.ensureMapStrHelpers()
	e.ensureStrHeaderRuntime()
	e.ensureStrlen()
	e.ensureMemcpy()
	e.emitGlobal(`
define ptr @__kml_xhr_headers_all(ptr %map) {
entry:
  %mnull = icmp eq ptr %map, null
  br i1 %mnull, label %empty, label %measure
empty:
  %es = call ptr @__kml_str_alloc(i64 0)
  store i8 0, ptr %es, align 1
  ret ptr %es
measure:
  %size = load i64, ptr %map, align 8
  %keys_p = getelementptr i8, ptr %map, i64 16
  %keys = load ptr, ptr %keys_p, align 8
  %vals_p = getelementptr i8, ptr %map, i64 24
  %vals = load ptr, ptr %vals_p, align 8
  br label %mloop
mloop:
  %i = phi i64 [ 0, %measure ], [ %in, %mbody ]
  %tot = phi i64 [ 0, %measure ], [ %tot2, %mbody ]
  %mdone = icmp sge i64 %i, %size
  br i1 %mdone, label %alloc, label %mbody
mbody:
  %kslot = getelementptr ptr, ptr %keys, i64 %i
  %kp = load ptr, ptr %kslot, align 8
  %klen = call i64 @strlen(ptr %kp)
  %vslot = getelementptr i64, ptr %vals, i64 %i
  %vraw = load i64, ptr %vslot, align 8
  %vp = inttoptr i64 %vraw to ptr
  %vlen = call i64 @strlen(ptr %vp)
  %pair = add i64 %klen, %vlen
  %pair4 = add i64 %pair, 4
  %tot2 = add i64 %tot, %pair4
  %in = add i64 %i, 1
  br label %mloop
alloc:
  %out = call ptr @__kml_str_alloc(i64 %tot)
  br label %wloop
wloop:
  %j = phi i64 [ 0, %alloc ], [ %jn, %wbody ]
  %off = phi i64 [ 0, %alloc ], [ %off4, %wbody ]
  %wdone = icmp sge i64 %j, %size
  br i1 %wdone, label %fin, label %wbody
wbody:
  %kslot2 = getelementptr ptr, ptr %keys, i64 %j
  %kp2 = load ptr, ptr %kslot2, align 8
  %klen2 = call i64 @strlen(ptr %kp2)
  %dst0 = getelementptr i8, ptr %out, i64 %off
  %ign1 = call ptr @memcpy(ptr %dst0, ptr %kp2, i64 %klen2)
  %off1 = add i64 %off, %klen2
  %dst1 = getelementptr i8, ptr %out, i64 %off1
  store i8 58, ptr %dst1, align 1
  %off1b = add i64 %off1, 1
  %dst1b = getelementptr i8, ptr %out, i64 %off1b
  store i8 32, ptr %dst1b, align 1
  %off2 = add i64 %off1b, 1
  %vslot2 = getelementptr i64, ptr %vals, i64 %j
  %vraw2 = load i64, ptr %vslot2, align 8
  %vp2 = inttoptr i64 %vraw2 to ptr
  %vlen2 = call i64 @strlen(ptr %vp2)
  %dst2 = getelementptr i8, ptr %out, i64 %off2
  %ign2 = call ptr @memcpy(ptr %dst2, ptr %vp2, i64 %vlen2)
  %off3 = add i64 %off2, %vlen2
  %dst3 = getelementptr i8, ptr %out, i64 %off3
  store i8 13, ptr %dst3, align 1
  %off3b = add i64 %off3, 1
  %dst3b = getelementptr i8, ptr %out, i64 %off3b
  store i8 10, ptr %dst3b, align 1
  %off4 = add i64 %off3b, 1
  %jn = add i64 %j, 1
  br label %wloop
fin:
  %term = getelementptr i8, ptr %out, i64 %tot
  store i8 0, ptr %term, align 1
  ret ptr %out
}`)
}
