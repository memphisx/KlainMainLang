// Number static methods and constants, plus global isNaN / isFinite

// ── Number constants ──────────────────────────────────────────────────────────
console.log(Number.MAX_SAFE_INTEGER)   // 9007199254740991
console.log(Number.MIN_SAFE_INTEGER)   // -9007199254740991

// ── Number.isInteger ──────────────────────────────────────────────────────────
console.log(Number.isInteger(42))      // 1
console.log(Number.isInteger(-5))      // 1
console.log(Number.isInteger(3.0))     // 1  (3.0 has no fractional part)
console.log(Number.isInteger(3.5))     // 0
console.log(Number.isInteger(0.0))     // 1

// ── Number.isFinite ───────────────────────────────────────────────────────────
console.log(Number.isFinite(42))                    // 1
console.log(Number.isFinite(3.14))                  // 1
console.log(Number.isFinite(Number.POSITIVE_INFINITY)) // 0
console.log(Number.isFinite(Number.NEGATIVE_INFINITY)) // 0
console.log(Number.isFinite(Number.NaN))            // 0

// ── Number.isNaN ──────────────────────────────────────────────────────────────
console.log(Number.isNaN(42))          // 0
console.log(Number.isNaN(3.14))        // 0
console.log(Number.isNaN(Number.NaN))  // 1

// ── Number.isSafeInteger ──────────────────────────────────────────────────────
console.log(Number.isSafeInteger(42))                       // 1
console.log(Number.isSafeInteger(Number.MAX_SAFE_INTEGER))  // 1
console.log(Number.isSafeInteger(3.5))                      // 0

// ── Number.parseInt / Number.parseFloat ──────────────────────────────────────
console.log(Number.parseInt('42'))       // 42
console.log(Number.parseInt('FF', 16))   // 255
console.log(Number.parseFloat('3.14'))   // 3.14

// ── global isNaN / isFinite ───────────────────────────────────────────────────
console.log(isNaN(42))               // 0
console.log(isNaN(Number.NaN))       // 1
console.log(isFinite(100))           // 1
console.log(isFinite(Number.POSITIVE_INFINITY)) // 0

// ── type inference without annotation ────────────────────────────────────────
const big = Number.MAX_SAFE_INTEGER
console.log(big)                     // 9007199254740991

const eps = Number.EPSILON
console.log(Number.isFinite(eps))    // 1

// ── practical: clamp to safe range ───────────────────────────────────────────
function safeMul(a: number, b: number): number {
    const result = a * b
    if (Number.isSafeInteger(result)) {
        return result
    }
    return Number.MAX_SAFE_INTEGER
}
console.log(safeMul(1000, 1000))        // 1000000
console.log(safeMul(100000000, 100000000)) // 9007199254740991 (clamped)

// ── Number.prototype instance methods ─────────────────────────────────────────
console.log((3.14159).toFixed(2))       // 3.14
console.log((1).toPrecision(4))         // 1.000
console.log((0.0012345).toPrecision(2)) // 0.0012
console.log((1234).toExponential(2))    // 1.23e+03
console.log((255).toString(16))         // ff
console.log((255).toString(2))          // 11111111
console.log((-255).toString(16))        // -ff
console.log((42).toString())            // 42 (radix defaults to 10)

// ── exponent (scientific) notation in number literals ─────────────────────────
console.log(1e3)          // 1000
console.log(1.5e3)        // 1500
console.log(2e-2)         // 0.02
console.log(6.022e23)     // 6.022e+23
const avogadro = 6.022e23
console.log(avogadro > 1e23)  // true

// ── Number.isInteger is false for any non-finite value ────────────────────────
console.log(Number.isInteger(Infinity))   // false
console.log(Number.isInteger(-Infinity))  // false
console.log(Number.isInteger(NaN))        // false
