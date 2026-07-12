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

// ── parse number ──────────────────────────────────────────────────────────────
const parsed: number = JSON.parse('123')
console.log(parsed)                  // 123

const neg2: number = JSON.parse('-99')
console.log(neg2)                    // -99

// ── parse string ──────────────────────────────────────────────────────────────
const str: string = JSON.parse("'world'")
console.log(str)                     // world

// ── round-trip ────────────────────────────────────────────────────────────────
const orig: number = 999
const serialized: string = JSON.stringify(orig)
const restored: number = JSON.parse(serialized)
console.log(restored)               // 999

const origStr: string = 'TypeGo'
const serializedStr: string = JSON.stringify(origStr)
const restoredStr: string = JSON.parse(serializedStr)
console.log(restoredStr)            // TypeGo

// ── parse object (flat objects, primitive fields only — nested object ──────────
// ── fields are not yet supported) ───────────────────────────────────────────────
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
