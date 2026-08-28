// Plain TypeScript that `tsc` still accepts — the JSDoc `@type` annotation is
// erasable, so this stays valid TS. But this compiler reads the width and gives
// `number` real machine-integer semantics, a precision knob standard TS lacks.
// See docs/status/TYPE-SYSTEM.md.

// An 8-bit unsigned integer wraps at its width, exactly like C / a typed array.
/** @type {uint8} */
let r = 255
r = r + 1
console.log(r)                    // 0  (wrapped at 8 bits)

// Single-precision float: narrower than the default IEEE-754 double.
/** @type {float32} */
let ratio = 1 / 3
console.log(ratio)                // 0.3333333432674408  (float32 rounding)

// A bare `number` is a JS-faithful double — same 2**53 precision ceiling as JS.
let big = 9007199254740993        // 2**53 + 1
console.log(big)                  // 9007199254740992  (precision loss, as in JS)

// ...but a JSDoc width fixes it: an int64 literal is parsed straight to a 64-bit
// integer, so 2**53 + 1 survives exactly — precision plain JS/TS can't express.
/** @type {int64} */
let bigExact = 9007199254740993
console.log(bigExact)             // 9007199254740993  (exact, via int64)

// Sized integers interconvert cleanly with the default float `number`.
/** @type {int64} */
let count = 1000000
console.log(count * 2)            // 2000000
