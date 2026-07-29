// fetch — an HTTP client backed by libcurl.
//
// Unlike every other example in this repo, this one needs real network
// access to run (it talks to httpbin.org, a stable public HTTP testing
// service) — `make examples` will report this one as FAILED on a machine
// with no network access, which is expected, not a bug.
//
// This file covers plain GET requests only. fetch() is genuinely
// non-blocking (ADR-00050) — awaited from inside an http.listen handler, it
// yields to this compiler's event loop instead of blocking the whole
// process, so two fetches issued before either is awaited really do run
// concurrently (see examples/http/http_server.ts and
// tests/http_test.go's TestE2EHTTPListenConcurrentAwaitFetch). Custom
// method/headers/request body (ADR-00074) are covered separately in
// examples/fetch/fetch_init.ts.

// ── status, ok, and the raw body text ───────────────────────────────────────
const r = await fetch('https://httpbin.org/get')
console.log(r.status)          // 200
console.log(r.ok)              // 1 (true)
console.log(r.text().length > 0)   // 1 (true) — httpbin echoes back a JSON blob

// ── a 404 still resolves normally — .ok is what distinguishes it ───────────
const missing = await fetch('https://httpbin.org/status/404')
console.log(missing.status)    // 404
console.log(missing.ok)        // 0 (false)

// ── redirects are followed automatically ────────────────────────────────────
const redirected = await fetch('https://httpbin.org/redirect-to?url=/get')
console.log(redirected.status) // 200, not 302 — the redirect was already followed

// ── .json() parses the body straight into a declared type ──────────────────
// (flat objects with primitive fields only, the same scope JSON.parse itself
// has — see examples/json/json_methods.ts)
interface Ip { origin: string }
const ipInfo: Ip = (await fetch('https://httpbin.org/ip')).json()
console.log(ipInfo.origin.length > 0)  // 1 (true) — some IP address string came back

// ── a network-level failure throws, same as any other Error ────────────────
try {
    await fetch('https://this-domain-absolutely-does-not-exist-12345.invalid/')
} catch (e) {
    console.log('caught: network failure')
}
