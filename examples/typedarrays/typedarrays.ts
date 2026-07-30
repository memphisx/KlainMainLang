// ArrayBuffer / TypedArrays — a fixed-length raw byte buffer plus typed
// views over it: Int8Array/Uint8Array/Int16Array/Uint16Array/Int32Array/
// Uint32Array/Float32Array/Float64Array.
//
// Not covered (see docs/tdd/TDD-00018.md): Uint8ClampedArray's real
// clamp-on-write semantics (non-clamped variants wrap instead, matching
// real JS's own mod-2^width behavior), DataView, BigInt64Array/
// BigUint64Array (no bigint support), a TypedArray's `.buffer` property,
// and the 3-argument `new XArray(buffer, byteOffset, length)` sub-range
// view form — only whole-buffer views at offset 0 are supported.

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
