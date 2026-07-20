// =============================================================================
// Object static methods and typeof operator
// =============================================================================

// ── Object.keys ──────────────────────────────────────────────────────────────
// Returns a string[] of the object's field names in declaration order.
// The result array can be iterated directly without assigning to a variable.

const person: { name: string; age: number; city: string } = {
  name: 'Alice',
  age: 30,
  city: 'London',
}

for (const k of Object.keys(person)) {
  console.log(k)
}
// name
// age
// city

console.log(Object.keys(person).length)   // 3

// ── Object.values ─────────────────────────────────────────────────────────────
// Returns a string[] of each field value converted to a string.

for (const v of Object.values(person)) {
  console.log(v)
}
// Alice
// 30
// London

// ── Object.entries ────────────────────────────────────────────────────────────
// Returns {key: string; value: string}[] — key/value pairs for each field.
// Access with .key and .value on each entry.

const dims: { width: number; height: number; depth: number } = {
  width: 100,
  height: 200,
  depth: 50,
}

for (const entry of Object.entries(dims)) {
  console.log(entry.key + ': ' + entry.value)
}
// width: 100
// height: 200
// depth: 50

// ── for…in + Object.keys together ────────────────────────────────────────────
// for…in iterates field names directly, avoiding an intermediate array.

const scores: { math: number; english: number; science: number } = {
  math: 95,
  english: 88,
  science: 91,
}

for (const subject in scores) {
  console.log(subject)
}
// math
// english
// science

// ── Object.groupBy with inline key iteration ───────────────────────────────────
const fruits: string[] = ['apple', 'avocado', 'banana', 'blueberry', 'cherry']
const byLetter = Object.groupBy(fruits, (f) => f.substring(0, 1))

// Object.keys on a group map can be iterated directly
for (const letter of Object.keys(byLetter)) {
  console.log(letter)
}
// a
// b
// c

// ── Object.assign ─────────────────────────────────────────────────────────────
// Copies each source's fields into target, in argument order — a later
// source overwrites an earlier one on a shared field name. Mutates target in
// place (same heap object, no copy) and returns it.

interface Settings { theme: string; fontSize: number; darkMode: boolean }

const defaults: Settings = { theme: 'light', fontSize: 12, darkMode: false }
const userPrefs: Settings = { theme: 'dark', fontSize: 14, darkMode: true }

const applied = Object.assign(defaults, userPrefs)
console.log(applied.theme)      // dark
console.log(defaults.theme)     // dark (same object — assign mutates target)

// A source can supply just a subset of target's fields.
interface FontOnly { fontSize: number }
const bigFont: FontOnly = { fontSize: 20 }
Object.assign(defaults, bigFont)
console.log(defaults.fontSize)  // 20
console.log(defaults.darkMode)  // 1 (untouched — still true from userPrefs)

// Multiple sources apply in order; the last one wins on overlapping fields.
interface Counter { count: number }
const base: Counter = { count: 0 }
const step1: Counter = { count: 1 }
const step2: Counter = { count: 2 }
Object.assign(base, step1, step2)
console.log(base.count)         // 2

// ── Object.freeze / Object.seal ──────────────────────────────────────────────
// Object.freeze(obj) blocks writes to obj's existing fields — tracked by the
// object's own heap identity, not by the variable that froze it, so a write
// attempted through a different alias (another variable, a function
// parameter) is blocked too. Field-value mutation is the only thing freeze
// needs to actively enforce here: this compiler's objects are fixed-shape
// heap structs, so adding or deleting a field was already impossible before
// freeze existed, for any object. A blocked write throws a catchable Error.

interface Config { host: string; port: number }
const conf: Config = { host: 'localhost', port: 8080 }
Object.freeze(conf)

try {
  conf.port = 9090
} catch (e) {
  console.log('blocked: cannot write to a frozen object')
}
console.log(conf.port)   // 8080 (write was rejected, value unchanged)

// Freezing is per-object, not global — an unrelated object is unaffected.
const other: Config = { host: 'example.com', port: 443 }
other.port = 8443
console.log(other.port)  // 8443

// Object.seal(obj) is accepted but is a genuine no-op here: seal blocks
// adding/removing fields in real JS, which this compiler's objects already
// can't do at all — sealing changes nothing further, existing fields stay
// freely writable.
const sealed: Config = { host: 'sealed.example', port: 1 }
Object.seal(sealed)
sealed.port = 2
console.log(sealed.port)  // 2 (seal never blocked the value write)

// ── typeof ────────────────────────────────────────────────────────────────────
// Returns a string describing the compile-time type. Evaluated entirely at
// compile time — no code is emitted for the argument expression.

const nums: number[] = [1, 2, 3]

console.log(typeof 42)         // number
console.log(typeof 3.14)       // number
console.log(typeof 'hello')    // string
console.log(typeof true)       // boolean
console.log(typeof person)     // object
console.log(typeof nums)       // object  (arrays are objects)

function greet(x: string): string { return 'hi ' + x }
console.log(typeof greet)      // function
