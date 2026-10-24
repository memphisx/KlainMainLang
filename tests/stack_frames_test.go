package tests

import "testing"

// An Error's stack names the frames of the program's own functions, as V8
// names them, each at the call in progress (TDD-00188): Node is the oracle,
// short of its own internals' frames (assertSameAsNodeFrames).

func TestE2EStackFramesNames(t *testing.T) {
	assertSameAsNodeFrames(t, `
function f() { return new Error("deep"); }
const g = () => f();
const o = { m() { return g(); }, n: function () { return o.m(); } };
class K {
  e: Error;
  constructor() { this.e = o.n(); }
  static s() { return new K().e; }
  meth() { return K.s(); }
}
console.log(new K().meth().stack);
const anon = [0].map(function () { return new Error("x").stack; })[0];
console.log(anon!.split("\n").length > 1);
`)
}

func TestE2EStackFramesPositions(t *testing.T) {
	assertSameAsNodeFrames(t, `
const o = { m() { return new Error("x").stack; } };
function f() { return o.m(); }
console.log(f());
function g() { return    new Error("y").stack; }
console.log(  g());
console.log((() => new Error("z").stack)());
function deref(v: any) { return v.x; }
try { deref(null); } catch (e: any) { console.log(e.stack); }
`)
}

func TestE2EStackFramesInspect(t *testing.T) {
	assertSameAsNodeFrames(t, `
class Gx extends Error { kode = 7; constructor(m: string) { super(m); this.name = "Gx"; } }
function mk() { return new Gx("m"); }
console.log(mk());
console.log({ e: new TypeError("t"), list: [new RangeError("r")] });
const set = new Error("s");
set.stack = "custom";
console.log(set.stack, set);
`)
}

func TestE2EStackFramesAsync(t *testing.T) {
	assertSameAsNodeFrames(t, `
async function a1() { return new Error("a").stack; }
async function b1() { return await a1(); }
async function a2() { await null; return new Error("b").stack; }
async function b2() { const r = await a2(); return r; }
async function c2() { return  await   b2(); }
async function main() { console.log(await b1()); console.log(await c2()); }
main();
`)
}

func TestE2EStackFramesCallbacks(t *testing.T) {
	assertSameAsNodeFrames(t, `
function* gen() { yield new Error("g").stack; }
function second() { return gen().next().value; }
console.log(second());
setTimeout(function tick() { console.log(new Error("t").stack); }, 1);
process.nextTick(() => console.log(new Error("n").stack));
`)
}

// A program with ES module syntax: its top-level frame is the location
// alone, not CommonJS's wrapper.
func TestE2EStackFramesESM(t *testing.T) {
	assertSameAsNodeFrames(t, `
export const x = 1;
function f() { return new Error("e").stack; }
console.log(f());
`)
}

// V8's Error.captureStackTrace and Error.stackTraceLimit.
func TestE2EStackFramesCaptureAndLimit(t *testing.T) {
	assertSameAsNodeFrames(t, `
const o: any = { message: "m" };
function f() { Error.captureStackTrace(o); }
f();
console.log(o.stack);
class MyErr extends Error { constructor(m: string) { super(m); Error.captureStackTrace(this, MyErr); } }
function g() { return new MyErr("x"); }
console.log(g().stack);
const p: any = {};
function h() { Error.captureStackTrace(p, h); }
function k() { h(); }
k();
console.log(p.stack);
console.log(Error.stackTraceLimit);
Error.stackTraceLimit = 1;
console.log(g().stack, Error.stackTraceLimit);
Error.stackTraceLimit = 0;
console.log(JSON.stringify(new Error("none").stack));
`)
}
