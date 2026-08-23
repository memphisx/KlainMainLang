// URLPattern — WHATWG route matching (TDD-00100). Each component pattern
// compiles once at construction; .test/.exec match a full URL string.
//
// V1 scope: object-literal init over protocol/hostname/port/pathname/search/
// hash (an omitted component defaults to "*"); pattern grammar is literals,
// '*', ':name' and ':name?'. .exec returns a merged Map<string,string> of
// every named group (null on no match) rather than the spec's per-component
// URLPatternResult object.

// ── route-style matching with named groups ───────────────────────────────────
const pattern = new URLPattern({ pathname: "/books/:id" })
console.log(pattern.pathname)                                  // /books/:id
console.log(pattern.protocol)                                  // * (defaulted)
console.log(pattern.test("https://example.com/books/123"))     // true
console.log(pattern.test("https://example.com/authors/9"))     // false

const m = pattern.exec("https://example.com/books/123")
if (m !== null) {
  console.log(m.get("id"))                                     // 123
}
console.log(pattern.exec("https://example.com/nope") === null) // true

// ── multiple components + an optional trailing segment ───────────────────────
const api = new URLPattern({
  protocol: "https",
  hostname: "api.example.com",
  pathname: "/v1/:resource/:id?",
})
console.log(api.test("https://api.example.com/v1/users/42"))   // true
console.log(api.test("https://api.example.com/v1/users"))      // true (optional :id?)
console.log(api.test("http://api.example.com/v1/users/42"))    // false (protocol)

const g = api.exec("https://api.example.com/v1/users/42")
if (g !== null) {
  console.log(g.get("resource"))                               // users
  console.log(g.get("id"))                                     // 42
}

// ── empty pattern means "must be empty"; no init matches everything ──────────
console.log(new URLPattern({ search: "" }).test("https://e.com/a?q=1")) // false
console.log(new URLPattern({ search: "" }).test("https://e.com/a"))     // true
console.log(new URLPattern().test("https://anything.example/x#f"))      // true

// ── an invalid pattern throws a catchable TypeError at construction ──────────
try {
  const bad = new URLPattern({ pathname: "/x/{group}" })
  console.log(bad.test("https://e.com/x/y"))
} catch (e) {
  console.log(e instanceof TypeError)                          // true
}
