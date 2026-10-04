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
	assertSameAsNode(t, `
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
