package llvm

import "fmt"

// runtime_eventsource.go — EventSource (Server-Sent Events) C-runtime
// helpers. See docs/tdd/TDD-00038.md for the full design (Stages 0-2:
// connection plumbing, SSE record parsing/dispatch, named events/lifecycle
// callbacks; Stage 3: CRLF-tolerant record boundaries, non-2xx/wrong-
// Content-Type terminal failure, and auto-reconnect) and
// docs/adr/ADR-00122.md/ADR-00123.md/ADR-00124.md for Stages 0-2's own
// write-ups.
//
// Deliberately reuses as much of ensureFetchAsync's existing machinery
// (runtime_fetch.go) as possible rather than inventing new C-level
// primitives: an EventSource's transfer is a libcurl easy handle added to
// the *same* global @__kml_curl_multi handle fetch already creates on first
// use, and its own "is this transfer still going" bookkeeping is the exact
// same 40-byte pending-fetch struct (`{ ptr easy, ptr buf, i64 done, i64
// status, i64 result }`) __kml_fetch_async already builds — so
// __kml_curl_drain_messages (unmodified) already detects "this SSE
// connection ended" (successfully or via a network error) for free, the
// same way it already detects a completed fetch. One deliberate difference
// from a plain fetch: no CURLOPT_TIMEOUT (13) is set — fetch's 30s hard cap
// exists because an ordinary HTTP response should complete promptly; an SSE
// stream is *meant* to stay open indefinitely, so capping it would be
// actively wrong, not an oversight.
//
// The "entry" struct (`{ ptr pending, ptr instance, i64 consumedOffset, i64
// state, ptr listeners, ptr url, i64 retryMs, i64 reconnectAtMs, ptr
// contentType }`, 72 bytes) is what's actually new here: pending is the
// struct described above; instance is the KML-level EventSource object
// pointer (the two-way link __kml_eventsource_scan needs to write a
// readyState transition back into user-visible object state); consumedOffset
// tracks how many bytes of pending->buf have been processed; state is
// 0=CONNECTING, 1=OPEN, 2=CLOSED; listeners is a Map<string,ptr> of
// event-type -> __kml_ee_list (TDD-00038 Stage 2); url/retryMs/reconnectAtMs/
// contentType are Stage 3's own auto-reconnect and terminal-failure-detection
// fields — see __kml_eventsource_scan's own doc comment for how they're
// used. Entries live in a flat, grow-only `{ptr,i64,i64}`-style
// array (@__kml_es_data/_len/_cap, one malloc'd entry pointer per slot —
// the same realloc-doubling shape the timer queue uses, just element size 8
// instead of a larger inline struct, since each element here is only ever a
// pointer to its own independently malloc'd entry). @__kml_es_active is a
// live counter (mirroring @__kml_conn_active's existing pattern) — the
// event loop's own "is there still work to do" check is a cheap counter
// compare, not a full array scan; an entry waiting to reconnect (Stage 3)
// still counts as active, exactly like a live transfer, since the event
// loop must keep running for the reconnect to ever happen. .close() marks
// an entry CLOSED in place rather than ever removing it from the array
// (same "grow-only, dead entries flagged" convention @__kml_conn_active
// already established).
//
//	__kml_eventsource_connect(ptr url, ptr headers) -> ptr pending
//	  Stage 3: factored out of what used to be __kml_eventsource_open's own
//	  inline body — builds one libcurl easy handle + pending struct
//	  (identical setopts to __kml_fetch_async, minus CURLOPT_TIMEOUT),
//	  lazily creates the shared CURLM multi handle exactly like
//	  __kml_fetch_async does, optionally attaches headers (a curl_slist*,
//	  nullable — used by a reconnect to replay Last-Event-ID, unused by the
//	  original connect), adds the easy handle to the multi handle, and calls
//	  curl_multi_perform() once to kick the transfer off. Shared by
//	  __kml_eventsource_open (the original connect) and
//	  __kml_eventsource_scan's reconnect step (Stage 3) — the only
//	  difference between the two call sites is whether headers is null.
//	__kml_eventsource_open(ptr url, ptr instance) -> ptr
//	  Calls __kml_eventsource_connect(url, null), builds the entry struct
//	  (state starts CONNECTING, retryMs defaults to 3000 — the real spec's
//	  own default reconnection time, reconnectAtMs starts 0 meaning "has a
//	  live transfer right now"), appends it to the array, increments
//	  @__kml_es_active, and returns the entry pointer — emit_eventsource.go
//	  stores this into the KML object's own hidden EventSourceHandleField.
//	__kml_eventsource_close(ptr entry)
//	  Idempotent (a no-op if already CLOSED, matching real EventSource's own
//	  close() being safe to call more than once). Branches on
//	  reconnectAtMs: nonzero means the entry is currently mid-retry-wait
//	  with no live easy handle to tear down (Stage 3) — skips straight to
//	  marking CLOSED; zero means a live easy handle exists — does the usual
//	  curl_multi_remove_handle + curl_easy_cleanup first. Either way, marks
//	  state CLOSED and decrements @__kml_es_active. Does NOT free
//	  `pending`/the entry itself or write the object's own readyState field
//	  — emit_eventsource.go's Go-level codegen does the readyState write
//	  directly (synchronously, matching real EventSource), and leaving the
//	  allocations unfreed matches this compiler's existing
//	  manual-mode-never-frees posture.
//	__kml_eventsource_now_ms() -> i64
//	  Stage 3: current time in milliseconds off CLOCK_MONOTONIC, used only
//	  for reconnect-deadline math (reconnectAtMs is an absolute deadline in
//	  this same clock). A small, standalone helper (declares clock_gettime
//	  itself via ensureClockGettime()) rather than reusing emit_timers.go's
//	  __kml_monotonic_ns — reconnecting is pure runtime bookkeeping, not
//	  user-visible KML code, so pulling in the general-purpose setTimeout
//	  timer-queue subsystem just for this would be indirection with no
//	  payoff.
//	__kml_eventsource_scan()
//	  Called once per __kml_event_loop_run iteration (right after that
//	  loop's own existing curl_multi_perform/__kml_curl_drain_messages
//	  calls, so every entry's pending->done/buf are already fresh this
//	  iteration). Per entry, skips if CLOSED; otherwise, if reconnectAtMs is
//	  nonzero (Stage 3: waiting to reconnect), checks whether the deadline
//	  has passed and if so rebuilds the connection (replaying Last-Event-ID
//	  as a header when the instance has one) via __kml_eventsource_connect;
//	  otherwise dispatches on state: CONNECTING reads the HTTP status —
//	  live via curl_easy_getinfo(CURLINFO_RESPONSE_CODE) while the transfer
//	  is still in flight (available as soon as libcurl has parsed response
//	  headers, independent of body bytes — Stage 3 replaces Stage 0's old
//	  "first body byte" proxy for the open transition with this more
//	  accurate header-arrival check), or from pending->status (captured
//	  safely by __kml_curl_drain_messages before cleanup) once the transfer
//	  has already ended, since the easy handle itself is gone by then — and
//	  reads Content-Type from the entry's own contentType field, populated
//	  by __kml_eventsource_header_cb as headers arrive rather than queried
//	  live at all (the only safe way to read it once a fast transfer may
//	  already be cleaned up). A non-2xx status or a Content-Type that isn't
//	  a text/event-stream prefix match is a *terminal* failure (permanently
//	  CLOSED, no reconnect, matching the real spec's "fail the connection"
//	  step); a network-level failure before any status ever arrives
//	  (pending->done set while status is still unavailable) is *retryable*
//	  (schedules a reconnect); a good status/content-type transitions to
//	  OPEN. OPEN
//	  always flushes available bytes via __kml_eventsource_process_available
//	  and, if the transfer has ended, schedules a reconnect (Stage 3: this
//	  used to unconditionally go straight to permanent CLOSED — the
//	  documented gap Stage 3 closes).
func (e *Emitter) ensureEventSourceRuntime() {
	if e.usedEventSourceRuntime {
		return
	}
	e.usedEventSourceRuntime = true
	// ensureHTTPRuntime pulls in the event loop (__kml_event_loop_run),
	// the fiber runtime, and — critically — ensureFetchAsync, which is
	// what already declares every curl_easy_*/curl_multi_* primitive and
	// the @__kml_curl_inited/@__kml_curl_multi globals this file's own IR
	// reuses below without redeclaring. Called unconditionally from
	// ensureHTTPRuntime itself too (not just from here) — see that
	// function's own doc comment on why every symbol
	// __kml_event_loop_run's IR references must always be defined,
	// whether or not the program actually constructs an EventSource.
	e.ensureHTTPRuntime()
	e.ensureMalloc()
	e.ensureRealloc()
	e.ensureMemcpy()
	// The SSE record parser reuses __kml_split (line splitting) and
	// __kml_split_first (field-name/value splitting on the first ':')
	// wholesale rather than hand-rolling character scanning — both already
	// exist for exactly this shape of problem (HTTP header parsing,
	// ensureHTTPParseHeaders, already splits "Name: Value" lines the same
	// way).
	e.ensureStringSplit()
	e.ensureSplitFirst()
	e.ensureStrlen()
	e.ensureStrcmp()
	// Stage 3: strncmp for the Content-Type prefix check, atoll for
	// parsing a retry: field's value, curl_slist_append (via
	// ensureCurlSlist, the same helper fetch(url, init)'s own custom
	// headers already use) for building the Last-Event-ID replay header,
	// and clock_gettime for reconnect-deadline math.
	e.ensureStrncmp()
	e.ensureAtoll()
	e.ensureCurlSlist()
	e.ensureClockGettime()
	// addEventListener/removeEventListener/dispatch-to-named-type reuse
	// EventEmitter's own listener-list C helpers
	// (__kml_ee_list_create/_push/_remove) and the Map<string,ptr>
	// primitives they're already built on, directly — see TDD-00038's own
	// Design section on why this is the right thing to reuse rather than
	// inventing new listener storage.
	e.ensureEventEmitterRuntime()
	e.ensureMapStrHelpers()

	e.emitGlobal("@__kml_es_data = internal global ptr null, align 8")
	e.emitGlobal("@__kml_es_len = internal global i64 0, align 8")
	e.emitGlobal("@__kml_es_cap = internal global i64 0, align 8")
	e.emitGlobal("@__kml_es_active = internal global i64 0, align 8")

	// Fixed string constants the record parser/scan need — interned once
	// here (Go-level) and embedded by name into the raw IR below, the same
	// pattern ensureFetchAsync's own __kml_pending_finish uses for its
	// interned "Error" name constant.
	nlnl := e.internString("\n\n")
	crnlcrnl := e.internString("\r\n\r\n")
	crcr := e.internString("\r\r")
	nl := e.internString("\n")
	colon := e.internString(":")
	fData := e.internString("data")
	fEvent := e.internString("event")
	fID := e.internString("id")
	fRetry := e.internString("retry")
	defaultType := e.internString("message")
	empty := e.internString("")
	openType := e.internString("open")
	errorType := e.internString("error")
	lastEventIdPrefix := e.internString("Last-Event-ID: ")
	eventStreamCT := e.internString("text/event-stream")
	eventStreamCTLen := len("text/event-stream")
	ctPrefix := e.internString("Content-Type:")

	// __kml_eventsource_connect (Stage 3): the shared "build one libcurl
	// transfer" body, factored out of what used to be
	// __kml_eventsource_open's own inline sequence so __kml_eventsource_scan's
	// reconnect step (below) can call it too. headers is a nullable
	// curl_slist* (null for the original connect, a one-entry
	// "Last-Event-ID: ..." list for a reconnect that has one to replay).
	// __kml_eventsource_header_cb: a dedicated CURLOPT_HEADERFUNCTION
	// callback (Stage 3), separate from @__kml_curl_write_cb's body
	// handling — libcurl calls this once per response header *line*,
	// strictly before it ever calls the body write callback, regardless of
	// how quickly the transfer as a whole completes afterward. This is
	// what makes Content-Type observation UAF-safe even for a tiny/fast
	// response that finishes inside a single curl_multi_perform call: an
	// earlier design tried to read Content-Type via curl_easy_getinfo from
	// __kml_eventsource_scan instead, which raced against
	// __kml_curl_drain_messages's own curl_easy_cleanup for any response
	// small enough to complete before the scan ever got to look — and the
	// lenient "couldn't check, assume OK" fallback that raced into meant a
	// permanently-misconfigured endpoint (wrong Content-Type, inherently
	// always a small/fast response) got silently retried forever instead
	// of ever reaching the terminal-failure path, an infinite-retry bug
	// caught by TestE2EEventSourceWrongContentTypeEndsClosedNoRetry hanging
	// during development. userdata is the KML-level es_entry pointer
	// (CURLOPT_HEADERDATA, set below) — matches on a literal
	// "Content-Type:" prefix (case-sensitive — a documented narrowing,
	// real servers overwhelmingly send this exact casing) and, on a match,
	// copies the trimmed value into a freshly malloc'd, null-terminated
	// buffer stored into the entry's own contentType field (index 8) —
	// overwriting any earlier line's copy is intentional, so a redirect
	// hop's final Content-Type wins over an intermediate one's.
	e.emitGlobal(fmt.Sprintf(`
define i64 @__kml_eventsource_header_cb(ptr %%buffer, i64 %%size, i64 %%nitems, ptr %%ent) {
entry:
  %%total = mul i64 %%size, %%nitems
  %%longenough = icmp sge i64 %%total, 13
  br i1 %%longenough, label %%checkprefix, label %%skip

checkprefix:
  %%cmp = call i32 @strncmp(ptr %%buffer, ptr %[1]s, i64 13)
  %%matches = icmp eq i32 %%cmp, 0
  br i1 %%matches, label %%findvalstart, label %%skip

findvalstart:
  %%p13 = getelementptr i8, ptr %%buffer, i64 13
  %%c13 = load i8, ptr %%p13, align 1
  %%isspace13 = icmp eq i8 %%c13, 32
  br i1 %%isspace13, label %%skipspace, label %%novalspace

skipspace:
  %%p14 = getelementptr i8, ptr %%buffer, i64 14
  br label %%havevalstart

novalspace:
  br label %%havevalstart

havevalstart:
  %%valstart = phi ptr [ %%p14, %%skipspace ], [ %%p13, %%novalspace ]
  %%valstart_int = ptrtoint ptr %%valstart to i64
  %%buffer_int = ptrtoint ptr %%buffer to i64
  %%consumed = sub i64 %%valstart_int, %%buffer_int
  %%rawvallen = sub i64 %%total, %%consumed
  %%hasval = icmp sgt i64 %%rawvallen, 0
  br i1 %%hasval, label %%copyval, label %%skip

copyval:
  %%rawvallen1 = add i64 %%rawvallen, 1
  %%ctbuf = call ptr @malloc(i64 %%rawvallen1)
  call ptr @memcpy(ptr %%ctbuf, ptr %%valstart, i64 %%rawvallen)
  %%endp = getelementptr i8, ptr %%ctbuf, i64 %%rawvallen
  store i8 0, ptr %%endp, align 1
  %%lenslot = alloca i64, align 8
  store i64 %%rawvallen, ptr %%lenslot, align 8
  br label %%trimloop

trimloop:
  %%tlen = load i64, ptr %%lenslot, align 8
  %%cangotrim = icmp sgt i64 %%tlen, 0
  br i1 %%cangotrim, label %%trimcheck, label %%trimdone

trimcheck:
  %%tidx = sub i64 %%tlen, 1
  %%tptr = getelementptr i8, ptr %%ctbuf, i64 %%tidx
  %%tc = load i8, ptr %%tptr, align 1
  %%istcr = icmp eq i8 %%tc, 13
  %%istnl = icmp eq i8 %%tc, 10
  %%istrim = or i1 %%istcr, %%istnl
  br i1 %%istrim, label %%dotrim, label %%trimdone

dotrim:
  store i8 0, ptr %%tptr, align 1
  store i64 %%tidx, ptr %%lenslot, align 8
  br label %%trimloop

trimdone:
  %%ct_p = getelementptr { ptr, ptr, i64, i64, ptr, ptr, i64, i64, ptr }, ptr %%ent, i32 0, i32 8
  store ptr %%ctbuf, ptr %%ct_p, align 8
  br label %%skip

skip:
  ret i64 %%total
}`, ctPrefix))

	e.emitGlobal(`
define ptr @__kml_eventsource_connect(ptr %url, ptr %headers, ptr %es_entry) {
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

  %buf = call ptr @malloc(i64 24)
  %buf_data_p = getelementptr { ptr, i64, i64 }, ptr %buf, i32 0, i32 0
  %buf_len_p = getelementptr { ptr, i64, i64 }, ptr %buf, i32 0, i32 1
  %buf_cap_p = getelementptr { ptr, i64, i64 }, ptr %buf, i32 0, i32 2
  store ptr null, ptr %buf_data_p, align 8
  store i64 0, ptr %buf_len_p, align 8
  store i64 0, ptr %buf_cap_p, align 8

  %curl = call ptr @curl_easy_init()
  call i32 (ptr, i32, ...) @curl_easy_setopt(ptr %curl, i32 10002, ptr %url)
  call i32 (ptr, i32, ...) @curl_easy_setopt(ptr %curl, i32 20011, ptr @__kml_curl_write_cb)
  call i32 (ptr, i32, ...) @curl_easy_setopt(ptr %curl, i32 10001, ptr %buf)
  call i32 (ptr, i32, ...) @curl_easy_setopt(ptr %curl, i32 52, i64 1)
  call i32 (ptr, i32, ...) @curl_easy_setopt(ptr %curl, i32 99, i64 1)
  call i32 (ptr, i32, ...) @curl_easy_setopt(ptr %curl, i32 20079, ptr @__kml_eventsource_header_cb)
  call i32 (ptr, i32, ...) @curl_easy_setopt(ptr %curl, i32 10029, ptr %es_entry)

  %hasheaders = icmp ne ptr %headers, null
  br i1 %hasheaders, label %sethdr, label %skiphdr

sethdr:
  call i32 (ptr, i32, ...) @curl_easy_setopt(ptr %curl, i32 10023, ptr %headers)
  br label %skiphdr

skiphdr:
  %pending = call ptr @malloc(i64 40)
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

  call i32 (ptr, i32, ...) @curl_easy_setopt(ptr %curl, i32 10103, ptr %pending)
  call i32 @curl_multi_add_handle(ptr %multi2, ptr %curl)
  %runningp = alloca i32, align 4
  call i32 @curl_multi_perform(ptr %multi2, ptr %runningp)

  ret ptr %pending
}`)

	e.emitGlobal(`
define ptr @__kml_eventsource_open(ptr %url, ptr %instance) {
entry:
  ; The entry is allocated *before* connecting (unlike Stage 0-2, which
  ; connected first) so __kml_eventsource_connect has a valid entry pointer
  ; to hand libcurl as CURLOPT_HEADERDATA — the header callback needs
  ; somewhere to write a captured Content-Type into as soon as headers
  ; arrive, which must exist before the transfer starts, not after.
  %entryptr = call ptr @malloc(i64 72)
  %e_instance = getelementptr { ptr, ptr, i64, i64, ptr, ptr, i64, i64, ptr }, ptr %entryptr, i32 0, i32 1
  store ptr %instance, ptr %e_instance, align 8
  %e_consumed = getelementptr { ptr, ptr, i64, i64, ptr, ptr, i64, i64, ptr }, ptr %entryptr, i32 0, i32 2
  store i64 0, ptr %e_consumed, align 8
  %e_state = getelementptr { ptr, ptr, i64, i64, ptr, ptr, i64, i64, ptr }, ptr %entryptr, i32 0, i32 3
  store i64 0, ptr %e_state, align 8
  %e_listeners_map = call ptr @__kml_map_str_create()
  %e_listeners = getelementptr { ptr, ptr, i64, i64, ptr, ptr, i64, i64, ptr }, ptr %entryptr, i32 0, i32 4
  store ptr %e_listeners_map, ptr %e_listeners, align 8
  %e_url = getelementptr { ptr, ptr, i64, i64, ptr, ptr, i64, i64, ptr }, ptr %entryptr, i32 0, i32 5
  store ptr %url, ptr %e_url, align 8
  %e_retry = getelementptr { ptr, ptr, i64, i64, ptr, ptr, i64, i64, ptr }, ptr %entryptr, i32 0, i32 6
  store i64 3000, ptr %e_retry, align 8
  %e_reconnectat = getelementptr { ptr, ptr, i64, i64, ptr, ptr, i64, i64, ptr }, ptr %entryptr, i32 0, i32 7
  store i64 0, ptr %e_reconnectat, align 8
  %e_ct = getelementptr { ptr, ptr, i64, i64, ptr, ptr, i64, i64, ptr }, ptr %entryptr, i32 0, i32 8
  store ptr null, ptr %e_ct, align 8

  %pending = call ptr @__kml_eventsource_connect(ptr %url, ptr null, ptr %entryptr)
  %e_pending = getelementptr { ptr, ptr, i64, i64, ptr, ptr, i64, i64, ptr }, ptr %entryptr, i32 0, i32 0
  store ptr %pending, ptr %e_pending, align 8

  %eslen = load i64, ptr @__kml_es_len, align 8
  %escap = load i64, ptr @__kml_es_cap, align 8
  %esdata = load ptr, ptr @__kml_es_data, align 8
  %esneedp1 = add i64 %eslen, 1
  %esneedgrow = icmp sgt i64 %esneedp1, %escap
  br i1 %esneedgrow, label %esgrow, label %esappend

esgrow:
  %escap2 = mul i64 %escap, 2
  %esatleast8 = icmp sgt i64 %escap2, 8
  %esnewcap = select i1 %esatleast8, i64 %escap2, i64 8
  %esnewcapbytes = mul i64 %esnewcap, 8
  %esnewdata = call ptr @realloc(ptr %esdata, i64 %esnewcapbytes)
  store ptr %esnewdata, ptr @__kml_es_data, align 8
  store i64 %esnewcap, ptr @__kml_es_cap, align 8
  br label %esappend

esappend:
  %esdatanow = load ptr, ptr @__kml_es_data, align 8
  %esslot = getelementptr ptr, ptr %esdatanow, i64 %eslen
  store ptr %entryptr, ptr %esslot, align 8
  %esnewlen = add i64 %eslen, 1
  store i64 %esnewlen, ptr @__kml_es_len, align 8

  %curactive = load i64, ptr @__kml_es_active, align 8
  %newactive = add i64 %curactive, 1
  store i64 %newactive, ptr @__kml_es_active, align 8

  ret ptr %entryptr
}`)

	// __kml_eventsource_has_pending_work (Stage 3 bug fix): scans every
	// non-CLOSED, non-waiting-to-reconnect entry for data libcurl already
	// delivered but __kml_eventsource_scan hasn't dispatched yet. Needed
	// because __kml_eventsource_connect (both the original connect and a
	// reconnect's own call to it) kicks off its transfer with its own
	// synchronous curl_multi_perform, entirely outside the event loop's
	// normal select()-then-perform cycle — for a fast/local response, that
	// one call can fully deliver the whole body into pending->buf (via
	// __kml_curl_write_cb) or even mark pending->done before this function's
	// caller ever reaches __kml_event_loop_run's own select() call. Without
	// this check, the loop had no way to know real, actionable EventSource
	// work was already sitting there: with no JS timer due and no other
	// socket activity forthcoming (the arrived bytes were already drained
	// off the real socket, so there's no *new* readability event left to
	// wait for), select()'s NULL-timeout notimeoutpath would then block
	// forever — the buffered "replayed-..." message, and the onmessage
	// handler's own es.close() that would have unblocked everything, both
	// permanently stuck behind a select() call with nothing left to wake it.
	// Found and root-caused via TestE2EEventSourceAutoReconnectReplaysLastEventID
	// hanging intermittently (locally reproducible in isolation; also seen
	// timing out CI's whole `go test ./...` run) — see ADR-00133. Mirrors
	// __kml_wsclient_scan's own %forcezero precedent (runtime_http.go,
	// TDD-00039 Stage 3) for the exact same underlying hazard: "a
	// synchronous side channel resolved real work outside select()'s own
	// wait/wake cycle" is not unique to WebSocket clients.
	e.emitGlobal(`
define i1 @__kml_eventsource_has_pending_work() {
entry:
  %islot = alloca i64, align 8
  store i64 0, ptr %islot, align 8
  br label %loop

loop:
  %i = load i64, ptr %islot, align 8
  %len = load i64, ptr @__kml_es_len, align 8
  %inb = icmp slt i64 %i, %len
  br i1 %inb, label %body, label %notfound

body:
  %data = load ptr, ptr @__kml_es_data, align 8
  %slot = getelementptr ptr, ptr %data, i64 %i
  %ent = load ptr, ptr %slot, align 8
  %state_p = getelementptr { ptr, ptr, i64, i64, ptr, ptr, i64, i64, ptr }, ptr %ent, i32 0, i32 3
  %state = load i64, ptr %state_p, align 8
  %isclosed = icmp eq i64 %state, 2
  br i1 %isclosed, label %next, label %checkwaiting

checkwaiting:
  %reconnectat_p = getelementptr { ptr, ptr, i64, i64, ptr, ptr, i64, i64, ptr }, ptr %ent, i32 0, i32 7
  %reconnectat = load i64, ptr %reconnectat_p, align 8
  %iswaiting = icmp ne i64 %reconnectat, 0
  br i1 %iswaiting, label %next, label %checkbuf

checkbuf:
  %pending_p = getelementptr { ptr, ptr, i64, i64, ptr, ptr, i64, i64, ptr }, ptr %ent, i32 0, i32 0
  %pending = load ptr, ptr %pending_p, align 8
  %buf_p = getelementptr { ptr, ptr, i64, i64, i64 }, ptr %pending, i32 0, i32 1
  %buf = load ptr, ptr %buf_p, align 8
  %buflen_p = getelementptr { ptr, i64, i64 }, ptr %buf, i32 0, i32 1
  %buflen = load i64, ptr %buflen_p, align 8
  %consumed_p = getelementptr { ptr, ptr, i64, i64, ptr, ptr, i64, i64, ptr }, ptr %ent, i32 0, i32 2
  %consumed = load i64, ptr %consumed_p, align 8
  %hasunconsumed = icmp sgt i64 %buflen, %consumed
  br i1 %hasunconsumed, label %found, label %checkdone

checkdone:
  %done_p = getelementptr { ptr, ptr, i64, i64, i64 }, ptr %pending, i32 0, i32 2
  %done = load i64, ptr %done_p, align 8
  %isdone = icmp ne i64 %done, 0
  br i1 %isdone, label %found, label %next

next:
  %inext = add i64 %i, 1
  store i64 %inext, ptr %islot, align 8
  br label %loop

found:
  ret i1 1

notfound:
  ret i1 0
}`)

	// __kml_eventsource_next_reconnect_ms (Stage 3 bug fix): returns the
	// soonest reconnectAtMs deadline among every active, waiting-to-reconnect
	// entry, or -1 if none is waiting. reconnectAtMs (Stage 3's own
	// auto-reconnect bookkeeping) is never entered into @__kml_timer_data —
	// it's pure runtime state, not a JS-visible setTimeout/setInterval — so
	// without this, __kml_event_loop_run's own select() timeout computation
	// (built entirely from @__kml_timer_data plus __kml_eventsource_has_
	// pending_work's own "already-arrived-data" check above) has no way to
	// know a reconnect is due soon, or at all, whenever nothing else in the
	// program happens to bound the wait. A reconnecting EventSource in a
	// program with no other timer, listener, or fetch in flight hung
	// deterministically on this — found immediately after fixing the
	// intermittent has_pending_work race above, by testing the exact same
	// scenario with its incidental setTimeout(...) removed. See ADR-00133.
	// Only computes the deadline; __kml_eventsource_scan's own dowait/
	// doreconnect (checked separately, right after select() returns) is
	// still what actually fires the reconnect once due.
	e.emitGlobal(`
define i64 @__kml_eventsource_next_reconnect_ms() {
entry:
  %islot = alloca i64, align 8
  %best = alloca i64, align 8
  store i64 0, ptr %islot, align 8
  store i64 -1, ptr %best, align 8
  br label %loop

loop:
  %i = load i64, ptr %islot, align 8
  %len = load i64, ptr @__kml_es_len, align 8
  %inb = icmp slt i64 %i, %len
  br i1 %inb, label %body, label %exit

body:
  %data = load ptr, ptr @__kml_es_data, align 8
  %slot = getelementptr ptr, ptr %data, i64 %i
  %ent = load ptr, ptr %slot, align 8
  %state_p = getelementptr { ptr, ptr, i64, i64, ptr, ptr, i64, i64, ptr }, ptr %ent, i32 0, i32 3
  %state = load i64, ptr %state_p, align 8
  %isclosed = icmp eq i64 %state, 2
  br i1 %isclosed, label %next, label %checkwaiting

checkwaiting:
  %reconnectat_p = getelementptr { ptr, ptr, i64, i64, ptr, ptr, i64, i64, ptr }, ptr %ent, i32 0, i32 7
  %reconnectat = load i64, ptr %reconnectat_p, align 8
  %iswaiting = icmp ne i64 %reconnectat, 0
  br i1 %iswaiting, label %checkbest, label %next

checkbest:
  %curbest = load i64, ptr %best, align 8
  %noneyet = icmp slt i64 %curbest, 0
  br i1 %noneyet, label %takebest, label %compare

compare:
  %better = icmp slt i64 %reconnectat, %curbest
  br i1 %better, label %takebest, label %next

takebest:
  store i64 %reconnectat, ptr %best, align 8
  br label %next

next:
  %inext = add i64 %i, 1
  store i64 %inext, ptr %islot, align 8
  br label %loop

exit:
  %result = load i64, ptr %best, align 8
  ret i64 %result
}`)

	e.emitGlobal(`
define void @__kml_eventsource_close(ptr %es_entry) {
entry:
  %state_p = getelementptr { ptr, ptr, i64, i64, ptr, ptr, i64, i64, ptr }, ptr %es_entry, i32 0, i32 3
  %state = load i64, ptr %state_p, align 8
  %alreadyclosed = icmp eq i64 %state, 2
  br i1 %alreadyclosed, label %done, label %doclose

doclose:
  %reconnectat_p = getelementptr { ptr, ptr, i64, i64, ptr, ptr, i64, i64, ptr }, ptr %es_entry, i32 0, i32 7
  %reconnectat = load i64, ptr %reconnectat_p, align 8
  %iswaiting = icmp ne i64 %reconnectat, 0
  br i1 %iswaiting, label %closewaiting, label %closelive

closelive:
  %pending_p = getelementptr { ptr, ptr, i64, i64, ptr, ptr, i64, i64, ptr }, ptr %es_entry, i32 0, i32 0
  %pending = load ptr, ptr %pending_p, align 8
  %easy_p = getelementptr { ptr, ptr, i64, i64, i64 }, ptr %pending, i32 0, i32 0
  %easy = load ptr, ptr %easy_p, align 8
  %multi = load ptr, ptr @__kml_curl_multi, align 8
  call i32 @curl_multi_remove_handle(ptr %multi, ptr %easy)
  call void @curl_easy_cleanup(ptr %easy)
  br label %closetail

closewaiting:
  br label %closetail

closetail:
  store i64 2, ptr %state_p, align 8
  %curactive2 = load i64, ptr @__kml_es_active, align 8
  %newactive2 = sub i64 %curactive2, 1
  store i64 %newactive2, ptr @__kml_es_active, align 8
  br label %done

done:
  ret void
}`)

	e.emitGlobal(fmt.Sprintf(`
define i64 @__kml_eventsource_now_ms() {
entry:
  %%ts = alloca { i64, i64 }, align 8
  %%r = call i32 @clock_gettime(i32 %s, ptr %%ts)
  %%sec_p = getelementptr { i64, i64 }, ptr %%ts, i32 0, i32 0
  %%nsec_p = getelementptr { i64, i64 }, ptr %%ts, i32 0, i32 1
  %%sec = load i64, ptr %%sec_p, align 8
  %%nsec = load i64, ptr %%nsec_p, align 8
  %%sec_ms = mul i64 %%sec, 1000
  %%nsec_ms = sdiv i64 %%nsec, 1000000
  %%total = add i64 %%sec_ms, %%nsec_ms
  ret i64 %%total
}`, monotonicClockID()))

	// __kml_eventsource_scan: see this file's own top-of-file doc comment
	// for the full per-entry dispatch this implements (waiting-to-reconnect
	// / CONNECTING-with-status-check / OPEN, converging on a shared
	// doReconnectSchedule tail for every retryable drop).
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_eventsource_scan() {
entry:
  %%islot = alloca i64, align 8
  store i64 0, ptr %%islot, align 8
  br label %%loop

loop:
  %%i = load i64, ptr %%islot, align 8
  %%len = load i64, ptr @__kml_es_len, align 8
  %%inb = icmp slt i64 %%i, %%len
  br i1 %%inb, label %%body, label %%exit

body:
  %%data = load ptr, ptr @__kml_es_data, align 8
  %%slot = getelementptr ptr, ptr %%data, i64 %%i
  %%ent = load ptr, ptr %%slot, align 8
  %%state_p = getelementptr { ptr, ptr, i64, i64, ptr, ptr, i64, i64, ptr }, ptr %%ent, i32 0, i32 3
  %%state = load i64, ptr %%state_p, align 8
  %%isclosed = icmp eq i64 %%state, 2
  br i1 %%isclosed, label %%next, label %%checkwait

checkwait:
  %%reconnectat_p = getelementptr { ptr, ptr, i64, i64, ptr, ptr, i64, i64, ptr }, ptr %%ent, i32 0, i32 7
  %%reconnectat = load i64, ptr %%reconnectat_p, align 8
  %%iswaiting = icmp ne i64 %%reconnectat, 0
  br i1 %%iswaiting, label %%dowait, label %%checkstatedispatch

dowait:
  %%nowms1 = call i64 @__kml_eventsource_now_ms()
  %%isdue = icmp sge i64 %%nowms1, %%reconnectat
  br i1 %%isdue, label %%doreconnect, label %%next

doreconnect:
  %%inst_rc_p = getelementptr { ptr, ptr, i64, i64, ptr, ptr, i64, i64, ptr }, ptr %%ent, i32 0, i32 1
  %%inst_rc = load ptr, ptr %%inst_rc_p, align 8
  %%lastid_p = getelementptr { ptr, ptr, ptr, i64, ptr, ptr, ptr }, ptr %%inst_rc, i32 0, i32 1
  %%lastid = load ptr, ptr %%lastid_p, align 8
  %%lastid_first = load i8, ptr %%lastid, align 1
  %%haslastid = icmp ne i8 %%lastid_first, 0
  br i1 %%haslastid, label %%buildhdr, label %%nohdr

buildhdr:
  %%hdrline = call ptr @__kml_es_concat(ptr %[4]s, ptr %%lastid)
  %%hdrs1 = call ptr @curl_slist_append(ptr null, ptr %%hdrline)
  br label %%mergehdr

nohdr:
  br label %%mergehdr

mergehdr:
  %%hdrsfinal = phi ptr [ %%hdrs1, %%buildhdr ], [ null, %%nohdr ]
  %%url_p = getelementptr { ptr, ptr, i64, i64, ptr, ptr, i64, i64, ptr }, ptr %%ent, i32 0, i32 5
  %%url = load ptr, ptr %%url_p, align 8
  %%ct_p_rc = getelementptr { ptr, ptr, i64, i64, ptr, ptr, i64, i64, ptr }, ptr %%ent, i32 0, i32 8
  store ptr null, ptr %%ct_p_rc, align 8
  %%newpending = call ptr @__kml_eventsource_connect(ptr %%url, ptr %%hdrsfinal, ptr %%ent)
  %%pending_p_rc = getelementptr { ptr, ptr, i64, i64, ptr, ptr, i64, i64, ptr }, ptr %%ent, i32 0, i32 0
  store ptr %%newpending, ptr %%pending_p_rc, align 8
  %%consumed_p_rc = getelementptr { ptr, ptr, i64, i64, ptr, ptr, i64, i64, ptr }, ptr %%ent, i32 0, i32 2
  store i64 0, ptr %%consumed_p_rc, align 8
  store i64 0, ptr %%reconnectat_p, align 8
  br label %%next

checkstatedispatch:
  %%isconnecting = icmp eq i64 %%state, 0
  br i1 %%isconnecting, label %%checkstatus, label %%checkopenstate

checkstatus:
  %%pending_cs_p = getelementptr { ptr, ptr, i64, i64, ptr, ptr, i64, i64, ptr }, ptr %%ent, i32 0, i32 0
  %%pending_cs = load ptr, ptr %%pending_cs_p, align 8
  %%donep_cs = getelementptr { ptr, ptr, i64, i64, i64 }, ptr %%pending_cs, i32 0, i32 2
  %%done_cs = load i64, ptr %%donep_cs, align 8
  %%isdone_cs = icmp ne i64 %%done_cs, 0
  br i1 %%isdone_cs, label %%statusfromdone, label %%statuslive

; The transfer may already have completed (and been curl_multi_remove_handle
; + curl_easy_cleanup'd by __kml_curl_drain_messages) by the time this scan
; gets to it — a small/fast response can finish inside a single
; curl_multi_perform cycle. curl_easy_getinfo on an already-cleaned-up easy
; handle is a use-after-free, not just stale data, so status/content-type
; must never be read live once done is set. __kml_curl_drain_messages
; already captured the HTTP status into pending->status *before* cleanup
; (runtime_fetch.go) — reuse that instead of re-querying the freed handle.
statuslive:
  %%easy_p_live = getelementptr { ptr, ptr, i64, i64, i64 }, ptr %%pending_cs, i32 0, i32 0
  %%easy_live = load ptr, ptr %%easy_p_live, align 8
  %%statusslot = alloca i64, align 8
  store i64 0, ptr %%statusslot, align 8
  call i32 (ptr, i32, ...) @curl_easy_getinfo(ptr %%easy_live, i32 2097154, ptr %%statusslot)
  %%statusval_live = load i64, ptr %%statusslot, align 8
  br label %%mergestatus

statusfromdone:
  %%statusp_done = getelementptr { ptr, ptr, i64, i64, i64 }, ptr %%pending_cs, i32 0, i32 3
  %%statusval_done = load i64, ptr %%statusp_done, align 8
  br label %%mergestatus

mergestatus:
  %%statusval = phi i64 [ %%statusval_live, %%statuslive ], [ %%statusval_done, %%statusfromdone ]
  %%statusavail = icmp ne i64 %%statusval, 0
  br i1 %%statusavail, label %%evalstatus, label %%checkdoneconnecting

evalstatus:
  %%isge200 = icmp sge i64 %%statusval, 200
  %%islt300 = icmp slt i64 %%statusval, 300
  %%is2xx = and i1 %%isge200, %%islt300
  br i1 %%is2xx, label %%checkct, label %%failterminal

; Content-Type is captured into the entry's own contentType field (index 8)
; by @__kml_eventsource_header_cb as soon as the header line arrives —
; strictly before the body (and therefore strictly before the transfer can
; possibly finish and get curl_easy_cleanup'd), regardless of how small/fast
; the response is. Reading it here is always safe, unlike status's
; live-vs-done split above: there's no easy-handle liveness to worry about
; since this is our own heap allocation, not libcurl's.
checkct:
  %%ct_p_check = getelementptr { ptr, ptr, i64, i64, ptr, ptr, i64, i64, ptr }, ptr %%ent, i32 0, i32 8
  %%ctval = load ptr, ptr %%ct_p_check, align 8
  %%ctnull = icmp eq ptr %%ctval, null
  br i1 %%ctnull, label %%doopen2, label %%checkctprefix

checkctprefix:
  %%ctlen = call i64 @strlen(ptr %%ctval)
  %%ctlongenough = icmp sge i64 %%ctlen, %[6]d
  br i1 %%ctlongenough, label %%doctcmp, label %%failterminal

doctcmp:
  %%ctcmp = call i32 @strncmp(ptr %%ctval, ptr %[5]s, i64 %[6]d)
  %%ctok = icmp eq i32 %%ctcmp, 0
  br i1 %%ctok, label %%doopen2, label %%failterminal

failterminal:
  br i1 %%isdone_cs, label %%ft_skip_teardown, label %%ft_teardown

ft_teardown:
  %%easy_p_ft = getelementptr { ptr, ptr, i64, i64, i64 }, ptr %%pending_cs, i32 0, i32 0
  %%easy_ft = load ptr, ptr %%easy_p_ft, align 8
  %%multi_ft = load ptr, ptr @__kml_curl_multi, align 8
  call i32 @curl_multi_remove_handle(ptr %%multi_ft, ptr %%easy_ft)
  call void @curl_easy_cleanup(ptr %%easy_ft)
  br label %%ft_skip_teardown

ft_skip_teardown:
  store i64 2, ptr %%state_p, align 8
  %%curactive_ft = load i64, ptr @__kml_es_active, align 8
  %%newactive_ft = sub i64 %%curactive_ft, 1
  store i64 %%newactive_ft, ptr @__kml_es_active, align 8
  %%inst_ft_p = getelementptr { ptr, ptr, i64, i64, ptr, ptr, i64, i64, ptr }, ptr %%ent, i32 0, i32 1
  %%inst_ft = load ptr, ptr %%inst_ft_p, align 8
  %%rs_ft_p = getelementptr { ptr, ptr, ptr, i64, ptr, ptr, ptr }, ptr %%inst_ft, i32 0, i32 3
  store i64 2, ptr %%rs_ft_p, align 8

  %%everr_ft = call ptr @malloc(i64 24)
  %%everr_ft_data = getelementptr { ptr, ptr, ptr }, ptr %%everr_ft, i32 0, i32 0
  store ptr %[1]s, ptr %%everr_ft_data, align 8
  %%everr_ft_type = getelementptr { ptr, ptr, ptr }, ptr %%everr_ft, i32 0, i32 1
  store ptr %[2]s, ptr %%everr_ft_type, align 8
  %%everr_ft_lastid = getelementptr { ptr, ptr, ptr }, ptr %%everr_ft, i32 0, i32 2
  store ptr %[1]s, ptr %%everr_ft_lastid, align 8

  %%onerror_ft_p = getelementptr { ptr, ptr, ptr, i64, ptr, ptr, ptr }, ptr %%inst_ft, i32 0, i32 6
  %%onerror_ft_cb = load ptr, ptr %%onerror_ft_p, align 8
  %%hasonerror_ft = icmp ne ptr %%onerror_ft_cb, null
  br i1 %%hasonerror_ft, label %%callonerror_ft, label %%afteronerror_ft

callonerror_ft:
  %%oe_ft_fp_p = getelementptr {ptr, ptr}, ptr %%onerror_ft_cb, i32 0, i32 0
  %%oe_ft_fp = load ptr, ptr %%oe_ft_fp_p, align 8
  %%oe_ft_ep_p = getelementptr {ptr, ptr}, ptr %%onerror_ft_cb, i32 0, i32 1
  %%oe_ft_ep = load ptr, ptr %%oe_ft_ep_p, align 8
  call void (ptr, ptr) %%oe_ft_fp(ptr %%oe_ft_ep, ptr %%everr_ft)
  br label %%afteronerror_ft

afteronerror_ft:
  call void @__kml_eventsource_dispatch_event(ptr %%ent, ptr %[2]s, ptr %%everr_ft)
  br label %%next

checkdoneconnecting:
  %%done_cdc_p = getelementptr { ptr, ptr, i64, i64, i64 }, ptr %%pending_cs, i32 0, i32 2
  %%done_cdc = load i64, ptr %%done_cdc_p, align 8
  %%isdone_cdc = icmp ne i64 %%done_cdc, 0
  br i1 %%isdone_cdc, label %%doreconnectschedule, label %%next

checkopenstate:
  call void @__kml_eventsource_process_available(ptr %%ent)
  %%pending_os_p = getelementptr { ptr, ptr, i64, i64, ptr, ptr, i64, i64, ptr }, ptr %%ent, i32 0, i32 0
  %%pending_os = load ptr, ptr %%pending_os_p, align 8
  %%done_os_p = getelementptr { ptr, ptr, i64, i64, i64 }, ptr %%pending_os, i32 0, i32 2
  %%done_os = load i64, ptr %%done_os_p, align 8
  %%isdone_os = icmp ne i64 %%done_os, 0
  br i1 %%isdone_os, label %%doreconnectschedule, label %%next

doopen2:
  store i64 1, ptr %%state_p, align 8
  %%inst_do_p = getelementptr { ptr, ptr, i64, i64, ptr, ptr, i64, i64, ptr }, ptr %%ent, i32 0, i32 1
  %%inst_do = load ptr, ptr %%inst_do_p, align 8
  %%rs_do_p = getelementptr { ptr, ptr, ptr, i64, ptr, ptr, ptr }, ptr %%inst_do, i32 0, i32 3
  store i64 1, ptr %%rs_do_p, align 8

  %%evopen_do = call ptr @malloc(i64 24)
  %%evopen_do_data = getelementptr { ptr, ptr, ptr }, ptr %%evopen_do, i32 0, i32 0
  store ptr %[1]s, ptr %%evopen_do_data, align 8
  %%evopen_do_type = getelementptr { ptr, ptr, ptr }, ptr %%evopen_do, i32 0, i32 1
  store ptr %[3]s, ptr %%evopen_do_type, align 8
  %%evopen_do_lastid = getelementptr { ptr, ptr, ptr }, ptr %%evopen_do, i32 0, i32 2
  store ptr %[1]s, ptr %%evopen_do_lastid, align 8

  %%onopen_do_p = getelementptr { ptr, ptr, ptr, i64, ptr, ptr, ptr }, ptr %%inst_do, i32 0, i32 5
  %%onopen_do_cb = load ptr, ptr %%onopen_do_p, align 8
  %%hasonopen_do = icmp ne ptr %%onopen_do_cb, null
  br i1 %%hasonopen_do, label %%callonopen_do, label %%afteronopen_do

callonopen_do:
  %%oo_do_fp_p = getelementptr {ptr, ptr}, ptr %%onopen_do_cb, i32 0, i32 0
  %%oo_do_fp = load ptr, ptr %%oo_do_fp_p, align 8
  %%oo_do_ep_p = getelementptr {ptr, ptr}, ptr %%onopen_do_cb, i32 0, i32 1
  %%oo_do_ep = load ptr, ptr %%oo_do_ep_p, align 8
  call void (ptr, ptr) %%oo_do_fp(ptr %%oo_do_ep, ptr %%evopen_do)
  br label %%afteronopen_do

afteronopen_do:
  call void @__kml_eventsource_dispatch_event(ptr %%ent, ptr %[3]s, ptr %%evopen_do)
  call void @__kml_eventsource_process_available(ptr %%ent)
  br label %%next

doreconnectschedule:
  %%inst_rs_p = getelementptr { ptr, ptr, i64, i64, ptr, ptr, i64, i64, ptr }, ptr %%ent, i32 0, i32 1
  %%inst_rs = load ptr, ptr %%inst_rs_p, align 8
  %%rs_rs_p = getelementptr { ptr, ptr, ptr, i64, ptr, ptr, ptr }, ptr %%inst_rs, i32 0, i32 3
  store i64 0, ptr %%rs_rs_p, align 8

  %%everr_rs = call ptr @malloc(i64 24)
  %%everr_rs_data = getelementptr { ptr, ptr, ptr }, ptr %%everr_rs, i32 0, i32 0
  store ptr %[1]s, ptr %%everr_rs_data, align 8
  %%everr_rs_type = getelementptr { ptr, ptr, ptr }, ptr %%everr_rs, i32 0, i32 1
  store ptr %[2]s, ptr %%everr_rs_type, align 8
  %%everr_rs_lastid = getelementptr { ptr, ptr, ptr }, ptr %%everr_rs, i32 0, i32 2
  store ptr %[1]s, ptr %%everr_rs_lastid, align 8

  %%onerror_rs_p = getelementptr { ptr, ptr, ptr, i64, ptr, ptr, ptr }, ptr %%inst_rs, i32 0, i32 6
  %%onerror_rs_cb = load ptr, ptr %%onerror_rs_p, align 8
  %%hasonerror_rs = icmp ne ptr %%onerror_rs_cb, null
  br i1 %%hasonerror_rs, label %%callonerror_rs, label %%afteronerror_rs

callonerror_rs:
  %%oe_rs_fp_p = getelementptr {ptr, ptr}, ptr %%onerror_rs_cb, i32 0, i32 0
  %%oe_rs_fp = load ptr, ptr %%oe_rs_fp_p, align 8
  %%oe_rs_ep_p = getelementptr {ptr, ptr}, ptr %%onerror_rs_cb, i32 0, i32 1
  %%oe_rs_ep = load ptr, ptr %%oe_rs_ep_p, align 8
  call void (ptr, ptr) %%oe_rs_fp(ptr %%oe_rs_ep, ptr %%everr_rs)
  br label %%afteronerror_rs

afteronerror_rs:
  call void @__kml_eventsource_dispatch_event(ptr %%ent, ptr %[2]s, ptr %%everr_rs)
  call void @__kml_eventsource_process_available(ptr %%ent)

  %%retryms_p = getelementptr { ptr, ptr, i64, i64, ptr, ptr, i64, i64, ptr }, ptr %%ent, i32 0, i32 6
  %%retryms = load i64, ptr %%retryms_p, align 8
  %%nowms2 = call i64 @__kml_eventsource_now_ms()
  %%deadline = add i64 %%nowms2, %%retryms
  %%reconnectat_p_rs = getelementptr { ptr, ptr, i64, i64, ptr, ptr, i64, i64, ptr }, ptr %%ent, i32 0, i32 7
  store i64 %%deadline, ptr %%reconnectat_p_rs, align 8
  store i64 0, ptr %%state_p, align 8
  br label %%next

next:
  %%inext = add i64 %%i, 1
  store i64 %%inext, ptr %%islot, align 8
  br label %%loop

exit:
  ret void
}`, empty, errorType, openType, lastEventIdPrefix, eventStreamCT, eventStreamCTLen))

	// __kml_es_concat: a plain two-string concat, equivalent to
	// emitStringConcat's own logic (emit_strings.go) but as a real,
	// standalone callable IR function — needed since the record-body
	// accumulation and Stage 3's Last-Event-ID header line both run from
	// hand-written runtime IR, not from KML-source-triggered codegen, so
	// emitStringConcat's own e.emitInstr/freshReg-based approach isn't
	// usable here.
	e.emitGlobal(`
define ptr @__kml_es_concat(ptr %a, ptr %b) {
entry:
  %na = call i64 @strlen(ptr %a)
  %nb = call i64 @strlen(ptr %b)
  %total = add i64 %na, %nb
  %total1 = add i64 %total, 1
  %buf = call ptr @malloc(i64 %total1)
  call ptr @memcpy(ptr %buf, ptr %a, i64 %na)
  %dst = getelementptr i8, ptr %buf, i64 %na
  %nb1 = add i64 %nb, 1
  call ptr @memcpy(ptr %dst, ptr %b, i64 %nb1)
  ret ptr %buf
}`)

	// __kml_es_normalize_record (Stage 3): converts every "\r\n" or lone
	// "\r" in [raw, raw+rawlen) into a single "\n", copying into a freshly
	// malloc'd, null-terminated buffer (always big enough at rawlen+1 bytes
	// since normalization only ever shrinks or preserves length, never
	// grows it). A two-pointer scan — output index never exceeds input
	// index — so writing into a separate buffer (never in place into raw
	// itself, which is a live view into the still-growing accumulator) is
	// safe and needs no bounds recomputation. See
	// __kml_eventsource_process_available below for why every line
	// terminator (not just the record-boundary one) needs this: the real
	// SSE stream-decoding algorithm treats \r, \n, and \r\n as
	// interchangeable line terminators throughout, not just at record
	// boundaries.
	e.emitGlobal(`
define ptr @__kml_es_normalize_record(ptr %raw, i64 %rawlen) {
entry:
  %rawlen1 = add i64 %rawlen, 1
  %out = call ptr @malloc(i64 %rawlen1)
  %islot = alloca i64, align 8
  store i64 0, ptr %islot, align 8
  %oslot = alloca i64, align 8
  store i64 0, ptr %oslot, align 8
  br label %loop

loop:
  %i = load i64, ptr %islot, align 8
  %atend = icmp sge i64 %i, %rawlen
  br i1 %atend, label %done, label %body

body:
  %cptr = getelementptr i8, ptr %raw, i64 %i
  %c = load i8, ptr %cptr, align 1
  %iscr = icmp eq i8 %c, 13
  br i1 %iscr, label %handlecr, label %notcr

notcr:
  %isnl = icmp eq i8 %c, 10
  br i1 %isnl, label %handlenl, label %handleother

handlenl:
  %o_nl = load i64, ptr %oslot, align 8
  %optr_nl = getelementptr i8, ptr %out, i64 %o_nl
  store i8 10, ptr %optr_nl, align 1
  %o_nl1 = add i64 %o_nl, 1
  store i64 %o_nl1, ptr %oslot, align 8
  %i_nl1 = add i64 %i, 1
  store i64 %i_nl1, ptr %islot, align 8
  br label %loop

handleother:
  %o_ot = load i64, ptr %oslot, align 8
  %optr_ot = getelementptr i8, ptr %out, i64 %o_ot
  store i8 %c, ptr %optr_ot, align 1
  %o_ot1 = add i64 %o_ot, 1
  store i64 %o_ot1, ptr %oslot, align 8
  %i_ot1 = add i64 %i, 1
  store i64 %i_ot1, ptr %islot, align 8
  br label %loop

handlecr:
  %o_cr = load i64, ptr %oslot, align 8
  %optr_cr = getelementptr i8, ptr %out, i64 %o_cr
  store i8 10, ptr %optr_cr, align 1
  %o_cr1 = add i64 %o_cr, 1
  store i64 %o_cr1, ptr %oslot, align 8
  %i_cr1 = add i64 %i, 1
  store i64 %i_cr1, ptr %islot, align 8
  %hasnext_cr = icmp slt i64 %i_cr1, %rawlen
  br i1 %hasnext_cr, label %checknext_cr, label %loop

checknext_cr:
  %nptr_cr = getelementptr i8, ptr %raw, i64 %i_cr1
  %n_cr = load i8, ptr %nptr_cr, align 1
  %isnextnl_cr = icmp eq i8 %n_cr, 10
  br i1 %isnextnl_cr, label %skipnl_cr, label %loop

skipnl_cr:
  %i_cr2 = add i64 %i_cr1, 1
  store i64 %i_cr2, ptr %islot, align 8
  br label %loop

done:
  %ofinal = load i64, ptr %oslot, align 8
  %termptr = getelementptr i8, ptr %out, i64 %ofinal
  store i8 0, ptr %termptr, align 1
  ret ptr %out
}`)

	// __kml_eventsource_process_available (Stage 3): the per-entry
	// record-boundary loop. Searches the unconsumed tail (from the entry's
	// own consumedOffset onward, so a previously-dispatched record's bytes
	// are never rescanned) for whichever of "\n\n"/"\r\n\r\n"/"\r\r" occurs
	// earliest — the three patterns can never spuriously overlap (no two
	// share a substring relationship), so "earliest wins" is unambiguous
	// and handles even a stream that varies its line-ending style
	// record-to-record. Reloads the buffer's data pointer/length fresh at
	// the top of every call (never holds one across a scan boundary) —
	// realloc-safe by construction, since nothing ever caches a raw
	// pointer into the accumulator across two separate calls. The
	// extracted record body is normalized (__kml_es_normalize_record)
	// before being handed to __kml_eventsource_dispatch_record, which
	// still splits on plain "\n" — see that helper's own doc comment for
	// why this is spec-correct, not an approximation.
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_eventsource_process_available(ptr %%ent) {
entry:
  %%pending_p = getelementptr { ptr, ptr, i64, i64, ptr, ptr, i64, i64, ptr }, ptr %%ent, i32 0, i32 0
  %%pending = load ptr, ptr %%pending_p, align 8
  %%buf_p = getelementptr { ptr, ptr, i64, i64, i64 }, ptr %%pending, i32 0, i32 1
  %%buf = load ptr, ptr %%buf_p, align 8
  %%bufdata_p = getelementptr { ptr, i64, i64 }, ptr %%buf, i32 0, i32 0
  %%bufdata = load ptr, ptr %%bufdata_p, align 8
  %%buflen_p = getelementptr { ptr, i64, i64 }, ptr %%buf, i32 0, i32 1
  %%buflen = load i64, ptr %%buflen_p, align 8
  br label %%looprecords

looprecords:
  %%consumed_p = getelementptr { ptr, ptr, i64, i64, ptr, ptr, i64, i64, ptr }, ptr %%ent, i32 0, i32 2
  %%consumed = load i64, ptr %%consumed_p, align 8
  %%remaining = sub i64 %%buflen, %%consumed
  %%hasremaining = icmp sgt i64 %%remaining, 0
  br i1 %%hasremaining, label %%search, label %%done

search:
  %%tailptr = getelementptr i8, ptr %%bufdata, i64 %%consumed
  %%f1 = call ptr @strstr(ptr %%tailptr, ptr %[1]s)
  %%f2 = call ptr @strstr(ptr %%tailptr, ptr %[2]s)
  %%f3 = call ptr @strstr(ptr %%tailptr, ptr %[3]s)
  %%has1 = icmp ne ptr %%f1, null
  %%has2 = icmp ne ptr %%f2, null
  %%has3 = icmp ne ptr %%f3, null
  %%a1 = ptrtoint ptr %%f1 to i64
  %%a2 = ptrtoint ptr %%f2 to i64
  %%a3 = ptrtoint ptr %%f3 to i64
  %%addr1 = select i1 %%has1, i64 %%a1, i64 -1
  %%addr2 = select i1 %%has2, i64 %%a2, i64 -1
  %%addr3 = select i1 %%has3, i64 %%a3, i64 -1
  %%lt12 = icmp ult i64 %%addr1, %%addr2
  %%min12 = select i1 %%lt12, i64 %%addr1, i64 %%addr2
  %%len12 = select i1 %%lt12, i64 2, i64 4
  %%lt123 = icmp ult i64 %%min12, %%addr3
  %%minall = select i1 %%lt123, i64 %%min12, i64 %%addr3
  %%lenall = select i1 %%lt123, i64 %%len12, i64 2
  %%wasfound = icmp ne i64 %%minall, -1
  br i1 %%wasfound, label %%haverecord, label %%done

haverecord:
  %%tailint = ptrtoint ptr %%tailptr to i64
  %%reclen = sub i64 %%minall, %%tailint
  %%normed = call ptr @__kml_es_normalize_record(ptr %%tailptr, i64 %%reclen)
  call void @__kml_eventsource_dispatch_record(ptr %%ent, ptr %%normed)
  %%bufdataint = ptrtoint ptr %%bufdata to i64
  %%foundoffset = sub i64 %%minall, %%bufdataint
  %%newconsumed = add i64 %%foundoffset, %%lenall
  store i64 %%newconsumed, ptr %%consumed_p, align 8
  br label %%looprecords

done:
  ret void
}`, nlnl, crnlcrnl, crcr))

	// __kml_eventsource_dispatch_record: parses one complete, already
	// line-ending-normalized SSE record (no trailing blank line — that
	// delimiter was already consumed by the caller) into data/event/id/
	// retry fields per the real spec's own field-processing algorithm,
	// then dispatches to `onmessage` if the resulting data buffer is
	// non-empty. `retry` (Stage 3) is parsed via atoll and stored into the
	// entry's own retryMs field, overriding the 3000ms default for any
	// future reconnect. `id`'s persistence across records (an id-less
	// record keeps the previous value) lives on the KML object's own
	// hidden EventSourceLastEventIdField, not on this function's own
	// locals.
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_eventsource_dispatch_record(ptr %%ent, ptr %%record) {
entry:
  %%lines = call {ptr, i64} @__kml_split(ptr %%record, ptr %[1]s)
  %%linesdata = extractvalue {ptr, i64} %%lines, 0
  %%numlines = extractvalue {ptr, i64} %%lines, 1

  %%databuf_slot = alloca ptr, align 8
  store ptr %[7]s, ptr %%databuf_slot, align 8
  %%eventtype_slot = alloca ptr, align 8
  store ptr null, ptr %%eventtype_slot, align 8
  %%lastid_slot = alloca ptr, align 8
  store ptr null, ptr %%lastid_slot, align 8
  %%li_slot = alloca i64, align 8
  store i64 0, ptr %%li_slot, align 8
  br label %%lineloop

lineloop:
  %%li = load i64, ptr %%li_slot, align 8
  %%lidone = icmp sge i64 %%li, %%numlines
  br i1 %%lidone, label %%linesdone, label %%linebody

linebody:
  %%lineslot = getelementptr ptr, ptr %%linesdata, i64 %%li
  %%line = load ptr, ptr %%lineslot, align 8
  %%firstchar = load i8, ptr %%line, align 1
  %%iscolon = icmp eq i8 %%firstchar, 58
  br i1 %%iscolon, label %%linenext, label %%parsefield

parsefield:
  %%sf = call {ptr, ptr} @__kml_split_first(ptr %%line, ptr %[2]s)
  %%fname = extractvalue {ptr, ptr} %%sf, 0
  %%fafter = extractvalue {ptr, ptr} %%sf, 1
  %%hascolon = icmp ne ptr %%fafter, null
  br i1 %%hascolon, label %%trimvalue, label %%novalue

novalue:
  br label %%gotvalue

trimvalue:
  %%vfirst = load i8, ptr %%fafter, align 1
  %%hasleadingspace = icmp eq i8 %%vfirst, 32
  br i1 %%hasleadingspace, label %%stripspace, label %%novstrip

stripspace:
  %%stripped = getelementptr i8, ptr %%fafter, i64 1
  br label %%gotvalue

novstrip:
  br label %%gotvalue

gotvalue:
  %%value = phi ptr [ %[7]s, %%novalue ], [ %%stripped, %%stripspace ], [ %%fafter, %%novstrip ]

  %%isdata = call i32 @strcmp(ptr %%fname, ptr %[3]s)
  %%isdata0 = icmp eq i32 %%isdata, 0
  br i1 %%isdata0, label %%dodata, label %%checkevent

dodata:
  %%curdata = load ptr, ptr %%databuf_slot, align 8
  %%step1 = call ptr @__kml_es_concat(ptr %%curdata, ptr %%value)
  %%step2 = call ptr @__kml_es_concat(ptr %%step1, ptr %[1]s)
  store ptr %%step2, ptr %%databuf_slot, align 8
  br label %%linenext

checkevent:
  %%isevent = call i32 @strcmp(ptr %%fname, ptr %[4]s)
  %%isevent0 = icmp eq i32 %%isevent, 0
  br i1 %%isevent0, label %%doevent, label %%checkid

doevent:
  store ptr %%value, ptr %%eventtype_slot, align 8
  br label %%linenext

checkid:
  %%isid = call i32 @strcmp(ptr %%fname, ptr %[5]s)
  %%isid0 = icmp eq i32 %%isid, 0
  br i1 %%isid0, label %%doid, label %%checkretry

doid:
  store ptr %%value, ptr %%lastid_slot, align 8
  br label %%linenext

checkretry:
  %%isretry = call i32 @strcmp(ptr %%fname, ptr %[8]s)
  %%isretry0 = icmp eq i32 %%isretry, 0
  br i1 %%isretry0, label %%doretry, label %%linenext

doretry:
  %%retryval = call i64 @atoll(ptr %%value)
  %%retryms_p = getelementptr { ptr, ptr, i64, i64, ptr, ptr, i64, i64, ptr }, ptr %%ent, i32 0, i32 6
  store i64 %%retryval, ptr %%retryms_p, align 8
  br label %%linenext

linenext:
  %%li1 = add i64 %%li, 1
  store i64 %%li1, ptr %%li_slot, align 8
  br label %%lineloop

linesdone:
  %%finaldata = load ptr, ptr %%databuf_slot, align 8
  %%datalen = call i64 @strlen(ptr %%finaldata)
  %%haslen = icmp sgt i64 %%datalen, 0
  br i1 %%haslen, label %%checklast, label %%skipstrip

checklast:
  %%lastidx = sub i64 %%datalen, 1
  %%lastptr = getelementptr i8, ptr %%finaldata, i64 %%lastidx
  %%lastchar = load i8, ptr %%lastptr, align 1
  %%islf = icmp eq i8 %%lastchar, 10
  br i1 %%islf, label %%dostrip, label %%skipstrip

dostrip:
  store i8 0, ptr %%lastptr, align 1
  br label %%skipstrip

skipstrip:
  %%finaldata2 = load ptr, ptr %%databuf_slot, align 8
  %%finallen = call i64 @strlen(ptr %%finaldata2)
  %%shoulddispatch = icmp sgt i64 %%finallen, 0
  br i1 %%shoulddispatch, label %%dodispatch, label %%noop

dodispatch:
  %%instance_p = getelementptr { ptr, ptr, i64, i64, ptr, ptr, i64, i64, ptr }, ptr %%ent, i32 0, i32 1
  %%instance = load ptr, ptr %%instance_p, align 8

  %%sawid = load ptr, ptr %%lastid_slot, align 8
  %%hasid = icmp ne ptr %%sawid, null
  br i1 %%hasid, label %%storeid, label %%readid

storeid:
  %%lid_p = getelementptr { ptr, ptr, ptr, i64, ptr, ptr, ptr }, ptr %%instance, i32 0, i32 1
  store ptr %%sawid, ptr %%lid_p, align 8
  br label %%readid

readid:
  %%lid_p2 = getelementptr { ptr, ptr, ptr, i64, ptr, ptr, ptr }, ptr %%instance, i32 0, i32 1
  %%curlastid = load ptr, ptr %%lid_p2, align 8

  %%evtype = load ptr, ptr %%eventtype_slot, align 8
  %%hasevtype = icmp ne ptr %%evtype, null
  br i1 %%hasevtype, label %%usetype, label %%defaulttype

usetype:
  br label %%havemsgtype

defaulttype:
  br label %%havemsgtype

havemsgtype:
  %%msgtype = phi ptr [ %%evtype, %%usetype ], [ %[6]s, %%defaulttype ]

  %%mev = call ptr @malloc(i64 24)
  %%mev_data = getelementptr { ptr, ptr, ptr }, ptr %%mev, i32 0, i32 0
  store ptr %%finaldata2, ptr %%mev_data, align 8
  %%mev_type = getelementptr { ptr, ptr, ptr }, ptr %%mev, i32 0, i32 1
  store ptr %%msgtype, ptr %%mev_type, align 8
  %%mev_lastid = getelementptr { ptr, ptr, ptr }, ptr %%mev, i32 0, i32 2
  store ptr %%curlastid, ptr %%mev_lastid, align 8

  ; The dedicated onmessage field only fires for an unnamed record
  ; (resolved type "message", whether that's the default or an explicit
  ; "event: message" line) — matching real EventSource's own onmessage
  ; semantics. A genuinely named event (e.g. "event: greeting") skips
  ; straight past this to the general dispatch_event call below, which
  ; reaches addEventListener('greeting', ...) registrations instead (and,
  ; since dispatch_event is keyed purely by type string with no
  ; special-casing, an addEventListener('message', ...) registration also
  ; still fires for an unnamed record — both registration surfaces reach
  ; the same listeners map).
  %%ismsgtype = call i32 @strcmp(ptr %%msgtype, ptr %[6]s)
  %%ismsgtype0 = icmp eq i32 %%ismsgtype, 0
  br i1 %%ismsgtype0, label %%checkonmsgfield, label %%afteronmsgfield

checkonmsgfield:
  %%cb_p = getelementptr { ptr, ptr, ptr, i64, ptr, ptr, ptr }, ptr %%instance, i32 0, i32 4
  %%cb = load ptr, ptr %%cb_p, align 8
  %%hascb = icmp ne ptr %%cb, null
  br i1 %%hascb, label %%callonmsgfield, label %%afteronmsgfield

callonmsgfield:
  %%fpSlot = getelementptr {ptr, ptr}, ptr %%cb, i32 0, i32 0
  %%fpVal = load ptr, ptr %%fpSlot, align 8
  %%epSlot = getelementptr {ptr, ptr}, ptr %%cb, i32 0, i32 1
  %%epVal = load ptr, ptr %%epSlot, align 8
  call void (ptr, ptr) %%fpVal(ptr %%epVal, ptr %%mev)
  br label %%afteronmsgfield

afteronmsgfield:
  call void @__kml_eventsource_dispatch_event(ptr %%ent, ptr %%msgtype, ptr %%mev)
  ret void

noop:
  ret void
}`, nl, colon, fData, fEvent, fID, defaultType, empty, fRetry))

	// __kml_eventsource_add_listener / _remove_listener (TDD-00038 Stage 2):
	// addEventListener(type, cb)/removeEventListener(type, cb) — the
	// listeners map (entry field index 4, created once in
	// __kml_eventsource_open) already exists for every entry, so unlike
	// EventEmitter's own on()/off() (which lazily creates a list on first
	// use per event name), only the per-*event-name* list itself needs
	// lazy creation here. once is always 0 (a plain, non-self-removing
	// listener) — addEventListener's {once:true} option isn't supported,
	// a deliberate narrowing (real-world SSE listener code essentially
	// never uses it).
	e.emitGlobal(`
define void @__kml_eventsource_add_listener(ptr %es_entry, ptr %type, ptr %closure) {
entry:
  %listeners_p = getelementptr { ptr, ptr, i64, i64, ptr, ptr, i64, i64, ptr }, ptr %es_entry, i32 0, i32 4
  %listeners = load ptr, ptr %listeners_p, align 8
  %raw = call i64 @__kml_map_str_get(ptr %listeners, ptr %type)
  %haslist = icmp ne i64 %raw, 0
  br i1 %haslist, label %havelist, label %makelist

makelist:
  %newlist = call ptr @__kml_ee_list_create()
  %newlist_i = ptrtoint ptr %newlist to i64
  call void @__kml_map_str_set(ptr %listeners, ptr %type, i64 %newlist_i)
  br label %havelist

havelist:
  %rawnow = call i64 @__kml_map_str_get(ptr %listeners, ptr %type)
  %list = inttoptr i64 %rawnow to ptr
  call void @__kml_ee_list_push(ptr %list, ptr %closure, i64 0)
  ret void
}`)

	e.emitGlobal(`
define void @__kml_eventsource_remove_listener(ptr %es_entry, ptr %type, ptr %closure) {
entry:
  %listeners_p = getelementptr { ptr, ptr, i64, i64, ptr, ptr, i64, i64, ptr }, ptr %es_entry, i32 0, i32 4
  %listeners = load ptr, ptr %listeners_p, align 8
  %raw = call i64 @__kml_map_str_get(ptr %listeners, ptr %type)
  %haslist = icmp ne i64 %raw, 0
  br i1 %haslist, label %havelist, label %done

havelist:
  %list = inttoptr i64 %raw to ptr
  call void @__kml_ee_list_remove(ptr %list, ptr %closure)
  br label %done

done:
  ret void
}`)

	// __kml_eventsource_dispatch_event(ptr entry, ptr type, ptr payload):
	// calls every addEventListener-registered listener for type (any type —
	// "message", "open", "error", or a genuinely custom name), each with
	// payload as its one argument. Snapshot-copies the listener-list
	// entries before iterating (memcpy into a scratch array) so a listener
	// that calls removeEventListener on itself (or another listener for the
	// same type) mid-dispatch can't corrupt the iteration — the exact same
	// defensive shape emitEventEmitterEmit (emit_eventemitter.go) already
	// uses for its own listener loop, translated into standalone IR since
	// this runs from the event-loop-driven scan, not KML-source-triggered
	// codegen.
	e.emitGlobal(`
define void @__kml_eventsource_dispatch_event(ptr %es_entry, ptr %type, ptr %payload) {
entry:
  %listeners_p = getelementptr { ptr, ptr, i64, i64, ptr, ptr, i64, i64, ptr }, ptr %es_entry, i32 0, i32 4
  %listeners = load ptr, ptr %listeners_p, align 8
  %raw = call i64 @__kml_map_str_get(ptr %listeners, ptr %type)
  %haslist = icmp ne i64 %raw, 0
  br i1 %haslist, label %walklist, label %done

walklist:
  %list = inttoptr i64 %raw to ptr
  %listlen = load i64, ptr %list, align 8
  %listdata_p = getelementptr i8, ptr %list, i64 16
  %listdata = load ptr, ptr %listdata_p, align 8
  %snapbytes = mul i64 %listlen, 16
  %snap = call ptr @malloc(i64 %snapbytes)
  call ptr @memcpy(ptr %snap, ptr %listdata, i64 %snapbytes)

  %wi_slot = alloca i64, align 8
  store i64 0, ptr %wi_slot, align 8
  br label %walkloop

walkloop:
  %wi = load i64, ptr %wi_slot, align 8
  %wdone = icmp sge i64 %wi, %listlen
  br i1 %wdone, label %done, label %walkbody

walkbody:
  %lslot = getelementptr {ptr, i64}, ptr %snap, i64 %wi, i32 0
  %lptr = load ptr, ptr %lslot, align 8
  %lfp_p = getelementptr {ptr, ptr}, ptr %lptr, i32 0, i32 0
  %lfp = load ptr, ptr %lfp_p, align 8
  %lep_p = getelementptr {ptr, ptr}, ptr %lptr, i32 0, i32 1
  %lep = load ptr, ptr %lep_p, align 8
  call void (ptr, ptr) %lfp(ptr %lep, ptr %payload)
  %wi1 = add i64 %wi, 1
  store i64 %wi1, ptr %wi_slot, align 8
  br label %walkloop

done:
  ret void
}`)
}
