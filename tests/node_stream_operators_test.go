package tests

import "testing"

// Node's stream operators (lib/node/stream.ts, ported from Node v24's
// lib/internal/streams/operators.js) and Readable.from over any iterable;
// each program's expected output taken from Node v24.

// map and filter chain into toArray.
func TestE2EStreamOperatorsMapFilterToArray(t *testing.T) {
	assertOutputImports(t, `
import { Readable } from 'stream';
async function main() {
  const r = await Readable.from([1, 2, 3, 4]).map((x: number) => x * 2).filter((x: number) => x > 2).toArray();
  console.log(r);
  console.log(await Readable.from([1, 2, 3]).map(async (x: number) => x * 10).toArray());
}
main();
`, "[ 4, 6, 8 ]\n[ 10, 20, 30 ]\n")
}

// flatMap, drop and take.
func TestE2EStreamOperatorsFlatMapDropTake(t *testing.T) {
	assertOutputImports(t, `
import { Readable } from 'stream';
async function main() {
  const src = () => Readable.from([1, 2, 3, 4, 5]);
  console.log(await src().flatMap((x: number) => [x, x]).toArray());
  console.log(await src().drop(2).toArray());
  console.log(await src().take(2).toArray());
  console.log(await src().map((x: number) => x * 2).filter((x: number) => x > 4).take(2).toArray());
}
main();
`, "[\n  1, 1, 2, 2, 3,\n  3, 4, 4, 5, 5\n]\n[ 3, 4, 5 ]\n[ 1, 2 ]\n[ 6, 8 ]\n")
}

// forEach, some, every, find and reduce.
func TestE2EStreamOperatorsPromiseReturning(t *testing.T) {
	assertOutputImports(t, `
import { Readable } from 'stream';
async function main() {
  const src = () => Readable.from([1, 2, 3, 4, 5]);
  const seen: number[] = [];
  await src().forEach((x: number) => { seen.push(x); });
  console.log(seen);
  console.log(await src().some((x: number) => x > 4), await src().some((x: number) => x > 9));
  console.log(await src().every((x: number) => x > 0), await src().every((x: number) => x > 1));
  console.log(await src().find((x: number) => x > 2), await src().find((x: number) => x > 9));
  console.log(await src().reduce((a: number, b: number) => a + b), await src().reduce((a: number, b: number) => a + b, 100));
  try { await Readable.from([]).reduce((a: number, b: number) => a + b); } catch (e) { console.log((e as any).code, (e as Error).message); }
}
main();
`, "[ 1, 2, 3, 4, 5 ]\ntrue false\ntrue false\n3 undefined\n15 115\nERR_MISSING_ARGS Reduce of an empty stream requires an initial value\n")
}

// map with concurrency runs calls in parallel and yields in order.
func TestE2EStreamOperatorsConcurrencyKeepsOrder(t *testing.T) {
	assertOutputImports(t, `
import { Readable } from 'stream';
async function main() {
  const out = await Readable.from([1, 2, 3, 4, 5]).map(async (x: number) => {
    await new Promise((r) => setTimeout(r, 6 - x));
    return x;
  }, { concurrency: 3 }).toArray();
  console.log(out);
}
main();
`, "[ 1, 2, 3, 4, 5 ]\n")
}

// a throwing callback, bad arguments and an aborted signal.
func TestE2EStreamOperatorsErrors(t *testing.T) {
	assertOutputImports(t, `
import { Readable } from 'stream';
async function main() {
  const src = () => Readable.from([1, 2, 3]);
  try { await src().map((x: number) => { if (x === 3) throw new Error('boom'); return x; }).toArray(); } catch (e) { console.log('throw', (e as Error).message); }
  try { src().map(5 as any); } catch (e) { console.log('fn', (e as any).code); }
  try { src().take(-1); } catch (e) { console.log('take', (e as any).code); }
  const ac = new AbortController();
  ac.abort();
  try { await src().toArray({ signal: ac.signal }); } catch (e) { console.log('aborted', (e as Error).name); }
}
main();
`, "throw boom\nfn ERR_INVALID_ARG_TYPE\ntake ERR_OUT_OF_RANGE\naborted AbortError\n")
}

// Readable.from over async and sync generators, an array and a class iterable.
func TestE2EReadableFromIterables(t *testing.T) {
	assertOutputImports(t, `
import { Readable } from 'stream';
async function* agen(): AsyncGenerator<string> { yield 'a'; yield 'b'; }
function* sgen(): Generator<number> { yield 1; yield 2; }
class Countdown {
  n = 3;
  [Symbol.iterator]() { return this; }
  next(): { value: number, done: boolean } { this.n--; return { value: this.n, done: this.n < 0 }; }
}
async function main() {
  for await (const c of Readable.from(agen())) console.log('async', c);
  for await (const c of Readable.from(sgen())) console.log('sync', c);
  for await (const c of Readable.from([5, 6])) console.log('array', c);
  for await (const c of Readable.from(new Countdown())) console.log('class', c);
  for await (const c of Readable.from('whole')) console.log('string', c);
  try { Readable.from(42 as any); } catch (e) { console.log((e as any).code); }
}
main();
`, "async a\nasync b\nsync 1\nsync 2\narray 5\narray 6\nclass 2\nclass 1\nclass 0\nstring whole\nERR_INVALID_ARG_TYPE\n")
}

// an iterator's error destroys the stream with that same error.
func TestE2EReadableFromErrorPropagates(t *testing.T) {
	assertOutputImports(t, `
import { Readable } from 'stream';
async function* bad(): AsyncGenerator<number> { yield 1; throw new Error('gen'); }
async function main() {
  try {
    for await (const c of Readable.from(bad())) console.log('got', c);
  } catch (e) {
    console.log('caught', (e as Error).message);
  }
}
main();
`, "got 1\ncaught gen\n")
}

// the signal option and addAbortSignal destroy a stream with an AbortError.
func TestE2EStreamSignalOption(t *testing.T) {
	assertOutputImports(t, `
import { Readable, Writable, Duplex, addAbortSignal } from 'stream';
const ac = new AbortController();
const r = new Readable({ read() {}, signal: ac.signal });
r.on('error', (e: any) => console.log('r err', e.name, e.code, e.message));
r.on('close', () => console.log('r close'));
const w = new Writable({ write(c, e, cb) { cb(); }, signal: ac.signal });
w.on('error', (e: any) => console.log('w err', e.name));
const d = new Duplex({ read() {}, write(c, e, cb) { cb(); }, signal: ac.signal });
d.on('error', (e: any) => console.log('d err', e.name));
ac.abort();
const ac2 = new AbortController();
ac2.abort('why');
const r2 = addAbortSignal(ac2.signal, new Readable({ read() {} }));
r2.on('error', (e: any) => console.log('r2 err', e.name, e.cause));
try { addAbortSignal({} as any, r2); } catch (e) { console.log((e as any).code); }
`, "ERR_INVALID_ARG_TYPE\nr err AbortError ABORT_ERR The operation was aborted\nr close\nw err AbortError\nd err AbortError\nr2 err AbortError why\n")
}

// pipeline with generator stages and a promise-returning sink, compose, and Duplex.from.
func TestE2EStreamPipelineComposeDuplexFrom(t *testing.T) {
	assertOutputImports(t, `
import { Readable, Writable, Transform, PassThrough, Duplex, pipeline, compose } from 'stream';
import { pipeline as pipelineP } from 'stream/promises';
async function* upper(source: any): AsyncGenerator<string> {
  for await (const chunk of source) yield String(chunk).toUpperCase();
}
async function main() {
  const out: string[] = [];
  const sink = new Writable({ objectMode: true, write(c, e, cb) { out.push(String(c)); cb(); } });
  await new Promise<void>((resolve) => {
    pipeline(Readable.from(['a', 'b']), upper, sink, (err: any) => { console.log('cb', err ?? null, out); resolve(); });
  });
  const res = await pipelineP(Readable.from(['x', 'y']), upper, async (source: any) => {
    let all = '';
    for await (const c of source) all += c;
    return all;
  });
  console.log('value', res);
  const c = compose(upper, new Transform({ objectMode: true, transform(chunk, enc, cb) { cb(null, chunk + '!'); } }));
  const got: string[] = [];
  c.on('data', (d: any) => got.push(String(d)));
  c.on('end', () => console.log('compose', got));
  c.write('hi');
  c.end('yo');
  await new Promise((r) => setTimeout(r, 20));
  const d = Duplex.from(['p', 'q']);
  for await (const v of d) console.log('from', v);
  const d2 = Duplex.from(async function* (src: any) { for await (const v of src) yield v + v; });
  d2.write('m');
  d2.end();
  for await (const v of d2) console.log('fromfn', v);
  try { await pipelineP(Readable.from(['z']), async function* (src: any) { for await (const v of src) throw new Error('bad ' + v); }, new PassThrough({ objectMode: true })); } catch (e) { console.log('err', (e as Error).message); }
}
main();
`, "cb null [ 'A', 'B' ]\nvalue XY\ncompose [ 'HI!', 'YO!' ]\nfrom p\nfrom q\nfromfn mm\nerr bad z\n")
}

// A web stream of typed chunks read where ReadableStream<any> is declared
// is converted (ADR-01189): `Readable.fromWeb(blob.stream())` and an
// annotated binding read the bytes, not their representation.
func TestE2EReadableStreamTypedChunksAsAny(t *testing.T) {
	assertSameAsNodeImports(t, `
import { Readable } from 'stream'
const r = Readable.fromWeb(new Blob(["hello web"]).stream())
r.on('data', (c: any) => console.log('chunk', String(c)))
r.on('end', async () => {
  const w: ReadableStream<any> = new Blob(["ab"]).stream()
  const rd = w.getReader()
  const x = await rd.read()
  console.log(x.done, String(x.value))
  console.log((await rd.read()).done)
})
`)
}

// A Duplex held as a Writable (Node's types allow it) writes through the
// Writable it is typed as; a subclass's override of a library method is
// still the one a library-typed call reaches.
func TestE2EStreamDuplexAsWritableAndOverride(t *testing.T) {
	assertSameAsNodeImports(t, `
import { Writable, PassThrough, Readable } from 'stream';
const p = new PassThrough();
p.on('data', (c: any) => console.log('got', String(c)));
const w: Writable = p;
w.write('x');
class Loud extends PassThrough {
  push(chunk: any, encoding?: BufferEncoding): boolean {
    if (chunk !== null) console.log('push', String(chunk));
    return super.push(chunk, encoding);
  }
}
const l = new Loud();
const r: Readable = l;
r.on('data', () => {});
l.write('y');
`)
}

// An underlying source or sink is read as JavaScript reads it: from any
// object (held in a variable, typed or not), members it does not use
// ignored, callbacks declaring more parameters than the stream passes
// called with the rest undefined, and its type member checked as Node does.
func TestE2EStreamUnderlyingObjectsJS(t *testing.T) {
	assertSameAsNodeCompatJS(t, `
function pullFn(c, extra) { console.log("pull", typeof extra); c.enqueue("x"); c.close(); }
const src = {
  start(c, z) { console.log("start", typeof z); },
  pull: pullFn,
  cancel(reason, q) { console.log("cancel", reason, q); },
};
const rs = new ReadableStream(src);
const r = rs.getReader();
const rs2 = new ReadableStream({ start: async (c) => { c.enqueue(1); }, cancel: (r) => console.log("c2", r) });
(async () => { await rs2.cancel("why"); })();
(async () => { const rr = new ReadableStream({ pull: pullFn, other: 1 }).getReader(); console.log(await rr.read()); })();
try { new ReadableStream({ type: "zz" }); console.log("ok zz"); } catch (e) { console.log(e.name, e.message); }
try { new WritableStream({ type: "x" }); console.log("ok w"); } catch (e) { console.log(e.name, e.message); }
try { new WritableStream({ type: undefined }); console.log("ok w2"); } catch (e) { console.log(e.name, e.message); }
`)
}

// ReadableStream.from over any iterable that is no array: an async
// generator, a Set, a string, a sync iterator (closed on cancel), and its
// errors.
func TestE2EReadableStreamFromIterableJS(t *testing.T) {
	assertSameAsNodeCompatJS(t, `
(async () => {
async function* ag() { yield 1; yield 2; }
for await (const v of ReadableStream.from(ag())) console.log("ag", v);
for await (const v of ReadableStream.from(new Set(["a", "b"]))) console.log("set", v);
for await (const v of ReadableStream.from("hi")) console.log("str", v);
const bad = { [Symbol.asyncIterator]() { return { next() { return 5; } }; } };
try { for await (const v of ReadableStream.from(bad)) console.log(v); } catch (e) { console.log(e.name, e.message); }
let closed = false;
const it = { [Symbol.iterator]() { let i = 0; return { next() { return { value: i++, done: false }; }, return() { closed = true; return {}; } }; } };
const rd = ReadableStream.from(it).getReader();
console.log(await rd.read()); await rd.cancel("x"); console.log("closed", closed);
try { ReadableStream.from(); } catch (e) { console.log(e.name, e.message); }
for (const x of [5, null]) { try { ReadableStream.from(x); } catch (e) { console.log(e.name, e.code, e.message); } }
})();
`)
}

func TestE2EReadableStreamFromAsyncGenerator(t *testing.T) {
	assertOutput(t, `
async function* ag(): AsyncGenerator<number> { yield 1; yield 2; }
const s: ReadableStream<number> = ReadableStream.from(ag());
async function main(): Promise<void> {
  for await (const v of s) console.log("v", v + 1);
  const r = ReadableStream.from(new Set(["a"])).getReader();
  const x = await r.read();
  console.log(x.value, x.done);
}
main();
`, "v 2\nv 3\na false")
}

// enqueue() with no chunk enqueues undefined; extra arguments are ignored.
func TestE2EStreamEnqueueArityJS(t *testing.T) {
	assertSameAsNodeCompatJS(t, `
const rs = new ReadableStream({ start(c) { c.enqueue(); c.enqueue(2, 3); c.close(); } });
(async () => { for await (const v of rs) console.log(v); })();
`)
}

// Calling a method a stream, reader or controller lacks throws JavaScript's
// TypeError.
func TestE2EStreamMissingMethodJS(t *testing.T) {
	assertSameAsNodeCompatJS(t, `
const rs = new ReadableStream({ start(c) { try { c.zap(1); } catch (e) { console.log(e.name, e.message); } c.close(); } });
try { rs.zork(); } catch (e) { console.log(e.name, e.message); }
const rd = rs.getReader();
try { rd.nope(); } catch (e) { console.log(e.name, e.message); }
`)
}

// A queuing strategy is read from any object, its unknown members ignored;
// highWaterMark defaults when undefined and is validated as Node does, and
// the strategy classes check their init.
func TestE2EStreamQueuingStrategyJS(t *testing.T) {
	assertSameAsNodeCompatJS(t, `
const t = (n, f) => { try { console.log(n, f()); } catch (e) { console.log(n, e.name, e.code, e.message); } };
t("unk", () => { new ReadableStream({}, { highWaterMark: 1, extra: 2 }); return "ok"; });
t("neg", () => new ReadableStream({}, { highWaterMark: -1 }));
t("nan", () => new ReadableStream({}, { highWaterMark: NaN }));
t("undefHwm", () => { new ReadableStream({ start(c) { console.log("ds", c.desiredSize); } }, { highWaterMark: undefined }); return "ok"; });
const strat = { highWaterMark: 3, size() { return 1; } };
t("var", () => { new ReadableStream({ start(c) { console.log("desired", c.desiredSize); } }, strat); return "ok"; });
t("cqs0", () => new ReadableStream({}, new CountQueuingStrategy()));
t("cqsNoHwm", () => new ReadableStream({}, new CountQueuingStrategy({})));
t("cqs2", () => { new ReadableStream({ start(c) { console.log("ds2", c.desiredSize); } }, new CountQueuingStrategy({ highWaterMark: 2 }, 9)); return "ok"; });
t("blqs", () => { new ReadableStream({ start(c) { c.enqueue(new Uint8Array(3)); console.log("ds3", c.desiredSize); } }, new ByteLengthQueuingStrategy({ highWaterMark: 8, size: () => 5 })); return "ok"; });
t("dynSize", () => { const s = JSON.parse('{"highWaterMark": 4}'); new ReadableStream({ start(c) { c.enqueue(1); console.log("ds4", c.desiredSize); } }, s); return "ok"; });
`)
}

// pipeTo/pipeThrough options and transformers read from any object (unknown
// members ignored), a transformer's start, missing or wrong arguments with
// Node's errors (pipeTo rejects, pipeThrough throws), extras ignored.
func TestE2EStreamPipeArgumentsJS(t *testing.T) {
	assertSameAsNodeCompatJS(t, `
const t = (n, f) => f().then((v) => console.log(n, v), (e) => console.log(n, e.name, e.code, e.message));
const src = () => new ReadableStream({ start(c) { c.enqueue("a"); c.enqueue("b"); c.close(); } });
const sink = (tag) => new WritableStream({ write(ch) { console.log(tag, ch); } });
(async () => {
await t("opts", () => src().pipeTo(sink("w1"), { preventClose: true, extra: 1 }));
const o = { preventAbort: false };
await t("optVar", () => src().pipeTo(sink("w2"), o));
await t("noArg", () => src().pipeTo());
await t("badDest", () => src().pipeTo({}));
await t("extraArg", () => src().pipeTo(sink("w3"), {}, 5));
await t("badSignal", () => src().pipeTo(sink("w4"), { signal: 5 }));
const tr = { transform(ch, c) { c.enqueue(ch.toUpperCase()); }, other: 1 };
await t("trVar", () => src().pipeThrough(new TransformStream(tr)).pipeTo(sink("w5")));
await t("trFlush", () => src().pipeThrough(new TransformStream({ flush(c) { c.enqueue("END"); } })).pipeTo(sink("w6")));
try { src().pipeThrough({}); } catch (e) { console.log("ptBad", e.name, e.code, e.message); }
try { src().pipeThrough(); } catch (e) { console.log("ptNone", e.name, e.code, e.message); }
await t("tr3", () => src().pipeThrough(new TransformStream({ transform(ch, c, x) { c.enqueue(ch + typeof x); } })).pipeTo(sink("w7")));
await t("trStart", () => src().pipeThrough(new TransformStream({ start(c) { c.enqueue("S"); } })).pipeTo(sink("w8")));
})();
`)
}
