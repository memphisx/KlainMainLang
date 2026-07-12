// crypto.getRandomValues / crypto.randomUUID — cryptographically-secure
// randomness (arc4random_buf on macOS/BSD, getrandom() on Linux — a real
// CSPRNG, not the weaker portable fallback Math.random() uses on some
// platforms).

// ── crypto.getRandomValues(buffer) ──────────────────────────────────────────
// Fills an existing array's elements with random byte values (0-255 each).
// A deliberate deviation from the real API, which fills a TypedArray in
// place: this compiler has no ArrayBuffer/TypedArrays yet, so a plain
// number[] stands in as the "buffer."
let buf: number[] = new Array<number>(16)
crypto.getRandomValues(buf)
console.log(buf.length)   // 16

let allInRange = true
for (const b of buf) {
    if (b < 0 || b > 255) { allInRange = false }
}
console.log(allInRange)   // 1 (true) — every value is a valid byte

// ── crypto.randomUUID() ──────────────────────────────────────────────────────
// A standard RFC 4122 version-4 UUID string:
// "xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx" — the "4" and the "y" (one of
// 8/9/a/b) are fixed by the version/variant bits, not random.
const id1: string = crypto.randomUUID()
const id2: string = crypto.randomUUID()
console.log(id1.length)      // 36
console.log(id1 !== id2)     // 1 (true) — two calls give two different UUIDs
console.log(id1[8])          // -
console.log(id1[13])         // -
console.log(id1[14])         // 4 (the version nibble, always 4)
console.log(id1[18])         // -
console.log(id1[23])         // -
