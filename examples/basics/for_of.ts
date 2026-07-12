// for...of iterates the elements of an array without a manual index variable.
// Supports const (read-only intent) and let (allows reassignment inside body).

// --- Basic iteration ---
const nums = [10, 20, 30, 40, 50]

for (const n of nums) {
    console.log(n)   // 10  20  30  40  50
}

// --- Accumulate ---
let sum: number = 0
for (const n of nums) {
    sum = sum + n
}
console.log(sum)  // 150

// --- Find max ---
let max: number = nums[0]
for (const n of nums) {
    if (n > max) {
        max = n
    }
}
console.log(max)  // 50

// --- Count matching elements ---
const scores = [55, 80, 90, 45, 75, 88]
let passing: number = 0
for (const s of scores) {
    if (s >= 60) {
        passing = passing + 1
    }
}
console.log(passing)  // 4

// --- Multiple for...of loops with the same variable name ---
const a = [1, 2, 3]
const b = [4, 5, 6]

let sumA: number = 0
for (const x of a) {
    sumA = sumA + x
}
let sumB: number = 0
for (const x of b) {   // same name 'x' — no conflict
    sumB = sumB + x
}
console.log(sumA)   // 6
console.log(sumB)   // 15

// --- for...of with let (can mutate loop variable locally) ---
let product: number = 1
for (let v of nums) {
    v = v * 2         // local mutation; does not affect nums
    product = product + v
}
console.log(product)  // 1 + 20+40+60+80+100 = 301

// --- String arrays ---
const langs = ['go', 'typescript', 'c']
for (const lang of langs) {
    console.log(lang)   // go  typescript  c
}

// --- for...of in a function ---
function sumArray(arr: number[]): number {
    let total: number = 0
    for (const elem of arr) {
        total = total + elem
    }
    return total
}

console.log(sumArray(nums))    // 150
console.log(sumArray(scores))  // 433

function allAbove(arr: number[], threshold: number): boolean {
    for (const v of arr) {
        if (v <= threshold) {
            return false
        }
    }
    return true
}

const high = [70, 80, 90]
console.log(allAbove(high, 60))   // 1
console.log(allAbove(high, 75))   // 0

// --- for...of over array of objects ---
let p0: { x: number; y: number } = { x: 1, y: 2 }
let p1: { x: number; y: number } = { x: 3, y: 4 }
let p2: { x: number; y: number } = { x: 5, y: 6 }
let points: { x: number; y: number }[] = [p0, p1, p2]

let totalX: number = 0
for (const p of points) {
    totalX = totalX + p.x
    console.log(p.x)   // 1  3  5
}
console.log(totalX)   // 9

// --- break inside for...of ---
let found: number = -1
for (const n of nums) {
    if (n > 25) {
        found = n
        break
    }
}
console.log(found)  // 30
