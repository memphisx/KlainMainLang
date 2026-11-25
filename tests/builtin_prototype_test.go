package tests

import "testing"

func TestE2EPrimitivePrototypeValues(t *testing.T) {
	assertSameAsNode(t, `console.log(typeof Number.prototype.toFixed, Number.prototype.toFixed.call(3.14159, 2));
console.log(Boolean.prototype.toString.call(true), Number.prototype.constructor === Number);
console.log(typeof Symbol.prototype.toString, Symbol.prototype.toString.call(Symbol("q")));
console.log(BigInt.prototype.toString.call(12n));
try { Number.prototype.toFixed.call("x" as any, 1); } catch (e: any) { console.log(e.constructor.name, e.message); }
console.log(Object.prototype.toString.call(Symbol.prototype), Object.getOwnPropertyNames(Boolean.prototype).sort());
`)
}

func TestE2EBuiltinPrototypeGetters(t *testing.T) {
	assertSameAsNode(t, `const d = Object.getOwnPropertyDescriptor(Map.prototype, "size")!;
console.log(typeof d.get, d.set, d.enumerable, d.configurable, d.get!.name);
console.log(d.get!.call(new Map([[1, 2]])), Object.getOwnPropertyDescriptor(Set.prototype, "size")!.get!.call(new Set([1, 2, 3])));
try { d.get!.call({}); } catch (e: any) { console.log(e.message); }
const b = Object.getOwnPropertyDescriptor(ArrayBuffer.prototype, "byteLength")!;
console.log(b.get!.call(new ArrayBuffer(8)));
console.log(Object.getOwnPropertyNames(Map.prototype).includes("size"), typeof Map.prototype.get);
`)
}

func TestE2EBuiltinPrototypeValuesMixed(t *testing.T) {
	assertSameAsNode(t, `const d = Object.getOwnPropertyDescriptor(Map.prototype, "size");
console.log(typeof d?.get, d?.set, d?.enumerable);
console.log(typeof URL.prototype, typeof Number.prototype.toFixed, typeof Boolean.prototype.valueOf);
console.log(typeof Symbol.prototype.toString, typeof BigInt.prototype.toString, typeof Uint8Array.prototype.subarray);
console.log(Object.getPrototypeOf(new Map()) === Map.prototype);
`)
}

func TestE2ETypedArrayMethodsThroughAny(t *testing.T) {
	assertSameAsNode(t, `const x: any = new Uint8Array([1, 2, 3]);
console.log(x.subarray(1), x.map((v: number) => v * 2), x.indexOf(2), x.join("-"), x.slice(0, 2));
const f: any = new Float64Array([1.5, 2.5]);
console.log(f.reduce((a: number, b: number) => a + b, 0), f.at(-1), f.length, f.byteLength);
const y: any = new Int16Array([5, -3]);
console.log(x.subarray === y.subarray, x.map === y.map, y.toSorted(), y.fill(7), y);
try { x.subarray.call([1, 2], 1); } catch (e: any) { console.log(e.constructor.name, e.message); }
`)
}

func TestE2ETypedArrayPrototypeReceivers(t *testing.T) {
	assertSameAsNode(t, `const tries: [string, () => unknown][] = [
  ["map-get-arr", () => Map.prototype.get.call([1] as any, 1)],
  ["sub-obj", () => Uint8Array.prototype.subarray.call({} as any, 1)],
  ["sub-arr", () => Uint8Array.prototype.subarray.call([1, 2] as any, 1)],
  ["sub-num", () => Uint8Array.prototype.subarray.call(5 as any, 1)],
  ["map-arr", () => Uint8Array.prototype.map.call([1] as any, (x: number) => x)],
  ["fill-obj", () => Uint8Array.prototype.fill.call({} as any, 1)],
  ["join-obj", () => Uint8Array.prototype.join.call({} as any)],
];
for (const [n, f] of tries) { try { f(); console.log(n, "ok"); } catch (e: any) { console.log(n, e.constructor.name, e.message); } }
const P = Object.getPrototypeOf(Uint8Array.prototype);
console.log(P === Object.getPrototypeOf(Float64Array.prototype), Uint8Array.prototype.BYTES_PER_ELEMENT, Int16Array.prototype.constructor === Int16Array);
console.log(Object.getOwnPropertyDescriptor(P, "byteLength")!.get!.call(new Int32Array(3)), Uint8Array.prototype.subarray === Float32Array.prototype.subarray);
console.log(Object.getPrototypeOf(new Uint8Array(1)) === Uint8Array.prototype);
`)
}

func TestE2ETypedArrayNumericSort1(t *testing.T) {
	assertSameAsNode(t, `const y = new Int16Array([5, -3, 2, 10]); y.sort(); console.log(y);
const f = new Float64Array([0.5, NaN, -0, 0, -1, 10, 2]); f.sort(); console.log(f, Object.is(f[2], -0));
const u = new Uint32Array([4000000000, 3, 20]); console.log(u.toSorted(), u.toReversed(), u.with(1, 7));
const b = new Uint8Array([200, 1, 30]); b.sort(); console.log(b, new Int8Array([-5, 3, -100]).toSorted());
const g = new Float32Array([2.5, -1.25]); console.log(g.toSorted(), new BigInt64Array([5n, -2n]).toSorted());
const z = new Int16Array([5, -3, 2]);
console.log(z.toSorted(), z.toReversed(), z.with(0, 9), z.toSorted((a, b) => b - a), z);
`)
}

func TestE2ETypedArrayNumericSort2(t *testing.T) {
	assertSameAsNode(t, `const g = new Float32Array([2.5, -1.25, 7]);
console.log(g.toSorted(), g.toSorted((a, b) => b - a));
g.sort((a, b) => b - a); console.log(g);
const h = new Int16Array([3, -9, 4]); h.sort((a, b) => b - a); console.log(h);
`)
}

func TestE2EArrayWithRangeError(t *testing.T) {
	assertSameAsNode(t, `for (const f of [() => [1, 2].with(5, 1), () => [1, 2].with(-3, 1), () => new Int8Array(2).with(2, 1), () => new Int8Array(2).with(-9, 1)]) {
  try { console.log(f()); } catch (e: any) { console.log(e.constructor.name, e.message); }
}
console.log([1, 2].with(-1, 9), new Int8Array(2).with(-2, 4));`)
}

// A primitive's constructor is its conversion function: compared, read
// as a value, and called.
func TestE2EPrimitiveConstructorValue(t *testing.T) {
	assertSameAsNode(t, `const n = 5, s = "a", b = true, g = 7n;
console.log(n.constructor === Number, s.constructor === String, b.constructor === Boolean, g.constructor === BigInt);
console.log(n.constructor.name, s.constructor.name, g.constructor.name, typeof b.constructor);
const f = s.constructor;
console.log(f(42), s.constructor(43), (7).constructor("8") + 1, true.constructor(0), g.constructor(3));
`)
}

// A class method read as a value keeps its identity and takes its `this`
// from call, apply and bind.
func TestE2EMethodValueThis(t *testing.T) {
	assertSameAsNode(t, `class K { n = "k"; greet(p: string = "hi") { return p + " " + this.n; } }
const k = new K(); const f = k.greet;
console.log(k.greet.name, typeof f, f === K.prototype.greet);
console.log(f.call(k), f.apply(k, ["yo"]), f.bind(k)(), f.call({ n: "obj" } as any, "hey"));
console.log(k.greet.call(k), k.greet.apply(k), k.greet.bind(k)("bound"));
`)
}

// Array.prototype is itself an (empty) Array, and its methods take any
// array-like receiver.
func TestE2EArrayPrototypeIsArray(t *testing.T) {
	assertSameAsNode(t, `const p: any = Array.prototype
console.log(p.length, Object.prototype.toString.call(p), p, Array.isArray(Array.prototype), Array.isArray(p))
console.log(JSON.stringify(p), Array.isArray({}), Array.isArray(Object.prototype))
function f(a: number, b: number) { return Array.prototype.slice.call(arguments, 1) }
console.log(f(1, 2), Array.prototype.map.call("ab", (c: string) => c + c), Array.prototype.join.call([1, 2], "-"))
`)
}

// A callback that is no function throws before the method iterates, and a
// call of a non-function names its callee, as V8 words both.
func TestE2ENotAFunctionMessages(t *testing.T) {
	assertSameAsNode(t, `const vals: any[] = [undefined, null, 5, "s", true, {}]
for (const v of vals) { try { [1].map(v) } catch (e: any) { console.log(e.constructor.name, e.message) } }
try { ([] as number[]).forEach(3 as any) } catch (e: any) { console.log(e.message) }
const p: any = [1, 2]
for (const m of ["forEach", "filter", "reduce", "flatMap"]) { try { p[m]() } catch (e: any) { console.log(m, e.message) } }
console.log(p.splice(), [1, 2].flatMap((x: number) => [x, x] as any))
const u: any = 5, arr: any[] = [5], o: any = { x: 5 }, g: any = () => 5
for (const h of [() => u(), () => arr[0](), () => o.x(), () => g()(), () => o["x"]()]) { try { h() } catch (e: any) { console.log(e.message) } }
`)
}

// A rejected write or delete names the object as V8 does: `#<Object>`,
// `#<Foo>`, `[object Object]` for a null prototype.
func TestE2EReadOnlyMessages(t *testing.T) {
	assertSameAsNode(t, `export {}
class Foo { a = 1; constructor() { Object.freeze(this) } }
const objs: any[] = [Object.freeze({ a: 1 }), Object.freeze(Object.assign(Object.create(null), { a: 1 })), new Foo()]
for (const o of objs) {
  try { o.a = 2 } catch (e: any) { console.log(e.message) }
  try { o.z = 2 } catch (e: any) { console.log(e.message) }
  try { delete o.a } catch (e: any) { console.log(e.message) }
  try { delete o.zz; console.log("absent ok") } catch (e: any) { console.log(e.message) }
}
const g: any = { get x() { return 1 } }
try { g.x = 2 } catch (e: any) { console.log(e.message) }
const sealed: any = Object.seal({ a: 1 })
sealed.a = 5
try { delete sealed.a } catch (e: any) { console.log(e.message, sealed.a) }
`)
}

// A typed array asserted to another kind through unknown stays its own
// kind, as in JavaScript.
func TestE2ETypedArrayCastKeepsKind(t *testing.T) {
	assertSameAsNode(t, `export {}
const u16 = new Uint16Array([1, 300, 65535])
const a = u16 as unknown as Uint8Array
console.log(a, a.length, a[1], a instanceof Uint16Array, a.constructor.name, a.byteLength)
`)
}

// A comparator over each element width, typed `number` (a predeclared
// function) or from the element (inline): each element is read at its own
// width and passed as the comparator takes it.
func TestE2ETypedArraySortComparatorWidths(t *testing.T) {
	assertSameAsNode(t, `
export {}
const d = (a: number, b: number) => b - a
console.log(new Int8Array([-5, 3, -100, 127]).sort(d), new Uint8Array([200, 1, 255]).sort(d))
console.log(new Int16Array([-30000, 5, 32767]).sort(d), new Uint16Array([65535, 2, 40000]).sort(d))
console.log(new Int32Array([-2147483648, 7, 2147483647]).sort(d), new Uint32Array([4294967295, 3, 3000000000]).sort(d))
console.log(new Float32Array([2.5, -1.25, 7, NaN]).sort(d), new Float64Array([0.5, -3]).sort(d))
console.log([true, false, true].sort((a, b) => Number(a) - Number(b)))
console.log(new Uint8ClampedArray([9, 0, 250]).toSorted(d))
const z = new Int16Array([5, -3, 2]); console.log(z.toSorted((a, b) => b - a), new Float32Array([1.5, -2]).toSorted((a, b) => a - b))
`)
}

// A builtin constructor held as a value reads the same prototype object a
// direct read gives, and a library class's instances inherit that object.
func TestE2EBuiltinCtorRefPrototype(t *testing.T) {
	assertSameAsNode(t, `const cs: any[] = [Map, Set, WeakMap, Promise, Array, Uint8Array, Function, Object, DataView, ArrayBuffer, Date, RegExp];
for (const c of cs) console.log(typeof c.prototype, typeof c.prototype.constructor, c.prototype.constructor === c);
const M: any = Map;
console.log(M.prototype === Map.prototype, M.prototype.get === Map.prototype.get, Object.getPrototypeOf(new M()) === M.prototype);
const d = new DataView(new ArrayBuffer(4));
const D: any = DataView;
console.log(Object.getPrototypeOf(d) === DataView.prototype, D.prototype === DataView.prototype);
const f: any = () => 1;
class K {}
const k: any = K;
console.log(f.constructor === Function, f.constructor.name, Function.prototype.constructor === Function, k.constructor === Function);
`)
}

// The error kinds' prototype objects: their name, empty message and
// constructor, Error.prototype's toString, each inheriting Error.prototype,
// and every error's (a subclass's too) [[Prototype]].
func TestE2EErrorKindPrototypes(t *testing.T) {
	assertSameAsNode(t, `const E: any = Error; const T: any = TypeError;
console.log(typeof Error.prototype, typeof E.prototype, Error.prototype.name, TypeError.prototype.name, JSON.stringify(Error.prototype.message));
console.log(Object.getPrototypeOf(TypeError.prototype) === Error.prototype, T.prototype === TypeError.prototype, Error.prototype.constructor === Error);
console.log(Object.getPrototypeOf(new TypeError("bad")) === TypeError.prototype, Object.getPrototypeOf(new Error("x")) === Error.prototype);
const a: any = new RangeError("r");
console.log(Object.getPrototypeOf(a) === RangeError.prototype, Error.prototype.toString.call(a));
console.log(Error.prototype.toString.call({ name: "N", message: "m" }), Error.prototype.toString.call({}), Error.prototype.toString.call({ name: "", message: "only" }));
try { Error.prototype.toString.call(1 as any); } catch (e) { console.log((e as Error).name); }
class MyErr extends Error {}
class Sub extends MyErr {}
console.log(Object.getPrototypeOf(MyErr.prototype) === Error.prototype, Object.getPrototypeOf(new MyErr("q")) === MyErr.prototype, Object.getPrototypeOf(Sub.prototype) === MyErr.prototype);
`)
}

// Object called as a function, held as a value, read as every object's
// constructor, constructed through a value and passed as a callback: one
// constructor, wrapping a primitive in each form.
func TestE2EObjectConstructorForms(t *testing.T) {
	assertSameAsNode(t, `const o = { a: 1 };
console.log(Object(o) === o, typeof Object(), Object(null), Object(undefined));
function f(a?: any) { return arguments.constructor === Object; }
console.log(f(1), ({}).constructor === Object);
const x: any = 5; console.log(typeof Object(x), Object(x) instanceof Number);
const g = Object; console.log(g === Object, typeof g({}));
const o2: any = { a: 1 };
console.log(Object.prototype.constructor === Object, o2.constructor === Object, Object.name, o2.constructor.name);
console.log(typeof o2.constructor.keys, o2.constructor.keys(o2), Object.getPrototypeOf(o2).constructor === Object);
const C: any = o2.constructor; console.log(C(null), C(o2) === o, new C() instanceof Object);
console.log([1, null].map(Object).length, typeof [5].map(Object)[0]);
const D: any = Object; console.log([1, null, "s"].map(D).map((w: any) => typeof w), [true].map(Object)[0] instanceof Boolean);
`)
}

// A promise's `constructor` is Promise.prototype's: Promise, or an accessor
// defined there, which await's and for await's PromiseResolve read too.
func TestE2EPromiseProtoConstructorJS(t *testing.T) {
	assertJSFileSameAsNode(t, `
const p = Promise.resolve(1);
console.log(p.constructor === Promise);
let n = 0;
Object.defineProperty(Promise.prototype, "constructor", { get() { n++; return Promise; }, configurable: true });
console.log(p.constructor === Promise, n);
(async () => {
  await Promise.resolve(1);
  console.log("a", n);
  for await (const x of [Promise.resolve(2)]) { console.log("b", n, x); }
  console.log("c", n);
  Object.defineProperty(Promise.prototype, "constructor", { get() { throw new Error("boom"); }, configurable: true });
  try { await Promise.resolve(3); console.log("no"); } catch (e) { console.log("caught", e.message); }
})();
`)
}

// Array.prototype's data properties and accessors (run with the array as
// this) as an array's property reads find them.
func TestE2EArrayProtoPropertiesJS(t *testing.T) {
	assertJSFileSameAsNode(t, `
Object.defineProperty(Array.prototype, "zz", { get() { return this.length + 9; }, configurable: true });
Array.prototype.yy = 5;
Object.defineProperty(Array.prototype, "ww", { value: 4, configurable: true });
Array.prototype.sum = function () { return this.reduce((a, b) => a + b, 0); };
const a = [1, 2];
const b = [1];
console.log(a.zz, a.yy, b.zz, b.yy, [].zz, a.ww, a.sum());
`)
}

// A constructor function's `prototype` may hold a primitive or null: it
// reads back as assigned, and `new F` then makes an Object.prototype
// object.
func TestE2EPrimitiveFunctionPrototypeJS(t *testing.T) {
	assertJSFileSameAsNode(t, `
function F() {}
F.prototype = 1;
console.log(F.prototype, Object.getPrototypeOf(new F()) === Object.prototype);
function G() {}
G.prototype += 1;
console.log(typeof G.prototype, G.prototype);
function H() {}
H.prototype = null;
console.log(H.prototype, Object.getPrototypeOf(new H()) === Object.prototype);
function K() { this.a = 1; }
K.prototype.m = function () { return this.a; };
console.log(new K().m(), Object.getPrototypeOf(new K()) === K.prototype);
K.prototype = { m() { return 9; } };
console.log(new K().m());
`)
}

// A static object (a class instance, an object literal of a static type) as
// a dynamic object's prototype: its fields, added properties and class
// members read through the chain, accessors with the receiver as this.
func TestE2EObjectCreateStaticObjectJS(t *testing.T) {
	assertJSFileSameAsNode(t, `
class C { constructor() { this.x = 1; } m() { return this.x + 1; } get g() { return this.x * 10; } }
const c = new C();
const o = Object.create(c);
console.log(o.x, o.m(), Object.getPrototypeOf(o) === c, o instanceof C);
o.x = 5;
console.log(o.x, c.x, o.m(), o.g, "m" in o, Object.keys(o), o.hasOwnProperty("m"));
const q = Object.create(o);
console.log(q.x, q.m(), q instanceof C, Object.getPrototypeOf(Object.getPrototypeOf(q)) === c);
const lit = { a: 1, b: "s" };
const p = Object.create(lit); lit.a = 2;
p.c = 3;
console.log(p.a, p.b, p.c, Object.keys(p), "a" in p, Object.setPrototypeOf({}, c).x);
`)
}

func TestE2EObjectCreateStaticObjectTS(t *testing.T) {
	assertOutput(t, `
class C { x = 1; m(): number { return this.x + 1; } }
const c = new C();
const o: C = Object.create(c);
console.log(o.x, o.m(), Object.getPrototypeOf(o) === c, o instanceof C);
`, "1 2 true true\n")
}

// A class extending a host record (Response, Request, XMLHttpRequest):
// its instance is the record, after its own header word; with or without a
// constructor of its own, through a further subclass, and read as any.
func TestE2EHostRecordSubclassTS(t *testing.T) {
	assertSameAsNode(t, `
class MyResp extends Response {
  tag = "t";
  constructor(body: string) { super(body, { status: 201, headers: { "x-a": "1" } }); }
  hello(): string { return this.tag + this.status; }
}
class Deeper extends MyResp { more(): number { return this.status + 1; } }
const r = new Deeper("hi");
console.log(r.status, r.ok, r.hello(), r.more(), r instanceof Response, r instanceof MyResp, r.headers.get("x-a"));
const a: any = r;
console.log(a.status, a.tag, typeof a.text, String(r));
class R0 extends Response {}
const r0 = new R0("body0", { status: 202 });
class R1 extends Response { f = 3; }
const r1 = new R1("b1");
console.log(r0.status, r1.status, r1.f, r0 instanceof Response);
class MyReq extends Request {
  constructor(url: string) { super(url, { method: "POST", body: "payload", headers: { "x-q": "9" } }); }
  info(): string { return this.method + " " + this.url; }
}
const q = new MyReq("http://example.com/a");
console.log(q.info(), q instanceof Request, q.headers.get("x-q"), String(q));
(async () => {
  console.log(await r.text(), r.bodyUsed, await r0.text(), await r1.text(), await q.text());
  console.log((await new MyResp("{\"k\":2}").json()).k);
})();
`)
}

// XMLHttpRequest is not a Node global: the expected values are the spec's.
func TestE2EXHRSubclassJS(t *testing.T) {
	assertOutputCLIJS(t, `
class X extends XMLHttpRequest { kind() { return "x" + this.readyState; } }
const x = new X();
console.log(x.readyState, x.kind(), x instanceof XMLHttpRequest, x instanceof X, x.status);
x.open("GET", "http://127.0.0.1:9/none");
console.log(x.readyState, String(new XMLHttpRequest()), new XMLHttpRequest() instanceof XMLHttpRequest);
`, "0 x0 true true 0\n1 [object XMLHttpRequest] true")
}

// A void value stored in a container is undefined: an array's or an
// object's element, a Map's value, a Set's element.
func TestE2EVoidValueInContainersTS(t *testing.T) {
	assertSameAsNode(t, `
function v(): void {}
const arr = [v(), v()];
console.log(arr, arr.length, arr[0] === undefined);
const o = { a: v() };
console.log(o, "a" in o, o.a);
const m = new Map<string, void>();
m.set("k", v());
console.log(m.get("k"), m.has("k"), m.size, [...m.entries()]);
const s = new Set<void>();
s.add(v()); s.add(undefined);
console.log(s.size, [...s]);
const r: void[] = [v()];
console.log(r);
`)
}

// A not-a-function call's callee prints as V8's call printer shows it: a
// regular expression literal as written, `new RegExp` as an intermediate
// value, a static member chain by its names.
func TestE2ENotFunctionCalleeNamesJS(t *testing.T) {
	assertJSFileSameAsNode(t, `
try { /a/.foo(); } catch (e) { console.log(e.message); }
try { /a\/b/gi.foo(); } catch (e) { console.log(e.message); }
try { new RegExp("a").foo(); } catch (e) { console.log(e.message); }
class D { static m = {}; }
try { D.m.zz(); } catch (e) { console.log(e.message); }
const o = { p: { q: {} } };
try { o.p.q.r(); } catch (e) { console.log(e.message); }
`)
}

// util.format's %s inspects an object whose toString is a builtin one (a
// class instance inheriting Object.prototype's) and calls one the program
// declared, down its prototype chain.
func TestE2EUtilFormatStringObjectJS(t *testing.T) {
	assertJSFileSameAsNode(t, `
const util = require("util");
class P { constructor() { this.x = 1; } }
class Q { toString() { return "Q!"; } }
class R extends Q {}
class A2 extends Array {}
const o = { toString() { return "own"; } };
console.log(util.format("%s|%s|%s|%s|%s", {}, new P(), new Q(), new R(), o));
console.log(util.format("%s|%s|%s", new A2(), new Map([[1, 2]]), [1, [2, 3]]));
console.log(util.format("%s", Object.create(null)), util.format("%s", new Error("e1")).split("\n")[0]);
console.log(util.format("%s", { [Symbol.toPrimitive]() { return "prim"; } }));
`)
}

// Queuing strategies are standalone objects; an invalid size() result
// raises a RangeError and errors the stream.
func TestE2EQueuingStrategiesJS(t *testing.T) {
	assertJSFileSameAsNode(t, `
const c = new CountQueuingStrategy({ highWaterMark: 3 });
console.log(c.highWaterMark, c.size(), typeof c.size, c instanceof CountQueuingStrategy);
const b = new ByteLengthQueuingStrategy({ highWaterMark: 16 });
console.log(b.highWaterMark, b.size(new Uint8Array(5)), b.size({ byteLength: 7 }));
for (const v of [NaN, -1, "3", Infinity]) {
  new ReadableStream({ start(ctl) { try { ctl.enqueue("x"); console.log("ok", ctl.desiredSize); } catch (e) { console.log("enq", String(v), e.constructor.name); } } }, { highWaterMark: 5, size() { return v; } });
}
const w = new WritableStream({ write(ch) { console.log("w", ch); } }, { size() { return -1; } }).getWriter();
w.write("a").then(() => console.log("wrote"), (e) => console.log("werr", e.constructor.name));
w.closed.then(() => {}, (e) => console.log("closed", e.constructor.name));
`)
}

// A transformer's start() promise holds both sides until it settles; its
// rejection errors them.
func TestE2ETransformStartPromiseJS(t *testing.T) {
	assertJSFileSameAsNode(t, `
const log = [];
const ts = new TransformStream({
  start() { log.push("start"); return new Promise((r) => setTimeout(() => { log.push("started"); r(); }, 30)); },
  transform(ch, c) { log.push("t:" + ch); c.enqueue(ch); },
});
const w = ts.writable.getWriter();
w.write("a");
ts.readable.getReader().read().then((r) => { console.log(r.value, log.join(",")); });
const ts2 = new TransformStream({ start() { return Promise.reject(new Error("boom")); } });
ts2.writable.getWriter().write("x").then(() => console.log("wrote?"), (e) => console.log("werr", e.message));
ts2.readable.getReader().read().then(() => console.log("read?"), (e) => console.log("rerr", e.message));
`)
}

// A stream controller typed any is the controller (its members through
// the box); a transformer's is a TransformStreamDefaultController, whose
// inspection shows its stream's backpressure; String() of a stream object
// is its [object Class].
func TestE2EStreamControllersTS(t *testing.T) {
	assertSameAsNode(t, `
new ReadableStream<string>({ start(c: any) { console.log(typeof c, Object.prototype.toString.call(c), c.desiredSize); } });
const r = new ReadableStream<string>({ start(c: any) { c.enqueue("q"); c.close(); } });
r.getReader().read().then((x) => console.log(x.value));
console.log(String(r), String(new ReadableStream<string>().getReader()));
new WritableStream<string>({ start(c) { console.log(String(c)); } });
const t = new TransformStream<string, string>({
  start(c) { console.log(String(c)); console.log(c); },
  transform(ch, c) { console.log(c); c.enqueue(ch + "1"); },
  flush(c) { c.terminate(); },
});
console.log(String(t), t);
const ww = t.writable.getWriter(); ww.write("a"); ww.close();
const rr = t.readable.getReader();
rr.read().then((x) => { console.log(x.value); return rr.read(); }).then((x) => console.log(x.done, t));
const t2 = new TransformStream<string, string>({ transform(ch: string, c: any) { console.log(typeof c.terminate, typeof c.error); c.enqueue(ch); } });
t2.writable.getWriter().write("q");
t2.readable.getReader().read().then((x) => console.log(x.value));
`)
}

// A primitive's index read logs undefined; destructuring null or undefined
// names the value as V8 does.
func TestE2EPrimitiveIndexAndNullDestructureTS(t *testing.T) {
	assertSameAsNode(t, `
console.log("a", (1n as any)[0]);
const b: any = 2n; console.log(b[0], (5 as any)[0]);
try { const [a] = null as any; console.log(a); } catch (e: any) { console.log(e.message); }
try { const [a] = undefined as any; console.log(a); } catch (e: any) { console.log(e.message); }
`)
}

// An explicit undefined cause is still an own property; an error made
// without a message has none of its own; getOwnPropertyNames lists an
// error's own keys, a subclass's fields among them.
func TestE2EErrorOwnCauseAndNamesTS(t *testing.T) {
	assertSameAsNode(t, `
const e1 = new Error("x", { cause: undefined });
console.log("cause" in e1, e1.cause, Object.keys(e1), Object.getOwnPropertyNames(e1));
console.log(Object.getOwnPropertyDescriptor(e1, "cause"));
console.log(require("util").inspect(e1).split("\n").filter((l: string) => !l.trimStart().startsWith("at ")).join("|"));
const e2 = new Error("y", {});
console.log("cause" in e2, Object.getOwnPropertyNames(e2));
class E extends Error { extra = 1; constructor(m: string, o?: { cause?: any }) { super(m, o); } }
const a = new E("a", { cause: undefined }), b = new E("b");
console.log("cause" in a, "cause" in b, Object.getOwnPropertyNames(a));
console.log(Object.getOwnPropertyDescriptor(a, "extra"));
const f = new Error();
console.log(Object.prototype.hasOwnProperty.call(f, "message"), "message" in f, Object.getOwnPropertyNames(f));
console.log(Object.getOwnPropertyNames(new AggregateError([1], "m")));
e1.cause = 5;
console.log(Object.keys(e1), e1.cause);
`)
}

// Through classes with no constructor of their own down to a host record:
// the record is built from the arguments as the builtin's `new` builds it,
// and Object.prototype.toString names the builtin.
func TestE2EHostRecordSubclassNoCtorChainTS(t *testing.T) {
	assertSameAsNode(t, `
class M extends Response {}
class D extends M { f = 1; }
const d = new D("x", { status: 203 });
console.log(d.status, d instanceof M, d instanceof Response, d.f, String(d));
class Q0 extends Request {}
class Q1 extends Q0 {}
const q = new Q1("http://a.b/", { method: "PUT" });
console.log(q.method, String(q), Object.prototype.toString.call(q));
d.text().then(t => console.log(t));
`)
}
