// Spread unpacks an array into another array literal.
// Rest parameters collect extra call arguments into an array.

// --- Spread: concatenate two arrays ---
const odds = [1, 3, 5]
const evens = [2, 4, 6]
const all = [...odds, ...evens]
console.log(all.length)  // 6
console.log(all[0])      // 1
console.log(all[3])      // 2

// --- Spread: insert static elements between spreads ---
const head = [0]
const tail = [8, 9]
const full = [...head, 1, 2, 3, ...tail]
console.log(full.length)  // 6
console.log(full[0])      // 0
console.log(full[4])      // 8
console.log(full[5])      // 9

// --- Spread: prepend / append ---
const base = [10, 20, 30]
const withPrefix = [0, ...base]
const withSuffix = [...base, 40]
console.log(withPrefix[0])  // 0
console.log(withPrefix[1])  // 10
console.log(withSuffix[3])  // 40

// --- Spread: copy an array ---
const original = [7, 8, 9]
const copy = [...original]
copy[0] = 99
console.log(original[0])  // 7   (unaffected)
console.log(copy[0])      // 99

// --- Spread of an empty array ---
const empty: number[] = []
const padded = [0, ...empty, 1]
console.log(padded.length)  // 2
console.log(padded[0])      // 0
console.log(padded[1])      // 1

// --- Rest parameters: collect all args ---
function sum(...nums: number[]): number {
    let total: number = 0
    for (const n of nums) {
        total = total + n
    }
    return total
}
console.log(sum(1, 2, 3))         // 6
console.log(sum(10, 20, 30, 40))  // 100
console.log(sum())                // 0

// --- Rest with leading regular parameters ---
function clampAll(lo: number, hi: number, ...vals: number[]): number[] {
    let result = new Array<number>(vals.length)
    for (let i = 0; i < vals.length; i++) {
        let v: number = vals[i]
        result[i] = v < lo ? lo : v > hi ? hi : v
    }
    return result
}
const clamped = clampAll(0, 10, -5, 3, 15, 7)
console.log(clamped[0])  // 0
console.log(clamped[1])  // 3
console.log(clamped[2])  // 10
console.log(clamped[3])  // 7

// --- Rest: inspect length and index ---
function first(...items: number[]): number {
    return items[0]
}
function last(...items: number[]): number {
    return items[items.length - 1]
}
function count(...items: number[]): number {
    return items.length
}
console.log(first(10, 20, 30))  // 10
console.log(last(10, 20, 30))   // 30
console.log(count(1, 2, 3, 4))  // 4

// --- Rest: use the collected array with for...of ---
function sumAndDouble(...nums: number[]): number {
    let s: number = 0
    for (const n of nums) {
        s = s + n
    }
    return s * 2
}
console.log(sumAndDouble(1, 2, 3))  // 12
