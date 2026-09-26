package tests

import "testing"

// Compiler fixes found by porting Node's fs streams to TypeScript (ADR-01168,
// ADR-01169); each program's expected output taken from Node v24.

// An exception no catch handles is rethrown after the finally runs, in a function, at top level, and from a catch.
func TestE2ETryFinallyRethrows(t *testing.T) {
	assertOutputImports(t, `
function f(): void { try { throw new Error('plain'); } finally { console.log('fin'); } }
try { f(); console.log('not reached'); } catch (e: any) { console.log('caught', e.message); }
function a(): string { try { throw new Error('a1'); } catch (e: any) { throw new Error('from catch ' + e.message); } finally { console.log('a-fin'); } }
try { a(); } catch (e: any) { console.log('caught', e.message); }
try { try { throw 'str'; } finally { console.log('inner fin'); } } catch (x) { console.log('outer caught', x); }
function c(): number { try { throw new Error('c'); } finally { return 7; } }
console.log('c =', c());
`, "fin\ncaught plain\na-fin\ncaught from catch a1\ninner fin\nouter caught str\nc = 7\n")
}

// A return, break or continue leaving a try pops its handler: a later throw reaches the handler outside, and the finally runs on the way out.
func TestE2ETryEarlyExitPopsHandler(t *testing.T) {
	assertOutputImports(t, `
function f(): number { try { return 1; } catch (e) { return 2; } finally { console.log('f fin'); } }
function g(): void { throw new Error('later'); }
console.log(f());
for (let i = 0; i < 3; i++) { try { if (i === 1) continue; if (i === 2) break; console.log('body', i); } finally { console.log('fin', i); } }
outer: for (let i = 0; i < 2; i++) { for (let j = 0; j < 2; j++) { try { if (j === 1) break outer; } finally { console.log('labelled fin', i, j); } } }
try { g(); } catch (e: any) { console.log('caught', e.message); }
`, "f fin\n1\nbody 0\nfin 0\nfin 1\nfin 2\nlabelled fin 0 0\nlabelled fin 0 1\ncaught later\n")
}

// try/finally in async functions and generators: the exception survives the finally.
func TestE2ETryFinallyAsync(t *testing.T) {
	assertOutputImports(t, `
async function k(): Promise<string> { try { await Promise.resolve(); throw new Error('k'); } catch (x: any) { await Promise.resolve(); throw new Error('k2 ' + x.message); } finally { console.log('k fin'); } }
async function* ag(): AsyncGenerator<number> { try { yield 1; throw new Error('ag'); } finally { console.log('ag fin'); } }
function* sg(): Generator<number> { try { yield 1; throw new Error('sg'); } finally { console.log('sg fin'); } }
async function main(): Promise<void> {
    try { await k(); } catch (x: any) { console.log('caught', x.message); }
    try { for await (const v of ag()) console.log(v); } catch (x: any) { console.log('caught', x.message); }
    try { for (const v of sg()) console.log(v); } catch (x: any) { console.log('caught', x.message); }
}
main();
`, "k fin\ncaught k2 k\n1\nag fin\ncaught ag\n1\nsg fin\ncaught sg\n")
}

// Leaving a for await early runs the async generator's finally (AsyncIteratorClose).
func TestE2EForAwaitBreakClosesGenerator(t *testing.T) {
	assertOutputImports(t, `
async function* ag(): AsyncGenerator<number> { try { yield 1; yield 2; yield 3; } finally { console.log('ag finally'); } }
class It { async *[Symbol.asyncIterator](): AsyncIterableIterator<number> { try { yield 1; yield 2; } finally { console.log('it finally'); } } }
async function first(): Promise<number> { for await (const v of ag()) { return v; } return -1; }
async function main(): Promise<void> {
    for await (const v of ag()) { console.log(v); if (v === 2) break; }
    console.log('after ag');
    for await (const v of new It()) { console.log(v); break; }
    console.log('after it');
    console.log('first', await first());
}
main();
`, "1\n2\nag finally\nafter ag\n1\nit finally\nafter it\nag finally\nfirst 1\n")
}

// An optional method (m?(): T) is undefined on a class that does not implement it; a subclass may.
func TestE2EOptionalClassMethods(t *testing.T) {
	assertOutputImports(t, `
class P { hook?(n: number): string; probe(): string { return this.hook != null ? this.hook(1) : 'none'; } }
class C extends P {}
class G extends C { hook(n: number): string { return 'G' + n; } }
console.log(new P().probe(), new C().probe(), new G().probe());
const ps: P[] = [new P(), new G()];
for (const p of ps) console.log(typeof p.hook, p.hook === undefined, p.hook?.(7));
class Base { _construct?(cb: () => void): void; ready(): boolean { return this._construct != null; } }
class Impl extends Base { _construct(cb: () => void): void { cb(); } }
console.log(new Base().ready(), new Impl().ready());
`, "none none G1\nundefined true undefined\nfunction false G7\nfalse true\n")
}

// && and || yield an operand, not a boolean.
func TestE2EValuePreservingAndOr(t *testing.T) {
	assertOutputImports(t, `
function f(err?: Error | null): void { console.log(err && err.message); }
f(new Error('m'));
function g(xs?: number[]): number[] { const ys = xs || [1, 2]; return ys; }
console.log(g(), g([5]));
function k(s?: string): string { return s || 'default'; }
console.log(k(), k('x'));
const n = 0 || 'zero'; console.log(n);
const e = '' && 'never'; console.log(JSON.stringify(e));
`, "m\n[ 1, 2 ] [ 5 ]\ndefault x\nzero\n\"\"\n")
}

// x ?? null keeps null for an absent number, and an absent number | null boxes to null.
func TestE2ENullishScalarNull(t *testing.T) {
	assertOutputImports(t, `
let x: number | undefined = undefined;
const v = x ?? null;
console.log(v);
const a: any = v;
console.log(a);
function take(...r: any[]): void { console.log(r[0]); }
take(x ?? null);
x = 4;
take(x ?? null);
`, "null\nnull\nnull\n4\n")
}

// x?.p on an any holding null or undefined is undefined.
func TestE2EOptionalChainOnAny(t *testing.T) {
	assertOutputImports(t, `
let p: any = null;
console.log(p?.offset, p?.offset ?? 0);
let q: any = undefined;
console.log(q?.x);
let r: any = { x: 3 };
console.log(r?.x);
`, "undefined 0\nundefined\n3\n")
}

// A class extending TypeError or RangeError is one: its name, instanceof, and catch.
func TestE2EErrorSubclassesOfBuiltinKinds(t *testing.T) {
	assertOutputImports(t, `
class NodeTypeError extends TypeError {
    code: string;
    constructor(code: string, message: string) { super(message); this.code = code; }
}
class Plain extends Error {}
const e = new NodeTypeError('ERR_X', 'bad thing');
console.log(e.name, e.message, e.code, e instanceof TypeError, e instanceof Error, e instanceof RangeError);
const anyE: any = e;
console.log(anyE instanceof TypeError, anyE instanceof RangeError, new Plain('p') instanceof TypeError);
try { throw e; } catch (c) { console.log(c instanceof TypeError, (c as Error).name, String(c)); }
`, "TypeError bad thing ERR_X true true false\ntrue false false\ntrue TypeError TypeError: bad thing\n")
}

// An Error's absent code, syscall, path and errno read undefined; properties set through any are kept.
func TestE2EErrorAbsentFieldsAndDynamicSet(t *testing.T) {
	assertOutputImports(t, `
const x: NodeJS.ErrnoException = new Error('boom');
console.log(x.code, x.code === undefined, x.syscall, x.path === undefined, typeof x.errno);
const e: any = new Error('m');
e.code = 'X';
e.errno = -5;
e.extraThing = { a: 1 };
console.log(e.code, e.errno, e.extraThing.a, e.syscall, e.message);
const none: Error | null = null;
console.log(none, String(new TypeError('x')));
`, "undefined true undefined true undefined\nX -5 1 undefined m\nnull TypeError: x\n")
}

// A Buffer encoding named at run time: from, toString, write, byteLength, and the unknown-encoding error.
func TestE2EBufferRuntimeEncodings(t *testing.T) {
	assertOutputImports(t, `
const encs: string[] = ['utf8', 'HEX', 'base64', 'base64url', 'latin1', 'ascii', 'utf16le', 'UCS-2', 'binary'];
for (const enc of encs) {
    const b = Buffer.from('héllo', enc as BufferEncoding);
    console.log(enc, b.length, b.toString(enc as BufferEncoding), Buffer.byteLength('héllo', enc as BufferEncoding));
}
let u: BufferEncoding | undefined = undefined;
console.log(Buffer.from('abc', u).toString(u));
try { Buffer.from('x', 'nope' as BufferEncoding); } catch (e: any) { console.log(e.name, e.code, e.message); }
const w = Buffer.alloc(8); const e2: string = 'hex'; console.log(w.write('ff00', 0, e2 as BufferEncoding), w);
console.log(Buffer.byteLength('abc', 'hex'), Buffer.byteLength('QUJD', 'base64'), Buffer.allocUnsafeSlow(3).length);
`, "utf8 6 h\u00e9llo 6\nHEX 0  2\nbase64 3 hllo 3\nbase64url 3 hllo 3\nlatin1 5 h\u00e9llo 5\nascii 5 hillo 5\nutf16le 10 h\u00e9llo 10\nUCS-2 10 h\u00e9llo 10\nbinary 5 h\u00e9llo 5\nabc\nTypeError ERR_UNKNOWN_ENCODING Unknown encoding: nope\n2 <Buffer ff 00 00 00 00 00 00 00>\n1 3 3\n")
}

// Arrays captured by a closure built in one branch; this captured by an arrow passed as any.
func TestE2EClosureCapturesInBranches(t *testing.T) {
	assertOutputImports(t, `
function f(x: boolean): void {
    const a = [1, 2];
    if (x) setTimeout(() => console.log('in', a.length));
    a.push(3);
    console.log('out', a.length);
}
function g(x: boolean, a: number[]): void { if (x) setTimeout(() => console.log('param in', a.length)); a.push(9); console.log('param out', a.length); }
function r(fd: number, ...rest: any[]): void { const cb = rest[0] as (n: number, b: Buffer) => void; cb(5, Buffer.from('hi')); }
class C { v = 0; go(): void { r(1, (n: number, b: Buffer) => { this.v = n; console.log('v', this.v, b.length, b.toString()); }); } }
f(true); f(false); g(true, [1]); g(false, [1, 2]);
new C().go();
`, "out 3\nout 3\nparam out 2\nparam out 3\nv 5 2 hi\nin 3\nparam in 2\n")
}

// A property path narrows along a closure's own flow.
func TestE2EClosureNarrowsPropertyPaths(t *testing.T) {
	assertOutputImports(t, `
class A {
    pos: number | undefined = undefined;
    run(f: (cb: (n: number) => void) => void): void {
        f((n: number) => { if (this.pos !== undefined) this.pos += n; else this.pos = n; });
        console.log(this.pos);
    }
}
new A().run((cb) => { cb(2); cb(3); });
const o: { p: number | undefined } = { p: undefined };
function g(f: () => void): void { f(); }
g(() => { if (o.p !== undefined) o.p += 1; else o.p = 10; });
console.log(o.p);
`, "5\n10\n")
}
