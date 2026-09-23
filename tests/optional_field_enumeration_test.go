package tests

import "testing"

// ADR-01066: Object.values, Object.entries and for…in list only the fields an
// object actually has — an omitted optional field contributes nothing, as with
// Object.keys/inspect/JSON (ADR-01063). Before this an optional numeric field
// was invalid IR in Object.values/entries (a `{ i1, double }` stored into a
// `double` slot).

func TestE2EOptionalFieldEnumeration(t *testing.T) {
	src := `
interface P { name: string; age?: number; nick?: string; tags?: string[] }
const a: P = { name: "a" }
const b: P = { name: "b", age: 3, nick: "bb", tags: ["x"] }
for (const k in a) console.log("k", k)
for (const k in b) console.log("k", k)
interface Q { x: number; y?: number }
const q: Q = { x: 1 }
const q2: Q = { x: 1, y: 2 }
console.log(Object.values(q), Object.values(q2), Object.entries(q), Object.entries(q2))
const sum = Object.values(q2).reduce((s, v) => s + v, 0)
console.log(sum, Object.values(q).length, Object.entries(q2)[1][1] + 1)
interface R { a?: string; b?: string }
const r: R = {}
console.log(Object.values(r), Object.entries(r), Object.keys(r))
for (const k in r) console.log("never", k)
`
	assertOutput(t, src, "k name\nk name\nk age\nk nick\nk tags\n[ 1 ] [ 1, 2 ] [ [ 'x', 1 ] ] [ [ 'x', 1 ], [ 'y', 2 ] ]\n3 1 3\n[] [] []")
	assertSameAsNode(t, src)
}
