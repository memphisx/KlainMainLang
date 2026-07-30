package tests

import (
	"testing"
)

// --- ArrayBuffer / TypedArrays (see docs/adr/ADR-00078.md, docs/tdd/TDD-00018.md) ---

func TestE2EArrayBufferByteLength(t *testing.T) {
	assertOutput(t, `
const buf = new ArrayBuffer(16)
console.log(buf.byteLength)
`, "16")
}

func TestE2ETypedArrayFromSize(t *testing.T) {
	assertOutput(t, `
const a: Uint8Array = new Uint8Array(4)
console.log(a.length)
console.log(a.byteLength)
console.log(a[0])
`, "4\n4\n0")
}

func TestE2ETypedArrayIndexingWraparound(t *testing.T) {
	// Non-clamped TypedArray writes wrap (mod 2^width), matching real JS —
	// Uint8ClampedArray's real clamp-instead-of-wrap semantics are out of
	// scope (TDD-00018), so only wraparound needs covering here.
	assertOutput(t, `
const a: Uint8Array = new Uint8Array(2)
a[0] = 300
a[1] = -1
console.log(a[0])
console.log(a[1])
`, "44\n255")
}

func TestE2ETypedArrayFromArrayLiteral(t *testing.T) {
	assertOutput(t, `
const a: Uint8Array = new Uint8Array([1, 2, 300, -1])
console.log(a.length)
console.log(a[0])
console.log(a[2])
console.log(a[3])
`, "4\n1\n44\n255")
}

func TestE2ETypedArrayCopyConstructFromAnotherTypedArray(t *testing.T) {
	assertOutput(t, `
const a: Uint8Array = new Uint8Array([1, 2, 44])
const b: Int16Array = new Int16Array(a)
console.log(b.length)
console.log(b[2])
`, "3\n44")
}

func TestE2EArrayBufferViewSharesMemory(t *testing.T) {
	// The one genuinely new mechanism this feature needed: one allocation,
	// multiple typed views, writes visible across all of them.
	assertOutput(t, `
const buf = new ArrayBuffer(8)
const view1: Uint8Array = new Uint8Array(buf)
const view2: Uint8Array = new Uint8Array(buf)
view1[0] = 42
console.log(view2[0])
view2[1] = 100
console.log(view1[1])
`, "42\n100")
}

func TestE2ETypedArrayViewsOfDifferentElementTypesShareBytes(t *testing.T) {
	// A Uint8Array view and an Int32Array view over the same buffer see
	// each other's writes reinterpreted at their own element width —
	// the core distinguishing ArrayBuffer/TypedArray behavior.
	assertOutput(t, `
const buf = new ArrayBuffer(8)
const bytes: Uint8Array = new Uint8Array(buf)
bytes[0] = 1
bytes[1] = 0
bytes[2] = 0
bytes[3] = 0
const ints: Int32Array = new Int32Array(buf)
console.log(ints.length)
console.log(ints[0])
`, "2\n1")
}

func TestE2ETypedArrayBufferLengthNotMultipleOfElementSizeThrows(t *testing.T) {
	assertOutput(t, `
try {
  const buf = new ArrayBuffer(3)
  const v: Int32Array = new Int32Array(buf)
  console.log(v.length)
} catch (e) {
  console.log("caught: " + e.message)
}
`, "caught: ArrayBuffer length is not a multiple of the element size")
}

func TestE2ETypedArraySet(t *testing.T) {
	assertOutput(t, `
const a: Uint8Array = new Uint8Array([10, 20, 30, 40, 50])
const b: Uint8Array = new Uint8Array(5)
b.set(a, 0)
console.log(b[0])
console.log(b[4])
`, "10\n50")
}

func TestE2ETypedArraySetTooLargeThrows(t *testing.T) {
	assertOutput(t, `
try {
  const a: Uint8Array = new Uint8Array(2)
  const b: Uint8Array = new Uint8Array([1, 2, 3])
  a.set(b, 0)
  console.log(a[0])
} catch (e) {
  console.log("caught: " + e.message)
}
`, "caught: source is too large for set()'s target, starting at the given offset")
}

func TestE2ETypedArraySubarrayIsAView(t *testing.T) {
	assertOutput(t, `
const a: Uint8Array = new Uint8Array([10, 20, 30, 40, 50])
const sub: Uint8Array = a.subarray(1, 4)
console.log(sub.length)
console.log(sub[0])
sub[0] = 99
console.log(a[1])
`, "3\n20\n99")
}

func TestE2ETypedArrayMapPreservesElementType(t *testing.T) {
	// Regression test for a real bug found while implementing this feature:
	// .map() must return the same TypedArray kind as the receiver (real JS
	// semantics), not whatever type the callback expression itself would
	// naturally produce — an unannotated arrow callback's own inferred
	// return type silently defaulted to i64 while the receiver was
	// Uint8Array's i8, corrupting every index but the first once read back
	// at the receiver's narrower element stride.
	assertOutput(t, `
const a: Uint8Array = new Uint8Array([10, 20, 30, 40, 50])
const doubled: Uint8Array = a.map((x: number) => x * 2)
console.log(doubled[0])
console.log(doubled[1])
console.log(doubled[4])
`, "20\n40\n100")
}

func TestE2ETypedArrayUnannotatedMapPreservesElementType(t *testing.T) {
	// Regression test for a second instance of the same bug class, in a
	// completely separate code path: emit_exprs_types.go's inferExprType
	// (used to decide an *unannotated* variable's declared type) had its
	// own independent "map" case that computed the result type from the
	// callback body's own inferred type, disagreeing with what
	// emitArrayMap actually emits for a TypedArray receiver. Without the
	// matching fix there, `const doubled = a.map(...)` (no `: Uint8Array`
	// annotation) declared `doubled` as a plain 8-byte-element number[]
	// while the real data was written at the receiver's 1-byte stride —
	// found directly via examples/typedarrays/typedarrays.ts, not by
	// inspection.
	assertOutput(t, `
const a: Uint8Array = new Uint8Array([10, 20, 30, 40, 50])
const doubled = a.map((x: number) => x * 2)
console.log(doubled[0])
console.log(doubled[4])
`, "20\n100")
}

func TestE2ETypedArrayReusedArrayMethods(t *testing.T) {
	// Confirms the "everything else is free" claim for a representative
	// sample: .filter, for-of, .slice, .reverse, .at all need zero
	// TypedArray-specific code, since they already operate purely on
	// (ptr, len, elemTy).
	assertOutput(t, `
const a: Uint8Array = new Uint8Array([10, 20, 30, 40, 50])
const evens = a.filter((x: number) => x % 20 === 0)
console.log(evens.length)

let sum = 0
for (const v of a) {
  sum = sum + v
}
console.log(sum)

const sliced = a.slice(1, 3)
console.log(sliced[0])
console.log(sliced.length)

const rev = a.reverse()
console.log(rev[0])
console.log(a.at(-1))
`, "2\n150\n20\n2\n50\n10")
}

func TestE2ETypedArrayFloat64(t *testing.T) {
	assertOutput(t, `
const f: Float64Array = new Float64Array([1.5, 2.25, -3.75])
console.log(f[0])
console.log(f[1])
console.log(f[2])
console.log(f.byteLength)
`, "1.5\n2.25\n-3.75\n24")
}

func TestE2ETypedArrayInt32Negative(t *testing.T) {
	assertOutput(t, `
const i32: Int32Array = new Int32Array(4)
i32[0] = -100000
console.log(i32[0])
console.log(i32.byteLength)
`, "-100000\n16")
}
