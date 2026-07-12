// Math methods and constants

// ── constants ─────────────────────────────────────────────────────────────────
console.log(Math.PI)     // 3.141592653589793
console.log(Math.E)      // 2.718281828459045
console.log(Math.SQRT2)  // 1.4142135623730951

// ── floor / ceil / round / trunc ─────────────────────────────────────────────
console.log(Math.floor(3.7))   // 3
console.log(Math.floor(-3.2))  // -4
console.log(Math.ceil(3.2))    // 4
console.log(Math.ceil(-3.7))   // -3
console.log(Math.round(3.5))   // 4
console.log(Math.round(3.4))   // 3
console.log(Math.trunc(3.9))   // 3
console.log(Math.trunc(-3.9))  // -3

// floor on integer is a no-op
const n: number = 5
console.log(Math.floor(n))  // 5

// ── abs ───────────────────────────────────────────────────────────────────────
console.log(Math.abs(-10))   // 10
console.log(Math.abs(10))    // 10
console.log(Math.abs(0))     // 0

const fv: number = -3
console.log(Math.abs(fv))   // 3

// ── min / max ─────────────────────────────────────────────────────────────────
console.log(Math.min(3, 7))     // 3
console.log(Math.min(7, 3))     // 3
console.log(Math.max(3, 7))     // 7
console.log(Math.max(7, 3))     // 7
console.log(Math.min(1, 5, 3))  // 1
console.log(Math.max(1, 5, 3))  // 5

// ── sign ──────────────────────────────────────────────────────────────────────
console.log(Math.sign(42))   // 1
console.log(Math.sign(-7))   // -1
console.log(Math.sign(0))    // 0

// ── pow / sqrt ────────────────────────────────────────────────────────────────
console.log(Math.pow(2.0, 10.0))   // 1024
console.log(Math.sqrt(9.0))         // 3

// ── log ───────────────────────────────────────────────────────────────────────
console.log(Math.floor(Math.log2(8.0)))   // 3
console.log(Math.floor(Math.log10(1000.0))) // 3

// ── trig ──────────────────────────────────────────────────────────────────────
// sin(PI/2) ≈ 1.0
const sinHalf = Math.sin(Math.PI / 2.0)
console.log(Math.round(sinHalf))   // 1

// cos(0) = 1.0
const cosZero = Math.cos(0.0)
console.log(Math.round(cosZero))   // 1

// ── hypot ─────────────────────────────────────────────────────────────────────
console.log(Math.hypot(3.0, 4.0))  // 5

// ── inverse trig ──────────────────────────────────────────────────────────────
console.log(Math.round(Math.asin(1.0) * 2.0))  // 3 (asin(1) = PI/2, *2 ≈ PI ≈ 3.14 → rounds to 3)
console.log(Math.acos(1.0))                     // 0
console.log(Math.round(Math.atan(1.0) * 4.0))   // 3 (atan(1) = PI/4, *4 ≈ PI ≈ 3.14 → rounds to 3)
console.log(Math.round(Math.atan2(1.0, 1.0) * 4.0))  // 3

// ── hyperbolic ────────────────────────────────────────────────────────────────
console.log(Math.sinh(0.0))   // 0
console.log(Math.cosh(0.0))   // 1
console.log(Math.tanh(0.0))   // 0

// ── cbrt / expm1 / log1p ──────────────────────────────────────────────────────
console.log(Math.cbrt(27.0))   // 3
console.log(Math.expm1(0.0))   // 0
console.log(Math.log1p(0.0))   // 0

// ── clamp (TypeGo extension) ──────────────────────────────────────────────────
console.log(Math.clamp(5, 0, 10))   // 5
console.log(Math.clamp(-5, 0, 10))  // 0
console.log(Math.clamp(15, 0, 10))  // 10

// ── random ────────────────────────────────────────────────────────────────────
// Math.random() returns a float in [0, 1); just verify it's in range
const r = Math.random()
const inRange = r >= 0.0 && r < 1.0
console.log(inRange)  // 1

// Common pattern: random integer in [0, n)
const roll = Math.floor(Math.random() * 6.0)
const rollOk = roll >= 0 && roll < 6
console.log(rollOk)  // 1
