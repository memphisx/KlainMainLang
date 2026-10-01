package tests

import "testing"

// A union of several object types needs no discriminant field: each value
// identifies itself (a class instance its TagID, a plain object its layout,
// a host object its lack of a header), narrowed by instanceof, `in` or
// typeof, and a member they all declare reads before narrowing.
func TestE2EUnionOfObjectTypes(t *testing.T) {
	assertSameAsNode(t, `
class A { kindA = 1; name = "a" }
class B { name = "b"; extra = [1] }
interface P { name: string; port: number }
function describe(x: A | B | P | string): string {
  if (typeof x === "string") return "str " + x
  if (x instanceof A) return "A " + x.kindA
  if (x instanceof B) return "B " + x.extra.length
  return "P " + x.port + " " + x.name
}
console.log(describe("s"), describe(new A()), describe(new B()), describe({ name: "p", port: 80 }))
function nm(x: A | B): string { return x.name }
console.log(nm(new A()), nm(new B()))
const xs: (A | P)[] = [new A(), { name: "q", port: 1 }]
for (const x of xs) { if ("port" in x) console.log(x.port, x.name); else console.log(x.kindA, x.name) }
`)
}

// http.request takes a URL (Node's urlToHttpOptions), and res.setHeaders a
// Headers: both are unions of object types in the builtin modules.
func TestE2EHTTPRequestURLAndSetHeaders(t *testing.T) {
	assertSameAsNodeImports(t, `
import http from 'http'
const srv = http.createServer((req, res) => {
  res.setHeaders(new Headers({ "x-b": "2", "content-type": "text/plain" }))
  res.end(req.method + " " + req.url)
})
srv.listen(0, () => {
  const port = (srv.address() as { port: number }).port
  const u = new URL("http://127.0.0.1:" + port + "/p?q=1")
  const req = http.request(u, (r) => {
    let b = ""
    r.on("data", (c) => b += c)
    r.on("end", () => { console.log(r.statusCode, r.headers["x-b"], b); srv.close() })
  })
  req.end()
})
`)
}

// A user type guard (`x is A`) narrows a union local, in its branch and
// after an early return.
func TestE2EUnionTypeGuardNarrowing(t *testing.T) {
	assertSameAsNode(t, `
class A { a = 1; name = "a" }
interface P { name: string; port?: number }
function isA(x: A | P): x is A { return x instanceof A }
function f(x: string | A | P): string {
  if (typeof x === "string") return x
  if (isA(x)) return "A" + x.a
  return "P" + (x.port ?? 0)
}
console.log(f("s"), f(new A()), f({ name: "n", port: 3 }))
`)
}

// A conditional expression narrows a union local in each branch.
func TestE2EUnionNarrowingInConditional(t *testing.T) {
	assertSameAsNode(t, `
class A { kindA = 1; name = "a" }
interface P { name: string; port: number }
const xs: (A | P)[] = [new A(), { name: "q", port: 8 }]
for (const x of xs) console.log("port" in x ? x.port : x.kindA, x instanceof A ? "A:" + x.name : "P:" + x.port)
function f(v: string | number): string { return typeof v === "string" ? v.toUpperCase() : (v * 2).toFixed(1) }
console.log(f("ab"), f(2))
`)
}
