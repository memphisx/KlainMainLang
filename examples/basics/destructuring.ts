// Destructuring extracts values from arrays or fields from objects into
// named local variables in a single declaration.
// The RHS can be a variable, a function call, or a literal.

// ─── Array destructuring from a variable ───────────────────────────────────

const coords = [10, 20, 30, 40, 50]

const [x, y, z] = coords
console.log(x)   // 10
console.log(y)   // 20
console.log(z)   // 30

// Holes skip elements at that index.
const [, second, , fourth] = coords
console.log(second)  // 20
console.log(fourth)  // 40

// Swap via a temporary array.
let a: number = 1
let b: number = 2
const tmp = [b, a]
const [newA, newB] = tmp
console.log(newA)  // 2
console.log(newB)  // 1

// ─── Array destructuring from a literal ────────────────────────────────────

const [lo, , hi] = [0, 50, 100]
console.log(lo)  // 0
console.log(hi)  // 100

// ─── Array destructuring from a function call ───────────────────────────────

function range(start: number, n: number): number[] {
    let r = new Array<number>(n)
    for (let i = 0; i < n; i++) {
        r[i] = start + i
    }
    return r
}

const [first, second2, third] = range(5, 4)
console.log(first)    // 5
console.log(second2)  // 6
console.log(third)    // 7

// ─── Object destructuring from a variable ───────────────────────────────────

let pt: { x: number; y: number } = { x: 3, y: 7 }

// Rename on extract.
const { x: px, y: py } = pt
console.log(px)  // 3
console.log(py)  // 7

// Shorthand: local name matches field name.
let size: { width: number; height: number } = { width: 800, height: 600 }
const { width, height } = size
console.log(width)   // 800
console.log(height)  // 600

// String fields.
let person: { label: string; age: number } = { label: 'Alice', age: 30 }
const { label, age } = person
console.log(label)  // Alice
console.log(age)    // 30

// ─── Object destructuring from a literal ────────────────────────────────────

const { x: ox, y: oy } = { x: 9, y: 4 }
console.log(ox)  // 9
console.log(oy)  // 4

// ─── Object destructuring from a function call ───────────────────────────────

function makeRect(w: number, h: number): { width: number; height: number } {
    let r: { width: number; height: number } = { width: w, height: h }
    return r
}

const { width: rw, height: rh } = makeRect(1920, 1080)
console.log(rw)  // 1920
console.log(rh)  // 1080

// ─── Destructuring inside functions ─────────────────────────────────────────

function sumCoords(obj: { x: number; y: number }): number {
    const { x, y } = obj
    return x + y
}
function makePoint(px: number, py: number): { x: number; y: number } {
    let p: { x: number; y: number } = { x: px, y: py }
    return p
}
console.log(sumCoords(pt))               // 10
console.log(sumCoords(makePoint(4, 6)))  // 10

function dot(p: { x: number; y: number }, q: { x: number; y: number }): number {
    const { x: ax, y: ay } = p
    const { x: bx, y: by } = q
    return ax * bx + ay * by
}

let p1: { x: number; y: number } = { x: 1, y: 2 }
let p2: { x: number; y: number } = { x: 3, y: 4 }
console.log(dot(p1, p2))  // 11

function firstPlusLast(arr: number[]): number {
    const [head] = arr
    let tail: number = arr[arr.length - 1]
    return head + tail
}
console.log(firstPlusLast(coords))           // 60  (10 + 50)
console.log(firstPlusLast(range(1, 5)))      // 1 + 5 = 6
