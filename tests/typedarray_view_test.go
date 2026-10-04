package tests

import "testing"

// A typed array's buffer and byteOffset (TDD-00243): views, subarrays and
// arrays with their own storage answer them as Node does, statically and
// through `any`.
func TestE2ETypedArrayBufferAndByteOffset(t *testing.T) {
	assertSameAsNode(t, `
const ab = new ArrayBuffer(16);
const u8 = new Uint8Array(ab, 4, 8);
u8[0] = 7;
console.log(u8.byteOffset, u8.buffer === ab, u8.buffer.byteLength);
const sub = u8.subarray(2);
console.log(sub.byteOffset, sub.buffer === ab, sub.length);
const own = new Uint16Array(4);
own[1] = 513;
console.log(own.byteOffset, own.buffer === own.buffer, own.buffer.byteLength, new Uint8Array(own.buffer)[2]);
const s2 = own.subarray(1, 3);
console.log(s2.byteOffset, s2.buffer === own.buffer);
const b = Buffer.from('hello');
const dv = new DataView(b.buffer, b.byteOffset, b.byteLength);
console.log(dv.getUint8(1), b.subarray(1).byteOffset - b.byteOffset);
const f = new Float64Array(ab, 8, 1);
f[0] = 1.5;
console.log(new Uint8Array(f.buffer, f.byteOffset, 8)[7]);
const x: any = new Uint8Array(ab, 2, 4);
console.log(x.byteOffset, x.buffer === ab, x.buffer.byteLength);
const y: any = new Uint8Array(4);
const yb = y.buffer;
console.log(yb, yb === y.buffer, yb instanceof ArrayBuffer);
`)
}

// DataView is the global module's class (TDD-00243): every accessor, Float16,
// V8's errors and inspect output, ArrayBuffer.isView through `any`, and a
// DataView as a Blob part or a TextDecoder input.
func TestE2EDataViewGlobalClass(t *testing.T) {
	assertSameAsNode(t, `
const ab = new ArrayBuffer(16);
const d = new DataView(ab, 2);
d.setUint16(0, 0x1234); d.setInt32(2, -5, true); d.setFloat64(6, Math.PI); d.setBigInt64(0, -2n, true);
console.log(d.getUint16(0), d.getInt32(2, true), d.getFloat64(6), d.getBigInt64(0, true), d.getInt8(0), d.byteLength, d.byteOffset, d.buffer === ab);
for (const v of [0.1, 65504, 65520, 1e-7, -0, 5.960464477539063e-8, NaN, -Infinity]) { d.setFloat16(0, v, true); console.log(d.getUint16(0, true), d.getFloat16(0, true)); }
try { d.getUint32(13); } catch (e) { console.log((e as Error).name, (e as Error).message); }
try { new DataView(ab, 20); } catch (e) { console.log((e as Error).name, (e as Error).message); }
console.log(new DataView(new ArrayBuffer(4), 1));
console.log([new DataView(new ArrayBuffer(2))]);
const x: any = d;
console.log(ArrayBuffer.isView(d), ArrayBuffer.isView(x), d instanceof DataView, Object.prototype.toString.call(d));
const t = new DataView(new ArrayBuffer(4), 1, 2);
t.setUint16(0, 0x6869);
console.log(new TextDecoder().decode(t));
const b = new Blob([t, '!']);
b.text().then((s) => console.log(s, b.size));
`)
}

// A view over a SharedArrayBuffer crosses to a worker as a view over the same
// memory, at its offset; its buffer is a SharedArrayBuffer, statically and
// through `any` (TDD-00243).
func TestE2ESharedViewAcrossWorker(t *testing.T) {
	assertMultiFileOutput(t, map[string]string{
		"w.ts": `
import { parentPort, workerData } from 'worker_threads';
const v = workerData as Int32Array;
v[0] = 42;
console.log('worker', v.byteOffset, v.length, v.buffer instanceof SharedArrayBuffer);
parentPort!.postMessage(1);
`,
		"main.ts": `
import { Worker } from 'worker_threads';
const sab = new SharedArrayBuffer(8);
const view = new Int32Array(sab, 4, 1);
console.log(view.buffer instanceof SharedArrayBuffer, view.buffer === sab, new Uint8Array(2).buffer instanceof SharedArrayBuffer);
const w = new Worker('./w.ts', { workerData: view });
w.on('message', () => { console.log('main sees', view[0], new Int32Array(sab)[1]); w.terminate(); });
`,
	}, "main.ts", "true true false\nworker 4 1 true\nmain sees 42 42")
}

// The BufferSource family resolves to its members: a typed array or a
// DataView fits an `ArrayBufferView` (in a union array too), and an
// ArrayBuffer or SharedArrayBuffer an `ArrayBufferLike` (ADR-01359).
func TestE2EBufferSourceTypeNames(t *testing.T) {
	assertSameAsNode(t, `
class K { v = 1; }
function f(p: (string | ArrayBuffer | ArrayBufferView | K)[]) { return p.length; }
console.log(f([new Uint8Array(2), "x", new DataView(new ArrayBuffer(1))]));
function g(v: ArrayBufferView): number { return v.byteLength; }
console.log(g(new Uint16Array(3)), g(new DataView(new ArrayBuffer(5))));
function h(b: ArrayBufferLike): number { return b.byteLength; }
console.log(h(new ArrayBuffer(4)), h(new SharedArrayBuffer(2)));
`)
}

// A typed array's constructor is its builtin (a Buffer's is Buffer)
// (ADR-01360).
func TestE2ETypedArrayConstructorProperty(t *testing.T) {
	assertSameAsNode(t, `
const u = new Uint8Array(2);
console.log(u.constructor.name, u.constructor === Uint8Array, new Float64Array(1).constructor.name, Buffer.from("a").constructor.name);
const C: any = u.constructor;
console.log(new C(3).length);
`)
}

// A non-array held in any, passed where an array is declared, throws a
// TypeError instead of reading through its payload (ADR-01361).
func TestE2EAnyNonArrayIntoArraySlotThrows(t *testing.T) {
	assertOutput(t, `
function f(p: any[]) { console.log(p.length); }
const x: any = "abc";
try { f(x); } catch (e) { console.log(e instanceof TypeError, (e as Error).message); }
const y: any = 5;
try { f(y); } catch (e) { console.log(e instanceof TypeError); }
f([1, 2] as any);
const n: any = null;
f(n ?? []);
`, "true the value is not an array\ntrue\n2\n0")
}

// Buffer.from(arrayBuffer[, offset, length]) views the buffer, as Node's
// (ADR-01365).
func TestE2EBufferFromArrayBufferIsAView(t *testing.T) {
	assertSameAsNode(t, `
const ab = new ArrayBuffer(8);
const b = Buffer.from(ab);
b[0] = 7;
console.log(new Uint8Array(ab)[0], b.buffer === ab, b.byteOffset, b.length);
const r = Buffer.from(ab, 2, 3);
r[0] = 9;
console.log(new Uint8Array(ab)[2], r.byteOffset, r.length, r.buffer === ab);
const x: any = ab;
const d = Buffer.from(x);
d[1] = 5;
console.log(new Uint8Array(ab)[1], d.length);
console.log(Buffer.from(new SharedArrayBuffer(4)).buffer instanceof SharedArrayBuffer);
`)
}
