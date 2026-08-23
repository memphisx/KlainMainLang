package tests

import "testing"

// Blob (TDD-00102): immutable binary data with a MIME type — mixed
// string/TypedArray/ArrayBuffer/Blob parts, .size/.type, .slice with the
// spec's non-inherited contentType, and the promise-shaped readers
// (.text/.arrayBuffer/.bytes) under await.
func TestE2EBlobBasics(t *testing.T) {
	assertOutput(t, `
const b = new Blob(["Hello, ", "world", "!"], { type: "text/plain" });
console.log(b.size, b.type);
console.log(await b.text());
const s = b.slice(7, 12);
console.log(s.size, s.type);
console.log(await s.text());
const s2 = b.slice(-1, 100, "text/x");
console.log(s2.size, s2.type);
const empty = new Blob([]);
console.log(empty.size, empty.type);
`, "13 text/plain\nHello, world!\n5 \nworld\n1 text/x\n0 ")
}

func TestE2EBlobMixedPartsAndReaders(t *testing.T) {
	assertOutput(t, `
const bytes = new Uint8Array([72, 105]);
const b = new Blob(["!"]);
const buf = new ArrayBuffer(4);
const mixed = new Blob([bytes, "?", b, buf]);
console.log(mixed.size);
const ab = await mixed.arrayBuffer();
console.log(ab.byteLength);
const u8 = await mixed.bytes();
console.log(u8.length, u8[0], u8[2], u8[3]);
u8[0] = 0;
console.log((await mixed.bytes())[0]);
`, "8\n8\n8 72 63 33\n72")
}
