// =============================================================================
// Array literals
// =============================================================================

// Inferred type: number[] → i64[]
let nums = [10, 20, 30, 40, 50]

// Read element
console.log(nums[0])   // 10
console.log(nums[4])   // 50

// Write element
nums[2] = 99
console.log(nums[2])   // 99

// Length
console.log(nums.length)  // 5

// Explicit type annotation: int32[]
/** @type {int32[]} */
let small: number[] = [1, 2, 3]
console.log(small[1])  // 2

// Float array
let floats = [1.5, 2.5, 3.5]
console.log(floats[0]) // 1.5

// Iterate with for loop
let sum: number = 0
for (let i = 0; i < nums.length; i++) {
    sum = sum + nums[i]
}
console.log(sum)  // 10+20+99+40+50 = 219

// Compound element assignment
nums[0] += 5
console.log(nums[0])  // 15

// Array element passed to a function
function double(x: number): number {
    return x * 2
}
console.log(double(nums[1]))  // 40

// =============================================================================
// Dynamic arrays  (new Array<T>(n))
// =============================================================================

// Size from a variable
let n: number = 5
let arr = new Array<number>(n)
console.log(arr.length)  // 5
console.log(arr[0])      // 0  (calloc zero-initialises)

// Fill with squares
for (let i = 0; i < arr.length; i++) {
    arr[i] = i * i
}
console.log(arr[0])  // 0
console.log(arr[2])  // 4
console.log(arr[4])  // 16

// Size from an expression
let m: number = 3
let buf = new Array<number>(m * 2)
console.log(buf.length)  // 6

// With explicit type annotation, no generic needed
let typed: number[] = new Array(4)
console.log(typed.length) // 4
console.log(typed[3])     // 0

// Function that builds a range [0..n-1]
function range(n: number): number[] {
    let out = new Array<number>(n)
    for (let i = 0; i < n; i++) {
        out[i] = i
    }
    return out
}

let r = range(6)
console.log(r.length)  // 6
console.log(r[0])      // 0
console.log(r[5])      // 5

// Function that doubles every element into a new array
function doubled(arr: number[]): number[] {
    let out = new Array<number>(arr.length)
    for (let i = 0; i < arr.length; i++) {
        out[i] = arr[i] * 2
    }
    return out
}

let d = doubled(r)
console.log(d[3])  // 6
console.log(d[5])  // 10
