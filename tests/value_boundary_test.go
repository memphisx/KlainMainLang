package tests

import (
	"strings"
	"testing"
)

// A callback parameter typed with fewer parameters than the function the
// caller passes (`cb: () => void` receiving Writable's `(error?) => void`):
// the call supplies the missing ones as undefined, so the stream sees no
// error.
func TestE2ECallbackParamFewerParams(t *testing.T) {
	assertSameAsNodeImports(t, `
import { Readable, Writable, pipeline } from 'stream'
const chunks: Buffer[] = []
const w = new Writable({ write(c: Buffer, _e: string, cb: () => void) { chunks.push(c); cb() } })
w.on('finish', () => console.log('finish', chunks.length))
w.write('a')
w.write('b')
w.end()
const r = Readable.from(['x', 'y', 'z'])
const w2 = new Writable({ write(c: Buffer, _e: string, cb: () => void) { console.log('w', c.toString()); cb() } })
pipeline(r, w2, (err: any) => console.log('done', err))
`)
}

// A plain object assigned to an index-signature binding becomes its
// dictionary (its fields are the entries), also through Object.freeze.
func TestE2EObjectIntoIndexSignature(t *testing.T) {
	assertSameAsNodeImports(t, `
const o = { a: 0, b: 2 }
const c: { [key: string]: number } = o
console.log(c.a, c['b'], c)
function f() {
  const d: { [key: string]: any } = Object.freeze({ x: 1, '-3': 'neg' })
  console.log(d.x, d['-3'])
}
f()
`)
}

// Views held in any: ArrayBuffer.isView sees a DataView, a DataView's
// setBigInt64 takes a bigint from any, and NodeJS.ArrayBufferView is any
// TypedArray or DataView.
func TestE2EViewsThroughAny(t *testing.T) {
	assertSameAsNodeImports(t, `
const d: any = new DataView(new ArrayBuffer(8))
const u: any = new Uint16Array(2)
console.log(ArrayBuffer.isView(d), ArrayBuffer.isView(u), ArrayBuffer.isView({}))
const big: any = 5n
const dv = new DataView(new ArrayBuffer(8))
dv.setBigInt64(0, big, true)
console.log(dv.getBigInt64(0, true))
function len(v: NodeJS.ArrayBufferView): number { return v.byteLength }
console.log(len(new Uint8Array(3)), len(new DataView(new ArrayBuffer(6))))
`)
}

// An overloaded call the checker cannot type an argument of (a
// codegen-only form) still takes the result the fitting overloads agree on.
func TestE2EOverloadResultWithUntypedArg(t *testing.T) {
	assertSameAsNodeImports(t, `
import zlib from 'zlib'
const dec = new TextDecoder()
const u8 = new TextEncoder().encode("round trip")
console.log(dec.decode(zlib.gunzipSync(zlib.gzipSync(u8))))
const gz = zlib.gzipSync(u8)
console.log(Buffer.concat([zlib.gunzipSync(gz), Buffer.from('!')]).toString())
`)
}

// One function through two differently typed slots is still itself, and a
// write/end callback gets Node's null.
func TestE2EFunctionIdentityThroughAdapters(t *testing.T) {
	assertSameAsNodeImports(t, `
import { Writable } from 'stream'
function nop(e?: Error | null): void {}
type Cb = (e?: any) => void
const a: Cb = nop
const b: Cb = nop
const c: (e?: Error | null) => void = nop
console.log(a === b, a !== b, c === nop, a === (nop as any))
const w = new Writable({ write(ch: any, e: string, cb: (err?: Error | null) => void) { cb() } })
w.write('a', (err: any) => console.log('write cb', err))
w.end((err?: any) => console.log('end cb', err))
`)
}

// A qualified builtin type codegen has no table for (`NodeJS.ExitListener`)
// holds its value boxed, and `NodeJS.ArrayBufferView` in a union takes a
// Buffer, a TypedArray or a DataView.
func TestE2EQualifiedBuiltinTypes(t *testing.T) {
	assertSameAsNodeImports(t, `
const s: NodeJS.Signals = 'SIGINT';
const d: NodeJS.Dict<number> = { a: 1 };
const o: NodeJS.UncaughtExceptionOrigin = 'uncaughtException';
const l: NodeJS.ExitListener = (code: number) => {};
const w: NodeJS.EmitWarningOptions = { code: 'X' };
function f(x: string | NodeJS.ArrayBufferView): number { return typeof x === 'string' ? x.length : x.byteLength; }
console.log(s, d.a, o, typeof l, w.code, f('abc'), f(Buffer.from('ab')), f(new DataView(new ArrayBuffer(3))));
`)
}

// An overloaded call whose implementation returns `any` into a class-typed
// binding, module-level and local.
func TestE2EAnyImplementationIntoClassBinding(t *testing.T) {
	assertSameAsNodeImports(t, `
class C { x = 1; hi() { return 'hi ' + this.x; } }
function f(n: number): C;
function f(n: string): C;
function f(n: any): any { return new C(); }
const c: C = f(1);
console.log(c.hi(), c.x);
function g() { const d: C = f('a'); console.log(d.hi()); }
g();
`)
}

// A ternary whose branches are different kinds, into a union.
func TestE2ETernaryDifferentKindsIntoUnion(t *testing.T) {
	assertSameAsNode(t, `
function f(s: string | number): string | boolean {
    return typeof s === 'string' ? s : true;
}
const v: string | boolean = Math.random() > 2 ? 'x' : false;
const w: string | number = Math.random() < 2 ? 'y' : 3;
console.log(f('a'), f(1), v, w);
`)
}

// instanceof a builtin class on a statically typed value.
func TestE2EInstanceofBuiltinOnStaticValue(t *testing.T) {
	assertSameAsNode(t, `
const u = new Uint8Array(2);
const b = Buffer.from('a');
const m = new Map<string, number>();
const d = new Date(0);
console.log(u instanceof Uint8Array, b instanceof Uint8Array, m instanceof Map, d instanceof Date, u instanceof Uint16Array);
`)
}

// ADR-01299: `type`/`interface` as variable names, an array literal
// holding an `any`, Errors held in `any` through rejections, a caught plain
// object's message, and string.toString() on a narrowed `any`.
func TestE2EContextualKeywordsAndAnyThroughRejections(t *testing.T) {
	assertSameAsNodeImports(t, `
let type: any = 'a';
type = undefined;
let interface_ = 1;
console.log(type, interface_);
const d: any = new Date(0);
const arr = [1, d.toISOString()];
console.log(typeof arr[1], arr);
const err: any = new Error('boom');
Promise.reject(err).catch((e: any) => console.log('err', e.message, e instanceof Error));
Promise.reject({ message: 'plain' }).catch((e: any) => console.log('obj', e.message));
try { throw { message: 'thrown' }; } catch (e: any) { console.log('caught', e.message); }
function get(o?: undefined): string[];
function get(o?: any): any { return ['a', 'b']; }
console.log(get().map((b) => b.toString()));
`)
}

// ADR-01299: a primitive is not an object type whose required member its
// apparent type lacks (TypeScript's TS2322).
func TestE2EPrimitiveIntoObjectTypeRejected(t *testing.T) {
	_, err := resolveAndCompile(t, "const x: { encoding: string } = 'buffer';\n")
	if err == nil || !strings.Contains(err.Error(), "is not assignable to type '{ encoding: string; }'") {
		t.Fatalf("expected an assignability error, got %v", err)
	}
}

// ADR-01299: undefined reaching an array-typed parameter through `any`
// reads as undefined; its length and iteration throw Node's TypeErrors.
func TestE2EUndefinedInArrayTypedBinding(t *testing.T) {
	assertSameAsNodeImports(t, `
const g: any = (e: any, d: Buffer) => {
    console.log('print', d);
    try { console.log(d.length); } catch (x: any) { console.log(x.name, x.message); }
};
g(null, undefined);
const h: any = (xs: number[]) => {
    try { for (const x of xs) console.log(x); } catch (e: any) { console.log(e.message); }
};
h(undefined);
h([1, 2]);
`)
}

// ADR-01299: super(message, { cause }) in an Error subclass.
func TestE2EErrorSubclassSuperCause(t *testing.T) {
	assertSameAsNodeImports(t, `
class E extends Error { constructor(m: string, o?: { cause?: any }) { super(m, o); } }
const a = new E('x', { cause: 42 });
console.log(a.message, a.cause);
const b = new E('y');
console.log(b.message, b.cause);
`)
}

// ADR-01299: an overloaded function's any result is its checker type, and
// an any[] unboxes element by element.
func TestE2EOverloadAnyResultUnboxes(t *testing.T) {
	assertSameAsNodeImports(t, `
function names(o: 'buffer'): Buffer[];
function names(o?: string): string[];
function names(o?: any): any {
    const out: any[] = ['ab', 'cd'];
    return o === 'buffer' ? out.map((s: any) => Buffer.from(s)) : out;
}
const bs = names('buffer');
console.log(bs.length, bs[0].toString('hex'), Buffer.isBuffer(bs[1]));
const ss = names();
console.log(ss.length, ss[1].toUpperCase());
`)
}
