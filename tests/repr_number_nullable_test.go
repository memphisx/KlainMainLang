package tests

import "testing"

// Builtin counts and callback indexes are numbers (doubles): arithmetic on
// them is JavaScript's, past 2^53 too (TDD-00230 P3.3).
func TestReprCountsAndIndexesAreNumbers(t *testing.T) {
	assertSameAsNode(t, `const a = [1, 2, 3];
console.log(a.length / 2, a.length * 2 ** 62, "ab".length / 4, new Set([1]).size / 2);
const u = new Uint8Array(5);
console.log(u.length / 2, u.byteLength / 2, new ArrayBuffer(3).byteLength / 2);
const big = [0, 0];
console.log(big.map((_, i) => (i + 4294967296) * 4294967296 + i));
console.log(Array.from({ length: 4 }, (_, i) => i * i), "x-y".replace(/-/g, (m, off) => String(off / 2)));
const v = new Int32Array(3);
const n = v[5];
console.log(n, n === undefined);
`)
}

// Awaiting a ` + "`Promise<T> | null`" + ` that holds null awaits a plain value: the
// null, after a tick.
func TestAwaitNullablePromiseHoldingNull(t *testing.T) {
	assertSameAsNode(t, `class Feed { promise: Promise<any> | null = null; }
class F2 { p: Promise<string> | null = null; }
async function main() {
  const f = new Feed();
  const p = f.promise;
  const msg: any = await p;
  console.log(msg);
  f.promise = Promise.resolve(5);
  const q = f.promise;
  console.log(await q);
  const g = new F2();
  const a = g.p;
  console.log(await a);
}
main();
`)
}

// A function with default parameters called through a function-typed value
// (whose type carries no defaults) still fills them when the argument is
// absent or undefined.
func TestDefaultParamsThroughFunctionTypedValue(t *testing.T) {
	assertSameAsNode(t, `const f = (a: string, b = "dflt"): string => a + b;
const g: (a: string, b?: string) => string = f;
console.log(g("p"), g("p", undefined), g("p", "q"));
function h(n = 5): number { return n; }
const hh: (n?: number) => number = h;
console.log(hh(), hh(undefined), hh(1));
const cbs: ((x?: number) => number)[] = [];
cbs.push((x = 3) => x * 2);
console.log(cbs[0](), cbs[0](1));
[1, 2].forEach((v, i, a, extra = 7) => console.log(v, extra));
[1, 2].forEach((v, i, a, k = v * 10) => console.log(k));
`)
}

// A parameter default is filled by the function itself: a non-literal
// default typed by the checker, one reading an earlier parameter, an
// explicit null kept where the type admits it, methods and closures.
func TestDefaultParamsFilledByTheBody(t *testing.T) {
	assertSameAsNode(t, `function h(n = 5, s = "x" + n, b = true): string { return n + " " + s + " " + b; }
console.log(h(), h(1), h(undefined, "y"), h(2, undefined, false));
function arr(xs: number[] = [1, 2]): number { return xs.length; }
console.log(arr(), arr([5]));
function cap(k = 3): () => number { return () => k * 2; }
console.log(cap()(), cap(4)());
function nul(x: string | null = "d"): string { return String(x); }
console.log(nul(), nul(null), nul(undefined));
const o = { m(z = 9) { return z + 1; } };
console.log(o.m(), o.m(1));
class C { v: number; constructor(v: number = 4) { this.v = v; } get(w = this.v * 2): number { return w; } }
console.log(new C().get(), new C(1).get(), new C().get(3));
function scaler(mult: number) { return (v: number, factor: number = mult): number => v * factor; }
console.log([1, 2, 3].map(scaler(10)).join(","));
`)
}

// Values whose union has no single slot are boxed: a three-state operand
// of `&&`/`||`, a ternary of a reference and a number, and an array of a
// three-state value and a number (TDD-00230 P3.3: every union has a
// representation).
func TestMixedUnionsAreBoxed(t *testing.T) {
	assertSameAsNode(t, `function f(err: Error | null | undefined, ok: boolean, n: number, s: string | null | undefined) {
  const r = err && ok;
  console.log(r, typeof r);
  const b = n > 1 ? s : n;
  console.log(b, s ?? 5, s || n);
  const d = [s, n];
  console.log(d);
  const k = n > 1 ? [n] : n;
  console.log(k);
}
f(null, true, 2, null); f(undefined, false, 2, undefined); f(null, true, 0, "q");
class K { v = 1 }
function g(k: K | null, o: { a: number } | undefined, e3: K | null | undefined) {
  const a = k ?? 0;
  const b = o ?? false;
  const c = e3 ?? 7;
  console.log(a, b, c, typeof a, typeof c);
}
g(null, undefined, null); g(new K(), { a: 2 }, undefined); g(null, { a: 3 }, new K());
`)
}

// A function's own property holding a string-keyed dictionary reads as the
// dictionary (its index and members statically), as util.inspect.colors.
func TestFunctionExpandoDictionaryReads(t *testing.T) {
	assertSameAsNodeImports(t, `import * as util from "node:util";
console.log(util.inspect.colors["red"], util.inspect.colors.bold, Object.keys(util.inspect.colors).length > 10);
function f() {}
f.tbl = { a: [1, 2] } as { [k: string]: number[] };
console.log(f.tbl["a"], f.tbl.a.length, f.tbl["zz"]);
const r: { [k: string]: number[] } = { a: [1] };
const m = new Map<string, number[]>([["a", [1]]]);
const g2 = m.get("zz");
console.log(r["zz"], r.zz, g2, g2 === undefined, r["zz"] ? 1 : 0, m.get("a") ? 1 : 0);
`)
}

// `a && b` with a possibly-absent object a and a scalar b is b's type with
// a's absence: a three-state boolean for `Error | null | undefined && boolean`.
func TestAndOfAbsentObjectAndScalar(t *testing.T) {
	assertSameAsNode(t, `class K { n = 3 }
function f(err: Error | null | undefined, ok: boolean, k: K | null, u: K | undefined) {
  const r = err && ok;
  const s = k && k.n;
  const t = u && 5;
  console.log(r, typeof r, s, t, r === null, r === undefined, JSON.stringify({ r, s, t }));
  if (err && ok) console.log("both");
}
f(null, true, null, undefined); f(undefined, false, new K(), new K());
f(new Error("x"), true, null, new K()); f(new Error("y"), false, new K(), undefined);
`)
}

// A string declaration given a read past an array's end holds undefined
// (BACKLOG 20's declaration case).
func TestStringDeclarationKeepsUndefined(t *testing.T) {
	assertSameAsNode(t, `const a: string[] = ["x"];
const s: string = a[5];
console.log(s, s === undefined, s === null, typeof s, String(s), `+"`${s}`"+`, [s], { s });
const t2: string = a[0];
console.log(t2, t2.length, typeof t2);
const j = new Date(NaN).toJSON();
console.log(j, typeof j, j === null);
function f() {
  const n: number[] = [1];
  const q: number = n[4];
  console.log(q, q + 1, q === undefined, typeof q);
  const r: number = n[0];
  console.log(r, r + 1);
}
f();
`)
}

// A union of a string and a stream stays a union (only members that are all
// plain strings collapse to a string).
func TestStringOrStreamUnionKeepsStream(t *testing.T) {
	assertSameAsNode(t, `interface Res { body: string | ReadableStream<Uint8Array> }
const a: Res = { body: new ReadableStream<Uint8Array>() };
const b: Res = { body: "text" };
console.log(typeof a.body, typeof b.body, b.body);
type Enc = 'utf8' | 'hex';
function f(e: Enc | 'buffer' | null | undefined): string { return String(e); }
console.log(f('hex'), f(null), f(undefined));
`)
}

// A string-keyed dictionary: `in`, a member array's push, JSON of array
// values and a nested dictionary literal.
func TestDictionaryMembershipAndNestedValues(t *testing.T) {
	assertSameAsNode(t, `const d: { [k: string]: number } = { a: 1, b: 2 };
d.c = 3; d["d"] = 4; delete d.a;
console.log(d, Object.keys(d), "b" in d, "a" in d, d.zz, d["b"] + 1);
for (const k in d) console.log(k, d[k]);
const r: Record<string, string[]> = {};
r.x = ["1"]; r.x.push("2"); r["x"].push("3");
console.log(r, JSON.stringify(r), r.missing === undefined);
const nested: Record<string, Record<string, number>> = { g: { h: 1 } };
nested.g.i = 2; nested.j = {};
console.log(nested, JSON.stringify(nested), "g" in nested);
const o = { a: 1, b: 2 };
const k: string = "valueOf";
console.log("toString" in o, k in o, Object.hasOwn(o, "toString"), o.hasOwnProperty(k));
class C { x = 1; m() { return 1; } get g() { return 2; } static s() {} }
class D extends C { n() {} }
const c = new D();
for (const q of ["m", "g", "n", "s", "zz", "x", "a", "toString"]) console.log(q, q in c, q in o, q in d);
console.log(Object.hasOwn(d, k), d.hasOwnProperty("b"), Object.hasOwn(d, "toString"), "toString" in d);
const bag: any = { q: 1 };
console.log(Object.hasOwn(bag, k), "q" in bag, "toString" in bag);
const nul: any = Object.create(null);
const viaProto: any = Object.create(Object.prototype);
console.log("toString" in nul, "toString" in viaProto);
`)
}

// A dictionary type is the object itself: a view of a static object, of a
// bag held as any, or boxed back into any, writes through to the one
// object (and a member array mutates in place).
func TestDictionaryIsTheObject(t *testing.T) {
	assertSameAsNode(t, `const o = { a: 1, b: 2 };
const view: { [k: string]: number } = o;
view.a = 10; view.z = 26;
console.log(o, view, Object.keys(o));
const bag: any = { x: 1 };
const d2: Record<string, number> = bag;
d2.x = 5; d2.y = 6;
console.log(bag, d2, bag === d2);
const d: Record<string, number> = { p: 1 };
const x: any = d;
x.e = 5;
console.log(d.e, Object.keys(d).length, d);
const r: Record<string, string[]> = {};
r.x = ["1"]; r.x.push("2"); r["x"].push("3");
const a: any = {};
a.l = ["1"];
(a.l as string[]).push("2");
console.log(r, a, r.missing, r.missing === undefined);
interface Sparse { [i: number]: string }
const s: Sparse = {};
s[0] = "zero";
console.log(s[0], s[1], s);
`)
}

// Object.assign onto an array writes its indices (growing it) and returns
// the target; `.buffer` reads through a TypedArray | DataView union.
func TestObjectAssignArrayTargetAndViewUnionBuffer(t *testing.T) {
	assertSameAsNode(t, `function f(v: Uint8Array | DataView): number { return v.buffer.byteLength + v.byteLength + v.byteOffset; }
console.log(f(new Uint8Array(4)), f(new DataView(new ArrayBuffer(8), 2)));
const xs = [1, 2];
const c = Object.assign([], xs);
const t = [9, 9, 9];
const r = Object.assign(t, xs, { 2: 7 });
console.log(c, c.length, r, t, r === t, Object.assign(["a"], ["b", "c"]));
`)
}

// A computed-key literal is a dynamic object: mixed values, methods,
// getters, a spread, and held in any as itself.
func TestComputedKeyLiteralIsAnObject(t *testing.T) {
	assertSameAsNode(t, `const k: string = process.argv.length > 99 ? "z" : "go";
const q: any = { [k]: () => 1, name() { return 2; }, ["x" + 1]: 3 };
console.log(q[k](), q.name(), q.x1, Object.keys(q));
const p = { [k]: 5, plain: 6 };
const h: any = p;
h.extra = 1;
console.log(p, p[k], Object.keys(p));
const m = { [k]() { return 7; }, n() { return 8; } };
console.log(m[k](), m.n());
const o = { [k]: 1, b: "s", c: [1, 2] };
console.log(o, JSON.stringify(o), o.b.length);
const g = { [k]: 1, get gg() { return 2; } };
console.log(g.gg, g[k], Object.keys(g));
`)
}

// A destructured for-of binding shadows a module-level name of the same
// name (the module renamer bound only a plain loop variable).
func TestForOfPatternShadowsModuleName(t *testing.T) {
	assertSameAsNode(t, `const k = 'b'
const v = 0
const o2 = { a: 1, b: 2 }
for (const [k, v] of Object.entries(o2)) console.log(k + '=' + v)
for (const [k] of [['p']]) console.log(k)
for (const { k: [v] } of [{ k: ['q'] }]) console.log(v)
console.log(k, v)
`)
}

// Parameter defaults: a void default runs for its effects; a name only
// the body declares is not in scope (ReferenceError when read); a
// method's declared `this` type; `{}` holds any non-nullish value.
func TestParamDefaultEdgesAndEmptyObjectType(t *testing.T) {
	assertSameAsNodeCompatJS(t, `let n = 0;
function bar(): void { n++; }
function foo(a = bar()) { console.log(a, n); }
foo(); foo(undefined); foo(5 as any);
var top = 1;
function strange(x = top, y = b) { var b = ""; return y; }
try { strange(); } catch (e) { console.log((e as Error).name, (e as Error).message); }
console.log(strange(5, "given"));
const h: {} = { foo: 1 };
const k: {} = 5;
function f(o: {}) { return o; }
console.log(h, k, f([1]), f(true));
const fn1 = (options: { headers?: {} }) => { console.log(options); };
fn1({ headers: { foo: 1 } });
const fn2 = ({ headers = {} }) => { console.log(headers); };
fn2({ headers: { foo: 1 } }); fn2({});
`)
	assertSameAsNode(t, `class Example { getNumber(): number { return 1; } }
class Weird {
  m(this: Example) { return this.getNumber(); }
  d(this: Example, a = this.getNumber()) { return a; }
}
console.log(typeof Weird);
`)
}

// A typed local declared without a value holds undefined until assigned.
func TestUninitializedTypedLocalIsUndefined(t *testing.T) {
	assertSameAsNode(t, `function f(c: boolean) {
  let n: number;
  let s: string;
  let b: boolean;
  if (c) { n = 1; s = "x"; b = true; }
  const g = () => console.log(n, s, b, typeof n, typeof s);
  g();
}
f(false); f(true);
function loop(): number {
  let total: number;
  total = 0;
  for (let i = 0; i < 3; i++) total += i;
  let last: string;
  for (const w of ["a", "b"]) last = w;
  return total + last!.length;
}
console.log(loop());
`)
}

// A module-level typed binding without a value, or given one that may be
// undefined, holds undefined.
func TestModuleGlobalHoldsUndefined(t *testing.T) {
	assertSameAsNode(t, `let n: number;
let s: string;
function show() { console.log(n, s, typeof n); }
show();
n = 2; s = "y";
show();
n += 3;
console.log(n * 2);
const a: number[] = [1];
const q: number = a[4];
const r: number = a[0];
console.log(q, q === undefined, r + 1);
const t: string = ["x"][3];
console.log(t, t === undefined);
`)
}

// instanceof against a builtin on a union member or an any (BACKLOG 161):
// the box decides, and narrowing to the builtin reads its members.
func TestInstanceofBuiltinOnUnionAndAny(t *testing.T) {
	assertSameAsNode(t, `
function f(x: string | ReadableStream<Uint8Array>): string {
  if (x instanceof ReadableStream) return "stream";
  return "str:" + x;
}
console.log(f("a"), f(new ReadableStream<Uint8Array>()));
function g(x: string | Uint8Array): string { return x instanceof Uint8Array ? "bytes" + x.length : "s" + x.length; }
console.log(g("ab"), g(new Uint8Array(3)));
function h(x: number | Promise<number> | Map<string, number>): string {
  if (x instanceof Promise) return "p";
  if (x instanceof Map) return "m" + x.size;
  return "n" + x;
}
console.log(h(1), h(Promise.resolve(2)), h(new Map([["a", 1]])));
const anyv: any = new ReadableStream();
console.log(anyv instanceof ReadableStream, ({} as any) instanceof ReadableStream);
function hm(x: number | Map<string, number>): string {
  if (x instanceof Map) return "m" + x.size;
  return "n" + x;
}
console.log(hm(1), hm(new Map([["a", 1]])));
function hm2(x: number | Promise<number> | Map<string, number>): string {
  if (x instanceof Map) return "m" + x.size;
  return "o";
}
console.log(hm2(1), hm2(new Map([["a", 1]])));
const anyv2: any = new ReadableStream();
console.log(typeof anyv2);
let x2: any = new ReadableStream();
console.log(typeof x2);
const p: any = Promise.resolve(1);
console.log(typeof p, p instanceof Promise);
const a1: any = new Map([["k", 1]]);
const a2: any = new ReadableStream();
const a3: any = [1, 2];
const a4: any = new Date(0);
console.log(a1 instanceof Map, a2 instanceof ReadableStream, Array.isArray(a3), a4 instanceof Date, typeof a2);
function loc() { const b2: any = new ReadableStream(); return b2 instanceof ReadableStream; }
console.log(loc());
`)
}

// A typed local declared without a value reads as its type where every
// path has assigned it, and as undefined from a closure before that.
func TestDefinitelyAssignedLocalReadsItsType(t *testing.T) {
	assertSameAsNode(t, `
const hx = ["0", "1"];
for (var i1 = 0; i1 < 2; i1++) {
  try { var uu = hx[i1] + hx[1 - i1]; console.log(uu === "01", uu.length); } catch (e) {}
}
if (hx.length > 5) { var skipped = hx[0] + "!"; }
console.log(skipped);
{ var past = hx[9]; console.log(past); }
function argsOf(a: number) { const xs = arguments; return xs.length + arguments[0]; }
console.log(argsOf(4));
function f(limit: any): number {
  let n: number;
  try { n = Number(limit); } catch (e) { throw e; }
  const k = n === Infinity ? n : Math.trunc(n);
  return k + 1;
}
console.log(f("3.7"), f("x"));
let s: string;
if (Math.random() > 2) s = "a"; else s = "b";
console.log(s.toUpperCase(), s.length);
let late: number;
const read = () => late;
console.log(read());
late = 3;
console.log(read(), late);
let t: string;
const rt = () => t;
console.log(rt() === undefined);
t = "z";
console.log(t + "!", rt());
`)
}

// x[Symbol.iterator]() on builtin iterables, a string's iterator read
// through any, and an iterator over a Set typed from its initializer
// (BACKLOG 132, 154).
func TestBuiltinSymbolIterator(t *testing.T) {
	assertSameAsNode(t, `
const s: any = "héy";
console.log(typeof s[Symbol.iterator], [...s[Symbol.iterator]()]);
const arr = [1, 2, 3];
const it = arr[Symbol.iterator]();
console.log(it.next(), [...arr[Symbol.iterator]()]);
const m = new Map([["a", 1]]);
console.log([...m[Symbol.iterator]()]);
const str = "ab";
const si = str[Symbol.iterator]();
console.log(si.next(), [...si]);
const ta = new Uint8Array([7, 8]);
console.log([...ta[Symbol.iterator]()], [...new Set([4])[Symbol.iterator]()]);
console.log([...new Set([4]).values()]);
const s5 = new Set([5]);
console.log([...s5.values()], [...s5[Symbol.iterator]()]);
const sk = new Set([5]); const itk = sk.keys(); console.log(itk.next());
const anys: any[] = [1, "s", {}];
console.log(anys[Symbol.iterator]().reduce((m: any, v: any) => v, {}));
function red() {
  const r = anys[Symbol.iterator]().reduce((m: any, v: any) => v, {});
  console.log(r, arr[Symbol.iterator]().reduce((a, b) => a + b, 10));
}
red();
`)
}

// A generator takes rest and array parameters and reads `arguments`;
// yield* delegates to a builtin iterable (BACKLOG 153).
func TestGeneratorRestArrayParamsAndYieldStarIterable(t *testing.T) {
	assertSameAsNode(t, `
function* g(...xs: number[]) { for (const x of xs) yield x * 2; }
console.log([...g(1, 2, 3)], [...g()]);
function* h(p: string, ...rest: string[]) { yield p; yield String(rest.length); yield* rest; }
console.log([...h("a", "b", "c")]);
function* arrp(a: number[]) { a.push(9); yield a.length; }
const src = [1];
console.log([...arrp(src)], src);
const gv = g;
console.log([...gv(4, 5)], g.length, h.length);
const more = [7, 8];
console.log([...g(...more)]);
function* ga(a: number) { yield arguments.length; yield a; yield arguments[1]; }
console.log([...ga(5)]);
`)
}

// An extra argument is tsc's TS2554; the js lane takes it.
func TestGeneratorArgumentsExtraArgumentsJS(t *testing.T) {
	assertSameAsNodeCompatJS(t, `
function* ga(a) { yield arguments.length; yield a; yield arguments[1]; }
console.log([...ga(5)], [...ga(5, 6)]);
`)
}

// x.constructor of builtin-made values, static and through any (BACKLOG
// 126).
func TestConstructorOfBuiltinValues(t *testing.T) {
	assertSameAsNode(t, `
class A { x = 1; }
class B extends A {}
const b = new B();
console.log(b.constructor.name, b.constructor === B, b.constructor === A);
const o = { a: 1 };
console.log(o.constructor === Object, o.constructor.name);
const arr = [1];
console.log(arr.constructor === Array, arr.constructor.name);
const m = new Map();
console.log(m.constructor === Map, m.constructor.name);
console.log("s".constructor.name, (5).constructor.name, true.constructor.name);
const vals: any[] = [{ a: 1 }, [1], new Map(), "s", 5, true, new Set(), new Date(0), /x/, new B()];
for (const v of vals) console.log(v.constructor.name, v.constructor === Object, v.constructor === Array, v.constructor === Map);
`)
}

// A dictionary boxed into any is the same object (BACKLOG 127).
func TestDictionaryBoxedIntoAnyIsTheObject(t *testing.T) {
	assertSameAsNode(t, `
const d: Record<string, number> = { a: 1 };
const a: any = d;
a.b = 2;
a.a = 10;
console.log(d, a === d);
const m: { [k: string]: string } = {};
const am: any = m;
am.x = "y";
console.log(m.x, Object.keys(m));
`)
}

// A variable stored from a key or element read may be absent whatever the
// checker says, so `=== undefined` is tested at run time; a narrowed
// nullable array reads present (ADR-01509).
func TestIndexReadLocalAbsenceAndNarrowedArrays(t *testing.T) {
	assertSameAsNode(t, `
class C { v = 2; }
const objs: Record<string, { v: number }> = { a: { v: 1 } };
const cls: { [k: string]: C } = { a: new C() };
const list: C[] = [new C()];
function g(k: string, i: number): string {
  let o = objs[k];
  const r1 = objs[k] === undefined;
  const r2 = objs[k]?.v;
  if (o !== undefined) { o.v += 1; }
  const c = cls[k];
  const l = list[i];
  const m = c ?? new C();
  return [r1, r2, o === undefined ? "none" : o.v, m.v, l === undefined, l?.v, typeof c].join(" ");
}
console.log(g("a", 0));
console.log(g("zz", 5));
class K {
  #p: Buffer | null = Buffer.from([0, 0, 2]);
  a: number[] | undefined = [3, 4];
  get(): number {
    if (this.#p === null) throw new Error("x");
    const k = this.#p as Buffer;
    let i = 0;
    while (i < k.length - 1 && k[i] === 0) i++;
    return i;
  }
  sum(): string {
    if (!this.a) return "none";
    const b = this.a;
    b.push(5);
    return b + " " + b.length + " " + this.a.map((x) => x * 2);
  }
}
function g2(x: string[] | null) { if (x === null) return -1; const y = x; return y.length + x.join("").length; }
const k = new K();
console.log(k.get(), k.sum(), g2(null), g2(["ab", "c"]));
const n = new K(); n.a = undefined; console.log(n.sum(), String(n.a));
`)
}

// `a && b` / `a || b` over one scalar kind where an operand may be absent
// keeps one nullable slot, and a nullable right operand keeps its absence
// (ADR-01510).
func TestShortCircuitNullableScalars(t *testing.T) {
	assertSameAsNode(t, `
function h(b: boolean | undefined, c: boolean, nn: number | null, u: number | undefined, x: number) {
  const r = [b && c, c && b, b || c, c || b, nn && x, x && nn, nn || x, x || nn, nn && u, u || nn, x || u, u && x];
  return r.map((v) => String(v)).join(",");
}
console.log(h(undefined, true, null, undefined, 0));
console.log(h(false, true, 0, 2, 5));
console.log(h(true, false, 7, NaN, NaN));
console.log(h(true, true, null, 3, 1));
class E2 extends Error { code = "P"; }
type R = { ended: boolean } | null;
function f2(err: Error | null | undefined, r: R, n: number | undefined, m: number) {
  const a = err && (err as E2).code === "P" && r !== null && r.ended;
  const b = n && m;
  const c = m && n;
  const d = n || m;
  const e = m || n;
  const g = (err && true) || false;
  const h = err && r !== null;
  return [a, b, c, d, e, g, h].map((v) => String(v) + ":" + typeof v).join(" ");
}
const r1 = { ended: true };
console.log(f2(new E2("x"), r1, 3, 4));
console.log(f2(null, r1, undefined, 0));
console.log(f2(undefined, null, 0, 5));
console.log(f2(new Error("y"), { ended: false }, NaN, 7));
`)
}

// `User[] | null` (a named interface or enum element) is a possibly-absent
// array, as `number[] | null` is (ADR-01514).
func TestNamedElementArrayNullableAnnotation(t *testing.T) {
	assertSameAsNode(t, `
interface H { name: string; value: number }
function f(c: H[] | null, d: H[] | undefined): string {
  if (c === null) return "none";
  return c.map((h) => h.name + h.value).join(",") + " " + (d === undefined ? -1 : d.length);
}
console.log(f(null, undefined), f([{ name: "x", value: 1 }], []), f([], undefined));
let g: H[] | null = null;
console.log(g === null, String(g));
g = [{ name: "q", value: 2 }];
console.log(g !== null && g[0].name);
`)
	assertOutput(t, `
enum E { A, B }
function n(d: E[] | undefined): number { return d === undefined ? -1 : d.length; }
console.log(n(undefined), n([E.B, E.A]));
`, "-1 2")
}

// A narrowing another function may undo (a call TypeScript's flow does
// not see) reads the variable or property as it is at run time
// (ADR-01519).
func TestStaleNarrowingAfterCall(t *testing.T) {
	assertSameAsNode(t, `
let o: { a: number } | undefined;
function reset() { o = undefined; }
o = { a: 1 };
reset();
console.log(o);
console.log(o === undefined);
`)
	assertSameAsNode(t, `
class Box { v: { a: number } | undefined = { a: 1 }; clear() { this.v = undefined; } }
const b = new Box();
if (b.v) {
  b.clear();
  console.log(b.v);
}
let arr: number[] | null = [1];
const kill = () => { arr = null; };
if (arr) { kill(); console.log(arr); }
`)
	assertSameAsNode(t, `
let n: number | undefined;
let s: string | undefined;
function reset() { n = undefined; s = undefined; }
n = 1; s = "x";
reset();
console.log(n, s, n === undefined);
class K { n: number | undefined = 1; s: string | undefined = "y"; clear() { this.n = undefined; this.s = undefined; } }
const o = new K();
if (o.n !== undefined && o.s !== undefined) { o.clear(); console.log(o.n, o.s); }
`)
	assertSameAsNode(t, `
let n: number | null = 1;
function z() { n = null; }
if (n === null) throw new Error("x");
z();
console.log(n);
let m: number | null = 2;
if (m !== null) { z(); console.log(m + 1); }
`)
}

// … an `any` or a union of scalars too: its box answers (ADR-01519).
func TestStaleUnionNarrowingAfterCall(t *testing.T) {
	assertSameAsNode(t, `
let v: any = 1;
function flip() { v = "s"; }
flip();
if (typeof v === "string") { console.log(v.toUpperCase(), v.length); }
let u: number | string = Math.random() > 2 ? 1 : "x";
function fl3() { u = "q"; }
if (typeof u === "string") { fl3(); console.log(u.toUpperCase(), u.length); }
if (typeof u === "number") { console.log(u.toFixed(1)); } else { console.log(u.charAt(0)); }
class C { x = 1; }
let w: any = new C();
function fl2() { w = 5; }
if (w instanceof C) { fl2(); console.log(w); }
`)
}

// A module binding typed by an `as` widening to a union, read in a
// function, is a global (ADR-01520).
func TestAsUnionModuleBindingInFunction(t *testing.T) {
	assertSameAsNode(t, `
let u = 0 as number | string;
function start() { u = "str"; }
start();
console.log(u);
`)
}
