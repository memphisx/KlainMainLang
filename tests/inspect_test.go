package tests

import "testing"

// --- Object-to-string: Node-style structured rendering (TDD-00075/ADR-00218).
// console.log(obj) → `Foo { x: 1 }` in both modes; string coercion → the same
// under -compat=strict (default), `[object Object]` under -compat=js. ---

func TestE2EInspectClassInstance(t *testing.T) {
	// Class name is demangled (`Point`, not `Point__kml_mod0`); strings are
	// single-quoted, booleans render true/false.
	assertOutput(t, `
class Point { x: number = 1; y: number = 2; }
console.log(new Point())
class Person { name: string = "bob"; age: number = 30; active: boolean = true; }
console.log(new Person())
`, "Point { x: 1, y: 2 }\nPerson { name: 'bob', age: 30, active: true }")
}

func TestE2EInspectObjectLiteralAndEmpty(t *testing.T) {
	assertOutput(t, `
const lit = { a: 5, b: "hi", c: true }
console.log(lit)
class Empty {}
console.log(new Empty())
`, "{ a: 5, b: 'hi', c: true }\nEmpty {}")
}

func TestE2EInspectNestedAndBigInt(t *testing.T) {
	assertOutput(t, `
const nested = { outer: 1, inner: { x: 2, y: "deep" } }
console.log(nested)
class Money { amount: bigint = 100n; }
console.log(new Money())
`, "{ outer: 1, inner: { x: 2, y: 'deep' } }\nMoney { amount: 100n }")
}

func TestE2EInspectDepthCap(t *testing.T) {
	// The inspector caps recursion (maxInspectDepth) and shows [Object] beyond —
	// matching Node, and crucially preventing infinite *compile-time* recursion
	// on a deeply-nested or self-referential type (ADR-00221).
	assertOutput(t, `
const deep = { a: { b: { c: { d: { e: { f: 1 } } } } } }
console.log(deep)
`, "{ a: { b: { c: { d: { e: [Object] } } } } }")
}

func TestE2EInspectStringCoercionStrict(t *testing.T) {
	// -compat=strict (default): string coercion uses the same useful view, never
	// [object Object].
	assertOutput(t, `
class Foo { x: number = 1; }
console.log(`+"`v=${new Foo()}`"+`)
console.log("" + new Foo())
`, "v=Foo { x: 1 }\nFoo { x: 1 }")
}

func TestE2EInspectArrays(t *testing.T) {
	// console.log(array) — previously a hard rejection — now renders Node-style
	// `[ 1, 2, 3 ]`; strings quote, empties are `[]`, nested arrays recurse, and
	// an array field inside an object renders inline.
	assertOutput(t, `
console.log([1, 2, 3])
console.log(["a", "b"])
const empty: number[] = []
console.log(empty)
console.log([[1, 2], [3, 4]])
const b = { items: [10, 20], owner: "me" }
console.log(b)
`, "[ 1, 2, 3 ]\n[ 'a', 'b' ]\n[]\n[ [ 1, 2 ], [ 3, 4 ] ]\n{ items: [ 10, 20 ], owner: 'me' }")
}

func TestE2EInspectToStringOverride(t *testing.T) {
	// A user-defined class toString() is honored in string coercion (both -compat
	// modes), while console.log still uses util.inspect (ignoring toString), as
	// in Node.
	assertOutput(t, `
class Money {
  amount: number = 42;
  toString(): string { return "$" + this.amount; }
}
const m = new Money()
console.log(`+"`I have ${m}`"+`)
console.log("total: " + m)
console.log(m)
`, "I have $42\ntotal: $42\nMoney { amount: 42 }")
}

func TestE2EInspectStringCoercionCompatJS(t *testing.T) {
	// -compat=js: string coercion is JS's primitive ToString, [object Object];
	// console.log still uses the structured view (util.inspect, not ToString).
	assertOutputCompatJS(t, `
class Foo { x: number = 1; }
console.log(`+"`v=${new Foo()}`"+`)
console.log(new Foo())
`, "v=[object Object]\nFoo { x: 1 }")
}
