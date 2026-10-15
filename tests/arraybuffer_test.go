package tests

import "testing"

// DataView (ADR-00294): endian-controlled reads/writes over an ArrayBuffer
// sub-range, properties, aliasing through .buffer, and RangeError throws on
// out-of-bounds access/construction.
func TestE2EDataViewBasics(t *testing.T) {
	assertOutput(t, `
const buf = new ArrayBuffer(16);
const dv = new DataView(buf);
console.log(dv.byteLength, dv.byteOffset);
dv.setInt32(0, 123456789);
console.log(dv.getInt32(0), dv.getInt32(0, true));
dv.setUint8(4, 300);
console.log(dv.getUint8(4), dv.getInt8(4));
dv.setFloat64(8, 3.14159, true);
console.log(dv.getFloat64(8, true));
dv.setInt16(2, -2, false);
console.log(dv.getInt16(2), dv.getUint16(2), dv.getUint16(2, true));
dv.setFloat32(0, 1.5);
console.log(dv.getFloat32(0));
`, "16 0\n123456789 365779719\n44 44\n3.14159\n-2 65534 65279\n1.5")
}

// A DataView setter returns undefined in JS. Binding that result to an untyped
// variable (`var r = dv.setInt8(0, v)`) takes JS's semantics — the call runs
// for its side effect and the binding is `undefined` — instead of emitting an
// `alloca void` for the pre-inferred slot (the void-return-to-var invalid-IR
// family, all of Test262's DataView `set*/set-values-return-undefined` files).
func TestE2EDataViewSetterReturnsUndefined(t *testing.T) {
	assertOutput(t, `
const dv = new DataView(new ArrayBuffer(8));
const r = dv.setInt8(0, 5);
console.log(r);
console.log(dv.getInt8(0));
`, "undefined\n5")
}

// A non-numeric DataView byteOffset/value (e.g. a Symbol, as several Test262
// `return-abrupt-from-tonumber-*` files pass) is a clean compile-time type
// error — the typed-subset equivalent of the runtime TypeError real JS throws,
// and what tsc itself reports (TS2345, for the methods and the constructor's
// non-generic parameters alike) — not invalid IR emitted at the
// arithmetic/store site (the DataView A1 invalid-IR cluster).
func TestE2EDataViewNonNumericRejected(t *testing.T) {
	mustCompileError(t, `
const dv = new DataView(new ArrayBuffer(8));
dv.getInt16(Symbol("x"));
`, "not assignable to parameter of type 'number'")
	mustCompileError(t, `
const dv = new DataView(new ArrayBuffer(8));
dv.setInt16(0, Symbol("x"));
`, "not assignable to parameter of type 'number'")
	mustCompileError(t, `
const dv = new DataView(new ArrayBuffer(8), Symbol("x"));
`, "not assignable to parameter of type 'number'")
}

// DataView Float16 accessors (ADR-00553): half-precision reads/writes with the
// spec's per-call endianness flag; values round-trip at float16 precision.
func TestE2EDataViewFloat16(t *testing.T) {
	assertOutput(t, `
const buf = new ArrayBuffer(8);
const dv = new DataView(buf);
dv.setFloat16(0, 1.5);
console.log(dv.getFloat16(0));
dv.setFloat16(2, 3.14, true);
console.log(dv.getFloat16(2, true));
dv.setFloat16(4, -0.5);
console.log(dv.getFloat16(4));
`, "1.5\n3.140625\n-0.5")
}

// DataView BigInt64/BigUint64 accessors: values round-trip through the
// arbitrary-precision bigint handles (raw i64/u64 bit pattern in memory),
// with the spec's per-call endianness flag and unsigned > 2^63 support.
func TestE2EDataViewBigInt64(t *testing.T) {
	assertOutput(t, `
const buf = new ArrayBuffer(16);
const dv = new DataView(buf);
dv.setBigInt64(0, 9007199254740993n);
console.log(dv.getBigInt64(0));
dv.setBigInt64(0, -2n);
console.log(dv.getBigInt64(0));
console.log(dv.getBigUint64(0));
dv.setBigUint64(8, 18446744073709551615n, true);
console.log(dv.getBigUint64(8, true));
console.log(dv.getBigInt64(8, true));
dv.setBigInt64(0, 258n);
console.log(dv.getBigInt64(0, true));
`, "9007199254740993n\n-2n\n18446744073709551614n\n18446744073709551615n\n-1n\n144396663052566528n")
}

// ArrayBuffer/SharedArrayBuffer .slice(): copy sub-range with array-slice
// clamping rules; the copy is independent of the source.
func TestE2EArrayBufferSlice(t *testing.T) {
	assertOutput(t, `
const buf = new ArrayBuffer(8);
const src = new Uint8Array(buf);
src[0] = 10; src[3] = 40; src[4] = 50; src[7] = 80;
const s1 = buf.slice(3, 5);
console.log(s1.byteLength);
const v1 = new Uint8Array(s1);
console.log(v1[0], v1[1]);
v1[0] = 99;
console.log(src[3]);
const s2 = buf.slice(-2);
const v2 = new Uint8Array(s2);
console.log(s2.byteLength, v2[1]);
const s3 = buf.slice(6, 100);
console.log(s3.byteLength);
const s4 = buf.slice(5, 2);
console.log(s4.byteLength);
const s5 = buf.slice();
console.log(s5.byteLength);
`, "2\n40 50\n40\n2 80\n2\n0\n8")
}

func TestE2ESharedArrayBufferSlice(t *testing.T) {
	assertOutput(t, `
const sab = new SharedArrayBuffer(8);
const a = new Int32Array(sab);
a[0] = 7; a[1] = 11;
const copy = sab.slice(4);
const b = new Int32Array(copy);
console.log(copy.byteLength, b[0]);
b[0] = 5;
console.log(a[1]);
`, "4 11\n11")
}

func TestE2EDataViewSubRangeAndBounds(t *testing.T) {
	assertOutput(t, `
const buf = new ArrayBuffer(16);
const dv = new DataView(buf);
dv.setUint8(4, 44);
const dv2 = new DataView(buf, 4, 8);
console.log(dv2.byteLength, dv2.byteOffset, dv2.getUint8(0));
console.log(dv2.buffer.byteLength);
dv2.setUint8(0, 99);
console.log(dv.getUint8(4));
try { dv.getInt32(14); } catch (e) { console.log("oob caught"); }
try { const bad = new DataView(buf, 12, 8); } catch (e) { console.log("ctor oob caught"); }
`, "8 4 44\n16\n99\noob caught\nctor oob caught")
}

// Growable SharedArrayBuffer / resizable ArrayBuffer (ADR-00494): the
// {maxByteLength} option reserves the max upfront (views never move), so
// grow()/resize() only bumps the length word; out-of-range throws a
// RangeError with V8's message (ADR-01636). Fixed-size buffers report growable=false and
// maxByteLength=byteLength.
func TestE2EGrowableBuffers(t *testing.T) {
	assertOutput(t, `
const sab = new SharedArrayBuffer(8, {maxByteLength: 32})
console.log(sab.byteLength)
console.log(sab.growable)
console.log(sab.maxByteLength)
sab.grow(16)
console.log(sab.byteLength)
const view = new Int32Array(sab)
view[3] = 42
console.log(view[3])
const fixed = new SharedArrayBuffer(4)
console.log(fixed.growable)
console.log(fixed.maxByteLength)
const ab = new ArrayBuffer(4, {maxByteLength: 8})
console.log(ab.resizable)
ab.resize(8)
console.log(ab.byteLength)
try { sab.grow(8) } catch (e) { console.log("caught RangeError:", e instanceof RangeError, (e as Error).message) }
`, "8\ntrue\n32\n16\n42\nfalse\n4\ntrue\n8\ncaught RangeError: true SharedArrayBuffer.prototype.grow: Invalid length parameter")
}

// ArrayBuffer.resize permits shrinking (ADR-00564), unlike SharedArrayBuffer.grow.
// The backing block stays reserved at maxByteLength, so shrinking is just a
// smaller length word; growing back up is still in range. A shrink on a
// SharedArrayBuffer's grow() still throws.
func TestE2EArrayBufferResizeShrink(t *testing.T) {
	assertOutput(t, `
const ab = new ArrayBuffer(8, {maxByteLength: 16})
console.log(ab.byteLength)
ab.resize(12)
console.log(ab.byteLength)
ab.resize(4)
console.log(ab.byteLength)
ab.resize(10)
console.log(ab.byteLength)
try { ab.resize(20) } catch (e) { console.log("caught:", e instanceof RangeError) }
const sab = new SharedArrayBuffer(8, {maxByteLength: 16})
try { sab.grow(4) } catch (e) { console.log("shrink-blocked:", e instanceof RangeError) }
`, "8\n12\n4\n10\ncaught: true\nshrink-blocked: true")
}

// A void builtin call's result held in a top-level binding is undefined.
func TestE2EVoidBuiltinResultGlobal(t *testing.T) {
	assertOutput(t, `
const m = new Map<string, number>();
const q = m.clear();
console.log(q);
const ta = new Uint8Array(4);
const s = ta.set([1, 2], 1);
console.log(s, ta.subarray(1, 3).join(","));
`, "undefined\nundefined 1,2")
}

// `.byteLength` is a number on every receiver form (ADR-01440).
func TestE2EArrayBufferByteLengthIsNumber(t *testing.T) {
	assertSameAsNode(t, `
function rest(buffer: ArrayBuffer, off: number): number {
  if (off < 0 || off > buffer.byteLength) throw new RangeError("bad " + off);
  return buffer.byteLength - off;
}
const ab = new ArrayBuffer(8);
const n = ab.byteLength;
console.log(rest(ab, 3), n / 3, ab.byteLength / 3, typeof n, new Uint8Array(ab).byteLength / 16);
`)
}

// An unannotated callback parameter typed by context as a builtin with a
// native layout is that layout before the callback is emitted (ADR-01441).
func TestE2EContextualNativeLayoutParam(t *testing.T) {
	assertSameAsNode(t, `
class B {
  data = new ArrayBuffer(4);
  arrayBuffer(): Promise<ArrayBuffer> { return Promise.resolve(this.data); }
  bytes(): Promise<Uint8Array> { return this.arrayBuffer().then((ab) => new Uint8Array(ab)); }
}
new B().bytes().then(u => console.log(u.length, u[0]));
const when = (d: Date) => d.getTime();
[new Date(5)].forEach((d) => console.log(when(d)));
`)
}

// A data block that cannot be created is a RangeError (CreateByteDataBlock),
// even for a buffer nothing uses: the optimizer deleted an unused calloc and
// took it to succeed (Test262's allocation-limit files). The range checks on
// typed-array views, resize/grow and Buffer accessors are RangeErrors with
// V8's and Node's messages, in the spec's order, and Buffer's carry Node's
// codes.
func TestE2EArrayBufferAllocationFailureIsRangeError(t *testing.T) {
	assertSameAsNode(t, `
const t = (name: string, f: () => void) => {
  try { f(); console.log(name, 'ok'); } catch (e: any) { console.log(name, e.constructor.name, JSON.stringify(e.message)); }
};
t('neg offset', () => { new Int32Array(new ArrayBuffer(8), -4); });
t('misaligned', () => { new Int32Array(new ArrayBuffer(8), 3); });
t('misaligned u16', () => { new Uint16Array(new ArrayBuffer(8), 1); });
t('offset past end', () => { new Int32Array(new ArrayBuffer(8), 12); });
t('offset past end, length', () => { new Int32Array(new ArrayBuffer(8), 12, 1); });
t('neg length', () => { new Uint8Array(new ArrayBuffer(8), 0, -1); });
t('length past end', () => { new Uint8Array(new ArrayBuffer(8), 0, 9); });
t('length past end f64', () => { new Float64Array(new ArrayBuffer(16), 8, 2); });
t('remainder', () => { new Int32Array(new ArrayBuffer(7)); });
t('remainder offset', () => { new Int32Array(new ArrayBuffer(10), 4); });
const rb = new ArrayBuffer(4, { maxByteLength: 8 });
t('resize up', () => rb.resize(16));
t('resize neg', () => rb.resize(-1));
const sb = new SharedArrayBuffer(4, { maxByteLength: 8 });
t('grow down', () => sb.grow(2));
t('grow up', () => sb.grow(16));
t('buffer readInt8', () => { Buffer.alloc(2).readInt8(5); });
const bc = (name: string, f: () => void) => {
  try { f(); console.log(name, 'ok'); } catch (e: any) { console.log(name, e.constructor.name, e.code, JSON.stringify(e.message)); }
};
bc('readUInt32LE short', () => { Buffer.alloc(2).readUInt32LE(0); });
bc('readInt16BE far', () => { Buffer.alloc(4).readInt16BE(5000000000); });
bc('readInt8 neg', () => { Buffer.alloc(4).readInt8(-1); });
bc('writeUInt8 past', () => { Buffer.alloc(4).writeUInt8(1, 4); });
bc('readInt8 frac', () => { Buffer.alloc(4).readInt8(1.5); });
t('neg', () => { new Int32Array(-1); });
t('2^40', () => { new Int32Array(1099511627776); });
t('2^50', () => { new Float64Array(1125899906842624); });
t('2^53-1', () => { new Uint8Array(9007199254740991); });
`)
}
