// General mapped types (TDD-00079 Stage 2): { [K in keyof T]: V }, with the
// `keyof` operator and indexed access `T[K]`. Because this compiler
// monomorphizes, a mapped type is evaluated at compile time to a concrete object
// type — for each key of the source, a field is produced whose type is the
// mapped body. V1 handles a body that is `T[K]` (homomorphic), a bare `K`, a
// concrete type (uniform), or `T[K]` wrapped one level in Array/Promise/Set; the
// `?`/`readonly` modifiers are accepted but near-no-ops in the current object
// model. See docs/status/TYPE-SYSTEM.md.

interface User {
	name: string
	age: number
}

// Homomorphic identity: each field keeps its own type via T[K].
type Clone = { [K in keyof User]: User[K] }
const copy: Clone = { name: "Zoe", age: 34 }
console.log(copy.name + " is " + copy.age)   // Zoe is 34

// Uniform: every field becomes the same type — e.g. a "which fields changed" map.
type Changed = { [K in keyof User]: boolean }
const dirty: Changed = { name: true, age: false }
console.log(dirty.name)                       // true
console.log(dirty.age)                        // false

// Each field wrapped in an array.
type History = { [K in keyof User]: Array<User[K]> }
const past: History = { name: ["Zo", "Zoe"], age: [33, 34] }
console.log(past.name[0])                     // Zo
console.log(past.age[1])                      // 34

// `readonly` and optional `?` modifiers are accepted.
type Draft = { readonly [K in keyof User]?: User[K] }
const partial: Draft = { name: "Kay" }
console.log(partial.name)                     // Kay

// A mapped type may also range over a string-literal union directly.
type Toggles = { [K in "wifi" | "bluetooth"]: boolean }
const state: Toggles = { wifi: true, bluetooth: false }
console.log(state.wifi)                        // true

// Indexed access is usable on its own, too.
const label: User["name"] = "hello"
console.log(label)                             // hello
