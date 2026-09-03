// JSON.stringify and JSON.parse

// ── stringify numbers ─────────────────────────────────────────────────────────
const n: number = 42
console.log(JSON.stringify(n))       // 42

const neg: number = -7
console.log(JSON.stringify(neg))     // -7

// ── stringify strings ─────────────────────────────────────────────────────────
const s: string = 'hello'
console.log(JSON.stringify(s))       // 'hello'

const esc: string = "say 'hi'"
console.log(JSON.stringify(esc))     // 'say \"hi\"'

// ── stringify booleans ────────────────────────────────────────────────────────
console.log(JSON.stringify(true))    // true
console.log(JSON.stringify(false))   // false

// ── stringify number arrays ───────────────────────────────────────────────────
const nums: number[] = [1, 2, 3]
console.log(JSON.stringify(nums))    // [1,2,3]

const empty: number[] = []
console.log(JSON.stringify(empty))   // []

// ── stringify string arrays ───────────────────────────────────────────────────
const words: string[] = ['foo', 'bar', 'baz']
console.log(JSON.stringify(words))   // ['foo','bar','baz']

// ── stringify boolean arrays ──────────────────────────────────────────────────
const flags: boolean[] = [true, false, true]
console.log(JSON.stringify(flags))   // [true,false,true]

// ── stringify object arrays ───────────────────────────────────────────────────
interface Point { x: number; y: number }
const points: Point[] = [{ x: 1, y: 2 }, { x: 3, y: 4 }]
console.log(JSON.stringify(points))  // [{"x":1,"y":2},{"x":3,"y":4}]

// ── stringify pretty-printed (the `space` argument) ───────────────────────────
interface Addr { city: string; zip: number }
interface User { name: string; active: boolean; addr: Addr; tags: string[] }
const user: User = { name: 'bob', active: true, addr: { city: 'Thessaloniki', zip: 54600 }, tags: ['a', 'b'] }

// A numeric space indents with N spaces and puts a space after each colon.
console.log(JSON.stringify(user, null, 2))
// {
//   "name": "bob",
//   "active": true,
//   "addr": {
//     "city": "Thessaloniki",
//     "zip": 54600
//   },
//   "tags": [
//     "a",
//     "b"
//   ]
// }

// A string space is used literally as the indent unit (here a tab).
console.log(JSON.stringify({ x: 1, y: [1, 2] }, null, '\t'))

// Empty containers stay inline even when pretty-printing.
console.log(JSON.stringify({ e: {}, arr: [] }, null, 2))  // { "e": {}, "arr": [] } across lines

// ── stringify with a custom toJSON() ──────────────────────────────────────────
// If a class defines toJSON(), JSON.stringify serializes its result instead of
// the object's own fields — exactly as in real JS.
class Money {
  amount: number = 42
  currency: string = 'EUR'
  toJSON(): string { return this.amount + this.currency }
}
console.log(JSON.stringify(new Money()))              // "42EUR"
console.log(JSON.stringify({ price: new Money() }))   // {"price":"42EUR"}

// ── parse number ──────────────────────────────────────────────────────────────
const parsed: number = JSON.parse('123')
console.log(parsed)                  // 123

const neg2: number = JSON.parse('-99')
console.log(neg2)                    // -99

// ── parse string ──────────────────────────────────────────────────────────────
const str: string = JSON.parse('"world"')
console.log(str)                     // world

// ── malformed JSON throws a catchable SyntaxError (strict, like real JS) ───────
try {
  const bad: number = JSON.parse("{oops}")
  console.log(bad)
} catch (e) {
  console.log(e.name)                // SyntaxError
}

// ── round-trip ────────────────────────────────────────────────────────────────
const orig: number = 999
const serialized: string = JSON.stringify(orig)
const restored: number = JSON.parse(serialized)
console.log(restored)               // 999

const origStr: string = 'TypeGo'
const serializedStr: string = JSON.stringify(origStr)
const restoredStr: string = JSON.parse(serializedStr)
console.log(restoredStr)            // TypeGo

// ── parse object into a statically-typed target ───────────────────────────────
interface Coord { x: number; y: number }
const coord: Coord = JSON.parse('{"x":10,"y":20}')
console.log(coord.x)                // 10
console.log(coord.y)                // 20

interface Account { name: string; balance: number; active: boolean }
const account: Account = JSON.parse('{"name":"Alice","balance":100,"active":true}')
console.log(account.name)           // Alice
console.log(account.balance)        // 100
console.log(account.active)         // 1

// A missing key falls back to the field type's zero value.
interface Pair { a: number; b: number }
const pair: Pair = JSON.parse('{"a":5}')
console.log(pair.a)                 // 5
console.log(pair.b)                 // 0

// ── parse nested objects, array fields, and top-level arrays ──────────────────
// (Addr is declared above, in the pretty-printing section.)
interface Member { name: string; addr: Addr; tags: string[] }
const m: Member = JSON.parse('{"name":"Nikos","addr":{"city":"Thessaloniki","zip":54600},"tags":["admin","dev"]}')
console.log(m.addr.city)            // Thessaloniki  (nested object field)
console.log(m.tags[1])              // dev           (array-typed field)

// a top-level array of objects
const members: Member[] = JSON.parse('[{"name":"A","addr":{"city":"X","zip":1},"tags":[]},{"name":"B","addr":{"city":"Y","zip":2},"tags":["x"]}]')
console.log(members.length)         // 2
console.log(members[1].addr.city)  // Y
