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
srv.listen(0, '127.0.0.1', () => {
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

// A union of two classes and null, held in an array and read back through
// a conditional, narrows by instanceof to each class (a module's classes
// keep their mangled names in the checker).
func TestE2EClassUnionInstanceofNarrowing(t *testing.T) {
	assertSameAsNode(t, `
class A { a = 1; hi() { return "A" + this.a; } }
class B { b = "x"; hi() { return "B" + this.b; } }
const xs: Array<A | B | null> = [new A(), null, new B()];
for (const x of xs) {
  if (x === null) { console.log("null"); continue; }
  if (x instanceof A) console.log(x.a, x.hi()); else console.log(x.b, x.hi());
}
const first = xs.length > 0 ? xs[0] : null;
console.log(first !== null && first instanceof A ? first.a : -1);
const last = xs.length > 2 ? xs[2] : null;
console.log(last !== null && last instanceof B ? last.b : "none");
function pick(n: number): A | B { return n > 0 ? new A() : new B(); }
console.log(pick(1).hi(), pick(0).hi());
`)
}

// A conditional between instances of two classes is their union: a method
// call dispatches on the instance's class; a class and its subclass meet as
// the base.
func TestE2EConditionalClassUnionMethod(t *testing.T) {
	assertSameAsNode(t, `
class A { update(s: string) { return "A" + s; } }
class B { update(s: string) { return "B" + s; } }
function f(enc: boolean) {
  const c = enc ? new A() : new B();
  return c.update("x");
}
console.log(f(true), f(false));
class P { once(e: string) { return "P" + e; } go() { if (this instanceof Q) { console.log("q"); } return this.once("f"); } }
class Q extends P { once(e: string) { return "Q" + e; } }
const p = Math.random() > 2 ? new P() : new Q();
console.log(new P().go(), new Q().go(), p.once("z"));
`)
}

// An object literal with a computed key of a non-literal string or number
// type has an index signature (tsc's typing), and reads, writes and
// enumerates as Node does.
func TestE2EComputedKeyIndexSignature(t *testing.T) {
	assertSameAsNode(t, `
let v = 'v';
const k = 'lit';
let n = 3;
const o = { [v]: 1, f() { return 2; } };
const p = { [k]: true, a: 1 };
const q = { [n]: "s", 2: 5, b: 1 };
const g = () => o;
g()[v] = 7;
console.log(o[v], o.f(), p.lit, p.a, q[3], q[2], q.b, Object.keys(o), Object.keys(q));
`)
}

// A conditional between object literals is tsc's normalized union: a
// property one branch lacks reads undefined.
func TestE2EConditionalObjectLiteralUnion(t *testing.T) {
	assertSameAsNode(t, `
function g(c: boolean) { return c ? { a: 1 } : {}; }
function h(c: boolean) { return c ? { a: 1 } : { b: "x" }; }
function f(options?: { id?: number; state?: string }) {
  const o = options !== undefined ? options : {};
  return (o.state || 'none') + ':' + ((o.id ?? 0) | 0);
}
console.log(g(true).a, g(false).a, h(true).a, h(true).b, h(false).a, h(false).b);
console.log(f(), f({ id: 4 }), f({ state: 'up' }));
`)
	// A member both shapes declare with different kinds (b?: undefined,
	// b: string) reads the value the object holds.
	assertSameAsNode(t, `
type A = { a: number; b?: undefined };
type B = { b: string; a?: undefined };
type U = A | B;
function mk(): B { return { b: "q" }; }
function k(c: boolean): U { if (c) return { a: 1 }; return { b: "x" }; }
const y: U = mk();
const z: U = { b: "r" };
console.log(y.b, y.a, z.b, k(false).b, k(true).a, k(true).b);
`)
}
