package tests

import "testing"

// TDD-00233: a value of an interface or type-literal type is any object
// that has its members, in any layout.

// An interface's method signatures are members: callable on an object
// literal, a class instance and through a function | object union.
func TestE2EInterfaceMethodsCallable(t *testing.T) {
	assertSameAsNode(t, `
interface Shape { area(): number; label?(): string; name: string }
class Sq implements Shape {
  name = "sq"
  s: number
  constructor(s: number) { this.s = s }
  area(): number { return this.s * this.s }
}
function total(xs: Shape[]): number { let t = 0; for (const x of xs) t += x.area(); return t }
console.log(total([new Sq(2), new Sq(3)]))
const lit: Shape = { name: "lit", area() { return 7 } }
console.log(total([lit, new Sq(1)]), lit.label === undefined, lit)
interface Handler { handleEvent(x: number): void }
type L = ((x: number) => void) | Handler
function call(l: L, x: number): void {
  if (typeof l === "function") l(x)
  else l.handleEvent(x)
}
call((x) => console.log("fn", x), 1)
call({ handleEvent(x: number) { console.log("obj", x) } }, 2)
class H implements Handler { n = 5; handleEvent(x: number): void { console.log("class", x + this.n) } }
call(new H(), 3)
`)
}

// A class instance behind an interface is the instance itself: reads,
// writes, compound assignment, array mutation and method calls reach it,
// whatever the field order.
func TestE2EClassInstanceThroughInterface(t *testing.T) {
	assertSameAsNode(t, `
interface Counter { count: number; label: string; tags: string[]; bump(n: number): number }
class C { tags: string[] = ["t"]; label = "c"; extra = 0; count = 1; bump(n: number): number { this.count += n; return this.count } }
function touch(c: Counter): void {
  c.count = 10
  c.count += 5
  c.count++
  c.label = c.label + "!"
  c.tags.push("u")
  c.tags[0] = "T"
  console.log(c.bump(2), c.count, c.tags.length)
}
const inst = new C()
touch(inst)
console.log(inst.count, inst.label, inst.tags, inst.extra)
const lit: Counter = { count: 0, label: "l", tags: [], bump(n: number) { return n } }
touch(lit)
console.log(lit.count, lit.label, lit.tags)
const asI: Counter = inst
console.log(asI === (inst as unknown), asI.count)
interface Named { name: string; age: number }
class P { age = 41; extra = "e"; name = "kyr" }
function show(n: Named): void { console.log(n.name, n.age) }
show(new P())
`)
}

// Whole-object operations on another layout behind an interface see the
// object as it is: destructuring, spread, console.log, JSON.stringify,
// Object.keys/values/entries, for...in; and interface-to-interface passing.
func TestE2EStructuralViewWholeObject(t *testing.T) {
	assertSameAsNode(t, `
interface R { n: number; xs: number[]; s?: string; nested: { a: number } }
class K { extra = "e"; nested = { a: 1 }; xs = [1, 2]; n = 5; s = "k" }
function go(r: R): void {
  const { n, xs } = r
  const { n: m, ...rest } = r
  console.log(n, xs, m, rest.xs, "n" in r)
  const cp = { ...r }
  console.log(cp.n, cp.xs)
  console.log(Object.keys(r), Object.values(r).length)
  console.log(Object.entries(r)[0])
  for (const k in r) console.log("key", k)
  console.log(JSON.stringify(r))
  console.log(r)
}
go(new K())
interface Small { s?: string }
function small(x: Small): string { return x.s ?? "none" }
const r: R = new K()
console.log(small(r), small({}))
const mixed = { a: 1, b: "two" }
console.log(Object.values(mixed), Object.entries(mixed))
`)
}

// A class instance in an `any` prints, serializes and enumerates as itself.
func TestE2EClassInstanceInAny(t *testing.T) {
	assertSameAsNode(t, `
class P { x = 1; s = "a"; inner = { k: [1, 2] } }
const xs: any[] = [new P(), 3]
const a: any = xs[0]
console.log(a)
console.log(JSON.stringify(a), JSON.stringify(xs))
console.log(Object.keys(a))
for (const k in a) console.log(k)
console.log(xs)
`)
}

// An object whose layout differs from the slot's only in its fields' flags
// (a `boolean` field read as `readable?: boolean`), an empty literal, and an
// `opts ?? {}` default all read as the slot's type.
func TestE2EStructuralViewEqualStorageAndEmpty(t *testing.T) {
	assertSameAsNode(t, `
interface FO { error?: boolean; readable?: boolean; writable?: boolean }
function f(options: FO | ((e?: Error | null) => void)): void {
  let opts: FO = {}
  if (typeof options !== "function") opts = options
  console.log(opts.readable ?? "none", opts.writable ?? "none")
}
const reading = true, writing = false
f({ readable: reading, writable: writing })
f({ writable: false })
f(() => {})
function g(o?: FO): void { f(o ?? {}) }
g()
g({ readable: false })
`)
}

// A property added through `any` to a class instance is its own: read,
// overwritten, enumerated, printed, serialized, tested with `in`, deleted,
// and shadowing a method. The instance stays itself (identity, class name).
func TestE2EAddedPropertyOnStaticObject(t *testing.T) {
	assertSameAsNode(t, `
class P { x = 1; s = "a"; get dbl(): number { return this.x * 2 } m(): number { return this.x + 10 } }
class Q { x = 1; s = "a" }
const a: any = new P()
a.extra = 5
a.extra += 1
console.log(a.m(), a.dbl, a, Object.keys(a), a.extra, "extra" in a, JSON.stringify(a))
const q: any = new Q()
const q2 = q
q.y = [1, 2]
console.log(q, q === q2, q instanceof Q)
for (const k in q) console.log("k", k)
console.log(delete q.y, q.y, Object.keys(q))
a.m = () => 42
console.log(a.m())
function inner(): void { const b: any = new Q(); console.log(b) }
inner()
`)
}

// A frozen or non-extensible instance refuses an added property.
func TestE2EAddedPropertyRefusedWhenFrozen(t *testing.T) {
	assertOutput(t, `
class Q { x = 1 }
const f: any = Object.freeze(new Q())
try { f.z = 1 } catch (e) { console.log((e as Error).message) }
const p: any = Object.preventExtensions(new Q())
try { p.z = 1 } catch (e) { console.log((e as Error).message) }
p.x = 3
console.log(p.x, Object.isExtensible(new Q()))
`, "Cannot add property z, object is not extensible\nCannot add property z, object is not extensible\n3 true")
}

// A class instance or plain object in a union prints as itself, and a
// static object in `any` enumerates only the fields it has.
func TestE2EHeaderedObjectInUnionAndAny(t *testing.T) {
	assertSameAsNode(t, `
class P { x = 1 }
interface R { a: number; b?: string }
const xs: (P | number)[] = [new P(), 2]
const ys: (R | string | null)[] = [{ a: 1 }, "s", null]
const o: { f: P | number } = { f: new P() }
console.log(xs, ys, o)
const r: R = { a: 1 }
const x: any = r
console.log(x, JSON.stringify(x), Object.keys(x), Object.values(x), Object.entries(x), "b" in x)
for (const k in x) console.log("key", k)
const p: any = new P()
console.log(Object.values(p), Object.entries(p))
`)
}

// Properties added through `any` show through the statically typed binding
// too: console.log, JSON.stringify (pretty included) and Object.keys.
func TestE2EAddedPropertySeenFromStaticBinding(t *testing.T) {
	assertSameAsNode(t, `
class P { x = 1; s = "a" }
const p = new P()
const a: any = p
a.extra = 5
console.log(p, JSON.stringify(p), Object.keys(p), JSON.stringify({ p }, null, 2))
const r = { k: 1 }
const b: any = r
b.z = [1]
console.log(r, JSON.stringify(r), Object.keys(r))
class Q { y = 2 }
console.log(new Q(), JSON.stringify(new Q()), Object.keys(new Q()))
`)
}

// TDD-00233 Stage 3: a plain object of another layout passed where a plain
// object type is declared is the object itself — writes reach the caller's
// object, `===` holds, and arrays, unions and optional parameters of the
// type hold it too.
func TestE2EPlainObjectIsItselfBehindAnotherType(t *testing.T) {
	assertSameAsNode(t, `
interface Opts { a?: string | number; b?: number; c?: string[]; e?: string }
function touch(o: Opts): void { o.b = 42; if (o.c) o.c.push("z") }
const src = { a: "x", c: ["y"], extra: true }
touch(src)
console.log(src)
const o2 = {}
function t2(o: Opts): Opts { o.e = "set"; return o }
const back = t2(o2)
console.log(o2, back === (o2 as unknown))
interface T { cert: string; key?: string }
function useT(t: T): string { return t.cert + (t.key ?? "-") }
console.log(useT({ cert: "C", key: "K", ca: 1 } as T), useT({ cert: "c" }))
const arr: Opts[] = [src, { b: 1 }]
console.log(arr[0].c, arr[1].b, arr)
type U = Opts | string
const u: U = src
if (typeof u !== "string") console.log(u.a, u === (src as unknown))
`)
	assertSameAsNode(t, `
class K { v = 1 }
interface O { a?: string; k?: K; n?: number }
function f(o?: O): void { console.log(o?.k === undefined, !!o?.k, o?.n, o?.a) }
const src = { n: 3, other: "x", z: [1] }
f(src)
f(undefined)
`)
}

// Function members through the view: a method signature's own `this: T`,
// a class-typed parameter, `.call` with another receiver, an absent optional
// function member, and a function stored on another object.
func TestE2EFunctionMembersThroughView(t *testing.T) {
	assertSameAsNode(t, `
class H { name = "h" }
interface O { read?(this: H, size: number): void }
function use(o: O): void {
  console.log("in")
  const f = o.read
  console.log("got", typeof f)
  const h = new H()
  if (f) f.call(h, 5)
}
use({ read(this: H, size: number) { console.log("read", this.name, size) } })
const src = { junk: 1, read(this: H, size: number) { console.log("read2", this.name, size) } }
use(src)
`)
	assertSameAsNode(t, `
class H { name = "h" }
interface P { read: (this: H, size: number) => void }
interface M { read(this: H, size: number): void }
function up(o: P): void { o.read.call(new H(), 1) }
function um(o: M): void { o.read.call(new H(), 2) }
up({ read(this: H, s: number) { console.log("p", this.name, s) } })
um({ read(this: H, s: number) { console.log("m", this.name, s) } })
`)
	assertSameAsNode(t, `
class H { name = "h" }
interface M { f(h: H): string }
function um(o: M): void { console.log(o.f(new H())) }
um({ f(h: H) { return h.name } })
`)
	assertSameAsNode(t, `
class H { name = "h"; _read?: (this: H, n: number) => void; go(): void { if (this._read) this._read(4) } }
interface O { mode?: boolean; read?: (this: H, size: number) => void }
function make(o: O): H { const h = new H(); if (o.read) h._read = o.read; return h }
make({ read(n: number) { console.log("same", n) } }).go()
const src = { junk: 1, read(n: number) { console.log("foreign", n) } }
make(src).go()
`)
}

// Node options objects of another layout (extra fields) reach http.request,
// a Readable's options, and a dictionary-typed member (headers).
func TestE2EOptionsObjectOfAnotherLayout(t *testing.T) {
	assertSameAsNodeImports(t, `
import fs from 'fs'
import os from 'os'
import http from 'http'
import { Readable } from 'stream'
const srv = http.createServer((req, res) => { res.end(req.method + " " + req.headers["x-a"]) })
srv.listen(0, () => {
  const port = (srv.address() as { port: number }).port
  const opts = { hostname: "127.0.0.1", port, path: "/", method: "PUT", headers: { "x-a": "2" }, extra: 1 }
  const req = http.request(opts, (r) => { let b = ""; r.on("data", (c) => b += c); r.on("end", () => { console.log(b); srv.close() }) })
  req.end()
})
fs.writeFileSync(os.tmpdir() + "/kml_s3.txt", "hello")
const ro = { encoding: "utf8" as BufferEncoding, flag: "r", junk: 2 }
console.log(fs.readFileSync(os.tmpdir() + "/kml_s3.txt", { encoding: "utf8" }), ro.junk)
const ropts = { objectMode: true, junk: "j", read() {} }
const r = new Readable(ropts)
r.push(1); r.push(null)
r.on("data", (v) => console.log("data", v))
`)
}

// An interface the builtin declarations declare (`RequestInit`) is an
// object type in annotations: a variable, a parameter and a field.
func TestE2EAmbientInterfaceAsType(t *testing.T) {
	assertSameAsNode(t, `
const init: RequestInit = { method: "POST", keepalive: true }
function describe(i: RequestInit): string { return (i.method ?? "GET") + " " + String(i.keepalive ?? false) }
const opts = { method: "PUT", extra: 1 }
console.log(init.method, describe(init), describe(opts), describe({}))
interface Call { url: string; init?: RequestInit }
const c: Call = { url: "/x", init: { method: "DELETE" } }
console.log(c.url, c.init?.method)
`)
}
