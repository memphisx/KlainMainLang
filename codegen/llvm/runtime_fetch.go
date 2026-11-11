// runtime_fetch.go — the fetch client runtime (ADR-00050, TDD-00097,
// TDD-00040): libcurl's multi interface driven by the event loop, the
// abort scan, the single/group/settled/headers waits and the Response
// header and status helpers. The routines live in fetchsrc/fetch.c and
// fetchsrc/fetch_stream.c (TDD-00240); this file declares them and picks
// which sections of the C unit a program links (FetchCFlags).
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
	"strings"
)

//go:embed fetchsrc/fetch.c
var fetchSource string

//go:embed fetchsrc/fetch_stream.c
var fetchStreamSource string

// FetchSource is the fetch runtime's C source, behind kml_layout.h.
func FetchSource() string { return layoutHeader() + fetchSource + fetchStreamSource }

// UsesFetch reports whether the program links the fetch runtime.
func (e *Emitter) UsesFetch() bool { return e.usedFetch || e.usedFetchAsync }

// FetchCFlags hands fetch.c the constants and offsets C shares with the
// emitter; they are the same for every program, so its object is too: the
// link drops the sections a program does not use (DeadStripFlags).
func (e *Emitter) FetchCFlags() []string {
	rsOffs, _ := irStructOffsets(rstreamStructIR)
	return append(append([]string{},
		fmt.Sprintf("-DKML_KIND_TYPEERR=%dLL", errorTypeIDStored(errorKindIDs["TypeError"])),
		fmt.Sprintf("-DKML_KIND_DOMEXC=%dLL", errorTypeIDStored(errorKindIDs["DOMException"])),
		fmt.Sprintf("-DKML_ERR_FLAG=%dLL", errorTypeIDFlag),
		fmt.Sprintf("-DKML_RS_STATE=%d", rsOffs[0]),
		fmt.Sprintf("-DKML_RS_RH=%d", rsOffs[14]),
		fmt.Sprintf("-DKML_RS_RL=%d", rsOffs[15])), append(e.signalCFlags(), DeadStripFlags(e.exportsRuntime())...)...)
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
	// fetch.c drives its transfers on the event loop (loop.c's handles,
	// timers.c's clock).
	e.ensureEventLoop()
	e.ensureStrHeaderRuntime() // error .message must be headered for concat/=== (TDD-00120)
	e.ensureExceptionHelpers()
	// A failure's cause (fetch.c): the system error's code and errno, a
	// boxed Error, the lookup's hostname in the extra bag.
	e.ensureNanBox()
	e.ensureAnyToPrimitive()
	e.ensureErrorNames()
	e.ensureStrHeaderRuntime()

	// The names and messages fetch's errors carry, interned here so the C
	// reads them (an error's name is compared by address).
	strs := []string{"TypeError", "AbortError", "The operation was aborted", "TimeoutError",
		"The operation timed out", "AggregateError", "All promises were rejected", "", "Error", "fetch failed"}
	refs := ""
	for i, s := range strs {
		if i > 0 {
			refs += ", "
		}
		refs += irPtr + " " + e.internString(s)
	}
	// hooks.c holds them; the hooks constructor fills them in.
	e.emitGlobal(fmt.Sprintf("@__kml_fetch_strs.ir = private"+irGlobalAS+" constant [%d x "+irPtr+"] [%s], align 8", len(strs), refs))
	e.emitGlobal(fmt.Sprintf("@__kml_fetch_strs = external"+irGlobalAS+" global [%d x "+irPtr+"], align 8", len(strs)))
	e.addHookInit(fmt.Sprintf("  call "+irPtr+" @memcpy("+irPtr+" @__kml_fetch_strs, "+irPtr+" @__kml_fetch_strs.ir, i64 %d)\n", 8*len(strs)))
	// Read by the worker pre-init (emitter.go) and fetch.c.
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
	e.ensureExceptionHelpers()
	e.ensureSignalAborted() // __kml_signal_aborted, used by the await abort check (TDD-00081)
	e.ensureNanBox()        // __kml_nb_tag/__kml_nb_pay, to throw the boxed signal.reason on abort

	// The one CURLM handle, created on first use; the HTTP client reads it too.
}

// emitOutAggCall calls C routine fn, which writes an aggregate of type agg
// through its last (out) parameter, and returns the aggregate's register: an
// aggregate this size cannot cross the C ABI by value.
func (e *Emitter) emitOutAggCall(fn, agg string, args ...string) string {
	o := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca %s, align 8", o, agg))
	e.emitInstr(fmt.Sprintf("call void @%s(%s, "+irPtr+" %s)", fn, strings.Join(args, ", "), o))
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, "+irPtr+" %s, align 8", r, agg, o))
	return r
}
