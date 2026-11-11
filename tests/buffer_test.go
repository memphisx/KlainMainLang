package tests

import "testing"

// Node Buffer (TDD-00103): a flagged Uint8Array. Construction statics,
// codecs, comparison, and the fixed-width accessors — every expectation
// diff-verified against real Node (v26).
func TestE2EBufferFromAndCodecs(t *testing.T) {
	assertOutput(t, `
const a = Buffer.from("Hello");
console.log(a.length, a[0], a[4]);
console.log(a.toString());
console.log(a.toString("hex"));
console.log(a.toString("base64"));
console.log(Buffer.from("48656c6c6f21", "hex").toString());
console.log(Buffer.from("SGVsbG8sIHdvcmxk", "base64").toString());
console.log(Buffer.from("SGk_", "base64url").toString("hex"));
console.log(Buffer.from("héllo", "latin1").toString("hex"));
console.log(Buffer.from([0xe9, 0x41]).toString("latin1"));
console.log(Buffer.byteLength("Hello"), Buffer.byteLength("48ff", "hex"));
`, "5 72 111\nHello\n48656c6c6f\nSGVsbG8=\nHello!\nHello, world\n48693f\n68e96c6c6f\néA\n5 2")
}

func TestE2EBufferAllocConcatCompare(t *testing.T) {
	assertOutput(t, `
const a = Buffer.from("Hello");
const b = Buffer.alloc(4, 7);
console.log(b[0], b[3], b.length);
const c = Buffer.from([1, 2, 300]);
console.log(c[2]);
const d = Buffer.concat([a, c]);
console.log(d.length, d.toString("hex"));
console.log(Buffer.concat([a, c], 6).toString("hex"));
console.log(Buffer.compare(a, a), Buffer.compare(Buffer.from("a"), Buffer.from("b")));
console.log(a.equals(Buffer.from("Hello")), a.equals(c));
console.log(a.compare(Buffer.from("Hellp")));
console.log(Buffer.isBuffer(a), Buffer.isBuffer("x"));
const u8 = Buffer.allocUnsafe(2);
u8[0] = 1; u8[1] = 2;
console.log(u8.toString("hex"));
`, "7 7 4\n44\n8 48656c6c6f01022c\n48656c6c6f01\n0 -1\ntrue false\n-1\ntrue false\n0102")
}

func TestE2EBufferAllocStringFill(t *testing.T) {
	// Buffer.alloc(size, stringFill) repeats the fill string's bytes (ADR-00576),
	// matching Node; a numeric fill still memsets.
	assertOutput(t, `
console.log(Buffer.alloc(5, "ab").toString());
console.log(Buffer.alloc(4, "x").toString());
console.log(Buffer.alloc(6, "abc").toString());
console.log(Buffer.alloc(3, 65).toString());
`, "ababa\nxxxx\nabcabc\nAAA")
}

func TestE2EBufferAccessorsAndWrite(t *testing.T) {
	assertOutput(t, `
const w = Buffer.alloc(8);
console.log(w.writeUInt16BE(0xCAFE, 0));
console.log(w.readUInt16BE(0), w.readUInt16LE(0));
w.writeInt32LE(-2, 4);
console.log(w.readInt32LE(4), w.readUInt32LE(4));
w.writeDoubleLE(3.5, 0);
console.log(w.readDoubleLE(0));
w.writeBigInt64BE(9007199254740993n, 0);
console.log(w.readBigInt64BE(0));
console.log(w.readBigUInt64BE(0));
const t = Buffer.alloc(10);
console.log(t.write("hi there!", 1));
console.log(t.toString("utf8", 1, 3));
const cp = Buffer.alloc(3);
const a = Buffer.from("Hello");
console.log(a.copy(cp, 0, 1));
console.log(cp.toString());
console.log(a.subarray(1, 3).toString());
console.log(a.slice(0, 2).toString("hex"));
try { w.readInt32LE(6); } catch (e) { console.log("oob"); }
`, "2\n51966 65226\n-2 4294967294\n3.5\n9007199254740993n\n9007199254740993n\n9\nhi\n3\nell\nel\n4865\noob")
}

func TestE2EBufferSharedArrayMachinery(t *testing.T) {
	assertOutput(t, `
const a = Buffer.from([10, 20, 30, 40]);
console.log(a.indexOf(30), a.includes(99));
a.fill(0, 2);
console.log(a.toString("hex"));
let sum = 0;
for (const b of a) { sum += b; }
console.log(sum, a.byteLength);
const doubled = a.map((x: number) => x * 2);
console.log(doubled[1]);
`, "2 false\n0a140000\n30 4\n40")
}

// Buffer.indexOf/includes/lastIndexOf with a STRING needle search the needle's
// bytes over the buffer (ADR-00558). Number forms still work (above).
func TestE2EBufferStringSearch(t *testing.T) {
	assertOutput(t, `
const b = Buffer.from("hello world hello");
console.log(b.indexOf("world"), b.indexOf("hello"), b.lastIndexOf("hello"));
console.log(b.indexOf("xyz"), b.includes("wor"), b.includes("nope"));
console.log(b.lastIndexOf("l"), b.indexOf(""), b.lastIndexOf(""));
`, "6 0 12\n-1 true false\n15 0 17")
}

// Buffer.fill(string, offset?, end?) repeats the needle's bytes aligned to the
// fill offset; an empty string fills zeros, as Node's _fill makes it 0.
func TestE2EBufferStringFill(t *testing.T) {
	assertOutput(t, `
const a = Buffer.alloc(5);
a.fill("ab", 1);
console.log(a.toString("hex"));
const b = Buffer.alloc(6);
b.fill("xyz");
console.log(b.toString());
const d = Buffer.from([1, 2, 3]);
d.fill("");
console.log(d[0], d[1], d[2]);
`, "0061626162\nxyzxyz\n0 0 0")
}

func TestE2EBufferRejections(t *testing.T) {
	// An encoding named by a variable picks its codec at run time (ADR-01169).
	assertOutput(t, `
const enc = "hex";
const b = Buffer.from("ff", enc);
console.log(b);
`, "<Buffer ff>")
	// An unknown one is Node's ERR_UNKNOWN_ENCODING at run time
	// (TestE2EBufferRuntimeEncodings).
}

// A Buffer boxed into `any` stays a Buffer (TDD-00231): inspect, isBuffer,
// String(), JSON and unboxing; long ones truncate at 50 bytes as Node's
// INSPECT_MAX_BYTES does.
func TestE2EBufferThroughAny(t *testing.T) {
	assertOutput(t, `
const x: any = Buffer.from("hi");
console.log(x, [x], Buffer.isBuffer(x), Buffer.isBuffer(42 as any))
console.log(String(x), JSON.stringify({ b: x }), typeof x, (x as Buffer).toString("hex"))
console.log(Buffer.alloc(51, 97))
console.log(Buffer.alloc(0))
`, "<Buffer 68 69> [ <Buffer 68 69> ] true false\nhi {\"b\":{\"type\":\"Buffer\",\"data\":[104,105]}} object 6869\n<Buffer 61 61 61 61 61 61 61 61 61 61 61 61 61 61 61 61 61 61 61 61 61 61 61 61 61 61 61 61 61 61 61 61 61 61 61 61 61 61 61 61 61 61 61 61 61 61 61 61 61 61 ... 1 more byte>\n<Buffer >")
}

// An `any` narrowed by typeof reads as the primitive, and a ternary of a
// Buffer and an `any` is an `any`, as a Node stream's chunk conversion needs.
func TestE2EAnyNarrowedByTypeofIntoBuffer(t *testing.T) {
	assertOutput(t, `
const chunks: any[] = [];
function push(c: any): void { chunks.push(typeof c === "string" ? Buffer.from(c) : c) }
push("hi"); push(Buffer.from([1, 2])); push(42);
for (const c of chunks) console.log(Buffer.isBuffer(c), c)
`, "true <Buffer 68 69>\ntrue <Buffer 01 02>\nfalse 42")
}

// JSON.stringify of a Buffer is its toJSON form, at the top level and nested.
func TestE2EBufferJSONStringify(t *testing.T) {
	assertOutput(t, `
const b = Buffer.from("hi");
console.log(JSON.stringify(b), JSON.stringify({ b }), JSON.stringify([b]))
`, `{"type":"Buffer","data":[104,105]} {"b":{"type":"Buffer","data":[104,105]}} [{"type":"Buffer","data":[104,105]}]`)
}

// An `any` passed to a Buffer parameter is the Buffer, and a BufferEncoding
// annotation is a string (TDD-00231).
func TestE2EAnyIntoBufferParameter(t *testing.T) {
	assertOutput(t, `
function hex(b: Buffer, enc: BufferEncoding): string { return enc === "hex" ? b.toString("hex") : b.toString() }
const x: any = Buffer.from("hi");
console.log(hex(x, "hex"), hex(x, "utf8"))
`, "6869 hi")
}

// `buffer` as a module (ADR-01281): the constants, isUtf8/isAscii/transcode,
// and namespace and default imports reaching the globals.
func TestE2EBufferModuleExtras(t *testing.T) {
	assertOutputImports(t, `
import { constants, kMaxLength, kStringMaxLength, INSPECT_MAX_BYTES, isUtf8, isAscii, transcode, Buffer as B } from 'buffer'
import * as bns from 'node:buffer'
import buffer from 'buffer'
console.log(constants.MAX_LENGTH, constants.MAX_STRING_LENGTH, kMaxLength, kStringMaxLength, INSPECT_MAX_BYTES)
console.log(isUtf8(Buffer.from([0xff])), isUtf8(Buffer.from('héllo')), isUtf8(new Uint8Array([0xed, 0xa0, 0x80])), isAscii(Buffer.from('é')), isAscii(new ArrayBuffer(2)))
console.log(transcode(Buffer.from('é'), 'utf8', 'latin1'))
try { isUtf8('x' as any) } catch (e: any) { console.log(e.code, e.message) }
console.log(B.from('hi').toString('hex'), bns.Buffer.from('ok').length, buffer.kMaxLength, typeof bns.isAscii, bns.atob('aGk='))
`, `9007199254740991 536870888 9007199254740991 536870888 50
false true false false true
<Buffer e9>
ERR_INVALID_ARG_TYPE The "input" argument must be an instance of ArrayBuffer, Buffer, or TypedArray. Received type string ('x')
6869 2 9007199254740991 function hi`)
}

// `instanceof Buffer`: a Buffer is a Uint8Array subclass, decided statically
// or from a boxed value's typed tag.
func TestE2EBufferInstanceOf(t *testing.T) {
	assertSameAsNode(t, `
const b = Buffer.alloc(2); const u = new Uint8Array(2);
const a: any = b; const au: any = u; const n: any = [1];
console.log(b instanceof Buffer, u instanceof Buffer, b instanceof Uint8Array);
console.log(a instanceof Buffer, au instanceof Buffer, a instanceof Uint8Array, n instanceof Buffer);
`)
}

// Buffer calls with missing or extra arguments behave as Node: extras are
// evaluated and ignored, a missing required one throws ERR_INVALID_ARG_TYPE.
func TestE2EBufferArityJS(t *testing.T) {
	assertSameAsNodeCompatJS(t, `
const t = (n, f) => { try { const r = f(); console.log(n, r); } catch (e) { console.log(n, e.name, e.code, e.message); } };
t("from0", () => Buffer.from());
t("from3", () => Buffer.from("ab", "utf8", 9));
t("alloc0", () => Buffer.alloc());
t("alloc4", () => Buffer.alloc(2, 1, "utf8", 5));
t("allocU0", () => Buffer.allocUnsafe().length);
t("concat0", () => Buffer.concat());
t("concat3", () => Buffer.concat([Buffer.from("a")], 1, 2));
t("compare1", () => Buffer.compare(Buffer.from("a")));
t("compare3", () => Buffer.compare(Buffer.from("a"), Buffer.from("b"), 3));
t("isBuffer0", () => Buffer.isBuffer());
t("isBuffer2", () => Buffer.isBuffer(Buffer.from("a"), 1));
t("byteLength0", () => Buffer.byteLength());
t("byteLength3", () => Buffer.byteLength("ab", "utf8", 1));
t("isEncoding0", () => Buffer.isEncoding());
t("isEncoding2", () => Buffer.isEncoding("hex", 1));
const b = Buffer.from("abc");
t("toString4", () => b.toString("utf8", 0, 2, 9));
t("write0", () => b.write());
t("write5", () => b.write("x", 0, 1, "utf8", 9));
t("copy0", () => b.copy());
t("copy5", () => b.copy(Buffer.alloc(3), 0, 0, 3, 9));
t("fill0", () => b.fill());
t("fill5", () => b.fill(1, 0, 1, "utf8", 9));
t("equals0", () => b.equals());
t("equals2", () => b.equals(b, 1));
t("writeUInt8_0", () => b.writeUInt8());
t("writeUInt8_3", () => b.writeUInt8(1, 0, 9));
`)
}

// Buffer's argument forms beyond the plain ones read as Node's do:
// spread and array-like sources, runtime-checked concat lists, a view's
// byteLength, copy into a wider view, search from a byteOffset in an
// encoding, compare over ranges; a bad argument is Node's coded TypeError.
func TestE2EBufferArgumentFormsJS(t *testing.T) {
	assertSameAsNodeCompatJS(t, `
const t = (g) => { try { console.log(g()); } catch (e) { console.log(e.code, e.message); } };
const a = [1, 2];
t(() => Buffer.from([...a, 3])); t(() => Buffer.from({ length: 2, 0: 65, 1: 66 }));
t(() => Buffer.from(5)); t(() => Buffer.from({})); t(() => Buffer.from(new BigInt64Array(1)));
t(() => Buffer.alloc(2, null)); t(() => Buffer.alloc(2, {})); t(() => Buffer.alloc(2, [1]));
const l16 = [new Uint16Array([258])];
t(() => Buffer.concat(l16)); t(() => Buffer.concat([1])); t(() => Buffer.concat(5));
const mix = [Buffer.from("ab"), new Uint8Array([67])]; t(() => Buffer.concat(mix)); t(() => Buffer.concat(mix, 2));
t(() => Buffer.concat([new Uint16Array([1]), Buffer.from("a")]));
t(() => Buffer.byteLength(new Uint16Array(3))); t(() => Buffer.byteLength(new ArrayBuffer(5))); t(() => Buffer.byteLength(new DataView(new ArrayBuffer(4))));
const c = Buffer.alloc(8); t(() => c.writeBigInt64LE(5));
const s = Buffer.from("abc"); const u16 = new Uint16Array(2); t(() => s.copy(u16)); console.log(u16); t(() => s.copy([]));
const h = Buffer.from("héllo hello");
t(() => [h.indexOf("l", 3), h.indexOf("l", -3), h.indexOf("", 100), h.lastIndexOf("l", 2), h.lastIndexOf("l", 100), h.lastIndexOf("he", -100)]);
t(() => [h.includes("l", 4, "utf8"), h.indexOf("6c6c", 0, "hex"), h.indexOf("6c6c", "hex"), h.indexOf("o", NaN), h.lastIndexOf("o", NaN)]);
const x = Buffer.from("abcdef"), y = Buffer.from("cdx");
t(() => [x.compare(y, 0, 2, 2, 4), x.compare(y, 0, 3, 2, 5), x.compare(y, 1), x.compare(y, 0, 0, 1, 2), x.compare(y, 4)]);
t(() => x.compare(y, 0, 9)); t(() => x.compare(y, undefined, undefined, 7)); t(() => x.equals(y, 1));
`)
}

// A Buffer method named by a key known at run time, on the Buffer itself
// and through `any`.
func TestE2EBufferComputedMethodCall(t *testing.T) {
	assertSameAsNodeCLIJS(t, `const buf = Buffer.alloc(8);
for (const e of ["LE", "BE"]) {
  buf[`+"`writeBigInt64${e}`"+`](123n, 0);
  const r = buf[`+"`readBigInt64${e}`"+`](0);
  console.log(r, r === 123n, buf["readUInt8"](7));
}
const k = "readUInt16" + "BE";
console.log(buf[k](0), buf[k](6));
try { buf["nope" + k](); } catch (e) { console.log(e.constructor.name); }
`)
}

// writeBigInt64*/writeBigUInt64* outside the 64-bit range: Node's
// ERR_OUT_OF_RANGE, the value left unwritten.
func TestE2EBufferBigInt64WriteRange(t *testing.T) {
	assertSameAsNode(t, `const buf = Buffer.alloc(8);
for (const v of [0x8000000000000000n, -0x8000000000000001n, 5n, -5n]) {
  try { buf.writeBigInt64LE(v, 0); console.log("ok", buf.readBigInt64LE(0)); }
  catch (e) { console.log((e as Error).name, (e as any).code, (e as Error).message); }
}
for (const v of [-1n, 0x10000000000000000n, 0xffffffffffffffffn, 1234n]) {
  try { buf.writeBigUInt64BE(v, 0); console.log("ok", buf.readBigUInt64BE(0)); }
  catch (e) { console.log((e as Error).name, (e as any).code, (e as Error).message); }
}
`)
}

// An array given a prototype (Object.setPrototypeOf): its reads, its
// prototype and instanceof follow that chain; a constructor given as a
// function's prototype lends it its statics; Buffer.prototype inherits
// Uint8Array.prototype.
func TestE2EArraySetPrototypeOf(t *testing.T) {
	assertSameAsNodeCLIJS(t, `function T(n) { const u = new Uint8Array(n); Object.setPrototypeOf(u, T.prototype); return u; }
Object.setPrototypeOf(T.prototype, Buffer.prototype);
Object.setPrototypeOf(T, Buffer);
console.log(Object.getPrototypeOf(T) === Buffer, typeof T.from);
T.prototype.sum = function () { let c = 0; for (let i = 0; i < this.length; i++) c += this[i]; return c; };
const x = T(3); x[0] = 2; x[2] = 5;
console.log(x.sum(), x instanceof T, x instanceof Uint8Array, x.length);
const v = [x][0];
console.log(v.sum(), Object.getPrototypeOf(v) === T.prototype, v instanceof T, v.constructor === T);
console.log(Object.getPrototypeOf(Object.getPrototypeOf(v)) === Buffer.prototype);
v.fill(5);
console.log(v.sum());
console.log(typeof Buffer.prototype.fill, Object.getPrototypeOf(Buffer.prototype) === Uint8Array.prototype, Buffer.prototype.constructor === Buffer);
const b = Buffer.from("ab");
console.log(Object.getPrototypeOf(b) === Buffer.prototype, b instanceof Buffer);
`)
}
