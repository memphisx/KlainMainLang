// Logical operators: && and ||
// Both genuinely short-circuit — the right operand is only evaluated when the
// left doesn't already decide the result. (The compound forms &&=/||= live in
// logical_assignment.ts; this file covers the plain binary operators.)

function sideL(): boolean {
    console.log('L evaluated')
    return false
}
function sideR(): boolean {
    console.log('R evaluated')
    return true
}

// --- && : right side runs only when the left is truthy ---
// left is false, so the right side is never evaluated (no "R evaluated" print)
const a: boolean = sideL() && sideR()
console.log(a)   // false

// --- || : right side runs only when the left is falsy ---
// left is true, so the right side is never evaluated (no "L evaluated" print)
const b: boolean = sideR() || sideL()
console.log(b)   // true

// --- The plain truth tables still hold ---
console.log(true && true)    // true
console.log(true && false)   // false
console.log(false && true)   // false (right side skipped)
console.log(true || false)   // true  (right side skipped)
console.log(false || false)  // false

// --- Nesting: each operand can itself be a compound expression ---
const x: number = 5
console.log((x > 0 && x < 10) || x === 42)  // true
console.log(x < 0 && sideR())               // false — sideR() never runs
