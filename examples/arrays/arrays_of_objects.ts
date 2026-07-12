// --- Array literal of objects ---
let a: { x: number; y: number } = { x: 1, y: 2 }
let b: { x: number; y: number } = { x: 3, y: 4 }
let c: { x: number; y: number } = { x: 5, y: 6 }

let pts: { x: number; y: number }[] = [a, b, c]

// Index read and field access
console.log(pts[0].x)   // 1
console.log(pts[1].y)   // 4
console.log(pts[2].x)   // 5

// Field write through index
pts[0].x = 10
console.log(pts[0].x)   // 10

// Compound field assignment through index
pts[1].y += 100
console.log(pts[1].y)   // 104

// push an object into the array
let d: { x: number; y: number } = { x: 7, y: 8 }
pts.push(d)
console.log(pts[3].x)   // 7
console.log(pts.length)  // 4

// pop returns the removed object
let popped: { x: number; y: number } = pts.pop()
console.log(popped.x)   // 7
console.log(pts.length)  // 3

// --- Type annotation on the array ---
let rows: { x: number; y: number }[] = [a, b]
console.log(rows[0].x)   // 1
console.log(rows[1].x)   // 3

// --- Function accepting and returning an array of objects ---
function sumX(arr: { x: number; y: number }[], n: number): number {
    let total: number = 0
    let i: number = 0
    for (i = 0; i < n; i++) {
        total = total + arr[i].x
    }
    return total
}

console.log(sumX(pts, 3))  // 1 + 3 + 5 => but pts[0].x was changed to 10, so 10+3+5 = 18

// --- Dynamic array of objects via new Array ---
let dyn: { x: number; y: number }[] = new Array<{ x: number; y: number }>(2)
dyn[0] = a
dyn[1] = b
console.log(dyn[0].x)  // 1 (but a.x is still 1, not 10; pts[0] points to same heap object)
console.log(dyn[1].y)  // 4 (but b.y was not changed, pts[1].y was changed to 104; b.y is still 4)
