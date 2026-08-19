package tests

import "testing"

// Conditional types T extends U ? X : Y, with infer, generic type aliases, and
// the structural assignability check (TDD-00079 Stage 3). Everything evaluates
// at compile time to a concrete type.

// A generic type alias instantiated with a concrete argument.
func TestE2EGenericTypeAlias(t *testing.T) {
	assertOutput(t, `
type Box<T> = { value: T }
const b: Box<number> = { value: 42 }
console.log(b.value)
`, "42")
}

// infer captures the element type of an array; otherwise the type itself.
func TestE2EConditionalInferArray(t *testing.T) {
	assertOutput(t, `
type ElemOf<T> = T extends Array<infer E> ? E : T
const n: ElemOf<number[]> = 7
const s: ElemOf<string> = "hi"
console.log(n + 1)
console.log(s)
`, "8\nhi")
}

// infer through Promise (an Awaited-style alias).
func TestE2EConditionalInferPromise(t *testing.T) {
	assertOutput(t, `
type Awaited1<T> = T extends Promise<infer V> ? V : T
const v: Awaited1<Promise<number>> = 5
console.log(v + 10)
`, "15")
}

// Structural assignability decides the branch (object width subtyping).
func TestE2EConditionalAssignability(t *testing.T) {
	assertOutput(t, `
type HasName<T> = T extends { name: string } ? "yes" : "no"
interface User { name: string; age: number }
interface Anon { age: number }
const a: HasName<User> = "yes"
const b: HasName<Anon> = "no"
console.log(a)
console.log(b)
`, "yes\nno")
}

// Nested conditionals are right-associative.
func TestE2EConditionalNested(t *testing.T) {
	assertOutput(t, `
type Kind<T> = T extends string ? "str" : T extends number ? "num" : "other"
const a: Kind<string> = "str"
const b: Kind<number> = "num"
const c: Kind<boolean> = "other"
console.log(a)
console.log(b)
console.log(c)
`, "str\nnum\nother")
}
