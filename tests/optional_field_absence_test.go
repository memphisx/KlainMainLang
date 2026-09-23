package tests

import "testing"

// ADR-01063: an omitted optional field has no key. console.log omits it,
// Object.keys leaves it out, `in`/hasOwnProperty test presence at runtime; an
// optional tuple field is a real `undefined` when omitted (it was a bare tuple
// with no absent state); a tuple prints like an array; an absent nullable
// object/tuple field prints its keyword instead of dereferencing null.

func TestE2EOptionalFieldOmittedHasNoKey(t *testing.T) {
	src := `
interface P { name: string; age?: number; tag?: string; ok?: boolean; list?: number[]; nested?: { x: number } }
const a: P = { name: "a" }
const b: P = { name: "b", age: 3, tag: "t", ok: false, list: [3], nested: { x: 1 } }
console.log(a)
console.log({ name: b.name, age: b.age, ok: b.ok, list: b.list }) // under Node's 72-column break
console.log(a.age === undefined, a.tag === undefined, a.ok === undefined, a.list === undefined, a.nested === undefined)
console.log(Object.keys(a).length, Object.keys(b).length, "age" in a, "age" in b, a.hasOwnProperty("tag"), Object.hasOwn(b, "tag"))
console.log(Object.keys(a), Object.keys(b))
const allAbsent: { x?: number; y?: string } = {}
console.log(allAbsent, Object.keys(allAbsent).length)
`
	assertOutput(t, src, "{ name: 'a' }\n{ name: 'b', age: 3, ok: false, list: [ 3 ] }\ntrue true true true true\n1 6 false true false true\n[ 'name' ] [ 'name', 'age', 'tag', 'ok', 'list', 'nested' ]\n{} 0")
	assertSameAsNode(t, src)
}

func TestE2EOptionalTupleFieldAbsence(t *testing.T) {
	src := `
interface P { name: string; pair?: [number, number] }
const a: P = { name: "a" }
const b: P = { name: "b", pair: [1, 2] }
console.log(a.pair === undefined, b.pair === undefined, a.pair, b.pair)
console.log(a)
console.log(b)
if (b.pair !== undefined) console.log(b.pair[0] + b.pair[1])
const t: [number, string] = [1, "a"]
console.log(t)
console.log({ p: t, q: [1, 2] as [number, number] })
`
	assertOutput(t, src, "true false undefined [ 1, 2 ]\n{ name: 'a' }\n{ name: 'b', pair: [ 1, 2 ] }\n3\n[ 1, 'a' ]\n{ p: [ 1, 'a' ], q: [ 1, 2 ] }")
	assertSameAsNode(t, src)
}

// A nullable object field that is null printed garbage (the inspector
// dereferenced the null pointer) and an object with several absent fields
// segfaulted.
func TestE2EInspectNullableObjectField(t *testing.T) {
	src := `
interface Q { n?: { x: number }; s: string | null; o: { y: number } | null }
const q: Q = { s: null, o: null }
console.log(q, q.n === undefined, q.o === null)
const q2: Q = { n: { x: 2 }, s: "x", o: { y: 3 } }
console.log(q2)
`
	assertOutput(t, src, "{ s: null, o: null } true true\n{ n: { x: 2 }, s: 'x', o: { y: 3 } }")
	assertSameAsNode(t, src)
}
