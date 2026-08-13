// fetch(url, init) — custom method, headers, and request body (ADR-00074,
// TDD-00017).
//
// Like examples/fetch/fetch.ts, this talks to a local fixture server
// (tools/httpbin-lite/, started by `make examples` before this file runs —
// see ADR-00096) instead of a real external website, so this file needs no
// real network access and gives deterministic results.
//
// init is any value with some subset of method: string /
// headers: Map<string,string> | Headers / body: string fields — a plain
// object works fine here with no Request/Headers class needed, matching how
// this compiler already represents every other bag of string headers
// (http.listen's req.headers, a handler's own optional response headers
// field) as a plain Map<string,string>. Real Request/Headers classes also
// exist (TDD-00040) for when you want them — see
// examples/fetch/fetch_request_headers.ts.

// ── a POST with a JSON body ─────────────────────────────────────────────────
const posted = await fetch('http://127.0.0.1:8765/post', {
    method: 'POST',
    body: JSON.stringify({ hello: 'world' }),
})
console.log(posted.status)                              // 200
console.log(posted.text().indexOf('"hello":"world"') > -1)  // true — the fixture echoes the body back verbatim

// ── custom headers, sent alongside a GET ────────────────────────────────────
const headers: Map<string, string> = new Map<string, string>()
headers.set('X-Example-Header', 'kml-value')
const withHeaders = await fetch('http://127.0.0.1:8765/headers', { headers: headers })
console.log(withHeaders.text().indexOf('kml-value') > -1)  // true — the fixture's /headers echoes every request header back

// ── an explicit method with no body still works (e.g. DELETE) ──────────────
const deleted = await fetch('http://127.0.0.1:8765/delete', { method: 'DELETE' })
console.log(deleted.status)  // 200

// ── setting a body without an explicit method sends it as POST ─────────────
// (real, well-known libcurl behavior — CURLOPT_POSTFIELDS implies POST
// unless overridden by an explicit method; confirmed directly, not assumed
// — see ADR-00074's Investigation)
const bodyOnly = await fetch('http://127.0.0.1:8765/post', { body: 'raw-body-text' })
console.log(bodyOnly.text().indexOf('raw-body-text') > -1)  // true

// ── fetch(url) with no init argument still works exactly as before ─────────
const plain = await fetch('http://127.0.0.1:8765/get')
console.log(plain.status)  // 200
