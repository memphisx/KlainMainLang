interface Point {
  x: number;
  y: number;
}

interface User {
  name: string;
  age: number;
}

// type alias — same power, different syntax
type Rect = { width: number; height: number }

function distance(p: Point): number {
  return Math.floor(Math.sqrt(p.x * p.x + p.y * p.y))
}

function greet(u: User): string {
  return `Hello ${u.name}, age ${u.age}`
}

function area(r: Rect): number {
  return r.width * r.height
}

const p: Point = { x: 3, y: 4 }
console.log(distance(p))     // 5

const u: User = { name: 'Alice', age: 30 }
console.log(greet(u))        // Hello Alice, age 30
console.log(JSON.stringify(u)) // {"name":"Alice","age":30}

const r: Rect = { width: 6, height: 7 }
console.log(area(r))         // 42

// Inline interface type in var decl
const p2: Point = { x: 0, y: 0 }
p2.x = 5
p2.y = 12
console.log(distance(p2))    // 13

// Interface fields default to i64 for `number` — a JSDoc @type comment
// overrides that per field, the same convention variable declarations
// already use (see jsdoc/ for the parser).
interface Measurement {
  label: string;
  /** @type {float64} */
  score: number;
}
const m: Measurement = { label: 'test', score: 9.5 }
console.log(m.score)               // 9.5
console.log(JSON.stringify(m))     // {"label":"test","score":9.5}

const parsed: Measurement = JSON.parse('{"label":"parsed","score":3.25}')
console.log(parsed.label)          // parsed
console.log(parsed.score)          // 3.25

// Array-typed fields carry their own length alongside the data, same as
// any other array value — .length, indexing, and for...of all just work.
interface Container {
  items: number[];
  label: string;
}
const initial: number[] = [10, 20, 30]
const c: Container = { items: initial, label: 'orig' }
console.log(c.items.length)        // 3
console.log(c.items[1])            // 20
for (const x of c.items) {
  console.log(x)                   // 10, 20, 30
}

const c2 = { ...c, label: 'copy' }
console.log(c2.items.length)       // 3

const { items: destructured } = c
destructured.push(40)
console.log(destructured.length)   // 4
console.log(c.items.length)        // 3 — destructuring copies, not aliases

function firstItems(cc: Container): number[] {
  return cc.items
}
console.log(firstItems(c).length)  // 3
