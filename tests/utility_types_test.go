package tests

import "testing"

// Built-in single-argument utility types (TDD-00079 Stage 1a). In this
// compiler's structural, zero-fill object model Partial/Required/Readonly have
// no observable effect yet, so they resolve to their argument's shape — the win
// is that they resolve *correctly* instead of silently becoming `Arg[]` via the
// old generic ElemType fallback. NonNullable strips the nullable flag.

func TestE2EUtilityPartial(t *testing.T) {
	// A partial literal (missing `age`) is accepted — pre-fix, Partial<User>
	// resolved to User[] and this failed with an array-initializer error.
	assertOutput(t, `
interface User { name: string; age: number }
const a: Partial<User> = { name: "Zoe" }
console.log(a.name)
`, "Zoe")
}

func TestE2EUtilityRequiredReadonly(t *testing.T) {
	assertOutput(t, `
interface User { name: string; age: number }
const b: Required<User> = { name: "Ada", age: 36 }
const c: Readonly<User> = { name: "Kay", age: 40 }
console.log(b.name + " " + b.age)
console.log(c.age)
`, "Ada 36\n40")
}

func TestE2EUtilityPartialParam(t *testing.T) {
	assertOutput(t, `
interface User { name: string; age: number }
function greet(u: Partial<User>): string { return "hi " + u.name }
console.log(greet({ name: "Sam" }))
`, "hi Sam")
}

func TestE2EUtilityNonNullable(t *testing.T) {
	assertOutput(t, `
function f(x: NonNullable<string | null>): string { return x }
console.log(f("hello"))
`, "hello")
}

// Pick<T, K> keeps only the named fields (K a string-literal union, TDD-00079
// Stage 1b), and genuinely drops the rest.
func TestE2EUtilityPick(t *testing.T) {
	assertOutput(t, `
interface User { name: string; age: number; email: string }
const a: Pick<User, "name" | "email"> = { name: "Zoe", email: "z@x.io" }
console.log(a.name + " " + a.email)
`, "Zoe z@x.io")
}

func TestE2EUtilityPickDropsField(t *testing.T) {
	// `age` is not in the Pick set, so assigning it is a clean rejection.
	mustCompileError(t, `
interface User { name: string; age: number }
const a: Pick<User, "name"> = { name: "Z", age: 5 }
`, "no field 'age'")
}

// Omit<T, K> drops the named fields.
func TestE2EUtilityOmit(t *testing.T) {
	assertOutput(t, `
interface User { name: string; age: number; email: string }
const b: Omit<User, "email"> = { name: "Ada", age: 36 }
console.log(b.name + " " + b.age)
`, "Ada 36")
}

// Record<K, V> with a string-literal-union key is a fixed-shape object; with a
// general `string` key it is a Map<string, V>.
func TestE2EUtilityRecord(t *testing.T) {
	assertOutput(t, `
const scores: Record<"math" | "science", number> = { math: 90, science: 85 }
console.log(scores.math + scores.science)
const m: Record<string, number> = new Map()
m.set("a", 1)
console.log(m.get("a"))
`, "175\n1")
}

// A standalone string-literal union type resolves to string (the literal value
// is not narrowed/enforced — a disclosed V1 simplification).
func TestE2EStringLiteralType(t *testing.T) {
	assertOutput(t, `
type Dir = "north" | "south"
const d: Dir = "north"
console.log(d)
`, "north")
}
