package tests

import "testing"

// Compiler fixes found by porting Node's `stream` module to TypeScript;
// each program's expected output taken from Node v24.

// A closure calls a `const` closure declared after it: mutual recursion in a function body and at top level, and an object-literal method calling a later arrow.
func TestE2EClosureCallsClosureDeclaredLater(t *testing.T) {
	assertOutputImports(t, `
function f(n: number): void {
  let count = 0;
  const a = (k: number): void => { count++; if (k > 0) b(k - 1); else console.log('done', count); };
  const b = (k: number): void => { count++; a(k); };
  a(n);
}
f(3);
const even = (n: number): boolean => n === 0 ? true : odd(n - 1);
const odd = (n: number): boolean => n === 0 ? false : even(n - 1);
console.log(even(10), odd(7));
function g(flag: boolean) {
  const o = { run() { return later(2); } };
  const later = (x: number) => x * 10;
  if (flag) console.log(o.run());
}
g(true);
`, "done 7\ntrue true\n20\n")
}

// A closure that calls one declared later, run before that declaration: a ReferenceError, as the temporal dead zone.
func TestE2EClosureCalledBeforeLaterDeclarationThrows(t *testing.T) {
	assertOutputImports(t, `
function f(): void {
  const a = (): number => b() + 1;
  try {
    console.log(a());
  } catch (e) {
    console.log((e as Error).name, (e as Error).message);
  }
  const b = (): number => 41;
  console.log(a());
}
f();
`, "ReferenceError Cannot access 'b' before initialization\n42\n")
}

// An object typed by one interface passed, returned, assigned and stored where another interface with the same properties in another order is expected.
func TestE2EObjectOfAnotherInterfaceLayout(t *testing.T) {
	assertOutputImports(t, `
interface A { hwm?: number; objectMode?: boolean; tag?: string }
interface B { objectMode?: boolean; tag?: string; extra?: number; hwm?: number }
function useB(b?: B): void { console.log(b?.objectMode ?? 'none', b?.tag, b?.hwm, b?.extra); }
class S { constructor(o?: B) { console.log('S', o?.tag, o?.objectMode); } }
function useA(a?: A): void { useB(a); new S(a); const bb: B | undefined = a; console.log('bb', bb?.hwm); }
useA({ hwm: 5, tag: 'x' });
useA({ objectMode: true });
useA();
function toB(a: A): B { return a; }
console.log(toB({ hwm: 3, tag: 't' }).hwm);
let b: B = {};
const a: A = { hwm: 9 };
b = a;
console.log(b.hwm, b.tag);
class H { opts: B = {}; set(x: A) { this.opts = x; } }
const h = new H(); h.set({ tag: 'q', hwm: 1 }); console.log(h.opts.tag, h.opts.hwm);
interface Box { v: number }
function show(x: Box) { console.log(x.v); }
const o = { w: 1, v: 7 };
show(o);
`, "none x 5 undefined\nS x undefined\nbb 5\ntrue undefined undefined undefined\nS undefined true\nbb undefined\nnone undefined undefined undefined\nS undefined undefined\nbb undefined\n3\n9 undefined\nq 1\n7\n")
}

// `typeof o === "function"` narrows an object-or-function union; an object literal and an arrow each box as their member.
func TestE2ETypeofFunctionNarrowsUnion(t *testing.T) {
	assertOutputImports(t, `
interface O { readable?: boolean; writable?: boolean }
function f(o: O | ((n: number) => void)): void {
  if (typeof o === 'function') { o(1); }
  else console.log(o.readable, o.writable);
}
function h(o: O | ((n: number) => void)): void {
  if (typeof o === 'function') { o(2); return; }
  console.log('h', o.readable);
}
function g(r: boolean, w: boolean) {
  f({ readable: r, writable: w });
  f({});
  f((n) => { console.log('called', n); });
  h({ readable: true });
  h((n) => console.log('hfn', n));
}
g(true, false);
`, "true false\nundefined undefined\ncalled 1\nh true\nhfn 2\n")
}

// addListener, prependListener and prependOnceListener.
func TestE2EEventEmitterPrependAndAddListener(t *testing.T) {
	assertOutputImports(t, `
import { EventEmitter } from 'events';
const e = new EventEmitter();
e.on('x', () => console.log('on'));
e.prependListener('x', () => console.log('prepended'));
e.prependOnceListener('x', () => console.log('prepended once'));
e.addListener('x', () => console.log('added'));
e.emit('x');
e.emit('x');
console.log(e.listenerCount('x'));
`, "prepended once\nprepended\non\nadded\nprepended\non\nadded\n3\n")
}

// A subclass redeclares a field it inherits, with the same type.
func TestE2ESubclassRedeclaresInheritedField(t *testing.T) {
	assertOutputImports(t, `
class Base { code: string = 'base'; n: number = 1; }
class Derived extends Base { code: string; constructor() { super(); this.code = 'derived'; } }
class CodedError extends Error { code: string; constructor(c: string) { super('failed ' + c); this.code = c; } }
const d = new Derived();
console.log(d.code, d.n);
const e = new CodedError('E1');
console.log(e.code, e.message);
`, "derived 1\nE1 failed E1\n")
}

// A Buffer held in `any` pushed into a `Buffer[]`, and an array held in `any` into a `number[][]`.
func TestE2EAnyPushedIntoBufferArray(t *testing.T) {
	assertOutputImports(t, `
const buf: any[] = [Buffer.from('ab'), Buffer.from('cd')];
const parts: Buffer[] = [];
for (const c of buf) parts.push(c);
console.log(Buffer.concat(parts).toString());
const nums: number[][] = [];
const src: any[] = [[1, 2], [3]];
for (const c of src) nums.push(c);
console.log(nums);
`, "abcd\n[ [ 1, 2 ], [ 3 ] ]\n")
}

// A method parameter captured by a closure in one branch and read in the other.
func TestE2EMethodParameterCapturedInOneBranch(t *testing.T) {
	assertOutputImports(t, `
class T {
  run(flag: boolean, cb: (s: string) => void): void {
    if (flag) {
      [1, 2].forEach((n) => cb('in ' + n));
    } else {
      cb('direct');
    }
  }
}
const t = new T();
t.run(true, (s) => console.log(s));
t.run(false, (s) => console.log(s));
`, "in 1\nin 2\ndirect\n")
}

// An object-literal method passed to `new` captures an enclosing parameter used after an early return.
func TestE2EMethodShorthandInNewArgumentCapturesParameter(t *testing.T) {
	assertOutputImports(t, `
class Src { read: () => string; constructor(o: { read(): string }) { this.read = o.read; } }
function make(v: string): Src {
  if (v === '') {
    return new Src({ read() { return 'empty'; } });
  }
  return new Src({ read() { return 'got ' + v; } });
}
console.log(make('').read(), make('x').read());
`, "empty got x\n")
}

// An object literal emitted to a listener that reads it through `any`.
func TestE2EDynamicEmitterObjectLiteralArgument(t *testing.T) {
	assertOutputImports(t, `
import { EventEmitter } from 'events';
const e = new EventEmitter();
e.on('x', (a: any, info: any) => { console.log(a, info.hasUnpiped); info.hasUnpiped = true; console.log(info); });
e.emit('x', 1, { hasUnpiped: false });
`, "1 false\n{ hasUnpiped: true }\n")
}

// `new m.C()`, `class D extends m.C` and the types `m.C`, `m.I`, `m.I[]` through a namespace import of a user module.
func TestE2EQualifiedNewAndExtendsThroughNamespaceImport(t *testing.T) {
	assertMultiFileOutput(t, map[string]string{
		"main.ts": `import * as m from './shapes.ts';
const c: m.Circle = new m.Circle(2);
console.log(c.area().toFixed(2));
function show(s: m.Named): string { return s.name + '/' + s.sides; }
const n: m.Named = { name: 'tri', sides: 3 };
const all: m.Named[] = [n, { name: 'sq', sides: 4 }];
console.log(all.map(show).join(','));
class Ring extends m.Circle {
  inner: number;
  constructor(r: number, inner: number) { super(r); this.inner = inner; }
  area(): number { return super.area() - Math.PI * this.inner * this.inner; }
}
console.log(new Ring(2, 1).area().toFixed(2));
`,
		"shapes.ts": `export class Circle {
  r: number;
  constructor(r: number) { this.r = r; }
  area(): number { return Math.PI * this.r * this.r; }
}
export interface Named { name: string; sides: number }
`,
	}, "main.ts", "12.57\ntri/3,sq/4\n9.42")
}

// A top-level object literal with methods, read from a named function and from another module; a method reads a global declared after it.
func TestE2ETopLevelObjectWithMethodReadFromFunction(t *testing.T) {
	assertMultiFileOutput(t, map[string]string{
		"main.ts": `import { util } from './util.ts';
const local = { twice(n: number): number { return n * 2; }, name: 'local', count(): number { return later.length; } };
const later: number[] = [1, 2];
function f(): number { return local.twice(util.level) + util.inc(1) + local.count(); }
console.log(f(), local.name, util.name);
`,
		"util.ts": `export const util = {
  inc(n: number): number { return n + 1; },
  level: 3,
  name: 'u',
};
`,
	}, "main.ts", "10 local u")
}

// An override declares a parameter of another representation than the overridden one (`Buffer` over `any`, `number` over `any`).
func TestE2EOverrideWithNarrowerParameterType(t *testing.T) {
	assertOutputImports(t, `
class Base {
  handle(chunk: any, tag: string): string { return 'base ' + tag; }
  run(chunk: any): string { return this.handle(chunk, 'run'); }
}
class Bytes extends Base {
  handle(chunk: Buffer, tag: string): string { return tag + ' ' + chunk.length + ' ' + chunk.toString(); }
}
class Nums extends Base {
  handle(n: number, tag: string): string { return tag + ' ' + (n * 2); }
}
console.log(new Bytes().run(Buffer.from('hey')));
console.log(new Nums().run(21));
const b: Base = new Bytes();
console.log(b.handle(Buffer.from('x'), 'direct'));
`, "run 3 hey\nrun 42\ndirect 1 x\n")
}

// A listener with a `Buffer` parameter passed where `(...args: any[]) => void` is expected.
func TestE2ETypedListenerThroughRestAnySlot(t *testing.T) {
	assertOutputImports(t, `
class Bus {
  private fns: ((...args: any[]) => void)[] = [];
  on(fn: (...args: any[]) => void): void { this.fns.push(fn); }
  send(...args: any[]): void { for (const f of this.fns) f(...args); }
}
const bus = new Bus();
bus.on((d: Buffer, n: number) => { console.log('typed', d.toString(), d.length, n); });
bus.on((d) => { console.log('untyped ' + d); });
const chunk: any = Buffer.from('foo');
bus.send(chunk, 3);
`, "typed foo 3 3\nuntyped foo\n")
}

// `buf.subarray()` returns a Buffer.
func TestE2EBufferSubarrayIsABuffer(t *testing.T) {
	assertOutputImports(t, `
const b = Buffer.from('hello');
const s = b.subarray(1, 3);
console.log(s, s.toString(), Buffer.isBuffer(s));
`, "<Buffer 65 6c> el true\n")
}

// Bitwise operators on typed-array elements convert each operand with ToInt32.
func TestE2EBitwiseOnTypedArrayElements(t *testing.T) {
	assertOutputImports(t, `
const b = Buffer.from([0x3d, 0xd8, 0xff]);
console.log(b[0] | (b[1] << 8), b[2] << 24, b[2] & 0x0f, b[1] ^ 256, (b[2] >>> 1));
const u = new Uint16Array([65535]);
console.log(u[0] << 16, u[0] | 0x10000);
const i8 = new Int8Array([-1]);
console.log(i8[0] >>> 0, i8[0] << 8);
`, "55357 -16777216 15 472 127\n-65536 131071\n4294967295 -256\n")
}

// Decoding bytes as UTF-8 replaces each invalid sequence with U+FFFD, as the WHATWG decoder does.
func TestE2EBufferToStringReplacesInvalidUtf8(t *testing.T) {
	assertOutputImports(t, `
const cases: number[][] = [[0xf0, 0x9f], [0xf0, 0x9f, 0x41], [0xe2, 0x82, 0xac], [0xc0, 0x80], [0xed, 0xa0, 0x80], [0xff, 0x61], [0xe2, 0x28, 0xa1], [0xf4, 0x90, 0x80, 0x80], [0xe0, 0x80]];
for (const c of cases) console.log(JSON.stringify(Buffer.from(c).toString()), Buffer.from(Buffer.from(c).toString()).length);
`, "\"\ufffd\" 3\n\"\ufffdA\" 4\n\"\u20ac\" 3\n\"\ufffd\ufffd\" 6\n\"\ufffd\ufffd\ufffd\" 9\n\"\ufffda\" 4\n\"\ufffd(\ufffd\" 7\n\"\ufffd\ufffd\ufffd\ufffd\" 12\n\"\ufffd\ufffd\" 6\n")
}
