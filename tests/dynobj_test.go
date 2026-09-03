package tests

import (
	"testing"
)

// --- D1 dynamic object model, Stage 1 (TDD-00155): an object literal in an
// any-typed slot becomes a runtime property bag — member/bracket get & set,
// delete, `in`, Object.keys, for...in, spread, identity, nesting ---

func TestE2EDynObjLiteralAndMemberRead(t *testing.T) {
	assertOutput(t, `
let o: any = { a: 1, b: "hi", c: true, f: 3.5 }
console.log(o.a)
console.log(o.b)
console.log(o.c)
console.log(o.f)
console.log(o.missing)
`, "1\nhi\ntrue\n3.5\nundefined")
}

func TestE2EDynObjPropertyAddAndUpdate(t *testing.T) {
	assertOutput(t, `
let o: any = { a: 1 }
o.b = "new"
o.a = 2
o["computed" + "Key"] = 42
console.log(o.a, o.b, o.computedKey)
`, "2 new 42")
}

func TestE2EDynObjBracketRuntimeKey(t *testing.T) {
	assertOutput(t, `
let o: any = { k1: "one", k2: "two" }
for (let i = 1; i <= 2; i++) {
  console.log(o["k" + i])
}
o[1] = "numkey"
console.log(o["1"])
`, "one\ntwo\nnumkey")
}

func TestE2EDynObjDeleteAndIn(t *testing.T) {
	assertOutput(t, `
let o: any = { a: 1, b: 2 }
console.log("a" in o)
console.log(delete o.a)
console.log("a" in o)
console.log("b" in o)
console.log(delete o.zz)
`, "true\ntrue\nfalse\ntrue\ntrue")
}

func TestE2EDynObjKeysAndForIn(t *testing.T) {
	assertOutput(t, `
let o: any = { first: 1, second: 2 }
o.third = 3
delete o.second
console.log(Object.keys(o))
for (const k in o) console.log(k)
`, "[ 'first', 'third' ]\nfirst\nthird")
}

func TestE2EDynObjNestedAndSpread(t *testing.T) {
	assertOutput(t, `
let o: any = { outer: { inner: "deep" }, n: 7 }
console.log(o.outer.inner)
let p: any = { ...o, extra: true }
p.n = 8
console.log(p.outer.inner, p.n, p.extra, o.n)
`, "deep\ndeep 8 true 7")
}

func TestE2EDynObjIdentityAndTypeof(t *testing.T) {
	assertOutput(t, `
let o: any = { a: 1 }
let alias: any = o
alias.a = 2
console.log(o.a)
console.log(o === alias)
let q: any = { a: 2 }
console.log(o === q)
console.log(typeof o)
`, "2\ntrue\nfalse\nobject")
}

func TestE2EDynObjNullUndefinedThrow(t *testing.T) {
	assertOutput(t, `
let z: any = null
try { console.log(z.x) } catch (e) { console.log("caught:", e.message) }
let u: any
try { u.x = 1 } catch (e) { console.log("caught:", e.message) }
`, "caught: Cannot read properties of null (reading 'x')\ncaught: Cannot set properties of undefined (setting 'x')")
}

// --- Stage 2 (TDD-00155): untyped JSON.parse → dynamic trees, dynamic
// arrays (tag 11), JSON.stringify of dynamic values ---

func TestE2EDynJSONParseUntyped(t *testing.T) {
	assertOutput(t, `
const data = JSON.parse('{"name":"klain","version":2,"ok":true,"pi":3.14,"none":null}')
console.log(data.name, data.version, data.ok, data.pi, data.none)
console.log(typeof data, data.missing)
`, "klain 2 true 3.14 null\nobject undefined")
}

func TestE2EDynJSONParseNestedTree(t *testing.T) {
	assertOutput(t, `
const cfg = JSON.parse('{"server":{"host":"thessaloniki","ports":[80,443]},"tags":["a","b"]}')
console.log(cfg.server.host)
console.log(cfg.server.ports.length, cfg.server.ports[0], cfg.server.ports[1])
console.log(cfg.tags[0], cfg.tags[1], cfg.tags[5])
`, "thessaloniki\n2 80 443\na b undefined")
}

func TestE2EDynArrMutationAndKeys(t *testing.T) {
	assertOutput(t, `
const d = JSON.parse('{"xs":[1,2]}')
d.xs[2] = 30
d.xs[4] = 50
console.log(d.xs.length, d.xs[2], d.xs[3], d.xs[4])
console.log(Object.keys(d.xs))
for (const i in d.xs) console.log(i)
`, "5 30 undefined 50\n[ '0', '1', '2', '3', '4' ]\n0\n1\n2\n3\n4")
}

func TestE2EDynJSONStringifyRoundTrip(t *testing.T) {
	assertOutput(t, `
const data = JSON.parse('{"a":1,"s":"hi\\nthere","arr":[true,null,2.5],"o":{"k":"v"}}')
const s = JSON.stringify(data)
console.log(s)
const round: any = JSON.parse(s)
console.log(round.arr[2], round.o.k)
`, "{\"a\":1,\"s\":\"hi\\nthere\",\"arr\":[true,null,2.5],\"o\":{\"k\":\"v\"}}\n2.5 v")
}

func TestE2EDynJSONStringifyLiteralsAndUndefined(t *testing.T) {
	assertOutput(t, `
let o: any = { keep: 1 }
o.gone = undefined
console.log(JSON.stringify(o))
let mix: any = [1, "two", null, 3.5]
console.log(JSON.stringify(mix))
console.log(JSON.stringify(o.missing))
`, "{\"keep\":1}\n[1,\"two\",null,3.5]\nundefined")
}

func TestE2EDynJSONStringifyPrettySpace(t *testing.T) {
	// TDD-00077 P4: JSON.stringify of a dynamic (any-typed) value honors the
	// `space` argument, byte-identical to the statically-typed pretty path and
	// to Node — nested tag-10 bags and tag-11 arrays indent, empties stay inline.
	assertOutput(t, `
const v: any = JSON.parse('{"a":1,"b":[2,3],"c":{"d":4},"e":{},"f":[]}')
console.log(JSON.stringify(v, null, 2))
`, "{\n  \"a\": 1,\n  \"b\": [\n    2,\n    3\n  ],\n  \"c\": {\n    \"d\": 4\n  },\n  \"e\": {},\n  \"f\": []\n}")
}

func TestE2EDynJSONNestedArrayLiteralInObject(t *testing.T) {
	// Regression: a nested array literal inside an any-typed object literal must
	// recurse into a dynamic array (tag 11), symmetric to a nested object
	// literal — so it is navigable dynamically and walkable by JSON.stringify,
	// not built as a static array boxed as any (which the dynamic walker rejects).
	assertOutput(t, `
const o: any = { name: "x", vals: [1, 2, [3, 4]], meta: { tags: ["a"], n: 5 } }
console.log(JSON.stringify(o))
console.log(o.vals[2][1])
console.log(o.meta.tags[0])
`, "{\"name\":\"x\",\"vals\":[1,2,[3,4]],\"meta\":{\"tags\":[\"a\"],\"n\":5}}\n4\na")
}

func TestE2EDynJSONErrors(t *testing.T) {
	assertOutput(t, `
try { JSON.parse("{bad") } catch (e) { console.log("caught:", e.name) }
let cyc: any = { a: 1 }
cyc.self = cyc
try { JSON.stringify(cyc) } catch (e) { console.log("caught:", e.message) }
`, "caught: SyntaxError\ncaught: Converting circular structure to JSON")
}

func TestE2EDynArrToStringJoin(t *testing.T) {
	assertOutput(t, `
const d = JSON.parse('{"xs":[1,"two",null,true,[5,6]]}')
console.log(`+"`joined: ${d.xs}`"+`)
`, "joined: 1,two,,true,5,6")
}

// --- Stage 3 (TDD-00155): the prototype chain — Object.create /
// getPrototypeOf / setPrototypeOf, __proto__, chain-walking get and `in` ---

func TestE2EDynProtoInheritShadowDelete(t *testing.T) {
	assertOutput(t, `
const base: any = { greet: "hello", shared: 1 }
const child: any = Object.create(base)
child.own = 2
console.log(child.greet, child.own)
child.shared = 99
console.log(child.shared, base.shared)
delete child.shared
console.log(child.shared)
`, "hello 2\n99 1\n1")
}

func TestE2EDynProtoInVsHasOwnAndKeys(t *testing.T) {
	assertOutput(t, `
const base: any = { greet: "hello" }
const child: any = Object.create(base)
child.own = 2
console.log("greet" in child, Object.hasOwn(child, "greet"))
console.log("own" in child, Object.hasOwn(child, "own"))
console.log(Object.keys(child))
console.log(JSON.stringify(child))
`, "true false\ntrue true\n[ 'own' ]\n{\"own\":2}")
}

func TestE2EDynProtoGetSetAndDunder(t *testing.T) {
	assertOutput(t, `
const base: any = { greet: "hello" }
const child: any = Object.create(base)
console.log(Object.getPrototypeOf(child) === base, child.__proto__ === base)
const p2: any = { greet: "yo" }
Object.setPrototypeOf(child, p2)
console.log(child.greet)
child.__proto__ = base
console.log(child.greet)
child.__proto__ = 5
console.log(child.greet)
console.log(Object.getPrototypeOf(Object.create(null)))
`, "true true\nyo\nhello\nhello\nnull")
}

func TestE2EDynProtoLiteralFormAndCycle(t *testing.T) {
	assertOutput(t, `
const base: any = { greet: "hello" }
const lit: any = { __proto__: base, mine: true }
console.log(lit.greet, lit.mine, Object.hasOwn(lit, "greet"))
const child: any = Object.create(base)
try { Object.setPrototypeOf(base, child) } catch (e) { console.log("caught:", e.message) }
try { let z: any = null; Object.getPrototypeOf(z) } catch (e) { console.log("caught:", e.name) }
`, "hello true false\ncaught: Cyclic __proto__ value\ncaught: TypeError")
}

func TestE2EDynObjPrimitiveMemberRead(t *testing.T) {
	assertOutput(t, `
let n: any = 5
console.log(n.foo)
let s: any = "str"
console.log(s.bar)
console.log(Object.keys(n))
`, "undefined\nundefined\n[]")
}
