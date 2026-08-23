package tests

import "testing"

// Node Buffer (TDD-00103): a flagged Uint8Array. Construction statics,
// codecs, comparison, and the fixed-width accessors — every expectation
// diff-verified against real Node (v26).
func TestE2EBufferFromAndCodecs(t *testing.T) {
	assertOutput(t, `
const a = Buffer.from("Hello");
console.log(a.length, a[0], a[4]);
console.log(a.toString());
console.log(a.toString("hex"));
console.log(a.toString("base64"));
console.log(Buffer.from("48656c6c6f21", "hex").toString());
console.log(Buffer.from("SGVsbG8sIHdvcmxk", "base64").toString());
console.log(Buffer.from("SGk_", "base64url").toString("hex"));
console.log(Buffer.from("héllo", "latin1").toString("hex"));
console.log(Buffer.from([0xe9, 0x41]).toString("latin1"));
console.log(Buffer.byteLength("Hello"), Buffer.byteLength("48ff", "hex"));
`, "5 72 111\nHello\n48656c6c6f\nSGVsbG8=\nHello!\nHello, world\n48693f\n68e96c6c6f\néA\n5 2")
}

func TestE2EBufferAllocConcatCompare(t *testing.T) {
	assertOutput(t, `
const a = Buffer.from("Hello");
const b = Buffer.alloc(4, 7);
console.log(b[0], b[3], b.length);
const c = Buffer.from([1, 2, 300]);
console.log(c[2]);
const d = Buffer.concat([a, c]);
console.log(d.length, d.toString("hex"));
console.log(Buffer.concat([a, c], 6).toString("hex"));
console.log(Buffer.compare(a, a), Buffer.compare(Buffer.from("a"), Buffer.from("b")));
console.log(a.equals(Buffer.from("Hello")), a.equals(c));
console.log(a.compare(Buffer.from("Hellp")));
console.log(Buffer.isBuffer(a), Buffer.isBuffer("x"));
const u8 = Buffer.allocUnsafe(2);
u8[0] = 1; u8[1] = 2;
console.log(u8.toString("hex"));
`, "7 7 4\n44\n8 48656c6c6f01022c\n48656c6c6f01\n0 -1\ntrue false\n-1\ntrue false\n0102")
}

func TestE2EBufferAccessorsAndWrite(t *testing.T) {
	assertOutput(t, `
const w = Buffer.alloc(8);
console.log(w.writeUInt16BE(0xCAFE, 0));
console.log(w.readUInt16BE(0), w.readUInt16LE(0));
w.writeInt32LE(-2, 4);
console.log(w.readInt32LE(4), w.readUInt32LE(4));
w.writeDoubleLE(3.5, 0);
console.log(w.readDoubleLE(0));
w.writeBigInt64BE(9007199254740993n, 0);
console.log(w.readBigInt64BE(0));
console.log(w.readBigUInt64BE(0));
const t = Buffer.alloc(10);
console.log(t.write("hi there!", 1));
console.log(t.toString("utf8", 1, 3));
const cp = Buffer.alloc(3);
const a = Buffer.from("Hello");
console.log(a.copy(cp, 0, 1));
console.log(cp.toString());
console.log(a.subarray(1, 3).toString());
console.log(a.slice(0, 2).toString("hex"));
try { w.readInt32LE(6); } catch (e) { console.log("oob"); }
`, "2\n51966 65226\n-2 4294967294\n3.5\n9007199254740993n\n9007199254740993n\n9\nhi\n3\nell\nel\n4865\noob")
}

func TestE2EBufferSharedArrayMachinery(t *testing.T) {
	assertOutput(t, `
const a = Buffer.from([10, 20, 30, 40]);
console.log(a.indexOf(30), a.includes(99));
a.fill(0, 2);
console.log(a.toString("hex"));
let sum = 0;
for (const b of a) { sum += b; }
console.log(sum, a.byteLength);
const doubled = a.map((x: number) => x * 2);
console.log(doubled[1]);
`, "2 false\n0a140000\n30 4\n40")
}

func TestE2EBufferRejections(t *testing.T) {
	assertChanCompileError(t, `
const enc = "hex";
const b = Buffer.from("ff", enc);
`, "string literal")
	assertChanCompileError(t, `
const b = Buffer.from("hi", "utf16le");
`, "not supported")
}
