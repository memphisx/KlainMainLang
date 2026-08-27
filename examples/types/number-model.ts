// `number` is a JS-faithful IEEE-754 double (TDD-00123). Untyped arithmetic
// matches JavaScript exactly; the explicit integer types (int8..int64,
// uint8..uint64) remain the opt-in escape hatch for real integer semantics.
// See docs/status/TYPE-SYSTEM.md.

// --- floating-point fidelity ---
console.log(0.1 + 0.2)        // 0.30000000000000004
console.log(10 / 3)          // 3.3333333333333335
console.log(5 / 2)           // 2.5
console.log(2 ** -1)         // 0.5  (a negative exponent is a real fraction)

// --- integer-valued results still print cleanly (no trailing .0) ---
console.log(3 * 4)           // 12
console.log(7 % 3)           // 1
console.log(0xff)            // 255  (hex/bin/oct literals are numbers too)

// --- division by zero is Infinity/NaN, as in JS (it does not throw) ---
console.log(1 / 0)           // Infinity
console.log(-1 / 0)          // -Infinity
console.log(0 / 0)           // NaN

// --- the integer escape hatch keeps integer semantics ---
let a: int32 = 7
let b: int32 = 2
console.log(a / b)           // 3   (both operands integer-typed -> integer division)
console.log(a / 2)           // 3.5 (a bare literal is a number -> promotes to float)

// an int64/uint64 literal is exact past 2^53 (a plain `number` would round it,
// as JS does) — the escape hatch is parsed straight to a 64-bit integer
let big: int64 = 9007199254740993
console.log(big)             // 9007199254740993  (a plain number rounds to ...992)
let umax: uint64 = 18446744073709551615
console.log(umax)            // 18446744073709551615  (unsigned, prints via %llu)

// --- bitwise operators use JS 32-bit semantics, and their result is a number ---
console.log(5 & 3)           // 1
console.log(1 << 4)          // 16
console.log((7 & 3) / 2)     // 1.5  (the result participates in float division)
console.log((7 & 6) / (7 & 5)) // 1.2  (both operands bitwise -> still a float divide)
console.log(~5)              // -6   (~x is ToInt32(x) ^ -1, a number)
console.log(-1 >>> 0)        // 4294967295 (unsigned 32-bit, as a number)
