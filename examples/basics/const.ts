// const works like let but communicates that a variable is not reassigned.
// The compiler treats const and let identically at the IR level.

// --- Basic scalar consts ---
const MAX: number = 100
const MIN: number = 0
const PI = 3               // inferred number
const HALF: number = 50

console.log(MAX)    // 100
console.log(MIN)    // 0
console.log(PI)     // 3
console.log(HALF)   // 50

// --- Const strings ---
const LANG: string = 'TypeScript'
const SEP: string = ', '
const EMPTY: string = ''

console.log(LANG)                       // TypeScript
console.log(LANG + SEP + 'compiled')    // TypeScript, compiled
console.log(EMPTY.length)              // 0

// --- Const booleans ---
const YES: boolean = true
const NO: boolean = false

console.log(YES)   // 1
console.log(NO)    // 0

// --- Const in expressions ---
const BASE: number = 10
const FACTOR: number = 3
const RESULT: number = BASE * FACTOR + 5

console.log(RESULT)  // 35

// --- Const in a for loop (loop var, not reassigned after init) ---
const LIMIT: number = 5
let sum: number = 0
for (let i = 0; i < LIMIT; i++) {
    sum = sum + i
}
console.log(sum)  // 10  (0+1+2+3+4)

// --- Const in functions ---
function greet(name: string): string {
    const prefix: string = 'Hello, '
    const suffix: string = '!'
    return prefix + name + suffix
}
console.log(greet('Alice'))  // Hello, Alice!
console.log(greet('Alexandros'))    // Hello, Alexandros!

// --- Const from function return ---
function double(n: number): number {
    return n * 2
}
const D = double(21)
console.log(D)  // 42

// --- Const array (the reference is const, elements are still mutable) ---
const nums = [10, 20, 30]
console.log(nums[0])   // 10
console.log(nums.length) // 3
nums[1] = 99
console.log(nums[1])   // 99

// --- Const object ---
const point: { x: number; y: number } = { x: 5, y: 7 }
console.log(point.x)   // 5
console.log(point.y)   // 7
point.x = 10           // field mutation is still allowed
console.log(point.x)   // 10
