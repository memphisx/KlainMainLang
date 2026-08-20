// fetch — an HTTP client backed by libcurl.
//
// Talks to a local fixture server (tools/httpbin-lite/, started by `make
// examples` before this file runs — see ADR-00096) instead of a real
// external website, so results here are fully deterministic and this file
// needs no real network access at all.
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
const r = await fetch('http://127.0.0.1:8765/get')
console.log(r.status)          // 200
console.log(r.ok)              // true
console.log(r.text().length > 0)   // true

// ── a 404 still resolves normally — .ok is what distinguishes it ───────────
const missing = await fetch('http://127.0.0.1:8765/status/404')
console.log(missing.status)    // 404
console.log(missing.ok)        // false

// ── redirects are followed automatically ────────────────────────────────────
const redirected = await fetch('http://127.0.0.1:8765/redirect-to?url=/get')
console.log(redirected.status) // 200, not 302 — the redirect was already followed

// ── .json() parses the body straight into a declared type ──────────────────
// (flat objects with primitive fields only, the same scope JSON.parse itself
// has — see examples/json/json_methods.ts)
interface Ip { origin: string }
const ipInfo: Ip = (await fetch('http://127.0.0.1:8765/ip')).json()
console.log(ipInfo.origin.length > 0)  // true — some IP address string came back

// ── awaiting the body accessors also works ─────────────────────────────────
// .text()/.json()/.arrayBuffer() are synchronous here (they return the value
// directly, not a Promise), but the instinctive `await res.text()` — how a TS
// developer reflexively writes them — is a safe no-op: awaiting a non-thenable
// is identity, and the type-directed .json() projection carries through it.
const ipRes = await fetch('http://127.0.0.1:8765/ip')
const ipText: string = await ipRes.text()
console.log(ipText.length > 0)         // true
const ipAwaited: Ip = await ipRes.json()
console.log(ipAwaited.origin.length > 0)  // true

// ── a network-level failure throws, same as any other Error ────────────────
try {
    await fetch('https://this-domain-absolutely-does-not-exist-12345.invalid/')
} catch (e) {
    console.log('caught: network failure')
}

// ── .arrayBuffer() for binary bodies (ADR-00094) ────────────────────────────
// .text()/.json() are plain (strlen-based) strings — fine for JSON/text, but
// a binary body containing an embedded null byte would read back shorter
// than its real size through them. .arrayBuffer() carries the real byte
// count instead, so it comes back whole regardless of content. The fixture
// server deliberately embeds a null byte in this response (unlike a real
// random-bytes endpoint, which would only sometimes happen to), so this
// path is exercised on every run, not just probabilistically.
const binary = await fetch('http://127.0.0.1:8765/bytes/16')
const bytes = new Uint8Array(binary.arrayBuffer())
console.log(bytes.length)  // 16 — exact, even though byte 5 is 0
