package tests

import "testing"

// An `any` argument reaches a typed parameter as the value it holds: a
// call checks the box's kind and runs the function's any-argument entry
// when it does not fit, so the callee's `typeof` and validation see it.
func TestE2EAnyArgumentKeepsItsKind(t *testing.T) {
	assertSameAsNode(t, `import * as path from "node:path"
function f(s: string): string { return typeof s + ":" + s }
function g(n: number): string { return typeof n + ":" + n }
function r(...xs: string[]): string { return xs.map(x => typeof x).join(",") }
const a: any = 5, b: any = "7", c: any = { k: 1 }
console.log(f(a), g(b), f(c), r(a, b))
try { path.join(a) } catch (e: any) { console.log(e.code) }
`)
	assertSameAsNode(t, `function r(...xs: string[]): string { const y = xs[0]; let o = ""; for (const x of xs) o += typeof x; return typeof xs[0] + typeof y + o + xs.map(x => typeof x).join(",") }
const a: any = 5
console.log(r(a, "s"))
`)
	assertSameAsNode(t, `import * as path from "node:path"
function dbl(n: number): number { return n * 2 }
function up(s: string, k: number = 1): string { return s.toUpperCase() + ":" + typeof s + k }
function len(s: string): number { return s.length }
function check(name: string): string { if (typeof name !== "string") throw new TypeError("bad " + typeof name); return name }
function fact(n: number): number { return n <= 1 ? 1 : n * fact(n - 1) }
function chain(s: string): string { return up(s) }
function flag(b: boolean): string { return b ? "yes" : "no" + typeof b }
const five: any = 5, str: any = "abc", arr: any = [1, 2, 3], nul: any = null, t: any = true
console.log(dbl(five), dbl(str), up(str), up(str, five))
console.log(len(str), len(arr), fact(five))
for (const v of [five, str, nul, arr, t]) { try { console.log(check(v)) } catch (e: any) { console.log(e.message) } }
try { console.log(up(five)) } catch (e: any) { console.log(e.constructor.name) }
console.log(chain(str), flag(t), flag(five), flag(str))
for (const v of [five, "x", nul]) { try { console.log(path.join(v, "y")) } catch (e: any) { console.log(e.code) } }
try { path.basename(five) } catch (e: any) { console.log(e.code) }
`)
}

// An `any` argument reaches a typed parameter as the value it holds: a
// call checks the box's kind and runs the function's any-argument entry
// when it does not fit, so the callee's `typeof` and validation see it.
func TestE2EAnyArgumentKeepsItsKindJS(t *testing.T) {
	assertSameAsNodeCompatJS(t, `import * as path from "node:path"
function f(s: string): string { return typeof s + ":" + s }
function g(n: number): string { return typeof n + ":" + n }
function r(...xs: string[]): string { return xs.map(x => typeof x).join(",") }
const a: any = 5, b: any = "7", c: any = { k: 1 }
console.log(f(a), g(b), f(c), r(a, b))
try { path.join(a) } catch (e: any) { console.log(e.code) }
`)
	assertSameAsNodeCompatJS(t, `function r(...xs: string[]): string { const y = xs[0]; let o = ""; for (const x of xs) o += typeof x; return typeof xs[0] + typeof y + o + xs.map(x => typeof x).join(",") }
const a: any = 5
console.log(r(a, "s"))
`)
	assertSameAsNodeCompatJS(t, `import * as path from "node:path"
function dbl(n: number): number { return n * 2 }
function up(s: string, k: number = 1): string { return s.toUpperCase() + ":" + typeof s + k }
function len(s: string): number { return s.length }
function check(name: string): string { if (typeof name !== "string") throw new TypeError("bad " + typeof name); return name }
function fact(n: number): number { return n <= 1 ? 1 : n * fact(n - 1) }
function chain(s: string): string { return up(s) }
function flag(b: boolean): string { return b ? "yes" : "no" + typeof b }
const five: any = 5, str: any = "abc", arr: any = [1, 2, 3], nul: any = null, t: any = true
console.log(dbl(five), dbl(str), up(str), up(str, five))
console.log(len(str), len(arr), fact(five))
for (const v of [five, str, nul, arr, t]) { try { console.log(check(v)) } catch (e: any) { console.log(e.message) } }
try { console.log(up(five)) } catch (e: any) { console.log(e.constructor.name) }
console.log(chain(str), flag(t), flag(five), flag(str))
for (const v of [five, "x", nul]) { try { console.log(path.join(v, "y")) } catch (e: any) { console.log(e.code) } }
try { path.basename(five) } catch (e: any) { console.log(e.code) }
`)
}

// A call whose any-argument entry runs returns what the body returns, a
// parameter of another kind included.
func TestE2EAnyArgumentReturnsItsValue(t *testing.T) {
	assertSameAsNode(t, `function id(s: string): string { return s }
function twice(n: number): number { return n + n }
function name(x: string): string { return "<" + x + ">" }
const five: any = 5, str: any = "ab"
const a = id(five)
const b: string = id(five)
console.log(typeof a, a, typeof b, b, id(five), typeof id(str), id(str).length)
console.log(twice(str), twice(five), typeof twice(str), name(five))
let x: string = "q"
x = id(five)
console.log(typeof x, x, id("lit").toUpperCase())
console.log([id(five), twice(str)].map(v => typeof v).join())
`)
	assertSameAsNode(t, `function t4(n: number): number { const r = n + n; return r }
function t5(n: number): number { return n }
function t6(n: number): number { return n * 1 }
const str: any = "ab"
console.log(t4(str), t5(str), t6(str))
`)
}

// A call whose any-argument entry runs returns what the body returns, a
// parameter of another kind included.
func TestE2EAnyArgumentReturnsItsValueJS(t *testing.T) {
	assertSameAsNodeCompatJS(t, `function id(s: string): string { return s }
function twice(n: number): number { return n + n }
function name(x: string): string { return "<" + x + ">" }
const five: any = 5, str: any = "ab"
const a = id(five)
const b: string = id(five)
console.log(typeof a, a, typeof b, b, id(five), typeof id(str), id(str).length)
console.log(twice(str), twice(five), typeof twice(str), name(five))
let x: string = "q"
x = id(five)
console.log(typeof x, x, id("lit").toUpperCase())
console.log([id(five), twice(str)].map(v => typeof v).join())
`)
	assertSameAsNodeCompatJS(t, `function t4(n: number): number { const r = n + n; return r }
function t5(n: number): number { return n }
function t6(n: number): number { return n * 1 }
const str: any = "ab"
console.log(t4(str), t5(str), t6(str))
`)
}

// A binding annotated with a primitive or typed-array type, given an `any`
// by its initializer or an assignment, holds the value as it is.
func TestE2EAnnotatedBindingHoldsAnyValue(t *testing.T) {
	assertSameAsNode(t, `const u16: any = new Uint16Array([300, 2, 65535])
const c: Uint8Array = u16
console.log(c.length, c[0], c[2], c.constructor.name, c instanceof Uint16Array)
const s: any = "s", five: any = 5
const n: number = s
const t: string = five
console.log(typeof n, n, typeof t, t, `+"`"+`${n}${t}`+"`"+`, n === "s" as any)
function take(x: number): string { return typeof x }
console.log(take(n), take(t as any))
let m: number = five
m = m + 1
console.log(m, typeof m)
const ok: number = five
console.log(ok * 2, ok.toFixed(1))
`)
	assertSameAsNode(t, `const s: any = "s", five: any = 5
let n: number = 0
n = s
console.log(typeof n, n)
let q: string
q = five
console.log(typeof q, q)
let k: number = 1
k += s
console.log(typeof k, k)
let plain: number = 3
plain = plain * 2
console.log(plain)
`)
}

// A binding annotated with a primitive or typed-array type, given an `any`
// by its initializer or an assignment, holds the value as it is.
func TestE2EAnnotatedBindingHoldsAnyValueJS(t *testing.T) {
	assertSameAsNodeCompatJS(t, `const u16: any = new Uint16Array([300, 2, 65535])
const c: Uint8Array = u16
console.log(c.length, c[0], c[2], c.constructor.name, c instanceof Uint16Array)
const s: any = "s", five: any = 5
const n: number = s
const t: string = five
console.log(typeof n, n, typeof t, t, `+"`"+`${n}${t}`+"`"+`, n === "s" as any)
function take(x: number): string { return typeof x }
console.log(take(n), take(t as any))
let m: number = five
m = m + 1
console.log(m, typeof m)
const ok: number = five
console.log(ok * 2, ok.toFixed(1))
`)
	assertSameAsNodeCompatJS(t, `const s: any = "s", five: any = 5
let n: number = 0
n = s
console.log(typeof n, n)
let q: string
q = five
console.log(typeof q, q)
let k: number = 1
k += s
console.log(typeof k, k)
let plain: number = 3
plain = plain * 2
console.log(plain)
`)
}

// A field shadowed by a value of another kind written through `any` is
// itself again once a fitting value is written, through `any` or the
// typed binding.
func TestE2EShadowDroppedByFittingWrite(t *testing.T) {
	assertSameAsNode(t, `const o = { x: 1, y: "a" }
const d: any = o
d.x = "s"
console.log(d.x, JSON.stringify(o))
d.x = 5
console.log(d.x, JSON.stringify(o), Object.keys(o))
`)
	assertSameAsNode(t, `const o = { x: 1, y: "a" }
const d: any = o
d.x = "s"
console.log(d.x, JSON.stringify(o))
o.x = 5
console.log(d.x, JSON.stringify(o), Object.keys(o))
`)
}

// A field shadowed by a value of another kind written through `any` is
// itself again once a fitting value is written, through `any` or the
// typed binding.
func TestE2EShadowDroppedByFittingWriteJS(t *testing.T) {
	assertSameAsNodeCompatJS(t, `const o = { x: 1, y: "a" }
const d: any = o
d.x = "s"
console.log(d.x, JSON.stringify(o))
d.x = 5
console.log(d.x, JSON.stringify(o), Object.keys(o))
`)
	assertSameAsNodeCompatJS(t, `const o = { x: 1, y: "a" }
const d: any = o
d.x = "s"
console.log(d.x, JSON.stringify(o))
o.x = 5
console.log(d.x, JSON.stringify(o), Object.keys(o))
`)
}

func TestE2EAnyArgumentArrayParameter(t *testing.T) {
	src := `function len(a: number[]): string { return typeof a + ":" + a.length + ":" + Array.isArray(a) }
function first(a: string[]): string { return typeof a[0] }
function push(a: number[]): void { a.push(9) }
function bytes(u: Uint8Array): string { return u.constructor.name + ":" + u[0] }
const s: any = "abc", n: any = 5, nums: any = [1, 2], strs: any = ["x"], mixed: any = JSON.parse('[1,"a"]')
const u16: any = new Uint16Array([300])
console.log(len(s), len(n), len(nums), len(mixed))
console.log(first(nums), first(strs), first(mixed))
push(nums); push(mixed)
console.log(nums, mixed)
console.log(bytes(u16), bytes(new Uint8Array([7]) as any))
const held: number[] = mixed
console.log(held, typeof held[1])
`
	assertSameAsNode(t, src)
	assertSameAsNodeCompatJS(t, src)
}

func TestE2EBigIntTypedArrayFromAny(t *testing.T) {
	src := `const n: any = 3, arr: any = [1n, 2n, -1n], bad: any = [1n, 2], strs: any = ["5", "6"]
const ab: any = new ArrayBuffer(16)
console.log(new BigInt64Array(n), new BigUint64Array(arr), new BigInt64Array(ab).length, new BigInt64Array(strs))
try { new BigInt64Array(bad) } catch (e: any) { console.log(e.name, e.message) }
`
	assertSameAsNode(t, src)
	assertSameAsNodeCompatJS(t, src)
}

func TestE2EBufferConcatOfAnyHeldElement(t *testing.T) {
	src := `const a: any = Buffer.from("ab"), s: any = "x"
const b: Buffer = a
console.log(Buffer.concat([b, Buffer.from("c")]).toString())
try { const c: Buffer = s; Buffer.concat([c]) } catch (e: any) { console.log(e.code) }
try { const c: Buffer = s; Buffer.concat([c]) } catch (e: any) { console.log(e.message) }
`
	assertSameAsNode(t, src)
	assertSameAsNodeCompatJS(t, src)
}

// A call returning any, bound to an interface-typed binding: the object as
// it is, a member it lacks read undefined.
func TestE2EInterfaceBindingFromAnyCall(t *testing.T) {
	src := `interface I { readonly a: number; readonly b: number }
function mk(): any { const c: any = { a: 1 }; if (process.argv.length > 99) c.b = 2; return c }
export const k: I = mk()
function show(): void { console.log(k.a, k.b, typeof k.b, Object.keys(k)) }
show()
`
	assertSameAsNode(t, src)
	assertSameAsNodeCompatJS(t, src)
}

// An instance of a class extending Array, held in any, at an array
// parameter: the instance, whose override a call in the body runs.
func TestE2EAnyArgumentArraySubclassInstance(t *testing.T) {
	src := `class Args extends Array<string> {
  slice(start?: number, end?: number): string[] { console.log("override"); return super.slice(start, end) }
}
function f(p: string[]): number { return p.slice().length + (p instanceof Args ? 100 : 0) }
const a = new Args(); a.push("x")
const b: any = a
console.log(f(a), f(b))
`
	assertSameAsNode(t, src)
	assertSameAsNodeCompatJS(t, src)
}

// In JavaScript a name nothing declares is read at run time (a
// ReferenceError unless the host defines it); what is built from it is
// `any`, as tsc types it.
func TestE2EJSUndeclaredGlobal(t *testing.T) {
	assertSameAsNodeCompatJS(t, `
try { console.log(typeof notDeclared, notDeclared.x); } catch (e) { console.log(e instanceof ReferenceError, e.message); }
let f = function () { return undeclaredFn(); };
try { f(); } catch (e) { console.log(e.name); }
let g = function () { let r = host.make(); let C = r(); return new C(); };
try { g(); } catch (e) { console.log(e.name); }
`)
}

// A sloppy script's assignment to a parameter or a catch binding stores to
// that binding; one to a name no scope declares makes a global property.
func TestE2EJSSloppyAssignTargets(t *testing.T) {
	assertMultiFileOutputPermissive(t, map[string]string{"main.js": `
function f(a, b) { a = 1; b += 2; try { throw 1 } catch (e) { e = 3 } return a + b }
function g() { zz = 5 }
g(); console.log(f(0, 1), globalThis.zz, zz)
`}, "main.js", "4 5 5")
}

// JavaScript: a class called without `new` throws once its arguments are
// evaluated; a method the receiver's type has no such member for reads
// undefined, so its call throws, named as V8 names the callee.
func TestE2EJSCallThrowsAtRunTime(t *testing.T) {
	assertMultiFileOutputPermissive(t, map[string]string{"main.js": `
class C { constructor(m) { this.m = m } }
function f(x) { if (x) throw C("boom"); return 1 }
console.log(f(0));
try { f(1) } catch (e) { console.log(e instanceof TypeError, e.message) }
var m = new ArrayBuffer(2);
try { m.transferToImmutable(1) } catch (e) { console.log(e.constructor.name + ": " + e.message) }
try { (new ArrayBuffer(2)).transferToImmutable() } catch (e) { console.log(e.message) }
try { "s".nope() } catch (e) { console.log(e.message) }
`}, "main.js", `1
true Class constructor C cannot be invoked without 'new'
TypeError: m.transferToImmutable is not a function
(intermediate value).transferToImmutable is not a function
"s".nope is not a function`)
}

// JavaScript: a top-level binding no static type fits (an array of builtin
// constructors grown by concat) is read from a function; a JSDoc-optional
// parameter left out is undefined.
func TestE2EJSUntypedGlobalsAndJSDocOptional(t *testing.T) {
	assertMultiFileOutputPermissive(t, map[string]string{"main.js": `
var a = [Float32Array, Float64Array];
var c = a.concat([Int8Array]);
function t(f, cs) { var ctors = cs || c; ctors.forEach(function (C) { f(C) }); }
t(function (C) { console.log(C.name) });
/**
 * @param {string[]} [xs] - optional
 */
function opt(xs) { if (xs) return "truthy " + xs.length; return "undef " + xs; }
console.log(opt(), opt(["a"]));
`}, "main.js", `Float32Array
Float64Array
Int8Array
undef undefined truthy 1`)
}

// Number(x) and -x of an `any` holding a bigint are ToNumeric: the nearest
// double, and a negated bigint.
func TestE2EAnyBigIntNumberAndNegate(t *testing.T) {
	assertSameAsNode(t, `
const p: any = 3n, q: any = 1n;
console.log(Number(p), -p, -q, p - q, Number(p - q));
const a = new BigInt64Array([3n, 1n, 2n]);
console.log(a.toSorted((x, y) => Number(x - y)), [5n].map((z) => Number(z)));
`)
}
