package tests

import "testing"

// Node's `stream` module (lib/node/stream.ts), each program's expected output
// taken from Node v24.

// Readable and Writable events: data/end on the read side, finish on the write side, in Node's order.
func TestE2ENodeStreamReadableWritableEvents(t *testing.T) {
	assertOutputImports(t, `
import { Readable, Writable } from 'stream';
const r = new Readable({ read() {} });
r.on("data", (chunk) => { console.log("data:", chunk.toString()); });
r.on("end", () => { console.log("end"); });
r.push("alpha");
r.push("beta");
r.push(null);
const w = new Writable({ objectMode: true, write(n, _enc, cb) { console.log("sink:", n * 2); cb(); } });
w.on("finish", () => { console.log("finished"); });
w.write(1);
w.end(3);
setTimeout(() => { console.log("done"); }, 30);
`, "sink: 2\nsink: 6\ndata: alpha\ndata: beta\nend\nfinished\ndone\n")
}

// write(chunk[, encoding][, callback]) and end(chunk[, encoding][, callback]): the callbacks run after the synchronous code, in order.
func TestE2ENodeStreamWriteEndCallbacks(t *testing.T) {
	assertOutputImports(t, `
import { Writable } from 'stream';
const w = new Writable({ write(_s, _e, cb) { cb(); } });
console.log("sync-start");
w.write("a", "utf8", () => { console.log("cb-a"); });
w.write("b", () => { console.log("cb-b"); });
w.end("c", "utf8", () => { console.log("cb-end"); });
console.log("sync-end");
`, "sync-start\nsync-end\ncb-a\ncb-b\ncb-end\n")
}

// An options-form write(chunk, encoding, callback) with decodeStrings: false receives the strings as written.
func TestE2ENodeStreamOptionsFormThreeArgSink(t *testing.T) {
	assertOutputImports(t, `
import { Writable } from 'stream';
const got: string[] = [];
const w = new Writable({
  decodeStrings: false,
  write(chunk: string, enc, cb) { got.push(chunk.toUpperCase() + "/" + enc); cb(); }
});
w.on("finish", () => { console.log("finish: " + got.join(",")); });
w.write("alpha");
w.end("beta");
`, "finish: ALPHA/utf8,BETA/utf8\n")
}

// A string written with an encoding is decoded to bytes by it; a Buffer chunk reports "buffer".
func TestE2ENodeStreamWriteEncodings(t *testing.T) {
	assertOutputImports(t, `
import { Writable } from 'stream';
const w = new Writable({ write(chunk: Buffer, enc, cb) { console.log(chunk, enc); cb(); } });
w.write("é", "latin1");
w.write("68656c6c6f", "hex");
w.write(Buffer.from("hi"));
w.end("aGk=", "base64");
`, "<Buffer e9> buffer\n<Buffer 68 65 6c 6c 6f> buffer\n<Buffer 68 69> buffer\n<Buffer 68 69> buffer\n")
}

// stream/promises pipeline through an object-mode Transform.
func TestE2ENodeStreamPipelineTransform(t *testing.T) {
	assertOutputImports(t, `
import { Readable, Writable, Transform } from 'stream';
import { pipeline } from 'stream/promises';
const src = Readable.from(["hello", "world"]);
const upper = new Transform({ objectMode: true, transform(chunk, _e, cb) { cb(null, String(chunk).toUpperCase()); } });
const collected: string[] = [];
const sink = new Writable({ objectMode: true, write(s, _e, cb) { collected.push(s); cb(); } });
await pipeline(src, upper, sink);
console.log("pipeline:", collected.join(" "));
`, "pipeline: HELLO WORLD\n")
}

// Readable.from piped into a Writable; stream/promises finished waits for the sink.
func TestE2ENodeStreamPipeFromFinished(t *testing.T) {
	assertOutputImports(t, `
import { Readable, Writable } from 'stream';
import { finished } from 'stream/promises';
const src = Readable.from([10, 20, 30]);
let sum = 0;
const sink = new Writable({ objectMode: true, write(n, _e, cb) { sum = sum + n; cb(); } });
src.pipe(sink);
await finished(sink);
console.log("sum:", sum);
`, "sum: 60\n")
}

// Readable.fromWeb and Readable.toWeb bridge to web streams.
func TestE2ENodeStreamWebBridges(t *testing.T) {
	assertOutputImports(t, `
import { Readable } from 'stream';
import { finished } from 'stream/promises';
const webRs = new ReadableStream<string>({
  start: (c) => { c.enqueue("bridged"); c.close(); }
});
const nodeR = Readable.fromWeb(webRs, { objectMode: true });
nodeR.on("data", (s) => { console.log("from web:", s); });
await finished(nodeR);
const back = Readable.toWeb(Readable.from(["to web"]));
for await (const s of back) { console.log("to web:", s); }
`, "from web: bridged\nto web: to web\n")
}

// A write callback's error rejects pipeline and emits error; pause/resume and once(end).
func TestE2ENodeStreamErrorsPauseResumeOnce(t *testing.T) {
	assertOutputImports(t, `
import { Readable, Writable } from 'stream';
import { pipeline } from 'stream/promises';
const src = Readable.from([1, 2, 3]);
const bad = new Writable({
  objectMode: true,
  write(n, _e, cb) { if (n === 2) cb(new Error("sink died")); else cb(); }
});
bad.on("error", (e) => { console.log("error event:", e.message); });
try {
  await pipeline(src, bad);
} catch (e) {
  console.log("pipeline rejected:", (e as Error).message);
}
const r2 = new Readable({ objectMode: true, read() {} });
let seen = 0;
r2.on("data", () => { seen = seen + 1; });
r2.once("end", () => { console.log("once end, seen:", seen); });
r2.push(1);
r2.pause();
r2.push(2);
r2.resume();
r2.push(3);
r2.push(null);
setTimeout(() => { console.log("done"); }, 30);
`, "error event: sink died\npipeline rejected: sink died\nonce end, seen: 3\ndone\n")
}

// An options-form read() pulls with this.push until it pushes null.
func TestE2ENodeStreamReadCallbackPull(t *testing.T) {
	assertOutputImports(t, `
import { Readable } from 'stream';
import { finished } from 'stream/promises';
let n = 0;
const r = new Readable({
  objectMode: true,
  read() {
    n = n + 1;
    if (n > 3) { this.push(null); } else { this.push(n * 10); }
  }
});
r.on("data", (v) => { console.log("v", v); });
await finished(r);
console.log("pulled", n, "times");
`, "v 10\nv 20\nv 30\npulled 4 times\n")
}

// new stream.Readable through the default import.
func TestE2ENodeStreamQualifiedNewReadable(t *testing.T) {
	assertOutputImports(t, `
import stream from 'stream';
let n: number = 0;
const r = new stream.Readable({
  objectMode: true,
  read() {
    n = n + 1;
    if (n > 2) { this.push(null); } else { this.push(n); }
  }
});
r.on("data", (v) => { console.log("q", v); });
r.on("end", () => { console.log("qdone"); });
`, "q 1\nq 2\nqdone\n")
}

// class extends Readable with a _read override.
func TestE2ENodeStreamClassExtendsReadable(t *testing.T) {
	assertOutputImports(t, `
import { Readable } from 'stream';
import { finished } from 'stream/promises';
class Counter extends Readable {
  n: number = 0;
  constructor() { super({ objectMode: true }); }
  _read() {
    this.n = this.n + 1;
    if (this.n > 3) { this.push(null); } else { this.push(this.n * 10); }
  }
}
const c = new Counter();
c.on("data", (v) => { console.log("v", v); });
await finished(c);
console.log("done", c.n);
`, "v 10\nv 20\nv 30\ndone 4\n")
}

// class extends Writable with a _write override.
func TestE2ENodeStreamClassExtendsWritable(t *testing.T) {
	assertOutputImports(t, `
import { Writable } from 'stream';
class Collector extends Writable {
  items: string[] = [];
  _write(chunk: Buffer, _enc: BufferEncoding, cb: (error?: Error | null) => void) {
    this.items.push(chunk.toString());
    cb();
  }
}
const sink = new Collector();
sink.on("finish", () => { console.log("collected:", sink.items.join(",")); });
sink.write("alpha");
sink.write("beta");
sink.end();
`, "collected: alpha,beta\n")
}

// class extends Duplex: independent _read and _write sides.
func TestE2ENodeStreamClassExtendsDuplex(t *testing.T) {
	assertOutputImports(t, `
import { Duplex } from 'stream';
import { finished } from 'stream/promises';
class Echo extends Duplex {
  queue: string[] = ["one", "two", "three"];
  i: number = 0;
  received: string[] = [];
  _read() {
    if (this.i >= this.queue.length) { this.push(null); }
    else { this.push(this.queue[this.i]); this.i = this.i + 1; }
  }
  _write(chunk: Buffer, _enc: BufferEncoding, cb: (error?: Error | null) => void) {
    this.received.push(chunk.toString());
    cb();
  }
}
const d = new Echo();
const out: string[] = [];
d.on("data", (c) => { out.push(c.toString()); });
d.on("finish", () => { console.log("wrote:", d.received.join(",")); });
d.write("alpha");
d.write("beta");
d.end();
await finished(d);
console.log("read:", out.join(" "));
`, "wrote: alpha,beta\nread: one two three\n")
}

// class extends Transform: _transform pushes to the readable side.
func TestE2ENodeStreamClassExtendsTransform(t *testing.T) {
	assertOutputImports(t, `
import { Transform } from 'stream';
import type { TransformCallback } from 'stream';
import { finished } from 'stream/promises';
class Upper extends Transform {
  _transform(chunk: Buffer, _enc: BufferEncoding, cb: TransformCallback) {
    this.push(chunk.toString().toUpperCase());
    cb();
  }
}
const up = new Upper();
const out: string[] = [];
up.on("data", (c) => { out.push(c.toString()); });
up.write("kalimera");
up.write("thessaloniki");
up.end();
await finished(up);
console.log("out:", out.join(" "));
`, "out: KALIMERA THESSALONIKI\n")
}

// A class Transform with a small highWaterMark as a pipe destination.
func TestE2ENodeStreamClassTransformPipe(t *testing.T) {
	assertOutputImports(t, `
import { Readable, Transform } from 'stream';
import type { TransformCallback } from 'stream';
import { finished } from 'stream/promises';
class Doubler extends Transform {
  constructor() { super({ objectMode: true, highWaterMark: 2 }); }
  _transform(n: number, _enc: BufferEncoding, cb: TransformCallback) {
    this.push(n * 2);
    cb();
  }
}
const src = Readable.from([1, 2, 3, 4, 5]);
const d = new Doubler();
const out: number[] = [];
d.on("data", (v) => { out.push(v); });
src.pipe(d);
await finished(d);
console.log("doubled:", out.join(","));
`, "doubled: 2,4,6,8,10\n")
}

// super({ highWaterMark, objectMode }) from a Readable subclass.
func TestE2ENodeStreamClassSuperOptions(t *testing.T) {
	assertOutputImports(t, `
import { Readable } from 'stream';
import { finished } from 'stream/promises';
class Counter extends Readable {
  n: number = 0;
  constructor() {
    super({ highWaterMark: 2, objectMode: true });
  }
  _read() {
    this.n = this.n + 1;
    if (this.n > 3) { this.push(null); } else { this.push("chunk" + this.n); }
  }
}
const c = new Counter();
const seen: string[] = [];
c.on("data", (v) => { seen.push(v); });
await finished(c);
console.log("read:", seen.join(","), c.readableHighWaterMark);
`, "read: chunk1,chunk2,chunk3 2\n")
}

// highWaterMark in the options, and the defaults.
func TestE2ENodeStreamOptionsHighWaterMark(t *testing.T) {
	assertOutputImports(t, `
import { Readable, Writable } from 'stream';
import { finished } from 'stream/promises';
let m = 0;
const r = new Readable({
  objectMode: true,
  highWaterMark: 8,
  read() { m = m + 1; if (m > 2) { this.push(null); } else { this.push(m); } }
});
const seen: number[] = [];
r.on("data", (v) => { seen.push(v); });
await finished(r);
console.log("hwm", r.readableHighWaterMark, seen.join(","));
console.log(new Readable().readableHighWaterMark, new Writable({ objectMode: true }).writableHighWaterMark);
`, "hwm 8 1,2\n65536 16\n")
}

// class extends stream.Readable through the default import.
func TestE2ENodeStreamQualifiedExtends(t *testing.T) {
	assertOutputImports(t, `
import stream from 'stream';
class Counter extends stream.Readable {
  n: number = 0;
  constructor() { super({ objectMode: true }); }
  _read() {
    this.n = this.n + 1;
    if (this.n > 2) { this.push(null); } else { this.push(this.n * 5); }
  }
}
const c = new Counter();
c.on("data", (v) => { console.log("qe", v); });
`, "qe 5\nqe 10\n")
}

// PassThrough passes each written chunk through as a Buffer.
func TestE2ENodeStreamPassThrough(t *testing.T) {
	assertOutputImports(t, `
import { PassThrough } from 'stream';
const p = new PassThrough();
p.on("data", (chunk) => { console.log("got: " + chunk); });
p.on("end", () => { console.log("ended"); });
p.write("hello");
p.end("world");
`, "got: hello\ngot: world\nended\n")
}

// stream.PassThrough piped into a Writable, and an object-mode PassThrough.
func TestE2ENodeStreamPassThroughQualifiedTyped(t *testing.T) {
	assertOutputImports(t, `
import stream from 'stream';
const s2 = new stream.PassThrough();
const sink = new stream.Writable({
  write(chunk: Buffer, _e, cb) { console.log("sink: " + chunk.toString()); cb(); }
});
s2.pipe(sink);
s2.write("a");
s2.end("b");
const nums = new stream.PassThrough({ objectMode: true, highWaterMark: 4 });
nums.on("data", (n) => { console.log(n * 2); });
nums.write(21);
nums.end();
`, "sink: a\nsink: b\n42\n")
}

// The callback form of finished runs once the stream has ended, without an error.
func TestE2ENodeStreamFinishedCallback(t *testing.T) {
	assertOutputImports(t, `
import { PassThrough, finished } from 'stream';
const p = new PassThrough();
finished(p, (err) => { console.log("finished, error: " + (err ? err.message : "none")); });
p.on("data", (c) => { console.log("data: " + c); });
p.write("x");
p.end();
`, "data: x\nfinished, error: none\n")
}

// stream.finished with a mustCall callback on a resumed, ended stream.
func TestE2ENodeStreamFinishedMustCall(t *testing.T) {
	assertOutputImports(t, `
import stream from 'stream';
import { mustCall } from 'test';
const p = new stream.PassThrough();
stream.finished(p, mustCall(() => { console.log("done"); }));
p.resume();
p.end();
`, "done\n")
}

// The callback form of pipeline across three stages.
func TestE2ENodeStreamPipelineCallback(t *testing.T) {
	assertOutputImports(t, `
import { PassThrough, Writable, pipeline } from 'stream';
const src = new PassThrough();
const mid = new PassThrough();
const sink = new Writable({
  write(chunk: Buffer, _e, cb) { console.log("sink: " + chunk.toString()); cb(); }
});
pipeline(src, mid, sink, (err) => { console.log("pipeline done", err ? err.message : "ok"); });
src.write("a");
src.end("b");
`, "sink: a\nsink: b\npipeline done ok\n")
}

// duplexPair: a write on one side is data on the other.
func TestE2ENodeStreamDuplexPair(t *testing.T) {
	assertOutputImports(t, `
import { duplexPair } from 'stream';
import { mustCall, mustNotCall } from 'test';
const [clientSide, serverSide] = duplexPair();
clientSide.on("data", mustCall((d: Buffer) => { console.log("client got: " + d); }));
clientSide.on("end", mustNotCall());
serverSide.write("foo");
const pair2 = duplexPair();
pair2[1].on("data", (d) => { console.log("side2 got: " + d); });
pair2[1].on("end", () => { console.log("side2 ended"); });
pair2[0].end("bar");
`, "client got: foo\nside2 got: bar\nside2 ended\n")
}

// A byte-mode Readable delivers a pushed string as a Buffer.
func TestE2ENodeReadableDefaultStringChunks(t *testing.T) {
	assertOutputImports(t, `
import stream from 'stream';
const r = new stream.Readable({ read() {} });
r.push("hello");
r.push(null);
r.on('data', (c) => { console.log(c, c.toString()); });
`, "<Buffer 68 65 6c 6c 6f> hello\n")
}

// destroy() before the data flows: close, and nothing read; setEncoding decodes.
func TestE2ENodeStreamDestroyAndSetEncoding(t *testing.T) {
	assertOutputImports(t, `
import stream from 'stream';
const r = new stream.Readable({ read() {} });
r.setEncoding("utf8");
r.push("one");
r.on('data', (c) => { console.log("data", c); });
r.on('close', () => { console.log("closed", r.destroyed); });
r.destroy();
console.log("done");
const e = new stream.Readable({ read() {} });
e.setEncoding("utf8");
e.on('data', (c) => { console.log("decoded", JSON.stringify(c)); });
e.push(Buffer.from([0xce]));
e.push(Buffer.from([0xb1, 0x62]));
`, "done\ndata one\nclosed true\ndecoded \"\u03b1b\"\n")
}

// read() takes the whole buffer in byte mode, one chunk in object mode, null when empty.
func TestE2ENodeReadableSyncRead(t *testing.T) {
	assertOutputImports(t, `
import stream from 'stream';
const r = new stream.Readable({ read() {} });
r.push("first");
r.push("second");
console.log(r.read(3));
console.log(r.read());
console.log(r.read() === null);
const o = new stream.Readable({ objectMode: true, read() {} });
o.push("first");
o.push("second");
console.log(o.read(), o.read(), o.read());
`, "<Buffer 66 69 72>\n<Buffer 73 74 73 65 63 6f 6e 64>\ntrue\nfirst second null\n")
}

// unshift() puts a chunk back at the front of the buffer.
func TestE2ENodeReadableUnshift(t *testing.T) {
	assertOutputImports(t, `
import stream from 'stream';
const r = new stream.Readable({ objectMode: true, read() {} });
r.push("b");
r.unshift("a");
console.log(r.read(), r.read(), r.read() === null);
r.push("x");
const got = r.read();
r.unshift(got);
console.log(r.read());
`, "a b true\nx\n")
}

// new Duplex({ read, write, final }): two independent sides.
func TestE2ENodeDuplexConstructor(t *testing.T) {
	assertOutputImports(t, `
import { Duplex } from 'stream';
const seen: string[] = [];
const d = new Duplex({
  read() { this.push("r1"); this.push(null); },
  write(chunk: Buffer, _e, cb) { seen.push("w:" + chunk.toString()); cb(); },
  final(cb) { seen.push("finished"); cb(); },
});
d.on('data', (c) => { console.log("data:" + c); });
d.write("hello");
d.end();
setTimeout(() => {
  console.log(seen.join(","));
}, 10);
`, "data:r1\nw:hello,finished\n")
}

// setEncoding decodes a UTF-16 surrogate pair, a base64 group and a UTF-8 character split across chunks whole, as string_decoder does.
func TestE2ENodeStreamSetEncodingKeepsSplitCharacters(t *testing.T) {
	assertOutputImports(t, `
import { Readable } from 'stream';
function run(enc: BufferEncoding, parts: number[][]): void {
  const r = new Readable({ read() {} });
  r.setEncoding(enc);
  const got: string[] = [];
  r.on('data', (s) => { got.push(s); });
  r.on('end', () => { console.log(enc, JSON.stringify(got)); });
  for (const p of parts) r.push(Buffer.from(p));
  r.push(null);
}
run('utf16le', [[0x68], [0x00, 0x69, 0x00], [0x3d, 0xd8], [0x00, 0xde]]);
run('base64', [[1, 2], [3, 4], [5, 6, 7]]);
run('hex', [[0xab], [0xcd, 0xef]]);
run('utf8', [[0xe2, 0x82], [0xac, 0x21], [0xf0, 0x9f]]);
run('latin1', [[0xe9, 0x41]]);
`, "utf16le [\"hi\",\"😀\"]\nbase64 [\"AQID\",\"BAUG\",\"Bw==\"]\nhex [\"ab\",\"cdef\"]\nutf8 [\"€!\",\"�\"]\nlatin1 [\"éA\"]\n")
}

// A duplexPair write completes when the peer reads it; end() finishes once the peer has ended.
func TestE2ENodeStreamDuplexPairWriteCompletesOnRead(t *testing.T) {
	assertOutputImports(t, `
import { duplexPair } from 'stream';
const [a, b] = duplexPair();
a.write("one", () => console.log("cb one"));
a.write("two", () => console.log("cb two"));
console.log("after writes", a.writableLength);
b.on("data", (d) => console.log("b data", d.toString()));
a.end(() => console.log("a finished"));
b.on("end", () => console.log("b end"));
b.write("back");
a.on("data", (d) => console.log("a data", d.toString()));
b.end();
`, "after writes 6\ncb one\ncb two\nb data one\nb data two\na data back\nb end\na finished\n")
}
