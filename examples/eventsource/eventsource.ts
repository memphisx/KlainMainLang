// EventSource (Server-Sent Events) — TDD-00038 Stages 0-2: connection
// plumbing (readyState/close), SSE record parsing, onmessage/onopen/
// onerror, and addEventListener/removeEventListener for named events; Stage
// 3: auto-reconnect (retry:/Last-Event-ID replay) and terminal (no-retry)
// failure on a non-2xx/wrong-Content-Type response. `new EventSource(url)`
// opens a real, non-blocking libcurl transfer (reusing the same event loop
// and multi-interface machinery fetch() already uses).
//
// Talks to local fixture server endpoints (tools/httpbin-lite/'s /stream
// and /stream-named, started by `make examples` — see ADR-00096) instead
// of a real external SSE feed, so this stays deterministic and
// offline-capable like every other examples/fetch/*.ts file.

// ── readyState / close() ─────────────────────────────────────────────────
const es = new EventSource('http://127.0.0.1:8765/stream')
console.log(es.readyState)   // 0 (CONNECTING) — the transfer has only just started

// readyState transitions to OPEN once the server's first bytes actually
// arrive — that only happens as the event loop actually runs (here, by
// waiting on a timer), not synchronously at construction.
setTimeout(() => {
  console.log(es.readyState) // 1 (OPEN)
  es.close()
  console.log(es.readyState) // 2 (CLOSED) — close() takes effect synchronously
}, 200)

// ── onmessage / onopen: receiving actual SSE data ────────────────────────
// A second, independent EventSource against the same endpoint — the
// fixture's /stream sends one "data: hello\n\n" record and then holds the
// connection open, so onopen and onmessage each fire exactly once.
const messages = new EventSource('http://127.0.0.1:8765/stream')
messages.onopen = (ev) => {
  console.log(ev.type)         // open
}
messages.onmessage = (ev) => {
  console.log(ev.data)         // hello
  console.log(ev.type)         // message — the default type for an unnamed record
  console.log(ev.lastEventId)  // (empty) — this record never set an id: field
  messages.close()
}

// ── addEventListener: named events ───────────────────────────────────────
// /stream-named sends one named ("greeting") event followed by one unnamed
// one — onmessage only ever fires for the unnamed record; the named one
// only reaches a matching addEventListener registration.
const named = new EventSource('http://127.0.0.1:8765/stream-named')
let seen = 0
named.addEventListener('greeting', (ev) => {
  console.log(ev.type)   // greeting
  console.log(ev.data)   // hi there
  seen = seen + 1
})
named.onmessage = (ev) => {
  console.log(ev.data)   // plain
  seen = seen + 1
  if (seen == 2) { named.close() }
}

// ── onerror / auto-reconnect ─────────────────────────────────────────────
// Nothing listens on this port, so the connection fails outright — a
// network-level failure, which Stage 3 treats as retryable: onerror fires,
// then readyState goes back to CONNECTING (not CLOSED) while it waits to
// retry (default 3000ms). close() here just stops the retry loop for this
// example; it doesn't mean the failure itself was terminal.
const bad = new EventSource('http://127.0.0.1:1/refused')
bad.onerror = (ev) => {
  console.log(ev.type)   // error
}
setTimeout(() => { bad.close() }, 200)

// ── terminal failure: non-2xx / wrong Content-Type never retries ────────
// /status/404 returns a plain 404 with no body — a response that *arrived*
// with the wrong shape, not a network failure, so Stage 3 fails the
// connection permanently instead of scheduling a reconnect.
const notFound = new EventSource('http://127.0.0.1:8765/status/404')
setTimeout(() => {
  console.log(notFound.readyState) // 2 (CLOSED) — and stays that way, no retry
}, 200)

// ── auto-reconnect with retry:/Last-Event-ID replay ──────────────────────
// /stream-retry sends one record (with a short retry: value and an id:)
// then ends the response, simulating a dropped connection. The client
// reconnects after the server-specified delay, replaying the id it saw as
// a Last-Event-ID request header — the second record proves the replay
// actually reached the server, not just that some reconnect happened.
const retrying = new EventSource('http://127.0.0.1:8765/stream-retry')
let retryCount = 0
retrying.onmessage = (ev) => {
  retryCount = retryCount + 1
  console.log(ev.data) // "dropping soon", then "reconnected, last id was first-attempt"
  if (retryCount == 2) { retrying.close() }
}
