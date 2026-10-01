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
