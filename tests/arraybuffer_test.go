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
