// General union types beyond T | null (TDD-00043) — string | number | boolean
// combinations, including with null/undefined, at the same runtime-tagged
// { i8, i64 } box any/unknown already use, but statically restricted to the
// declared member set at every assignment/call/return boundary. V1 scope:
// scalar members only (number/string/boolean/null/undefined) — a union
// nested inside an array element or object field, or a member that's itself
// an interface/array, is not yet supported and gives a clean compile-time
// error instead. See docs/status/TYPE-SYSTEM.md.

// --- declare, print, typeof, reassign across members ---
let x: string | number = "hello"
console.log(x)           // hello
console.log(typeof x)    // string
x = 42
console.log(x)           // 42
console.log(typeof x)    // number

// --- a third member, plus null ---
let y: number | boolean | null = 5
console.log(y)           // 5
y = true
console.log(y)           // true
y = null
console.log(y)           // null

// --- functions: a union param is checked at every call site, a union return
// is boxed like any other dynamic value. Arithmetic and member access on a
// union-typed value are still a clean compile error (Staged V1, same as
// any/unknown) — typeof/===/!== and printing are what's usable without first
// narrowing to a concrete type, which this compiler doesn't do yet either. ---
function describe(value: string | number): string | number {
	if (typeof value === "string") {
		return "matched a string"
	}
	return value
}
console.log(describe("hi"))  // matched a string
console.log(describe(21))    // 21

// --- arrow functions work the same way ---
const toDisplay = (n: number | boolean): string | number => {
	return n
}
console.log(toDisplay(7))     // 7
console.log(toDisplay(false)) // false

// --- equality reuses any/unknown's own tag-aware comparison ---
let a: string | number = 5
let b: string | number = 5
console.log(a === b)     // 1
let c: string | number = "5"
console.log(a === c)     // 0
