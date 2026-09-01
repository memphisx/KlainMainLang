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

// A second argument is a base against which a relative URL is resolved
// (curl's own relative resolution) — an absolute first argument overwrites
// the base, an invalid base throws.
console.log(new URL("/p?x=1", "http://host.com/a/b").href)  // http://host.com/p?x=1
console.log(new URL("page", "http://host.com/dir/").href)   // http://host.com/dir/page
console.log(new URL("../up", "http://host.com/a/b/c").pathname)  // /a/up

// A malformed URL throws a catchable Error rather than crashing.
try {
  new URL("not a url")
} catch (e) {
  console.log(e.message)  // Invalid URL
}

// ── component setters: re-parse and re-derive every field ───────────────────
// Every settable component (href/protocol/host/hostname/port/pathname/search/
// hash/username/password) re-parses the URL under the hood, so the derived
// fields — and href/origin — never desync.
const w = new URL("http://example.com/path")
w.username = "alice"
w.password = "secret"
console.log(w.username)  // alice
console.log(w.href)      // http://alice:secret@example.com/path

// The combined `host` splits on the first ':' into hostname + port; a value
// with no port keeps the existing one (matching Node).
w.host = "api.example.com:9090"
console.log(w.hostname)  // api.example.com
console.log(w.port)      // 9090
w.host = "cdn.example.com"
console.log(w.host)      // cdn.example.com:9090 (port preserved)

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
