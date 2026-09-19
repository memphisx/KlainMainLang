// The ES2022 error-options bag: `new Error(msg, { cause })` records why an
// error was raised, chaining the underlying failure (ADR-01007). The cause is
// untyped (`any`): a string, a number, or another Error all round-trip, and it
// rides through throw/catch.

const low = new Error("disk read failed")
const high = new Error("config load failed", { cause: low })

console.log(high.message)                 // config load failed
console.log(high.cause instanceof Error)  // true
console.log(String(high.cause))           // Error: disk read failed

const tagged = new TypeError("bad input", { cause: 42 })
console.log(tagged.cause, typeof tagged.cause) // 42 number

// A cause-less error reads undefined, as in Node.
console.log(new Error("plain").cause)     // undefined

try {
  throw high
} catch (e) {
  console.log("caught:", e.message)       // caught: config load failed
  console.log("because:", String(e.cause)) // because: Error: disk read failed
}
