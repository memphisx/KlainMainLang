// runtime_fetch.go — the fetch client runtime (ADR-00050, TDD-00097,
// TDD-00040): libcurl's multi interface driven by the event loop, the
// abort scan, the single/group/settled/headers waits and the Response
// header and status helpers. The routines live in fetchsrc/fetch.c and
// fetchsrc/fetch_stream.c (TDD-00240); this file declares them, defines the
// by-value wrappers the C ABI cannot express, and picks which sections of
// the C unit a program links (FetchCFlags).
//
// A pending fetch is a malloc'd { ptr easy, ptr buf, i64 done, i64
// httpStatus, i64 curlResult, ptr signal, i64 headersDone, ptr bodyStream,
// i64 paused, ptr bridge } (80 bytes); buf is the { ptr, i64, i64, ptr, ptr }
// growable write buffer the write callback fills (slot 3: the pending
// backpointer, slot 4: the header-capture buffer). The CURLOPT_*/CURLINFO_*
// values the C uses are libcurl's frozen ABI numbers.
package llvm

import (
	_ "embed"
	"fmt"
)

//go:embed fetchsrc/fetch.c
var fetchSource string

//go:embed fetchsrc/fetch_stream.c
var fetchStreamSource string

// FetchSource is the fetch runtime's C source, behind kml_layout.h.
func FetchSource() string { return layoutHeader() + fetchSource + fetchStreamSource }

// UsesFetch reports whether the program links the fetch runtime.
func (e *Emitter) UsesFetch() bool { return e.usedFetch || e.usedFetchAsync }

// FetchCFlags selects the sections of fetch.c the program uses (each one
// references runtime pieces only its ensure*() brings in) and hands over the
// constants and offsets C shares with the emitter.
func (e *Emitter) FetchCFlags() []string {
	connOffs, _ := irStructOffsets("{ i64, ptr, ptr, ptr, ptr }")
	rsOffs, _ := irStructOffsets(rstreamStructIR)
	f := []string{
		fmt.Sprintf("-DKML_KIND_TYPEERR=%dLL", errorTypeIDStored(errorKindIDs["TypeError"])),
		fmt.Sprintf("-DKML_KIND_DOMEXC=%dLL", errorTypeIDStored(errorKindIDs["DOMException"])),
		fmt.Sprintf("-DKML_KIND_AGG=%dLL", errorTypeIDStored(errorKindIDs["AggregateError"])),
		fmt.Sprintf("-DKML_AGG_SIZE=%d", aggregateErrorStructSize),
		fmt.Sprintf("-DKML_TAG_OBJECT=%d", kmlTagObject),
		fmt.Sprintf("-DKML_ERR_FLAG=%dLL", errorTypeIDFlag),
		fmt.Sprintf("-DKML_NB_UNDEFINED=%dLL", nbUndefined),
		fmt.Sprintf("-DKML_FETCH_CONN_PFETCH=%d", connOffs[3]),
		fmt.Sprintf("-DKML_FETCH_CONN_PGROUP=%d", connOffs[4]),
		fmt.Sprintf("-DKML_RS_STATE=%d", rsOffs[0]),
		fmt.Sprintf("-DKML_RS_RH=%d", rsOffs[14]),
		fmt.Sprintf("-DKML_RS_RL=%d", rsOffs[15]),
	}
	for _, s := range []struct {
		on   bool
		flag string
	}{
		{e.usedFetchAsync, "KML_FETCH_ASYNC"},
		{e.usedAwaitFetchHeaders, "KML_FETCH_HDRWAIT"},
		{e.usedPromiseCombinators, "KML_FETCH_GROUP"},
		{e.usedPendingFinishSettled, "KML_FETCH_SETTLED"},
		{e.usedFetchAwaitSettled, "KML_FETCH_AWAITSET"},
		{e.usedFetchHeadersRaw, "KML_FETCH_HDRRAW"},
		{e.usedFetchHeadersMap, "KML_FETCH_HDRMAP"},
		{e.fnDecls["__kml_fetch_status_text"], "KML_FETCH_STATUSTEXT"},
		{e.usedXHRHeadersAll, "KML_XHR_HDRS"},
		{e.usedFetchBodyStream, "KML_FETCH_STREAM"},
		{e.usedFetchAsync && e.hasMaySuspend, "KML_FETCH_TASKS"},
		{e.usedFetchAsync && e.hasMaySuspend && e.isGCMode(), "KML_FETCH_GC_RESTORE"},
	} {
		if s.on {
			f = append(f, "-D"+s.flag+"=1")
		}
	}
	return f
}

// ensureFetch declares the curl_easy_* primitives and the pieces shared by
// every fetch call, sync or async: the write callback, the failure-message
// lookup and the pre-settled failed fetch. (ADR-00095 removed the old
// blocking __kml_fetch; ADR-00050's multi interface replaced it.)
func (e *Emitter) ensureFetch() {
	if e.usedFetch {
		return
	}
	e.usedFetch = true
	e.requireLink("curl")
	e.ensureMalloc()
	e.ensureCalloc()
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
	// The names and messages fetch's errors carry, interned here so the C
	// reads them (an error's name is compared by address).
	strs := []string{"TypeError", "AbortError", "The operation was aborted", "TimeoutError",
		"The operation timed out", "AggregateError", "All promises were rejected", ""}
	refs := ""
	for i, s := range strs {
		if i > 0 {
			refs += ", "
		}
		refs += "ptr " + e.internString(s)
	}
	e.emitGlobal(fmt.Sprintf("@__kml_fetch_strs = constant [%d x ptr] [%s], align 8", len(strs), refs))
	// Read by the worker pre-init (emitter.go) and fetch.c.
	e.emitGlobal("@__kml_curl_inited = global i1 0, align 1")
	e.emitGlobal(`declare ptr @__kml_fetch_errstr(i32)
declare ptr @__kml_fetch_failed_pending(i64)`)
}

// ensureFetchAsync declares everything a real, non-blocking `await
// fetch(...)` needs (ADR-00050): libcurl's multi interface, driven by the
// same select() loop http.listen uses, so a fetch awaited from inside a
// connection-handler fiber (or a task) yields instead of blocking, letting
// other connections' fibers and their own concurrent fetches progress.
//
//	__kml_fetch_async(url, method, headers, body, signal) -> pending
//	  Starts the transfer and returns at once; the nullable method/headers/
//	  body skip their setopt (a plain fetch(url) passes all three null).
//	__kml_curl_drain_messages()
//	  Records each finished transfer into its pending struct, drops the easy
//	  handle and sets done.
//	__kml_await_fetch(pending) -> { i64 status, ptr body, i64 bodyLen }
//	  Loops until done: parks the task or connection fiber it runs on, else
//	  takes a turn of the event loop; throws a catchable TypeError on a
//	  transfer-level failure.
func (e *Emitter) ensureFetchAsync() {
	if e.usedFetchAsync {
		return
	}
	e.usedFetchAsync = true
	e.noteLoopTurn() // the main-stack waits take turns of the real loop
	e.ensureFetch()
	e.ensureStrHeaderRuntime() // error .message must be headered for concat/=== (TDD-00120)
	e.ensureFiberRuntime()
	e.ensureCurrentTaskGlobal() // @__kml_current_task, read by the task-park path
	e.ensureExceptionHelpers()
	e.ensureSignalAborted() // __kml_signal_aborted, used by the await abort check (TDD-00081)
	e.ensureNanBox()        // __kml_nb_tag/__kml_nb_pay, to throw the boxed signal.reason on abort

	e.emitGlobal("declare ptr @curl_multi_init()")
	e.emitGlobal("declare i32 @curl_multi_add_handle(ptr noundef, ptr noundef)")
	e.emitGlobal("declare i32 @curl_multi_remove_handle(ptr noundef, ptr noundef)")
	e.emitGlobal("declare i32 @curl_multi_fdset(ptr noundef, ptr noundef, ptr noundef, ptr noundef, ptr noundef)")
	e.emitGlobal("declare i32 @curl_multi_timeout(ptr noundef, ptr noundef)")
	e.emitGlobal("declare i32 @curl_multi_perform(ptr noundef, ptr noundef)")
	e.emitGlobal("declare ptr @curl_multi_info_read(ptr noundef, ptr noundef)")
	// The one CURLM handle, created on first use; the HTTP client reads it too.
	e.emitGlobal("@__kml_curl_multi = thread_local global ptr null, align 8")
	e.emitGlobal(`declare ptr @__kml_fetch_async(ptr, ptr, ptr, ptr, ptr)
declare ptr @__kml_fetch_async_n(ptr, ptr, ptr, ptr, i64, ptr)
declare void @__kml_curl_drain_messages()
declare i32 @__kml_curl_inflight()
declare zeroext i1 @__kml_fetch_pump()
declare void @__kml_sigfetch_add(ptr)
declare void @__kml_sigfetch_scan()
declare void @__kml_fetch_abort_now(ptr)
declare void @__kml_fetch_throw_abort(ptr)
declare void @__kml_pending_finish_c(ptr, ptr)
declare void @__kml_await_fetch_c(ptr, ptr)`)
	// The {status, body, len} aggregate cannot cross the C ABI by value: the
	// C routines fill an out-param, these keep the by-value signatures the
	// call sites use.
	e.emitGlobal(`define { i64, ptr, i64 } @__kml_pending_finish(ptr %pending) {
entry:
  %o = alloca { i64, ptr, i64 }, align 8
  call void @__kml_pending_finish_c(ptr %pending, ptr %o)
  %r = load { i64, ptr, i64 }, ptr %o, align 8
  ret { i64, ptr, i64 } %r
}

define { i64, ptr, i64 } @__kml_await_fetch(ptr %pending) {
entry:
  %o = alloca { i64, ptr, i64 }, align 8
  call void @__kml_await_fetch_c(ptr %pending, ptr %o)
  %r = load { i64, ptr, i64 }, ptr %o, align 8
  ret { i64, ptr, i64 } %r
}`)
}

// ensureAwaitFetchHeaders declares @__kml_await_fetch_headers (TDD-00097
// Stage 4): drive the multi loop until the response's headers have arrived
// or the transfer is done, then return the HTTP status — the
// resolve-at-headers point `await fetch(...)` uses, so `.body` can stream
// the rest. Also brings in @__kml_fetch_pump, the no-fiber await drive's
// hook (a no-op stub is emitted at finalize when fetch is unused).
func (e *Emitter) ensureAwaitFetchHeaders() {
	if e.usedAwaitFetchHeaders {
		return
	}
	e.usedAwaitFetchHeaders = true
	e.ensureFetchAsync()
	e.ensureMicrotasks()
	e.emitGlobal("declare i64 @__kml_await_fetch_headers(ptr)")
}

// ensureCurlSlist declares curl_slist_append (ADR-00074/TDD-00017) —
// emit_fetch.go's buildFetchHeaderList calls it once per Map<string,string>
// entry to build the linked list CURLOPT_HTTPHEADER expects. The built list
// is never freed in manual mode, like every other fetch-related allocation
// (-mm=gc is the opt-in fix, TDD-00001).
func (e *Emitter) ensureCurlSlist() {
	if e.usedCurlSlist {
		return
	}
	e.usedCurlSlist = true
	e.requireLink("curl")
	e.emitGlobal("declare ptr @curl_slist_append(ptr noundef, ptr noundef)")
}

// ensurePromiseCombinators declares the group-wait primitives
// Promise.all/.race/.allSettled/.any (emit_promise.go, ADR-00073) use to wait
// on N pending fetches at once. A group is a malloc'd { ptr membersArr, i64
// count, i64 mode } (mode 0 = every member done, 1 = the first, 2 = the first
// to transport-succeed or all done); membersArr points at the same pending
// structs the Promise<Response> slots hold.
func (e *Emitter) ensurePromiseCombinators() {
	if e.usedPromiseCombinators {
		return
	}
	e.usedPromiseCombinators = true
	e.ensureFetchAsync()
	e.ensureMalloc()
	e.ensureExceptionHelpers()
	e.ensurePendingFinishSettled()
	e.emitGlobal(`declare zeroext i1 @__kml_group_satisfied(ptr)
declare i64 @__kml_first_done_index(ptr)
declare i64 @__kml_first_success_index(ptr)
declare void @__kml_group_throw_aggregate(ptr)
declare void @__kml_await_group_wait(ptr)`)
}

// ensurePendingFinishSettled declares __kml_pending_finish_settled(ptr
// pending) -> {i1 failed, i64 status, ptr body, ptr reasonMsg, i64 bodyLen}:
// a non-throwing sibling of __kml_pending_finish for Promise.allSettled and
// XMLHttpRequest.send() (TDD-00040). On failure reasonMsg is curl's message
// and bodyLen 0.
func (e *Emitter) ensurePendingFinishSettled() {
	if e.usedPendingFinishSettled {
		return
	}
	e.usedPendingFinishSettled = true
	e.ensureFetch()
	e.emitGlobal(`declare void @__kml_pending_finish_settled_c(ptr, ptr)

define { i1, i64, ptr, ptr, i64 } @__kml_pending_finish_settled(ptr %pending) {
entry:
  %o = alloca { i1, i64, ptr, ptr, i64 }, align 8
  call void @__kml_pending_finish_settled_c(ptr %pending, ptr %o)
  %r = load { i1, i64, ptr, ptr, i64 }, ptr %o, align 8
  ret { i1, i64, ptr, ptr, i64 } %r
}`)
}

// ensureFetchAwaitSettled declares __kml_await_fetch_settled(ptr pending) ->
// the settled result (TDD-00040): __kml_await_fetch's wait finishing without
// throwing — XMLHttpRequest.send()'s whole transfer mechanism, which never
// throws on a network failure (it fires .onerror instead).
func (e *Emitter) ensureFetchAwaitSettled() {
	if e.usedFetchAwaitSettled {
		return
	}
	e.usedFetchAwaitSettled = true
	e.ensureFetchAsync()
	e.ensurePendingFinishSettled()
	e.emitGlobal(`declare void @__kml_await_fetch_settled_c(ptr, ptr)

define { i1, i64, ptr, ptr, i64 } @__kml_await_fetch_settled(ptr %pending) {
entry:
  %o = alloca { i1, i64, ptr, ptr, i64 }, align 8
  call void @__kml_await_fetch_settled_c(ptr %pending, ptr %o)
  %r = load { i1, i64, ptr, ptr, i64 }, ptr %o, align 8
  ret { i1, i64, ptr, ptr, i64 } %r
}`)
}

// ensureFetchHeadersRaw declares __kml_fetch_headers_raw: a Response's
// captured raw header text (every response's block of a redirect chain
// included) as a string, "" for a Response with none. Headers' #fillRaw
// parses it.
func (e *Emitter) ensureFetchHeadersRaw() {
	if e.usedFetchHeadersRaw {
		return
	}
	e.usedFetchHeadersRaw = true
	e.ensureFetch()
	e.ensureStrHeaderRuntime()
	e.emitGlobal("declare ptr @__kml_fetch_headers_raw(ptr)")
}

// ensureFetchHeadersMap declares __kml_fetch_headers_map (ADR-00490): lazily
// parses a Response's captured raw header text into a Map<string,string> with
// lowercased keys, matching the Fetch spec's Headers case rule.
func (e *Emitter) ensureFetchHeadersMap() {
	if e.usedFetchHeadersMap {
		return
	}
	e.usedFetchHeadersMap = true
	e.ensureFetch()
	e.ensureMapStrHelpers()
	e.ensureStrHeaderRuntime()
	e.emitGlobal("declare ptr @__kml_fetch_headers_map(ptr)")
}

// ensureFetchStatusText declares __kml_fetch_status_text: a fetched
// Response's statusText, the reason phrase of the last status line in its
// captured headers, or "" when there is none (HTTP/2 sends no reason phrase)
// or the Response was not fetched.
func (e *Emitter) ensureFetchStatusText() {
	if e.fnDecls["__kml_fetch_status_text"] {
		return
	}
	e.fnDecls["__kml_fetch_status_text"] = true
	e.ensureFetch()
	e.ensureStrHeaderRuntime()
	e.emitGlobal("declare ptr @__kml_fetch_status_text(ptr)")
}

// ensureXHRHeadersAll declares __kml_xhr_headers_all (ADR-00490): serializes
// a parsed response-header map into getAllResponseHeaders()'s "name:
// value\r\n" text, in stored (arrival) order; a null map yields "".
func (e *Emitter) ensureXHRHeadersAll() {
	if e.usedXHRHeadersAll {
		return
	}
	e.usedXHRHeadersAll = true
	e.ensureMapStrHelpers()
	e.ensureStrHeaderRuntime()
	e.emitGlobal("declare ptr @__kml_xhr_headers_all(ptr)")
}
