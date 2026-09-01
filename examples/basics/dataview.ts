// DataView: endian-controlled binary reads/writes over an ArrayBuffer —
// packing a little "network header" and reading it back both ways.

const buf = new ArrayBuffer(12);
const view = new DataView(buf);

view.setUint16(0, 0xCAFE);        // big-endian by default, as the spec
view.setUint32(2, 123456789, true); // explicit little-endian
view.setFloat32(6, 21.5, true);
view.setInt16(10, -7);

console.log(view.getUint16(0), view.getUint16(0, true));
console.log(view.getUint32(2, true));
console.log(view.getFloat32(6, true));
console.log(view.getInt16(10), view.getUint16(10));

const tail = new DataView(buf, 6, 6);
console.log(tail.byteOffset, tail.byteLength, tail.buffer.byteLength);
console.log(tail.getFloat32(0, true));

// Half-precision (Float16) accessors round-trip at 16-bit float precision.
const half = new DataView(new ArrayBuffer(4));
half.setFloat16(0, 1.5);
console.log(half.getFloat16(0));            // 1.5
half.setFloat16(2, 3.14, true);
console.log(half.getFloat16(2, true));      // 3.140625 (float16 rounding)

// 64-bit integer accessors carry real bigints — no 2^53 precision loss.
const big = new DataView(new ArrayBuffer(8));
big.setBigUint64(0, 18446744073709551615n);
console.log(big.getBigUint64(0)); // 18446744073709551615n
console.log(big.getBigInt64(0));  // -1n — same bytes, signed reinterpretation

try {
  view.getInt32(10);
} catch (e) {
  console.log("out of bounds throws");
}
