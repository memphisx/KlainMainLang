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
