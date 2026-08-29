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
