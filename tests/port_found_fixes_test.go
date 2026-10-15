package tests

import "testing"

// Compiler fixes found by porting URLPattern and Web Crypto (ADR-01341).
// Outputs are checked against the local Node.

func TestE2ENeverCallEndsTheFlow(t *testing.T) {
	assertSameAsNode(t, `
function fail(): never { throw new Error("x"); }
function f(s: string): string {
  let r: string;
  try { r = s; } catch (e) { fail(); }
  return r;
}
function g(s: string): string {
  try { return s; } catch (e) { return fail(); }
}
console.log(f("ok"), g("ok2"));
`)
}

func TestE2EConstructorRestAndSpreadInNew(t *testing.T) {
	assertSameAsNode(t, `
class A { constructor(...args: any[]) { console.log(args.length, JSON.stringify(args)); } }
new A(1, 2); new A(); new A(...[3, 4, 5]); new A(1, undefined, undefined);
class B { n: number; constructor(first: string, ...rest: number[]) { this.n = rest.reduce((a, b) => a + b, 0); console.log(first, rest.length); } }
console.log(new B("x", 1, 2, 3).n, new B("y").n);
class C extends B { constructor(...a: number[]) { super("c", ...a); } }
console.log(new C(4, 5).n);
function f(...a: any[]): number { return a.length; }
const nums = [1, 2, 3];
console.log(f(...nums), f(0, ...nums), f(...["x"], ...nums));
`)
}

func TestE2ETernaryIntoADictionary(t *testing.T) {
	assertSameAsNodeImports(t, `
type Init = { [k: string]: string | undefined };
function read(): Init { const i: Init = {}; i["a"] = "x"; return i; }
function m(cond: boolean): number {
  const init: Init = cond ? read() : {};
  return Object.keys(init).length;
}
console.log(m(true), m(false));
`)
}

func TestE2EJSONStringifyDictionariesMapsAndSets(t *testing.T) {
	assertSameAsNode(t, `
type Init = { [k: string]: string | undefined };
const o: Init = {}; o["a"] = "x"; o["b"] = undefined; o["c"] = "z";
console.log(JSON.stringify(o));
console.log(JSON.stringify(o, null, 2));
console.log(JSON.stringify({ outer: o }), JSON.stringify([o]));
const m = new Map<string, number>([["a", 1]]);
console.log(JSON.stringify(m), JSON.stringify({ m, s: new Set([1]) }));
`)
}

func TestE2EModuleBindingsReadByFunctions(t *testing.T) {
	assertSameAsNode(t, `
const P = (1n << 255n) - 19n;
function f(a: bigint): bigint { return a % P; }
const c: any = { subtle: { n: 1 } };
const s = c.subtle;
async function g() { return s.n; }
const enc = (x: string): number => x.length;
function use(): number { return enc("abc"); }
console.log(f(5n), use());
g().then((v) => console.log(v));
`)
}

func TestE2EAsyncResultsAndAdoption(t *testing.T) {
	assertSameAsNode(t, `
const o: any = { go(): Promise<any> { return Promise.resolve(5); } };
async function f1(): Promise<any> { return Promise.resolve(5); }
async function f2(): Promise<number> { return Promise.resolve(6); }
async function f3(): Promise<any> { await 0; return o.go(); }
async function a(): Promise<number> { return 1; }
async function runAll() {
  console.log(await f1(), await f2(), await f3());
  const r = await (async () => [await a(), await a()])();
  console.log(r);
  const p: Promise<any> = Promise.resolve(41);
  p.then((n: number) => console.log(n + 1));
}
runAll();
`)
}

func TestE2ELoopCapturedParameterOfATask(t *testing.T) {
	assertSameAsNode(t, `
async function run(p: number): Promise<string> {
  const out: number[] = [];
  for (let i = 0; i < 2; i++) { await Promise.resolve(0); out.push(((x: number) => p + x)(i)); }
  const g = () => p * 10;
  return out.join(",") + " " + g();
}
run(3).then(s => console.log(s));
`)
}

func TestE2ETypedArrayElementBindingIsANumber(t *testing.T) {
	assertSameAsNode(t, `
const b = new Uint8Array([0x30, 0x82, 0x01, 0x22]);
let len = b[1];
len = len * 256 + b[2];
function f() { const c = new Uint8Array([0, 130, 1]); let n = c[1]; n = n * 256 + c[2]; return n; }
const i = new Int8Array([127]); let y = i[0]; y++;
console.log(len, f(), y);
`)
}

func TestE2EGenericFromCallbackReturnAndTupleAssignment(t *testing.T) {
	assertSameAsNode(t, `
function job<T>(fn: () => T): T { return fn(); }
console.log(job(() => { const x = 5; return x; }), job(() => "s"));
function decode(b: Uint8Array | null): [number, number] | null {
  if (b === null) return null;
  return [b[0], b[1]];
}
function g(pub: Uint8Array | null, d: number): number | null {
  let pt: [number, number] | null = null;
  if (pub !== null) { pt = decode(pub); if (pt === null) return null; } else { pt = [d, d]; }
  return pt[0] + pt[1];
}
console.log(g(new Uint8Array([1, 2]), 5), g(null, 5));
const flag = process.argv.length > 5;
const h = () => { if (flag) return false; return new ArrayBuffer(4); };
const q: any = h(); console.log(q.byteLength);
`)
}

func TestE2EErrorsDOMExceptionAndDefineProperty(t *testing.T) {
	assertSameAsNode(t, `
const e = new DOMException("boom", { name: "OperationError", cause: new Error("x") });
console.log(e.name, e.message, (e as any).cause?.message);
const d: any = new DOMException("m", "DataError");
console.log(d.constructor.name, d instanceof DOMException);
class MyErr extends Error {}
const m: any = new MyErr("x"); console.log(m.constructor.name);
const r: any = new Error("m"); r.name = "Custom"; console.log(r.constructor.name);
const er: any = new Error("m");
Object.defineProperty(er, "cause", { value: 1, writable: true, configurable: true, enumerable: false });
console.log(er.cause);
`)
}

func TestE2ETextDecoderAndBase64url(t *testing.T) {
	assertSameAsNode(t, `
const d = new TextDecoder();
console.log(d.decode(new Uint8Array([0xEF, 0xBB, 0xBF, 0x68, 0x69])), JSON.stringify(d.decode(new Uint8Array([0x61, 0xFF, 0x62]))), d.decode(new Uint16Array([0x6968])));
const anyv: any = new Uint8Array([111, 107]); console.log(d.decode(anyv));
for (const n of [0, 1, 2, 3, 31]) console.log(Buffer.from(new Uint8Array(n).fill(250)).toString("base64url"));
`)
}

func TestE2EInspectTypedArrayPastDepthAndCustomDepth(t *testing.T) {
	assertSameAsNode(t, `
console.log({ a: { b: { c: new Uint8Array(2), d: Buffer.from("x"), e: new Float64Array(1), f: [1], g: new Int32Array(0) } } });
class K { [Symbol.for("nodejs.util.inspect.custom")](depth: number, options: any): string { return "K " + JSON.stringify({ depth, optDepth: options.depth }); } }
console.log(new K());
console.log([new K()]);
`)
}

func TestE2EEventsDefaultExportNamesItself(t *testing.T) {
	assertSameAsNodeImports(t, `
import events from "events";
import { EventEmitter } from "events";
console.log(typeof events.EventEmitter, events.EventEmitter === EventEmitter);
const E = events.EventEmitter; const e = new E(); console.log(typeof e.on);
`)
}

// An async function's returned promise is adopted when the function
// settles (PromiseResolve), not awaited at the `return`: an enclosing try
// does not see its rejection, a finally runs first, and each settles on
// Node's tick.
func TestE2EAsyncReturnedPromiseIsAdopted(t *testing.T) {
	assertSameAsNode(t, `
let t = 0;
function tick(): void { if (t < 12) { t++; Promise.resolve().then(tick); } }
tick();
const rej: Promise<string> = Promise.reject(new Error("boom"));
rej.catch(() => {});
const ok: Promise<string> = Promise.resolve("v");
async function r1(): Promise<string> { try { return rej; } catch (e) { return "caught"; } }
async function r2(): Promise<string> { return ok; }
async function r3(): Promise<string> { await 0; return ok; }
async function r4(): Promise<string> { await 0; try { return rej; } catch (e) { return "caught"; } }
async function r5(): Promise<string> { try { return rej; } finally { console.log("fin", t); } }
async function r6(): Promise<number> { try { return Promise.resolve(1); } finally { return 2; } }
async function r7(): Promise<string> { return "plain"; }
r1().then((v) => console.log("r1 ok", v, t), (e: any) => console.log("r1 rejected", e.message, t));
r2().then((v) => console.log("r2", v, t));
r3().then((v) => console.log("r3", v, t));
r4().then((v) => console.log("r4 ok", v, t), (e: any) => console.log("r4 rejected", e.message, t));
r5().then((v) => console.log("r5 ok", v), (e: any) => console.log("r5 rejected", e.message, t));
r6().then((v) => console.log("r6", v, t));
r7().then((v) => console.log("r7", v, t));
`)
}

func TestE2EAsyncReturnedPromiseIsAdoptedJS(t *testing.T) {
	assertSameAsNodeCompatJS(t, `
let t = 0;
function tick() { if (t < 12) { t++; Promise.resolve().then(tick); } }
tick();
const anyv = JSON.parse("5");
const rej = Promise.reject(new Error("boom"));
rej.catch(() => {});
async function r1() { try { return rej; } catch (e) { return "caught"; } }
async function r2() { return anyv; }
async function r3() { return 5; }
r1().then((v) => console.log("r1 ok", v), (e) => console.log("r1 rejected", e.message, t));
r2().then(() => console.log("r2 at", t));
r3().then(() => console.log("r3 at", t));
async function g2(n) { if (n <= 0) return 0; const r = await g2(n - 1); return r + n; }
async function k2(n) { if (n <= 0) return 0; return (await k2(n - 1)) + n; }
g2(3).then((v) => console.log("g", v));
k2(2).then((v) => console.log("k", v));
`)
}

// A reaction on a promise of void is called with undefined (an `any`
// parameter compares === undefined; a rest holds one undefined).
func TestE2EThenOnVoidPromisePassesUndefined(t *testing.T) {
	assertSameAsNode(t, `
function d(error?: any): void { console.log(error !== undefined, error === undefined, error == null, typeof error); }
function check(): void {}
Promise.resolve(1).then(check).then(d);
const v: any = undefined; d(v); d(); d(undefined);
Promise.resolve(1).then(check).then((x: any) => console.log(x === undefined, typeof x));
Promise.resolve(1).then(check).then(function (x?: number) { console.log(x === undefined, x); });
Promise.resolve(1).then(check).then((...a: any[]) => console.log(a.length));
`)
}

// A module-level [] pushed from inside a function declaration stores what
// is pushed (strings, not NaN).
func TestE2EModuleArrayPushedFromFunction(t *testing.T) {
	assertSameAsNodeCompatJS(t, `
var actual = [];
async function p(v) { actual.push("A: " + v); }
async function c() { await p(1); await p(2); }
c();
new Promise(function (r) { actual.push("P1"); r(); }).then(function () { actual.push("P2"); }).then(() => console.log(actual));
`)
}

// A plain function's `this` is undefined when it is called bare, and a
// function returning it returns that.
func TestE2EPlainFunctionReturnsThis(t *testing.T) {
	assertSameAsNodeCompatJS(t, `
function f() { "use strict"; return this;};
function foo() { return f();}
console.log(foo());
`)
}

// An `any` holding a BigInt steps by 1n under `++`/`--`, on a binding, a
// property and an element; a string still steps numerically.
func TestE2EAnyBigIntUpdate(t *testing.T) {
	assertSameAsNode(t, `
let x: any = 1n;
console.log(x--, x);
const o: any = { v: "4", n: 1n };
console.log(o.v++, o.v, ++o.v, o.n--, --o.n, o.n);
const a: any[] = [1, 2n];
a[0]++; ++a[1];
console.log(a);
`)
}

// A -compat=js `var` redeclared with a BigInt and then an object.
func TestE2EAnyBigIntUpdateRedeclaredVarJS(t *testing.T) {
	assertSameAsNodeCompatJS(t, `
var x = 1n;
console.log(x--, x);
var x = {y: 1n};
console.log(x.y--, x.y);
var x = [null, [null, null, 1n]];
console.log(x[1][2]--, x[1][2]);
`)
}

// An untyped `[]` pushed from an accessor of a returned object literal
// takes the pushed type (the pre-pass walks return values).
func TestE2EArrayPushedFromReturnedAccessorJS(t *testing.T) {
	assertSameAsNodeCompatJS(t, `
var log = [];
function s1() { return { get p() { log.push("g"); return 1; } }; }
s1().p;
console.log(log);
`)
}

// An unannotated rest parameter collects any values (any[]), on a
// declaration, an arrow and a function expression.
func TestE2EUntypedRestCollectsAnyJS(t *testing.T) {
	assertSameAsNodeCompatJS(t, `
var af = (...x) => { return x.length; };
function f(...a) { return a.join("-"); }
var fe = function (...b) { return b[0]; };
console.log(af(1, 1, 1), f("a", 2, true), fe("z"), af());
`)
}

// A class method reading `arguments` takes the arguments passed, called
// directly, by key, or as a value (a getter returning it, a private one
// included); a method with a rest parameter read as a value is a function.
func TestE2EMethodArgumentsAndValuesJS(t *testing.T) {
	assertSameAsNodeCompatJS(t, `
class C {
  m() { console.log(arguments.length, arguments[0]); }
  r(...xs) { console.log(xs.length, xs[1]); }
  #p() { console.log("p", arguments.length, arguments[1]); }
  get g() { return this.m; }
  get q() { return this.#p; }
}
var c = new C();
c.m(7);
c["m"](8, 9);
c.g(1, 2);
c.q(42, "TC39");
var f = c.r; f.call(c, "a", "b");
console.log(typeof f);
`)
}

// A private method read as a value, in the strict lane.
func TestE2EPrivateMethodValue(t *testing.T) {
	assertSameAsNode(t, `
class C {
  #m() { console.log("hi"); }
  get g() { return this.#m; }
  pub() { return this.#m; }
}
new C().g();
const f = new C().pub();
f.call(new C());
console.log(typeof f);
`)
}

// An AggregateError's errors are any[]: numbers, strings and errors keep
// their values, read typed, through any, caught, from Promise.any, and
// printed.
func TestE2EAggregateErrorErrorsAny(t *testing.T) {
	assertSameAsNode(t, `
const e = new AggregateError([1, "s", new TypeError("in")], "m");
console.log(e.errors.length, e.errors[0], e.errors[1], e.errors[2].message);
console.log(e.errors.slice(0, 2));
const a: any[] = [2, "x"];
console.log(new AggregateError(a).errors);
try { throw new AggregateError([3, "q"], "z"); } catch (c) { console.log((c as AggregateError).errors); }
Promise.any([Promise.reject(1), Promise.reject("r")]).catch((x: any) => {
  console.log(x.errors, x.errors.length, x.message);
});
`)
}

// A typed array over a SharedArrayBuffer held in a variable.
func TestE2ESharedArrayBufferViewOfVariable(t *testing.T) {
	assertSameAsNode(t, `
const sab = new SharedArrayBuffer(16);
const v = new Int32Array(sab);
v[1] = 7;
const b = new BigInt64Array(sab);
console.log(v.length, v[1], b.length, new Int32Array(sab)[1]);
`)
}

// A union with a Promise member, annotated or inferred from a mixed array
// literal: typeof narrows to the promise ("object"), which then settles.
func TestE2EPromiseUnionMemberNarrowing(t *testing.T) {
	assertSameAsNode(t, `
function f(z: Promise<number> | string | number) {
  if (typeof z === "string") console.log("s", z);
  else if (typeof z === "number") console.log("n", z);
  else z.then((v) => console.log("p", v));
}
f(Promise.resolve(4)); f("x"); f(2);
const a = [Promise.resolve(1), "s", 3];
for (const x of a) {
  if (typeof x === "string") console.log("s", x);
  else if (typeof x === "number") console.log("n", x);
  else x.then((v) => console.log("p", v));
}
const e = new Error("x");
const it = [Promise.reject(e), "u"];
console.log(typeof it[0], typeof it[1], it.length);
(it[0] as Promise<never>).catch((r: Error) => console.log("caught", r.message));
`)
}
