// fetch(url, init) — custom method, headers, and request body (ADR-00074,
// TDD-00017).
//
// Like examples/fetch/fetch.ts, this needs real network access to run —
// `make examples` will report this one as FAILED on a machine with no
// network access, which is expected, not a bug.
//
// init is any value with some subset of method: string /
// headers: Map<string,string> / body: string fields — no Request/Headers
// class exists, matching how this compiler already represents every other
// bag of string headers (http.listen's req.headers, a handler's own
// optional response headers field) as a plain Map<string,string>.

// ── a POST with a JSON body ─────────────────────────────────────────────────
const posted = await fetch('https://httpbin.org/post', {
    method: 'POST',
    body: JSON.stringify({ hello: 'world' }),
})
console.log(posted.status)                              // 200
console.log(posted.text().indexOf('"hello": "world"') > -1)  // 1 (true) — httpbin echoes the body back

// ── custom headers, sent alongside a GET ────────────────────────────────────
const headers: Map<string, string> = new Map<string, string>()
headers.set('X-Example-Header', 'kml-value')
const withHeaders = await fetch('https://httpbin.org/headers', { headers: headers })
console.log(withHeaders.text().indexOf('kml-value') > -1)  // 1 (true) — httpbin's /headers echoes every request header back

// ── an explicit method with no body still works (e.g. DELETE) ──────────────
const deleted = await fetch('https://httpbin.org/delete', { method: 'DELETE' })
console.log(deleted.status)  // 200

// ── setting a body without an explicit method sends it as POST ─────────────
// (real, well-known libcurl behavior — CURLOPT_POSTFIELDS implies POST
// unless overridden by an explicit method; confirmed directly, not assumed
// — see ADR-00074's Investigation)
const bodyOnly = await fetch('https://httpbin.org/post', { body: 'raw-body-text' })
console.log(bodyOnly.text().indexOf('raw-body-text') > -1)  // 1 (true)

// ── fetch(url) with no init argument still works exactly as before ─────────
const plain = await fetch('https://httpbin.org/get')
console.log(plain.status)  // 200
