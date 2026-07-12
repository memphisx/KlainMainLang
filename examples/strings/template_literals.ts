// --- Plain template (no interpolation) ---
let plain: string = `hello world`
console.log(plain)  // hello world

// --- Single string interpolation ---
let name: string = 'Alice'
console.log(`Hello, ${name}!`)  // Hello, Alice!

// --- Number interpolation ---
let x: number = 42
console.log(`x = ${x}`)         // x = 42

// --- Multiple interpolations ---
let a: number = 3
let b: number = 4
console.log(`${a} + ${b} = ${a + b}`)  // 3 + 4 = 7

// --- Expression interpolation ---
let radius: number = 5
console.log(`area = ${radius * radius}`)  // area = 25

// --- Boolean interpolation ---
let flag: boolean = true
console.log(`flag is ${flag}`)  // flag is 1

// --- Float interpolation ---
let pi: number = 3
let approx: string = `pi ≈ ${pi}`
console.log(approx)  // pi ≈ 3

// --- Nested concatenation in template ---
let first: string = 'John'
let last: string = 'Doe'
console.log(`Name: ${first + ' ' + last}`)  // Name: John Doe

// --- Template in a function ---
function greet(who: string, n: number): string {
    return `Hello, ${who}! You have ${n} messages.`
}
console.log(greet('Alexandros', 5))   // Hello, Alexandros! You have 5 messages.
console.log(greet('Carol', 0)) // Hello, Carol! You have 0 messages.

// --- Template assigned to variable and printed ---
let msg: string = `The answer is ${6 * 7}`
console.log(msg)  // The answer is 42

// --- Escaped backtick and dollar ---
console.log(`price: \$${100}`)  // price: $100
