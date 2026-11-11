package tests

import "testing"

func TestE2EBlockFunctionsAndGlobalImports(t *testing.T) {
	// A block's functions exist from its entry, each able to call a later
	// sibling; a module's same-name re-export (`Blob` from 'buffer') is the
	// global, also through its namespace; `const { x } = globalThis` reads
	// each global; a DOMException's prototype has `code` and the constants.
	assertSameAsNode(t, `
import { Blob } from 'buffer';
import buffer from 'buffer';
if (true) {
  const u = 2;
  const f = () => bind();
  function bind(): number { return dis(); }
  function dis(): number { return u; }
  console.log(f(), bind());
}
{ console.log(typeof h, h()); function h() { return 9; } }
{ const blob = new Blob(['ab']); console.log(blob.size, new buffer.Blob(['abc']).size, new buffer.File(['x'], 'n').name); }
const { crypto, performance: perf } = globalThis;
const a = new Uint8Array(4);
console.log(crypto.getRandomValues(a) === a, typeof crypto.randomUUID(), String(crypto), typeof perf.now());
function g() { const { setTimeout: st, Math: M } = globalThis; return [typeof st, M.max(1, 2)]; }
console.log(g());
const d: any = new DOMException('m', 'TypeMismatchError');
console.log(['code', 'name', 'INDEX_SIZE_ERR', 'nope'].map((k) => k in d), 'code' in new Error('x'));
`)
}

func TestE2EBlockDestructuringReadByBlockFunctionJS(t *testing.T) {
	// JavaScript: a block's function, made at its entry, reads the block's
	// destructured names once the pattern has run.
	assertSameAsNodeCLIJS(t, `
const g = () => [1, 2];
{ const [c] = g(); console.log(c); }
{ const [c] = [2]; function d() { const f = () => c; return f(); } console.log(d()); }
{ const { a: c, b } = { a: 3, b: 4 }; function d() { return c + b; } console.log(d()); }
`)
}

func TestE2EErrorToStringAcrossShapes(t *testing.T) {
	// String() of an error is its toString: a plain error's name and
	// message, a subclass's own method also once caught, and an own one.
	assertSameAsNode(t, `
const e = new Error("x"); e.name = "W";
const c: any = new Error("m"); c.code = "C";
console.log(String(e), `+"`${e}`"+`, String(c), c.toString());
class W extends Error { toString(): string { return "Q" + this.message; } }
class V extends Error { constructor(m: string) { super(m); this.name = "V"; } }
try { throw new W("m"); } catch (err) { console.log(String(err)); }
try { throw new V("q"); } catch (err) { console.log(String(err)); }
process.emitWarning("x y", "DeprecationWarning", "DEP1");
`)
}

func TestE2EStreamsCarryAnyErrorAndWeb(t *testing.T) {
	// A stream destroyed with, or a write callback failing with, a value
	// that is no Error passes that value on; Duplex.from takes a Blob, a
	// WritableStream pair and a rejected promise; Writable.fromWeb and
	// [Symbol.asyncDispose] behave as Node's.
	assertSameAsNode(t, `
import { Readable, Writable, Duplex } from 'stream';
const r = new Readable({ read() {} }); r.on('error', (e) => console.log('err', e, typeof e)); (r as any).destroy('m');
const w0 = new Writable({ write(c: any, e: any, cb: any) { cb('bad'); } }); w0.on('error', (e) => console.log('werr', e)); w0.write('x');
Duplex.from(Promise.reject('rj')).on('error', (e) => console.log('rej', e));
Duplex.from(new Blob(['blob'])).on('data', (ab: any) => console.log('blob', ab.byteLength));
let ret = '';
const w = Writable.fromWeb(new WritableStream({ write(chunk: any) { ret += chunk; } }));
w.write('ab');
w.end('c', () => console.log('end', ret));
let r2 = '';
const d = Duplex.from({ writable: new WritableStream({ write(ch: any) { r2 += ch; } }) });
console.log(d.readable, d.writable);
d.end('zz', () => console.log('d', r2));
const w2 = new Writable({ write(c: any, e: any, cb: any) { cb(); } });
w2.on('error', (e: any) => console.log('w2', e.name));
w2[Symbol.asyncDispose]().then(() => console.log('disposed', w2.destroyed));
`)
}

func TestE2EMustCallKeepsReceiverJS(t *testing.T) {
	// mustCall's wrapper calls the function with the call's receiver, as
	// Node's test/common does (`fn.apply(this, arguments)`).
	assertOutputCLIJS(t, `
import { mustCall } from 'test';
const o = { k: 5, m: null };
o.m = mustCall(function (a) { console.log(this.k, a); process.nextTick(() => console.log('nt', this.k)); });
o.m(1);
`, "5 1\nnt 5")
}

func TestE2EClassReadsLetAssignedAfter(t *testing.T) {
	// A class reading a function's `let` declared without a value (alone
	// or in a list) and assigned afterwards sees its current value.
	assertSameAsNode(t, `
function f(alt: boolean): string {
  let si: (cb: () => number) => string, ci: number | undefined;
  if (!alt) { si = (cb) => "real" + cb(); ci = 1; } else { si = function s(cb) { return "fake"; }; }
  class P {
    k = 3;
    start(): string { return si(() => this.k) + ci; }
  }
  const p = new P();
  const first = p.start();
  ci = 7;
  return first + " " + p.start();
}
console.log(f(false), f(true));
`)
	assertSameAsNodeCLIJS(t, `
function f(alt) {
  let si, ci;
  if (!alt) { si = (cb) => "real"; ci = 1; } else { si = function s(cb) { return "fake"; }; }
  class P {
    constructor() { this.k = 0; }
    start() { return si(() => this.k) + ci; }
  }
  return new P().start();
}
console.log(f(false), f(true));
`)
}

func TestE2EProcessFeatures(t *testing.T) {
	// process.features of the baseline release (v22.11.0): its keys in its
	// order, the last three as getters; this build has no inspector.
	assertOutput(t, `
const f: any = process.features;
const shape = Object.keys(f).map((k) => k + ":" + typeof f[k] + ":" + (Object.getOwnPropertyDescriptor(f, k)!.get ? "get" : "value"));
console.log(shape.join(" "));
console.log(f.inspector, f.uv, f.tls, f.typescript);
`, "inspector:boolean:value debug:boolean:value uv:boolean:value ipv6:boolean:value tls_alpn:boolean:value tls_sni:boolean:value tls_ocsp:boolean:value tls:boolean:value cached_builtins:boolean:get require_module:boolean:get typescript:boolean:get\nfalse true true false")
}

func TestE2EGlobalObjectCryptoAndGenericDescriptor(t *testing.T) {
	// The global object carries `crypto` (enumerable), whose prototype's
	// WebIDL members, `subtle` among them, are enumerable; a descriptor
	// naming only attributes keeps an accessor an accessor.
	assertSameAsNodeCLIJS(t, `
const self = globalThis;
console.log(typeof self.crypto, typeof self.crypto.subtle.importKey, Object.keys(self).includes("crypto"), self.crypto === crypto);
const P = Object.getPrototypeOf(self.crypto);
for (const k of Object.getOwnPropertyNames(P)) { const d = Object.getOwnPropertyDescriptor(P, k); console.log(k, d.enumerable, typeof d.get, typeof d.value); }
class K { get g() { return 1; } }
Object.defineProperty(K.prototype, "g", { enumerable: true });
const d = Object.getOwnPropertyDescriptor(K.prototype, "g");
console.log(d.enumerable, typeof d.get, new K().g);
let s = 0;
const o = { get h() { return 2; }, set h(v) { s = v; } };
Object.defineProperty(o, "h", { enumerable: false });
Object.defineProperty(o, "h", { get() { return 5; } });
o.h = 9;
console.log(o.h, s, Object.keys(o).length);
`)
}

func TestE2EReadableFromWebErrorsAndCancel(t *testing.T) {
	// Readable.fromWeb as Node's: the options' encoding, the web stream's
	// error destroying it, and destroy cancelling the reader.
	assertSameAsNode(t, `
import { Readable } from 'stream';
const rs = new ReadableStream({ start(c) { c.enqueue('a'); c.enqueue('b'); c.close(); } });
const r = Readable.fromWeb(rs, { encoding: 'utf8' });
r.on('data', (d) => console.log('data', d)); r.on('end', () => console.log('end')); r.on('close', () => console.log('close'));
const bad = new ReadableStream({ pull(c) { c.error(new Error('boom')); } });
const r2 = Readable.fromWeb(bad);
r2.on('error', (e) => console.log('err', e.message)); r2.resume();
let cancelled: any = 'no';
const slow = new ReadableStream({ cancel(reason) { cancelled = reason; } });
const r3 = Readable.fromWeb(slow);
r3.on('close', () => console.log('r3 close', String(cancelled)));
r3.destroy();
`)
}

func TestE2EWritevAndArrayParamAdapter(t *testing.T) {
	// Writable's writev (options and corked batches) as Node's, and a
	// function whose parameter is an object-element array cast to one
	// taking any[] converts its argument.
	assertSameAsNode(t, `
import { Writable } from 'stream';
const w = new Writable({
  write(c: any, e: any, cb: any) { console.log('write', String(c)); cb(); },
  writev(chunks: any[], cb: any) { console.log('writev', chunks.map((x) => String(x.chunk) + ':' + x.encoding).join(',')); cb(); },
});
w.cork(); w.write('a'); w.write('b'); w.write('c'); process.nextTick(() => w.uncork());
const w2 = new Writable({ writev(chunks: any[], cb: any) { console.log('only writev', chunks.length); setTimeout(cb, 1); } });
w2.write('x'); w2.write('y'); w2.write('z'); w2.end(() => console.log('w2 done'));
interface O { w?: (chunks: Array<{ chunk: any }>) => void; }
const o: O = { w(chunks) { console.log(chunks.map((x) => String(x.chunk)).join(',')); } };
const g = o.w as (chunks: any[]) => void;
const a: any = [{ chunk: 'q' }];
g(a);
`)
}

func TestE2EStreamToWebBackpressureAndDuplexWeb(t *testing.T) {
	// Readable.toWeb pauses the source under the high-water mark, error and
	// cancel carry across, and Duplex.toWeb/fromWeb compose the halves.
	assertSameAsNode(t, `
import { Readable, Duplex, PassThrough } from 'stream';
let n = 0;
const r = new Readable({ highWaterMark: 4, read() { n++; if (n > 6) this.push(null); else this.push(Buffer.from('ab')); } });
const reader = Readable.toWeb(r).getReader();
setTimeout(async () => {
  console.log('before', n, r.isPaused());
  let total = 0;
  for (;;) { const { done, value } = await reader.read(); if (done) break; total += value.byteLength; }
  console.log('total', total, n);
  const r3 = new Readable({ read() {} });
  await Readable.toWeb(r3).cancel('why');
  console.log('r3', r3.destroyed);
  const { readable, writable } = Duplex.toWeb(new PassThrough());
  const w = writable.getWriter(); await w.write(Buffer.from('hi')); await w.close();
  const rr = readable.getReader(); let s = '';
  for (;;) { const { done, value } = await rr.read(); if (done) break; s += Buffer.from(value).toString(); }
  console.log('toWeb', s);
  const ts = new TransformStream({ transform(c, ctl) { ctl.enqueue(String(c).toUpperCase()); } });
  const d = Duplex.fromWeb(ts, { encoding: 'utf8' });
  d.on('data', (c) => console.log('fromWeb', c));
  d.on('end', () => console.log('end'));
  d.end('abc');
}, 20);
`)
}

func TestE2EStreamUtilsAndWrap(t *testing.T) {
	// stream's default high-water marks, its isReadable/isWritable/
	// isErrored/isDisturbed, and Readable.wrap over an old-style stream.
	assertSameAsNode(t, `
import * as stream from 'stream';
import { Readable, PassThrough, getDefaultHighWaterMark, setDefaultHighWaterMark } from 'stream';
import { EventEmitter } from 'events';
console.log(getDefaultHighWaterMark(false), getDefaultHighWaterMark(true));
setDefaultHighWaterMark(true, 4); console.log(new Readable({ objectMode: true, read() {} }).readableHighWaterMark);
try { setDefaultHighWaterMark(false, -1); } catch (e: any) { console.log(e.code); }
const p = new PassThrough();
console.log(stream.isReadable(p), stream.isWritable(p), stream.isErrored(p), stream.isDisturbed(p), stream.isReadable({} as any));
p.on('data', () => {}); p.write('x');
const old: any = new EventEmitter();
old.pause = () => console.log('paused'); old.resume = () => console.log('resumed');
const r = new Readable({ highWaterMark: 2 }).wrap(old);
r.setEncoding('utf8');
r.on('data', (d) => console.log('data', d)); r.on('end', () => console.log('end'));
old.emit('data', Buffer.from('ab')); old.emit('data', Buffer.from('cd'));
setTimeout(() => {
  old.emit('end');
  console.log(stream.isDisturbed(p)); p.on('error', () => {}); p.destroy(new Error('e'));
  setTimeout(() => console.log(stream.isErrored(p), stream.isReadable(p), stream.isWritable(p)), 5);
}, 5);
`)
}

func TestE2EOverrideCallbackParamAndDeclaredCause(t *testing.T) {
	// An override whose callback parameter's own parameter is typed
	// differently from the base's still receives the base's callback
	// converted; an error class declaring `cause` lists it once.
	assertSameAsNode(t, `
class B { d(err: any, cb: (e?: any) => void): void { cb(err); } run(x: any) { this.d(x, (er?: any) => console.log('got', er?.name, typeof er)); } }
class C extends B { d(err: Error | null, cb: (e?: Error | null) => void): void { cb(err); } }
new C().run(new TypeError('t'));
class A extends Error { cause: any; code: string; constructor(c: any) { super('m'); this.code = 'X'; this.cause = c; } }
class D extends A { constructor() { super(1); this.cause = 2; } }
console.log(Object.keys(new D()), Object.keys(new A(3)), JSON.stringify(new A(4)));
class Q extends Error {}
const q = new Q('x'); q.cause = 2; console.log(Object.keys(q));
`)
}

func TestE2ECryptoGlobalClassAndRequireDefault(t *testing.T) {
	// The global Crypto is the Web Crypto object's class, its members
	// reject a foreign receiver; require of events/assert binds Node's
	// module.exports (the default export); util.inherits links prototypes.
	assertSameAsNodeCLIJS(t, `
const P = Object.getPrototypeOf(globalThis.crypto);
console.log(globalThis.crypto instanceof Crypto, ({}) instanceof Crypto, P === Crypto.prototype);
try { new Crypto(); } catch (e) { console.log(e.code); }
for (const f of [() => Object.getOwnPropertyDescriptor(P, 'subtle').get.call({}), () => P.randomUUID.call({}), () => P.getRandomValues.call({}, new Uint8Array(1))]) {
  try { f(); console.log('no throw'); } catch (e) { console.log(e.code); }
}
const E = require('events'); const assert = require('assert'); const util = require('util');
console.log(typeof E, typeof E.once, E === E.EventEmitter, typeof assert, typeof assert.strictEqual);
function A() {} A.prototype.hi = function () { return 'hi'; };
function B() {} util.inherits(B, A);
console.log(new B().hi(), B.super_ === A, Object.keys(B));
try { util.inherits(B, null); } catch (e) { console.log(e.code); }
`)
}

func TestE2ERequireAnywhereAndNodeModule(t *testing.T) {
	// require inside a function, as a value, by a computed name and with
	// an unknown name (MODULE_NOT_FOUND); node:module's builtinModules,
	// isBuiltin and createRequire; a module has one namespace object.
	assertSameAsNodeCLIJS(t, `
function f() { const fs = require("fs"); return typeof fs.readFileSync; }
console.log(f(), typeof require, typeof require.resolve, require.resolve('os'), require.main === module);
const name = ['o', 's'].join('');
console.log(typeof require(name).platform, require('node:path') === require('path'));
try { require('nope-mod'); } catch (e) { console.log(e.code, e.message.split('\n')[0]); }
try { require(name + 'x'); } catch (e) { console.log(e.code); }
try { require(1); } catch (e) { console.log(e.code); }
const { builtinModules, isBuiltin, createRequire } = require('module');
console.log(builtinModules.includes('fs'), isBuiltin('node:fs'), isBuiltin('fs/x'), isBuiltin('node:test'), isBuiltin('test'));
const r2 = createRequire(__filename); console.log(r2('events') === require('events'));
try { createRequire('rel'); } catch (e) { console.log(e.code); }
`)
}

func TestE2ECreateRequireInModule(t *testing.T) {
	// A require function made with createRequire in an ES module finds the
	// builtins the file names.
	assertSameAsNode(t, `
import { createRequire, builtinModules, isBuiltin } from "node:module";
import Module from "module";
const require2 = createRequire(import.meta.url);
console.log(typeof require2("os").platform, builtinModules.length > 50, isBuiltin("fs"), typeof Module, Module.isBuiltin("os"));
try { require2("./missing-file"); } catch (e: any) { console.log(e.code); }
`)
}

func TestE2ELibraryConstructorCallOnThis(t *testing.T) {
	// The pre-class inheritance pattern: a library class's constructor run
	// on `this` of a function inheriting its prototype, the function's own
	// prototype members overriding the class's.
	assertSameAsNodeCompatJS(t, `
const EventEmitter = require('events'); const util = require('util');
const { Readable, Writable, Transform } = require('stream');
function Foo(name) { EventEmitter.call(this); this.name = name; }
util.inherits(Foo, EventEmitter);
Foo.prototype.hello = function () { return 'hi ' + this.name; };
const f = new Foo('a');
console.log(f instanceof Foo, f instanceof EventEmitter, f.hello(), Object.getPrototypeOf(f) === Foo.prototype, f.constructor === Foo);
f.once('e', function (x) { console.log('once', x, this === f); }); f.emit('e', 2); f.emit('e', 3);
function Bar() { Foo.call(this, 'b'); } util.inherits(Bar, Foo);
console.log(new Bar().hello(), new Bar() instanceof EventEmitter);
function W() { Writable.call(this); this.got = []; } util.inherits(W, Writable);
W.prototype._write = function (c, enc, cb) { this.got.push(String(c)); cb(); };
const w = new W(); w.write('a'); w.end('b', () => console.log('w', w.got.join(',')));
function T() { Transform.call(this); } util.inherits(T, Transform);
T.prototype._transform = function (c, e, cb) { cb(null, String(c).toUpperCase()); };
const t = new T(); t.on('data', (d) => console.log('t', String(d))); t.write('x'); t.end();
function R() { Readable.call(this); this.i = 0; } util.inherits(R, Readable);
R.prototype._read = function () { this.push(this.i < 2 ? 'r' + this.i++ : null); };
new R().on('data', (d) => console.log('r', String(d)));
class K { constructor(v) { this.v = v; } }
try { K.call({}, 1); } catch (e) { console.log(e.constructor.name, e.message); }
`)
}

func TestE2EInstancePrototypeReplaced(t *testing.T) {
	// A class instance given another prototype (Object.setPrototypeOf,
	// Reflect.construct's newTarget): its members, calls and instanceof
	// read that chain.
	assertSameAsNodeCompatJS(t, `
class C { constructor(n) { this.n = n; this.first = this.step(); } run() { return this.step() + 1; } step() { return 10; } }
const c = new C(5);
const P = Object.create(C.prototype); P.step = function () { return 100 + this.n; };
Object.setPrototypeOf(c, P);
console.log(Object.getPrototypeOf(c) === P, c instanceof C, c.step(), c.run());
const c2 = new C(1); Object.setPrototypeOf(c2, null); console.log(Object.getPrototypeOf(c2), c2.n, typeof c2.run);
function F() {} F.prototype = Object.create(C.prototype); F.prototype.step = function () { return 7; };
const o = Reflect.construct(C, [3], F);
console.log(o.run(), o.first, o instanceof F, o instanceof C, Object.getPrototypeOf(o) === F.prototype);
function G() { this.g = 1; } function H() {} H.prototype.h = 2;
const g = Reflect.construct(G, [], H); console.log(g.g, g.h, g instanceof H, g instanceof G);
try { Reflect.construct(C, [], () => {}); } catch (e) { console.log(e.constructor.name, e.message); }
const c3 = new C(9); console.log(Object.getPrototypeOf(c3) === C.prototype, c3.run());
`)
}
