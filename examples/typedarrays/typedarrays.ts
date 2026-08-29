// ArrayBuffer / TypedArrays — a fixed-length raw byte buffer plus typed
// views over it: Int8Array/Uint8Array/Int16Array/Uint16Array/Int32Array/
// Uint32Array/Float32Array/Float64Array.
//
// Not covered (see docs/tdd/TDD-00018.md): a TypedArray's `.buffer`
// property. DataView lives in examples/basics/dataview.ts.

// ── three ways to construct a TypedArray ─────────────────────────────────────

// 1. From a size: an implicit, own, zero-initialized buffer.
const owned: Uint8Array = new Uint8Array(4)
console.log(owned.length)      // 4
console.log(owned[0])          // 0

// 2. From an array literal or another array/TypedArray: copy-construct,
//    coercing each element the same way plain assignment already does —
//    non-clamped writes wrap (mod 2^width), matching real JS.
const fromLiteral: Uint8Array = new Uint8Array([1, 2, 300, -1])
console.log(fromLiteral[2])    // 44  (300 mod 256)
console.log(fromLiteral[3])    // 255 (-1 wraps to 255)

// 3. From an existing ArrayBuffer: a VIEW sharing the same memory, not a
//    copy — this is the one genuinely new mechanism this feature adds.
const buf = new ArrayBuffer(8)
console.log(buf.byteLength)    // 8

const bytes: Uint8Array = new Uint8Array(buf)
const ints: Int32Array = new Int32Array(buf)
bytes[0] = 1
bytes[1] = 0
bytes[2] = 0
bytes[3] = 0
console.log(ints[0])           // 1 — the same 4 bytes, reinterpreted as an i32

bytes[0] = 42
console.log(bytes[0])          // 42
// A second Uint8Array view over the SAME buffer sees the write above.
const bytesAgain: Uint8Array = new Uint8Array(buf)
console.log(bytesAgain[0])     // 42

// The sub-range view form: new XArray(buffer, byteOffset, length?) — still
// a view over buf's own memory, starting at byte 4, 1 element long.
const subView: Int32Array = new Int32Array(buf, 4, 1)
console.log(subView.length)    // 1
subView[0] = 7
console.log(bytes[4])          // 7 — same memory

// ArrayBuffer.slice(): a COPY of a byte sub-range (negative indices count
// from the end, like array .slice) — writes to the copy don't touch buf.
const tail = buf.slice(-4)
console.log(tail.byteLength)   // 4
const tailBytes: Uint8Array = new Uint8Array(tail)
tailBytes[0] = 9
console.log(bytes[4])          // 7 — buf unchanged by the write to the copy

// ── everything a plain number[] already does works here too, for free ──────
// (indexing, .length, .fill, .slice, .reverse, .at, .indexOf, .includes,
// .map/.filter/.reduce/.forEach/.some/.every, for-of, .keys/.values/.entries
// — no TypedArray-specific code needed for any of these)
const nums: Uint8Array = new Uint8Array([10, 20, 30, 40, 50])
const doubled = nums.map((x: number) => x * 2)
console.log(doubled[4])        // 100 — .map() preserves the receiver's own element type
console.log(nums.filter((x: number) => x % 20 === 0).length) // 2
console.log(nums.slice(1, 3)[0]) // 20
console.log(nums.at(-1))       // 50

// ── the two methods only a TypedArray has ───────────────────────────────────
const dst: Uint8Array = new Uint8Array(5)
dst.set(nums, 0)                // copy nums into dst starting at offset 0
console.log(dst[4])             // 50

const view: Uint8Array = nums.subarray(1, 4) // a VIEW, not a copy
console.log(view.length)        // 3
view[0] = 99
console.log(nums[1])            // 99 — the write above is visible through nums too
console.log(view.byteLength)    // 3 (1 byte per element)

// ── Uint8ClampedArray: stores clamp instead of wrapping ─────────────────────
// (floats round half to even, NaN becomes 0 — the spec's ToUint8Clamp)
const clamped = new Uint8ClampedArray([-1, 300, 254.5, 255.5])
console.log(clamped[0], clamped[1], clamped[2], clamped[3]) // 0 255 254 255

// ── BigInt64Array / BigUint64Array: 64-bit elements as real bigints ─────────
// No 2^53 precision loss; elements are written and read as bigint (1n) values.
const bigs = new BigInt64Array(2)
bigs[0] = 9007199254740993n
console.log(bigs[0])            // 9007199254740993n
const ubigs = new BigUint64Array([18446744073709551615n])
console.log(ubigs[0])           // 18446744073709551615n
for (const v of bigs.subarray(0, 1)) { console.log(v) } // 9007199254740993n

// ── growable / resizable buffers ────────────────────────────────────────────
// The {maxByteLength} option reserves the maximum upfront, so existing views
// stay valid across a grow — grow()/resize() only extends the visible length
// (and only up to the max; shrinking or exceeding it throws a RangeError).
const grow = new SharedArrayBuffer(8, { maxByteLength: 32 })
console.log(grow.growable)       // true
grow.grow(16)
console.log(grow.byteLength)     // 16
const rz = new ArrayBuffer(4, { maxByteLength: 8 })
console.log(rz.resizable)        // true
rz.resize(8)
console.log(rz.byteLength)       // 8
