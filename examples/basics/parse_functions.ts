// parseInt and parseFloat global functions

// ── parseInt ──────────────────────────────────────────────────────────────────
console.log(parseInt('42'))        // 42
console.log(parseInt('-7'))        // -7
console.log(parseInt('  99  '))    // 99  (leading whitespace stripped)
console.log(parseInt('3abc'))      // 3   (stops at first non-digit)
console.log(parseInt('0'))         // 0

// With radix
console.log(parseInt('FF', 16))    // 255
console.log(parseInt('1010', 2))   // 10
console.log(parseInt('17', 8))     // 15

// Round-trip with JSON
const n: number = 123
const s: string = JSON.stringify(n)
console.log(parseInt(s))           // 123

// ── parseFloat ────────────────────────────────────────────────────────────────
console.log(parseFloat('3.14'))    // 3.14
console.log(parseFloat('-2.5'))    // -2.5
console.log(parseFloat('1e3'))     // 1000
console.log(parseFloat('  0.5 ')) // 0.5

// Use result in math
const pi = parseFloat('3.14159265358979')
console.log(Math.floor(pi))        // 3
console.log(Math.round(pi))        // 3

// ── combined ──────────────────────────────────────────────────────────────────
const hex: string = '1A'
const dec = parseInt(hex, 16)
console.log(dec)                   // 26

const raw: string = '2.718'
const e2 = parseFloat(raw)
console.log(Math.floor(e2))        // 2
