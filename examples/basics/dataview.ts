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

try {
  view.getInt32(10);
} catch (e) {
  console.log("out of bounds throws");
}
