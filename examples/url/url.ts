// URL / URLSearchParams — parsing and building request URLs, the natural
// companion to fetch() for the REST-API-client priority.
//
// URLSearchParams is backed by a single-value-per-key Map<string,string>
// (the same simplification http.listen's own req.query already makes) —
// not a true multi-value store, so a repeated query-string key keeps only
// its last value, and .getAll() never returns more than one element.

// ── parsing a URL into its parts ─────────────────────────────────────────────
const u = new URL("https://example.com:8080/a/b?x=1&y=hello%20world#frag")
console.log(u.href)      // https://example.com:8080/a/b?x=1&y=hello%20world#frag
console.log(u.protocol)  // https:
console.log(u.hostname)  // example.com
console.log(u.host)      // example.com:8080
console.log(u.port)      // 8080
console.log(u.pathname)  // /a/b
console.log(u.search)    // ?x=1&y=hello%20world
console.log(u.hash)      // #frag
console.log(u.origin)    // https://example.com:8080

// Parts that don't appear in the URL come back as an empty string, not
// undefined/null (this compiler has no optional-field concept for a plain
// object type) — matching real URL's own "" default for an absent part.
const bare = new URL("https://example.com/path")
console.log(bare.port)    // (empty)
console.log(bare.search)  // (empty)
console.log(bare.hash)    // (empty)

// A malformed URL throws a catchable Error rather than crashing.
try {
  new URL("not a url")
} catch (e) {
  console.log(e.message)  // Invalid URL
}

// ── searchParams: URL's own query string as a live URLSearchParams ──────────
console.log(u.searchParams.get("x"))       // 1
console.log(u.searchParams.get("y"))       // hello world (percent-decoded)
console.log(u.searchParams.has("z"))       // 0 (false — not present)
console.log(u.searchParams.get("z"))       // null

// ── URLSearchParams on its own, for building a query string ─────────────────
const params = new URLSearchParams()
params.set("q", "hello world")
params.set("page", "2")
console.log(params.toString())  // q=hello%20world&page=2

// Parsing an existing query string tolerates a leading '?' (so passing a
// URL's own .search straight through just works).
const parsed = new URLSearchParams(u.search)
console.log(parsed.get("x"))  // 1

// get/set/has/delete/size/keys()/values()/entries()/forEach() all come for
// free from the underlying Map<string,string> — no separate API surface.
console.log(parsed.size)
for (const key of parsed.keys()) {
  console.log(key)
}
