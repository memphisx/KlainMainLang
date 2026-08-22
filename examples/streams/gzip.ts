// CompressionStream / DecompressionStream (TDD-00097 Stage 6): gzip a text
// stream, inspect the raw bytes on one tee branch, and round-trip it back
// through DecompressionStream on the other — all through the same WHATWG
// pipeline machinery, with zlib linked only because these constructors are
// actually used.
const enc = new TextEncoder();
const dec = new TextDecoder();
const original = "Thessaloniki says: streams compress well. ".repeat(100);

const source = new ReadableStream<Uint8Array>({
  start: (c) => { const b = enc.encode(original); c.enqueue(b); c.close(); }
});

const [rawBranch, decodeBranch] = source.pipeThrough(new CompressionStream("gzip")).tee();

let gzBytes = 0;
let magicOk = false;
let sawFirst = false;
for await (const chunk of rawBranch) {
  if (!sawFirst) { magicOk = chunk[0] === 0x1f && chunk[1] === 0x8b; sawFirst = true; }
  gzBytes = gzBytes + chunk.length;
}
console.log("gzip magic bytes:", magicOk);
console.log("compressed", original.length, "->", gzBytes, "bytes");

let out = "";
for await (const chunk of decodeBranch.pipeThrough(new DecompressionStream("gzip"))) {
  out = out + dec.decode(chunk);
}
console.log("round-trip intact:", out === original);
