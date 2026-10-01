package tests

import "testing"

// A function converts as an object: its own Symbol.toPrimitive, valueOf and
// toString (a function's own properties) take part, with the operator's
// hint; `+` on a string passes "default".
func TestE2EFunctionOwnToPrimitive(t *testing.T) {
	assertSameAsNode(t, `
function g() { return 1 }
(g as any)[Symbol.toPrimitive] = (h: string) => "g:" + h
console.log(`+"`${g}`"+`, g + "", "" + g, String(g), +g)
function h() { return 2 }
(h as any).valueOf = () => 42
console.log(+h, Number(h))
const f: any = () => 1
f.toString = () => "ts"
console.log(`+"`${f}`"+`, String(f), f + "")
`)
}

// A well-known symbol set at run time keys the same member a literal's
// [Symbol.x] does, and is a symbol key: hidden from Object.keys, for...in
// and JSON, shown by inspect.
func TestE2EWellKnownSymbolKeys(t *testing.T) {
	assertSameAsNode(t, `
const o: any = { a: 1 }
o[Symbol.toPrimitive] = (h: string) => "prim:" + h
console.log(`+"`${o}`"+`, o + "", Object.keys(o), JSON.stringify(o), Symbol.toPrimitive in o)
for (const k in o) console.log("key", k)
console.log(o)
const p: any = { [Symbol.toPrimitive](h: string) { return h }, b: 2 }
console.log(Object.keys(p), p, `+"`${p}`"+`, p + "")
`)
}

// Computed-key methods in an object literal: generator, async and plain.
func TestE2EObjectLiteralComputedMethods(t *testing.T) {
	assertSameAsNode(t, `
const it = { *[Symbol.iterator]() { yield 1; yield 2 } }
for (const x of it) console.log(x)
const ai = { async *[Symbol.asyncIterator]() { yield "a"; yield "b" } }
for await (const x of ai) console.log(x)
const k = "dyn"
const o: any = { [k]() { return 5 }, async [k + "2"]() { return 7 } }
console.log(o.dyn(), await o.dyn2(), Object.keys(o))
`)
}

// A class instance converts through its own [Symbol.toPrimitive], valueOf
// and toString, with the operator's hint; without them it is
// Object.prototype.toString's [object Object].
func TestE2EClassToPrimitiveLadder(t *testing.T) {
	assertSameAsNode(t, `
class Q { n = 1; valueOf() { return 3 } toString() { return "q" } }
const q = new Q()
console.log(`+"`${q}`"+`, +q, q + "", String(q), "" + q)
class P { [Symbol.toPrimitive](h: string) { return h === "number" ? 5 : "p:" + h } }
const p = new P()
console.log(`+"`${p}`"+`, +p, p + "", String(p))
class V { valueOf() { return 7 } }
const v = new V()
console.log(+v, v + "", `+"`${v}`"+`, String(v))
const lit = { valueOf() { return 5 } }
console.log(lit + "", `+"`${lit}`"+`, +lit)
class E { valueOf(): void {} }
console.log(+new E(), new E() + "")
`)
}

// Number.isInteger/isSafeInteger/isNaN/isFinite answer for a number only;
// the global isNaN/isFinite convert. Through `any` too.
func TestE2ENumberPredicatesOnAny(t *testing.T) {
	assertSameAsNode(t, `
const a: any = 1.5, b: any = 4, s: any = "5", n: any = NaN, i: any = Infinity, big: any = 2 ** 60, u: any = undefined
const vals = [a, b, s, n, i, big, u, null, true, "x"]
for (const v of vals) console.log(Number.isInteger(v), Number.isSafeInteger(v), Number.isNaN(v), Number.isFinite(v), isNaN(v), isFinite(v))
console.log(Number.isInteger(5), Number.isInteger(5.5), Number.isSafeInteger(3), Number.isNaN(NaN), Number.isFinite(1 / 0))
`)
}
