package tests

import "testing"

// Dates, collections and weak collections held in `any` (ADR-01287), each
// compared with Node's output under TZ=UTC.

func TestE2EDateThroughAny(t *testing.T) {
	assertOutput(t, `
const t = new Date(1000)
const a: any = new Date(1000), b: any = new Date(2000)
console.log(typeof a, a instanceof Date, a.getTime(), a.toISOString())
console.log(+a, b - a, a < b, a === b, a + 1)
console.log(String(a), JSON.stringify(a), JSON.stringify({ t }))
const arr: any[] = [a]
console.log(a, arr)
const back: Date = a
console.log(back.getTime(), (a as Date).getUTCSeconds())
`, `object true 1000 1970-01-01T00:00:01.000Z
1000 1000 true false Thu Jan 01 1970 00:00:01 GMT+0000 (Coordinated Universal Time)1
Thu Jan 01 1970 00:00:01 GMT+0000 (Coordinated Universal Time) "1970-01-01T00:00:01.000Z" {"t":"1970-01-01T00:00:01.000Z"}
1970-01-01T00:00:01.000Z [ 1970-01-01T00:00:01.000Z ]
1000 1`)
}

func TestE2EDateToStringAndLog(t *testing.T) {
	assertOutput(t, `
const d = new Date(0)
console.log(String(d))
console.log(d.toString() === `+"`${d}`"+`, d + '' === String(d))
console.log(d, [d], { d })
`, `Thu Jan 01 1970 00:00:00 GMT+0000 (Coordinated Universal Time)
true true
1970-01-01T00:00:00.000Z [ 1970-01-01T00:00:00.000Z ] { d: 1970-01-01T00:00:00.000Z }`)
}

func TestE2ECollectionSpread(t *testing.T) {
	assertOutput(t, `
const m = new Map([['a', 1], ['b', 2]])
const s = new Set([1, 2])
const x = [...m]
console.log(x, x[0][0], [0, ...s, 3])
console.log([...new Headers({ a: '1' })], [...new URLSearchParams('a=1&b=2')])
`, `[ [ 'a', 1 ], [ 'b', 2 ] ] a [ 0, 1, 2, 3 ]
[ [ 'a', '1' ] ] [ [ 'a', '1' ], [ 'b', '2' ] ]`)
}

func TestE2ECollectionsIterateThroughAny(t *testing.T) {
	assertOutput(t, `
function f(m: any) { console.log([...m], m.size); for (const x of m) console.log(x) }
f(new Map([['a', 1]])); f(new Set(['x'])); f(new URLSearchParams('q=1'))
const m: any = new Map<string, number>([['z', 9]])
for (const [k, v] of m) console.log(k, v)
function g() { return m }
console.log(g().has('z'), m instanceof Map, m instanceof Set)
`, `[ [ 'a', 1 ] ] 1
[ 'a', 1 ]
[ 'x' ] 1
x
[ [ 'q', '1' ] ] 1
[ 'q', '1' ]
z 9
true true false`)
}

func TestE2EWeakCollectionsThroughAny(t *testing.T) {
	assertOutput(t, `
const w: any = new WeakMap()
const k = {}
w.set(k, 2)
console.log(w.get(k), w.has(k), w instanceof WeakMap, w instanceof Map, w)
const s: any = new WeakSet()
s.add(k)
console.log(s.has(k), s)
const tw = new WeakMap<object, number>()
const a: any = 3
tw.set(k, a)
console.log(tw.get(k), tw, new WeakSet<object>())
try { tw.set(5 as any, 1) } catch (e) { console.log(String(e)) }
try { new WeakSet<object>().add('x' as any) } catch (e) { console.log(String(e)) }
`, `2 true true false WeakMap { <items unknown> }
true WeakSet { <items unknown> }
3 WeakMap { <items unknown> } WeakSet { <items unknown> }
TypeError: Invalid value used as weak map key
TypeError: Invalid value used in weak set`)
}

func TestE2EWeakRefAndDataViewInspect(t *testing.T) {
	assertOutput(t, `
const o = { a: 1 }
const r: any = new WeakRef(o)
const t = new WeakRef(o)
console.log(typeof r, r instanceof WeakRef, r.deref() === o, r, [t], { t })
const dv = new DataView(new ArrayBuffer(2))
console.log(dv)
const b: any = new DataView(new ArrayBuffer(8), 2, 4)
console.log(b.byteLength, [b])
`, `object true true WeakRef {} [ WeakRef {} ] { t: WeakRef {} }
DataView {
  [byteLength]: 2,
  [byteOffset]: 0,
  [buffer]: ArrayBuffer { [Uint8Contents]: <00 00>, [byteLength]: 2 }
}
4 [
  DataView {
    [byteLength]: 4,
    [byteOffset]: 2,
    [buffer]: ArrayBuffer {
      [Uint8Contents]: <00 00 00 00 00 00 00 00>,
      [byteLength]: 8
    }
  }
]`)
}

func TestE2EStructuredCloneBoxedCollections(t *testing.T) {
	assertOutputImports(t, `
const m: any = new Map([['k', 1]])
const c = structuredClone(m)
console.log(c, c === m, structuredClone(new Set([1, 2]) as any), structuredClone(new Map([['a', new Date(0)]]) as any))
`, `Map(1) { 'k' => 1 } false Set(2) { 1, 2 } Map(1) { 'a' => 1970-01-01T00:00:00.000Z }`)
}

func TestE2ERegExpInspectAndClone(t *testing.T) {
	assertOutputImports(t, `
const c = /a+/g
console.log(c, [c], String(c), { c })
const s: any = 'a+'
console.log(new RegExp(s, 'g'))
const v: any = /b/i
const n: any = new RegExp('x', 'm')
console.log(new RegExp(v.source, v.flags), n, [n], structuredClone(v))
`, `/a+/g [ /a+/g ] /a+/g { c: /a+/g }
/a+/g
/b/i /x/m [ /x/m ] /b/i`)
}

// Every host object and handle carries its own header word (ADR-01656):
// Object.prototype.toString, instanceof and identity through `any` answer
// as Node does, and a boxed Response reads its fields.
func TestE2EHostHeaderWordsThroughAny(t *testing.T) {
	assertSameAsNode(t, `
const xs: any[] = [
  new ArrayBuffer(4), new SharedArrayBuffer(4), new Map<string, number>().keys(),
  new Set<number>().values(), [1].entries(), new WritableStream<string>(),
  new TransformStream<string, string>(), new CompressionStream("gzip"),
  new ReadableStream<string>(), new ReadableStream<string>().getReader(),
  new WritableStream<string>().getWriter(), new WeakMap<object, number>(),
  new WeakSet<object>(), new Response("x"), new Request("http://a/"),
  new FinalizationRegistry<number>((n: number) => {}), new AbortController(),
  new Event("e"), new EventTarget(),
]
for (const x of xs) console.log(Object.prototype.toString.call(x), x === x)
const r: any = new Response("hi", { status: 201 })
console.log(r.status, r.ok, r instanceof Response, xs[15] instanceof FinalizationRegistry)
console.log(xs[5] instanceof WritableStream, xs[6] instanceof TransformStream, xs[0] instanceof ArrayBuffer)
const rs = new ReadableStream<string>()
const r1 = rs.getReader()
r1.releaseLock()
console.log(r1 === rs.getReader())
`)
}

// A Map get() miss and a RegExp exec()/match() miss are undefined/null, not
// an empty array (ADR-01655).
func TestE2EArrayAbsenceFromProducers(t *testing.T) {
	assertSameAsNode(t, `
const m = new Map<string, number[]>()
let list = m.get("a")
if (list === undefined) { list = []; m.set("a", list) }
list.push(1)
console.log(m.get("a"), m.get("a") === list, m.get("b") === undefined)
console.log(/x/.exec("abc") === null, "abc".match(/x/) === null)
const g = /\d/g
let n = 0
while (g.exec("a1b2") !== null) n++
console.log(n)
`)
}

// buf.fill(view) repeats the view's bytes; an empty view is
// ERR_INVALID_ARG_VALUE.
func TestE2EBufferFillWithTypedArray(t *testing.T) {
	assertSameAsNode(t, `
const b = Buffer.alloc(5); b.fill(new Uint8Array([1, 2])); console.log([...b])
const b3 = Buffer.alloc(4); b3.fill(new Uint16Array([0x0102]) as any); console.log([...b3])
const b5 = Buffer.alloc(4); b5.fill(new Uint8Array([1, 2, 3]), 1); console.log([...b5])
try { Buffer.alloc(3).fill(new Uint8Array(0)) } catch (e) { console.log((e as any).code, (e as Error).message) }
`)
}

// A union binding narrowed to a host member reads that member's properties
// and methods: a Buffer's byteLength, buffer and BYTES_PER_ELEMENT, a Map's
// size and get.
func TestE2EUnionNarrowedToHostMember(t *testing.T) {
	assertSameAsNode(t, `
function b(x: string | Buffer) { if (typeof x !== "string") console.log(x.byteLength, x.byteOffset, x.buffer.byteLength > 0, x.toString("hex"), x.subarray(1).length, x.readUInt8(0)) }
b(Buffer.from("ab"))
function u(x: number | Uint8Array) { if (typeof x !== "number") console.log(x.byteLength, x.BYTES_PER_ELEMENT, x.at(-1)) }
u(new Uint8Array([4, 5]))
function m(x: string | Map<string, number>) { if (typeof x !== "string") console.log(x.size, x.get("k")) }
m(new Map([["k", 1]]))
function s(x: string | Set<number>) { if (typeof x !== "string") console.log(x.size, x.has(2)) }
s(new Set([2]))
function d(x: string | Date) { if (typeof x !== "string") console.log(x.getTime()) }
d(new Date(5))
function a(x: string | ArrayBuffer) { if (typeof x !== "string") console.log(x.byteLength, x.slice(1).byteLength) }
a(new ArrayBuffer(4))
console.log(Uint16Array.BYTES_PER_ELEMENT, new Float64Array(1).BYTES_PER_ELEMENT)
`)
}

// typeof a builtin global follows its declaration (callable or constructible
// is "function"), and new Object()/Object() make a dynamic object or return
// their object argument.
func TestE2ETypeofBuiltinGlobalsAndObjectCtor(t *testing.T) {
	assertSameAsNode(t, `
console.log(typeof Function, typeof Object, typeof Number, typeof Date, typeof Promise, typeof Map, typeof Symbol, typeof Error, typeof Math, typeof JSON, typeof Reflect, typeof Proxy, typeof BigInt, typeof Array, typeof WeakMap, typeof globalThis, typeof fetch, typeof parseInt)
const a: any = new Object()
a.x = 1
const d: any = Object()
const e: any = Object(null)
const o = { k: 2 }
const f: any = Object(o)
console.log(typeof a, a.x, typeof d, typeof e, f === o, f.k, Object.keys(a))
`)
}

// Object(...) without an annotation infers its own result type: a dynamic
// object, not a number slot. Object of an any, and Object as a value, decide
// at run time.
func TestE2EObjectCtorCallInferred(t *testing.T) {
	assertSameAsNode(t, `
const a = Object()
const b = Object(null)
const c = Object({ x: 1 })
const d: any = Object([1, 2])
console.log(typeof a, typeof b, JSON.stringify(c), JSON.stringify(d))
const O: any = Object
const o = { k: 1 }
const v: any = o
const n: any = null
console.log(typeof O, O(o) === o, typeof O(null), typeof O(), Object(v) === o, typeof Object(n))
const x = globalThis
console.log(typeof x)
`)
}

// A builtin constructor is one value however it is reached: its name, a
// primitive's .constructor, an element, a binding of any. It stringifies
// with its name, and calling it as a value runs its call signature; one that
// requires new throws.
func TestE2EBuiltinConstructorValueIdentity(t *testing.T) {
	assertSameAsNode(t, `
const s: any = "x", n: any = 1, b: any = true, o: any = {}
console.log(s.constructor === String, n.constructor === Number, b.constructor === Boolean, o.constructor === Object)
console.log(String(String), String(Number), String(Boolean), String(Object), String(Symbol), String(parseInt), String(Math.max))
console.log([1, 2].map(String), ["3", "4"].map(Number), [0, 1, ""].filter(Boolean))
const S: any = String, N: any = Number, B: any = Boolean, O: any = Object
console.log(S(5), N("6"), B(0), typeof O(), O(o) === o)
console.log(String.name, Number.length, Symbol.length, s.constructor, [String], { f: Number })
const st = "x", nm = 1
console.log(st.constructor === String, nm.constructor === Number, s.constructor.name, s.constructor("z"))
const M: any = Map
try { M() } catch (e: any) { console.log(e.name, e.message) }
`)
}

// Functions of different signatures in one slot (a ternary, an array
// literal) are each called through their own signature.
func TestE2EMixedSignatureFunctionUnion(t *testing.T) {
	assertSameAsNode(t, `
const f = Math.random() > 2 ? String : Number
console.log(f("8"), typeof f("8"))
const fs = [String, Number]
console.log(fs.length, typeof fs[0], fs[0](7), fs[1]("8"))
const gs = [(x: any) => String(x), (x: any) => Number(x)]
console.log(gs[1]("8"), typeof gs[0](1))
const hs = [(x: number) => x + 1, (x: number) => x * 2]
console.log(hs.map(h => h(3)))
`)
}

// An Object annotation takes every non-nullish value, an array included.
func TestE2EObjectTypeAnnotation(t *testing.T) {
	assertSameAsNode(t, `
const b: Object = { x: 1 }
console.log(typeof b, b)
function f(o: Object): string { return o.toString() }
console.log(f([1, 2]), f({}))
`)
}

func TestE2EBuiltinConstructorStaticsThroughAny(t *testing.T) {
	assertSameAsNode(t, `
const A: any = Array
console.log(A.isArray([]), A.isArray(1), A.from([1, 2]), A.of(3, 4), A.length, typeof A.nope)
const B: any = Buffer
console.log(B.from("ab").length, B.isBuffer(B.alloc(2)), typeof B)
const N: any = Number
console.log(N.isInteger(2), N.MAX_SAFE_INTEGER)
const o: any = { k: 10 }
let n = 0
const src = () => { n++; return [1, 2] }
console.log(Array.from(src(), function (this: any, x: number, i: number) { return this.k + x + i }, o), n)
console.log(Array.from("ab", (c: string) => c + c, undefined))
const ab: any = new ArrayBuffer(8)
const s: any = "6162"
const enc: any = "hex"
const u: any = undefined
console.log(Buffer.from(ab, 2, 3).length, Buffer.from(s, enc, u).length)
const mf: any = undefined
console.log(Array.from([5, 6], mf))
`)
}

func TestE2EBufferFillNodeArguments(t *testing.T) {
	assertSameAsNode(t, `
const f: any = "6162"
const e: any = "hex"
const u: any = undefined
const n: any = 7
const b = Buffer.alloc(4)
console.log(b.fill(f, e), b.fill(f, e) === b, Buffer.alloc(3).fill(u), Buffer.alloc(2).fill(n, u), Buffer.alloc(3).fill(new Uint8Array([1, 2])))
console.log(Buffer.alloc(5).fill("ab", 1), Buffer.alloc(5).fill("ab", 1, 4), Buffer.alloc(4).fill("YWI=", "base64"), Buffer.alloc(4).fill("ab", 0, 4, "latin1"), Buffer.alloc(3).fill(true as any), Buffer.alloc(3).fill(-1), Buffer.alloc(3).fill(""))
for (const bad of [() => Buffer.alloc(3).fill(1, -1), () => Buffer.alloc(3).fill(1, 0, 9), () => Buffer.alloc(3).fill("a", "nope" as any), () => Buffer.alloc(3).fill("zz", "hex"), () => Buffer.alloc(3).fill(1, "x" as any), () => Buffer.alloc(3).fill(new Uint8Array(0)), () => Buffer.alloc(3).fill(1, 1.5)]) {
  try { bad(); console.log("no throw") } catch (err: any) { console.log(err.name, err.code, err.message) }
}
console.log(Buffer.alloc(4, "ab"), Buffer.alloc(4, f, e), Buffer.alloc(2, u))
const ba: any = Buffer.alloc
console.log(ba(2, 1))
`)
}
