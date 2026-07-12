// Hex, binary, and octal numeric literals

const red: number = 0xFF0000
console.log(red)           // 16711680

const mask: number = 0xFF
console.log(mask)          // 255

const flagA: number = 0b0001
const flagB: number = 0b0010
const flagC: number = 0b0100
console.log(flagA | flagB | flagC)   // 7
console.log(flagA & 0b0001)          // 1
console.log(flagC >> 1)              // 2

const perms: number = 0o755
console.log(perms)         // 493

// Mixed in expressions
const combined: number = 0xFF & 0b11110000
console.log(combined)      // 240
