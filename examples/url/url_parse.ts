// Legacy `url.parse` (Node's `url` module) — importable named, via namespace,
// or as the default export. Returns the legacy `Url` object (protocol/auth/
// host/port/hostname/hash/search/query/pathname/path/href), distinct from the
// WHATWG `URL`. See TDD-00165 Stage 4.
import { parse } from 'url'

const u = parse("https://user:pw@example.com:8080/a/b?x=1&y=2#frag")
console.log("protocol:", u.protocol) // https:
console.log("auth:", u.auth)         // user:pw
console.log("host:", u.host)         // example.com:8080
console.log("hostname:", u.hostname) // example.com
console.log("port:", u.port)         // 8080
console.log("pathname:", u.pathname) // /a/b
console.log("search:", u.search)     // ?x=1&y=2
console.log("query:", u.query)       // x=1&y=2
console.log("hash:", u.hash)         // #frag
console.log("path:", u.path)         // /a/b?x=1&y=2

// url.format is the inverse — reconstructs the URL string from the Url object.
import { format } from 'url'
console.log("round-trip:", format(u))

// The other legacy helpers: resolve (base-relative), urlToHttpOptions, and
// domainToASCII (Punycode/IDN — ASCII domains pass through everywhere).
import { resolve, urlToHttpOptions, domainToASCII } from 'url'
console.log("resolve:", resolve("http://a.com/x/", "../y"))
console.log("httpOptions.path:", urlToHttpOptions(new URL("https://h/p?a=1")).path)
console.log("domainToASCII:", domainToASCII("example.com"))
