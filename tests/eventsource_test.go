package tests

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// --- EventSource (TDD-00038 Stages 0-2: connection plumbing, SSE record
// parsing + onmessage, named events via addEventListener/onopen/onerror;
// Stage 3: CRLF-tolerant record boundaries, non-2xx/wrong-Content-Type
// terminal failure, auto-reconnect with retry:/Last-Event-ID replay) ---
//
// Spins up a local httptest.Server, same reasoning as newFetchTestServer:
// deterministic and offline-capable while still exercising the real
// libcurl multi-interface path end to end, not a mocked-out call site.

func newEventSourceTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/stream", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "data: hello\n\n")
		w.(http.Flusher).Flush()
		// Block until the client disconnects (es.close() on the KML side)
		// rather than a fixed sleep — keeps the test's own wall-clock cost
		// down to whatever the KML-side setTimeout delays actually are,
		// and avoids httptest.Server.Close() blocking on a still-sleeping
		// handler.
		<-r.Context().Done()
	})
	// /multi exercises the SSE-parsing surface in one flight: a comment line
	// (must produce no dispatch), a *named* event (must be withheld from
	// onmessage and only reach a matching addEventListener registration —
	// Stage 2), a multi-line data record with an id: field, and a trailing
	// id-less record that must inherit the previous record's lastEventId
	// (real spec's own persistence behavior).
	mux.HandleFunc("/multi", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl := w.(http.Flusher)
		fmt.Fprint(w, ": a comment, ignored\n\n")
		fl.Flush()
		fmt.Fprint(w, "event: greeting\nid: 1\ndata: named-event-data\n\n")
		fl.Flush()
		fmt.Fprint(w, "id: 2\ndata: line1\ndata: line2\n\n")
		fl.Flush()
		fmt.Fprint(w, "data: after-id\n\n")
		fl.Flush()
		<-r.Context().Done()
	})
	// /crlf and /barecr exercise Stage 3's CRLF-tolerant record-boundary
	// detection: a record terminated by "\r\n\r\n" or bare "\r\r" instead of
	// the plain "\n\n" every other route here uses.
	mux.HandleFunc("/crlf", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "data: crlf-hello\r\n\r\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	mux.HandleFunc("/barecr", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "data: cr-hello\r\r")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	// /notfound and /wrongtype exercise Stage 3's terminal-failure path: a
	// response that *arrived* but has the wrong shape (non-2xx status, or a
	// 2xx with a non-text/event-stream Content-Type) must permanently close
	// rather than schedule a reconnect — the real spec's "fail the
	// connection" step, distinct from a network-level failure.
	mux.HandleFunc("/notfound", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	})
	mux.HandleFunc("/wrongtype", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "not an SSE stream")
	})
	// /dropthenretry exercises Stage 3's auto-reconnect end to end: the
	// first connection sends a short retry: value plus an id:, then the
	// handler returns (ending the response, simulating a mid-stream drop)
	// rather than blocking on r.Context().Done() — the client should
	// reconnect after the server-specified delay, replaying the id it saw
	// as a Last-Event-ID request header. The second connection (detected via
	// that header actually showing up) echoes it back so the KML-side test
	// can observe the replayed value directly, not just "some reconnect
	// happened."
	mux.HandleFunc("/dropthenretry", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl := w.(http.Flusher)
		lastEventID := r.Header.Get("Last-Event-ID")
		if lastEventID == "" {
			fmt.Fprint(w, "retry: 50\nid: abc123\ndata: first\n\n")
			fl.Flush()
			return
		}
		fmt.Fprintf(w, "data: replayed-%s\n\n", lastEventID)
		fl.Flush()
		<-r.Context().Done()
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestE2EEventSourceReadyStateStartsConnecting(t *testing.T) {
	srv := newEventSourceTestServer(t)
	// close() right after the read (not before it) — printed readyState
	// still reflects the pre-close CONNECTING state; the close() only
	// exists so the process actually exits afterward instead of the event
	// loop's own (correct, by-design) keep-alive-while-open behavior
	// running forever with nothing left to ever close this EventSource.
	src := fmt.Sprintf(`
const es = new EventSource("%s/stream");
console.log(es.readyState);
es.close();
`, srv.URL)
	assertOutput(t, src, "0")
}

// The event loop's own per-iteration scan (__kml_eventsource_scan) is what
// transitions CONNECTING -> OPEN once the server's first bytes arrive —
// observed here via a setTimeout callback, since that's what forces the
// full __kml_event_loop_run (not just a do-nothing top-level script) to
// actually run and drive the transfer forward.
func TestE2EEventSourceReadyStateTransitionsToOpen(t *testing.T) {
	srv := newEventSourceTestServer(t)
	src := fmt.Sprintf(`
const es = new EventSource("%s/stream");
setTimeout(() => {
  console.log(es.readyState);
  es.close();
}, 150);
`, srv.URL)
	assertOutput(t, src, "1")
}

// close() takes effect synchronously (readyState reads CLOSED immediately
// after the call, not just on the next scan iteration) — matching real
// EventSource's own close() semantics.
func TestE2EEventSourceCloseIsSynchronous(t *testing.T) {
	srv := newEventSourceTestServer(t)
	src := fmt.Sprintf(`
const es = new EventSource("%s/stream");
setTimeout(() => {
  es.close();
  console.log(es.readyState);
}, 150);
`, srv.URL)
	assertOutput(t, src, "2")
}

// close() is idempotent — a second call must not double-decrement
// @__kml_es_active or otherwise misbehave (confirmed via output rather than
// IR inspection: if the second close() corrupted the active counter, the
// keep-alive condition could either exit the process early, dropping the
// second setTimeout's output, or hang).
func TestE2EEventSourceCloseIsIdempotent(t *testing.T) {
	srv := newEventSourceTestServer(t)
	src := fmt.Sprintf(`
const es = new EventSource("%s/stream");
setTimeout(() => {
  es.close();
  es.close();
  console.log(es.readyState);
}, 150);
setTimeout(() => {
  console.log("still alive");
}, 250);
`, srv.URL)
	assertOutput(t, src, "2\nstill alive")
}

// A transfer that ends (here: refused outright, since nothing listens on
// this port) without ever delivering a byte is a network-level failure, not
// a "wrong shape" response — Stage 3's auto-reconnect treats it as
// retryable: readyState goes back to CONNECTING (0) to wait out the retry
// delay, not permanently CLOSED. es.close() during that wait must still
// work (and end the process) — see TestE2EEventSourceCloseIsSynchronous's
// idempotency counterpart for the "was still live" case; this exercises
// __kml_eventsource_close's other branch, closing while reconnectAtMs is
// set and there's no live easy handle to tear down.
func TestE2EEventSourceConnectionRefusedRetries(t *testing.T) {
	src := `
const es = new EventSource("http://127.0.0.1:1/refused");
setTimeout(() => {
  console.log(es.readyState);
  es.close();
}, 150);
`
	assertOutput(t, src, "0")
}

// --- Stage 1: SSE record parsing + onmessage ---

func TestE2EEventSourceOnMessageReceivesData(t *testing.T) {
	srv := newEventSourceTestServer(t)
	src := fmt.Sprintf(`
const es = new EventSource("%s/stream");
es.onmessage = (ev) => {
  console.log(ev.data);
  console.log(ev.type);
  es.close();
};
setTimeout(() => {}, 300);
`, srv.URL)
	assertOutput(t, src, "hello\nmessage")
}

// Real spec appends every data: line's value plus a trailing LF to a
// running buffer, then strips exactly one trailing LF at dispatch time — so
// two data: lines join with a single embedded '\n' between them, no
// trailing one.
func TestE2EEventSourceMultiLineDataConcatenated(t *testing.T) {
	srv := newEventSourceTestServer(t)
	src := fmt.Sprintf(`
const es = new EventSource("%s/multi");
let count = 0;
es.onmessage = (ev) => {
  count = count + 1;
  console.log(ev.data);
  if (count == 2) { es.close(); }
};
setTimeout(() => {}, 300);
`, srv.URL)
	// First dispatched record is the id:2/multi-line one ("line1\nline2");
	// the earlier comment (no dispatch) and named "greeting" event
	// (withheld from onmessage — see the next test) both produce no output
	// of their own.
	assertOutput(t, src, "line1\nline2\nafter-id")
}

// id: persists across records that don't set their own — confirmed via
// lastEventId on a record with no id: field of its own.
func TestE2EEventSourceIdPersistsAcrossRecords(t *testing.T) {
	srv := newEventSourceTestServer(t)
	src := fmt.Sprintf(`
const es = new EventSource("%s/multi");
let count = 0;
es.onmessage = (ev) => {
  count = count + 1;
  console.log(ev.lastEventId);
  if (count == 2) { es.close(); }
};
setTimeout(() => {}, 300);
`, srv.URL)
	assertOutput(t, src, "2\n2")
}

// A record with a genuine event: field (a named event, "greeting" here) is
// Stage 2's addEventListener territory — Stage 1 must not misdeliver it to
// onmessage. Confirmed via a dispatch count: 4 records arrive (a comment, a
// named event, and two unnamed records), and only the 2 unnamed ones should
// ever reach onmessage.
func TestE2EEventSourceNamedEventNotDeliveredToOnMessage(t *testing.T) {
	srv := newEventSourceTestServer(t)
	src := fmt.Sprintf(`
const es = new EventSource("%s/multi");
let count = 0;
es.onmessage = (ev) => {
  count = count + 1;
};
setTimeout(() => {
  console.log(count);
  es.close();
}, 300);
`, srv.URL)
	assertOutput(t, src, "2")
}

// --- Stage 2: named events (addEventListener/removeEventListener), onopen/onerror ---

func TestE2EEventSourceOnOpenFires(t *testing.T) {
	srv := newEventSourceTestServer(t)
	src := fmt.Sprintf(`
const es = new EventSource("%s/stream");
es.onopen = (ev) => {
  console.log(ev.type);
  es.close();
};
setTimeout(() => {}, 300);
`, srv.URL)
	assertOutput(t, src, "open")
}

// A connection that never even succeeds (nothing listening on this port)
// must still fire onerror — a dropped/failed transfer, not just a
// completed one, is what "error" means here. Default retry is 3000ms, so
// within this test's 200ms window only the initial failure's error fires
// once, not a second one from an auto-reconnect attempt — es.close() is
// still needed at the end so the process actually exits instead of running
// out the retry-and-keep-alive loop indefinitely.
func TestE2EEventSourceOnErrorFires(t *testing.T) {
	src := `
const es = new EventSource("http://127.0.0.1:1/refused");
es.onerror = (ev) => {
  console.log(ev.type);
};
setTimeout(() => {
  es.close();
}, 200);
`
	assertOutput(t, src, "error")
}

// A record with a genuine event: field, previously silently withheld
// (Stage 1), now reaches a matching addEventListener registration.
func TestE2EEventSourceAddEventListenerReceivesNamedEvent(t *testing.T) {
	srv := newEventSourceTestServer(t)
	src := fmt.Sprintf(`
const es = new EventSource("%s/multi");
es.addEventListener("greeting", (ev) => {
  console.log(ev.type);
  console.log(ev.data);
  es.close();
});
setTimeout(() => {}, 300);
`, srv.URL)
	assertOutput(t, src, "greeting\nnamed-event-data")
}

// removeEventListener actually stops delivery — also exercises the
// "MessageEvent" type name now being resolvable in an explicit annotation
// (needed here since an extracted, non-inline handler has no call-site
// context to infer its parameter's type from, unlike an arrow function
// literal passed directly to addEventListener).
func TestE2EEventSourceRemoveEventListenerStopsDelivery(t *testing.T) {
	srv := newEventSourceTestServer(t)
	src := fmt.Sprintf(`
const es = new EventSource("%s/multi");
let count = 0;
const handler = (ev: MessageEvent) => { count = count + 1; };
es.addEventListener("greeting", handler);
es.removeEventListener("greeting", handler);
setTimeout(() => {
  console.log(count);
  es.close();
}, 300);
`, srv.URL)
	assertOutput(t, src, "0")
}

// onmessage and addEventListener('message', ...) are two independent
// registration surfaces reaching the same underlying dispatch for an
// unnamed record — both should fire.
func TestE2EEventSourceOnMessageAndAddEventListenerBothFireForUnnamedRecord(t *testing.T) {
	srv := newEventSourceTestServer(t)
	src := fmt.Sprintf(`
const es = new EventSource("%s/stream");
let count = 0;
es.onmessage = (ev) => { count = count + 1; };
es.addEventListener("message", (ev) => { count = count + 1; });
setTimeout(() => {
  console.log(count);
  es.close();
}, 300);
`, srv.URL)
	assertOutput(t, src, "2")
}

// --- Stage 3: CRLF-tolerant boundaries, terminal vs. retryable failure, auto-reconnect ---

func TestE2EEventSourceCRLFRecordBoundary(t *testing.T) {
	srv := newEventSourceTestServer(t)
	src := fmt.Sprintf(`
const es = new EventSource("%s/crlf");
es.onmessage = (ev) => {
  console.log(ev.data);
  es.close();
};
setTimeout(() => {}, 300);
`, srv.URL)
	assertOutput(t, src, "crlf-hello")
}

func TestE2EEventSourceBareCRRecordBoundary(t *testing.T) {
	srv := newEventSourceTestServer(t)
	src := fmt.Sprintf(`
const es = new EventSource("%s/barecr");
es.onmessage = (ev) => {
  console.log(ev.data);
  es.close();
};
setTimeout(() => {}, 300);
`, srv.URL)
	assertOutput(t, src, "cr-hello")
}

// A 404 (or any non-2xx) is a response that *arrived* with the wrong shape,
// not a network-level failure — Stage 3 treats this as terminal (readyState
// -> CLOSED, no reconnect ever scheduled), matching the real spec's own
// "fail the connection" step. If this were misclassified as retryable
// instead, @__kml_es_active would never hit 0 and the process would hang
// past this test's timeout rather than exiting cleanly.
func TestE2EEventSourceNonOKStatusEndsClosedNoRetry(t *testing.T) {
	srv := newEventSourceTestServer(t)
	src := fmt.Sprintf(`
const es = new EventSource("%s/notfound");
setTimeout(() => {
  console.log(es.readyState);
}, 200);
`, srv.URL)
	assertOutput(t, src, "2")
}

// A 200 response with a Content-Type that isn't text/event-stream is the
// same terminal-failure shape as a bad status.
func TestE2EEventSourceWrongContentTypeEndsClosedNoRetry(t *testing.T) {
	srv := newEventSourceTestServer(t)
	src := fmt.Sprintf(`
const es = new EventSource("%s/wrongtype");
setTimeout(() => {
  console.log(es.readyState);
}, 200);
`, srv.URL)
	assertOutput(t, src, "2")
}

// End-to-end auto-reconnect: the server drops the connection after sending
// retry: 50 and an id:, ending its response. The client should reconnect
// ~50ms later, replaying the id it saw as a Last-Event-ID request header —
// confirmed by the reconnected server echoing that header's value back,
// not just by a second onmessage firing (which reconnecting with the wrong
// header, or no header, would also produce).
func TestE2EEventSourceAutoReconnectReplaysLastEventID(t *testing.T) {
	srv := newEventSourceTestServer(t)
	src := fmt.Sprintf(`
const es = new EventSource("%s/dropthenretry");
let count = 0;
es.onmessage = (ev) => {
  count = count + 1;
  console.log(ev.data);
  if (count == 2) { es.close(); }
};
setTimeout(() => {}, 800);
`, srv.URL)
	assertOutput(t, src, "first\nreplayed-abc123")
}

// Same scenario as above but with no setTimeout (or any other timer/
// listener/fetch) anywhere in the program — deliberately, to catch a real
// bug ADR-00133 found and fixed: without an unrelated JS timer to
// incidentally bound select()'s wait, the event loop had two separate gaps
// (an already-arrived-but-undispatched EventSource response racing ahead of
// select()'s own readiness check, and a waiting-to-reconnect entry's own
// deadline never participating in select()'s timeout computation at all)
// that could each, independently, leave select() blocked on a NULL timeout
// forever. This is the only test in this file with no setTimeout at all —
// keep it that way; adding one back would silently stop covering either gap.
func TestE2EEventSourceAutoReconnectNoOtherTimerInProgram(t *testing.T) {
	srv := newEventSourceTestServer(t)
	src := fmt.Sprintf(`
const es = new EventSource("%s/dropthenretry");
let count = 0;
es.onmessage = (ev) => {
  count = count + 1;
  console.log(ev.data);
  if (count == 2) { es.close(); }
};
`, srv.URL)
	assertOutput(t, src, "first\nreplayed-abc123")
}
