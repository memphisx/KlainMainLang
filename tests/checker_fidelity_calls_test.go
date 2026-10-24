package tests

import "testing"

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
// a named member is a string, an unknown key reads undefined.
func TestE2EProcessVersionsTyped(t *testing.T) {
	assertSameAsNode(t, `
console.log(process.versions.node.split(".").length > 1, typeof process.versions.v8, process.versions.nope);
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
