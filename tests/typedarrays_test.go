package tests

import (
	"strings"
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

// The 3-argument sub-range view form: new XArray(buffer, byteOffset, length?).
func TestE2ETypedArraySubRangeView(t *testing.T) {
	assertOutput(t, `
const buf = new ArrayBuffer(16)
const all: Uint8Array = new Uint8Array(buf)
for (let i = 0; i < 16; i++) { all[i] = i }
const mid: Uint8Array = new Uint8Array(buf, 4, 8)
console.log(mid.length, mid[0], mid[7])
mid[0] = 99
console.log(all[4])
const tail: Int32Array = new Int32Array(buf, 8)
console.log(tail.length, tail.byteLength)
const one: Int32Array = new Int32Array(buf, 12, 1)
console.log(one.length)
`, "8 4 11\n99\n2 8\n1")
}

func TestE2ETypedArraySubRangeViewRangeErrors(t *testing.T) {
	assertOutput(t, `
const buf = new ArrayBuffer(16)
try { const a: Int32Array = new Int32Array(buf, 3) } catch (e) { console.log("misaligned") }
try { const b: Int32Array = new Int32Array(buf, 20) } catch (e) { console.log("oob offset") }
try { const c: Int32Array = new Int32Array(buf, 8, 3) } catch (e) { console.log("oob length") }
try { const d: Int32Array = new Int32Array(buf, 4, -1) } catch (e) { console.log("neg length") }
try { const f: Uint16Array = new Uint16Array(buf, 2, 7) } catch (e) { console.log("unreached") }
console.log("ok")
`, "misaligned\noob offset\noob length\nneg length\nok")
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
  console.log("caught: " + e.message, e instanceof RangeError, e.name)
}
`, "caught: offset is out of bounds true RangeError")
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

// ADR-00593: console.log of a whole TypedArray shows Node's TypeName(len) prefix.
func TestE2ETypedArrayInspectPrefix(t *testing.T) {
	assertOutput(t, `
console.log(new Uint8Array([1, 2, 3]))
console.log(new Int16Array([10, -20]))
console.log(new Float32Array([1.5, 2.5]))
console.log(new BigInt64Array([1n, 2n]))
console.log(new Uint8Array(0))
console.log([1, 2, 3])
`, "Uint8Array(3) [ 1, 2, 3 ]\nInt16Array(2) [ 10, -20 ]\nFloat32Array(2) [ 1.5, 2.5 ]\nBigInt64Array(2) [ 1n, 2n ]\nUint8Array(0) []\n[ 1, 2, 3 ]")
}

// ADR-00592: compound element assignment on a bigint typed array.
func TestE2EBigInt64ArrayCompoundAssign(t *testing.T) {
	assertOutput(t, `
const a = new BigInt64Array(3)
a[0] = 10n
a[0] += 5n
a[1] = 100n
a[1] -= 30n
a[2] = 3n
a[2] *= 4n
console.log(a[0], a[1], a[2])
const u = new BigUint64Array(1)
u[0] = 8n
u[0] += 2n
console.log(u[0])
console.log(u)
`, "15n 70n 12n\n10n\nBigUint64Array(1) [ 10n ]")
}

// BigInt64Array/BigUint64Array (TDD-00101): raw i64/u64 storage, bigint
// handles at the language boundary.
func TestE2EBigInt64ArrayBasics(t *testing.T) {
	assertOutput(t, `
const a = new BigInt64Array(3)
a[0] = 9007199254740993n
a[1] = -5n
console.log(a[0], a[1], a[2])
console.log(a.length, a.byteLength)
const b = new BigUint64Array([1n, 18446744073709551615n])
console.log(b[1])
for (const v of b) { console.log(v) }
console.log(a.at(1))
a.fill(7n, 1)
console.log(a[1], a[2])
const c = new BigInt64Array(a)
c[0] = 1n
console.log(a[0], c[0])
const sub = a.subarray(1)
console.log(sub[0])
const buf = new ArrayBuffer(16)
const d = new BigInt64Array(buf, 8, 1)
d[0] = 42n
console.log(d[0], d.length)
`, "9007199254740993n -5n 0n\n3 24\n18446744073709551615n\n1n\n18446744073709551615n\n-5n\n7n 7n\n9007199254740993n 1n\n7n\n42n 1")
}

func TestE2EBigInt64ArrayAtomics(t *testing.T) {
	assertOutput(t, `
const sab = new SharedArrayBuffer(16)
const a = new BigInt64Array(sab)
Atomics.store(a, 0, 9007199254740993n)
console.log(Atomics.load(a, 0))
console.log(Atomics.add(a, 0, 1n))
console.log(Atomics.compareExchange(a, 0, 9007199254740994n, -1n))
console.log(Atomics.load(a, 0))
`, "9007199254740993n\n9007199254740993n\n9007199254740994n\n-1n")
}

func TestE2EBigInt64ArrayRejections(t *testing.T) {
	assertChanCompileError(t, `
const a = new BigInt64Array(2)
a[0] = 5
`, "must be a bigint")
	assertChanCompileError(t, `
const a = new BigInt64Array(2)
const m = a.map((x) => x)
`, "not supported on a BigInt64Array/BigUint64Array")
	assertChanCompileError(t, `
const a = new BigInt64Array([1, 2])
`, "must be a bigint")
}

// Uint8ClampedArray (TDD-00101): real ToUint8Clamp stores — clamp to
// [0,255], floats round half to even, NaN → 0.
func TestE2EUint8ClampedArray(t *testing.T) {
	assertOutput(t, `
const a = new Uint8ClampedArray([-1, 300, 254.5, 255.5, NaN, 2.5, 3.5])
console.log(a[0], a[1], a[2], a[3], a[4], a[5], a[6])
const b = new Uint8ClampedArray(3)
b[0] = 999
b[1] = -7
b[2] = 127.5
console.log(b[0], b[1], b[2])
b.fill(300.7, 0, 2)
console.log(b[0], b[1], b[2])
const src = new Float64Array([1.2, 500, -3])
const c = new Uint8ClampedArray(src)
console.log(c[0], c[1], c[2])
const d = new Uint8ClampedArray(2)
d.set(src.subarray(1), 0)
console.log(d[0], d[1])
const e = a.map((x: number) => x * 2)
console.log(e[1])
b[0] += 200
console.log(b[0])
console.log(a.includes(255), a.indexOf(254))
`, "0 255 254 255 0 2 4\n255 0 128\n255 255 128\n1 255 0\n255 0\n255\n255\ntrue 2")
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

// TDD-00018 Stage 5 (ADR-00512): a TypedArray constructor as a general
// expression — inline function argument, function return value, object field,
// and an inline method chain — not only a variable declaration's initializer.
func TestE2ETypedArrayGeneralExpression(t *testing.T) {
	assertOutput(t, `
function sum(a: Uint8Array): number {
  let s = 0
  for (const x of a) { s = s + x }
  return s
}
function make(n: number): Uint8Array {
  return new Uint8Array([n, n + 1, n + 2])
}
console.log(sum(new Uint8Array([10, 20, 30])))
const m = make(5)
console.log(m.length, m[2])
console.log(sum(new Uint8Array(4)))
console.log(new Int32Array([1, 2, 3]).map((x) => x * 2).join(","))
const o: { buf: Uint8Array } = { buf: new Uint8Array([7, 8, 9]) }
console.log(o.buf.length, o.buf[1])
`, "60\n3 7\n0\n2,4,6\n3 8")
}

// `() => ta.set(...)` — set() returns undefined; the arrow's return type once
// inferred i64 and emitted `ret i64 ` with no value (invalid IR). Found by the
// Test262 staging/sm/TypedArray/set-tointeger.js `assert.throws(RangeError,
// () => ta.set(source, offset))` shape (ADR-01054).
func TestE2ETypedArraySetInArrowReturn(t *testing.T) {
	assertOutputCompatJS(t, `
let ta = new Int32Array(4);
const f = () => ta.set([1], 9);
try { f(); } catch (e) { console.log(e instanceof RangeError, e.name, e.message); }
const g = () => ta.set([7], 1);
console.log(g(), ta[1]);
`, "true RangeError offset is out of bounds\nundefined 7")
}

// %TypedArray%.prototype.set(src, offset) runs ToIntegerOrInfinity on the
// offset (ADR-01057): NaN/undefined/"junk" → 0, "3"/{valueOf} convert, -0.9
// truncates to -0 (valid), a negative or infinite offset is a RangeError
// before any write (a negative offset used to write out of bounds), and a
// Symbol offset is a TypeError. Lines match Node.
func TestE2ETypedArraySetOffsetToInteger(t *testing.T) {
	assertOutputCompatJS(t, `
let ta = new Int32Array(4);
let sources = [[], [7]];
let typed = [new Int32Array(0), new Int32Array(1)];
let valid = [0, 0.1, 3, 3.9, -0, -0.9, NaN, undefined, null, true, "", "3", "  1\t\n", "junk", {valueOf() { return 2; }}];
let n = 0;
for (let offset of valid) for (let source of sources) { ta.set(source, offset); n++; }
for (let offset of valid) for (let source of typed) { ta.set(source, offset); n++; }
console.log(n, ta);
let invalid = [5, 2147483648, Infinity, -1, -1.1, -4294967297, -Infinity, "8", "  Infinity  ", {valueOf() { return 10; }}];
let bad = 0;
for (let offset of invalid) for (let source of sources) {
  try { ta.set(source, offset); bad++; } catch (e) { if (!(e instanceof RangeError)) bad++; }
}
for (let offset of invalid) for (let source of typed) {
  try { ta.set(source, offset); bad++; } catch (e) { if (!(e instanceof RangeError)) bad++; }
}
console.log(bad);
ta.set([], 4); ta.set([], 4.9);
try { ta.set([1], 4.9); console.log("no throw"); } catch (e) { console.log(e instanceof RangeError); }
try { ta.set([1], Symbol()); console.log("no throw"); } catch (e) { console.log(e instanceof TypeError); }
`, "60 Int32Array(4) [ 0, 0, 0, 0 ]\n0\ntrue\ntrue")
}

// ADR-01059: a mixed array literal never reinterprets a buffer — strict
// rejects it cleanly, -compat=js boxes it to any[] keeping each element's
// identity (Node prints `Int32Array(1) [ 8 ]`, and set() reads the i32s).
func TestE2EMixedArrayLiteralStrictRejected(t *testing.T) {
	_, err := parseAndCompile(`
const mixed = [[7], new Int32Array([8])];
console.log(mixed);
`)
	if err == nil {
		t.Fatal("expected a compile error for a number[] / Int32Array mixed literal, got none")
	}
	if !strings.Contains(err.Error(), "element 1 is a Int32Array, not a number[]") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestE2EMixedArrayLiteralJSBoxes(t *testing.T) {
	assertOutputCompatJS(t, `
const mixed = [[7], new Int32Array([8])];
console.log(mixed);
const ta = new Int32Array(2);
ta.set(mixed[1]);
console.log(ta, mixed[1].length, mixed[0][0]);
`, "[ [ 7 ], Int32Array(1) [ 8 ] ]\nInt32Array(2) [ 8, 0 ] 1 7")
}

// ADR-01059: set() takes an `any` (or any non-array) source through the
// spec's array-like walk — a boxed typed array, a string, an array-like
// object, a number (no length → no-op); null/undefined throw.
func TestE2ETypedArraySetAnySource(t *testing.T) {
	assertOutput(t, `
const ta = new Int32Array(3);
const src: any = new Int16Array([1, -2, 3]);
ta.set(src);
console.log(ta);
const s: any = "12x";
ta.set(s);
console.log(ta);
const arrLike: any = { length: 2, 0: 7, 1: "8" };
ta.set(arrLike, 1);
console.log(ta);
const plain: any = [9.5, true];
ta.set(plain);
console.log(ta);
ta.set(5);
try { ta.set(null); } catch (e) { console.log((e as Error).message); }
try { ta.set(undefined); } catch (e) { console.log((e as Error).message); }
ta.set("4");
console.log(ta);
ta.set({ length: 2, 0: 6, 1: 5 }, 1);
const named = { length: 1, 0: 3 };
ta.set(named);
console.log(ta);
`, "Int32Array(3) [ 1, -2, 3 ]\nInt32Array(3) [ 1, 2, 0 ]\nInt32Array(3) [ 1, 7, 8 ]\nInt32Array(3) [ 9, 1, 8 ]\nCannot convert undefined or null to object\nCannot convert undefined or null to object\nInt32Array(3) [ 4, 1, 8 ]\nInt32Array(3) [ 3, 6, 5 ]")
}

// ADR-01059: a TypedArray keeps its identity through `any` — inspect prefix,
// JSON object form, Array.isArray false, element/length reads — and an
// any-annotated top-level typed array is boxed, not promoted as a raw array.
func TestE2ETypedArrayThroughAny(t *testing.T) {
	assertOutput(t, `
const y: any = new Int32Array([8, 9]);
console.log(y, y.length, y[1], y[5], JSON.stringify(y), Array.isArray(y));
const c: any = new Uint8ClampedArray([300, 5]);
console.log(c, JSON.stringify(c));
const plain: any = [1.5, 2];
console.log(plain, JSON.stringify(plain), String(plain), plain[0], Array.isArray(plain));
const strs: any = ["a", "b"];
console.log(strs[1], strs.length);
const nested: any = [[1], [2, 3]];
console.log(nested, JSON.stringify(nested));
const named = [[1], [2, 3]]; const viaNamed: any = named; console.log(viaNamed[1], viaNamed[1][0], JSON.stringify(viaNamed), String(viaNamed));
console.log(Array.isArray(new Uint8Array(1)), Array.isArray([1]));
function f() { return y.length; }
console.log(f());
`, "Int32Array(2) [ 8, 9 ] 2 9 undefined {\"0\":8,\"1\":9} false\nUint8ClampedArray(2) [ 255, 5 ] {\"0\":255,\"1\":5}\n[ 1.5, 2 ] [1.5,2] 1.5,2 1.5 true\nb 2\n[ [ 1 ], [ 2, 3 ] ] [[1],[2,3]]\n[ 2, 3 ] 2 [[1],[2,3]] 1,2,3\nfalse true\n2")
}

// The same programs as the pinned tests above, with Node as the oracle
// (ADR-01060) — skipped where Node isn't installed.
func TestOracleTypedArrayThroughAny(t *testing.T) {
	assertSameAsNode(t, `
const y: any = new Int32Array([8, 9]);
console.log(y, y.length, y[1], y[5], JSON.stringify(y), Array.isArray(y));
const c: any = new Uint8ClampedArray([300, 5]);
console.log(c, JSON.stringify(c));
const plain: any = [1.5, 2];
console.log(plain, JSON.stringify(plain), String(plain), plain[0], Array.isArray(plain));
const named = [[1], [2, 3]]; const viaNamed: any = named;
console.log(viaNamed[1], viaNamed[1][0], JSON.stringify(viaNamed), String(viaNamed));
const ta = new Int32Array(3);
ta.set(y); console.log(ta);
ta.set("12x"); console.log(ta);
ta.set({ length: 2, 0: 7, 1: "8" }, 1); console.log(ta);
try { ta.set(null); } catch (e) { console.log((e as Error).message); }
`)
}

func TestOracleMixedArrayLiteralJS(t *testing.T) {
	assertSameAsNodeCompatJS(t, `
const mixed = [[7], new Int32Array([8])];
console.log(mixed);
const ta = new Int32Array(2);
ta.set(mixed[1]);
console.log(ta, mixed[1].length, mixed[0][0]);
`)
}
