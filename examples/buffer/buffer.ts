// Node Buffer: a Uint8Array with string codecs (hex/base64/latin1/utf8),
// comparison helpers, and fixed-width binary accessors.
//
// Not covered: utf16le/ucs2 encodings, string-valued .fill/.indexOf,
// .swap16/32/64, .toJSON, the 4-arg .write form, runtime-computed encoding
// names (an encoding must be a string literal), and Buffer.from(arrayBuffer)
// copies instead of viewing.

// ── construction ────────────────────────────────────────────────────────────
const hello = Buffer.from("Hello");            // utf8 bytes of a string
console.log(hello.length, hello[0])            // 5 72
console.log(Buffer.from("4869", "hex").toString())      // Hi
console.log(Buffer.from("SGVsbG8=", "base64").toString()) // Hello
const zeroed = Buffer.alloc(4)                 // zero-filled
const filled = Buffer.alloc(4, 0xAB)           // byte-filled
console.log(zeroed[0], filled.toString("hex")) // 0 abababab
console.log(Buffer.from([1, 2, 300])[2])       // 44 — wraps like Uint8Array

// ── codecs both ways ────────────────────────────────────────────────────────
console.log(hello.toString("hex"))             // 48656c6c6f
console.log(hello.toString("base64"))          // SGVsbG8=
console.log(Buffer.byteLength("Hello"))        // 5

// ── it IS a Uint8Array: the whole array surface works ───────────────────────
console.log(hello.indexOf(108), hello.includes(111)) // 2 true
console.log(hello.subarray(1, 3).toString())   // el
let sum = 0
for (const b of hello) { sum += b }
console.log(sum)                               // 500

// ── comparison ──────────────────────────────────────────────────────────────
console.log(hello.equals(Buffer.from("Hello"))) // true
console.log(Buffer.compare(Buffer.from("a"), Buffer.from("b"))) // -1
console.log(Buffer.concat([hello, Buffer.from("!")]).toString()) // Hello!

// ── fixed-width binary accessors (a network header, by hand) ───────────────
const pkt = Buffer.alloc(12)
pkt.writeUInt16BE(0xCAFE, 0)                   // big-endian magic
pkt.writeUInt32LE(123456789, 2)                // little-endian length
pkt.writeBigInt64BE(9007199254740993n, 4)      // 64-bit without precision loss
console.log(pkt.readUInt16BE(0))               // 51966
console.log(pkt.readUInt16LE(0))               // 65226 — same bytes, other order
console.log(pkt.readBigInt64BE(4))             // 9007199254740993n
console.log(pkt.write("hi", 10))               // 2 — bytes written, clamped to room
