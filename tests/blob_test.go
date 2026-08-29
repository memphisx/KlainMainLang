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

// Blob.stream() (ADR-00341): a ReadableStream<Uint8Array> over the blob's
// bytes — one owned-copy chunk, then closed; an empty blob yields no chunk.
func TestE2EBlobStreamForAwait(t *testing.T) {
	assertOutput(t, `
async function run(): Promise<void> {
  const b = new Blob(["Hello, ", "streamed ", "world"]);
  let bytes = 0;
  let chunks = 0;
  for await (const chunk of b.stream()) {
    chunks += 1;
    for (let i = 0; i < chunk.length; i++) bytes += 1;
  }
  console.log(chunks + " " + bytes);
}
run();
`, "1 21")
}

func TestE2EBlobStreamReader(t *testing.T) {
	assertOutput(t, `
async function run(): Promise<void> {
  const b = new Blob(["abc"]);
  const reader = b.stream().getReader();
  const r1 = await reader.read();
  console.log(r1.done ? "done" : "len " + r1.value.length);
  const r2 = await reader.read();
  console.log(r2.done ? "closed" : "more");
}
run();
`, "len 3\nclosed")
}

func TestE2EBlobStreamEmpty(t *testing.T) {
	assertOutput(t, `
async function run(): Promise<void> {
  const empty = new Blob([]);
  let chunks = 0;
  for await (const c of empty.stream()) { chunks += 1; }
  console.log(chunks);
}
run();
`, "0")
}

func TestE2EBlobVariableStringParts(t *testing.T) {
	// ADR-00489: a variable-bound string[] parts array builds the Blob at
	// runtime (two-pass length + copy).
	assertOutput(t, `
const parts: string[] = ["hello", " ", "blob"];
const b = new Blob(parts, { type: "text/plain" });
console.log(b.size, b.type);
async function show(): Promise<void> {
    console.log(await b.text());
}
show();
`, "10 text/plain\nhello blob")
}
