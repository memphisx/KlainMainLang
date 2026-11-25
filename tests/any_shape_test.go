package tests

import "testing"

// Static values held in `any` (TDD-00230 phase 5): an object's header word
// names its layout, whose generated get/set/methods serve dynamic access;
// generators and promises keep their protocols through `any`; for...of over
// `any` follows the iterator protocol. Expected output taken from Node v24.

// fields, getters, default and rest parameters of a class instance read and called through any.
func TestE2EAnyStaticObjectFieldsAndMethods(t *testing.T) {
	assertOutput(t, `
class Box {
  v: number;
  label = "box";
  constructor(v: number) { this.v = v; }
  double(k: number = 2): number { return this.v * k; }
  get twice(): number { return this.v * 2; }
  greet(name: string, ...rest: string[]): string { return "hi " + name + " " + rest.length; }
}
function probe(o: any) {
  console.log(o.v, o.label, o.twice, o.double(), o.double(3), o.greet("a", "b", "c"));
  o.v = 10;
  console.log(o.v, o.double(), o.nope);
  console.log(o.double === o.double, typeof o.double);
}
probe(new Box(4));
const lit = { x: 1, y: "two", z: [1, 2, 3] };
const a: any = lit;
console.log(a.x, a.y, a.z.length, a.missing);
a.x = 42;
console.log(lit.x);
`, "4 box 8 8 12 hi a 2\n10 20 undefined\ntrue function\n1 two 3 undefined\n42\n")
}

// a generator held in any keeps next/return and its iterator method.
func TestE2EAnyGeneratorProtocol(t *testing.T) {
	assertOutput(t, `
function* sgen() { yield 1; yield 2; }
async function* agen() { yield 'a'; yield 'b'; }
async function run() {
  const s: any = sgen();
  console.log(s.next().value, s.next().done, s.next().done);
  const a: any = agen();
  const r = await a.next();
  console.log(r.value, r.done, typeof a[Symbol.asyncIterator], a[Symbol.asyncIterator]() === a);
  const k = Symbol.asyncIterator;
  console.log(typeof a[k]);
}
run();
`, "1 false true\na false function true\nfunction\n")
}

// a promise held in any is awaitable, thenable and adopted by Promise.resolve.
func TestE2EAnyPromise(t *testing.T) {
	assertOutput(t, `
async function five(): Promise<number> { return 5; }
async function run() {
  const p: any = five();
  console.log(await p, p === p);
  p.then((v: any) => console.log('then', v));
  const q = Promise.resolve(p);
  console.log(await q);
  const r: any = Promise.reject(new Error('bad'));
  r.catch((e: any) => console.log('caught', e.message));
  const plain: any = 7;
  console.log(await plain);
}
run();
`, "5 true\nthen 5\n5\ncaught bad\n7\n")
}

// for...of and for await...of over any: generators, arrays, a class iterator, early exit closing the iterator.
func TestE2EAnyForOfProtocol(t *testing.T) {
	assertOutput(t, `
async function* gen() { yield 'a'; yield 'b'; }
function* sgen() { try { yield 1; yield 2; yield 3; } finally { console.log('closed'); } }
class Counter {
  n = 0;
  [Symbol.iterator]() { return this; }
  next(): { value: number, done: boolean } { this.n++; return { value: this.n, done: this.n > 2 }; }
}
async function run() {
  const a: any = gen();
  for await (const c of a) console.log('async', c);
  const b: any = sgen();
  for (const c of b) { console.log('sync', c); if (c === 2) break; }
  const arr: any = [5, 6];
  for (const c of arr) console.log('arr', c);
  for await (const c of arr) console.log('arrawait', c);
  const ctr: any = new Counter();
  for (const c of ctr) console.log('ctr', c);
  const five: any = 5;
  try { for (const c of five) console.log(c); } catch (e) { console.log((e as Error).message); }
}
run();
`, "async a\nasync b\nsync 1\nsync 2\nclosed\narr 5\narr 6\narrawait 5\narrawait 6\nctr 1\nctr 2\nfive is not iterable\n")
}

// A next() result that is not an object throws instead of reading its
// done as undefined: every protocol walk over an any (for-of, spread,
// Array.from, new Set, AggregateError's errors) stops (ADR-01995).
func TestE2EAnyIteratorResultNotObject(t *testing.T) {
	assertSameAsNodeCompatJS(t, `
function mk(r) { return { [Symbol.iterator]() { return { next() { return r; } }; } }; }
const runs = {
  forof: (it) => { for (const x of it) {} },
  spread: (it) => [...it],
  from: (it) => Array.from(it),
  set: (it) => new Set(it),
  agg: (it) => new AggregateError(it),
};
for (const r of ['a string', 7, undefined, null, true]) {
  for (const k of Object.keys(runs)) {
    try { runs[k](mk(r)); console.log(k, 'no throw'); } catch (e) { console.log(k, e.constructor.name, e.message); }
  }
}
`)
}

// Array.from(iterable, mapFn) calls mapFn at each step: a throw ends an
// endless iterator and closes it; indexes and values follow the walk.
func TestE2EArrayFromMapFnPerStep(t *testing.T) {
	assertSameAsNodeCompatJS(t, `
let closed = 0, nexts = 0;
const endless = { [Symbol.iterator]() { return {
  next() { nexts++; return { value: nexts, done: false }; },
  return() { closed++; return {}; } }; } };
try { Array.from(endless, (v, i) => { if (i === 2) throw new Error('stop at ' + v); return v; }); }
catch (e) { console.log(e.message, 'closed', closed, 'nexts', nexts); }
function* g() { yield 'a'; yield 'b'; yield 'c'; }
console.log(Array.from(g(), (v, i) => v + i));
const order = [];
const two = { [Symbol.iterator]() { let n = 0; return { next() { order.push('next'); return n < 2 ? { value: n++, done: false } : { done: true }; } }; } };
console.log(Array.from(two, (v) => { order.push('map ' + v); return v * 10; }), order.join(','));
console.log(Array.from({ length: 3, 0: 'x', 1: 'y' }, (v, i) => String(v) + i));
console.log(Array.from([1, 2, 3], (v) => v * 2));
`)
}

// Array.from/of/fromAsync called with another constructor as `this` build
// `new C()`, each element defined on it, and throw when it refuses one.
func TestE2EArrayStaticsWithConstructorThis(t *testing.T) {
	assertSameAsNodeCompatJS(t, `
function C() { this.tag = "C"; }
var r = Array.from.call(C, [1, 2]);
console.log(r instanceof C, r.length, r[0], r.tag);
var ra = Array.from.call(C, { length: 2, 0: "a", 1: "b" }, function (v, i) { return v + i; });
console.log(ra instanceof C, ra.length, ra[1]);
var r2 = Array.of.apply(C, [5, 6]);
console.log(r2 instanceof C, r2.length, r2[1]);
var r3 = Array.from.call(undefined, [7]);
console.log(Array.isArray(r3), r3[0]);
function M() { Object.defineProperty(this, 0, { value: 0, writable: true, configurable: false }); }
try { Array.from.call(M, [1]); } catch (e) { console.log(e.constructor.name); }
try { Array.from({ length: 4294967296 }); } catch (e) { console.log(e.constructor.name, e.message); }
try { Array.from({ length: Infinity, 0: 1 }); } catch (e) { console.log(e.constructor.name, e.message); }
(async function () {
  try { await Array.fromAsync({ length: 4294967296 }); } catch (e) { console.log(e.constructor.name, e.message); }
  var x = await Array.fromAsync.call(C, [1, 2]);
  console.log(x instanceof C, x.length, x.tag);
  var closed = false;
  var it = { next() { return Promise.resolve({ value: 1, done: false }); },
    return() { closed = true; return Promise.resolve({ done: true }); },
    [Symbol.asyncIterator]() { return this; } };
  try { await Array.fromAsync.call(M, it); } catch (e) { console.log(e.constructor.name, closed); }
})();
`)
}

// an unannotated function returning its for...of binding infers the element type.
func TestE2EReturnInferenceSeesForOfBinding(t *testing.T) {
	assertOutput(t, `
function firstLong(words: string[]) {
  for (const w of words) { if (w.length > 3) return w; }
  return "none";
}
function firstBig(xs: number[]) {
  for (const x of xs) { if (x > 10) return x; }
  return -1;
}
console.log(firstLong(["a", "bbbb"]), firstBig([1, 20]), firstBig([]));
`, "bbbb 20 -1\n")
}

// an async generator function expression passed and called as a value.
func TestE2EGeneratorExpressionAsValue(t *testing.T) {
	assertOutput(t, `
async function run() {
  const g: any = async function* (src: any) { for await (const v of src) { yield v + v; } };
  for await (const v of g(['a', 'b'])) console.log('gen', v);
  const take = async (f: any) => { for await (const v of f(3)) console.log('n', v); };
  await take(async function* (n: any) { for (let i = 0; i < n; i++) yield i; });
}
run();
`, "gen aa\ngen bb\nn 0\nn 1\nn 2\n")
}

// A collection method replaced on its prototype is what instances call and
// read, and what the constructor adds with — read once, before iterating —
// so an adder that throws stops an endless iterable and closes it.
func TestE2ECollectionPrototypeMethodReplaced(t *testing.T) {
	assertSameAsNodeCompatJS(t, `
var origSet = Map.prototype.set, calls = 0;
Map.prototype.set = function (k, v) { calls++; return origSet.call(this, k, v); };
var two = {}; two[Symbol.iterator] = function () { var n = 0; return { next: function () { return n < 2 ? { value: [n, n++], done: false } : { done: true }; } }; };
var m = new Map(two); console.log('map iter', calls, m.size); calls = 0;
new Map([[1, 2], [3, 4]]); console.log('map arr', calls);
console.log(m.set === Map.prototype.set); m.set(9, 9); console.log('call', calls, m.get(9));
var origAdd = Set.prototype.add, adds = 0;
Set.prototype.add = function (v) { adds++; return origAdd.call(this, v); };
console.log('set', new Set(two).size, adds);
Map.prototype.set = function () { throw new Error('boom'); };
var closed = 0, endless = {};
endless[Symbol.iterator] = function () { return { next: function () { return { value: [], done: false }; }, return: function () { closed++; return {}; } }; };
try { new Map(endless); } catch (e) { console.log(e.message, closed); }
Map.prototype.set = 5;
try { new Map([]); } catch (e) { console.log(e.constructor.name, e.message); }
console.log(new Map().size);
`)
}
