// fetch(url, init) — custom method, headers, and request body (ADR-00074,
// TDD-00017).
//
// Like examples/fetch/fetch.ts, this talks to a local fixture server
// (tools/httpbin-lite/, started by `make examples` before this file runs —
// see ADR-00096) instead of a real external website, so this file needs no
// real network access and gives deterministic results.
//
// init is a RequestInit: method, headers (a Headers, a Map, a record or
// [name, value] pairs) and body (a string, URLSearchParams, Blob, ArrayBuffer
// or bytes). examples/fetch/fetch_request_init.ts passes it through a typed
// wrapper; real Request/Headers classes also exist (TDD-00040) — see
// examples/fetch/fetch_request_headers.ts.

// ── a POST with a JSON body ─────────────────────────────────────────────────
const posted = await fetch('http://127.0.0.1:8765/post', {
    method: 'POST',
    body: JSON.stringify({ hello: 'world' }),
})
console.log(posted.status)                              // 200
console.log((await posted.text()).indexOf('"hello":"world"') > -1)  // true — the fixture echoes the body back verbatim

// ── custom headers, sent alongside a GET ────────────────────────────────────
const headers: Map<string, string> = new Map<string, string>()
headers.set('X-Example-Header', 'kml-value')
const withHeaders = await fetch('http://127.0.0.1:8765/headers', { headers: headers })
console.log((await withHeaders.text()).indexOf('kml-value') > -1)  // true — the fixture's /headers echoes every request header back

// ── an explicit method with no body still works (e.g. DELETE) ──────────────
const deleted = await fetch('http://127.0.0.1:8765/delete', { method: 'DELETE' })
console.log(deleted.status)  // 200

// ── a body needs a method that takes one ────────────────────────────────────
// (the default method is GET, and a GET or HEAD request cannot carry a body:
// the fetch rejects with Node's TypeError and nothing is sent)
try {
    await fetch('http://127.0.0.1:8765/post', { body: 'raw-body-text' })
} catch (e) {
    console.log((e as Error).name, (e as Error).message)  // TypeError Request with GET/HEAD method cannot have body.
}

// ── fetch(url) with no init argument still works exactly as before ─────────
const plain = await fetch('http://127.0.0.1:8765/get')
console.log(plain.status)  // 200
