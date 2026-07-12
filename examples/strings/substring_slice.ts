// substring and slice string methods

const s: string = 'Hello, World!'

// ── substring ────────────────────────────────────────────────────────────────
console.log(s.substring(0, 5))     // Hello
console.log(s.substring(7, 12))    // World
console.log(s.substring(7))        // World!
console.log(s.substring(0, 1))     // H

// negative args clamped to 0 in substring
console.log(s.substring(-3, 5))    // Hello   (start clamped to 0)

// start > end swapped in substring
console.log(s.substring(5, 0))     // Hello   (swapped: 0..5)

// ── slice ────────────────────────────────────────────────────────────────────
console.log(s.slice(0, 5))         // Hello
console.log(s.slice(7, 12))        // World
console.log(s.slice(7))            // World!

// negative indices in slice (count from end)
console.log(s.slice(-6))           // orld!   — last 6 chars
console.log(s.slice(-6, -1))       // orld    — last 6, excluding last 1

// ── on string literals ────────────────────────────────────────────────────────
console.log('TypeGo'.substring(0, 4))   // Type
console.log('TypeGo'.slice(-2))         // Go

// ── empty / zero-length ───────────────────────────────────────────────────────
console.log(s.substring(3, 3))     // (empty)
console.log(s.slice(3, 3))         // (empty)

// ── use in a function ────────────────────────────────────────────────────────
function firstWord(str: string): string {
    const idx: number = str.indexOf(' ')
    if (idx < 0) {
        return str
    }
    return str.substring(0, idx)
}
console.log(firstWord('Hello World'))   // Hello
console.log(firstWord('TypeGo'))        // TypeGo

// ── combined slice + substring ────────────────────────────────────────────────
const lang: string = 'TypeScript'
console.log(lang.slice(0, 4))           // Type
console.log(lang.substring(4))          // Script
console.log(lang.slice(-6))             // Script
