// URLPattern — WHATWG route matching, the global Node has. A pattern is an
// init object or a constructor string; .test/.exec match a URL string or an
// init object, optionally against a base URL. .exec returns the standard's
// URLPatternResult: the inputs, and per component its input and groups.

// ── route-style matching with named groups ───────────────────────────────────
const pattern = new URLPattern({ pathname: "/books/:id" })
console.log(pattern.pathname)                                  // /books/:id
console.log(pattern.protocol)                                  // * (defaulted)
console.log(pattern.test("https://example.com/books/123"))     // true
console.log(pattern.test("https://example.com/authors/9"))     // false

const m = pattern.exec("https://example.com/books/123")
if (m !== null) {
  console.log(m.pathname.groups.id)                            // 123
  console.log(m.hostname.input)                                // example.com
}
console.log(pattern.exec("https://example.com/nope") === null) // true

// ── a constructor string, an optional segment and a regexp group ─────────────
const api = new URLPattern("https://api.example.com/v1/:resource/:id(\\d+)?")
console.log(api.test("https://api.example.com/v1/users/42"))   // true
console.log(api.test("https://api.example.com/v1/users"))      // true (optional :id)
console.log(api.test("https://api.example.com/v1/users/x"))    // false (\d+)
console.log(api.hasRegExpGroups)                               // true

const g = api.exec("https://api.example.com/v1/users/42")
if (g !== null) {
  console.log(g.pathname.groups.resource, g.pathname.groups.id) // users 42
}

// ── relative patterns and inputs against a base URL ──────────────────────────
const rel = new URLPattern("/files/*", "https://cdn.example.com")
console.log(rel.test("/files/a/b.png", "https://cdn.example.com")) // true
console.log(rel.exec("https://cdn.example.com/files/x.js")!.pathname.groups[0]) // x.js

// ── username/password components match a URL's userinfo ──────────────────────
const authP = new URLPattern({ username: "admin", pathname: "/books/:id" })
console.log(authP.test("http://admin@example.com/books/5"))   // true
console.log(authP.test("http://guest@example.com/books/5"))   // false

// ── empty pattern means "must be empty"; no init matches everything ──────────
console.log(new URLPattern({ search: "" }).test("https://e.com/a?q=1")) // false
console.log(new URLPattern({ search: "" }).test("https://e.com/a"))     // true
console.log(new URLPattern().test("https://anything.example/x#f"))      // true

// ── an invalid pattern throws a TypeError at construction ────────────────────
try {
  new URLPattern({ pathname: "/x/(" })
} catch (e) {
  console.log(e instanceof TypeError)                          // true
}
