// Built-in utility types — first stage (TDD-00079 Stage 1a): the single-type-
// argument utilities Partial, Required, Readonly, and NonNullable. Because this
// compiler monomorphizes, a utility type is evaluated at compile time to a
// concrete type. In the current structural, zero-fill object model
// Partial/Required/Readonly have no observable runtime effect — object fields
// are already omittable and there is no mutation checking — so they resolve to
// their argument's own shape. The point is that they resolve *correctly* (a
// `Partial<User>` used to silently become `User[]`). Pick/Omit/Record and the
// general mapped/conditional forms are later stages. See docs/status/TYPE-SYSTEM.md.

interface User {
	name: string
	age: number
}

// Partial<User>: every field is optional, so a literal may omit some.
const draft: Partial<User> = { name: "Zoe" }
console.log(draft.name)            // Zoe

// Required<User> / Readonly<User>: same shape as User here.
const full: Required<User> = { name: "Ada", age: 36 }
console.log(full.name + " is " + full.age)  // Ada is 36

const frozen: Readonly<User> = { name: "Kay", age: 40 }
console.log(frozen.age)            // 40

// A Partial as a function parameter — a common "options bag" pattern.
function describe(u: Partial<User>): string {
	return u.name + " (" + u.age + ")"
}
console.log(describe({ name: "Sam", age: 22 }))  // Sam (22)

// NonNullable<T> strips null/undefined from the type.
function must(x: NonNullable<string | null>): string {
	return x.toUpperCase()
}
console.log(must("ready"))         // READY

// --- Stage 1b: key-based utilities over a string-literal-union key ---

// Pick<T, K> keeps only the named fields; Omit<T, K> drops them.
const contact: Pick<User, "name"> = { name: "Ivy" }
console.log(contact.name)          // Ivy

const noAge: Omit<User, "age"> = { name: "Jo" }
console.log(noAge.name)            // Jo

// Record<K, V>: a literal-union key gives a fixed-shape object …
const grades: Record<"math" | "science", number> = { math: 90, science: 85 }
console.log(grades.math + grades.science)   // 175

// … and a general `string` key gives a Map<string, V>.
const counts: Record<string, number> = new Map()
counts.set("hits", 3)
console.log(counts.get("hits"))    // 3

// A string-literal union type is usable on its own (resolves to string).
type Direction = "north" | "south" | "east" | "west"
const heading: Direction = "north"
console.log(heading)               // north
