// Tuple types: [T0, T1, ...] — a fixed-arity, heterogeneous, positional value.
// Stored as a fixed-shape struct, so it's cheap and its per-position types are
// known at compile time.

// --- Declaration and constant-index access ---
const point: [number, number] = [3, 4]
console.log(point[0])              // 3
console.log(point[1])              // 4

const labeled: [string, number] = ["temperature", 21]
console.log(labeled[0])            // temperature
console.log(labeled[1])            // 21

// --- Destructuring ---
const [name, value] = labeled
console.log(name + " = " + value)  // temperature = 21

// --- Heterogeneous elements, including arrays and nested tuples ---
const mixed: [string, number[], [number, boolean]] = ["tags", [1, 2, 3], [7, true]]
console.log(mixed[0])              // tags
console.log(mixed[1][2])           // 3
console.log(mixed[2][0])           // 7
console.log(mixed[2][1])           // true

// --- Tuple as a function parameter and return value ---
function divmod(a: number, b: number): [number, number] {
    return [Math.floor(a / b), a % b]
}
const [q, r] = divmod(17, 5)
console.log("17 / 5 = " + q + " remainder " + r)   // 17 / 5 = 3 remainder 2

// --- An array of tuples, iterated with the standard destructuring idiom ---
const scores: [string, number][] = [["alice", 90], ["bob", 85]]
for (const [who, score] of scores) {
    console.log(who + ": " + score)               // alice: 90, bob: 85
}

// --- .entries() yields real [key, value] / [index, value] tuples ---
const inventory = new Map<string, number>()
inventory.set("apples", 12)
inventory.set("pears", 7)
for (const [item, count] of inventory.entries()) {
    console.log(item + " x" + count)              // apples x12, pears x7
}

const colors = ["red", "green", "blue"]
for (const [i, color] of colors.entries()) {
    console.log(i + " => " + color)               // 0 => red, ...
}

// --- Rendering: a tuple prints/serializes like an array ---
console.log(`${point}`)                            // 3,4
console.log(JSON.stringify(labeled))               // ["temperature",21]

// --- Destructured callback parameters (TDD-00199) ---
// The dominant idiom for iterating [key, value] pairs with a higher-order
// method: destructure the tuple element right in the callback's parameter
// list. The element type flows from the receiver, so no annotation is needed.
const prices = new Map<string, number>()
prices.set("pen", 2)
prices.set("mug", 8)
const lines = [...prices.entries()].map(([item, cost]) => item + ": $" + cost)
console.log(lines.join(", "))                      // pen: $2, mug: $8

const config = { width: 640, height: 480, depth: 24 }
const big = Object.entries(config)
    .filter(([key, val]) => val >= 100)
    .map(([key, val]) => key)
console.log(big.join(","))                         // width,height

const pairs: [string, number][] = [["a", 3], ["b", 4]]
const total = pairs.reduce((sum, [key, n]) => sum + n, 0)
console.log(total)                                 // 7
