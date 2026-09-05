package tests

import "testing"

// TypeScript type assertions (ADR-00371): `as T`, `as const`, `satisfies T`.
// All are erased — accepted syntactically, identity at runtime, the expression
// keeps its own type.

func TestE2EAsCastScalar(t *testing.T) {
	assertOutput(t, `
const x = 5 as number
console.log(x + 1)
`, "6")
}

func TestE2EAsCastString(t *testing.T) {
	assertOutput(t, `
const y = "hi" as string
console.log(y)
console.log(("world" as string).length)
`, "hi\n5")
}

func TestE2EAsConst(t *testing.T) {
	assertOutput(t, `
const z = 5 as const
console.log(z)
`, "5")
}

func TestE2ESatisfies(t *testing.T) {
	assertOutput(t, `
const o = { a: 1 } satisfies object
console.log(o.a)
`, "1")
}

func TestE2EAsCastChained(t *testing.T) {
	assertOutput(t, `
const s = "x" as string as string
console.log(s)
`, "x")
}

func TestE2EAsCastObjectToInterface(t *testing.T) {
	assertOutput(t, `
interface P { name: string }
const p = { name: "kb" } as P
console.log(p.name)
`, "kb")
}

// --- Old-style angle-bracket type assertions (ADR-00451) ---

func TestE2EAngleBracketTypeAssertionErased(t *testing.T) {
	assertOutput(t, `
const n: number = <number>5;
const s = <string>("a" + "b");
console.log(n + 1);
console.log(s);
console.log(<number>n * 2);
const arr = <number[]>[1, 2, 3];
console.log(arr.length);
`, "6\nab\n10\n3")
}

func TestE2EAngleBracketAssertionDoesNotBreakLessThan(t *testing.T) {
	assertOutput(t, `
let a = 3; let b = 2;
console.log(a < b);
console.log(1 < 2);
`, "false\ntrue")
}

// --- `as T` on JSON.parse / .json() supplies the projection target ---
// The one carve-out from full assertion erasure: `JSON.parse(s) as T`
// projects into T exactly as `const p: T = JSON.parse(s)` does, matching
// the assertion's real static effect in TypeScript (narrowing `any` to T).

func TestE2EAsCastJSONParseObject(t *testing.T) {
	assertOutput(t, `
interface Rec { id: number; name: string; scores: number[] }
const one = JSON.parse('{"id":1,"name":"a","scores":[5,6]}') as Rec
console.log(one.name)
console.log(one.scores[1])
`, "a\n6")
}

func TestE2EAsCastJSONParseArray(t *testing.T) {
	assertOutput(t, `
interface Rec { id: number; name: string }
const parsed = JSON.parse('[{"id":1,"name":"a"},{"id":2,"name":"b"}]') as Rec[]
console.log(parsed.length)
console.log(parsed[1].name)
`, "2\nb")
}

func TestE2EAsCastJSONParseMemberTarget(t *testing.T) {
	// The declaration-context caveat (ADR-00571: member/element assignment
	// targets aren't projected) doesn't apply to the asserted form — the
	// type rides on the call itself.
	assertOutput(t, `
interface Item { id: number; label: string }
interface Holder { items: Item[] }
const h: Holder = { items: [] }
h.items = JSON.parse('[{"id":7,"label":"x"}]') as Item[]
console.log(h.items[0].label)
`, "x")
}

func TestE2EAsCastJSONParseSatisfiesStaysErased(t *testing.T) {
	// `satisfies` never narrows in TypeScript — the result stays a dynamic
	// tree, observable through the dynamic printer's formatting.
	assertOutput(t, `
const v = JSON.parse('{"a":1}') satisfies object
console.log(typeof v)
`, "object")
}
