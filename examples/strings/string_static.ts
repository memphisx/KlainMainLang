// String static methods: fromCharCode, fromCodePoint

// ── basic ASCII ───────────────────────────────────────────────────────────────
console.log(String.fromCharCode(65))           // A
console.log(String.fromCharCode(72, 105))      // Hi
console.log(String.fromCharCode(72, 101, 108, 108, 111))  // Hello

// ── digits and punctuation ────────────────────────────────────────────────────
console.log(String.fromCharCode(48))           // 0
console.log(String.fromCharCode(57))           // 9
console.log(String.fromCharCode(33))           // !
console.log(String.fromCharCode(32))           // (space)

// ── lowercase ─────────────────────────────────────────────────────────────────
console.log(String.fromCharCode(97, 98, 99))   // abc

// ── fromCodePoint (BMP alias) ─────────────────────────────────────────────────
console.log(String.fromCodePoint(84, 121, 112, 101, 71, 111))  // TypeGo

// ── zero-length ───────────────────────────────────────────────────────────────
const empty = String.fromCharCode()
console.log(empty.length)                      // 0

// ── runtime values ────────────────────────────────────────────────────────────
function charFromCode(n: number): string {
    return String.fromCharCode(n)
}
console.log(charFromCode(65))   // A
console.log(charFromCode(90))   // Z

// ── round-trip with charCodeAt (via array of codes) ──────────────────────────
// Build 'ABC' from char codes 65,66,67 and verify each character
const abc: string = String.fromCharCode(65, 66, 67)
console.log(abc)           // ABC
console.log(abc[0])        // A
console.log(abc[1])        // B
console.log(abc[2])        // C
console.log(abc.length)    // 3

// ── practical: hex digit encoding ────────────────────────────────────────────
function hexChar(n: number): string {
    if (n < 10) {
        return String.fromCharCode(48 + n)   // '0'..'9'
    }
    return String.fromCharCode(55 + n)       // 'A'..'F'  (65 - 10 = 55)
}
console.log(hexChar(0))   // 0
console.log(hexChar(9))   // 9
console.log(hexChar(10))  // A
console.log(hexChar(15))  // F
