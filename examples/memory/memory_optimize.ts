// -optimize-memory (TDD-00134 Stage 1): object literals the escape analysis
// proves never outlive their block are stack-allocated instead of
// heap-allocated. Semantics are identical with or without the flag — this
// example compiles and behaves the same either way; compile it with
// `klainmain -optimize-memory` to get the stack-allocation lowering.
interface Point { x: number; y: number }

function dist2(ax: number, ay: number): number {
  // Non-escaping: only field reads — stack-allocated under the flag.
  const p: Point = { x: ax, y: ay }
  return p.x * p.x + p.y * p.y
}

const kept: Point[] = []
function stash(i: number): void {
  // Escaping (stored into an outer array) — always heap-allocated.
  const q: Point = { x: i, y: i }
  kept.push(q)
}

let total = 0
for (let i = 0; i < 100000; i++) {
  // Non-escaping in a loop: the single stack slot is reused (and re-zeroed)
  // every iteration.
  const r: Point = { x: i, y: i + 1 }
  total += r.x + r.y
  if (i % 25000 === 0) { stash(i) }
}
console.log(total + dist2(3, 4))
console.log(kept.length)

function tally(base: number): number {
  // Non-escaping closure: its {fn,env} header and env stack-allocate under
  // the flag; the shared cell holding `acc` stays heap.
  let acc = 0
  const add = (x: number): void => { acc += x + base }
  add(1)
  add(2)
  return acc
}
console.log(tally(10))

class Vec2 {
  x: number
  y: number
  constructor(x: number, y: number) { this.x = x; this.y = y }
  dot(ox: number, oy: number): number { return this.x * ox + this.y * oy }
}
function measure(i: number): number {
  // Tuple + this-clean class instance: both stack-allocate under the flag.
  const pair: [number, number] = [i, i * 2]
  const v = new Vec2(pair[0], pair[1])
  return v.dot(1, 1)
}
console.log(measure(7))

function divmod(a: number, b: number): [number, number] {
  // Stage 3: a small tuple (≤2 scalar fields) returned from a top-level
  // function goes back BY VALUE — a register-passable struct aggregate, no
  // allocation for the literal at all. The Go-style destructuring consumer
  // below is allocation-free too (extractvalue, no memory traffic).
  return [Math.floor(a / b), a % b]
}
const [quot, rem] = divmod(17, 5)
console.log(quot, rem)
