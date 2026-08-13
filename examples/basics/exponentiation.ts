// Exponentiation operator: ** (ES2016) and its compound form **=

// --- Integer operands: exact i64 result (matching this compiler's number model) ---
console.log(2 ** 10)   // 1024
console.log(3 ** 4)    // 81
console.log(2 ** 0)    // 1  (anything ** 0 is 1)

// --- Right-associative: 2 ** 3 ** 2 === 2 ** (3 ** 2) === 2 ** 9 ---
console.log(2 ** 3 ** 2)  // 512

// --- Binds tighter than * / % ---
console.log(5 ** 2 * 2)   // 50  (== (5 ** 2) * 2, not 5 ** (2 * 2))
console.log(10 ** 3 + 1)  // 1001

// --- A negative exponent truncates to 0 in the integer model ---
// (like this compiler's integer division; use a float operand for real
//  fractional powers)
console.log(2 ** -1)   // 0

// --- Float operands route through libm pow(), yielding a float ---
const base: float64 = 2.0
console.log(base ** 10.0)  // 1024
console.log(base ** 0.5)   // 1.4142135... (square root)

// --- The compound **= assignment ---
let n: number = 2
n **= 5
console.log(n)   // 32
n **= 2
console.log(n)   // 1024

// --- Parentheses disambiguate a unary operator on the left ---
// `-2 ** 2` on its own is a SyntaxError (ambiguous); parenthesize:
console.log((-2) ** 2)   // 4   (square of -2)
console.log(-(2 ** 2))   // -4  (negate 2 squared)
