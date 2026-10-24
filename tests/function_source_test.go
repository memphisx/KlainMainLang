package tests

import "testing"

// Function.prototype.toString gives a compiled function's source text, its
// types blanked as Node's type stripping leaves them.
func TestE2EFunctionSourceText(t *testing.T) {
	assertSameAsNode(t, `async function af(x: number): Promise<number> { return x; }
function* gen(n: number): Generator<number> { yield n; }
function generic<T extends object>(v: T, k?: keyof T): T { return v as T; }
const fe = function named(a: string = "x") { return a!; };
const anon = function (b: number[]) { return b.length; };
const obj = { m(x: number) { return x; }, get g(): number { return 1; }, async am() {} };
function outer() {
  function inner(p: number): string { return String(p); }
  return inner;
}
const mk = () => new Map<string, number>();
const show = (f: unknown) => console.log(String(f));
for (const f of [af, gen, generic, fe, anon, obj.m, outer(), mk, (async (q: number) => q)]) show(f);
show(Object.getOwnPropertyDescriptor(obj, "g")!.get);
show(obj.am);
console.log(`+"`${(x: number) => x * 2}`"+`);
`)
}

func TestE2EClassSourceText(t *testing.T) {
	assertSameAsNode(t, `class K<T> {
  private readonly items: T[] = [];
  label?: string;
  constructor(label?: string) { this.label = label; }
  add(item: T): this { this.items.push(item); return this; }
  get size(): number { return this.items.length; }
}
class S2 { static count: number = 0; declare x: number; protected y!: string; opt? = 1; }
abstract class A { abstract run(): void; protected abstract go?(): void; }
const show = (f: unknown) => console.log(String(f));
show(K);
show(K.prototype.add);
show(Object.getOwnPropertyDescriptor(K.prototype, "size")!.get);
show(new K<number>().add);
show(S2);
show(A);
console.log(new K<number>("a").add(1).size);
`)
}

// A function unboxed from any into a typed slot stays that function.
func TestE2EFunctionIdentityUnboxedFromAny(t *testing.T) {
	assertSameAsNode(t, `import { promisify } from "node:util";
const anyF: any = (a: number) => a + 1;
const typed: (a: number) => number = anyF;
const again: (a: number) => number = anyF;
console.log(typed === anyF, typed === again, typed !== again);
const p = (cb: (e: Error | null, v?: number) => void) => cb(null, 1);
const pp = promisify(p);
console.log(promisify(pp) === pp);
`)
}

// An object literal's accessors are its own enumerable properties.
func TestE2EObjectLiteralOwnAccessors(t *testing.T) {
	assertSameAsNode(t, `let store = 0;
const o = { a: 1, get g(): number { return 1; }, b: 2, set s(v: number) { store = v; }, get gs(): number { return store; }, set gs(v: number) { store = v * 2; } };
console.log(Object.keys(o), JSON.stringify(o));
console.log(o);
console.log(Object.getOwnPropertyDescriptor(o, "g"));
console.log(Object.getOwnPropertyDescriptor(o, "s"));
console.log(Object.getOwnPropertyDescriptor(o, "gs"));
console.log({ ...o }, Object.entries(o), Object.values(o), "s" in o, Object.hasOwn(o, "gs"));
for (const k in o) console.log(k);
o.gs = 5;
console.log(o.gs, store);
class C { get z(): number { return 2; } }
console.log(Object.getOwnPropertyDescriptor(C.prototype, "z"), Object.keys(new C()));
`)
}

// Interfaces and type aliases declared in a function body, generic
// functions and classes used as values.
func TestE2ELocalTypesAndGenericValues(t *testing.T) {
	assertSameAsNode(t, `function outer() {
  interface Local { a: number }
  type L2 = string;
  function inner(p: Local): L2 { return String(p.a); }
  {
    interface Local { b: string }
    const v: Local = { b: "inner" };
    console.log(v.b);
  }
  return inner({ a: 7 });
}
console.log(outer());
function id<T>(v: T): T { return v; }
const f = id;
console.log(typeof f, f.name, f.length, f(3));
class Box<T> { v: T; constructor(v: T) { this.v = v; } get(): T { return this.v; } }
const B = Box;
console.log(typeof B, B.name, new Box<number>(4).get());
`)
}

func TestE2EFunctionToStringForms(t *testing.T) {
	assertSameAsNode(t, `function add(a: number, b: number) { return a + b; }
const arrow = (s: string): string => s.trim();
class Point { x = 1 }
class Sub extends Point { y: number = 2 }
console.log(add.toString());
console.log(arrow.toString());
console.log(Point.toString());
console.log(Point, Sub, [Point], { k: Sub });
console.log(`+"`${Sub}`"+`);
const f: Function = add;
console.log(f.toString() === String(add), (add as any).toString());
console.log(parseInt.toString(), Math.max.toString(), String(Map));
`)
}

// An accessor object, or a class instance, behind a structural type:
// read and written through its accessors.
func TestE2EAccessorsThroughStructuralType(t *testing.T) {
	assertSameAsNode(t, `const o = { y: 2, get x(): number { return 1; } };
const p: { x: number } = o;
console.log(p.x, p);
function f(q: { x: number; y: number }) { return q.x + q.y; }
console.log(f(o));
interface HasX { x: number; y: number }
const arr: HasX[] = [o, { x: 5, y: 6 }];
console.log(arr.map(h => h.x + h.y), JSON.stringify(arr), Object.keys(p));
let store = 0;
const w = { get v(): number { return store; }, set v(n: number) { store = n; } };
const tgt: { v: number } = w;
tgt.v = 9;
console.log(store, tgt.v, w.v);
let store2 = 0;
class W { get v(): number { return store2; } set v(n: number) { store2 = n; } }
const tgt2: { v: number } = new W();
tgt2.v = 9;
console.log(store2, tgt2.v);
class D { v = 1 }
const t2: { v: number } = new D();
t2.v = 4;
console.log(t2.v);
`)
}

// A generic class with generator methods and static members; a method
// returning this chained on a fresh instantiation.
func TestE2EGenericClassGeneratorsAndStatics(t *testing.T) {
	assertSameAsNode(t, `class Bag<T> implements Iterable<T> {
  private items: T[] = [];
  add(x: T): this { this.items.push(x); return this; }
  *[Symbol.iterator](): Iterator<T> { yield* this.items; }
  *pairs(): Generator<[number, T]> { let i = 0; for (const x of this.items) yield [i++, x]; }
  async *slow(): AsyncGenerator<T> { for (const x of this.items) yield x; }
}
const b = new Bag<string>().add("a").add("b");
console.log([...b], Array.from(b));
for (const [i, x] of b.pairs()) console.log(i, x);
const n = new Bag<number>().add(1).add(2);
let sum = 0;
for (const v of n) sum += v;
console.log(sum);
(async () => { for await (const v of n.slow()) console.log("slow", v); })();
class C2<T> {
  static n = 1;
  static label: string;
  static { C2.label = "set in block"; }
  static make<U>(v: U): C2<U> { C2.n++; return new C2<U>(v); }
  static get twice(): number { return C2.n * 2; }
  v: T;
  constructor(v: T) { this.v = v; }
}
console.log(C2.n, C2.make(3).v, C2.make("s").v, C2.n, C2.twice, C2.label);
C2.n += 10;
console.log(C2.n);
`)
}

// A generic function read as a value inside its own body; an
// instantiation is the same wherever it is first made.
func TestE2EGenericFunctionValueInOwnBody(t *testing.T) {
	assertSameAsNode(t, `interface Box<T> {
  v: T;
  map<U>(f: (x: T) => U): Box<U>;
}
function box<T>(v: T): Box<T> {
  console.log(typeof box, box.name);
  return { v: v, map: <U>(f: (x: T) => U): Box<U> => box(f(v)) };
}
const b: Box<number> = box(2);
const c = b.map((x) => x + 1);
console.log(c.v, c.map((x) => x * 10).v);
`)
}

// Function.prototype: a function whose toString, call, apply and bind act
// on their receiver; a function value reads them unbound.
func TestE2EFunctionPrototypeValue(t *testing.T) {
	assertSameAsNode(t, `function f(a: number) { return a; }
console.log(Function.prototype.toString.call(f));
console.log(typeof Function.prototype, Object.getPrototypeOf(f) === Function.prototype);
console.log(Function.prototype.call.call(f, null, 4));
class K { m() { return 1; } }
const cv: any = K;
console.log(typeof cv.call, typeof cv.bind, String(Function.prototype), Function.prototype.name === "");
const bound = Function.prototype.bind.call(f, null, 9);
console.log(bound(), bound.name, bound.length, f.call(null, 3), f.apply(null, [8]));
try { Function.prototype.toString.call({} as any); } catch (e) { console.log((e as Error).message); }
console.log(Object.getPrototypeOf(Function.prototype.call) === Function.prototype);
class A2 {}
class B2 extends A2 {}
const a: any = A2, b: any = B2;
console.log(Object.getPrototypeOf(a) === Function.prototype, Object.getPrototypeOf(b) === A2, Object.getPrototypeOf(Map) === Function.prototype);
console.log(a.toString === Function.prototype.toString, ((x: number) => x).call(null, 2));
function f3(a: number) { return a; }
function g3(b: string) { return b; }
const ts3: any = (g3 as any).toString;
console.log(ts3.call(f3));
const c3: any = (g3 as any).call;
console.log(c3.call(f3, null, 5));
const bd3: any = (g3 as any).bind;
console.log(bd3.call(f3, null)(6));
const ap3: any = (g3 as any).apply;
console.log(ap3.call(f3, null, [7]));
`)
}
