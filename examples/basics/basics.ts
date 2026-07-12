// =============================================================================
// Primitives and control flow
// =============================================================================

// Basic arithmetic
let x: number = 5 + 3 * 2
let y: number = x - 1

// For loop accumulator
for (let i = 0; i < 10; i++) {
    y = y + i
}

console.log(y)

// JSDoc extended type
/** @type {int32} */
let count = 0

while (count < 5) {
    count += 1
}
console.log(count)

// If / else
if (x > 10) {
    console.log(x)
} else {
    console.log(0)
}

// String output
console.log('done')

// =============================================================================
// Functions
// =============================================================================

// Returns a value
function add(x: number, y: number): number {
    return x + y
}

// Void function
function printLine(n: number): void {
    console.log(n)
}

// Recursion
function factorial(n: number): number {
    if (n <= 1) {
        return 1
    }
    return n * factorial(n - 1)
}

// JSDoc extended type in a function
function sum32(a: number, b: number): number {
    /** @type {int32} */
    let result = a + b
    return result
}

// Call order: uses add() declared above
let r: number = add(3, 4)
printLine(r)              // 7

printLine(factorial(10))  // 3628800

printLine(sum32(100, 200)) // 300

// Forward call: greet() is declared after its call site
greet(42)

function greet(x: number): void {
    console.log(x)        // 42
}
