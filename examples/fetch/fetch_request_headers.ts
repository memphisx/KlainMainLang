// Real Request/Headers classes (TDD-00040) — this reverses ADR-00074's
// original decision to model fetch()'s init argument as a plain object with
// no Request/Headers types; see docs/tdd/TDD-00040.md and the ADR(s) that
// implemented it.
//
// Like examples/fetch/fetch_init.ts, this talks to a local fixture server
// (tools/httpbin-lite/, started by `make examples` before this file runs —
// see ADR-00096) instead of a real external website, so this file needs no
// real network access and gives deterministic results.

// ── Headers is case-insensitive, like the real spec ─────────────────────────
const headers = new Headers()
headers.set('Content-Type', 'application/json')
console.log(headers.get('content-type'))   // application/json — lookup is case-insensitive
console.log(headers.has('CONTENT-TYPE'))   // true

// append() combines with a comma instead of overwriting, unlike set()
headers.append('X-Trace', 'a')
headers.append('X-Trace', 'b')
console.log(headers.get('x-trace'))  // a, b

// ── Request bundles a URL with method/headers/body ──────────────────────────
const req = new Request('http://127.0.0.1:8765/post', {
    method: 'POST',
    headers: headers,
    body: JSON.stringify({ hello: 'world' }),
})
console.log(req.method)       // POST
console.log(req.headers.get('content-type'))  // application/json

// fetch(request) — a single Request argument, alongside fetch(url) and
// fetch(url, init) (all three forms are still supported side by side)
const posted = await fetch(req)
console.log(posted.status)  // 200
console.log(posted.text().indexOf('"hello":"world"') > -1)  // true

// fetch(url, init)'s own init.headers field also accepts a real Headers
// instance directly, not just a plain Map<string,string>
const withHeaders = new Headers()
withHeaders.set('X-Example-Header', 'kml-value')
const r = await fetch('http://127.0.0.1:8765/headers', { headers: withHeaders })
console.log(r.text().indexOf('kml-value') > -1)  // true
