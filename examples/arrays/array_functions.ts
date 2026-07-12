// =============================================================================
// Arrays as function parameters
// =============================================================================

// Sum all elements
function sum(arr: number[]): number {
    let total: number = 0
    for (let i = 0; i < arr.length; i++) {
        total = total + arr[i]
    }
    return total
}

// In-place multiply every element
function scale(arr: number[], factor: number): void {
    for (let i = 0; i < arr.length; i++) {
        arr[i] = arr[i] * factor
    }
}

// Find max element
function max(arr: number[]): number {
    let m: number = arr[0]
    for (let i = 1; i < arr.length; i++) {
        if (arr[i] > m) {
            m = arr[i]
        }
    }
    return m
}

// Mix of array and scalar params
function clamp(arr: number[], lo: number, hi: number): void {
    for (let i = 0; i < arr.length; i++) {
        if (arr[i] < lo) {
            arr[i] = lo
        }
        if (arr[i] > hi) {
            arr[i] = hi
        }
    }
}

let nums = [3, 1, 4, 1, 5, 9, 2, 6]

console.log(sum(nums))      // 31
console.log(max(nums))      // 9

scale(nums, 2)
console.log(nums[0])        // 6
console.log(sum(nums))      // 62

clamp(nums, 5, 15)
console.log(sum(nums))      // 66

let extra = [100, 200, 300]
console.log(sum(extra))     // 600
console.log(max(extra))     // 300

scale(extra, 3)
console.log(sum(extra))     // 1800

// =============================================================================
// Arrays as function return values
// =============================================================================

// Build an array of n zeros.
function zeros(n: number): number[] {
    let arr = [0, 0, 0, 0, 0]
    for (let i = 0; i < arr.length; i++) {
        arr[i] = 0
    }
    return arr
}

// Return a new array that is a copy scaled by factor.
function scaled(arr: number[], factor: number): number[] {
    let out = [0, 0, 0, 0, 0]
    for (let i = 0; i < arr.length; i++) {
        out[i] = arr[i] * factor
    }
    return out
}

// Return the first three elements of a fixed-length (6-element) array.
function firstThree(arr: number[]): number[] {
    let out = [0, 0, 0]
    out[0] = arr[0]
    out[1] = arr[1]
    out[2] = arr[2]
    return out
}

// Return the array parameter directly.
function identity(arr: number[]): number[] {
    return arr
}

let z = zeros(5)
console.log(z.length)  // 5
console.log(z[0])      // 0
console.log(z[4])      // 0

let src = [10, 20, 30, 40, 50]
let s = scaled(src, 3)
console.log(s[0])      // 30
console.log(s[2])      // 90
console.log(s[4])      // 150

let t = firstThree(src)
console.log(t.length)  // 3
console.log(t[0])      // 10
console.log(t[2])      // 30

let id = identity(src)
console.log(id[1])     // 20
console.log(id.length) // 5

// Chain: scale the already-scaled array
let s2 = scaled(s, 2)
console.log(s2[0])     // 60
console.log(s2[4])     // 300
