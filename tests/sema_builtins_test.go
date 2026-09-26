package tests

import "testing"

// Which `new X(…)` constructs a builtin is decided after name resolution
// (TDD-00230 P1.6, ADR-01103): a user binding named like a builtin shadows it
// wherever it is in scope. Expected outputs are Node's (26.10.0).

func TestE2EUserClassNamedLikeABuiltin(t *testing.T) {
	// The parser used to turn `new Event(5)` into the builtin Event
	// unconditionally ("no field 'n'").
	assertOutput(t, `
class Event { n: number; constructor(n: number) { this.n = n } }
console.log(new Event(5).n)
`, "5")
}

func TestE2EBuiltinConstructorForms(t *testing.T) {
	assertOutput(t, `
const r = new RegExp()
console.log(r.source, r.test("abc"))
const d = new Date
console.log(typeof d.getTime())
const a = new Array(1, 2, 3)
console.log(a.length, a[2])
const m = new Map<string, Array<number>>()
m.set("k", [4, 5])
console.log(m.get("k")!.length)
`, "(?:) true\nnumber\n3 3\n2")
}
