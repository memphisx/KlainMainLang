// --- Simple arrow function (no captures) ---
let double = (x: number) => x * 2
console.log(double(5))   // 10
console.log(double(21))  // 42

// --- Arrow function capturing a scalar ---
let base = 100
let addBase = (x: number) => x + base
console.log(addBase(5))   // 105
console.log(addBase(50))  // 150

// --- Block-body arrow function ---
let clamp = (x: number): number => {
    if (x < 0) { return 0; }
    if (x > 100) { return 100; }
    return x
}
console.log(clamp(-5))   // 0
console.log(clamp(50))   // 50
console.log(clamp(200))  // 100

// --- Closure with mutable captured state (counter) ---
function makeCounter(): () => number {
    let count = 0
    let inc = (): number => {
        count = count + 1
        return count
    }
    return inc
}
let c = makeCounter()
console.log(c())  // 1
console.log(c())  // 2
console.log(c())  // 3

// --- Two independent counters share no state ---
let c1 = makeCounter()
let c2 = makeCounter()
console.log(c1())  // 1
console.log(c2())  // 1
console.log(c1())  // 2
console.log(c2())  // 2

// --- Capturing multiple values ---
let scale = 3
let offset = 10
let transform = (x: number) => x * scale + offset
console.log(transform(5))   // 25
console.log(transform(10))  // 40

// --- Immediately-invoked arrow function ---
let r = ((x: number, y: number) => x + y)(7, 8)
console.log(r)  // 15

// --- Arrow function as function parameter ---
function apply(f: (x: number) => number, val: number): number {
    return f(val)
}
let triple = (x: number) => x * 3
console.log(apply(triple, 7))   // 21
console.log(apply(double, 6))   // 12

// --- Closure capturing an object (reference semantics for objects) ---
let point: { x: number; y: number } = { x: 0, y: 0 }
let moveRight = (dx: number): void => {
    point.x = point.x + dx
}
moveRight(5)
console.log(point.x)   // 5
moveRight(3)
console.log(point.x)   // 8

// --- Mutating a captured variable is visible in the enclosing scope too ---
let sum = 0
let addToSum = (n: number): void => {
    sum += n
}
addToSum(1)
addToSum(2)
addToSum(3)
console.log(sum)  // 6

// --- Adder factory ---
function makeAdder(base: number): (x: number) => number {
    return (x: number) => x + base
}
let add5 = makeAdder(5)
let add10 = makeAdder(10)
console.log(add5(3))   // 8
console.log(add10(3))  // 13
console.log(add5(7))   // 12

// --- Return type wrapped in extra disambiguating parens: (() => number) ---
function makeCounter2(): (() => number) {
    let count = 0
    let inc = (): number => {
        count = count + 1
        return count
    }
    return inc
}
let c3 = makeCounter2()
console.log(c3())  // 1
console.log(c3())  // 2

// --- Arrow function assigned to a const, with an explicit return type
// annotation, returning a closure — calling the result now works regardless
// of whether the outer binding is a `function` declaration or an arrow
// function assigned to `const` (previously only the `function` form worked). ---
const middle = (): (() => void) => {
    let n = 0
    return () => { n = n + 1; console.log(n) }
}
const inner = middle()
inner()  // 1
inner()  // 2

// --- Unannotated function/arrow function returning an object literal — the
// caller can now access fields on the result without an explicit return type
// on the callee (previously required one, or field access failed). ---
function makePoint(x, y) { return { x: x, y: y } }
const p4 = makePoint(3, 4)
console.log(p4.x)  // 3
console.log(p4.y)  // 4

const makePoint2 = (x, y) => { return { x: x, y: y } }
const p5 = makePoint2(5, 6)
console.log(p5.x)  // 5
console.log(p5.y)  // 6

// --- Unannotated function returning a plain number, including recursively ---
function addOne(n) { return n + 1 }
console.log(addOne(5))  // 6

function factorial(n) {
    if (n <= 1) { return 1 }
    return n * factorial(n - 1)
}
console.log(factorial(5))  // 120
