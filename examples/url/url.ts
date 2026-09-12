// URL / URLSearchParams — parsing and building request URLs, the natural
// companion to fetch() for the REST-API-client priority.
//
// URLSearchParams is backed by an ordered name/value pair list (the WHATWG
// "list of tuples", TDD-00203) — duplicate keys and cross-key insertion order
// round-trip faithfully, and the full method surface is supported.

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

// WHATWG normalization: a default port is stripped and the host is lowercased.
console.log(new URL("http://example.com:80/x").href)   // http://example.com/x
console.log(new URL("HTTP://ExAmple.COM/y").hostname)  // example.com

// Parts that don't appear in the URL come back as an empty string, not
// undefined/null — matching real URL's own "" default for an absent part.
const bare = new URL("https://example.com/path")
console.log(bare.port)    // (empty)
console.log(bare.search)  // (empty)

// A second argument is a base against which a relative URL is resolved.
console.log(new URL("/p?x=1", "http://host.com/a/b").href)  // http://host.com/p?x=1
console.log(new URL("page", "http://host.com/dir/").href)   // http://host.com/dir/page

// The WHATWG statics don't throw (unlike `new URL()`): canParse returns a
// boolean, parse returns a URL or null.
console.log(URL.canParse("https://example.com/"))  // true
console.log(URL.canParse("not a url"))              // false
const parsed = URL.parse("https://example.com/x")
console.log(parsed !== null ? parsed.href : "null")  // https://example.com/x

// JSON.stringify honors URL.prototype.toJSON() (== href).
console.log(JSON.stringify(new URL("https://example.com/")))  // "https://example.com/"

// A malformed URL throws a catchable Error rather than crashing.
try {
  new URL("not a url")
} catch (e) {
  console.log(e.message)  // Invalid URL
}

// ── component setters: re-parse and re-derive every field ───────────────────
const w = new URL("http://example.com/path")
w.username = "alice"
w.password = "secret"
console.log(w.href)      // http://alice:secret@example.com/path
w.host = "api.example.com:9090"
console.log(w.hostname)  // api.example.com
console.log(w.port)      // 9090

// ── searchParams: the URL's query string as a URLSearchParams ───────────────
// (a snapshot — mutate it, then assign url.search = params.toString() to apply)
console.log(u.searchParams.get("x"))  // 1
console.log(u.searchParams.get("y"))  // hello world (percent-decoded)
console.log(u.searchParams.has("z"))  // false

// ── URLSearchParams: the ordered multi-value model ──────────────────────────
const params = new URLSearchParams("a=1&b=2&a=3")
console.log(params.getAll("a").join(","))  // 1,3  (duplicate keys preserved)
console.log(params.get("a"))               // 1    (first value)
params.append("a", "4")
console.log(params.toString())             // a=1&b=2&a=3&a=4  (order preserved)

// toString serializes application/x-www-form-urlencoded — a space is '+'.
const q = new URLSearchParams()
q.set("query", "hello world")
q.set("page", "2")
console.log(q.toString())  // query=hello+world&page=2

// sort() is stable (by key); iteration and forEach walk the pairs in order.
const s = new URLSearchParams("c=3&a=1&b=2")
s.sort()
console.log(s.toString())  // a=1&b=2&c=3
for (const [k, v] of s) {
  console.log(k + "=" + v)  // a=1, then b=2, then c=3
}
console.log(s.size)  // 3
