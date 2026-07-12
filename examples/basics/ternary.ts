// Ternary operator: condition ? valueIfTrue : valueIfFalse
// Emits alloca + branch + store/load, so both branches can produce one result.

// --- Basic number ternary ---
let x: number = 10
console.log(x > 5 ? 1 : 0)    // 1
console.log(x > 50 ? 1 : 0)   // 0

// --- Assign ternary result ---
let label: string = x > 5 ? 'big' : 'small'
console.log(label)   // big

let abs: number = x >= 0 ? x : -x
console.log(abs)     // 10

let neg: number = -3
let absNeg: number = neg >= 0 ? neg : -neg
console.log(absNeg)  // 3

// --- Ternary with booleans ---
let flag: boolean = true
let msg: string = flag ? 'yes' : 'no'
console.log(msg)   // yes

// --- Nested ternary (right-associative) ---
let score: number = 75
let grade: string = score >= 90 ? 'A' : score >= 75 ? 'B' : score >= 60 ? 'C' : 'F'
console.log(grade)   // B

let score2: number = 55
let grade2: string = score2 >= 90 ? 'A' : score2 >= 75 ? 'B' : score2 >= 60 ? 'C' : 'F'
console.log(grade2)  // F

// --- Ternary in arithmetic ---
let a: number = 4
let b: number = 7
let bigger: number = a > b ? a : b
console.log(bigger)  // 7

let smaller: number = a < b ? a : b
console.log(smaller) // 4

// --- Ternary for clamp ---
function clamp(v: number, lo: number, hi: number): number {
    return v < lo ? lo : v > hi ? hi : v
}
console.log(clamp(-5, 0, 10))   // 0
console.log(clamp(5, 0, 10))    // 5
console.log(clamp(15, 0, 10))   // 10

// --- Ternary for sign ---
function sign(n: number): number {
    return n > 0 ? 1 : n < 0 ? -1 : 0
}
console.log(sign(42))    //  1
console.log(sign(-7))    // -1
console.log(sign(0))     //  0

// --- Ternary to choose between two computed values ---
function maxOf(a: number, b: number): number {
    return a >= b ? a : b
}
function minOf(a: number, b: number): number {
    return a <= b ? a : b
}
console.log(maxOf(3, 9))    // 9
console.log(maxOf(9, 3))    // 9
console.log(minOf(3, 9))    // 3
console.log(minOf(9, 3))    // 3

// --- Ternary with string comparison ---
let lang: string = 'go'
let verdict: string = lang === 'go' ? 'compiled' : 'interpreted'
console.log(verdict)  // compiled

// --- Ternary inside a loop ---
let evens: number = 0
let odds: number = 0
for (let i = 0; i < 10; i++) {
    let bucket: number = i % 2 === 0 ? 1 : 0
    evens = evens + bucket
    odds = odds + (bucket === 0 ? 1 : 0)
}
console.log(evens)  // 5
console.log(odds)   // 5
