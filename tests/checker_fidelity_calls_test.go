package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"KlainMainLang/codegen/llvm"
)

// A call whose callee is itself a call (`mk(k)(x)`) takes the call's type:
// the declaration's slot was an integer, so a fractional result truncated
// and a string one did not compile.
func TestE2ECallOfCallResultSlot(t *testing.T) {
	assertSameAsNode(t, `
function mk(k: number) { return (x: number) => x * k; }
const r = mk(0.5)(3);
console.log(r);
const s = mk(1)(2) > 1;
console.log(s);
function greet(p: string) { return (n: string) => p + n; }
const g = greet("hi ")("Thessaloniki");
console.log(g);
`)
	assertSameAsNodeCompatJS(t, `
var a = function (f, dx) { return function (x) { return (f(x + dx) - f(x)) / dx; }; }(function (x) { return x * x; }, 0.5)(1);
console.log(a);
var d = function (f, dx) { return function (x) { return f(x + dx); }; }(function s(x) { return Math.sin(x); }, 0.5)(1);
console.log(d, typeof s);
`)
}

// A function passed to an immediately invoked function, at an unannotated
// parameter, is typed without context, as tsc resolves that circularity:
// its own unannotated parameters are any (TS7006), and the invoked
// function's parameter takes its type.
func TestE2EIIFEFunctionArgumentTyping(t *testing.T) {
	mustCompileError(t, `
const v: string = function d(f) { return f(1); }(function s(x) { return 2; });
`, "type 'number' is not assignable to type 'string'")
	mustCompileError(t, `
const u = (function (f: (x: number) => number) { return f(1); })((x) => 2);
const w = (function (f) { return f(1); })((x) => 2);
`, "parameter 'x' implicitly has an 'any' type")
}

// Class members declared twice are TypeScript errors (TS2300, TS2393)
// that JavaScript allows.
func TestE2EDuplicateClassMembers(t *testing.T) {
	mustCompileError(t, `
class A { static f = "a"; static f = "b"; }
console.log(A.f);
`, "identifier 'f' has already been declared")
	mustCompileError(t, `
class A { m() { return 1; } m() { return 2; } }
console.log(new A().m());
`, "duplicate function implementation of 'm'")
	assertSameAsNodeCompatJS(t, `
var C = class { static f = 'test'; static f = this.f + '262'; m() { return 1; } m() { return 2; } };
console.log(C.f, new C().m());
`)
}

// A primitive's undeclared member is TS2339 (its wrapper interface lacks
// it), and in JavaScript it reads Object.prototype's property, a getter
// there running with the primitive as its receiver.
func TestE2EPrimitiveMemberObjectPrototype(t *testing.T) {
	mustCompileError(t, `
const n = 5;
console.log(n.x);
`, "property 'x' does not exist on type '5'")
	assertSameAsNode(t, `
Object.defineProperty(Object.prototype, "x", { get: function () { return 42; } });
const v: any = 5;
const s: any = "a";
console.log(v.x, s.x, ({} as any).x);
(Object.prototype as any).y = 7;
console.log(v.y, s.y, true && (v as any).zz);
`)
	assertSameAsNodeCompatJS(t, `
Object.defineProperty(Object.prototype, "x", { get: function () { return 42; } });
console.log((5).x, "s".x, true.x);
`)
}

// A destructuring parameter whose default is null or undefined is
// rejected as tsc rejects it.
func TestE2ENullishPatternParameterDefault(t *testing.T) {
	mustCompileError(t, "function f({} = null) {}\nf();\n", "object is possibly 'null'")
	mustCompileError(t, "function g([b] = null) {}\ng();\n", "must have a '[Symbol.iterator]()' method")
	mustCompileError(t, "function h({a} = undefined) {}\nh();\n", "property 'a' does not exist on type 'undefined'")
}

// A function whose return inference reaches back into itself through a
// call has an any result (TS7023), its calls included.
func TestE2EReturnCycleIsAny(t *testing.T) {
	mustCompileError(t, `
function f(arg: number) { if (arg === 1) { return arg; } else { return f(arg - 1) * arg; } }
console.log(f(3));
`, "'f' implicitly has return type 'any'")
	assertSameAsNodeCompatJS(t, `
function fact(arg) { if (arg === 1) { return arg; } else { return fact(arg - 1) * arg; } }
var x = fact(4);
console.log(x, x / 3);
`)
}

// A function literal reading itself in its return: asked for its own
// result there, TS7023; returned as a value, a function whose type contains
// itself, held and called as Node does.
func TestE2ESelfReferentialFunctionLiteral(t *testing.T) {
	mustCompileError(t, "const g = () => g();\nconsole.log(typeof g);\n", "'g' implicitly has return type 'any'")
	assertOutput(t, "const f = () => f;\nconsole.log(typeof f(), f()() === f);\n", "function true")
	assertSameAsNodeCompatJS(t, "const g = () => 1;\nconst h = function () { return h; };\nconsole.log(g(), h()() === h);\n")
}

// A generic's object literal whose methods return it: each call reads the
// same object, as in Node.
func TestE2ESelfReferentialGenericLiteral(t *testing.T) {
	assertOutput(t, `
function example<T1>() {
  let x = {
    foo: <T2>(t2: T2) => x,
    bar: (t1: T1) => x,
  };
  return x;
}
const e = example<number>();
console.log(e.foo("hello").bar(1) === e);
`, "true")
}

// Programs tsc accepts though a function reaches itself: a self-call among
// the returns, a call outside them, a cycle through parameter defaults, a
// namespace reading an outer var, a computed method name.
func TestE2ERecursionTscAccepts(t *testing.T) {
	assertSameAsNode(t, `
const fn1 = (): number => 0;
const fn2 = (n: number) => {
  if (n > 0) {
    return fn2(n - 1);
  }
  return n;
};
async function rec(n: number) {
  if (n > 0) {
    return await rec(n - 1);
  }
  return "done";
}
function f(x: number) {
  var z = x > 0 ? f(x - 1) : 0;
  console.log(z);
  return x;
}
function foo(a: Parameters<typeof bar>[0]) { return a; }
function bar(a: number) { return foo(a); }
const self = function me(a: string) { if (a) me(""); };
self("x");
console.log(fn1(), fn2(3), f(3), bar(4));
rec(2).then((v) => console.log(v));
`)
	mustCompileError(t, "namespace a {\n  var x: number;\n  console.log(x);\n}\n", "variable 'x' is used before being assigned")
}

// A module `var` with an initializer that shares a function declaration's
// name is one binding: the function (hoisted) until the initializer
// assigns it, and whatever is assigned after; calls read the binding.
func TestE2EVarSharingFunctionName(t *testing.T) {
	assertSameAsNodeCompatJS(t, `
console.log(f(2), typeof f);
function show() { return typeof f; }
console.log(show());
var f = "str";
console.log(f, show());
f = function (x) { return x * 3; };
console.log(f(2), show());
function f(x) { return x + 1; }
`)
}

// A property read on a union is TS2339 when any member certainly lacks it,
// as tsc's union property lookup gives; a member every member has reads.
func TestE2EUnionMemberMissingProperty(t *testing.T) {
	mustCompileError(t, "const m = [1, { label: \"x\" }];\nconsole.log(m[1].label);\n", "property 'label' does not exist on type 'number | { label: string; }'")
	assertSameAsNode(t, `
declare const u: { k: "a"; x: 1 } | { k: "b"; y: 2 };
const v = Math.random() > 2 ? { k: "a" as const, x: 1 as const } : { k: "b" as const, y: 2 as const };
console.log(v.k);
const s: string | string[] = Math.random() > 2 ? "ab" : ["a", "b"];
console.log(s.length, s.slice(1));
`)
}

// A generic function's overload signature keeps its type parameters'
// defaults and constraints: `T = void` with nothing to infer is void.
func TestE2EOverloadTypeParamDefault(t *testing.T) {
	mustCompileError(t, `function g2<T = void>(v?: T): T[];
function g2<T = void>(v?: T): T[] { return []; }
const d: string = g2();
`, "type 'void[]' is not assignable to type 'string'")
}

// A typed array's own iterator spreads its elements.
func TestE2ETypedArraySymbolIteratorSpread(t *testing.T) {
	assertSameAsNode(t, `
const ta = new Uint8Array([7, 8]);
console.log([...ta[Symbol.iterator]()]);
function f() { const tb = new Float64Array([1.5, 2]); return [...tb[Symbol.iterator]()]; }
console.log(f());
`)
}

// A call of an ambient function declared as overloads (`declare function`
// twice, no implementation) evaluates its arguments, then throws, whichever
// signature the arguments fit.
func TestE2EAmbientOverloadCallThrows(t *testing.T) {
	assertOutput(t, `declare function mc(exact?: number): () => void;
declare function mc<T extends (...args: any[]) => any>(fn: T, exact?: number): T;
try {
  mc(() => {});
} catch (e) {
  console.log((e as Error).message);
}
`, "ambient function 'mc' has no implementation")
}

// process.versions is ProcessVersions (a Dict<string> with named members):
// a named member is a string, an unknown key reads undefined; uv is the
// libuv the program links.
func TestE2EProcessVersionsTyped(t *testing.T) {
	assertSameAsNode(t, `
console.log(process.versions.node.split(".").length > 1, typeof process.versions.v8, process.versions.nope);
console.log(/^\d+\.\d+\.\d+$/.test(process.versions.uv));
`)
}

// typeof a host handle (a compression stream, URL, AbortController,
// TextEncoder, Headers) is "object"; it read "string", the bare-pointer
// default.
func TestE2ETypeofHostHandle(t *testing.T) {
	assertSameAsNode(t, `
const cs = new CompressionStream("gzip");
const ds = new DecompressionStream("gzip");
const u = new URL("http://x/");
const ac = new AbortController();
const te = new TextEncoder();
const h = new Headers();
console.log(typeof cs, typeof ds, typeof u, typeof ac, typeof ac.signal, typeof te, typeof h, typeof u.href);
`)
}

// Buffer swap16/32/64 swap every 2-, 4- or 8-byte group in place and return
// the buffer, through a view too; a size that is not a multiple is Node's
// coded RangeError. On a Buffer held as `any` the call compiled forever:
// the dynamic Buffer branch's static call came back to the same probe.
func TestE2EBufferSwap(t *testing.T) {
	assertSameAsNode(t, `
const b = Buffer.from([1, 2, 3, 4, 5, 6, 7, 8]);
console.log(b.swap16());
console.log(b.swap32());
console.log(b.swap64() === b, b);
b.subarray(1, 5).swap32();
console.log(b);
try { Buffer.alloc(3).swap16(); } catch (e: any) { console.log(e.name, e.code, e.message); }
try { Buffer.alloc(6).swap64(); } catch (e: any) { console.log(e.code, e.message); }
const words = new Uint32Array(2).fill(0x04030201);
const d: any = Buffer.from(words.buffer, words.byteOffset);
d.swap32();
console.log(d);
`)
}

// Array.prototype's mutators called on an array-like object work on the
// object's own properties, as ECMA-262 states them: the result changes the
// object, and a `length` near 2**32 costs only the indices visited (the
// receiver was copied into an array of `length` elements first).
func TestE2EArrayMutatorsOnArrayLike(t *testing.T) {
	assertSameAsNodeCompatJS(t, `
var obj = {};
obj.pop = Array.prototype.pop;
obj[0] = "x";
obj[4294967296] = "y";
obj.length = 4294967297;
console.log(obj.pop(), obj.length, obj[0], obj[4294967296]);
var o2 = { 0: "a", 1: "b", length: 2 };
console.log(Array.prototype.push.call(o2, "c", "d"), JSON.stringify(o2));
console.log(Array.prototype.shift.call(o2), JSON.stringify(o2));
console.log(Array.prototype.unshift.call(o2, "z"), JSON.stringify(o2));
console.log(JSON.stringify(Array.prototype.splice.call(o2, 1, 2, "q")), JSON.stringify(o2));
console.log(JSON.stringify(Array.prototype.reverse.call(o2)));
var o3 = { length: 9007199254740991 };
try { Array.prototype.push.call(o3, 1); } catch (e) { console.log(e.constructor.name); }
var o4 = {};
console.log(Array.prototype.pop.call(o4), o4.length);
var arr = [1, 2, 3];
console.log(Array.prototype.pop.call(arr), arr);
var o5 = {};
o5.push = Array.prototype.push;
var push = o5.push(-1);
console.log(push, o5.length, o5[0]);
var key = "pu" + "sh";
var o6 = {};
o6.f = Array.prototype[key];
console.log(o6.f(-1), o6.length, o6[0]);
`)
}

// The read-only and copying Array.prototype methods run on an array-like
// receiver through its own properties, holes skipped where the spec skips
// them, and an object with a huge length costs only the elements it holds.
func TestE2EArrayMethodsOnArrayLike(t *testing.T) {
	assertSameAsNodeCompatJS(t, `var o = { 0: 3, 1: 1, 3: 2, length: 5 };
var P = Array.prototype;
console.log(P.indexOf.call(o, 2), P.lastIndexOf.call(o, 3), P.includes.call(o, undefined), P.includes.call(o, 9));
console.log(P.join.call(o, "-"), P.at.call(o, -2), P.at.call(o, 0));
console.log(JSON.stringify(P.slice.call(o, 1, 4)), JSON.stringify(P.map.call(o, function (v, i) { return v * 10 + i; })));
console.log(JSON.stringify(P.filter.call(o, function (v) { return v > 1; })));
console.log(P.some.call(o, function (v) { return v === 2; }), P.every.call(o, function (v) { return v > 0; }));
console.log(P.reduce.call(o, function (a, v) { return a + v; }), P.reduceRight.call(o, function (a, v) { return a + "" + v; }, ""));
console.log(P.find.call(o, function (v) { return v === undefined; }), P.findIndex.call(o, function (v) { return v === undefined; }));
console.log(P.findLast.call(o, function (v) { return v > 1; }), P.findLastIndex.call(o, function (v) { return v > 1; }));
var seen = [];
P.forEach.call(o, function (v, i) { seen.push(i + ":" + v); });
console.log(seen.join(" "));
console.log(JSON.stringify(P.toSorted.call(o)), JSON.stringify(P.toReversed.call(o)), JSON.stringify(P.with.call(o, 1, "w")));
console.log(JSON.stringify(P.toSpliced.call(o, 1, 2, "x")));
var s = { 0: "b", 1: "a", 3: "c", length: 5 };
P.sort.call(s);
console.log(JSON.stringify(s));
var f = { length: 4 };
P.fill.call(f, 7, 1, 3);
console.log(JSON.stringify(f));
var c = { 0: "a", 1: "b", 2: "c", length: 3 };
P.copyWithin.call(c, 0, 1);
console.log(JSON.stringify(c));
try { P.reduce.call({ length: 0 }, function () {}); } catch (e) { console.log(e.constructor.name, e.message); }
`)
	assertOutputCompatJS(t, `var P = Array.prototype;
var big = { 0: "a", 5000000000: "z", length: 9007199254740991 };
console.log(P.indexOf.call(big, "z"), P.some.call(big, function (v) { return v === "z"; }), P.lastIndexOf.call(big, "a"));
try { P.map.call(big, function (v) { return v; }); } catch (e) { console.log(e.constructor.name); }
`, "5000000000 true 0\nRangeError\n")
}

// A getter returning its own object literal's binding builds the literal's
// class once instead of recursing.
func TestE2EObjectLiteralGetterReturnsSelf(t *testing.T) {
	assertSameAsNode(t, `const a = {
  prop: 42,
  get self(): any {
    return a;
  },
} satisfies { prop: number; self: any };
console.log(a.self.prop, a.self.self.prop);
`)
}

// A property initialized from the object literal's own binding reads the
// binding's value before the assignment (undefined), and typing it does not
// recurse.
func TestE2EObjectLiteralPropertyReadsOwnBinding(t *testing.T) {
	assertSameAsNodeCompatJS(t, `var a = { f: a, g: 1 };
console.log(a.f, a.g);
`)
}

// A class adding nothing to a builtin module's class whose constructor
// reads `arguments` (URL) inherits that constructor.
func TestE2EImplicitConstructorForwardsRestBase(t *testing.T) {
	assertSameAsNode(t, `import util from "util";
class MyURL extends URL {}
const u = new MyURL("https://a.example/x");
console.log(u.pathname, u instanceof URL, util.inspect(u).startsWith("MyURL {"));
class B { n: number; constructor(...xs: number[]) { this.n = xs.length; } }
class C extends B {}
console.log(new C(1, 2, 3).n);
`)
}

// A throw out of a for-of body closes a user iterator before propagating,
// the body's error winning over one return() throws; new Map() of a user
// iterable adds each entry as it is yielded, so an endless iterator whose
// entry throws stops there.
func TestE2EForOfThrowClosesIterator(t *testing.T) {
	assertSameAsNodeCompatJS(t, `var count = 0;
var it = {};
it[Symbol.iterator] = function () {
  return { next: function () { return { value: 1, done: false }; }, return: function () { count++; return {}; } };
};
try { for (var x of it) { throw new Error("b"); } } catch (e) { console.log(e.message); }
console.log(count);
var bad = {};
bad[Symbol.iterator] = function () {
  return { next: function () { return { value: 1, done: false }; }, return: function () { count++; throw new Error("from return"); } };
};
try { for (var y of bad) { throw new Error("body"); } } catch (e) { console.log(e.message, count); }
var item = ["foo", "bar"];
Object.defineProperty(item, 1, { get: function () { throw new Error("boom"); } });
var endless = {};
endless[Symbol.iterator] = function () {
  return { next: function () { return { value: item, done: false }; }, return: function () { count++; } };
};
try { new Map(endless); } catch (e) { console.log(e.message, count); }
`)
}

// A module binding holding the box an any-argument call returns, read from
// a closure, is the module global the declaration stores.
func TestE2EAnyHeldModuleGlobalFromClosure(t *testing.T) {
	assertMultiFileOutput(t, map[string]string{"main.ts": `import path from "path";
const p: any = "/tmp";
const dest = path.resolve(p, "x");
(async () => { console.log(typeof dest, dest); })();
`}, "main.ts", "string /tmp/x")
}

// A method call through a member of a possibly-undefined object unboxes
// the receiver the generic getter reads.
func TestE2EMethodCallThroughOptionalMember(t *testing.T) {
	assertSameAsNodeCompatJS(t, `class P { on(e) { console.log("on", e); } }
class W { constructor() { this.process = new P(); } }
class C { constructor() { this.worker = new W(); } }
const c = new C();
c.worker.process.on("x");
`)
}

// spawn's env values of another type are stringified, not null-byte
// checked as strings; http's expected protocol falls back to the default
// agent's when the agent has none.
func TestE2ESpawnEnvNonStringValues(t *testing.T) {
	assertSameAsNodeCompatJS(t, `const { execFileSync } = require("child_process");
const out = execFileSync("sh", ["-c", "echo $N $B"], { env: { N: 0, B: true, PATH: process.env.PATH } });
console.log(String(out).trim());
`)
}

// A function with its own "use strict" directive is a strict caller: a
// sloppy callee's `caller` reads null during its call.
func TestE2EFunctionCallerOfStrictDirective(t *testing.T) {
	assertOutput(t, `function f() { "use strict"; return g(); }
function g() { const ga: any = g; return ga.caller; }
function h() { return g(); }
console.log(f() === null, h() === h);
`, "true true")
}

// A destructured member whose default calls a function returning nothing
// is typed void (as tsc infers it): it holds undefined.
func TestE2EDestructuringDefaultOfVoidCall(t *testing.T) {
	assertOutput(t, `function t(): void { throw new Error("t"); }
const f = function({ x = t() }) { return 1; };
try { f({}); } catch (e) { console.log("caught", (e as Error).message); }
const o: { y?: void } = {};
console.log(o.y);
`, "caught t\nundefined")
}

// An object literal's getter updates its own field through `this`.
func TestE2EObjectLiteralGetterUpdatesThisField(t *testing.T) {
	assertSameAsNode(t, `const o = { index: 0, get val() { this.index++; return 1 << this.index; } };
console.log(o.val, o.val, o.index);
`)
}

// A number's trailing dot belongs to the literal: `10.` is 10, `10..x`
// reads x of it, `1.e3` is 1000.
func TestE2ENumericLiteralTrailingDot(t *testing.T) {
	assertSameAsNodeCompatJS(t, `console.log(1.e3, 10..toString(), (10.), [1.,2.].length, 1 .toFixed(1));
`)
}

// A builtin method called on a primitive receiver works on its wrapper
// (whose index and length are read-only); null and undefined throw.
func TestE2EBuiltinMethodsOnPrimitiveReceivers(t *testing.T) {
	assertSameAsNodeCompatJS(t, `function t(name, f) { try { console.log(name, f()); } catch (e) { console.log(name, e.constructor.name); } }
t("pop-str", () => Array.prototype.pop.call("abc"));
t("push-str", () => Array.prototype.push.call("abc", 1));
t("join-str", () => Array.prototype.join.call("abc", "-"));
t("pop-undef", () => Array.prototype.pop.call(undefined));
t("values-undef", () => Array.prototype.values.call(undefined));
t("valueOf-null", () => Object.prototype.valueOf.call(null));
t("isProto-undef", () => Object.prototype.isPrototypeOf.call(undefined, {}));
t("isProto-prim", () => Object.prototype.isPrototypeOf.call(undefined, 1));
t("padEnd-sym", () => String.prototype.padEnd.call(Symbol()));
const w = Object("abc");
t("delete-index", function () { "use strict"; return delete w[2]; });
t("delete-past", function () { "use strict"; return delete w[5]; });
`)
}

// A for-await-of array pattern steps an iterator element only as far as
// it reads: `[]` never runs the generator, `[a]` takes one value.
func TestE2EForAwaitPatternStepsIterator(t *testing.T) {
	assertSameAsNodeCompatJS(t, `var n = 0;
async function f() {
  for await (const [] of [function*() { n += 1; yield 1; }()]) console.log("empty", n);
  for await (const [a] of [function*() { n += 10; yield 7; n += 100; }()]) console.log("one", a, n);
}
f().then(() => console.log("end", n));
`)
}

// A for-await over an array takes the async-from-sync iterator's ticks:
// two per element, plain or promise.
func TestE2EForAwaitArrayTicks(t *testing.T) {
	assertSameAsNodeCompatJS(t, `const log = [];
async function h() { for await (const x of [1, Promise.resolve(2), 3]) log.push("v" + x); }
h();
(async () => { for (let i = 0; i < 8; i++) { await 0; log.push("t" + i); } })();
setTimeout(() => console.log(log.join(" ")), 10);
`)
}

func TestE2EStringConversionSkipsUndefinedToString(t *testing.T) {
	assertSameAsNodeCompatJS(t, `const o = { toString: undefined, valueOf() { return 7; } };
console.log(String(o), `+"`${o}`"+`, String({}));
try { String({ toString: undefined }); } catch (e) { console.log(e.constructor.name, e.message); }
const g = { toString: undefined, get valueOf() { throw new RangeError("v"); } };
try { String(g); } catch (e) { console.log(e.constructor.name); }
`)
}

func TestE2EResizableBufferAccessorsOnPrototype(t *testing.T) {
	assertSameAsNodeCompatJS(t, `function t(name, f) { try { console.log(name, f()); } catch (e) { console.log(name, e.constructor.name); } }
t("mbl", () => ArrayBuffer.prototype.maxByteLength);
t("rs", () => ArrayBuffer.prototype.resizable);
t("gr", () => SharedArrayBuffer.prototype.growable);
const b = new ArrayBuffer(8, { maxByteLength: 16 });
b.resize(12);
console.log(b.resizable, b.maxByteLength, b.byteLength);
`)
}

func TestE2EFreezeRegExp(t *testing.T) {
	assertSameAsNodeCompatJS(t, `const r = new RegExp(); Object.freeze(r);
console.log(Object.isFrozen(r), Object.isFrozen(new RegExp()));
const s = /x/; Object.seal(s); console.log(Object.isSealed(s), Object.isExtensible(s));
`)
}

func TestE2EProxyDefinePropertyTrap(t *testing.T) {
	assertSameAsNodeCompatJS(t, `function t(name, f) { try { console.log(name, f()); } catch (e) { console.log(name, e.constructor.name); } }
const s = new Proxy({}, { defineProperty() { throw new RangeError(); } });
t("dp", () => Object.defineProperty(s, "a", { get() {} }));
t("dg", () => s.__defineGetter__("a", function () {}));
const f = new Proxy({}, { defineProperty() { return false; } });
t("false", () => Object.defineProperty(f, "a", { value: 1 }));
const p = new Proxy({}, {});
p.__defineGetter__("a", function () { return 5; });
console.log(p.a);
`)
}

func TestE2EPromiseCatchOnThenable(t *testing.T) {
	assertSameAsNodeCompatJS(t, `function t(name, f) { try { console.log(name, f()); } catch (e) { console.log(name, e.constructor.name); } }
t("c1", () => typeof Promise.prototype.catch.call({ then() { return 3; } }));
t("c2", () => Promise.prototype.catch.call({ then: 1 }));
t("c3", () => Promise.prototype.catch.call(undefined));
t("th", () => Promise.prototype.then.call({}));
`)
}

func TestE2EPromiseAllOfIterables(t *testing.T) {
	assertSameAsNodeCompatJS(t, `const x = JSON.parse("[1,2]");
Promise.all(x).then(v => console.log(v));
function* g() { yield 3; yield Promise.resolve(4); }
Promise.all(g()).then(v => console.log(v));
Promise.allSettled(new Set([5])).then(v => console.log(v.length));
console.log(Array.from(new ArrayBuffer(4)).length);
`)
}

func TestE2EUpdateDynamicKeyConvertsOnce(t *testing.T) {
	assertOutput(t, `var n = 0;
var base: any = {};
var prop: any = { toString: function() { n++; return 1; } };
++base[prop]; console.log(n);
base[prop]--; console.log(n, base[1]);
`, "1\n2 NaN")
}

func TestE2ERegExpInArrays(t *testing.T) {
	assertSameAsNodeCompatJS(t, `const a = [/ef/]; console.log(a[0].source, a[0].flags);
function f() { return [/ab/g, 0]; }
console.log(f()[0].source, f()[0].global);
const y = [/cd/][0]; const z = y; console.log(String(z));
`)
}

// A callback passed to a generic function is checked against the
// instantiation the other arguments fixed: its result must fit it, as tsc's
// second inference pass checks it (TS2345); an overloaded callee whose
// every overload's callback result disagrees matches none (TS2769).
func TestE2EGenericCallbackResultChecked(t *testing.T) {
	mustCompileError(t, `function red<U>(init: U, cb: (p: U) => U): U { return cb(init) }
console.log(red(0, (x) => "s"))
`, "argument of type '(x: number) => string' is not assignable")
	mustCompileError(t, `function red<U>(cb: (p: U) => U, init: U): U { return cb(init) }
console.log(red(function (x) { return "s" }, 0))
`, "is not assignable")
	mustCompileError(t, `const xs: number[] = [1, 2, 3]
console.log(xs.reduce((x, y) => "s", 0))
`, "no overload matches this call")
	assertSameAsNode(t, `const xs: number[] = [1, 2, 3]
console.log(xs.reduce((a, b) => a + b, 0), xs.reduce((a, b) => a + b), xs.reduce((s, n) => s + n, ""))
console.log(xs.reduce((acc, n) => { acc.push(n * 2); return acc }, [] as number[]))
console.log(xs.reduce<Record<string, number>>((o, n) => { o["k" + n] = n; return o }, {}))
`)
	assertSameAsNode(t, `function red<U>(init: U, cb: (p: U) => U): U { return cb(init) }
console.log(red(1, (x) => x + 1), red("a", (s) => s + "b"))
const pick = <T,>(xs: T[], f: (x: T) => boolean): T[] => xs.filter(f)
console.log(pick([1, 2, 3], (n) => n > 1))
`)
}

// An object spread keeps an index signature only where every part of the
// literal has one, as tsc's getSpreadType does: `{ ...env }` reads any key,
// `{ ...env, A: "x" }` only its own and the spread's declared members.
func TestE2EObjectSpreadIndexSignature(t *testing.T) {
	mustCompileError(t, `
const env: { [key: string]: string | undefined } = { HOME: "/root" };
const c = { ...env, NODE_ENV: "production" };
console.log(c.HOME);
`, "property 'HOME' does not exist")
	mustCompileError(t, `
const c = { ...process.env, NODE_ENV: "production" };
console.log(c.PATH);
`, "property 'PATH' does not exist")
	assertSameAsNode(t, `
const env: { [key: string]: string | undefined } = { HOME: "/root" };
const d = { ...env };
console.log(d.HOME, d.OTHER);
const both = { ...env, ...{ [String("k")]: "v" } as { [key: string]: string } };
console.log(both.HOME, both.k);
const p: NodeJS.ProcessEnv = { ...process.env, NODE_ENV: "production" };
console.log(p.NODE_ENV);
const e = { ...process.env };
console.log(typeof e.PATH);
`)
}

func TestE2EAmbientClassIsAGlobal(t *testing.T) {
	// `declare class` types a global the environment provides: constructing
	// it throws ReferenceError until something defines it.
	assertSameAsNode(t, `declare class Widget { constructor(n: number); size: number; }
function make() { return new Widget(3); }
try { make(); } catch (e) { console.log((e as Error).name, (e as Error).message); }
(globalThis as any).Widget = class { size: number; constructor(n: number) { this.size = n * 2; } };
console.log(make().size);
`)
}

func TestE2EContextualTypingThroughCalls(t *testing.T) {
	// A cast to the program's own type, the right operand of `??`/`||`
	// typed from the left, a context that is a type parameter's constraint,
	// a predicate-returning context's `this`, an iterable's element, and a
	// literal kept through the call's return-context inference.
	assertSameAsNode(t, `interface Shape { kind: string; area?: number }
const s = new Object() as Shape;
console.log(typeof s, s.kind === undefined);
function get(): null | ((x: string) => number) { return null; }
const len = get() ?? ((t) => t.length);
const len2 = get() || ((t) => t.length * 2);
console.log(len("Thessaloniki"), len2("Volos"));
function first<T extends [(n: number) => number]>(p: T): T { return p; }
console.log(first([(x) => x + 1])[0](41));
function pick<T extends { q: (n: number) => string }>(p: T): T { return p; }
console.log(pick({ q: (x) => "n=" + x }).q(7));
const it: Iterable<(x: string) => number> = [(s) => s.length];
for (const f of it) console.log(f("four"));
type Keep<C> = (this: C, x: string | null) => x is string;
function keep<C>(f: Keep<C>, ctx: C): string[] { return ["a", null, "b"].filter((v): v is string => f.call(ctx, v)); }
console.log(keep(function (x): x is string { return this.ok && x !== null; }, { ok: true }).join());
function invoke<T>(f: () => T): T { return f(); }
const level: 0 | 1 | 2 = invoke(() => 1);
type Mode = "fast" | "slow";
const mode: Promise<Mode> = Promise.resolve().then(() => "fast");
mode.then((m) => console.log(m, level));
type Tagged<T> = string & { tag?: T };
const tagged: Tagged<number> = "plain";
console.log(tagged.toUpperCase());
`)
}

func TestE2EAmbientClassMethodOverloadsAndThis(t *testing.T) {
	// A declare class's repeated method is an overload set; a function
	// expression passed to it takes the receiver its context declares.
	assertSameAsNode(t, `declare class Registry<T> {
    each<C>(fn: (this: C, item: T) => void, ctx: C): void;
    each(fn: (item: T) => void): void;
}
(globalThis as any).Registry = class {
    items = ["Thessaloniki", "Kavala"];
    each(fn: any, ctx?: any) { for (const i of this.items) fn.call(ctx, i); }
};
const r = new Registry<string>();
r.each(function (item) { console.log(this.prefix + item); }, { prefix: "> " });
r.each((item) => console.log(item.length));
class Holder {
    c: { stop(): void } | undefined = undefined;
    run() {
        try { this.c = { stop() { console.log("stopped"); } }; throw new Error("x"); }
        catch { if (this.c !== undefined) this.c.stop(); }
    }
}
new Holder().run();
`)
}

func TestStrictImplicitAnyFidelity(t *testing.T) {
	// TS7010, TS7005, TS7017, TS2332: what tsc --strict reports.
	cases := []struct{ src, want string }{
		{"declare function f();\n", "'f', which lacks return-type annotation"},
		{"interface I { m(); }\n", "'m', which lacks return-type annotation"},
		{"declare var v;\n", "variable 'v' implicitly has an 'any' type"},
		{"console.log(globalThis.nope);\n", "'typeof globalThis' has no index signature"},
		{"enum E { A = this }\n", "'this' cannot be referenced in current location"},
		{"namespace N { export const t = this; }\n", "'this' cannot be referenced in a module or namespace body"},
		{"let z = 3;\nconsole.log(globalThis.z);\n", "property 'z' does not exist on type 'typeof globalThis'"},
		{"const f = () => this.window;\n", "captures the global value of 'this'"},
	}
	for _, tc := range cases {
		_, err := parseAndCompile(tc.src)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%q: expected %q, got: %v", tc.src, tc.want, err)
		}
	}
	// A private member of an ambient class is never reported, nor a
	// script's `var` read through the global object (which, in a file Node
	// runs as CommonJS, holds no such property).
	if _, err := parseAndCompile("declare class A { private m(x); }\nconsole.log(1);\n"); err != nil {
		t.Errorf("private ambient member reported: %v", err)
	}
	assertSameAsNode(t, "var x = 1;\nconsole.log(globalThis.x, globalThis['x'], globalThis['setTimeout'] === setTimeout);\n")
}

func TestE2EDiscriminatedObjectLiteralContext(t *testing.T) {
	// A union context is narrowed by the literal's discriminant (a constant
	// template too): the callback's parameter is that member's.
	assertSameAsNode(t, `type S = { d: "s"; cb: (x: string) => void };
type N = { d: "n"; cb: (x: number) => void };
function foo(o: S | N): void { if (o.d === "s") o.cb("Thessaloniki"); else o.cb(42); }
foo({ d: "s", cb: (x) => { console.log(x.toUpperCase()); } });
foo({ d: "n", cb: (x) => { console.log(x.toFixed(1)); } });
foo({ d: `+"`${\"s\"}`"+`, cb: (x) => { console.log(x.length); } });
`)
}

func TestE2EUnionOfArraysMethods(t *testing.T) {
	// A method of a union of array types reads off an array of their
	// elements; `x || []` beside a non-array keeps the empty literal.
	assertSameAsNode(t, `const words = "Thessaloniki Volos".match(/\w+/g);
console.log((words || []).map((s) => s.toLowerCase()).join("|"));
const none = "".match(/x/);
const y = none || [];
console.log(y.map((s) => s.length).length);
function pick(n: boolean): string[] | number[] { return n ? [1, 2] : ["a"]; }
console.log(pick(true).map((v) => typeof v).join(), pick(false).filter((v) => v !== "b").length);
`)
}

// An annotated class field types its initializer; a type parameter's value
// reads its constraint's members; a callback capturing `this` inside a
// dynamically dispatched call shares one cell across the call's branches.
func TestE2EClassFieldContextAndConstraintMembers(t *testing.T) {
	assertSameAsNode(t, `type F = (r: { a: number }) => number;
type G = <W extends { a: number }>(r: W) => number;
const g: G = (r) => r.a * 2;
class R {
  a = 7;
  f: F = (r) => r.a;
  cs: F[] = [(r) => r.a * 2];
  gs: G[] = [g];
  run() { this.cs.forEach(c => console.log(c(this))); this.gs.forEach(h => console.log(h(this))); }
}
const r = new R();
console.log(r.f({ a: 1 }), r.cs[0]({ a: 4 }));
r.run();
`)
}

// An enum member names its literal type (`p: E.A`), and a conditional type
// infers from a function type's `this` parameter.
func TestE2EEnumMemberTypeAndThisInfer(t *testing.T) {
	// (Node's type stripping has no enums: the expected output is tsc's.)
	assertOutputImports(t, `enum E { A, B, C }
function f2(p4: E.A = E.A, q: E.C = E.C) { return [p4, q]; }
type This<T> = T extends (this: infer U, ...args: any[]) => any ? U : unknown;
const n: This<(this: number) => void> = 5;
console.log(f2(), n);
`, "[ 0, 2 ] 5\n")
}

// An import-equals alias names a namespace's interface or enum as a type,
// at the top level and inside another namespace.
func TestE2EImportEqualsAliasAsType(t *testing.T) {
	assertOutputImports(t, `namespace a { export interface I { n: number } }
namespace Keyboard { export enum Key { UP, DOWN } }
import b = a.I;
var x: b = { n: 1 };
namespace App {
  import Key = Keyboard.Key;
  import J = a.I;
  export function foo(key: Key, j: J): void { console.log(key, j.n); }
  foo(Key.DOWN, x);
}
`, "1 1\n")
}

// A host value (a Date, a Map, a typed array) read through a structural type
// or a member-preserving mapping of its own is the same object: its members
// are its own, with itself as `this`.
func TestE2EHostValueThroughStructuralType(t *testing.T) {
	assertSameAsNode(t, `interface HasSize { readonly size: number; get(k: string): number | undefined }
function size(b: HasSize) { return [b.size, b.get("a")]; }
function time(b: { getTime(): number }) { return b.getTime(); }
const o: { getTime(): number } = new Date(7);
let p: { getTime(): number } = new Date(8);
const a: any = new Date(9);
const q: { getTime(): number } = a;
function ro(d: Readonly<Date>, m: Readonly<Map<string, number>>, f: Readonly<Float32Array>) { return [d.getTime(), m.size, f[0]]; }
console.log(size(new Map([["a", 1]])), time(new Date(5)), o.getTime(), p.getTime(), q.getTime());
console.log(ro(new Date(3), new Map([["b", 2]]), new Float32Array([2])), time(o));
`)
}

// An enum member discriminates a union as a plain literal of its value does
// (an object literal's member, `===` narrowing, a template case), and a
// mapping over enum keys of one value merges them.
func TestE2EEnumMemberDiscriminants(t *testing.T) {
	assertOutputImports(t, `enum K { P = "P", D = "D" }
interface D { type: K.D; alts: string[] }
interface P { type: K.P; els: string[] }
function f(n: D | P) {
  if (n.type === "D") { console.log(n.alts.slice()); } else { console.log(n.els.slice()); }
  switch (n.type) {
    case `+"`${K.D}`"+`: console.log("d", n.alts.length); break;
    case K.P: console.log("p", n.els.length); break;
  }
}
f({ type: K.D, alts: ["a"] });
f({ type: K.P, els: ["b", "c"] });
function g(n: D | P) { console.log(n); }
g({ alts: ["z"], type: K.D });
enum A { X = "x" }
enum B { X = "x", Y = "y" }
type M = { [V in A | B]: V[] };
const m: M = { x: [A.X, B.X], y: [B.Y] };
console.log(m.x.length, m.y.length);
`, "[ 'a' ]\nd 1\n[ 'b', 'c' ]\np 2\n{ alts: [ 'z' ], type: 'D' }\n2 1\n")
}

// Intersections reduce as tsc reduces them: primitives and literals to
// their common subtype or never, null with an object to never, a union
// member distributed, and conflicting literal properties to never.
func TestStrictIntersectionReduction(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{"type T = string & never;\nconst a: T = 1;\n", "type 'number' is not assignable to type 'never'"},
		{"type T = string & number;\nconst a: T = 1;\n", "type 'number' is not assignable to type 'never'"},
		{"type T = (\"a\" | \"b\") & (\"b\" | \"c\");\nconst a: T = \"a\";\n", "type '\"a\"' is not assignable to type '\"b\"'"},
		{"type T = null & { a: string };\nconst a: T = null;\n", "type 'null' is not assignable to type 'never'"},
		{"type T = { x: true } & { x: false };\nconst a: T = { x: true };\n", "is not assignable to type 'never'"},
	} {
		_, err := parseAndCompile(c.src)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q: got %v, want %q", c.src, err, c.want)
		}
	}
	assertSameAsNode(t, `interface X { a?: { aProp: string }; b?: { bProp: string } }
type AorB = { a: object; b: undefined } | { a: undefined; b: object };
function f(q: X & AorB) { return q.a !== undefined ? "a" : "b"; }
console.log(f({ a: { aProp: "p" }, b: undefined }), f({ a: undefined, b: { bProp: "q" } }));
`)
}

// An interface of overloaded call signatures calls with the arguments as
// written, and an interface of a construct signature holds a class.
func TestE2ECallableAndConstructableInterfaces(t *testing.T) {
	assertSameAsNode(t, `interface Ov { (): number; (v: number): string }
const f: Ov = ((v?: number) => v === undefined ? 1 : "s" + v) as Ov;
console.log(f(), f(2));
interface Ctor { new (x: number): { x: number } }
class K { x: number; constructor(x: number) { this.x = x; } }
const C: Ctor = K;
console.log(new C(3).x);
interface Obs<T> { (): T; (value: T): any }
const o: Obs<boolean> = ((v?: boolean) => v === undefined ? true : "set") as Obs<boolean>;
console.log(o(), o(false));
interface A1 { f(x: number): number; f(x: string): string }
const a1: A1 = { f: (x: any) => x };
console.log(a1.f(1), a1.f("s"));
interface A3<T> { h(x: T): T; h(x: string, y: number): string }
const a3: A3<number> = { h: (x: any, y?: any) => y === undefined ? x : x + y };
console.log(a3.h(1), a3.h("s", 2));
`)
}

// A mapped alias reached from its own value (`Bar<any>`'s conditional)
// resolves as an object of itself, and its dictionary representation holds
// the inner occurrence as a box.
func TestE2ERecursiveMappedAliasOverAny(t *testing.T) {
	assertSameAsNode(t, `type Bar<V> = { [K in keyof V]: V[K] extends object ? Bar<V[K]> : string };
const b: Bar<any> = {};
b.x = "s";
console.log(Object.keys(b), b.x);
`)
}

// An interface method returning `this`, generic or not, returns the object
// it is called on.
func TestE2EInterfaceMethodReturningThis(t *testing.T) {
	assertSameAsNode(t, `interface MyPromise<T> {
  then(cb: (t: T) => void): void;
  again(): this;
}
const p: MyPromise<number> = { then(cb) { cb(1); }, again() { return this; } };
p.again().then(x => console.log(x));
interface Plain { n: number; self(): this }
const q: Plain = { n: 2, self() { return this; } };
console.log(q.self().n);
`)
}

// A type literal's accessors are properties to their readers: an accessor
// object's getter and setter answer them, a plain data property too, and a
// union discriminates on a getter's literal type.
func TestE2ETypeLiteralAccessors(t *testing.T) {
	assertSameAsNode(t, `type Box = { get done(): boolean; set done(v: boolean) };
let store = false;
const a: Box = { get done() { return store; }, set done(v) { store = v; } };
a.done = true;
console.log(a.done, store);
const b: Box = { done: false };
b.done = true;
console.log(b.done, b);
type W = { get done(): false; set done(v: number | null) } | { get done(): true; set done(v: number | null); value: number };
function show(w: W) { if (w.done) { console.log("v", w.value); } else { console.log("not done"); } }
show({ done: true, value: 3 } as W);
show({ get done() { return false as const; }, set done(v: number | null) {} });
`)
}

// An element access by a key of type parameter type is the indexed access
// `S[K]`, evaluated once K is bound; an erased generic arrow's reads the
// object by its run-time key. A generic arrow's constraint names the
// file's own declarations.
func TestE2EGenericKeyElementAccess(t *testing.T) {
	assertSameAsNode(t, `const src = { content: 5, name: "n" };
function g(k: "content" | "name") { return src[k]; }
console.log(g("content"), g("name"));
function h<K extends keyof typeof src>(k: K) { return src[k]; }
const a: number = h("content");
console.log(a, h("name"));
const f = <K extends keyof typeof src>(k: K) => src[k];
console.log(f("content"), f("name"));
type Data<T> = { get: <K extends keyof T>(prop: K) => T[K] };
class Parent<M> {
  data: Data<M>;
  constructor(data: Data<M>) { this.data = data; }
  getData(): Data<M> { return this.data; }
}
const p = new Parent<{ content: number; name: string }>({ get: (k) => src[k] });
console.log(p.getData().get("content"), p.getData().get("name"));
const d: Data<{ a: number }> = { get: (k) => ({ a: 7 })[k] };
console.log(d.get("a"));
`)
	for _, c := range []struct{ src, want string }{
		{"const src = { content: 5 };\nfunction h<K extends keyof typeof src>(k: K) { return src[k]; }\nconst c: string = h(\"content\");\n", "type 'number' is not assignable to type 'string'"},
		{"const src = { content: 5 };\nconst k: any = \"content\";\nconsole.log(src[k]);\n", "expression of type 'any' can't be used to index type"},
	} {
		_, err := parseAndCompile(c.src)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q: got %v, want %q", c.src, err, c.want)
		}
	}
}

// A callback's context through a type parameter's index-signature
// constraint, with what the other arguments inferred; a literal's other
// properties infer before a callback beside them (tsc's intra-expression
// inference), returned from an arrow too; a template expression
// discriminates its object's union context.
func TestE2ECallbackContextInference(t *testing.T) {
	assertSameAsNode(t, `function test1<C, M extends Record<string, (ctx: C, ...args: never[]) => unknown>>(context: C, methods: M): M { return methods; }
const m = test1({ count: 0 }, { checkLimit: (ctx, max = 500) => ctx.count + max, hasAccess: (ctx, user: { name: string }) => user.name });
console.log(m.checkLimit({ count: 1 }), m.hasAccess({ count: 0 }, { name: "u" }));
interface Foo<A> { a: A; b: (x: A) => void; }
function infer<A>(fn: () => Foo<A>): A { const f = fn(); f.b(f.a); return f.a; }
const result = infer(() => ({ a: { BLAH: 33 }, b: x => { console.log(x.BLAH); } }));
console.log(result.BLAH);
function infer2<A>(foo: Foo<A>): A { foo.b(foo.a); return foo.a; }
const r2 = infer2({ a: { n: 1 }, b: x => { console.log(x.n + 1); } });
console.log(r2.n);
type Link = { href: string; onClick?: (event: string) => void };
type Button = { href?: never; onClick?: (event: number) => void };
const p3: Link | Button = { href: `+"`2${undefined}3`"+`, onClick: (ev) => console.log("ev", ev.length) };
if (p3.onClick) p3.onClick("abc");
`)
	for _, c := range []struct{ src, want string }{
		{"interface Foo<A> { a: A; b: (x: A) => void; }\ndeclare function infer2<A>(foo: Foo<A>): A;\ninfer2({ a: { n: 1 }, b: x => { const z: string = x; } });\n", "type '{ n: number; }' is not assignable to type 'string'"},
		{"declare function infer<A>(fn: () => [A]): A;\nconst r: string = infer(() => [1]);\n", "type 'number' is not assignable to type 'string'"},
	} {
		_, err := parseAndCompile(c.src)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q: got %v, want %q", c.src, err, c.want)
		}
	}
}

// A union spread distributes per member; an omitted optional discriminant
// selects its member; a generic mapped parameter gives its template as a
// callback's context; a function-valued property infers only what nothing
// else does.
func TestE2EUnionSpreadAndMappedContext(t *testing.T) {
	assertSameAsNode(t, `interface S { ok: true; s: string } interface F { ok: false; n: number }
function g(a: (S | F)[]) { return a.map(item => ({ ...item, extra: 1 })).filter(value => value.ok); }
console.log(g([{ ok: true, s: "x" }, { ok: false, n: 2 }]));
const one: S | F = { ok: false, n: 3 };
const c2 = { ...one };
if (c2.ok) console.log(c2.s); else console.log(c2.n);
type T1 = { disc: true; cb: (x: string) => void };
type T2 = { disc?: false; cb: (x: number) => void };
function f(o: T1 | T2) { if (o.disc) o.cb("ab"); else o.cb(4); }
f({ cb: n => console.log(n.toFixed(1)) });
function mapped2<T extends { [P in keyof T]: (s: string) => number }>(obj: T): T { return obj; }
console.log(mapped2({ foo: s => s.length }).foo("abc"));
type Schema = { query?: unknown };
function route<TS extends Schema>(o: { pre: (a: TS) => void; schema: TS; handle: (q: TS["query"]) => void }) { o.pre(o.schema); o.handle(o.schema.query); }
route({ pre: (_: { query?: unknown }) => {}, schema: { query: "q" }, handle: q => { const s: string = q; console.log(s); } });
interface Id { kind: "id"; name: string }
function isId(n: unknown): n is Id { return typeof n === "object" && n !== null && (n as Id).kind === "id"; }
function mk<T, R>(p: { matcher: (n: unknown) => n is T; extract: (n: T) => R }): (n: unknown) => R | undefined { return n => p.matcher(n) ? p.extract(n) : undefined; }
const ex = mk({ matcher: isId, extract: node => ({ node, v: node.name }) });
console.log(ex({ kind: "id", name: "q" })?.v, ex(3));
`)
}

// A return in an unannotated function expression, a generator's included,
// takes its contextual signature's result; a destructuring pattern's
// defaults are its initializer's context; a type parameter's default may
// name an earlier one.
func TestE2EContextualReturnsAndPatternDefaults(t *testing.T) {
	assertSameAsNode(t, `const f: () => (n: number) => void = function () { return (n) => console.log(n + 1); };
f()(2);
type G = () => Generator<string, (arg: number) => void, undefined>;
const g: G = function* () { yield "y"; return (num) => console.log(num * 2); };
const it = g(); console.log(it.next().value); const r = it.next(); if (r.done) r.value(4);
function id<T>(x: T): T { return x; }
const { h = (x: string) => x.length } = id({ h: x => x.charAt(1) });
console.log(h("ab"));
interface Res<T = never> { data: T }
function get<T = never, Q = Res<T>>(v: unknown): Q { return { data: v } as Q; }
const got = get<number>(7);
console.log(got.data + 1);
`)
	_, err := parseAndCompile("interface Res<T = never> { data: T }\ndeclare function get<T = never, Q = Res<T>>(): Q;\nconst b: string = get<number>().data;\n")
	if err == nil || !strings.Contains(err.Error(), "type 'number' is not assignable to type 'string'") {
		t.Errorf("default naming an earlier type parameter: got %v", err)
	}
}

// A generic callable interface is its instantiated signature; an
// uninferred type parameter its constraint doesn't admit `unknown` for is
// the constraint; a conditional over a deferred mapped argument evaluates
// once bound; `infer` inside a mapped pattern; a mapped type over any keys.
func TestE2EGenericCallableAndConstraintFallback(t *testing.T) {
	assertSameAsNode(t, `interface Make<T> { (): T }
function foo<T>(i: Make<T>) { return i(); }
console.log(foo(() => "hi"));
const mk: Make<number> = () => 3;
console.log(mk() + 1);
type U<O extends object> = O extends object ? { [K in keyof O]: O[K] } : never;
class Table<S extends Record<string, unknown>> {
  row: S;
  constructor(row: S) { this.row = row; }
  rows<C extends keyof S>(): Array<U<Pick<S, C>>> { return [this.row as any]; }
}
console.log(new Table({ a: 1 }).rows()[0].a);
type V<T> = T extends { [_ in keyof T]: infer U } ? U : never;
const v: V<{ a: 1; b: 2 }> = 2;
console.log(v);
const rec: Record<any, number> = { x: 1 };
console.log(rec.x);
`)
	for _, c := range []struct{ src, want string }{
		{"declare class T<S> { pk<C extends keyof S>(): Pick<S, C>[] }\nconst n: number = new T<{ a: number }>().pk();\n", "type '{ a: number; }[]' is not assignable to type 'number'"},
		{"type V<T> = T extends { [_ in keyof T]: infer U } ? U : never;\nconst v: V<{ a: 1; b: 2 }> = 1; const s: string = v;\n", "is not assignable to type 'string'"},
		{"type U<O> = O;\ntype K = 1;\ntype V<T> = T extends { a: infer U } ? { [K in keyof T]: U } : never;\nconst v: V<{ a: 1 }> = { a: 1 }; const s: string = v.a;\n", "type 'number' is not assignable to type 'string'"},
	} {
		_, err := parseAndCompile(c.src)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q: got %v, want %q", c.src, err, c.want)
		}
	}
}

// An interface may extend an alias of an object type, itself through it
// too; an optional property's undefined is not related to an index
// signature.
func TestE2EInterfaceExtendsAlias(t *testing.T) {
	assertSameAsNode(t, `interface Q extends Record<string, Q> {}
const q: Q = { a: {} };
q.b = { c: {} };
console.log(Object.keys(q), Object.keys(q.b));
interface P extends Partial<{ x: number; y: string }> { z: boolean }
const p: P = { z: true, x: 2 };
console.log(p.x, p.y, p.z);
type Refs<K extends string> = { [P in K]: number };
type SI<K extends string> = Refs<K> & { prepare: () => { type: K } };
function setup<K extends string>(keys: K[]): SI<K> { const o: any = { prepare: () => ({ type: keys[0] }) }; for (const k of keys) o[k] = k.length; return o; }
const { prepare, ...rest } = setup(["image", "ab"]);
console.log(prepare().type, rest);
const foo: {} | undefined = Math.random() < 2 ? {} : undefined;
const rr: Q = {};
rr.propA = { ...(foo !== undefined ? { foo } : {}) };
rr.propC = { ...(foo !== undefined && { foo }) };
console.log(Object.keys(rr));
`)
	_, err := parseAndCompile("const o2: { a?: string } = {};\nconst r2: Record<string, number> = o2;\n")
	if err == nil || !strings.Contains(err.Error(), "is not assignable to type") {
		t.Errorf("optional string property against a number index: got %v", err)
	}
}

// A recursive alias whose self-reference is an interface's type argument
// (`T | Box<R<T>>`) types, infers through, and runs as a box; a variable's
// `typeof` in its own initializer is TS7022.
func TestE2ERecursiveUnionAlias(t *testing.T) {
	assertSameAsNode(t, `interface Box<T> { __: T }
type Recursive<T> = T | Box<Recursive<T>>;
const r: Recursive<number> = { __: { __: 1 } };
console.log(JSON.stringify(r));
function depth(x: Recursive<number>): number { return typeof x === "number" ? 0 : 1 + depth(x.__); }
console.log(depth(r), depth(7));
`)
	_, err := parseAndCompile("interface Box<T> { __: T }\ntype R<T> = T | Box<R<T>>;\ntype I<T> = T extends R<infer X> ? X : never;\nconst a: I<Box<string | Box<number>>> = 1 as any;\nconst n: null = a;\n")
	if err == nil || !strings.Contains(err.Error(), "is not assignable to type 'null'") {
		t.Errorf("inference through a recursive alias: got %v", err)
	}
}

// The `Equals` idiom relates two generic signatures and their deferred
// conditional results; a local type alias names the file's types; a
// `typeof` reads its name as narrowed where written; `this is` guards
// intersect, conflicting literals giving never.
func TestStrictEqualsIdiomAndNarrowedTypeof(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{"type Eq<A, B> = (<T>() => T extends A ? 1 : 0) extends (<T>() => T extends B ? 1 : 0) ? true : false;\nconst a: Eq<string, string> = false;\n", "type 'false' is not assignable to type 'true'"},
		{"type Eq<A, B> = (<T>() => T extends A ? 1 : 0) extends (<T>() => T extends B ? 1 : 0) ? true : false;\nconst e: Eq<any, unknown> = true;\n", "type 'true' is not assignable to type 'false'"},
		{"type Eq<A, B> = (<T>() => T extends A ? 1 : 0) extends (<T>() => T extends B ? 1 : 0) ? true : false;\nfunction f() { type N = Eq<1, 1>; const b: N = false; }\n", "type 'false' is not assignable to type 'true'"},
		{"function f(v: string | number) { if (typeof v === \"string\") { type X = typeof v; const n: X = 1; } }\n", "type 'number' is not assignable to type 'string'"},
		{"declare class S<C> { c: C; m<V extends string>(v: V): this is S<C> & { value: V } }\nfunction g(s: S<number>) { if (s.m(\"a\")) { const q: null = s; } }\n", "is not assignable to type 'null'"},
	} {
		_, err := parseAndCompile(c.src)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q: got %v, want %q", c.src, err, c.want)
		}
	}
	for _, src := range []string{
		"declare class S<C> { c: C; m<V extends string>(v: V): this is S<C> & { value: V } }\nfunction g(s: S<number>) { if (s.m(\"a\") && s.m(\"b\")) { const r: null = s; } }\n",
		"type Eq<A, B> = (<T>() => T extends A ? 1 : 0) extends (<T>() => T extends B ? 1 : 0) ? true : false;\nconst c: Eq<{ x: 1 }, { x: 1 }> = true;\nconst d: Eq<Date, Date | null> = false;\n",
	} {
		if _, err := parseAndCompile(src); err != nil && !strings.Contains(err.Error(), "codegen") {
			t.Errorf("%q: unexpected %v", src, err)
		}
	}
}

// `typeof` of a namespace, an enum, a namespace alias and a class's own
// static member; an assertion's type naming a type parameter in scope.
func TestE2ETypeofValuesAndScopedAssertions(t *testing.T) {
	out := compileAndRun(t, `namespace A { export var o = 3; export function f() { return 2 } }
namespace C { import a = A; export var m: typeof a = A; }
enum Days { mon, tue }
class K { static g(t: typeof K.g) { return 7 } }
const t: typeof A = A;
const d: typeof Days = Days;
const id = <T>(x: any) => x as T;
console.log(t.o, t.f(), C.m.o, d.tue, d[0], id<number>(5));
`)
	if out != "3 2 3 1 mon 5" {
		t.Errorf("got %q", out)
	}
	for _, c := range []struct{ src, want string }{
		{"namespace A { export var o = 3; }\ndeclare const t: typeof A; const s: string = t.o;\n", "type 'number' is not assignable to type 'string'"},
		{"const q = <T>(x: any) => x as T; const y: null = q;\n", "is not assignable to type 'null'"},
	} {
		_, err := parseAndCompile(c.src)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q: got %v, want %q", c.src, err, c.want)
		}
	}
}

// A type parameter goes only where its constraint (unknown) goes; `this` in
// a field's arrow names the class, outside a non-static member it is
// TS2526; a generator in a union context returns to its generator members;
// a union of callables intersects object parameters; inference reaches
// through nested deferred conditionals.
func TestStrictTypeParamSourceThisTypeAndUnionCalls(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{"function q<T>(y: T) { const z: string = y; }\n", "type 'T' is not assignable to type 'string'"},
		{"function q<T extends string>(y: T) { const z: number = y; }\n", "type 'T' is not assignable to type 'number'"},
		{"class C { static m(): this { return 1 as any } }\n", "a 'this' type is available only in a non-static member"},
		{"type G = () => (number | Generator<string, (arg: number) => void, undefined>);\nconst g: G = function* () { return (n) => { const s: string = n; }; }\n", "type 'number' is not assignable to type 'string'"},
		{"declare const v: ((a: {x: string}) => string) | ((a: {y: string}) => string); v({x: \"\"});\n", "argument of type '{ x: string; }' is not assignable"},
		{"type C1<T> = T extends unknown ? T | undefined : never;\ntype C2<T> = T extends unknown ? T | undefined : never;\ntype C3<T> = T extends unknown ? T | undefined : never;\ndeclare function f3<T>(x: C1<C2<C3<T>>>): [T];\nconst z: null = f3(7);\n", "type '[number]' is not assignable to type 'null'"},
	} {
		_, err := parseAndCompile(c.src)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q: got %v, want %q", c.src, err, c.want)
		}
	}
	for _, src := range []string{
		"function q<T>(y: T) { const z: unknown = y; const w: T | null = y; const v: {} | null | undefined = y; }\n",
		"class X { f = (): this => this; g = async (): Promise<this> => this; }\n",
	} {
		if _, err := parseAndCompile(src); err != nil && !strings.Contains(err.Error(), "codegen") {
			t.Errorf("%q: unexpected %v", src, err)
		}
	}
}

// Element access by a constant key: a primitive's member (its apparent
// interface's, then Object's), a union's shared property, an empty enum's
// reverse mapping, a function's expando member; `object` by a computed key
// is TS7053 at the receiver.
func TestE2EConstantKeyElementAccess(t *testing.T) {
	out := compileAndRun(t, `var x = 'abc';
enum E { }
function foo() {}
const k = "kk";
foo[k] = "ok";
interface A { kind: 'A'; a: number } interface B { kind: 'B'; b: string }
function pick(v: A | B) { return v['kind'] === 'B' ? v.b : String(v.a); }
console.log(x['charAt'](1), x['length'], x['hasOwnProperty']('length'), true['toString'](), (5)['toFixed'](1), E[0], foo[k], pick({ kind: 'B', b: 'bb' }));
`)
	if out != "b 3 true true 5.0 undefined ok bb" {
		t.Errorf("got %q", out)
	}
	_, err := parseAndCompile("var a: object = {};\nfor (var key in a) { var value = a[key]; }\n")
	if err == nil || !strings.Contains(err.Error(), "2:34: element implicitly has an 'any' type") {
		t.Errorf("object key: got %v", err)
	}
}

// A class member whose inferred result reads itself is TS7023 at its name,
// every member on the cycle; a function returned from an unannotated one,
// or from an accessor, has implicitly-any parameters; `[...[]]` is never[];
// a class iterating as its own iterator spreads its `next` values.
func TestE2EMemberCyclesAndReturnedFunctions(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{"class C { get p() { return this.p; } }\n", "1:15: 'p' implicitly has return type 'any'"},
		{"class C { m() { return this.m(); } }\n", "1:11: 'm' implicitly has return type 'any'"},
		{"class C { get p() { return this.q; } get q() { return this.p; } }\n", "1:42: 'q' implicitly has return type 'any'"},
		{"function f() { return (s) => 1; }\n", "1:24: parameter 's' implicitly has an 'any' type"},
		{"var x: { foo: (x: number) => number };\nx = { get foo() { return (n) => n }, set foo(v) {} };\n", "2:27: parameter 'n' implicitly has an 'any' type"},
		{"var a = [...[...[]]]; const z: null = a;\n", "type 'never[]' is not assignable to type 'null'"},
	} {
		_, err := parseAndCompile(c.src)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q: got %v, want %q", c.src, err, c.want)
		}
	}
	out := compileAndRun(t, `class It {
  i = 0;
  next() { this.i++; return { value: this.i * 10, done: this.i > 3 }; }
  [Symbol.iterator]() { return this; }
}
const a = [...new It];
console.log(a.join(","), a.length);
`)
	if out != "10,20,30 3" {
		t.Errorf("iterator spread: got %q", out)
	}
}

// A callable with properties (`F & Meta`, `Object.assign(fn, {…})`) is a
// function object, held boxed and `typeof` "function"; an interface with
// members beside an index signature is an open object; a type literal's
// bracketed literal or well-known symbol key names its member.
func TestE2ECallableObjectsAndOpenInterfaces(t *testing.T) {
	out := compileAndRun(t, `type Meta = { foo: string }
interface F { (): string }
function f1(a: (F & Meta) | string) { if (typeof a === "function") { console.log(a(), a.foo); } else { console.log(a.length); } }
f1(Object.assign(() => "hi", { foo: "m" })); f1("abc");
const g = Object.assign(function (n: number) { return n * 2 }, { tag: "t" });
console.log(g(4), g.tag, typeof g);
interface O { m(x: number): number; [key: string]: unknown; }
const o: O = { m(x) { return x + 1 }, extra: 5 };
console.log(o.m(2), o["extra"]);
declare const lit: { ["a b"]: string; [Symbol.toPrimitive]?: string };
`)
	if out != "hi m\n3\n8 t function\n3 5" {
		t.Errorf("got %q", out)
	}
	_, err := parseAndCompile("declare const x: { [\"ab\"]: string; [0]: number }; const w: null = x[0];\n")
	if err == nil || !strings.Contains(err.Error(), "type 'number' is not assignable to type 'null'") {
		t.Errorf("literal key: got %v", err)
	}
}

// A tuple with a trailing rest element (`[string, ...number[]]`) types
// each leading position and its rest, through indexing, destructuring
// (of a union of them too) and assignment.
func TestE2ERestTuples(t *testing.T) {
	out := compileAndRun(t, `type Expression = ['and', ...Expression[]] | ['not', Expression] | 'true' | 'false';
function evaluate(expression: Expression): boolean {
  if (Array.isArray(expression)) {
    const [operator, ...operands] = expression;
    if (operator === 'and') return operands.every((child) => evaluate(child));
    return !evaluate(operands[0]);
  }
  return expression === 'true';
}
const g: [string, ...number[]] = ["a", 1, 2];
const [h, ...rest] = g;
console.log(evaluate(["and", "true", ["not", "false"]]), h.toUpperCase(), rest[1] + 1, g.length);
`)
	if out != "true A 3 3" {
		t.Errorf("got %q", out)
	}
	for src, want := range map[string]string{
		"declare const e: [string, ...number[]]; const b: null = e[0];":                  "type 'string' is not assignable to type 'null'",
		"const x: [string, ...number[]] = [];":                                           "type '[]' is not assignable to type '[string, ...number[]]'",
		"const a: number[] = [1]; const x: [string, ...number[]] = a;":                   "type 'number[]' is not assignable to type '[string, ...number[]]'",
		"declare const e: [string, ...number[]]; const [, ...t] = e; const n: null = t;": "type 'number[]' is not assignable to type 'null'",
	} {
		if _, err := parseAndCompile(src + "\n"); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v", src, err)
		}
	}
}

// An array return inferred from a `ReadonlyArray<X>` context, and a
// conditional whose branches both fit the target, are accepted, as tsc
// accepts them.
func TestStrictReadonlyArrayContextAndConditionalBranches(t *testing.T) {
	for _, src := range []string{
		"declare function from<T>(): T[];\nconst c2: ReadonlyArray<string> = from();\n",
		"type One<T> = T extends string ? string : string;\ntype A<T> = T extends number ? One<T> : never;\nfunction f<T>() { const x: One<T> = null as any as A<T>; return x; }\n",
	} {
		if _, err := parseAndCompile(src); err != nil {
			t.Errorf("%q: %v", src, err)
		}
	}
	_, err := parseAndCompile("type One<T> = T extends string ? string : number;\ntype A<T> = T extends number ? boolean : never;\nfunction f<T>() { const x: One<T> = null as any as A<T>; return x; }\n")
	if err == nil || !strings.Contains(err.Error(), "is not assignable") {
		t.Errorf("mismatched branches: got %v", err)
	}
}

// Context-sensitive arguments infer left to right; a union context's
// callable member is the signature; an async body meets its context
// awaited; `this` in a literal's context is that context; a tuple spreads
// into a tuple type; an alias the program never reads compiles.
func TestE2ESequentialCallbacksAndAsyncContexts(t *testing.T) {
	out := compileAndRun(t, `interface Int<T, U> { method(x: T): U; }
function foo<T, U>(x: T, y: Int<T, U>, z: Int<U, T>): T { return z.method(y.method(x)); }
console.log(foo("abc", { method: (p1) => p1.length }, { method: (p2) => "n" + p2.toFixed(1) }));
type CB = (t: string) => void;
function h(cb: (v: boolean) => CB | PromiseLike<CB>) { const r = cb(true); if (typeof r === "function") r("hi"); }
h(v => v ? (s) => console.log(s.length) : Promise.reject());
const p: PromiseLike<number> = Promise.resolve(3);
async function g(cb: () => Promise<"a" | "b">) { console.log(await cb(), (await p) + 1); }
g(async () => "a");
interface X { a: (p: this) => this; n: number }
const x: X = { n: 7, a(q) { return q; } };
console.log(x.a(x).n);
type A = [string];
function spread(...args: [...A, ...A]) { return args[1]; }
console.log(spread("l", "r"));
type Unread = { [K in keyof [1, 2]]: K };
`)
	if out != "n3.0\n2\n7\nr\na 4" {
		t.Errorf("got %q", out)
	}
	for src, want := range map[string]string{
		"declare function foo<T, U>(x: T, y: (a: T) => U): U;\nconst r: string = foo(\"\", p => p.length);\n": "type 'number' is not assignable to type 'string'",
		"declare const p: PromiseLike<number>;\nasync function f() { const z: null = await p; }\n":            "type 'number' is not assignable to type 'null'",
	} {
		if _, err := parseAndCompile(src); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: got %v", src, err)
		}
	}
	for _, src := range []string{
		"declare function foo<T>(cb: (x: T) => void): T;\nconst r: string = foo(x => {});\n",
		"declare function g(cb: () => Promise<\"a\" | \"b\">): void;\ng(async () => { return \"a\"; });\n",
	} {
		if _, err := parseAndCompile(src); err != nil {
			t.Errorf("%q: %v", src, err)
		}
	}
}

// A callback inside a generic function takes that function's own type
// parameters as its context; a mapped type over a type parameter's keys
// infers them as the object's keys; an object rest of a type parameter is
// its Omit.
func TestE2EEnclosingTypeParamsAndKeyInference(t *testing.T) {
	out := compileAndRun(t, `function each<K>(keys: K[]) { const out: K[] = []; keys.forEach(k => out.push(k)); return out.map(k => [k]); }
function keys<K extends string, V>(obj: Record<K, V>): K[] { return Object.keys(obj) as K[]; }
function test<T extends { a: string }>(obj: T) { const { a, ...rest } = obj; return rest; }
const ks = keys({ fr: 1, en: 2 });
const first: "fr" | "en" = ks[0];
console.log(JSON.stringify(each([1, 2])), first, ks.length, JSON.stringify(test({ a: "x", n: 1 })));
`)
	if out != `[[1],[2]] fr 2 {"n":1}` {
		t.Errorf("got %q", out)
	}
	for src, want := range map[string]string{
		"function S<K extends string>(keys: K[]) { keys.forEach(key => { const z: null = key; }); }\n":           "type 'K' is not assignable to type 'null'",
		"declare function keys<K extends string, V>(obj: Record<K, V>): K[];\nconst z: null = keys({ a: 1 });\n": "type '\"a\"[]' is not assignable to type 'null'",
	} {
		if _, err := parseAndCompile(src); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: got %v", src, err)
		}
	}
}

// A tagged template resolves an overloaded or generic tag as a call over
// its strings array and substitutions; an ambient class's computed member
// key is never evaluated, so it reads no binding before its declaration.
func TestE2ETaggedTemplateOverloadsAndAmbientKeys(t *testing.T) {
	assertSameAsNode(t, `function tag<T>(s: TemplateStringsArray, v: T): T[] { return [v, v]; }
console.log(tag`+"`a${5}`"+`.length, tag<string>`+"`b${\"x\"}`"+`[1]);
function foo1(strs: TemplateStringsArray, x: number): string;
function foo1(strs: string[], x: number): number;
function foo1(...stuff: any[]): any { return Array.isArray(stuff[0]) ? "t" + stuff[1] : 0; }
const s: string = foo1`+"`${1}`"+`;
console.log(s);
declare class D { [k2]: number; }
const k2 = "x";
console.log(k2);
`)
	for src, want := range map[string]string{
		"function foo1(strs: TemplateStringsArray, x: number): string;\nfunction foo1(strs: string[], x: number): number;\nfunction foo1(...stuff: any[]): any { return 0; }\nconst a: null = foo1`${1}`;\n": "type 'string' is not assignable to type 'null'",
	} {
		if _, err := parseAndCompile(src); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: got %v", src, err)
		}
	}
}

// A callable interface reached again from its own result is held boxed
// there; a class extending a namespace-qualified class (`extends M.C1`,
// `extends M.In.C3`) inherits its members and constructor.
func TestE2ERecursiveCallablesAndQualifiedBases(t *testing.T) {
	out := compileAndRun(t, `interface A { (): B }
interface B { (): A }
const mk = (n: number): A => () => () => mk(n + 1);
const x = mk(0)();
console.log(typeof x, typeof x());
namespace M { export class C1 { a = 1; hi() { return "hi" + this.a; } } export namespace In { export class C3 { q = 3 } } }
namespace N { export class D1 extends M.C1 { b = 2 } export class D3 extends M.In.C3 {} }
const d = new N.D1();
console.log(d.a, d.b, d.hi(), d instanceof M.C1, new N.D3().q);
`)
	if out != "function function\n1 2 hi1 true 3" {
		t.Errorf("got %q", out)
	}
	_, err := parseAndCompile("namespace M { export class C1 { a = 1 } }\nclass D1 extends M.C1 {}\nconst z: null = new D1().a;\n")
	if err == nil || !strings.Contains(err.Error(), "type 'number' is not assignable to type 'null'") {
		t.Errorf("qualified base: got %v", err)
	}
}

// A class value gains properties that are none of its statics, through
// `any` and Object.assign; a constructor type with members is held boxed.
func TestE2EClassValueExpandos(t *testing.T) {
	out := compileAndRun(t, `class K { static s = 5; a = 1 }
const c: any = K;
c.s = 6; console.log(K.s);
c.extra = 7; console.log(c.extra, Object.keys(K));
console.log(K);
const x: { foo: number } & { new(): { a: number } } = Object.assign(K, { foo: 2 });
console.log(new x().a, x.foo);
`)
	if out != "6\n7 [ 's', 'extra' ]\n[class K] { s: 6, extra: 7 }\n1 2" {
		t.Errorf("got %q", out)
	}
}

// A conditional type over the polymorphic `this` is decided per receiver.
func TestE2EThisConditionalReturn(t *testing.T) {
	out := compileAndRun(t, `class B { m(): string | (this extends C ? undefined : null) { return this instanceof C ? "c" : "b"; } }
class C extends B { k = 1 }
const b = new B().m(); const c = new C().m();
console.log(b, c);
`)
	if out != "b c" {
		t.Errorf("got %q", out)
	}
	_, err := parseAndCompile("class B { m(): string | (this extends C ? undefined : null) { return \"\"; } }\nclass C extends B { k = 1 }\nconst z: number = new B().m();\n")
	if err == nil || !strings.Contains(err.Error(), "type 'string | null' is not assignable to type 'number'") {
		t.Errorf("this conditional: got %v", err)
	}
}

// `instanceof` a generic class holds for each instantiation and for a
// class extending one; an instance intersected with a union of object
// types distributes.
func TestE2EGenericInstanceofAndInstanceUnionIntersection(t *testing.T) {
	out := compileAndRun(t, `class G<T> { v?: T }
class H<T> { w?: T }
const g = new G<number>(); const s = new G<string>();
const a: any = g; const h: any = new H<number>();
console.log(g instanceof G, s instanceof G, a instanceof G, h instanceof G, a instanceof H, ({}) instanceof G);
class D extends G<boolean> { d = 1 }
const d: any = new D(); console.log(d instanceof G, new D() instanceof G);
class N { y = 0 }
const v3 = new N() as N & ({ a?: string } | { b?: number });
v3.y = 42; console.log(v3.y);
`)
	if out != "true true true false false false\ntrue true\n42" {
		t.Errorf("got %q", out)
	}
}

// A spread of a generic value is the intersection of the spread types; a
// generic function argument passes its type parameters to the call's
// function result; a generic value's destructured property is T["a"].
func TestE2EGenericSpreadsAndHigherOrderInference(t *testing.T) {
	out := compileAndRun(t, `const createTransform = <I, O>(tr: (from: I) => O) => tr;
function withP2<P>(p: P) {
  const m = <I,>(from: I) => ({ ...from, ...p });
  return createTransform(m);
}
const added2 = withP2({ foo: 1 })({ bar: 2 });
console.log(added2, added2.foo);
function w<I>(from: I) { return { a: 1, ...from }; }
const r = w({ bar: "x", a: 5 });
console.log(r, r.bar);
function test<T extends { a: string }>(obj: T) {
  let { a, ...rest } = obj;
  return { ...rest, b: a };
}
let o2: { b: string, x: number } = test({ a: "hello", x: 42 });
console.log(o2, o2.b.length);
function g<T extends { n: number }>(o: T) { const { n } = o; return n + 1; }
console.log(g({ n: 2, m: "q" }));
`)
	if out != "{ bar: 2, foo: 1 } 1\n{ a: 5, bar: 'x' } x\n{ x: 42, b: 'hello' } 5\n3" {
		t.Errorf("got %q", out)
	}
	for src, want := range map[string]string{
		"declare function ct<I, O>(tr: (from: I) => O): (from: I) => O;\nconst z: null = ct(<J,>(x: J) => [x])(1);\n": "type 'number[]' is not assignable to type 'null'",
		"function w<I, P>(from: I, p: P) { return { ...from, ...p }; }\nconst z: null = w({ bar: 2 }, { foo: 1 });\n": "type '{ bar: number; foo: number; }' is not assignable to type 'null'",
	} {
		if _, err := parseAndCompile(src); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v", src, err)
		}
	}
}

// A signature whose result reads the value it types (`var b: (n: number)
// => typeof b`, `function foo(): typeof foo`) resolves that result when
// read.
func TestE2ESelfReferentialTypeofSignatures(t *testing.T) {
	out := compileAndRun(t, `let k = 0;
var b: (n: number) => typeof b = (n) => { k += n; return b; };
b(1)(2)(3);
function foo(): typeof foo { return foo; }
var foo1: typeof foo = foo;
console.log(k, typeof b(0), foo1()() === foo);
class A { m(isNaN: typeof globalThis.isNaN, bar?: typeof globalThis.isNaN) { return bar ?? isNaN } }
const q: typeof globalThis.Math.max = Math.max;
console.log(new A().m(isNaN)(NaN), q(1, 5));
const g: (...v: number[]) => number = Math.min;
console.log(g(1, 5), g());
const gt: (typeof globalThis)["globalThis"] = globalThis;
console.log(gt.isNaN(NaN), gt.Math.abs(-2));
`)
	if out != "6 function true\ntrue 5\n1 Infinity\ntrue 2" {
		t.Errorf("got %q", out)
	}
	_, err := parseAndCompile("function foo(): typeof foo { return null; }\n")
	if err == nil || !strings.Contains(err.Error(), "type 'null' is not assignable") {
		t.Errorf("self typeof: got %v", err)
	}
}

// An intersection of function types is their overload list; an object
// type may declare several call signatures; a callback against several
// signatures takes the union of their parameters; a union of generic
// signatures is called through one.
func TestE2EFunctionIntersectionsAndSignatureLists(t *testing.T) {
	out := compileAndRun(t, `const h = ((x: any) => typeof x === "number" ? String(x) : x.length) as ((x: number) => string) & ((x: string) => number);
const s: string = h(12); const n: number = h("abcd");
console.log(s, n);
type F = { (e: number): string; (e: string): number };
const f = h as F;
console.log(f(3), f("xy"));
function g(a: ((e: 0) => unknown) & ((e: 1) => unknown)) { a(0); a(1); }
g((x) => { const v: 0 | 1 = x; console.log("got", v); });
const u: (<T>(x: T) => T) | (<U>(y: U) => U[]) = <T,>(x: T) => x;
console.log(u(7));
`)
	if out != "12 4\n3 2\ngot 0\ngot 1\n7" {
		t.Errorf("got %q", out)
	}
	_, err := parseAndCompile("declare function g(a: { (e: 0): unknown; (e: 1): unknown }): void;\ng((x) => { const z: null = x; });\n")
	if err == nil || !strings.Contains(err.Error(), "is not assignable to type 'null'") {
		t.Errorf("signature list context: got %v", err)
	}
}

// A type may declare a string and a number index signature both.
func TestE2EStringAndNumberIndexSignatures(t *testing.T) {
	out := compileAndRun(t, `type D = { [k: string]: number | string; [i: number]: string };
const d: D = { a: 1, 0: "z" };
d.b = 2; d[1] = "y";
console.log(d, d[0], d.a);
interface I { [k: string]: unknown; [i: number]: number }
const i: I = { 0: 5, x: "q" };
console.log(i[0], i.x);
`)
	if out != "{ '0': 'z', '1': 'y', a: 1, b: 2 } z 1\n5 q" {
		t.Errorf("got %q", out)
	}
}

// A namespace member declaration may bind several names (`export const a =
// 1, b = 2`) or destructure (`export const { p, q: [r] } = o`).
func TestE2ENamespaceMultiBindings(t *testing.T) {
	out := compileAndRun(t, `namespace N { export const a = 1, b = 2; let c = 3, d = 4; export function f() { return c + d; } export let e = 5, g = "x"; }
console.log(N.a + N.b, N.f(), N.e, N.g);
N.e = 9; console.log(N.e);
namespace M { export const { p, q: [r, s] } = { p: 1, q: [2, 3] }; const [t] = [p + r]; export const u = t * 10; }
console.log(M.p, M.r, M.s, M.u);
`)
	if out != "3 7 5 x\n9\n1 2 3 30" {
		t.Errorf("got %q", out)
	}
}

// import.meta's dirname and filename are the module's own path, and the
// object itself holds them with its url.
func TestE2EImportMetaMembers(t *testing.T) {
	out := compileAndRun(t, `import * as path from "node:path";
console.log(import.meta.filename === path.join(import.meta.dirname, path.basename(import.meta.filename)), import.meta.url.startsWith("file:///"));
const m = import.meta;
console.log(m.filename === import.meta.filename, m.dirname === import.meta.dirname, typeof m.url);
`)
	if out != "true true\ntrue true string" {
		t.Errorf("got %q", out)
	}
}

// An object literal's method reads a later one through `this`, its result
// resolved when read; one reading itself is TS7023.
func TestE2ELiteralMethodsReadLaterMethods(t *testing.T) {
	out := compileAndRun(t, `let mr = { a: 3, start() { return this.passthrough(this.a); }, passthrough(n: number) { return this.sub1(n); }, sub1(n: number): number { if (n > 0) { return this.passthrough(n - 1); } return n; } };
const i: number = mr.start();
let o = { d: "bar", m() { return this.d.length + this.k(); }, k() { return 10; } };
console.log(i, o.m());
`)
	if out != "0 13" {
		t.Errorf("got %q", out)
	}
	_, err := parseAndCompile("let o = { a() { return this.a(); } };\n")
	if err == nil || !strings.Contains(err.Error(), "implicitly has return type 'any'") {
		t.Errorf("self cycle: got %v", err)
	}
}

// A conditional joining a tuple with a non-tuple holds the boxed union.
func TestE2EConditionalTupleAndNonTuple(t *testing.T) {
	out := compileAndRun(t, `const c6: { kind: 123 } = { kind: 123 };
const c7: [1 | 2, "foo" | "bar"] = [1, "bar"];
for (const k of [true, false]) { const a = k ? c7 : "x"; const b = k ? c6 : k ? c7 : "hello"; const c = k ? c6 : c7; console.log(a, b, c); }
`)
	if out != "[ 1, 'bar' ] { kind: 123 } { kind: 123 }\nx hello [ 1, 'bar' ]" {
		t.Errorf("got %q", out)
	}
}

// The string-mapping intrinsics map literals, a generic argument once it is
// known; a type parameter's or literal union's string methods are a
// string's; one generic function's instantiations of one layout but
// different signatures are separate instances.
func TestE2EStringMappingsAndConstrainedStringMethods(t *testing.T) {
	out := compileAndRun(t, `const a: Capitalize<"ab" | "cd"> = "Ab";
const b: Uncapitalize<"Xy"> = "xy";
function up<T extends string>(x: T): Uppercase<T> { return x.toUpperCase() as Uppercase<T>; }
const r: "AB" = up("ab");
function g<T extends string>(x: T) { return x.trim() + x.toUpperCase() + x.length; }
function h<T extends "a" | "b", U extends T>(x: U) { return x.toUpperCase(); }
const k = Math.random() > 2 ? "ab" : " cd ";
console.log(a, b, r, g(" ab "), h("a"), k.toUpperCase(), k.trim(), k.padStart(6, "-"));
type EP<C> = C extends "a" ? 1 : "u";
function f<T>(q: EP<T>) { return q; }
console.log(f<"a">(1), f<"b">("u"), f<"a">(1));
type EM = { event: {} };
type EP2<C> = C extends "event" ? EM[C] : "u";
type VE<T extends string> = { h(event: EP2<Lowercase<T>>): any }["h"];
const on = <T extends string>(q: VE<T>) => typeof q;
console.log(on(() => 1));
`)
	if out != "Ab xy AB ab AB 4 A  CD  cd -- cd \n1 u 1\nfunction" {
		t.Errorf("got %q", out)
	}
	_, err := parseAndCompile("const w: Uppercase<'a'> = 'b';\n")
	if err == nil || !strings.Contains(err.Error(), `'"b"' is not assignable to type '"A"'`) {
		t.Errorf("mapping: got %v", err)
	}
	_, err = parseAndCompile("type EP<C> = C extends 'a' ? 1 : 'u';\nfunction f<T>(q: EP<T>) { const z: null = q; }\n")
	if err == nil || !strings.Contains(err.Error(), "is not assignable to type 'null'") {
		t.Errorf("conditional source: got %v", err)
	}
}

// A class's static array is mutated in place (`C.xs.push(v)`), and a
// generic class's static field is the class's, not an instance field, even
// one typed by the class's own instantiation.
func TestE2EStaticArraysAndGenericStatics(t *testing.T) {
	out := compileAndRun(t, `class X { static xs: number[] = [5]; static add(n: number) { X.xs.push(n); } }
X.xs.push(1); X.add(2); const a = X.xs; a.push(3);
console.log(X.xs, X.xs.pop(), X.xs.length); X.xs.unshift(0); X.xs.splice(1, 1); console.log(X.xs);
class Bar<T extends string> { static instance: Bar<string>[] = []; static count = 0; name: T; constructor(n: T) { this.name = n; Bar.count++; } pushThis() { Bar.instance.push(this); } }
new Bar("a").pushThis(); new Bar<"b">("b").pushThis();
console.log(Bar.instance.length, Bar.count, Bar.instance.map(b => b.name), Object.keys(new Bar("c")));
`)
	if out != "[ 5, 1, 2 ] 3 3\n[ 0, 1, 2 ]\n2 2 [ 'a', 'b' ] [ 'name' ]" {
		t.Errorf("got %q", out)
	}
}

// A `super(…)` call's arguments take the base constructor's parameters as
// their context, the base's type arguments applied; an interface method's
// `this & T` result is the receiver's type with T's members.
func TestE2ESuperArgumentContextAndThisIntersection(t *testing.T) {
	out := compileAndRun(t, `interface CT<T> { method(p: T): void; }
class CBase<T> { constructor(param: CT<T>) { param.method("ab" as any); } foo(param: CT<T>) { param.method("xyz" as any); } }
class C extends CBase<string> { constructor() { super({ method(p) { console.log(p.length); } }); super.foo({ method(p) { console.log(p.toUpperCase()); } }); } }
new C();
interface Comp { extend<T>(p: T): this & T; }
interface Label extends Comp { title: string; }
function test(l: Label) { const e = l.extend({ id: 67 }).extend({ tag: "h" }); return e.id + e.tag + e.title; }
const l: Label = { title: "t", extend(p: any) { return Object.assign(this, p); } };
console.log(test(l));
`)
	if out != "2\nXYZ\n67ht" {
		t.Errorf("got %q", out)
	}
}

// IterableIterator takes tsc's three type parameters; a generator's
// [Symbol.iterator]() is itself; an iteration interface a function returns
// is held boxed.
func TestE2EIterableIteratorShapes(t *testing.T) {
	out := compileAndRun(t, `function* g(): IterableIterator<number, undefined> { yield 1; yield 2; }
const it = g(); const it2 = it[Symbol.iterator](); console.log(it2 === it, [...it2]);
const a8 = (): Iterator<number> => [1, 2][Symbol.iterator](); console.log(a8().next());
const mk = (): IterableIterator<number, any, any> => new Set([3, 4]).values(); console.log([...mk()]);
`)
	if out != "true [ 1, 2 ]\n{ value: 1, done: false }\n[ 3, 4 ]" {
		t.Errorf("got %q", out)
	}
}

// A homomorphic mapped type infers its type parameter from an object
// (tsc's reverse mapped type), a literal kept where the constraint types
// the property with a primitive; over a primitive it is that primitive, and
// over a union it distributes.
func TestE2EReverseMappedInference(t *testing.T) {
	out := compileAndRun(t, `type Narrowable = string | number | bigint | boolean;
type Narrow<A> = (A extends Narrowable ? A : never) | ({ [K in keyof A]: Narrow<A[K]> });
const satisfies = <TWide,>() => <TNarrow extends TWide>(narrow: Narrow<TNarrow>) => narrow;
const isNotNull = <T,>(value: T | null): value is T => value !== null;
type Item = { value: string | null };
const item1 = satisfies<Item>()({ value: "1" });
const item3 = satisfies<Item>()({ value: null });
const values1 = [item1, item3].map(item => item.value);
console.log(values1.filter(isNotNull));
function id<A extends { v: string }>(x: { [K in keyof A]: A[K] }): A { return x as A; }
const r = id({ v: "1", w: "x" });
const v: "1" = r.v;
let q = r; q.w = "y";
type M<A> = { [K in keyof A]: A[K] };
const s: M<string> = "str";
const p: Partial<{ a: number } | { b: string }> = {};
console.log(v, q.w, s.length, p);
`)
	if out != "[ '1' ]\n1 y 3 {}" {
		t.Errorf("got %q", out)
	}
	_, err := parseAndCompile("declare function f<A>(x: { [K in keyof A]: A[K] }): A;\nconst r = f({ v: '1' });\nconst v: '1' = r.v;\n")
	if err == nil || !strings.Contains(err.Error(), `type 'string' is not assignable to type '"1"'`) {
		t.Errorf("widened: got %v", err)
	}
}

// A reverse mapped type infers from a tuple, an array and an intersection
// of mapped types, past context-sensitive callbacks; a generic `for…in`
// key is `Extract<keyof T, string>`.
func TestE2EReverseMappedListsAndForInKeys(t *testing.T) {
	out := compileAndRun(t, `
type Results<T> = { [K in keyof T]: { data: T[K]; onSuccess: (data: T[K]) => void } };
type Errors<E> = { [K in keyof E]: { error: E[K]; onError: (data: E[K]) => void } };
function run<T, E>(arg: Results<T> & Errors<E>): void {
  for (const k in arg) { const v = (arg as any)[k]; v.onSuccess(v.data); v.onError(v.error); }
}
run({ a: { data: "x", onSuccess: (d) => console.log(d.toUpperCase()), error: 404, onError: (e) => console.log(e + 1) } });
function pick<A>(x: { [K in keyof A]: A[K] }): A { return x as A; }
const t = pick([1, "a"]);
const first: number = t[0];
const xs: number[] = [4, 5];
const ys: number[] = pick(xs);
const pair: [{ a: number }] & [{ b: string }] = [{ a: 1, b: "q" }];
console.log(first, t[1].length, ys.length, pair[0].a + pair[0].b);
function each<T>(a: { [K in keyof T]: { d: T[K] } }) { for (const k in a) { const v = a[k]; console.log(k, v.d); } }
each({ x: { d: 1 }, y: { d: 2 } });
`)
	if want := "X\n405\n1 1 2 1q\nx 1\ny 2"; out != want {
		t.Fatalf("got %q, want %q", out, want)
	}
	for _, bad := range []string{
		`function g<T>(a: T) { for (const k in a) { const z: null = k; } }`,
		`function g<T>(a: T) { for (const k in a) { const z: number = a[k]; } }`,
		`function g<T>(a: T & { x: 1 }) { for (const k in a) { const v = a[k]; } }`,
	} {
		if _, err := parseAndCompile(bad); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}

// A parameter no context types is the implicit any TS7006 reports, so its
// function is still checked; an unconstrained type parameter gives a
// literal's callbacks no context.
func TestE2EImplicitAnyParamsStillChecked(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{`const g = (n) => n; const z: null = g;`, "'(n: any) => any' is not assignable to type 'null'"},
		{`function g(n) { return 1 } const z: string = g(1);`, "'number' is not assignable to type 'string'"},
		{`declare function f<T>(target: T): T; const y = f({ d: (n) => n });`, "parameter 'n' implicitly has an 'any' type"},
	} {
		_, err := parseAndCompile(tc.src)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want %q", tc.src, err, tc.want)
		}
	}
}

// A union callee with generic and plain members types its callback from
// the combined signature; a literal source keeps its name against null.
func TestE2EUnionCallMixedGenericMembers(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{`interface Err<T> { f<U>(a: (err: T) => U): Err<U>; }
interface Ok<T> { f(a: (err: T) => unknown): Err<T>; }
declare const e: Err<0> | Err<1> | Ok<2>;
e.f((q) => { const y: null = q; return q; });`, "type '0 | 1 | 2' is not assignable to type 'null'"},
		{`declare function g<U>(a: ((e: 0) => U) & ((e: 1) => U)): U; const r = g((q) => q); const z: null = r;`, "type '0 | 1' is not assignable to type 'null'"},
		{`const x: string | undefined = 1;`, "type 'number' is not assignable to type 'string'"},
	} {
		_, err := parseAndCompile(tc.src)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want %q", tc.src, err, tc.want)
		}
	}
}

// `any`, `unknown` and a bare type parameter give a callback no contextual
// signature (TS7006); an `unknown` call context infers nothing, an `any`
// one infers any.
func TestE2EAnyContextGivesNoSignature(t *testing.T) {
	for _, src := range []string{
		`const f: any = (x) => x;`,
		`declare const a: any; a((x) => x);`,
		`declare function g(p: any): void; g((x) => x);`,
		`declare function h<T>(p: T): T; const r: unknown = h((x) => x);`,
		`declare function o(arg: { prop: any }): void; declare function i<T>(arg: T & ((arg: string) => any)): T; o({ prop: i((arg) => arg) });`,
	} {
		_, err := parseAndCompile(src)
		if err == nil || !strings.Contains(err.Error(), "implicitly has an 'any' type") {
			t.Errorf("%s: got %v", src, err)
		}
	}
	out := compileAndRun(t, `
function inner<T extends (arg: string) => any>(arg: T): T { return arg; }
function outer(arg: { prop: unknown }): void { console.log(typeof arg.prop); }
outer({ prop: inner((arg) => arg.length) });
`)
	if out != "function" {
		t.Fatalf("got %q", out)
	}
}

// Variadic tuple types (`[...A, L]`, `[X, ...A]`, `[...A]`) wait for their
// type variables, infer from the arguments, and lay out as the tuples
// they are bound to.
func TestE2EVariadicTupleTypes(t *testing.T) {
	out := compileAndRun(t, `
function last<A extends any[], L>(...args: [...A, L]): L { return args[args.length - 1] as L; }
function rest<A extends any[]>(...args: [string, ...A]): A { return args.slice(1) as any; }
function same<A extends unknown[]>(x: [...A]): A { return x; }
function pair<A extends [number, boolean]>(x: A): A { const s: any = [2, true]; return s; }
const f = <TFirstArgs extends any[], TLastArg>(...args: [...TFirstArgs, TLastArg]): void => { console.log(args.length); };
const l: string = last(1, 2, "x");
const r = rest("a", 1, true);
const s = same([1, "a"]);
f(1, 2, 3);
console.log(l, r.length, r[0], s[1].length, pair([1, false])[0]);
`)
	if want := "3\nx 2 1 1 2"; out != want {
		t.Fatalf("got %q, want %q", out, want)
	}
	for _, tc := range []struct{ src, want string }{
		{`function f<A extends any[], L>(x: [...A, L]) { const z: null = x; }`, "type '[...A, L]' is not assignable to type 'null'"},
		{`function f<A extends any[]>(x: [string, ...A]) { const a: null = x[0]; }`, "type 'string' is not assignable to type 'null'"},
		{`function g<T>(a: T) { for (const k in a) { const z: null = k; } }`, "type 'Extract<keyof T, string>' is not assignable to type 'null'"},
	} {
		_, err := parseAndCompile(tc.src)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want %q", tc.src, err, tc.want)
		}
	}
}

// An ambient class's constructor overloads are signatures, and a namespace
// merged with a function still names its members qualified.
func TestE2EAmbientCtorOverloadsAndFundules(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{`declare class B { constructor(s: string); constructor(n: number); }
class D extends B { constructor(s: string); constructor(x: any) { } }`, "constructors for derived classes must contain a 'super' call"},
		{`declare function B(): B.C; declare namespace B { export class C {} } const z: null = B();`, "type 'C' is not assignable to type 'null'"},
	} {
		_, err := parseAndCompile(tc.src)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want %q", tc.src, err, tc.want)
		}
	}
}

// An auto-typed binding assigned `[]` evolves as an array declared `[]`
// does, through receivers in commas and assignments.
func TestE2EAutoTypedEvolvingArrays(t *testing.T) {
	out := compileAndRun(t, `
function f16() {
    let x;
    (x = [], x).push(5);
    (x.push("hello"), x).push(true);
    ((x))[3] = { a: 1 };
    return x;
}
function f3() { let x; x = []; x.push(5, "hello"); return x; }
const a = f16(); const b = f3();
console.log(JSON.stringify(a), a.length, JSON.stringify(b), typeof b[1]);
`)
	if want := `[5,"hello",true,{"a":1}] 4 [5,"hello"] string`; out != want {
		t.Fatalf("got %q, want %q", out, want)
	}
	for _, tc := range []struct{ src, want string }{
		{`function f() { let x; x = []; x.push(5, "a"); const z: null = x; }`, "type '(string | number)[]' is not assignable to type 'null'"},
		{`function f() { let x; x = 1; x = []; x.push("s"); return x; } const z: null = f();`, "type 'string[]' is not assignable to type 'null'"},
		{`function f() { let x; x = []; return x; }`, "variable 'x' implicitly has an 'any[]' type"},
	} {
		_, err := parseAndCompile(tc.src)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want %q", tc.src, err, tc.want)
		}
	}
}

// An intersection of an array with object types has the array's members
// and the objects', as an interface extending it does.
func TestE2EArrayObjectIntersection(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{`interface A { a: number } declare const t: A & string[]; const z: null = t[0];`, "type 'string' is not assignable to type 'null'"},
		{`interface A { a: number } declare const t: A & string[]; const z: string[] = t; const y: null = t.a;`, "type 'number' is not assignable to type 'null'"},
		{`interface A { a: number } type B = A & string[]; interface C extends B { c: string } declare const t: C; const z: null = t.map(s => s.length);`, "type 'number[]' is not assignable to type 'null'"},
	} {
		_, err := parseAndCompile(tc.src)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want %q", tc.src, err, tc.want)
		}
	}
}

// A generic object alias naming itself is an instance per argument list,
// laid out boxed where it holds itself; a generic call's contextual type
// infers what its arguments do not.
func TestE2ERecursiveGenericAliasesAndReturnContext(t *testing.T) {
	out := compileAndRun(t, `
type L<T> = { v: T; next: L<T> | null };
const a: L<number> = { v: 1, next: { v: 2, next: null } };
let s = 0;
for (let p: L<number> | null = a; p; p = p.next) s += p.v;
const b = a.next!;
b.v = 40;
console.log(s, a.next!.v, a.next === b);
function len<T>(l: L<T> | null): number { return l ? 1 + len(l.next) : 0; }
console.log(len(a));
`)
	if want := "3 40 true\n2"; out != want {
		t.Fatalf("got %q, want %q", out, want)
	}
	for _, tc := range []struct{ src, want string }{
		{`type Thing<T> = { value: T; pipe(a: T): Thing<T>; }; declare const thing: Thing<number>; const z: null = thing.pipe;`, "type '(a: number) => Thing<number>' is not assignable to type 'null'"},
		{`type Thing<T> = { value: T; pipe<A>(a: A): Thing<A>; }; declare const thing: Thing<number>; const z: null = thing.pipe("s").value;`, "type 'string' is not assignable to type 'null'"},
		{`declare function mp<T, R>(p: (v: T) => R): (x: T) => R; const o: (x: number) => string = mp(d => { const z: null = d; return String(d); });`, "type 'number' is not assignable to type 'null'"},
		{`declare function mp<T>(p: (v: T) => void): T; const o: number = mp(d => { const z: null = d; });`, "type 'number' is not assignable to type 'null'"},
	} {
		_, err := parseAndCompile(tc.src)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want %q", tc.src, err, tc.want)
		}
	}
}

// A callback whose `any` context is the program's own is reported, as tsc
// does; one this compiler's library leaves untyped (where Node's own
// declarations type it) is not.
func TestE2EAnyContextLibraryOrigin(t *testing.T) {
	for _, src := range []string{
		`declare const g: any; g((x) => {});`,
		`function h(a: any) { a((x) => {}); }`,
	} {
		_, err := parseAndCompile(src)
		if err == nil || !strings.Contains(err.Error(), "parameter 'x' implicitly has an 'any' type") {
			t.Errorf("%s: got %v", src, err)
		}
	}
	out := compileAndRunImports(t, `
import { PassThrough, Writable, pipeline } from 'stream';
const src = new PassThrough();
const sink = new Writable({ write(chunk: Buffer, _e, cb) { cb(); } });
pipeline(src, sink, (err) => { console.log("done", err ? "err" : "ok"); });
src.end("b");
`)
	if out != "done ok" {
		t.Fatalf("got %q", out)
	}
}

// Function.prototype[Symbol.hasInstance] is OrdinaryHasInstance, a class's
// own @@hasInstance can call it, and a computed key reads Function.
// prototype's methods; `instanceof` an unrelated generic class narrows to
// the intersection.
func TestE2EFunctionPrototypeHasInstance(t *testing.T) {
	out := compileAndRun(t, `
class A {
    a: string = "";
    static [Symbol.hasInstance]<T>(this: T, value: unknown): value is (T extends (abstract new (...args: any) => infer U) ? U : never) {
        return Function.prototype[Symbol.hasInstance].call(this, value);
    }
}
const x: unknown = new A();
console.log(x instanceof A, ({}) instanceof A, typeof Function.prototype[Symbol.hasInstance]);
function f(v: unknown) { if (v instanceof A) { console.log(v.a.length); } }
f(new A());
const fp: any = Function.prototype;
const k: any = "call";
console.log(typeof fp[k], typeof (function () {} as any)[k]);
`)
	if want := "true false function\n0\nfunction function"; out != want {
		t.Fatalf("got %q, want %q", out, want)
	}
	_, err := parseAndCompile(`function f2(s: Set<number>) { if (s instanceof Promise) { const z: null = s.then; } }`)
	if err == nil || !strings.Contains(err.Error(), "is not assignable to type 'null'") {
		t.Errorf("got %v", err)
	}
}

// A generic instance with a ThisType marker types `this` in its methods;
// a shorthand property of a unit-typed binding discriminates a union
// context.
func TestE2EThisTypeInstanceAndShorthandDiscriminant(t *testing.T) {
	out := compileAndRun(t, `
type ADT = { kind: "a", method(x: string): number; } | { kind: "b", method(x: number): string; };
function invoke(item: ADT) { console.log(item.kind === "a" ? item.method("7") : item.method(42)); }
const kind = "a";
invoke({ kind, method(a) { return +a; } });
`)
	if out != "7" {
		t.Fatalf("got %q", out)
	}
	_, err := parseAndCompile(`interface B<D> { d?: D; m?(): void } const o: B<number> & ThisType<{ q: string }> = { d: 1, m() { const z: null = this.q; } };`)
	if err == nil || !strings.Contains(err.Error(), "type 'string' is not assignable to type 'null'") {
		t.Errorf("got %v", err)
	}
}

// A class method declared to return a tuple returns it; an element read of
// an array-constrained type parameter is its constraint's element; a `new`
// with an `any` argument takes the instance layout its context expects; a
// rest parameter typed by an erased type parameter collects its arguments.
func TestE2EMethodTuplesAndConstrainedElements(t *testing.T) {
	out := compileAndRun(t, `
class C { m(): [number, string] { return [1, "b"]; } }
const z = new C().m();
console.log(z[1], new C().m()[0]);
class Opt<T> {
  constructor(public v: T) {}
  pair<O>(o: O): [T, string] { return [this.v, "q"]; }
  zip<O extends Array<Opt<any>>>(...others: O): number { return others.length; }
}
const p = new Opt(5).pair(2);
console.log(p[0], p[1], new Opt(1).zip(new Opt("s"), new Opt(true)));
function zf<O extends Array<Opt<any>>>(...others: O): number { return others.length + others[0].v; }
console.log(zf(new Opt(2), new Opt("x")));
function mk(): Opt<[number, string]> { return new Opt([2, "b"] as any); }
function mk2(): Opt<number> { return new Opt(7 as any); }
console.log(mk().v[1], mk2().v + 1);
interface Option<T> { zip1<O extends Array<Option<any>>>(...others: O): Option<[T, ...O]>; }
const o1: Option<number> = { zip1: (...xs: any[]) => o1 as any };
console.log(typeof o1.zip1(o1));
`)
	if want := "b 1\n5 q 2\n4\nb 8\nobject"; out != want {
		t.Fatalf("got %q, want %q", out, want)
	}
	_, err := parseAndCompile(`function zf<O extends [string, number]>(o: O) { const z: null = o[1]; }`)
	if err == nil || !strings.Contains(err.Error(), "type 'number' is not assignable to type 'null'") {
		t.Errorf("got %v", err)
	}
}

// Class index signatures type a class's instances and a literal of its
// layout; typeof and instanceof narrow a type parameter (`T & C`, an
// exhaustive `switch (typeof x)`), and a guard an instantiation decides
// emits only the branch that runs; a tuple pattern's leaves narrow
// together; a distributive conditional relates through its constraint;
// numeric keys too large for an integer name the double they spell.
func TestE2ETypeParamGuardsAndIndexSignatures(t *testing.T) {
	out := compileAndRun(t, `
class JQueryElement { id: string = ""; }
class JQuery { [n: number]: JQueryElement }
var jq: JQuery = { 0: { id: "a" }, 1: { id: "b" } };
console.log(jq[0].id, jq[1].id);
class Bag { [k: string]: number; size = 0; }
const bag = new Bag();
bag["x"] = 3; bag.y = 4;
console.log(bag["x"] + bag.y, bag.size, Object.keys(bag).join(","));
function len<T extends string | number>(x: T): number {
  if (typeof x === "number") return x + 1;
  return x.length;
}
console.log(len(4), len("abc"));
function kind(x: number | string | ((n: number) => string) | { y: number }): string {
  switch (typeof x) {
    case "number": return "n";
    case "string": return "s";
    case "function": return x(1);
    case `+"`object`"+`: return "o" + x.y;
  }
}
console.log(kind(1), kind("a"), kind((n) => "f" + n), kind({ y: 2 }));
class C0 { p = "c"; }
function pick<T>(x: T): string {
  if (x instanceof C0) { const c: C0 = x; return c.p; }
  if (typeof x === "string") { const s: string = x; return s; }
  return "?";
}
console.log(pick(new C0()), pick("str"), pick(3));
function okR(): [true, number] | [false, undefined] { return [true, 41]; }
const [ok, val] = okR();
if (ok) console.log(val + 1);
type IsArr<T> = T extends unknown[] ? true : false;
function f2<T extends unknown[]>(x: IsArr<T>): true { return x; }
console.log(f2<number[]>(true));
const big = { 0b11: "a", 0x10: "c" };
console.log(big[3], big["16"]);
interface Ix { [x: number]: string; 1: string; }
const ix: Ix = { 1: "one", 3: "three" };
console.log(ix["3"], ix[1]);
`)
	if want := "a b\n7 0 size,x,y\n5 3\nn s f1 o2\nc str ?\n42\ntrue\na c\nthree one"; out != want {
		t.Fatalf("got %q, want %q", out, want)
	}
	for _, c := range []struct{ src, want string }{
		{`function zz<T extends number | string>(x: T) { if (typeof x === "number") { const q: null = x; } }`, "type 'number' is not assignable to type 'null'"},
		{`function g<T>(x: T) { if (x instanceof Date) { const q: null = x; } }`, "type 'T & Date' is not assignable to type 'null'"},
		{`declare class B { constructor(x: string); constructor(x: number, y: number); } new B(true);`, "argument of type 'boolean' is not assignable to parameter of type 'string'"},
		{`type Ctor<T> = new (...a: any[]) => T; interface Tg { tag: string } declare const M: Ctor<Tg> & (new (x: number) => { x: number }); const q: null = new M(1).tag;`, "type 'string' is not assignable to type 'null'"},
		{`enum E { a } const s = E["zz"];`, "index expression is not of type 'number'"},
		{`declare var u: { (a: string, ...b: number[]): string } | { (a: string, b: number): number }; u("x");`, "expected 2 arguments, but got 1"},
		{`function h(x: number | string): string { switch (typeof x) { case "number": return ""; } }`, "function lacks ending return statement"},
	} {
		_, err := parseAndCompile(c.src)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want %q", c.src, err, c.want)
		}
	}
	// A call reports its first argument that does not fit, as tsc does.
	_, err := parseAndCompile(`declare function f(a: number, b: number): void; f("", "");`)
	if err == nil || strings.Count(err.Error(), "is not assignable to parameter") != 1 {
		t.Errorf("got %v", err)
	}
}

// A tuple context shapes an array literal with spreads (each spread
// tuple's elements in place, evaluated in order); an object literal read
// by a string key is its properties' types or undefined; a class
// expression's static field keeps its context's literals; a property a
// type lacks names the closest one it has (TS2551); a const enum has no
// reverse mapping (TS2339, TS2476); an empty pattern checks only that its
// initializer is not nullish.
func TestE2ETupleSpreadsAndPropertySuggestions(t *testing.T) {
	out := compileAndRun(t, `
const sbb: [string, boolean, ...boolean[]] = ["s", true, false];
let k5: [number, string, boolean, ...boolean[]];
k5 = [1, ...sbb];
console.log(k5.length, k5[1], k5[3]);
let n = 0;
function f() { n++; return n; }
const pr: [string, number] = ["a", 2];
const t4: [number, string, number, boolean] = [f(), ...pr, n === 1];
console.log(t4[0], t4[1], t4[2], t4[3]);
const p1: [number, string] = [1, ...(["x"] as [string])];
console.log(p1[1]);
function pick(id: string) { return { a: 1, b: "x" }[id]; }
console.log(pick("b"), pick("a"), pick("z"));
interface I { x: { a: "a" } }
const c: I = class { static x = { a: "a" } };
console.log(c.x.a);
interface Th { baz?: Error }
function g(o: Th | undefined): string {
  if (o?.baz instanceof Error) return o.baz.message;
  return "none";
}
console.log(g({ baz: new Error("e") }), g(undefined));
const { }: { q: number } = { q: 1 };
class C9 { v = 1 }
class E9 extends Error {}
class M9 { c?: C9; e?: E9; d?: Date; m?: Map<string, number> }
const m9 = new M9();
console.log(m9.c instanceof C9, m9.e instanceof Error, m9.e instanceof E9, m9.d instanceof Date, m9.m instanceof Map);
m9.c = new C9(); m9.e = new E9(); m9.d = new Date(); m9.m = new Map();
console.log(m9.c instanceof C9, m9.e instanceof Error, m9.e instanceof E9, m9.d instanceof Date, m9.m instanceof Map);
interface Tup { 0: number[] | string[]; 1: number[] | string[]; }
const t2: [number[], string[]] = [[1, 2], ["a"]];
const c0: Tup = t2;
const c1: Tup = [...t2];
console.log(c0[0].length, c1[1][0]);
const yy: number[] & [number, ...number[]] = [1, 2];
const xx: [number, ...number[]] = yy;
console.log(xx.length);
function lit() { return "b" as const; }
const lb: "b" = lit();
console.log(lb);
`)
	if want := "4 s false\n1 a 2 true\nx\nx 1 undefined\na\ne none\nfalse false false false false\ntrue true true true true\n2 a\n2\nb"; out != want {
		t.Fatalf("got %q, want %q", out, want)
	}
	for _, c := range []struct{ src, want string }{
		{`interface I { foof: number } declare const i: I; i.foo;`, "property 'foo' does not exist on type 'I'; did you mean 'foof'?"},
		{`interface I { foof: number } declare const i: I; i["foo"];`, "did you mean 'foof'?"},
		{`const enum E { A } var x = E["B"];`, "property 'B' does not exist on type"},
		{`const enum E { A } var y = E[0];`, "a const enum member can only be accessed using a string literal"},
		{`declare const id: string; const v = { a: 1, b: "" }[id]; const q: null = v;`, "type 'string | number | undefined' is not assignable to type 'null'"},
		{`function fn<T extends string | undefined>(one: T) { const s: string = one!; const q: null = one!; }`, "type 'T & string' is not assignable to type 'null'"},
	} {
		_, err := parseAndCompile(c.src)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want %q", c.src, err, c.want)
		}
	}
	if _, err := parseAndCompile(`const { }: undefined = 1; export {};`); err == nil || strings.Contains(err.Error(), "type '1'") {
		t.Errorf("empty pattern: got %v", err)
	}
}

// TestE2EIndexSignatureInference: a string-index parameter infers from a
// literal's properties, reverse-maps through a mapped type, and a union
// source infers from each member.
func TestE2EIndexSignatureInference(t *testing.T) {
	out := compileAndRun(t, `
type Box<T> = { value: T };
type Boxified<T> = { [P in keyof T]: Box<T[P]> };
function unboxify<T extends object>(obj: Boxified<T>): T {
  const r: any = {};
  for (const k in obj) r[k] = obj[k].value;
  return r;
}
function makeDictionary<T>(obj: { [x: string]: T }) { return obj; }
const d = makeDictionary({ a: { value: 1 }, b: { value: "x" } });
const v = unboxify(d);
const x: string | number = v["b"];
console.log(x);
function first<T>(o: { v: T }): T { return o.v; }
const u: { v: number } | { v: string } = { v: 3 };
const w: string | number = first(u as { v: number });
console.log(w);
interface Fooer { foo: (v: string) => string; }
function compact<T>(array: T[]): T[] { return array; }
const fs = compact([{ foo: (v: string) => v + "!" }, { foo: (v) => v.toUpperCase() }]);
console.log(fs[1].foo("ok"));
type RV = { type: "number"; value: number } | { type: "string"; value: string };
function only(x: RV & { type: "number" }) {
  if (x.type === "number") return x.value;
  else return x.value + 1;
}
console.log(only({ type: "number", value: 4 }));
function pipe<A extends any[], B>(ab: (...args: A) => B): (...args: A) => B;
function pipe<A extends any[], B, C>(ab: (...args: A) => B, bc: (b: B) => C): (...args: A) => C;
function pipe(...fs: any[]): any { return (...a: any[]) => fs.slice(1).reduce((v: any, f: any) => f(v), fs[0](...a)); }
const f30: (n: number) => number = pipe(x => x + 1, x => x * 2);
console.log(f30(2));
type F2 = { kind: "foo"; c: number };
type B2 = { kind: "bar" };
type D2 = { kind: "document"; c: number };
function isN2(x: unknown): x is F2 | B2 { return (x as any).kind !== "document"; }
function wn(node: D2 | F2 | B2): string { if (isN2(node)) { const k: "foo" | "bar" = node.kind; return k; } return "doc"; }
console.log(wn({ kind: "bar" }), wn({ kind: "document", c: 1 }));
const mk: () => { prop: "foo" } = () => ({ prop: "foo" });
const mk2: { a: () => { prop: "foo" } } = { a: () => { return { prop: "foo" }; } };
console.log(mk().prop, mk2.a().prop);
type Opts<D> = object & { data: D; watch?: Record<string, (v: any) => void> } & ThisType<D>;
function mkOpts<D>(o: Opts<D>): Opts<D> { return o; }
const o = mkOpts({ data: { bar: true }, watch: { foo(v: any): void { this.bar = v; } } });
o.watch!.foo.call(o.data, false);
console.log(o.data.bar);
type GG = Generator<number, string, any> | AsyncGenerator<number, string, any>;
function* gg(): GG { yield 1; return "r"; }
const it = gg() as Generator<number, string, any>;
console.log(it.next().value, it.next().value);
function* lens(): IterableIterator<(x: string) => number> {
  yield x => x.length;
  yield* [x => x.length * 2];
  yield* (function* () { yield x => x.length * 3; })();
}
console.log([...lens()].map(f => f("ab")).join(","));
const tg = (t: [number, string] = [1, "z"]) => t[1] + t[0];
const th = function (t: [boolean, string] = [true, "d"]) { return t[0] ? t[1] : "no"; };
function pdef(cb: (x: string) => number = x => x.length, o: { k: "a" | "b" } = { k: "a" }) { return cb("abc") + o.k; }
interface SI { x: { a: "a" } }
function sdef(c: SI = class { static x = { a: "a" } }) { return c.x.a; }
console.log(tg(), tg([2, "y"]), th(), th([false, "x"]), pdef(), sdef());
let cn = 0;
const cf: (a: number) => number = (cn++, a => a + cn);
console.log(cf(1));
`)
	if want := "x\n3\nOK\n4\n6\nbar doc\nfoo foo\nfalse\n1 r\n2,4,6\nz1 y2 d no 3a a\n2"; out != want {
		t.Fatalf("got %q, want %q", out, want)
	}
}

// A constructor returning an instance of its class replaces the one `new`
// allocated; a literal-named or destructured field needs no initializer.
func TestE2EConstructorReturnOverride(t *testing.T) {
	out := compileAndRun(t, `
let cache: P | null = null;
class P {
  n: number;
  constructor(n: number) {
    this.n = n;
    if (cache) return cache;
    cache = this;
  }
}
const a = new P(1), b = new P(2);
console.log(a === b, b.n);
class C { self: C; constructor() { return (this.self = this); } }
const c = new C();
console.log(c.self === c);
class S { tag = "s"; constructor(o: S | null) { return o; } }
const s1 = new S(null);
console.log(new S(s1) === s1, s1.tag);
class B { constructor() { return 5 as any; } }
console.log(new B() instanceof B);
class AF {
  readonly #p: string;
  "q-r": number;
  constructor(arg: { key: string }) { ({ key: this.#p } = arg); }
  get p() { return this.#p; }
}
const af = new AF({ key: "k1" });
console.log(af.p, af["q-r"]);
class CS { self: CS; constructor() { return (this.self = this); } }
console.log(new CS().self instanceof CS);
`)
	if want := "true 1\ntrue\ntrue s\ntrue\nk1 undefined\ntrue"; out != want {
		t.Fatalf("got %q, want %q", out, want)
	}
}

// An undeclared name constructed or called in JavaScript reads the
// global property, else throws its ReferenceError when the code runs; a
// static eval string that does not parse throws SyntaxError.
func TestE2EUndeclaredNewAndCallJS(t *testing.T) {
	bin := buildCLI(t)
	dir := tempDir(t)
	src := filepath.Join(dir, "main.js")
	if err := os.WriteFile(src, []byte(`function f() { throw new Nope("x"); }
console.log(typeof f);
try { f(); } catch (e) { console.log(e instanceof ReferenceError, e.message); }
globalThis.Late = class { constructor(v) { this.v = v; } };
console.log(new Late(4).v);
globalThis.late = (x) => x + 1;
console.log(late(4));
function g() { return nope(1); }
try { g(); } catch (e) { console.log(e instanceof ReferenceError, e.message); }
try { eval("1 +"); console.log("no"); } catch (e) { console.log(e instanceof SyntaxError, e.name); }
console.log(eval("2 * 3"));
`), 0644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "prog"+llvm.HostExeSuffix())
	if b, err := exec.Command(bin, "-compat=js", "-o", out, src).CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, b)
	}
	got, err := exec.Command(out).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, got)
	}
	compareLines(t, strings.TrimRight(string(got), "\n"), "function\ntrue Nope is not defined\n4\n5\ntrue nope is not defined\ntrue SyntaxError\n6")
}

// The Function constructor with constant strings makes a function whose
// body closes over the global scope, never its caller's or its module's;
// tsc types it Function and never checks the body.
func TestE2EFunctionConstructor(t *testing.T) {
	assertSameAsNode(t, `const add = new Function("a", "b", "return a + b;");
console.log(add(1, 2, 3), add.length);
const x: Function = Function("return [1, 2].map(v => v * 2).join()");
console.log(x());
(globalThis as any).gq = 5;
console.log(Function("return gq * 2")());
`)
	bin := buildCLI(t)
	dir := tempDir(t)
	src := filepath.Join(dir, "main.js")
	if err := os.WriteFile(src, []byte(`var add = new Function("a", "b", "return a + b;");
console.log(add(2, 3), typeof add, add.length);
var f2 = Function("return 7");
console.log(f2(), f2 === Function("return 7"));
var g = 10;
function outer() { var g = 1; return Function("return g")(); }
try { console.log(outer()); } catch (e) { console.log(e instanceof ReferenceError, e.message); }
globalThis.gg = 3; console.log(Function("return gg + 1")());
try { Function("return ("); } catch (e) { console.log(e instanceof SyntaxError); }
var noArgs = new Function();
console.log(noArgs());
`), 0644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "prog"+llvm.HostExeSuffix())
	if b, err := exec.Command(bin, "-compat=js", "-o", out, src).CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, b)
	}
	got, err := exec.Command(out).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, got)
	}
	compareLines(t, strings.TrimRight(string(got), "\n"), "5 function 2\n7 false\ntrue g is not defined\n4\ntrue\nundefined")
}

// A nested pattern over a primitive field reads its wrapper's members
// (TS2339 for one it lacks); a class value has its statics and Function's.
func TestCheckerPrimitivePatternAndClassValueMembers(t *testing.T) {
	mustCompileError(t, "const o = { a: 5 };\nconst { a: { b } } = o;\n", "property 'b' does not exist on type 'number'")
	mustCompileError(t, "class A { static y = 1 }\nconsole.log(A.zz);\n", "property 'zz' does not exist on type 'typeof A'")
	mustCompileError(t, "class A {}\nA.zz = 1;\n", "property 'zz' does not exist on type 'typeof A'")
	assertSameAsNode(t, `
const o = { a: 5, s: "x" };
const { a: { toFixed }, s: { length } } = o;
class A { static y = 1; static m() { return 2; } }
console.log(typeof toFixed, length, A.y, A.m(), A.name, typeof A.bind(null), A.hasOwnProperty("y"));
`)
}
