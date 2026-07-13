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

// Shift operators follow JS's 32-bit semantics, not this compiler's native
// 64-bit `number`: operands wrap to Int32/Uint32, and the shift count is
// masked to 0-31 (a shift count >= 32 wraps around instead of zeroing out).
console.log(1 << 31)          // -2147483648 (Int32 overflow into a negative number)
console.log(-1 >>> 0)         // 4294967295  (Uint32 view of -1)
console.log(1 << 32)          // 1           (shift count 32 masks to 0)
console.log(1 << 33)          // 2           (shift count 33 masks to 1)
