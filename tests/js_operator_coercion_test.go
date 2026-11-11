package tests

import "testing"

func TestE2EJSOperatorCoercionsAcrossTypes(t *testing.T) {
	// Operators JavaScript applies across types this compiler stores
	// differently: a symbol is never loosely equal to another primitive,
	// arrays compare and combine through their primitives, a bigint target
	// mixed with a number throws, `++` on a string, object or array binding
	// stores a number, and different types are never strictly equal.
	assertSameAsNodeCompatJS(t, `
const s = Symbol("a");
console.log(s == 1, 1 != s, s == s);
const a = [1], b = [2];
console.log(a < b, a + b, a == b, a - b, a < null, a > null, a == null);
let big = 1n;
try { big += 1; } catch (e) { console.log(e.message); }
console.log(big);
let str = "5"; str++; let obj = {}; obj++; let arr = [2]; arr--;
console.log(str, obj, arr);
const one = "1";
console.log(a === one, a !== one, a & 1, a | 0, a << 1, a >>> 0, a ^ 3);
`)
}

func TestE2EJSOverridesOfAnotherType(t *testing.T) {
	// An override returning (or taking) a different type from the method or
	// accessor it overrides, called through either class.
	assertSameAsNodeCompatJS(t, `
class P { m(a) { return a + 1 } n() { return 1 } get v() { return 1 } }
class Q extends P { m(a) { return a + "!" } n() { return "s" } get v() { return "q" } }
for (const p of [new P(), new Q()]) console.log(p.m(1), p.n(), p.v);
class A { f(x) { return x * 2 } g() { return [1] } }
class B extends A { f(x) { return x.toUpperCase() } }
class C extends B { g() { return "c" } }
console.log(new A().f(2), new B().f("q"), new C().f("z"), new A().g(), new C().g());
for (const x of [new A(), new C()]) console.log(typeof x.g());
`)
}

func TestE2EGeneratorOverridingAbstractMethod(t *testing.T) {
	// A generator method implementing an abstract one typed Generator<T>,
	// and one overriding a generator, called through the base type.
	assertOutput(t, `
abstract class Z { abstract g(): Generator<number> }
class Y extends Z { *g() { yield 1; yield 2 } }
const z: Z = new Y();
console.log([...z.g()]);
for (const v of z.g()) console.log(v);
const it = z.g(); console.log(it.next().value, it.next().value, it.next().done);
class W { *g(): Generator<number> { yield 0 } }
class V extends W { *g() { yield 9 } }
const ws: W[] = [new W(), new V()]; for (const w of ws) console.log([...w.g()]);
`, "[ 1, 2 ]\n1\n2\n1 2 true\n[ 0 ]\n[ 9 ]")
}

func TestE2EHostObjectIntegrityLevels(t *testing.T) {
	assertOutput(t, `
const m = new Map([[1, 2]])
console.log(Object.isFrozen(m), Object.isExtensible(m), Object.isSealed(m))
Object.seal(m)
console.log(Object.isSealed(m), Object.isFrozen(m), Object.isExtensible(m))
m.set(3, 4)
console.log(m.size)
const d = Object.freeze(new Date(0))
console.log(Object.isFrozen(d), d.getTime())
const s = new Set([1]); Object.preventExtensions(s)
console.log(Object.isExtensible(s), Object.freeze(5), Object.seal("s"))
`, "false true false\ntrue true false\n2\ntrue 0\nfalse 5 s")
}

func TestE2EReflectMetadataOnAnyTarget(t *testing.T) {
	assertOutput(t, `
let seen: any
function role(v: string) { return (t: any) => { seen = t; Reflect.defineMetadata("role", v, t) } }
@role("admin")
@Reflect.metadata("tag", "c")
class C {
  @Reflect.metadata("t", 1) m() {}
}
console.log(seen === C, Reflect.getMetadata("role", C), Reflect.getMetadata("tag", C))
console.log(Reflect.getMetadata("t", C.prototype, "m"), Reflect.getMetadata("t", C, "m"))
const o: { a: number } = { a: 1 }
function f() {}
Reflect.defineMetadata("k", 1, o)
Reflect.defineMetadata("k", [2], f)
Reflect.defineMetadata("k", "u", o, undefined)
console.log(Reflect.getMetadata("k", o), Reflect.getMetadata("k", f), Reflect.hasMetadata("k", { a: 1 }))
try { Reflect.getMetadata("k", 5 as any) } catch (e) { console.log((e as Error).name) }
`, "true admin c\n1 undefined\nu [ 2 ] false\nTypeError")
}

func TestE2EProxyAndConstructArgumentsJS(t *testing.T) {
	assertSameAsNodeCompatJS(t, `
try { new Proxy({}) } catch (e) { console.log(e.constructor.name, e.message) }
try { new Proxy({}, 5) } catch (e) { console.log(e.message) }
const p = new Proxy({ a: 1 }, { get: (t, k) => k + "!" }, 3); console.log(p.a)
try { Reflect.construct() } catch (e) { console.log(e.message) }
try { Reflect.construct(5, []) } catch (e) { console.log(e.message) }
console.log(Reflect.construct(Map, [[[1, 2]]]).size)
`)
}

func TestE2EStreamReasonsAndUnderlyingObjectsJS(t *testing.T) {
	// A stream keeps a reason as given; an underlying object of any shape
	// is read when it runs, and a primitive one is ERR_INVALID_ARG_TYPE.
	assertSameAsNodeCompatJS(t, `
const rs = new ReadableStream({ cancel(r) { console.log("cancel", r, typeof r) } }); rs.cancel("why").then(() => console.log("done"))
const rs3 = new ReadableStream({ start(c) { c.error(42) } }); rs3.getReader().read().catch((e) => console.log("caught", e, typeof e))
const w = new WritableStream({ write(c) { console.log("w", c) }, abort(r) { console.log("abort", r) } })
const wr = w.getWriter(); wr.write().then(() => wr.write(1, 2)).then(() => wr.abort("stop")).then(() => console.log("aborted"))
for (const [C, a] of [[ReadableStream, 5], [ReadableStream, null], [WritableStream, true], [TransformStream, "s"]]) {
  try { new C(a); console.log(C.name, "ok") } catch (e) { console.log(C.name, e.code, e.message) }
}
const s = { ...{ write(c) { console.log("spread", c) } } }; new WritableStream(s).getWriter().write(7)
console.log(new ReadableStream(undefined).locked)
`)
}

func TestE2EWeakFinalizationTypedArrayArgumentsJS(t *testing.T) {
	// Lookups of a non-object key miss, stores throw; a cleanup callback of
	// any shape is checked callable when constructed; TypedArray stores and
	// constructions mixing bigint and number throw V8's TypeErrors.
	assertSameAsNodeCompatJS(t, `
{
const m = new WeakMap(); const k = {}; console.log(m.set(k), m.get(k), m.get(), m.has(), m.delete(), m.set(k, 1, 2) === m, m.get(k, 9), m.has(k, 1))
}
{
const s = new WeakSet(); const k = {}; try { s.add() } catch (e) { console.log(e.constructor.name, e.message) } console.log(s.add(k, 1) === s, s.has(k), s.delete(k, 2), s.has())
}
{
const m = new WeakMap(); try { m.set(5, 1) } catch (e) { console.log(e.constructor.name, e.message) } console.log(m.get(5), m.has("s"), m.delete(1))
}
{
try { new FinalizationRegistry() } catch (e) { console.log(e.constructor.name, e.message) }
try { new FinalizationRegistry(5) } catch (e) { console.log(e.message) }
const r = new FinalizationRegistry((h, x) => { console.log("held", h, x) }, 1); console.log(typeof r)
const cb = (h) => console.log("cb", h); const any = [cb][0]; const r2 = new FinalizationRegistry(any); console.log(typeof r2)
}
{
const r = new FinalizationRegistry(() => {}); const t = {};
try { r.register(5) } catch (e) { console.log(e.constructor.name, e.message) }
try { r.unregister() } catch (e) { console.log(e.message) }
try { r.unregister(3, 4) } catch (e) { console.log(e.message) }
console.log(r.unregister(t, 1), r.register({}, [1, 2]))
try { r.cleanupSome(console.log("arg")) } catch (e) { console.log(e.constructor.name, e.message) }
}
{
try{new Uint8Array(2).set()}catch(e){console.log(e.constructor.name,e.message)}
try{new Uint8Array(2).set(new BigInt64Array(1))}catch(e){console.log(e.message)}
try{new Uint8Array(2).set([1n])}catch(e){console.log(e.message)}
try{new BigInt64Array(2).set(new Uint8Array(1))}catch(e){console.log(e.message)}
const b=new ArrayBuffer(8,{maxByteLength:16}); b.resize(); console.log(b.byteLength)
try{new SharedArrayBuffer(8,{maxByteLength:16}).grow()}catch(e){console.log(e.constructor.name, e.message)}
const t = new Uint8Array(4); t.set([1], 1, 9); console.log(t)
}
{
try { new BigInt64Array([1]) } catch (e) { console.log(e.constructor.name, e.message) }
try { new Float64Array(new BigInt64Array(1)) } catch (e) { console.log(e.message) }
try { new BigInt64Array(new Uint8Array(1)) } catch (e) { console.log(e.message) }
try { new Float64Array([1n]) } catch (e) { console.log(e.message) }
console.log(new BigInt64Array([true, 2n]), new BigUint64Array([1n, 3n]))
const src = [1n, 2n]; console.log(new BigInt64Array(src))
try { const ns = [1, 2]; new BigInt64Array(ns) } catch (e) { console.log(e.message) }
}
{
const t = new Uint8Array([1, ...[2, 3]]); console.log(t); const b = new BigInt64Array([1n, 2n]); console.log(b.sort((x, y) => Number(y - x)))
}
`)
}

func TestE2EPrototypesAndAccessorLiterals(t *testing.T) {
	// A primitive's prototype is its wrapper class's; Object.create takes a
	// static object; a getter and setter may differ in type; an accessor
	// literal may hold a spread.
	assertSameAsNode(t, `
console.log(Object.getPrototypeOf(5) === Number.prototype, Object.getPrototypeOf(true) === Boolean.prototype, Object.getPrototypeOf(10n) === BigInt.prototype, Object.getPrototypeOf(Symbol()) === Symbol.prototype)
const base = { greet() { return "hi" }, n: 1 };
const c: any = Object.create(base); console.log(c.greet(), c.n, Object.getPrototypeOf(c) === base);
const sp: any = { ...("ab" as any), z: 1 }; console.log(sp);
let store = 0;
const o = { get a(): number { return store }, set a(v: string) { store = v.length } };
o.a = "hello"; console.log(o.a, typeof o.a);
const p = { get b(): string | number { return "x" }, set b(v: number) { console.log("setb", v) } }; p.b = 3; console.log(p.b);
const extra = { b: 2, c: 3 };
const q = { get a() { return 1 }, set a(v: number) { console.log("set", v) }, ...extra, d: 4 };
console.log(q.a, q.b, q.d, Object.keys(q)); q.a = 9;
`)
}

func TestE2EBindUnionArraysAndConsoleForms(t *testing.T) {
	// A rest-parameter function binds through its function object; a
	// union-held array literal is shared by its narrowed reads; a tuple
	// of mixed function types holds each; console spreads any elements and
	// dir takes options of any shape; a prototype constructor takes arrays
	// and optional parameters.
	assertSameAsNode(t, `
function cnt(this: any, ...xs: number[]) { return xs.length }
console.log(cnt.bind(null, 1, 2)(3), cnt.bind(null)(4, 5))
function add(a: number, b: number) { return a + b }
const b3 = (add as any).bind(null, 1, 2, 3); console.log(b3(), b3.length, b3.name)
const u: number[] | string = Math.random() > 2 ? "x" : [1]; if (Array.isArray(u)) { u.push(3) } console.log(u)
const f = (x: number) => x * 2; const g = (s: string) => s + "!";
const t = [f, g] as const; console.log(t[0](3), t[1]("x"))
console.log(...[[1, 2], [3]], ...[{ p: 1 }])
console.dir(); const d = 1; console.dir({ a: { b: { c: 1 } } }, { depth: d }); console.dir({ a: 1 }, { showHidden: false })
function P(this: any, xs: number[], n?: number) { this.xs = xs; this.n = n; }
const p = new (P as any)([1, 2, 3]); console.log(p.xs, p.n)
const s = Symbol("x"); const wm = new WeakMap<symbol, number>([[s, 5]]); console.log(wm.get(s))
`)
	assertSameAsNodeCompatJS(t, `
const o = { status: 201, statusText: "C", headers: { a: "1" } }; const r = new Response("x", o); console.log(r.status, r.statusText, r.headers.get("a"))
function mk(i) { return new Response("y", i) } console.log(mk(undefined).status, mk({ status: 404 }).status)
try { new Response("x", 5) } catch (e) { console.log(e.constructor.name, e.message) }
try { Response.json() } catch (e) { console.log(e.constructor.name, e.message) }
console.log(Response.error(1).type, new Response("a", {}, 3).status)
console.dir({ a: { b: { c: 1 } } }, { depth: 0 })
`)
}

func TestE2EQueuingStrategiesAndReaderOptionsJS(t *testing.T) {
	// The queuing strategy classes are values; a strategy held in a
	// binding is read at run time; a size of any result is its number;
	// getReader checks its options before locking the stream.
	assertSameAsNodeCompatJS(t, `
const s = new CountQueuingStrategy({ highWaterMark: 3 }); console.log(s.highWaterMark, s.size(1), Object.keys(s))
const b = new ByteLengthQueuingStrategy({ highWaterMark: "7" }); console.log(b.highWaterMark, b.size(new Uint8Array(5)))
try { new CountQueuingStrategy() } catch (e) { console.log(e.code, e.message) }
try { new CountQueuingStrategy({}) } catch (e) { console.log(e.code, e.message) }
const rs = new ReadableStream({ start(c) { c.enqueue(1); console.log(c.desiredSize); c.close() } }, s)
const strat = { highWaterMark: 2, size(c) { return c.length } }
const rs1 = new ReadableStream({ start(c) { c.enqueue("abc"); console.log(c.desiredSize); c.close() } }, strat)
new ReadableStream({ start(c) { c.enqueue("x"); console.log(c.desiredSize) } }, { size() { return "5" } })
const rs2 = new ReadableStream()
for (const o of [{ mode: "byob" }, { mode: "x" }, 5, null, { mode: null }, {}]) { try { const r = rs2.getReader(o); console.log("ok"); r.releaseLock() } catch (e) { console.log(e.code, e.message) } }
rs.getReader().read().then(v => console.log(v))
`)
}

func TestE2EStreamCallbackThrowsReject(t *testing.T) {
	// A sink's or source's method that throws rejects as Node's async
	// wrapper of it does, a tick ahead of one returning a promise.
	assertSameAsNode(t, `
const a = new WritableStream<number>({ write: async (c) => { throw new Error("A"); } });
a.getWriter().write(1).catch(e => console.log("a", e?.message));
const c = new WritableStream<number>({ write(c) { throw new Error("C"); } });
c.getWriter().write(1).catch(e => console.log("c", e?.message));
const d = new WritableStream<number>({ write(c) { } });
d.getWriter().write(1).then(() => console.log("d ok"));
const f = new WritableStream<number>({ write: async (c) => { } });
f.getWriter().write(1).then(() => console.log("f ok"));
const cl = new WritableStream<number>({ close() { throw new Error("CL"); } });
cl.getWriter().close().catch(e => console.log("close", e?.message));
const ab = new WritableStream<number>({ abort(r) { throw new Error("AB"); } });
ab.getWriter().abort("x").catch(e => console.log("abort", e?.message));
const r = new ReadableStream<number>({ pull(ctl) { throw new Error("P"); } });
r.getReader().read().catch(e => console.log("pull", e?.message));
const k = new ReadableStream<number>({ cancel(r) { throw new Error("K"); } });
k.cancel("why").catch(e => console.log("cancel", e?.message));
`)
}

func TestE2EOwnPropertyDescriptorsAndPrimitives(t *testing.T) {
	assertSameAsNode(t, `
const s = Symbol("s");
const o = { a: 1, [s]: 2, get g() { return 3 } };
console.log(Object.getOwnPropertyDescriptors(o));
console.log(Object.getOwnPropertyDescriptors([1]));
const an: any = { z: "q", get h() { return 1 } };
console.log(Object.getOwnPropertyDescriptors(an), Object.getOwnPropertyDescriptor(an, "h"));
console.log(Object.getOwnPropertyDescriptors("ab" as any));
class K { x = 1; m() {} }
console.log(Object.getOwnPropertyDescriptors(new K()));
try { Object.getOwnPropertyDescriptors(undefined as any) } catch (e) { console.log((e as Error).message) }
console.log(Object.defineProperties({}, Object.getOwnPropertyDescriptors({ v: 5 })));
const str: any = "ab", n: any = 5;
console.log(Object.getOwnPropertyNames(str), Object.getOwnPropertyNames("ab"), Object.getOwnPropertyNames(n), Object.getOwnPropertyNames(true));
console.log(Object.getOwnPropertyDescriptor(str, "0"), Object.getOwnPropertyDescriptor("ab", "length"), Object.getOwnPropertyDescriptor(5, "x"));
console.log(Object.getOwnPropertyDescriptor("héllo", "1"), Object.getOwnPropertyDescriptor(str, "01"), Object.getOwnPropertyDescriptor(str, "9"));
try { Object.getOwnPropertyDescriptor(null as any, "x") } catch (e) { console.log((e as Error).message) }
`)
}

func TestE2ERequestInitCoercionsJS(t *testing.T) {
	// A Request init's members are converted as WebIDL converts them: the
	// method by ToString, a primitive init or a non-AbortSignal signal a
	// TypeError; JSON.parse takes any value's ToString.
	assertSameAsNodeCLIJS(t, `
const r = new Request("http://x.test", { method: 5, mode: "cors", cache: undefined });
console.log(r.method, r.mode, r.cache);
try { new Request("http://x.test", 7) } catch (e) { console.log(e.name, e.message) }
for (const sig of ["s", false, { a: 1 }]) {
  try { new Request("http://x.test", { signal: sig }) } catch (e) { console.log(e.name, e.message) }
}
console.log(new Request("http://x.test", null).method, new Request("http://x.test", { signal: null }).signal.aborted);
console.log(JSON.parse(12), JSON.parse(true));
`)
}

func TestE2EBuiltinCalledWithoutNew(t *testing.T) {
	// RegExp and the Error constructors construct when called as functions;
	// RegExp(re) is re itself; Date() is the current time's string. A Date
	// setter's missing argument, NaN or infinity makes an Invalid Date.
	assertSameAsNode(t, `
console.log(RegExp("a", "g").flags, Error("e").message, TypeError("t") instanceof TypeError);
const r = /x/g; console.log(RegExp(r) === r, RegExp(r, "i") === r, RegExp(r, "i").flags);
const s = Date(); console.log(typeof s, s === new Date().toString());
const d = new Date(0); d.setUTCHours(1.9, Infinity); console.log(d.getTime());
const f = new Date(0); f.setUTCHours(NaN); console.log(f.getTime(), f.setUTCMinutes(3));
const g = new Date(0); g.setUTCHours(2, 30); console.log(g.toISOString());
const u: any = Promise.reject(5); u.catch(() => {});
`)
}

func TestE2EUnhandledRejectionNonError(t *testing.T) {
	// A non-Error reason reaches 'uncaughtException' as Node's
	// UnhandledPromiseRejection Error.
	assertSameAsNode(t, `
process.on("uncaughtException", (e: any) => console.log(e.name, "|", e.message.split("reason ")[1], "|", e.code, e instanceof Error));
class Foo {}
for (const r of [5, { a: 1 }, [1, 2], undefined, null, Symbol("q"), 10n, new Foo(), "s"] as any[]) Promise.reject(r);
Promise.reject(new TypeError("real"));
`)
}

func TestE2EPolymorphicRecursionAndNestedArraysAsAny(t *testing.T) {
	// A generic function calling itself at an ever deeper type argument, and
	// a static array nested past two levels read through any.
	assertSameAsNode(t, `
function deep<T>(x: T, n: number): number { return n <= 0 ? 0 : deep([x], n - 1) + 1 }
function wrap<T>(x: T, n: number): string { return n <= 0 ? JSON.stringify(x) : wrap([x], n - 1) }
console.log(deep("s", 20), wrap(1, 12));
const b3: number[][][] = [[[1, 2]], [[3]]]; const c3: any = b3;
console.log(JSON.stringify(c3), c3, c3[1][0][0], c3.length);
c3[0][0][1] = 9; console.log(b3[0][0][1]);
class P { x = 1 } const pp: P[][] = [[new P()]]; const pa: any = pp; console.log(JSON.stringify(pa), pa);
const s4: string[][][][] = [[[["q"]]]]; const a4: any = s4; console.log(JSON.stringify(a4), a4[0][0][0][0]);
`)
}

func TestE2EPromiseCombinatorNonIterableRejects(t *testing.T) {
	// A combinator's iterable is read inside it: a value without an
	// iterator rejects the promise it returns, as GetIterator's TypeError.
	assertSameAsNode(t, `
const x: any = undefined; Promise.all(x).catch((e: any) => console.log("a", e.message));
const P: any = Promise; P.race().catch((e: any) => console.log("b", e.message));
Promise.allSettled(5 as any).catch((e: any) => console.log("c", e.message));
class MP<T> extends Promise<T> {}
MP.all(undefined as any).catch((e: any) => console.log("d", e.message));
(MP as any).any().catch((e: any) => console.log("e", e.message));
function* g() { yield 1; yield Promise.resolve(2) }
Promise.all(g()).then(v => console.log("g", v));
Promise.all(new Set([3, Promise.resolve(4)])).then(v => console.log("s", v));
Promise.allSettled("ab").then(v => console.log("str", v));
const it: any = [1]; Promise.allSettled(it).then((v: any) => console.log("any", v));
console.log("sync");
`)
}

func TestE2EStreamDynamicUnderlyingTicksJS(t *testing.T) {
	// An underlying object read at run time: its methods run through Node's
	// async wrapper (a plain return settles now, a promise is adopted), its
	// start through `new Promise((r) => r(result))` (a thenable's then is a
	// job). The tick counter makes every ordering observable.
	assertSameAsNodeCompatJS(t, `
let t = 0; const tick = () => { t++; if (t < 30) queueMicrotask(tick) }; queueMicrotask(tick)
const s = { write(c) { console.log("w", c, t) } }
new WritableStream(s).getWriter().write(1).then(() => console.log("w done", t))
const a = { async start() { console.log("as", t) }, async write(c) { console.log("aw", c, t) }, close() { console.log("ac", t) } }
const wa = new WritableStream(a).getWriter(); wa.write(2).then(() => console.log("aw done", t)); wa.close().then(() => console.log("closed", t))
const th = { start() { return { then(r) { console.log("then called", t); r() } } }, write(c) { console.log("tw", c, t) } }
new WritableStream(th).getWriter().write(3).then(() => console.log("tw done", t))
const bad = { write() { throw new Error("boom") } }
new WritableStream(bad).getWriter().write(4).catch((e) => console.log("bad", e.message, t))
const src = { n: 0, pull(c) { c.enqueue(this.n++); if (this.n > 1) c.close() } }
const r = new ReadableStream(src).getReader(); r.read().then(v => console.log("r", v, t)); r.read().then(v => console.log("r", v, t)); r.read().then(v => console.log("r", v, t))
`)
}

func TestE2EArrayCallbacksWithoutResults(t *testing.T) {
	// A map or reduce callback that returns nothing, or not on every path,
	// gives undefined there. A reduce one is JavaScript only: tsc fixes the
	// accumulator to the initial value's type, which a possibly undefined
	// result does not fit (TS2769).
	assertSameAsNode(t, `
const a = [1, 2].map(() => {}); console.log(a, a.length, typeof a[0]);
const b = [1, 2].map((x) => { console.log(x) }); console.log(b);
`)
	assertSameAsNodeCompatJS(t, `
console.log([1, 2].reduce((a, x) => { if (x > 5) return a + x }, 0));
console.log([1, 9].reduce((a, x) => { if (x > 5) return a + x }, 3), [9, 9].reduce((a, x) => { if (x > 5) return a + x }, 3));
console.log(Array.from({ length: "2" }), Array.from({ length: "x" }), Array.from({ length: 2.7 }))
console.log(Array.from(5), Array.from(true), Array.from({}), Array.from(function f(a, b) {}))
try { Array.from(null) } catch (e) { console.log(e.message) }
console.log([1, 2, 3].reduce((a, x) => { a + x }), [1, 2].reduce((a, x) => a + "" + x, 0))
console.log(new Uint8Array([1, 2]).map(() => {}), typeof ((a) => 1), Object.keys(function (a) {}))
`)
}

func TestE2EFromEntriesSourcesAndTupleCollections(t *testing.T) {
	// Object.fromEntries over a Map, a generator, a Set of pairs and any[]
	// rows keeps each value's type; a Set of tuples iterates, spreads and
	// seeds a Map; a nullish argument throws V8's short message.
	assertSameAsNode(t, `
console.log(Object.fromEntries(new Map([["b", 2.5]])), Object.fromEntries(new Map([["c", "s"]])))
function* g(): Generator<[string, number]> { yield ["x", 1.5]; yield ["y", 2] }
console.log(Object.fromEntries(g()))
const xs: any[] = [["a", 1.5], ["b", "q"]]; console.log(Object.fromEntries(xs))
const st = new Set<[string, number]>(); st.add(["c", 3]); st.add(["d", 4.5])
for (const v of st) console.log(v[0], v[1] + 1)
console.log(st, [...st], new Map(st), Object.fromEntries(st))
console.log(Object.fromEntries(new Set([["s", 1]] as [string, number][])))
const s2: [string, number][] = [["a", 1]]; const sp = [...s2, ...s2]; console.log(sp, sp[1][1] + 1)
let u: any; try { Object.fromEntries(u) } catch (e) { console.log((e as Error).message) }
`)
}

func TestE2EReplacerResultsAndBigIntRadix(t *testing.T) {
	// A replace callback's result is ToString'd (undefined included); a
	// BigInt's toString radix is ToIntegerOrInfinity'd and range-checked;
	// BigInt() of an object goes through ToPrimitive; a timer given a
	// non-function throws Node's validateFunction TypeError.
	assertSameAsNodeCompatJS(t, `
console.log("abc".replace(/b/, () => undefined), "abc".replace("b", () => { if (0) return "x" }), "abcb".replaceAll("b", () => { }))
console.log("abc".replace(/b/g, () => null), "abc".replace("b", () => 5), "aXa".replaceAll("a", (m, i) => i))
console.log((255n).toString("16"), (255n).toString(2.9), (255n).toString(undefined), (255n).toString(16, 1))
for (const r of [1, NaN, 37, 5n]) try { (5n).toString(r) } catch (e) { console.log(e.name, e.message) }
console.log(BigInt({ valueOf() { return 3 } }), BigInt([7]), BigInt([]), BigInt(["0x10"]))
try { BigInt([1, 2]) } catch (e) { console.log(e.name, e.message) }
for (const f of [() => setTimeout(5), () => setInterval("x"), () => setImmediate(null)]) try { f() } catch (e) { console.log(e.code, e.message) }
const a = [1, 2]; console.log(Object.isFrozen(a), Object.isExtensible(a), Object.isSealed(1, 2))
`)
}

func TestE2EBuiltinArgumentCoercionsJS(t *testing.T) {
	// escape/unescape (Annex B); Symbol.for's ToString key; new Promise
	// with a non-function, or past its executor; Math.max/min over
	// spread objects; a function in a conditional's branch.
	assertSameAsNodeCompatJS(t, `
console.log(escape(), escape("a b+c/ä€\u0001~"), escape(12), unescape("%41%u20AC%zz%u12%4"), unescape(escape("Ωx y")))
console.log(Symbol.for().description, Symbol.for(5).description, Symbol.for(undefined) === Symbol.for(), Symbol.for(null).description)
try { Symbol.for(Symbol("x")) } catch (e) { console.log(e.name, e.message) }
for (const v of [5, "x", {}, [1, 2], null, Symbol("s"), true, 10n, undefined]) try { new Promise(v) } catch (e) { console.log(e.name, e.message) }
let m = 0; const f = (r) => { m++; r(m) }; new Promise(f, m++).then((v) => console.log("q", v, m))
console.log(Math.max(...[[1], [5]]), Math.min(...[{ valueOf() { return 2 } }, 3]), Math.max(...[[1, 2]]))
const ex = Math.random() < 2 ? (res) => res(7) : 0; new Promise(ex).then((v) => console.log("dyn", v))
const g = Math.random() > 2 ? 0 : function (a, b) { return a + b }; console.log(g(1, 2), g("a", "b"))
for (const k of ["getTime", "valueOf", "toISOString", "setTime", "setDate"]) try { Date.prototype[k].call({}) } catch (e) { console.log(k, e.message) }
`)
}

func TestE2ETypedDescriptorsAndProxyReads(t *testing.T) {
	// Typed descriptors and handlers are read by key; a read through a
	// proxy is its trap's result, whatever the target declares.
	assertSameAsNode(t, `
class D { value = 3; enumerable = true }
const o2: any = {}; Object.defineProperty(o2, "y", new D()); console.log(o2.y, Object.keys(o2))
const m = new Map([[1, 2]]); Object.defineProperties(m, { tag: { value: "t", enumerable: true } }); console.log((m as any).tag)
const h: ProxyHandler<{ a: number }> = { get: (t, k) => "h:" + String(k) }
const p = new Proxy({ a: 1 }, h); console.log(p.a)
const q = new Proxy({ a: 1, b: "s" }, { get: (t, k) => k === "a" ? "q:" + String(k) : (t as any)[k] })
const v = q.a; console.log(typeof v, v, q.b.length, `+"`${q.a}`"+`)
function mk<T extends object>(t: T, hh: ProxyHandler<T>): T { return new Proxy(t, hh) }
const r = mk({ a: 1, b: 2 }, { get: (t, k) => k === "a" ? 10 : (t as any)[k], has: () => true })
console.log(r.a, r.b, "zz" in r)
for (const k of ["getTime", "valueOf", "toISOString", "setTime"]) try { (Date.prototype as any)[k].call({}) } catch (e) { console.log(k, (e as Error).message) }
`)
}

func TestE2EParsedValuesInClassAndUnionBindings(t *testing.T) {
	// A parse is a plain object: a class-typed or object-union binding
	// holds it as it is, as a class-typed binding of any box does.
	assertSameAsNode(t, `
class K { a = 1; m() { return 7 } }
const k: K = JSON.parse('{"a":5}'); console.log(k.a, k, k instanceof K)
const box: any = new K(); const k2: K = box; console.log(k2.a, k2.m(), k2 instanceof K)
const raw: any = { a: 9 }; const k3: K = raw; console.log(k3.a); try { k3.m() } catch (e) { console.log((e as Error).name) }
let k4: K = new K(); k4 = box; console.log(k4.m())
type U = { a: number } | { b: string }
const u: U = JSON.parse('{"a":1}'); console.log(u, "a" in u)
const w: U[] = JSON.parse('[{"b":"x"}]'); console.log(w)
`)
}

func TestE2EPromisesInCollections(t *testing.T) {
	// A promise held by a Set or Map comes back as itself, and the
	// combinators read a Set of promises as its promises.
	assertSameAsNode(t, `
const s: Set<Promise<number>> = new Set(); const p = Promise.resolve(5); s.add(p); console.log(s.size, s.has(p)); for (const x of s) console.log(x === p, typeof x)
const r = new Set([Promise.resolve(2.5)]); for (const x of r) x.then((v) => console.log("r", v))
const m = new Map([["k", Promise.resolve(4)]]); m.get("k")!.then((v) => console.log("m", v * 2))
Promise.all(new Set([Promise.resolve(3)])).then((v) => console.log("all", v))
Promise.race(new Set([Promise.resolve("r")])).then((v) => console.log("race", v))
Promise.all(new Map([[1, Promise.resolve(8)]]).values()).then((v) => console.log("vals", v))
function* g() { yield Promise.resolve(1); yield Promise.resolve(2) } Promise.all(g()).then((v) => console.log("gen", v))
`)
}

func TestE2EDynamicImportOfBuiltins(t *testing.T) {
	// import() of a builtin module is its namespace; a computed specifier
	// reaches the builtins the file names, else rejects as Node does.
	assertSameAsNode(t, `
const a = await import("os"); console.log("a", typeof a.platform)
const b = await import("node:path"); console.log("b", b.join("x", "y"))
const spec = "node:" + "os"; const c = await import(spec); console.log("c", typeof c.cpus, c === a)
const bad = "nope-" + "mod"; await import(bad).catch((e) => console.log("d", e.code))
const { readFileSync } = await import("node:fs"); console.log("e", typeof readFileSync)
const sp2 = { toString() { return "os" } }; const f = await import(sp2 as any); console.log("f", typeof f.arch)
`)
}

func TestE2ESortComparatorValuesJS(t *testing.T) {
	// A comparator held in a binding, a declaration or a bound function
	// sorts as an inline arrow does.
	assertSameAsNodeCompatJS(t, `
const anyc = (a, b) => a - b; console.log([5, 4, 7, 1].sort(anyc), [3, 1].sort((a, b) => a - b))
const byLen = (a, b) => a.length - b.length; console.log(["ccc", "a", "bb"].sort(byLen))
const byK = (a, b) => a.k - b.k; console.log([{ k: 2 }, { k: 1 }].sort(byK))
const fl = (a, b) => b - a; console.log([1.5, 2.5, 0.5].sort(fl), [3, 1, 2].toSorted(fl))
function cmp(a, b) { return b - a } const o = { c(a, b) { return a - b } }
console.log([3, 1, 2].sort(cmp), [3, 1, 2].sort(o.c), [3, 1, 2].sort(cmp.bind(null)))
let n = 0; const mk = () => { n++; return (a, b) => a < b ? 1 : -1 }; console.log(["a", "c", "b"].sort(mk()), n)
`)
}

func TestE2EPipeIntoUntypedSinks(t *testing.T) {
	// A typed stream piped into an untyped sink or transform delivers
	// each chunk as its value.
	assertSameAsNode(t, `
const rs = new ReadableStream<number>({ start(c) { c.enqueue(1); c.enqueue(2.5); c.close() } })
await rs.pipeTo(new WritableStream({ write(c) { console.log("w", c, typeof c) } }))
const r2 = new ReadableStream<string>({ start(c) { c.enqueue("s"); c.close() } })
await r2.pipeTo(new WritableStream({ write(c) { console.log("s", c) } }))
const r4 = new ReadableStream<number>({ start(c) { c.enqueue(3); c.close() } })
await r4.pipeTo(new WritableStream<number>({ write(c) { console.log("n", c + 1) } }))
const r5 = new ReadableStream<number>({ start(c) { c.enqueue(1.5); c.enqueue(2); c.close() } })
for await (const v of r5.pipeThrough(new TransformStream({ transform(c, ctl) { ctl.enqueue(c * 2) } }))) console.log("t", v)
`)
}

func TestE2EPipeOptionsValidationJS(t *testing.T) {
	// pipeTo's options: a primitive other than undefined or null rejects
	// with Node's ERR_INVALID_ARG_TYPE.
	assertSameAsNodeCompatJS(t, `
const mk = () => new ReadableStream({ start(c) { c.enqueue(1); c.close() } })
const sink = (t) => new WritableStream({ write(c) { console.log(t, c) } })
for (const o of [5, "s", null, true, undefined, {}, { preventClose: true }, () => 1]) await mk().pipeTo(sink(String(o)), o).then(() => console.log("ok"), (e) => console.log("rej", e.code, e.message))
`)
}

func TestE2EReduceBoxedResultsJS(t *testing.T) {
	// A step returning either kind widens the accumulator, with or without
	// an initial value.
	assertSameAsNodeCompatJS(t, `
console.log([1, 2].reduce((x, y) => `+"`${x}+${y}`"+`, 0), [1, 2, 3].reduce((x, y) => x > 1 ? "big" : x + y))
console.log([1, 2, 3].reduce((x, y) => x + y), ["a", "b"].reduce((a, b) => a + b), [1, 2].reduceRight((x, y) => x > 1 ? [x] : y))
console.log([3, 4].reduce((a, x) => x > 3 ? null : a, 1), [{ k: 1 }, { k: 2 }].reduce((a, o) => a + o.k, 0))
`)
}

func TestE2EArrayLiteralsInArrayOrTupleUnions(t *testing.T) {
	// An array literal against a union with a tuple member is the member
	// the checker types it as; such a union has a length.
	assertSameAsNode(t, `
const u: number[] | [string, number] = ["x", 1]; console.log(u, Array.isArray(u), u.length)
const w: number[] | [string, number] = [1, 2, 3]; console.log(w, w.length)
let z: string[] | [number, boolean] = [5, true]; console.log(z); z = ["q"]; console.log(z)
function f(a: number[] | [string, number]) { return a.length } console.log(f(["p", 2]), f([9]))
`)
}

func TestE2EClassBoundToObjectType(t *testing.T) {
	// A class bound to an object type is the class itself.
	assertSameAsNode(t, `
class K { static x = 1; static m() { return this.x * 2 } }
const c: { x: number; m(): number } = K; c.x = 5; console.log(K.x, c.x, c.m())
const d: { readonly name: string } = K; console.log(d.name)
`)
}

func TestE2ETextEncoderDecoderStreams(t *testing.T) {
	// TextEncoderStream and TextDecoderStream as Node builds them: a
	// surrogate pair split across chunks joins, a lone half is U+FFFD, and
	// a streaming decoder holds a split code unit.
	assertSameAsNode(t, `
async function collect<T>(r: ReadableStream<T>): Promise<T[]> { const out: T[] = []; const rd = r.getReader(); for (;;) { const { value, done } = await rd.read(); if (done) return out; out.push(value as T); } }
async function main() {
  const e = new TextEncoderStream(); console.log(e.encoding, typeof e.readable, typeof e.writable);
  const w = e.writable.getWriter(); w.write("a\uD83D"); w.write("\uDE00b"); w.write("\uD800"); w.close();
  console.log((await collect(e.readable)).map((u) => Array.from(u)));
  const d = new TextDecoderStream("utf-16le", { fatal: false }); console.log(d.encoding, d.fatal, d.ignoreBOM);
  const dw = d.writable.getWriter(); dw.write(new Uint8Array([0x68, 0x00, 0x69])); dw.write(new Uint8Array([0x00])); dw.close();
  console.log(await collect(d.readable));
  const s = new ReadableStream<string>({ start(c) { c.enqueue("héllo "); c.enqueue("wörld"); c.close(); } });
  console.log(await collect(s.pipeThrough(new TextEncoderStream()).pipeThrough(new TextDecoderStream())));
}
main();
`)
}

func TestE2EPipeThroughAnyTransformPair(t *testing.T) {
	// pipeThrough takes any { readable, writable } pair, reading both as
	// Node does, and controller.terminate() ends a transform.
	assertSameAsNode(t, `
async function collect<T>(r: ReadableStream<T>): Promise<T[]> { const out: T[] = []; const rd = r.getReader(); for (;;) { const { value, done } = await rd.read(); if (done) return out; out.push(value as T); } }
const src = (xs: string[]) => new ReadableStream<string>({ start(c) { for (const x of xs) c.enqueue(x); c.close(); } });
async function main() {
  const t = new TransformStream<string, string>({ transform(ch, c) { c.enqueue(ch.toUpperCase()); } });
  console.log(await collect(src(["a", "b"]).pipeThrough({ readable: t.readable, writable: t.writable })));
  class P { #t = new TransformStream<string, number>({ transform(ch, c) { c.enqueue(ch.length); } }); get readable() { return this.#t.readable; } get writable() { return this.#t.writable; } }
  console.log(await collect(src(["abc", "de"]).pipeThrough(new P())));
  try { src(["x"]).pipeThrough({ readable: 5, writable: t.writable } as any); } catch (e: any) { console.log(e.code, e.message); }
  try { src(["x"]).pipeThrough({ readable: new ReadableStream(), writable: {} } as any); } catch (e: any) { console.log(e.code, e.message); }
  const anyT: any = new TransformStream(); console.log(await collect(src(["z"]).pipeThrough(anyT)));
  const k = new TransformStream<number, number>({ transform(ch, c) { c.enqueue(ch * 2); if (ch === 2) c.terminate(); } });
  const w = k.writable.getWriter(); const r = k.readable.getReader();
  await w.write(1); console.log(await r.read());
  await w.write(2); console.log(await r.read(), await r.read());
  try { await w.write(3); } catch (e: any) { console.log(e.name, e.message, e.code); }
  const k2 = new TransformStream<string, string>({ flush(c) { c.enqueue("end"); c.terminate(); } });
  const w2 = k2.writable.getWriter(); const r2 = k2.readable.getReader();
  const cl = w2.close(); console.log(await r2.read(), await r2.read()); await cl; console.log("closed ok");
}
main();
`)
}

func TestE2ELoneSurrogateEscapes(t *testing.T) {
	// An escaped lone surrogate is that code unit, and an escaped pair is
	// its code point.
	assertSameAsNode(t, `
const s = "a\uD83D"; console.log(s.length, s.charCodeAt(1).toString(16)); const it = s[1]; console.log(it.charCodeAt(0).toString(16), (it + "\uDE00").codePointAt(0)!.toString(16))
console.log("\uD83D\uDE00" === "😀", "\u{D83D}\u{DE00}".length, "\uDE00\uD83D".length, Array.from(new TextEncoder().encode("x\uDE00")))
console.log(JSON.stringify("\uD800"), encodeURIComponent("\uD83D\uDE00"), `+"`t\\uD83D\\uDE00`"+`.codePointAt(1))
`)
}

func TestE2ENamedFunctionExpressionsAsValues(t *testing.T) {
	// A named function expression held as a value reads its own name.
	assertSameAsNode(t, `
function take(f: Function) { return f; }
console.log(take(function q() { return q; })() === take);
const h = take(function r(n: number): number { return n <= 1 ? 1 : n * r(n - 1); }); console.log(h(5), h.name);
const k: any = function s() { return typeof s + ":" + s.name; }; console.log(k());
const m: any = function t(t: number) { return t; }; console.log(m(3));
`)
	assertSameAsNodeCLIJS(t, `
const w = function v() { v = 1; return typeof v; }; console.log(w());
const z = function u() { "use strict"; try { u = 1; } catch (e) { return e.constructor.name; } return "no"; }; console.log(z());
function take(f) { return f; } console.log(take(function q() { return q; })() === take, take(function r(n) { return n <= 1 ? 1 : n * r(n - 1); })(5));
var h = function k() { k += 1; return typeof k; }; console.log(h());
`)
}

func TestE2EAssignmentThroughTypeAssertions(t *testing.T) {
	// `(x as T) = v` and `x! = v` assign x, as TypeScript has them.
	assertSameAsNode(t, `
const o: { p?: number } = {}; o.p! = 4; (o.p as number)++; console.log(o);
let n: number | undefined = 2; n!++; (n as number) += 3; console.log(n);
const a = [1, 2]; (a[0] as any) = 9; console.log(a);
`)
}

func TestE2EUntypedParamFromMixedCallSites(t *testing.T) {
	// A parameter some call passes a function with parameters to is any:
	// the first call's function type does not decide the rest.
	assertSameAsNodeCLIJS(t, `
function take(f) { return f; }
console.log(take(function () { return 1; })());
console.log(take(function (n) { return n; })(5));
console.log(take((a, b) => a + b)(2, 3));
`)
}

func TestE2EFillWithoutArgumentsJS(t *testing.T) {
	// fill() fills with undefined (an array) or 0 (a Buffer or typed array);
	// an any receiver's fill is its own method.
	assertSameAsNodeCLIJS(t, `
const ctx = { fill() { return "f"; }, stroke() { return 1; } }; const c2 = ctx;
console.log(c2.fill(), c2.stroke());
let a = [1, 2]; const aa = a; console.log(aa.fill(), [1, 2, 3].fill(7, 1, 2, 99));
console.log(Buffer.from([1, 2]).fill(), new Uint8Array([1, 2]).fill());
function dyn(x) { return x.fill(); } console.log(dyn([5, 6]), dyn(Buffer.from([3])), dyn({ fill: () => "obj" }));
`)
}

func TestE2EArrowAnnotatedTupleResult(t *testing.T) {
	// An arrow's annotated result is the context of its expression body,
	// and a tuple it declares is that tuple.
	assertSameAsNode(t, `
const a = (p: number[]): [string, number] => { return ["x", p[0]]; }; console.log(a([3]))
const b = (p: number[]): [string, number] => ["x", p[0]]; console.log(b([3]))
const w = (p: number[]): [string, ...number[]] => ["x", ...p]; console.log(w([3, 4]))
const f = async (): Promise<[string, number]> => ["a", 1]; f().then(console.log)
const g = (): { k: "a" | "b" } => ({ k: "a" }); console.log(g())
const h = (): (n: number) => string => (n) => String(n * 2); console.log(h()(4))
`)
}

func TestE2ECallableProxyAndCallTracker(t *testing.T) {
	// A Proxy of a function is a function: typeof, calls through its apply
	// trap (or to its target), call/apply, and forwarded length and name;
	// assert.CallTracker tracks calls with one, as Node's does.
	assertSameAsNodeCLIJS(t, `
function f(a, b) { return a + b; }
const p = new Proxy(f, { apply(t, th, args) { console.log("trap", args); return Reflect.apply(t, th, args) * 10; } });
console.log(typeof p, p.length, p.name, p(1, 2), p.call(null, 3, 4), p.apply(null, [5, 6]));
const q = new Proxy(f, {}); console.log(q(5, 6), typeof new Proxy({}, {}));
const pp = new Proxy(p, {}); console.log(pp(1, 1), typeof pp);
const assert = require("assert");
const tracker = new assert.CallTracker();
function func(a, b, c = 2) { return a + b; }
const cf = tracker.calls(func, 2);
console.log(cf.length, cf(1, 2), cf.name);
console.log(tracker.report().map((r) => [r.message, r.actual, r.expected, r.operator]));
cf(3, 4); tracker.verify(); console.log(tracker.getCalls(cf));
const n = tracker.calls(); n(); tracker.reset(); console.log(tracker.report().length);
try { tracker.verify(); } catch (e) { console.log(e.name, e.message, e.code); }
try { tracker.getCalls(func); } catch (e) { console.log(e.code, e.message); }
try { tracker.calls(func, 1.5); } catch (e) { console.log(e.code, e.message); }
`)
}

func TestE2EBlockFunctionShadowingTopLevel(t *testing.T) {
	// A block function named as a top-level one assigns that binding when
	// the block runs (Annex B.3.3).
	assertSameAsNodeCLIJS(t, `
function func() { return 1; }
console.log(func());
{
  function func() { return 2; }
  const g = func; console.log(g(), func.length);
}
{
  function func(a, b, c = 2) { return 3; }
  console.log(func.length, func());
}
console.log(func());
`)
}

func TestE2ESpreadArgumentToOverload(t *testing.T) {
	// A spread argument of `any` picks an overload as its elements fit it.
	assertSameAsNode(t, `
class K { t(a?: string, b?: string): boolean;
  t(a?: any, b?: any): boolean { return a === "x"; } }
const k = new K(); const xs: any = ["x"];
const r = k.t(...xs); console.log(r);
const f: () => void = () => { console.log(k.t(...xs)); }; f();
const pattern = new URLPattern("https://x/*"); const ys: any = ["https://x/a"];
const g = () => pattern.test(...ys); console.log(g());
`)
}

func TestE2EUndeclaredConstructorJS(t *testing.T) {
	// new of an undeclared name is a ReferenceError at run time, its
	// value's uses compiling as any's.
	assertSameAsNodeCLIJS(t, `
try {
  var c = new Nope(100, 50); var d = c.get(1);
  for (var i = 0, len = d.width * d.height * 4; i < len; i += 4) {}
} catch (e) { console.log(e.name, e.message); }
`)
}

func TestE2EUndeclaredCalleeAssertsNothingJS(t *testing.T) {
	// A call of an undeclared function is no assertion on its arguments:
	// a binding read after it keeps its type.
	assertSameAsNodeCLIJS(t, `
class Bi { constructor() { this.length = 0; } emit(x) { this.length += x; } }
class M { tb(debug = false) { let b = new Bi; if (debug) print("at " + b.length); b.emit(2); return b.length; } ta() { return [this.tb()]; } }
console.log(new M().ta());
`)
}

func TestE2EQualifiedUndeclaredConstructorJS(t *testing.T) {
	// new through an undeclared qualifier reads the global property, else
	// throws its ReferenceError, as the root name's read does.
	assertSameAsNodeCLIJS(t, `
try { const i = new Nope.Instance(1); console.log(i); } catch (e) { console.log(e.name, e.message); }
try { const i = new Nope.a.b(); console.log(i); } catch (e) { console.log(e.name, e.message); }
globalThis.Q = { C: class { constructor(x) { this.x = x; } }, a: { C: class { constructor(x) { this.y = x; } } } };
console.log(new Q.C(4).x, new Q.a.C(5).y);
`)
}

func TestE2ELibOnlyGlobalsReadAsUndeclared(t *testing.T) {
	// A global only TypeScript's library declares, which the runtime does
	// not provide, is read as an undeclared name.
	assertSameAsNode(t, `
try { const x = IntersectionObserver; console.log(typeof x); } catch (e: any) { console.log(e.name, e.message); }
`)
	assertSameAsNodeCLIJS(t, `
try { console.log(typeof IntersectionObserver, typeof ResizeObserver); } catch (e) { console.log(e.name); }
try { new IntersectionObserver(() => {}); } catch (e) { console.log(e.name, e.message); }
`)
}

func TestE2EProcessOwnPropertyJS(t *testing.T) {
	// A property no module lowers is the process object's own.
	assertSameAsNodeCLIJS(t, `
process.maxTickDepth = 10;
console.log(process.maxTickDepth);
process.title2 = "x"; console.log(process.title2, typeof process.nextTick);
`)
}

func TestE2EArrayFromTypedArrayReturned(t *testing.T) {
	// Array.from of a typed array makes numbers, whichever boundary (a
	// return, a binding) reads them.
	assertSameAsNode(t, `
class B { tb() { const u = new Uint8Array(4); u[0] = 7; return u; } ta() { return Array.from(this.tb()); } tc() { return [...this.tb()]; } }
console.log(new B().ta(), new B().tc());
const a: number[] = Array.from(new Uint8Array([200, 1])); a.push(3.5); console.log(a, Array.from(new Int16Array([-2])), Array.from(new Float32Array([1.5])));
function f(): number[] { return Array.from(new Uint16Array([65535, 2])); } console.log(f());
`)
}

func TestE2EDestructuredFromAnyIsAny(t *testing.T) {
	// A property, nested pattern or rest destructured from any is any.
	assertSameAsNode(t, `
const i: any = JSON.parse('{"b":[1],"c":{"d":5},"e":2}');
const { b, c: { d }, ...rest } = i;
console.log(b.length + 1, d * 2, rest);
const { z = 9 } = i; console.log(z);
`)
	assertSameAsNodeCLIJS(t, `
const i = JSON.parse('{"buffer":[1,2]}');
const { buffer } = i;
const n = Buffer.from(buffer).write("x");
console.log(n);
`)
}

func TestE2ETypeOnlyLibGlobalsReadAsUndeclared(t *testing.T) {
	// A global the library declares only as a type (an interface) and
	// gives no value: a ReferenceError at run time, not a compile error.
	assertSameAsNode(t, `
for (const f of [() => new IntersectionObserver(() => {}), () => ResizeObserver]) {
  try { f(); console.log("ok"); } catch (e: any) { console.log(e.name); }
}
`)
}
