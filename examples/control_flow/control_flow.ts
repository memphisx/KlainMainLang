// =============================================================================
// Control flow — do…while, for…in, and braceless bodies
// =============================================================================

// ── do…while ─────────────────────────────────────────────────────────────────
// The body runs at least once before the condition is checked.

let i = 0
do {
  console.log(i)
  i++
} while (i < 3)
// 0
// 1
// 2

// Body executes even when the condition is false from the start.
let ran = 0
do {
  ran++
} while (false)
console.log(ran)  // 1

// break and continue work inside do…while
let j = 0
do {
  j++
  if (j === 2) continue  // skip printing 2
  if (j === 4) break     // stop at 4
  console.log(j)
} while (j < 10)
// 1
// 3

// ── for…in ───────────────────────────────────────────────────────────────────
// Iterates over the field names of an object as strings.

const point: { x: number; y: number; z: number } = { x: 10, y: 20, z: 30 }

for (const key in point) {
  console.log(key)
}
// x
// y
// z

// Collecting keys into a string
const config: { host: string; port: number; debug: number } = {
  host: 'localhost',
  port: 8080,
  debug: 1,
}

let keys = ''
for (const k in config) {
  keys = keys + k + ' '
}
console.log(keys)  // host port debug

// break works inside for…in
const coords: { a: number; b: number; c: number } = { a: 1, b: 2, c: 3 }
for (const k in coords) {
  if (k === 'b') break
  console.log(k)
}
// a

// ── Labeled break / continue ─────────────────────────────────────────────────
// A label on a loop lets break/continue target an outer loop, not just the
// innermost one.

outer: for (let a = 0; a < 3; a++) {
  for (let b = 0; b < 3; b++) {
    if (b === 1) break outer  // stops BOTH loops entirely
    console.log(a)
    console.log(b)
  }
}
// 0
// 0

again: for (let a = 0; a < 3; a++) {
  for (let b = 0; b < 3; b++) {
    if (b === 1) continue again  // skips to the next outer iteration
    console.log(a)
    console.log(b)
  }
}
// 0
// 0
// 1
// 0
// 2
// 0

// ── Braceless bodies ─────────────────────────────────────────────────────────
// Braces are optional when the body is a single statement.

// Braceless if / else
const x = 7
if (x > 5) console.log('big')
else console.log('small')
// big

// Braceless if / else if / else chain
const grade = 85
if (grade >= 90) console.log('A')
else if (grade >= 80) console.log('B')
else console.log('C')
// B

// Braceless while
let k = 0
while (k < 3) console.log(k++)
// 0
// 1
// 2

// Braceless for
for (let n = 0; n < 3; n++) console.log(n)
// 0
// 1
// 2

// Braceless for…of
const words: string[] = ['hello', 'world', 'TypeGo']
for (const w of words) console.log(w)
// hello
// world
// TypeGo
