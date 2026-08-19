package tests

import "testing"

// General mapped types { [K in keyof T]: V } with keyof and indexed access T[K]
// (TDD-00079 Stage 2). Everything evaluates at compile time to a concrete object
// type.

// Homomorphic identity clone: each field keeps its own type via T[K].
func TestE2EMappedIdentity(t *testing.T) {
	assertOutput(t, `
interface User { name: string; age: number }
type Clone = { [K in keyof User]: User[K] }
const a: Clone = { name: "Zoe", age: 34 }
console.log(a.name + " " + a.age)
`, "Zoe 34")
}

// Uniform mapping: every field becomes the same concrete type.
func TestE2EMappedUniform(t *testing.T) {
	assertOutput(t, `
interface User { name: string; age: number }
type Flags = { [K in keyof User]: boolean }
const f: Flags = { name: true, age: false }
console.log(f.name)
console.log(f.age)
`, "true\nfalse")
}

// T[K] wrapped one level in a container (Array<T[K]>).
func TestE2EMappedWrappedArray(t *testing.T) {
	assertOutput(t, `
interface User { name: string; age: number }
type Lists = { [K in keyof User]: Array<User[K]> }
const l: Lists = { name: ["a", "b"], age: [1, 2] }
console.log(l.name[1])
console.log(l.age[0])
`, "b\n1")
}

// The `readonly` and `?` modifiers are accepted (near-no-ops in this model).
func TestE2EMappedModifiers(t *testing.T) {
	assertOutput(t, `
interface User { name: string; age: number }
type RO = { readonly [K in keyof User]?: User[K] }
const r: RO = { name: "Kay" }
console.log(r.name)
`, "Kay")
}

// A mapped type over a string-literal-union source (no T, uniform value).
func TestE2EMappedLiteralSource(t *testing.T) {
	assertOutput(t, `
type Toggles = { [K in "a" | "b" | "c"]: boolean }
const t: Toggles = { a: true, b: false, c: true }
console.log(t.a)
console.log(t.b)
console.log(t.c)
`, "true\nfalse\ntrue")
}

// Standalone indexed access T["name"] resolves to the field's type.
func TestE2EIndexedAccessStandalone(t *testing.T) {
	assertOutput(t, `
interface User { name: string; age: number }
const nm: User["name"] = "hi"
const ag: User["age"] = 7
console.log(nm)
console.log(ag + 1)
`, "hi\n8")
}
