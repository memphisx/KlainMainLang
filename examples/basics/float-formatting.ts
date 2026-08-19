// JS-faithful float printing (TDD-00080): floats print the shortest decimal
// string that round-trips to the same value, exactly as JavaScript's
// Number.prototype.toString does — replacing the old bare-%g formatting that
// truncated to 6 significant digits. See docs/status/TYPE-SYSTEM.md.

// The classic floating-point example: full precision is preserved.
/** @type {float64} */
const sum = 0.1 + 0.2
console.log(sum)                 // 0.30000000000000004

// Shortest representation — no spurious trailing digits.
/** @type {float64} */
const tenth = 0.1
console.log(tenth)               // 0.1

// Integer-valued floats print without a decimal point.
/** @type {float64} */
const whole = 42.0
console.log(whole)               // 42

// Many significant digits are kept.
/** @type {float64} */
const pi = 3.141592653589793
console.log(pi)                  // 3.141592653589793
console.log(`pi ≈ ${pi}`)        // pi ≈ 3.141592653589793

// Special values match JavaScript.
console.log(NaN)                 // NaN
console.log(Infinity)            // Infinity
console.log(-Infinity)           // -Infinity

// Very large / very small magnitudes switch to exponential notation.
/** @type {float64} */
const large = 1000000000000.0 * 1000000000000.0
console.log(large)               // 1e+24

/** @type {float64} */
const small = 1.0 / 10000000.0
console.log(small)               // 1e-7
