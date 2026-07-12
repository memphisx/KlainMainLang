// Bitwise operators
const a: number = 10  // 0b1010
const b: number = 12  // 0b1100

console.log(a & b)    // 8   (AND)
console.log(a | b)    // 14  (OR)
console.log(a ^ b)    // 6   (XOR)
console.log(~a)       // -11 (NOT)
console.log(a << 1)   // 20  (left shift)
console.log(a >> 1)   // 5   (right shift)

// Compound assignments
let x: number = 15
x &= 6
console.log(x)  // 6
x |= 8
console.log(x)  // 14
x ^= 3
console.log(x)  // 13
x <<= 1
console.log(x)  // 26
x >>= 2
console.log(x)  // 6

// Unsigned right shift (large positive for negative input)
const neg: number = -8
const shifted: number = neg >>> 1
console.log(shifted > 0)  // true
