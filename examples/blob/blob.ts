// Blob: an immutable binary value with a MIME type, built from mixed
// string / TypedArray / ArrayBuffer / Blob parts.
//
// Not covered: .stream() (use .bytes()/.arrayBuffer()), the `endings`
// option, and spec type-lowercasing (the type string is stored as-is).

const greeting = new Blob(["Hello, ", "world", "!"], { type: "text/plain" })
console.log(greeting.size, greeting.type) // 13 text/plain

// The readers are awaitable, like fetch's Response body readers.
console.log(await greeting.text())        // Hello, world!

// .slice copies a byte sub-range; its type is the contentType argument
// (never inherited — the spec's rule), and negative indices count from
// the end like array .slice.
const word = greeting.slice(7, 12, "text/x-word")
console.log(await word.text(), word.type) // world text/x-word

// Mixed binary parts concatenate byte-for-byte.
const header = new Uint8Array([0x48, 0x69]) // "Hi"
const packet = new Blob([header, " ", greeting])
console.log(packet.size)                  // 16
const bytes = await packet.bytes()        // a Uint8Array copy
console.log(bytes[0], bytes.length)       // 72 16
const buf = await packet.arrayBuffer()    // an ArrayBuffer copy
console.log(buf.byteLength)               // 16
