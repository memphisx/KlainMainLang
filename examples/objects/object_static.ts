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
