// Conditional types (TDD-00079 Stage 3): T extends U ? X : Y, with `infer`,
// generic type aliases, and a structural assignability check. Because this
// compiler monomorphizes, the check type is concrete, so the conditional is
// evaluated at compile time — the assignability test picks a branch, and any
// `infer` variable captures the matched sub-type. See docs/status/TYPE-SYSTEM.md.

// A generic type alias: reusable shape parameterized by T.
type Box<T> = { value: T }
const boxed: Box<number> = { value: 42 }
console.log(boxed.value)                // 42

// infer captures the element type of an array; otherwise yields T unchanged.
type ElementOf<T> = T extends Array<infer E> ? E : T
const one: ElementOf<number[]> = 7
const raw: ElementOf<string> = "hi"
console.log(one + 1)                    // 8
console.log(raw)                        // hi

// An Awaited-style alias: unwrap a Promise via infer.
type Resolve<T> = T extends Promise<infer V> ? V : T
const value: Resolve<Promise<number>> = 5
console.log(value + 10)                 // 15

// Structural assignability drives the branch: does T have a `name: string`?
type Named<T> = T extends { name: string } ? "named" : "anonymous"
interface User {
	name: string
	age: number
}
interface Widget {
	id: number
}
const u: Named<User> = "named"
const w: Named<Widget> = "anonymous"
console.log(u)                          // named
console.log(w)                          // anonymous

// Nested conditionals are right-associative — a small type-level match.
type Kind<T> = T extends string ? "string"
	: T extends number ? "number"
	: "other"
const ks: Kind<string> = "string"
const kn: Kind<number> = "number"
const kb: Kind<boolean> = "other"
console.log(ks + " " + kn + " " + kb)   // string number other
