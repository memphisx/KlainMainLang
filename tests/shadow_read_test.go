package tests

import "testing"

// A value of another kind written through any into an object's field: the
// object's own statically typed reads see it, as in JavaScript (ADR-01742).

// direct reads, a fitting write, a parameter.
func TestE2EShadowedFieldStaticRead(t *testing.T) {
	src := `class P { x: number = 1; y: string = "a" }
const p = new P()
const a: any = p
a.x = "s"
console.log(p.x, typeof p.x, p.x + 1, p.y)
p.x = 5
console.log(p.x, a.x, typeof p.x)
const o = { n: 2, label: "L" }
const b: any = o
b.n = true
console.log(o.n, typeof o.n, JSON.stringify(o))
function show(q: P): string { return typeof q.x + ":" + q.x }
a.x = [1, 2]
console.log(show(p), p.x)
`
	assertSameAsNode(t, src)
	assertSameAsNodeCompatJS(t, src)
}

// this reads, compound writes, narrowing.
func TestE2EShadowedFieldInMethodsAndCompound(t *testing.T) {
	src := `class C {
  n: number = 1
  s: string = "a"
  bump(): number { this.n += 1; return this.n }
  desc(): string { return typeof this.n + ":" + this.n }
}
const c = new C()
console.log(c.bump(), c.desc())
const a: any = c
a.n = "str"
console.log(c.desc(), c.n + 1, ` + "`${c.n}`" + `, c.n === "str" as any)
c.n += 1
console.log(c.n, typeof c.n)
c.n = 10; c.n++
console.log(c.n, a.n, c.bump())
if (typeof c.n === "number") console.log(c.n * 2)
function twice(x: number): string { return typeof x + (x * 2) }
a.n = "7"
console.log(twice(c.n), (c.n as any) > "5")
a.s = 42
console.log(c.s.length, typeof c.s)
const plain = { k: 3 }
let sum = 0
for (let i = 0; i < 3; i++) sum += plain.k
console.log(sum)
`
	assertSameAsNode(t, src)
	assertSameAsNodeCompatJS(t, src)
}

// ++ and -- on a shadowed field.
func TestE2EShadowedFieldUpdate(t *testing.T) {
	src := `class C { n: number = 1 }
const c = new C()
const a: any = c
console.log(c.n++, ++c.n, c.n)
a.n = "str"
console.log(c.n++, c.n, typeof c.n)
a.n = "5"
console.log(++c.n, c.n--, c.n, typeof c.n)
`
	assertSameAsNode(t, src)
	assertSameAsNodeCompatJS(t, src)
}

// ++ on an any member converts with ToNumber.
func TestE2EAnyMemberUpdateIsNumeric(t *testing.T) {
	src := `const a: any = { n: "5", m: "x" }
console.log(a.n++, a.n, ++a.m, a.m)
let v: any = "7"
console.log(v++, v, typeof v)
`
	assertSameAsNode(t, src)
	assertSameAsNodeCompatJS(t, src)
}

// -- on an any member holding a BigInt.
func TestE2EAnyMemberUpdateBigInt(t *testing.T) {
	src := `const a: any = { y: 1n, z: "3" }
a.y--; a.z++
console.log(a.y, a.z)
`
	assertSameAsNode(t, src)
	assertSameAsNodeCompatJS(t, src)
}

// A write under a string-literal key, through a structural type, and a
// read off a call evaluated once.
func TestE2EShadowedFieldOtherWriters(t *testing.T) {
	src := `class P { x: number = 1; y: number = 2; z: number = 3 }
const p = new P()
let calls = 0
function get(): P { calls++; return p }
const a: any = p
a["y"] = "Y"
interface V { z: number | string }
const v: V = p
v.z = "Z"
a.x = "X"
console.log(p.y, p.z, get().x, calls, typeof get().y, calls)
`
	assertSameAsNode(t, src)
	assertSameAsNodeCompatJS(t, src)
}

// A private field is never written through any, and a class's own field
// declared any or a union holds what is written: neither is a shadow key.
func TestE2EShadowKeysSkipPrivateAndOwnFields(t *testing.T) {
	src := `class Buf { #data = ''; add(s: string): void { this.#data += s + '\n' } take(): string { const d = this.#data; this.#data = ''; return d } }
class Holder { #data: any; name: string | number = "n"; constructor(v: any) { this.#data = v } get data(): any { return this.#data } }
interface Named { name: string | number }
const h = new Holder(3)
const view: Named = h
view.name = 7
const b = new Buf()
b.add("x"); b.add("y")
console.log(JSON.stringify(b.take()), Object.keys(b), h.data, h.name, typeof h.name)
`
	assertSameAsNode(t, src)
	assertSameAsNodeCompatJS(t, src)
}
