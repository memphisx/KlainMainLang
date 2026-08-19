package tests

import "testing"

// Intersection types A & B & ... (TDD-00078). An object-type intersection
// merges its members' fields into one struct; these exercise the happy paths
// (named/inline/alias members, multi-member, array/nullable positions, field
// dedupe) and the clean rejections (non-object member, conflicting field type,
// and the `&`-binds-tighter-than-`|` precedence that turns A & B | C into a
// union with an object member).

// Two named interfaces intersected directly at the use site.
func TestE2EIntersectionNamedDirect(t *testing.T) {
	assertOutput(t, `
interface HasName { name: string }
interface HasAge { age: number }
const p: HasName & HasAge = { name: "Kyriakos", age: 34 }
console.log(p.name)
console.log(p.age)
`, "Kyriakos\n34")
}

// The same intersection behind a type alias — resolved at registration time,
// so this also covers the rewriteType member-descent that named members need.
func TestE2EIntersectionTypeAlias(t *testing.T) {
	assertOutput(t, `
interface HasName { name: string }
interface HasAge { age: number }
type Person = HasName & HasAge
const p: Person = { name: "Ada", age: 36 }
console.log(p.name + " " + p.age)
`, "Ada 36")
}

// Inline object types, and an intersection used as a function parameter and
// return type across three members.
func TestE2EIntersectionInlineAndFunc(t *testing.T) {
	assertOutput(t, `
function describe(x: { id: number } & { label: string }): string {
  return x.label + "#" + x.id
}
console.log(describe({ id: 7, label: "widget" }))

interface A { a: number }
interface B { b: number }
interface C { c: number }
function sum(x: A & B & C): number { return x.a + x.b + x.c }
console.log(sum({ a: 1, b: 2, c: 3 }))
`, "widget#7\n6")
}

// An array of an intersection, iterated.
func TestE2EIntersectionArray(t *testing.T) {
	assertOutput(t, `
interface A { a: number }
interface B { b: string }
const xs: (A & B)[] = [{ a: 1, b: "x" }, { a: 2, b: "y" }]
for (const it of xs) console.log(it.a + ":" + it.b)
`, "1:x\n2:y")
}

// A nullable intersection: (A & B) | null narrows on a null check.
func TestE2EIntersectionNullable(t *testing.T) {
	assertOutput(t, `
interface A { a: number }
interface B { b: number }
const v: (A & B) | null = { a: 1, b: 2 }
if (v !== null) console.log(v.a + v.b)
`, "3")
}

// A field appearing in two members with the identical type is deduplicated,
// not treated as a conflict.
func TestE2EIntersectionDuplicateFieldDedupe(t *testing.T) {
	assertOutput(t, `
const v: { x: number } & { x: number } = { x: 5 }
console.log(v.x)
`, "5")
}

// A non-object member (a scalar, which intersects to `never`) is a clean
// compile error rather than broken codegen.
func TestE2EIntersectionNonObjectRejected(t *testing.T) {
	mustCompileError(t, `
interface A { x: number }
const v: A & number = { x: 1 }
console.log(v.x)
`, "object type")
}

// A field declared with conflicting types across members is rejected under the
// default -compat=strict (the -compat=js `never`-field path is a later stage).
func TestE2EIntersectionConflictRejected(t *testing.T) {
	mustCompileError(t, `
const v: { x: number } & { x: string } = { x: 1 }
console.log(v.x)
`, "conflicting types")
}

// Same-named object fields across members are recursively intersected
// (`{ inner: A } & { inner: B }` ⇒ `inner: A & B`), not a conflict.
func TestE2EIntersectionSameNameObjectField(t *testing.T) {
	assertOutput(t, `
interface A { a: number }
interface B { b: string }
type Combined = { inner: A } & { inner: B }
const c: Combined = { inner: { a: 1, b: "x" } }
console.log(c.inner.a)
console.log(c.inner.b)
`, "1\nx")
}

// A valid intersection as a function-type parameter merges normally.
func TestE2EIntersectionFunctionTypeParam(t *testing.T) {
	assertOutput(t, `
interface A { x: number }
interface B { y: number }
function call(f: (p: A & B) => number): number { return f({ x: 3, y: 4 }) }
console.log(call((p) => p.x + p.y))
`, "7")
}

// A valid intersection nested as an object field type merges normally.
func TestE2EIntersectionNestedObjectField(t *testing.T) {
	assertOutput(t, `
interface A { x: number }
interface B { y: number }
interface W { p: A & B }
const w: W = { p: { x: 1, y: 2 } }
console.log(w.p.x + w.p.y)
`, "3")
}

// A bad intersection nested in an object field (a position the use-site
// checkpoints don't reach directly) must be a clean rejection, not invalid IR.
// Regression: it used to resolve to a malformed merged struct and emit a
// `store ptr 5` type mismatch that clang rejected. See ADR-00225.
func TestE2EIntersectionNestedFieldNonObjectRejected(t *testing.T) {
	mustCompileError(t, `
interface A { x: number }
interface W { p: A & number }
const w: W = { p: 5 }
console.log(w.p)
`, "object type")
}

// The same for a non-object intersection as an array element type.
func TestE2EIntersectionArrayElementNonObjectRejected(t *testing.T) {
	mustCompileError(t, `
interface A { x: number }
const xs: (A & string)[] = []
console.log(xs.length)
`, "object type")
}

// `&` binds tighter than `|`: A & B | C parses as (A & B) | C, so C makes it a
// union whose members include an object — outside union V1's scalar-only scope,
// a clean rejection that also proves the precedence.
func TestE2EIntersectionPrecedenceOverUnion(t *testing.T) {
	mustCompileError(t, `
interface A { a: number }
interface B { b: number }
interface C { c: number }
const v: A & B | C = { a: 1, b: 2 }
console.log(1)
`, "union member")
}
