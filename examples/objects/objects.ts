// --- Basic object with type annotation ---
let point: { x: number; y: number } = { x: 10, y: 20 }
console.log(point.x)   // 10
console.log(point.y)   // 20

// Field write
point.x = 30
console.log(point.x)   // 30

// Compound field assignment
point.y += 5
console.log(point.y)   // 25

// --- Inferred object type ---
let v = { vx: 3, vy: 4 }
console.log(v.vx)   // 3
console.log(v.vy)   // 4

// --- Object with multiple field types ---
let rect: { x: number; y: number; w: number; h: number } = { x: 0, y: 0, w: 100, h: 50 }
console.log(rect.w)   // 100
console.log(rect.h)   // 50
rect.w *= 2
console.log(rect.w)   // 200

// --- Function returning an object ---
function makePoint(x: number, y: number): { x: number; y: number } {
    let p: { x: number; y: number } = { x: x, y: y }
    return p
}

let p2 = makePoint(7, 8)
console.log(p2.x)   // 7
console.log(p2.y)   // 8

// --- Function taking an object parameter ---
// Objects are passed by reference (heap pointer), so mutations are visible to caller.
function translate(p: { x: number; y: number }, dx: number, dy: number): void {
    p.x = p.x + dx
    p.y = p.y + dy
}

translate(point, 5, 10)
console.log(point.x)   // 35
console.log(point.y)   // 35

// --- Function reading fields ---
function length2(v: { vx: number; vy: number }): number {
    return v.vx * v.vx + v.vy * v.vy
}

console.log(length2(v))   // 25  (3*3 + 4*4)

// --- Objects in arrays ---
let a: { x: number; y: number } = { x: 1, y: 2 }
let b: { x: number; y: number } = { x: 3, y: 4 }
let pts: number[] = [a.x, a.y, b.x, b.y]
console.log(pts[2])   // 3

// --- Shorthand properties ---
// `{ x }` is sugar for `{ x: x }`, referencing the in-scope variable of the same name.
let sx: number = 5
let sy: number = 6
let shorthandPoint = { sx, sy }
console.log(shorthandPoint.sx)   // 5
console.log(shorthandPoint.sy)   // 6

// Shorthand and explicit properties can be mixed freely.
let label: string = 'origin'
let mixed = { label, x: 0, y: 0 }
console.log(mixed.label)   // origin
console.log(mixed.x)       // 0

// Common pattern: shorthand-returning a typed object from a function.
function makeShorthandPoint(x: number, y: number): { x: number; y: number } {
    return { x, y }
}
let sp = makeShorthandPoint(9, 10)
console.log(sp.x)   // 9
console.log(sp.y)   // 10

// --- Object spread ---
// `{ ...obj, key: val }` copies obj's fields, then applies later properties
// in source order — a property mentioned again (spread or explicit) simply
// overwrites the earlier value, without moving its position.
interface Coord { x: number; y: number }
let base: Coord = { x: 1, y: 2 }

let copy = { ...base }
console.log(copy.x)   // 1
console.log(copy.y)   // 2

let moved = { ...base, y: 20 }
console.log(moved.x)   // 1
console.log(moved.y)   // 20

// A spread AFTER an explicit property overrides it (last write wins).
let merged = { x: 100, ...base }
console.log(merged.x)   // 1

// Spreading can also add new fields not present on the source.
let withZ = { ...base, z: 3 }
console.log(withZ.x)   // 1
console.log(withZ.y)   // 2
console.log(withZ.z)   // 3

// Spread is a SHALLOW copy — nested object fields are shared, not cloned.
interface Wrapper { name: string; inner: Coord }
let w1: Wrapper = { name: 'first', inner: { x: 0, y: 0 } }
let w2 = { ...w1, name: 'second' }
w2.inner.x = 99
console.log(w1.inner.x)   // 99 (same underlying inner object)
