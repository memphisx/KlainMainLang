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
