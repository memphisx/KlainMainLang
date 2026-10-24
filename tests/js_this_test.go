package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"KlainMainLang/codegen/llvm"
	"KlainMainLang/parser"
)

// Untyped JavaScript (`-compat=js`): a `function` that reads its own `this`
// takes the receiver its caller supplies — an emitter's listeners, a
// server's `listen` callback — through a typed callback parameter and back,
// and keeps its identity for removeListener. Node, running the same
// CommonJS file, is the oracle.

// assertJSFileSameAsNode compiles src as a `.js` program under -compat=js
// with the CLI and compares its output with `node` running the same file.
func assertJSFileSameAsNode(t *testing.T, src string) {
	t.Helper()
	node := nodeStripTypesBin(t)
	bin := buildCLI(t)
	dir := tempDir(t)
	file := filepath.Join(dir, "main.js")
	if err := os.WriteFile(file, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}
	want, err := exec.Command(node, file).CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, want)
	}
	out := filepath.Join(dir, "prog"+llvm.HostExeSuffix())
	if b, err := exec.Command(bin, "-compat=js", "-o", out, file).CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, b)
	}
	got, err := exec.Command(out).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, got)
	}
	compareLines(t, strings.TrimRight(string(got), "\n"), strings.TrimRight(string(want), "\n"))
}

func TestE2EJSThisInFunctionExpressionCallbacks(t *testing.T) {
	assertJSFileSameAsNode(t, `'use strict';
const net = require('net');
const { EventEmitter } = require('events');
const server = net.createServer();
server.listen(0, '127.0.0.1', function () {
  console.log(typeof this.address().port, this === server);
  this.close();
});
const e = new EventEmitter();
e.on('x', function (v) { console.log('x', v, this === e); });
e.emit('x', 5);
[1].forEach(function () { console.log(this === undefined); });
`)
}

func TestE2EJSThisInFunctionDeclarationCallbacks(t *testing.T) {
	assertJSFileSameAsNode(t, `'use strict';
const net = require('net');
const { EventEmitter } = require('events');
const server = net.createServer();
server.listen(0, '127.0.0.1', onListen);
function onListen() {
  console.log(typeof this.address().port, this === server);
  this.close();
}
const e = new EventEmitter();
function onX(v) { console.log('x', v, this === e); }
e.on('x', onX);
e.emit('x', 5);
e.removeListener('x', onX);
console.log(e.listenerCount('x'));
function outer() {
  function inner() { return this === e; }
  e.once('y', function () { console.log('y', inner(), this === e); });
  e.emit('y');
}
outer();
`)
}

func TestE2EJSThisCallApplyBind(t *testing.T) {
	assertJSFileSameAsNode(t, `'use strict';
function who(a, b) { return (this && this.name) + ':' + a + ':' + b; }
const o = { name: 'o' };
console.log(who.call(o, 1, 2));
console.log(who.apply(o, [3, 4]));
const args = [9, 10];
console.log(who.apply(o, args));
const bound = who.bind(o, 5);
console.log(bound(6), bound.length);
const f = function () { return this === undefined; };
console.log(f(), f.call(undefined));
const holder = { name: 'h', who: who };
console.log(holder.who(7, 8));
const add = (a, b) => a + b;
console.log(add.call(null, 1, 2), add.bind(null, 3)(4));
`)
}

// A function used as a constructor: new, instanceof, its prototype and
// prototype.constructor, inspection by its name, a plain call with
// globalThis as `this`, and an object a constructor returns.
func TestE2EJSFunctionConstructors(t *testing.T) {
	assertJSFileSameAsNode(t, `function F(v) { this.v = v; }
F.prototype.get = function () { return this.v * 2; };
const x = new F(3);
console.log(x, x instanceof F, x.constructor === F, x.get(), Object.getPrototypeOf(x) === F.prototype);
function Animal(name) { this.name = name; }
new Animal("x");
Animal("y");
console.log(globalThis.name);
function P(v) { if (!(this instanceof P)) return new P(v); this.v = v; }
console.log(P(1), new P(2), P(3) instanceof P);
function Sum(a, b) { return a + b; }
Sum.prototype.tag = "t";
console.log(Sum(1, 2), new Sum(1, 2), new Sum(1, 2).tag);
`)
}

// An empty array that functions are pushed into holds functions: called
// by index, iterated, passed to forEach/map, with one signature or several.
func TestE2EJSPushedFunctionArray(t *testing.T) {
	assertJSFileSameAsNode(t, `var fs = [];
fs.push(() => 1);
fs.push(function () { return 2; });
console.log(fs[0](), fs[1]());
for (var f of fs) console.log(f());
fs.forEach(g => console.log(g()));
var hs = [];
for (var i = 0; i < 3; i++) hs.push(function () { return i; });
console.log(hs.map(h => h()));
function m() { var fs = []; fs.push(function (a) { return a * 2; }); fs.push(function () { return "s"; }); return fs.map(function (f) { return f(4); }); }
console.log(m());
var t2 = []; t2.push((x) => x + 1); t2.push((y) => y + 2); console.log(t2[0](1), t2[1](1));
`)
}

// An undefined or null array index is 0; an onFinally reading `arguments`
// is called with none.
func TestE2EJSUndefinedIndexAndFinallyArguments(t *testing.T) {
	assertJSFileSameAsNode(t, `console.log([0, 1, 2, 3].copyWithin(1, undefined), [0, 1, 2, 3].copyWithin(1, null));
console.log(["a", "b", "c"].toSpliced(undefined, undefined), ["a", "b"].toSpliced(undefined));
console.log([1, 2, 3].slice(undefined, null), [1, 2, 3].at(undefined));
Promise.resolve(1).finally(function () { console.log("fin", arguments.length); return {}; }).then(function (x) { console.log("then", x); });
`)
}

// A generator resumed from its own body throws "already running" and ends;
// a done step read through a dynamic receiver has value undefined.
func TestE2EJSGeneratorReentry(t *testing.T) {
	assertJSFileSameAsNode(t, `var iter;
function* g() { iter.next(); }
iter = g();
try { iter.next(); } catch (e) { console.log(e.constructor.name, e.message); }
var r = iter.next(); console.log(r.value, r.done);
function* h() { iter.return(); }
iter = h();
try { iter.next(); } catch (e) { console.log(e.constructor.name); }
r = iter.next(); console.log(r.value, r.done);
`)
}

// flatMap with a callback that falls off the end keeps each undefined.
func TestE2EJSFlatMapFallOff(t *testing.T) {
	assertJSFileSameAsNode(t, `var r = [1, 2, 3, 4].flatMap(function (x) { if (x % 2) return [x, x * 10]; });
console.log(r);
var s = ["a", "b"].flatMap(function (x) { if (x === "a") return x; });
console.log(s, s.length);
var c = [1, 2, 3].map(function (x) { if (x !== 2) return [[x], x]; });
console.log(c.flat(), c.flat(2));
`)
}

// A sloppy-mode (CommonJS) function called plainly sees globalThis as
// `this`; a method call its receiver (ADR-01716).
func TestE2EJSSloppyThisIsGlobal(t *testing.T) {
	assertJSFileSameAsNode(t, `function f() { return this; }
console.log(f() === globalThis, typeof f());
function g() { this.zz = 5; }
g();
console.log(globalThis.zz);
var o = { m: f };
console.log(o.m() === o);
var h = o.m; console.log(h() === globalThis);
console.log(f.call(null) === globalThis, f.call(o) === o);
`)
}

func TestE2EJSFileSloppyThisWrapsPrimitive(t *testing.T) {
	// A sloppy function's primitive `this` is its wrapper object, whose
	// members are the primitive's (ADR-01760).
	assertJSFileSameAsNode(t, `
function f() { return [typeof this, this + 1, this.toFixed(1), this instanceof Number].join(" "); }
function s() { return this.length; }
var y = [Object("xyz")];
console.log(f.call(5), s.call("abc"), [1].map(function () { return typeof this; }, 7)[0], y[0].length, y[0].at(-1));
`)
}

func TestE2EJSFileUndeclaredNames(t *testing.T) {
	// An undeclared name: read, a ReferenceError unless the global object
	// has it; assigned in sloppy code, a global property (ADR-01764).
	assertJSFileSameAsNode(t, `
function t(fn) { try { console.log(fn()); } catch (e) { console.log(e.constructor.name, e.message); } }
t(function () { return nope; });
t(function () { return typeof nope; });
t(function () { return nope(); });
var x = 1;
y = 2;
function f() { z = 3; return y + z; }
console.log(f(), y, z);
t(function () { q += 1; });
globalThis.zz = 4;
t(function () { return zz * 2; });
`)
}

func TestE2EJSFileDetachedPrototypeMethods(t *testing.T) {
	// Generator methods sit on C.prototype, and a detached method whose
	// body never reads `this` runs on any receiver (ADR-01767).
	assertJSFileSameAsNode(t, `
class C { m(a) { return a + 1; } *g() { yield 2; } async *ag() { yield arguments.length; } }
console.log(typeof C.prototype.g, typeof C.prototype.ag, Object.getOwnPropertyNames(C.prototype));
var f = C.prototype.m; console.log(f(1));
var g = C.prototype.g; console.log([...g()]);
var ag = C.prototype.ag; ag(1, 2).next().then(function (r) { console.log(r.value); });
`)
}

func TestE2EJSFileMethodCallArgumentCounts(t *testing.T) {
	// Extra arguments to a method are evaluated and ignored, and a
	// generator's `arguments` is not the count an earlier call left
	// (ADR-01769).
	assertJSFileSameAsNode(t, `
var log = [];
function note(v) { log.push(v); return v; }
class C { m(a) { return a + 1; } static s() { return 9; } *g() { yield arguments.length; } }
var o = new C();
console.log(o.m(note(1), note(2), note(3)), C.s(note(4)), log);
var f = C.prototype.m; f(1);
var g = C.prototype.g; console.log(g(1, 2).next().value);
`)
}

func TestE2EJSFileDetachedAsyncMethodThen(t *testing.T) {
	// A detached async method called through `any` settles through its
	// own then (ADR-01768).
	assertJSFileSameAsNode(t, `
class C { async am() { return arguments.length; } }
var am = C.prototype.am;
am(1, 2).then(function (r) { console.log("D", r); });
`)
}

func TestE2EJSFileAsyncGeneratorStepTiming(t *testing.T) {
	// An async generator's yield and return await their value, so each
	// request settles a microtask after an async function's (ADR-01770).
	assertJSFileSameAsNode(t, `
var log = [];
async function* ag() { yield 1; await undefined; return 2; }
async function af() { return 0; }
var it = ag();
it.next().then(function (r) { log.push("next1 " + r.value); });
it.next().then(function (r) { log.push("next2 " + r.value); });
it.next().then(function (r) { log.push("next3 " + r.done); });
af().then(function () { log.push("af"); });
var p = Promise.resolve();
for (var i = 1; i <= 6; i++) (function (n) { p = p.then(function () { log.push("tick" + n); }); })(i);
async function* pg() { yield Promise.resolve(5); return Promise.resolve(6); }
var pit = pg();
pit.next().then(function (r) { log.push("p " + r.value); });
pit.next().then(function (r) { log.push("p " + r.value + " " + r.done); });
setTimeout(function () { console.log(log.join(", ")); }, 10);
`)
}

func TestE2EJSFileYieldStarProtocol(t *testing.T) {
	// yield* over a plain iterable object forwards next, throw and return,
	// in sync and async generators (ADR-01771).
	assertJSFileSameAsNode(t, `
var so = { [Symbol.iterator]() { var n = 0; return { next(v) { n++; console.log("next", v); return { value: n, done: n > 2 }; }, return(v) { console.log("ret", v); return { value: v, done: true }; } }; } };
function* g() { var r = yield* so; console.log("r", r); return 9; }
var it = g(); console.log(it.next("a"), it.next("b"), it.next("c"), it.next());
var it2 = g(); it2.next(); console.log(it2.return(5));
var it1 = { [Symbol.iterator]() { return this; }, next() { return { value: 1, done: false }; }, throw(e) { console.log("inner throw", e); return { value: "t", done: true }; } };
function* g1() { var r = yield* it1; console.log("after", r); yield "end"; }
var a = g1(); a.next(); console.log(a.throw("boom"));
var it3 = { [Symbol.iterator]() { return this; }, next() { return { value: 1, done: false }; }, return() { console.log("closed"); return {}; } };
function* g3() { yield* it3; }
var b = g3(); b.next(); try { b.throw(new Error("x")); } catch (e) { console.log(e.constructor.name, e.message); }
var ao = { [Symbol.asyncIterator]() { var n = 0; return { next() { n++; return Promise.resolve({ value: "x" + n, done: n > 2 }); } }; } };
async function* ag() { var r = yield* ao; console.log("ar", r); yield* [Promise.resolve(7)]; }
(async () => { for await (const v of ag()) console.log("got", v); })();
`)
}

func TestE2EJSFileSpreadInAnyArrayAndOwnArguments(t *testing.T) {
	// Spread in an any-typed array literal, and a nested function's own
	// arguments (ADR-01771).
	assertJSFileSameAsNode(t, `
var log = [];
function f() { log.push({ args: [...arguments], more: [0, ...new Set([1, 2]), ..."ab"] }); }
f(1, "x");
console.log(JSON.stringify(log));
var o = { m() { return function () { return arguments.length; }; }, get g() { return function () { return arguments.length; }; } };
var m = o.m(), g = o.g;
console.log(m(undefined), m(1, 2), g(undefined), g.call(o, 1, 2));
function outer() { var h = function () { return arguments.length; }; var k = () => arguments.length; return [h(1, 2, 3), k(), arguments.length]; }
console.log(outer(9, 8));
`)
}

func TestE2EJSFileGeneratorThrowsAnyValue(t *testing.T) {
	// A generator throws, and .throw() injects, the value itself, not an
	// Error wrapping it (ADR-01772).
	assertJSFileSameAsNode(t, `
var reason = { r: 1 };
function* s1() { throw reason; }
try { s1().next(); } catch (e) { console.log("s1", e === reason); }
function* s2() { throw 42; }
try { s2().next(); } catch (e) { console.log("s2", e); }
async function* a1() { throw reason; }
a1().next().then(() => console.log("a1 fulfilled"), e => console.log("a1 rejected", e === reason));
function* g() { try { yield 1; } catch (e) { console.log("caught", typeof e, e); yield 2; } }
var it = g(); it.next(); console.log(it.throw("str"));
var it2 = g(); try { it2.throw(7); } catch (e) { console.log("unstarted", e); }
async function* ag() { try { yield 1; } catch (e) { console.log("acaught", e); } }
var ai2 = ag(); ai2.throw(null).catch(function (e) { console.log("arej", e); });
`)
}

func TestE2EJSFileAsyncGeneratorCompletionValues(t *testing.T) {
	// An async generator's completion results carry undefined (not a
	// zero), return(v) carries v, and each return awaits v as Node does
	// (ADR-01773).
	assertJSFileSameAsNode(t, `
async function* ag() { yield 1; }
var a = ag(); a.next(); a.next().then(function (r) { console.log("A", r); });
async function* ag2() { yield 1; return 5; }
var b = ag2(); b.next(); b.next().then(function (r) { console.log("B", r); }); b.next().then(function (r) { console.log("B2", r); });
var c = ag(); c.next(); c.return(7).then(function (r) { console.log("C", r); }); c.return(8).then(function (r) { console.log("C2", r); }); c.next().then(function (r) { console.log("C3", r); });
var d = ag(); d.return(3).then(function (r) { console.log("D", r); });
(async () => { for await (const v of ag2()) console.log("F", v + 1); })();
var log = [];
var e = ag(); e.next().then(function () { e.return(9).then(function () { log.push("suspended-return"); }); log.push("e-next1"); });
var p = Promise.resolve();
for (var i = 1; i <= 6; i++) (function (n) { p = p.then(function () { log.push("t" + n); }); })(i);
setTimeout(function () { console.log(log.join(", ")); }, 10);
`)
}

func TestE2EJSFileGeneratorParameterDefaults(t *testing.T) {
	// Generator functions and methods take default and optional parameters
	// (ADR-01774).
	assertJSFileSameAsNode(t, `
function* g(a, b = a + 1, c) { yield a; yield b; yield c; }
console.log([...g(1)], [...g(1, 5, 6)], [...g(1, undefined, 2)], g.length);
var k = 10;
function* h(x = k * 2) { yield x; }
console.log([...h()], [...h(3)]);
async function* ag(x = 7) { yield x; }
(async () => { for await (const v of ag()) console.log("ag", v); })();
var o = { *m(p = "d") { yield p; } };
console.log([...o.m()], [...o.m("q")]);
class C { *m(a, b = []) { yield b.length; } }
console.log([...new C().m(1)]);
var f = function* (z = 4) { yield z; };
console.log(f().next().value);
`)
}

func TestE2EJSFileGeneratorMethodsOnForeignReceiver(t *testing.T) {
	// A this-reading generator or async method runs on a receiver that is
	// not an instance, its prototype included (ADR-01775).
	assertJSFileSameAsNode(t, `
var C = class {
  async *method() { console.log("ag", typeof this.method, this === C.prototype); yield 1; }
  *g(a) { yield this === C.prototype; yield a; }
  async am(x) { return this === C.prototype ? x : -1; }
};
C.prototype.method().next().then(function (r) { console.log("r", r); });
console.log([...C.prototype.g(5)]);
C.prototype.am(3).then(function (v) { console.log("am", v); });
var o = { k: 9 };
console.log([...C.prototype.g.call(o, 2)]);
`)
}

func TestE2EJSFileGeneratorDefaultsRunAtCall(t *testing.T) {
	// Parameter defaults run when the generator is called, a throwing one
	// throwing from the call (ADR-01774).
	assertJSFileSameAsNode(t, `
var log = [];
function* g(a = log.push("dflt")) { log.push("body"); yield a; }
var it = g(); log.push("made"); it.next(); console.log(log.join(","));
function* t(a = (() => { throw new Error("x"); })()) { yield 1; }
try { t(); console.log("no throw"); } catch (e) { console.log("threw", e.message); }
async function* ag(a = log.push("adflt")) { yield a; }
log = []; var ai = ag(); log.push("amade"); console.log(log.join(","));
ai.next().then(function (r) { console.log(r.value); });
var r = g(); console.log(JSON.stringify(r.return(4)), JSON.stringify(r.next()));
var th = g(); try { th.throw(new Error("e")); } catch (e) { console.log("thr", e.message, JSON.stringify(th.next())); }
`)
}

func TestE2EJSFileDefaultParamsTakeNull(t *testing.T) {
	// A null argument is not absent: the default stays unused; a method's
	// length stops at its first defaulted parameter (ADR-01776).
	assertJSFileSameAsNode(t, `
var n = 0, obj = {};
var C = class { *m(a = n += 1, b = n += 1, c = n += 1) { console.log(a, b, c === obj, n); } };
C.prototype.m(false, null, obj).next();
new C().m(false, null, obj).next();
function* g(a = n += 1, b = n += 1) { console.log(a, b, n); }
g(false, null).next();
function f(a = 5) { return a; }
console.log(f(null), f(), f(undefined), (function (x = 2) { return x; })(null), ((y = 3) => y)(null));
var C = class { async *method(a, b = 39,) { console.log("in", a, b); } };
var ref = C.prototype.method;
console.log(ref.length);
ref(42, undefined, 1).next().then(function (r) { console.log("done", r.done); });
var D = class { *m(a, b = 3) { yield b; } };
console.log(D.prototype.m.length, [...D.prototype.m(1)]);
function g2(s = "x") { return s + "!"; } console.log(g2(), g2("y"), g2(null));
`)
}

func TestE2EJSFileClassValuesAndRedeclaredClasses(t *testing.T) {
	// A class value has its prototype; a name declared twice holds each
	// class in turn (ADR-01777).
	assertJSFileSameAsNode(t, `
function mk() { return class { m(a) { return a + 1; } }; }
var K = mk();
console.log(typeof K.prototype, K.prototype.m(1), new K().m(2));
var C = class { m() { return 1; } };
var C = class { m() { return 2; } };
console.log(typeof C.prototype, new C().m());
`)
}

func TestE2EJSFileDestructuredParamDefaults(t *testing.T) {
	// A destructured parameter takes a default (ADR-01778).
	assertJSFileSameAsNode(t, `
function f({ x, y } = { x: 1, y: 2 }) { return x + y; }
console.log(f(), f({ x: 5, y: 6 }));
function g([a, b] = [3, 4]) { return a * b; }
console.log(g(), g([2, 2]));
var h = ({ k } = { k: "d" }) => k;
console.log(h(), h({ k: "z" }));
var o = { m([p] = [9]) { return p; } };
console.log(o.m(), o.m([1]));
class C { m({ q } = { q: 7 }) { return q; } }
console.log(new C().m(), new C().m({ q: 0 }));
var n = 0;
function s([z] = [n++]) { return z; }
console.log(s(), s(), s([42]), n);
`)
}

func TestE2EJSFileGeneratorDestructuredParams(t *testing.T) {
	// Generators take destructured parameters, unpacked at the call
	// (ADR-01779).
	assertJSFileSameAsNode(t, `
function* g([a, b], { c }) { yield a; yield b; yield c; }
console.log([...g([1, 2], { c: 3 })]);
function* h([x, y] = [7, 8]) { yield x + y; }
console.log([...h()], [...h([1, 1])]);
var o = { *m({ p, q = 5 } = {}) { yield p; yield q; } };
console.log([...o.m()], [...o.m({ p: 1, q: 2 })]);
class C { *m([z, ...r]) { yield z; yield r.length; } }
console.log([...new C().m([1, 2, 3])]);
async function* ag({ v }) { yield v; }
(async () => { for await (const w of ag({ v: "av" })) console.log(w); })();
function* t([u]) { yield u; }
try { t(null); console.log("no"); } catch (e) { console.log(e.constructor.name); }
var log = [];
function* it([k]) { log.push("body"); yield k; }
var gi = it({ [Symbol.iterator]() { log.push("iter"); return [5][Symbol.iterator](); } });
log.push("made"); gi.next(); console.log(log.join(","));
`)
}

func TestE2EJSFileNestedPatternDefaults(t *testing.T) {
	// A nested destructuring pattern takes a default, in declarations,
	// parameters and assignments; an undefined element takes its default
	// (ADR-01780).
	assertJSFileSameAsNode(t, `
var [[a, b] = [1, 2], c] = [undefined, 3];
console.log(a, b, c);
var [[d] = [9]] = [[4]];
console.log(d);
var { k: { x, y = 7 } = { x: 5 } } = {};
console.log(x, y);
var { m: [p, q] = [8, 9] } = { m: [1] };
console.log(p, q);
function f([{ u } = { u: "dflt" }]) { return u; }
console.log(f([]), f([{ u: "given" }]));
function* g({ w: [z] = [11] }) { yield z; }
console.log([...g({})]);
var n = 0;
var [[e1] = [n++]] = [];
console.log(e1, n);
var [h = [7, 8]] = [];
console.log(h);
let [hh2 = [7, 8]] = [];
console.log(hh2);
var src4 = [];
var [hh3 = [7, 8]] = src4;
console.log(hh3);
{
let a, b, c, x, y, p;
[[a, b] = [1, 2], c] = [undefined, 3];
console.log(a, b, c);
[[a] = [9]] = [[4]];
console.log(a);
({ k: { x, y = 7 } = { x: 5 } } = {});
console.log(x, y);
({ m: [p] = [8] } = { m: [1] });
console.log(p);
({ m: [p] = [8] } = { m: undefined });
console.log(p);
}
`)
}

func TestE2EJSFileGetterReturnedMethodsCalled(t *testing.T) {
	// A static or instance getter returning a function or (private)
	// generator method is called through its value (ADR-01781).
	assertJSFileSameAsNode(t, `
class C {
  *#g(a) { yield a; }
  get g() { return this.#g; }
  static *#s(b) { yield b; }
  static get s() { return this.#s; }
  run() { return [...this.#g(3)]; }
}
console.log(new C().run());
console.log([...new C().g(1)]);
console.log([...C.s(2)]);
class D { static get f() { return function (x) { return x * 2; }; } get h() { return (y) => y + 1; } }
console.log(D.f(4), new D().h(1));
class C6 {
  *m(a) { console.log("ran", a); return; }
  get g() { return this.m; }
}
new C6().g(1).next();
`)
}

func TestE2EJSFileIteratorDestructuring(t *testing.T) {
	// Array destructuring of an iterable steps it only as far as the
	// pattern reads, closing it when unfinished (ADR-01782).
	assertJSFileSameAsNode(t, `
var log = [];
function mk(n) {
  var i = 0, o = {};
  o[Symbol.iterator] = function () { return { next: function () { i++; log.push("next" + i); return { value: i, done: i > n }; }, return: function () { log.push("return"); return {}; } }; };
  return o;
}
function f([a, b]) { return a + b; }
console.log(f(mk(10)), log.join(","));
log = [];
function g([a, ...r]) { return r.length; }
console.log(g(mk(3)), log.join(","));
log = [];
function h([]) { return 0; }
console.log(h(mk(3)), log.join(","));
log = [];
var k = ([x, , z = 9]) => [x, z];
console.log(k(mk(1)), log.join(","));
log = [];
function m([[p] = [5]]) { return p; }
console.log(m(mk(0)), log.join(","));
var [a, b] = mk(10);
console.log(a, b, log.join(","));
log = [];
var [c, ...r] = mk(3);
console.log(c, r, log.join(","));
log = [];
var [] = mk(3);
console.log(log.join(","));
log = [];
var x, y;
[x, y] = mk(5);
console.log(x, y, log.join(","));
log = [];
var [[p, q]] = [mk(9)];
console.log(p, q, log.join(","));
log = [];
console.log([...mk(2)], log.join(","));
`)
}

func TestE2EJSFileVoidThenCallbacks(t *testing.T) {
	// A callback block with no return resolves the next promise to
	// undefined, however its body's last statement is typed (ADR-01783).
	assertJSFileSameAsNode(t, `
var cc = 1;
function f(a, b) { return a === b; }
function done(v) { console.log("done", v); }
Promise.resolve(5).then(() => { cc++; }).then(done);
Promise.resolve(5).then(() => { f(cc, 1); }).then(done, done);
Promise.reject(5).catch(() => { cc++; }).then(done);
Promise.reject(5).then(null, (e) => { f(e, 1); }).then(done);
async function* ag() { cc = 2; }
ag().next().then(() => { f(cc, 1); }).then(done, done);
`)
}

func TestE2EJSFilePatternParamMethodValues(t *testing.T) {
	// A method with an unannotated destructured parameter, read as a
	// value (directly or through a getter), is callable (ADR-01784).
	assertJSFileSameAsNode(t, `
class C {
  #m([x, y]) { console.log("priv", x, y); }
  get m() { return this.#m; }
  pub({ a }) { console.log("pub", a); }
}
var c = new C();
c.m([1, 2]);
var f = c.pub;
console.log(typeof f);
f({ a: 7 });
`)
}

func TestE2EJSFilePatternDefaultNames(t *testing.T) {
	// An anonymous function or class that is a pattern element's,
	// parameter's, property's or assignment's default takes that name
	// (ADR-01785).
	assertJSFileSameAsNode(t, `
function f([a = () => {}, b = function () {}, c = class {}, d = (function () {}), e = function* () {}]) {
  console.log(a.name, b.name, c.name, d.name, e.name);
}
f([]);
function g({ a = () => {}, b = function () {}, c = class {} }) { console.log(a.name, b.name, c.name); }
g({});
var [x = () => {}] = []; console.log(x.name);
var { y = () => {} } = {}; console.log(y.name);
function h(k = class {}) { console.log(k.name); } h();
var o = { q: class {} }; console.log(o.q.name);
var z; z = class {}; console.log(z.name);
`)
}

func TestE2EJSFileEmptyObjectPatternCoercible(t *testing.T) {
	// An empty object pattern still rejects null and undefined (ADR-01786).
	assertJSFileSameAsNode(t, `
function t(f) { try { f(); console.log("no throw"); } catch (e) { console.log(e instanceof TypeError, e.message); } }
class C { method({}) {} }
var c = new C();
t(function () { c.method(null); });
t(function () { c.method(undefined); });
t(function () { c.method(0); });
function f({}) {} t(function () { f(null); });
var g = ({}) => 1; t(function () { g(); });
`)
}

func TestE2EJSFileParamDefaultCaptures(t *testing.T) {
	// A parameter default, or a default inside a destructured parameter,
	// reading an enclosing local captures it (ADR-01787).
	assertJSFileSameAsNode(t, `
var iter = function* () {}();
var f;
f = function (a = iter) { return typeof a; };
console.log(f());
var g;
g = function* ([] = iter) { yield 1; };
console.log(g().next().value);
var calls = 0;
var h;
h = function* ([[] = function () { calls += 1; return iter; }()]) { yield calls; };
console.log(h([]).next().value);
function w() {
  var local = [1, 2];
  var k = function (x = local) { return x.length; };
  var m = ([y] = local, { z = local } = {}) => y + z.length;
  return k() + m();
}
console.log(w());
var p = function (a = arguments.length) { return a; };
console.log(p(undefined, 2, 3));
`)
}

func TestE2EJSFileFunctionSelfName(t *testing.T) {
	// A named function or generator expression's own name, inside it, is
	// an immutable binding to itself (ADR-01788).
	assertJSFileSameAsNode(t, `
let ref = function B() { B = 1; return B; };
console.log(ref() === ref);
let gen = function* G() { G = 1; yield 1; return G; };
var it = gen(); it.next();
console.log(it.next().value === gen);
function w() {
  var x = 1;
  let h = function* H() { H += 2; return [H, x]; };
  console.log(h().next().value[0] === h);
  function* n() { yield x; yield n; }
  var j = n();
  console.log(j.next().value, j.next().value === n);
  function* m() { return m; }
  console.log(m().next().value === m);
}
w(); w();
var arr = [function* A() { return A; }];
console.log(arr[0]().next().value === arr[0]);
var g = 'outside';
var probe;
var func = function* g(_ = (probe = function () { return g; })) {};
func().next();
console.log(probe() === func, g);
let viaArrow = function C() { (() => { C = 1; })(); return C; };
console.log(viaArrow() === viaArrow);
let asyncRef = async function D() { (() => { D = 1; })(); return D; };
asyncRef().then(v => console.log(v === asyncRef));
`)
}

func TestE2EJSFileFunctionSelfNameStrict(t *testing.T) {
	// In strict code an assignment to the self name throws (ADR-01788).
	assertJSFileSameAsNode(t, `"use strict";
let ref = function B() { try { B = 1; } catch (e) { console.log(e.constructor.name, e.message); } return B; };
console.log(ref() === ref);
let gen = function* G() { try { G = 1; } catch (e) { console.log(e.message); } return G; };
console.log(gen().next().value === gen);
let viaArrow = function C() { try { (() => { C = 1; })(); } catch (e) { console.log(e.constructor.name); } return C; };
console.log(viaArrow() === viaArrow);
`)
}

func TestE2EJSFileGeneratorReturnValues(t *testing.T) {
	// A generator's value slot holds returns of another type than its
	// yields (ADR-01789).
	assertJSFileSameAsNode(t, `
function* g() { yield 1; return g; }
var it = g(); it.next();
var r = it.next();
console.log(typeof r.value, r.value === g, r.done);
function* s() { yield 1; return "end"; }
console.log([...s()], s().next(), Array.from(s()));
var x = s(); x.next(); console.log(x.next());
`)
}

func TestE2EJSFilePropertyIsEnumerable(t *testing.T) {
	// propertyIsEnumerable on a dynamic object reads its own property's
	// attributes, symbol keys included (ADR-01790).
	assertJSFileSameAsNode(t, `
var s = Symbol("s");
var o = { a: 1 }; o[s] = 4;
Object.defineProperty(o, "hid", { value: 5, enumerable: false });
console.log(o.propertyIsEnumerable("a"), o.propertyIsEnumerable(s), o.propertyIsEnumerable("hid"), o.propertyIsEnumerable("toString"));
console.log(Object.prototype.propertyIsEnumerable.call(o, s), Object.prototype.propertyIsEnumerable.call(o, "hid"));
console.log(Object.prototype.propertyIsEnumerable.call([1], "0"), Object.prototype.propertyIsEnumerable.call([1], "length"));
`)
}

func TestE2EJSFileDynamicObjectRest(t *testing.T) {
	// An object rest element over a dynamic or primitive value copies its
	// remaining own enumerable properties (ADR-01791).
	assertJSFileSameAsNode(t, `
var s = Symbol("s");
var src = { a: 1, b: 2, c: 3, get g() { return "got"; } };
src[s] = 4;
Object.defineProperty(src, "hid", { value: 5, enumerable: false });
function f({ a, ...rest }) { return [a, rest]; }
console.log(f(src));
var { b, ...r2 } = src; console.log(b, r2);
var { ...all } = "xy"; console.log(all);
var x, r3; ({ a: x, ...r3 } = src); console.log(x, r3);
var k = "c", r4; ({ [k]: x, ...r4 } = src); console.log(x, r4);
var r5; ({ [s]: x, ...r5 } = src); console.log(x, r5);
var g = ({ c, ...q } = { c: 9, d: 10 }) => q; console.log(g(), g({ c: 1, e: 2 }));
try { var { ...n } = null; } catch (e) { console.log(e.constructor.name, e.message); }
class K { constructor() { this.p = 1; this.q = 2; } }
var { p, ...kr } = new K(); console.log(p, kr);
function h(v) { var { ...a } = v; return a; }
console.log(h({ z: 1 }), h("ab"), h(5), h([7, 8]));
var { length } = "abc"; console.log(length);
`)
}

func TestE2EJSFileStaticNameMember(t *testing.T) {
	// A class's own static `name` or `length` member replaces the class's
	// name and constructor length, read directly or through a reference
	// (ADR-01792).
	assertJSFileSameAsNode(t, `
class C { static name() { return 1; } static length() {} }
console.log(typeof C.name, typeof C.length);
var d = C; console.log(typeof d.name, typeof d.length);
var e = [C][0]; console.log(typeof e.name, e.name(), typeof e.length);
class P { constructor(a, b) {} }
var q = [P][0]; console.log(q.name, q.length);
var obj = { method({ cls = class {}, xCls = class X {}, xCls2 = class { static name() {} } }) { console.log(cls.name, xCls.name, typeof xCls2.name); } };
obj.method({});
`)
}

func TestE2EJSFileComputedPatternKeys(t *testing.T) {
	// A runtime computed key in an object pattern is evaluated once, in
	// pattern order, as a property key; a rest leaves it out (ADR-01793).
	assertJSFileSameAsNode(t, `
var s = Symbol("s"), k = "b", log = [];
var src = { a: 1, b: 2, c: 3 }; src[s] = 4;
var { [k]: x, ...r } = src; console.log(x, r);
var { [s]: y, [k + "x"]: z = 9 } = src; console.log(y, z);
function f({ [k]: v, ...rest }) { return [v, rest]; }
console.log(f(src));
var g = ({ [(log.push("key"), "a")]: q = (log.push("dflt"), 0) }) => q; console.log(g({}), g({ a: 5 }), log);
var { [1]: one } = [7, 8]; console.log(one);
var { [k]: { inner } } = { b: { inner: "in" } }; console.log(inner);
class C { m({ [k]: w }) { return w; } } console.log(new C().m({ b: "cm" }));
function d({ a: [p = k] }) { return p; } console.log(d({ a: [] }));
`)
}

func TestE2EJSFileFunctionOwnPropertyChecks(t *testing.T) {
	// hasOwnProperty on a function or primitive reads its boxed value, and
	// a generator function's typeof is "function" (ADR-01794).
	assertJSFileSameAsNode(t, `
function* g(a, b = 1) {}
function f(x) {}
console.log(typeof g, g.name, g.length);
console.log(Object.prototype.hasOwnProperty.call(g, "name"), Object.prototype.hasOwnProperty.call(f, "length"), Object.prototype.hasOwnProperty.call(f, "nope"));
console.log(Object.getOwnPropertyDescriptor(g, "name"), Object.getOwnPropertyDescriptor(f, "length"));
console.log(Object.prototype.propertyIsEnumerable.call(g, "name"), Object.hasOwn(f, "name"));
console.log(Object.prototype.hasOwnProperty.call("ab", "length"), Object.prototype.hasOwnProperty.call("ab", "2"));
`)
}

func TestE2EJSFileGeneratorPatternDefaultNames(t *testing.T) {
	// A generator's destructured parameter defaults take their binding's
	// name, as an ordinary function's do (ADR-01795).
	assertJSFileSameAsNode(t, `
class C {
  *method({ arrow = () => {}, fn = function() {}, xFn = function x() {}, cls = class {}, cover = (function () {}) }) {
    console.log(arrow.name, fn.name, xFn.name, cls.name, cover.name);
  }
  static *s([a = () => {}]) { console.log(a.name); }
}
new C().method({}).next();
C.s([]).next();
function* g({ q = () => {} }) { console.log(q.name); }
g({}).next();
`)
}

func TestE2EJSFileNonObjectElementPatterns(t *testing.T) {
	// An object pattern over an array element that is not a static object
	// destructures the element as a value, and a dynamic default keeps its
	// value in a number array's binding (ADR-01796).
	assertJSFileSameAsNode(t, `
var obj = { m([{ x }]) { return x; } };
try { obj.m([null]); } catch (e) { console.log(e.constructor.name, e.message); }
try { obj.m([undefined]); } catch (e) { console.log(e.constructor.name); }
try { obj.m([]); } catch (e) { console.log(e.constructor.name); }
console.log(obj.m([{ x: 3 }]), obj.m(["ab"]));
var [{ length }] = ["xyz"]; console.log(length);
var [{ q } = { q: 7 }] = []; console.log(q);
var [h = { q: 7 }] = []; console.log(h);
`)
}

func TestE2EJSFileGeneratorThis(t *testing.T) {
	// A generator function reads the receiver its call supplies, the global
	// object when a sloppy call supplies none, as does any sloppy function's
	// `this` held in a binding (ADR-01797).
	assertJSFileSameAsNode(t, `
var o = { n: 2 };
o.g = function* () { yield this.n; yield this.n + 1; };
console.log([...o.g()]);
function* h(a = 1) { yield typeof this; yield arguments.length; yield a; }
console.log([...h()], [...h.call(o, 5, 6)]);
var f = function* () { yield this === o; };
console.log(f.call(o).next().value);
function k() { const t = this; return typeof t; }
function j() { const t = this; return () => t === globalThis; }
console.log(k(), j()());
function q(a = 1) { const t = this; return arguments.length; }
function s(a) { const t = this; return arguments.length; }
var m = function (a) { const t = this; return [arguments.length, a]; };
console.log(q(), q(5), q(5, 6), s(), s(1, 2, 3), m(), m(4, 5));
`)
}

func TestE2EJSFileObjectPrototypeMethodValues(t *testing.T) {
	// Object.prototype's methods read as values are functions over their
	// receiver, as Test262's propertyHelper binds them (ADR-01798).
	assertJSFileSameAsNode(t, `
var h = Object.prototype.hasOwnProperty;
console.log(typeof h, h.call({ a: 1 }, "a"), h.name, h.length);
var __hasOwnProperty = Function.prototype.call.bind(Object.prototype.hasOwnProperty);
console.log(__hasOwnProperty({ a: 1 }, "a"), __hasOwnProperty({}, "a"));
var __pie = Function.prototype.call.bind(Object.prototype.propertyIsEnumerable);
console.log(__pie({ a: 1 }, "a"), __pie([], "length"));
var ts = Object.prototype.toString; console.log(ts.call([]), ts.call(null), ts.call(1));
var vo = Object.prototype.valueOf; var c = {}; console.log(vo.call(c) === c, typeof vo.call(5));
console.log(Object.prototype.constructor === Object, Object.prototype.propertyIsEnumerable("toString"), Object.keys(Object.prototype));
console.log(Object.prototype);
for (var k in { a: 1 }) console.log(k);
var o = { a: 1 };
var f = o.hasOwnProperty; console.log(typeof f, f === Object.prototype.hasOwnProperty, f.call(o, "a"));
var t = o.toString; console.log(typeof t, t.call([]), o.toString === Object.prototype.toString);
var parsed = JSON.parse('{"x":1}'); console.log(typeof parsed.hasOwnProperty, typeof parsed.valueOf, String(parsed), parsed + "");
var np = Object.create(null); console.log(typeof np.toString);
`)
}

func TestE2EJSFileYieldIdentifier(t *testing.T) {
	// Outside a generator, sloppy code's `yield` is an identifier: a
	// binding, a function expression's name, a read (ADR-01799).
	assertJSFileSameAsNode(t, `
function* gen() {
  return (function (arg) { var yield = arg + 1; return yield; }(yield));
}
var it = gen(); it.next(); console.log(it.next(42));
var g = function* () { (function yield() {}); }; console.log(g().next());
var yield = 3; console.log(yield + 1);
function* s() { var o = { yield: 1 }; yield o.yield; } console.log([...s()]);
`)
}

func TestE2EJSFileClassMethodArity(t *testing.T) {
	// Untyped JavaScript may pass fewer or more arguments to a method,
	// static method or constructor than it declares (ADR-01800).
	assertJSFileSameAsNode(t, `
var log = [];
function s(x) { log.push(x); return x; }
class C { constructor(a, b) { this.a = a; this.b = b; } static m(a, b) { return [a, b]; } m2(a) { return a; } }
console.log(C.m(1), new C().m2(), new C(1).b);
console.log(C.m(s(1), s(2), s(3)), new C(s(4), s(5), s(6)).a, new C().m2(s(7), s(8)), log);
`)
}

func TestE2EJSFileUndefinedReceiverAndGeneratorPatternGlobals(t *testing.T) {
	// A read off a value typed undefined or null is a run-time TypeError,
	// and need not run; a generator parameter pattern's default reads a
	// top-level binding (ADR-01801).
	assertJSFileSameAsNode(t, `
function v() {}
var u = v(); console.log(u && u.next);
try { u.x; } catch (e) { console.log(e.message); }
var z = null; try { z.y; } catch (e) { console.log(e.message); }
var n = 0;
var iter = function* () { n += 1; }();
function* a([y = iter], [[] = function () { return iter; }()]) { console.log(n, typeof y); }
a([], []).next();
`)
}

func TestE2EJSFileBareYieldUndefined(t *testing.T) {
	// A bare `yield` (or one ended by a line break) yields undefined, also
	// beside yields of numbers (ADR-01803).
	assertJSFileSameAsNode(t, "function* g() {\n  yield\n  1\n}\n"+`
var it = g(); console.log(it.next(), it.next());
function* h() { yield 1; yield; var x = yield; return x; }
var i2 = h(); console.log(i2.next(), i2.next(), i2.next(), i2.next(7));
console.log([...h()]);
`)
}

func TestE2EJSFileSloppyFailedWrites(t *testing.T) {
	// In sloppy code a write or delete the object refuses does nothing;
	// strict code (a class body, a "use strict" function) throws. A
	// function's deleted `name`/`length` is no longer its own (ADR-01804).
	assertJSFileSameAsNode(t, `
function f() {}
f.name = "zz"; console.log(f.name);
var o = Object.freeze({ a: 1 }); o.a = 2; o.b = 3; console.log(o.a, o.b, delete o.a);
var d = Object.defineProperty({}, "x", { value: 1 }); console.log(delete d.x, d.x);
var j = JSON.parse('{"q":1}'); Object.freeze(j); j.q = 9; console.log(j.q, delete j.q);
(function () { "use strict"; try { o.a = 5; } catch (e) { console.log("strict", e.constructor.name); } })();
class K { static t() { try { o.a = 5; } catch (e) { console.log("class", e.constructor.name); } } } K.t();
function g() {}
g.x = 1; console.log("name" in g, g.hasOwnProperty("name"), "call" in g, g.hasOwnProperty("x"));
console.log(delete g.length, g.length, "length" in g, g.hasOwnProperty("length"), Object.keys(g));
console.log(delete g.name, Object.prototype.hasOwnProperty.call(g, "name"), JSON.stringify(Object.getOwnPropertyDescriptor(g, "name")));
var a = Object.freeze([1, 2]); var i = 1; a[0] = 9; a[5] = 1; a.length = 0; a[i] += 5; a[0]++; console.log(a, a.length);
var sl = Object.seal([1, 2]); sl.length = 1; sl[0] = 7; console.log(sl);
var ws = new String("ab"); ws[0] = "z"; ws.length = 9; console.log(ws[0], ws.length);
class A { constructor() { this.x = 1; } }
var fi = Object.freeze(new A()); fi.x = 2; fi.y = 3; fi.x += 5; fi.x++; console.log(fi.x, fi.y);
var ne = Object.preventExtensions({ k: 1 }); ne.z = 1; console.log(ne.z);
var str = "abc"; str.length = 1; str.q = 2; console.log(str.length, str.q, (5).foo, true.bar);
var lit = { a: 1, b: 2 }; console.log(delete lit.a, lit, Object.keys(lit));
(function () { "use strict";
  for (var f of [function () { a[0] = 3; }, function () { "abc".length = 1; }, function () { "abc".foo = 1; }, function () { (5).x = 1; }, function () { fi.x = 4; }])
    try { f(); } catch (e) { console.log(e.message); }
})();
`)
}

func TestE2EJSFileGeneratorArity(t *testing.T) {
	// A generator call may omit arguments (undefined) or pass extra ones,
	// evaluated in order (ADR-01805).
	assertJSFileSameAsNode(t, `
var log = [];
function s(x) { log.push(x); return x; }
function* g(a, b) { yield a; yield b; }
function* r(a, ...rest) { yield a; yield rest.length; }
console.log([...g(1)], [...g(s(1), s(2), s(3))], [...r()], [...r(1, 2, 3)], log);
var o = { *m(a, b) { yield [a, b]; } };
console.log([...o.m(1)], [...o.m(1, 2, 3)]);
`)
}

func TestE2EJSFileStringBindingAssignedNull(t *testing.T) {
	// A string binding later assigned null, in its own scope or a nested
	// function's, holds null rather than reading undefined (ADR-01807).
	assertJSFileSameAsNode(t, `
var g = "outside";
var setB = function() { g = null; };
setB(); console.log(g);
var h = "x"; function seth() { h = null; } seth(); console.log(h);
var k = "y"; k = null; console.log(k, k === null);
var a = "s"; var seta = () => { a = 5; }; seta(); console.log(a, typeof a);
`)
}

func TestE2EJSFileComputedSymbolMethodCall(t *testing.T) {
	// A call through a well-known symbol key on a plain object has its
	// method's result, in a binding too (ADR-01808).
	assertJSFileSameAsNode(t, `
var o = { [Symbol.iterator]() { return [5][Symbol.iterator](); } };
var it = o[Symbol.iterator](); console.log(it.next());
var [k] = o; console.log(k);
function z([q]) { return q; } console.log(z(o));
var s = { [Symbol.iterator]() { return "s"; } };
var f = s[Symbol.iterator](); console.log(typeof f, f.length);
`)
	assertSameAsNode(t, `
const o = { [Symbol.iterator]() { return [5][Symbol.iterator](); } };
const f = o[Symbol.iterator]();
console.log(f.next());
`)
}

func TestE2EJSFileRestParameterPattern(t *testing.T) {
	// A rest parameter may be a destructuring pattern, over the collected
	// array (ADR-01809).
	assertJSFileSameAsNode(t, `
var fe = function(...[a, , b]) { return [a, b]; }; console.log(fe(1, 2, 3));
var ar = (...{ length, 1: s }) => [length, s]; console.log(ar(4, 5));
class K { m(...{ length }) { return length; } static s(...[x = 7]) { return x; } }
console.log(new K().m(1, 2, 3), K.s(), K.s(2));
async function af(...[a, b]) { return a * b; } af(3, 4).then(console.log);
function nest(...[[a], { b }]) { return a + b; } console.log(nest([1], { b: 2 }));
function dflt(...[a = 10, ...r]) { return [a, r]; } console.log(dflt(), dflt(1, 2, 3));
function* g(x, ...{ length }) { yield length; } console.log([...g(1, 2, 3)]);
class C { *m(...[p, q]) { yield q; } } console.log([...new C().m(4, 6)]);
`)
	assertSameAsNode(t, `
function f(...[a, b]: number[]): number { return a + b; }
console.log(f(1, 2));
const g = (...{ length }: string[]) => length; console.log(g("a", "b"));
`)
}

func TestE2EJSFileGeneratorPrototypes(t *testing.T) {
	// A generator function owns a `prototype` its instances inherit, over
	// %GeneratorPrototype%; its own prototype is %GeneratorFunction.prototype%
	// (ADR-01810).
	assertJSFileSameAsNode(t, `
var g = function*() { yield 1; };
console.log(typeof g.prototype, Object.getPrototypeOf(g()) === g.prototype);
var gp = Object.getPrototypeOf(g);
console.log(typeof gp, typeof gp.prototype, gp.prototype === Object.getPrototypeOf(g.prototype));
function* h() {}
console.log(Object.getPrototypeOf(h()) === h.prototype, Object.getOwnPropertyNames(h).sort());
h.prototype.extra = function() { return "ex"; };
var it = h(); var f = it.extra;
console.log(it.extra(), typeof f, h().extra());
h.prototype.v = 5; console.log(it.v, it.zz);
console.log(g.prototype, Object.prototype.toString.call(g.prototype), Object.getOwnPropertyNames(function*() {}.prototype));
async function* ag() {} console.log(ag.prototype);
var GP = Object.getPrototypeOf(g).prototype; g.prototype = null;
console.log(Object.getPrototypeOf(g()) === GP);
var k = function*(a = (k.prototype = null)) {}; var old = k.prototype;
console.log(Object.getPrototypeOf(k()) === old);
try { new h(1); } catch (e) { console.log(e.constructor.name, e.message); }
`)
}

func TestE2EJSFileStaticAsyncGeneratorErrorFrames(t *testing.T) {
	// An error made in a function called from a static async generator
	// method that destructures its parameter: the body started at the call,
	// and its callees' frames must not reach the creator's, gone by the
	// time it resumes (ADR-01813).
	assertJSFileSameAsNode(t, `
function h() { return new Error("e").message; }
function throws(f) { try { f(); } catch (e) { return e.constructor.name; } }
class K {
  static async *m({ x: y }) {
    console.log(h(), y, throws(function() { throw new ReferenceError("q"); }));
    yield 1;
    console.log(h(), throws(function() { null.p; }));
  }
}
var it = K.m({ x: 23 });
it.next().then(() => it.next()).then(() => console.log("done"));
`)
}

func TestE2EJSFileGeneratorFunctionObject(t *testing.T) {
	// A generator function's own prototype chain and instanceof, and its
	// `caller`/`arguments`: Function.prototype's throwing accessors, while
	// a sloppy function owns read-only ones (ADR-01815).
	assertJSFileSameAsNode(t, `
function f() {}
var g = function* () {};
var GF = Object.getPrototypeOf(g);
console.log(Object.getPrototypeOf(GF) === Object.getPrototypeOf(f), Object.getPrototypeOf(f) === Function.prototype);
function* h() {}
var it = h();
console.log(g() instanceof g, it instanceof h, it instanceof g, ({}) instanceof h);
h.prototype = {};
console.log(it instanceof h, h() instanceof h);
async function* ag() {}
console.log(ag() instanceof ag);
console.log(h.hasOwnProperty("caller"), h.hasOwnProperty("arguments"), f.hasOwnProperty("caller"));
for (const k of ["caller", "arguments"]) {
  try { h[k]; console.log("read"); } catch (e) { console.log(e.constructor.name); }
  try { h[k] = {}; console.log("write"); } catch (e) { console.log(e.constructor.name); }
}
f.caller = 1; console.log(f.caller);
(function () { "use strict"; try { f.arguments = 1; console.log("write"); } catch (e) { console.log(e.constructor.name); } })();
`)
}

func TestE2EJSFileArgumentsCountCallPaths(t *testing.T) {
	// arguments.length counts the parameters the call passed, through a
	// generator (directly, as a value, through any), a parameter default
	// reading it with an empty body, and a constructor function
	// (ADR-01816).
	assertJSFileSameAsNode(t, `
var a1, a2, a3, a4;
var g = function* (x = a1 = arguments) {};
g().next();
var h = function* (x = a2 = arguments) { function arguments() {} };
h().next();
var k = function* (x, y = a3 = arguments) { yield arguments.length; };
console.log(k(5).next().value);
function p(x = a4 = arguments) {}
p();
console.log(a1.length, a2.length, a3.length, a3[0], a4.length);
function* d(p, q) { yield arguments.length; }
console.log(d(1).next().value, d().next().value, d(1, 2, 3).next().value);
var dv = d; console.log(dv(1).next().value);
var anyF = [d][0]; console.log(anyF(1, 2).next().value);
function E(message) { this.message = message || ""; this.n = arguments.length; }
var e1 = new E("x"), e2 = new E(), e3 = new E("a", "b", "c");
console.log(e1.message, e1.n, e2.n, e3.n, typeof f0);
function f0() {} f0.caller;
`)
}

func TestE2EJSFileAwaitIdentifier(t *testing.T) {
	// Script code: await names a binding outside async functions (in a
	// generator's parameters and name, in a class static block's nested
	// function); async arrows with one bare parameter (ADR-01817).
	assertJSFileSameAsNode(t, `
var await = 0;
function f() { var await = 1; return await; }
console.log(await, f());
(function* await(await) { console.log("named"); })().next();
var fromParam, fromBody;
class C {
  static {
    (function* (x = fromParam = await) { fromBody = await; })().next();
  }
}
console.log(fromParam, fromBody);
var o = { await: 4 }; console.log(o.await);
const inc = async y => y + 1;
class K { async m() { const r = [1, 2].map(async x => await inc(x)); return Promise.all(r); } }
new K().m().then(v => console.log(v.join()));
async function g() { return await 7; }
g().then(console.log);
`)
}

func TestE2EAsyncArrowBareParam(t *testing.T) {
	// `async x => …` with one bare parameter, typed from its context
	// (ADR-01817).
	assertSameAsNode(t, `
const twice = async (y: number): Promise<number> => y * 2;
const all = [1, 2].map(async n => await twice(n));
Promise.all(all).then(v => console.log(v.join(",")));
`)
}

func TestModuleAwaitNameRejected(t *testing.T) {
	// Module code reserves await as a name; property names stay free
	// (ADR-01817).
	if _, err := parser.Parse("export const x = 1;\nfunction f() { var await = 2; return await; }"); err == nil || !strings.Contains(err.Error(), "'await' is a reserved word") {
		t.Errorf("want the reserved-word error, got %v", err)
	}
	if _, err := parser.Parse("export const o = { await: 1 };\nconsole.log(o.await);"); err != nil {
		t.Errorf("property name: %v", err)
	}
}

func TestE2EMethodArgumentsCountThroughAny(t *testing.T) {
	// A method reading `arguments`, called through `any`, counts the
	// arguments passed: Headers' webidl arity errors (ADR-01816).
	assertSameAsNode(t, `
const h: any = new Headers();
try { h.append("a"); } catch (e: any) { console.log(e.constructor.name, e.message); }
try { h.get(); } catch (e: any) { console.log(e.message); }
h.append("x", "1");
console.log(h.get("x"));
class C { m(a: number, b?: number): number { return arguments.length; } }
const c: any = new C();
console.log(c.m(1), c.m(1, 2), c.m());
`)
}

func TestE2EJSFileArrayIteratorReplaced(t *testing.T) {
	// Replacing or deleting Array.prototype[Symbol.iterator] reroutes an
	// array's destructuring, spread, for…of and Array.from through the
	// property as it is then (ADR-01819).
	assertJSFileSameAsNode(t, `
var a = [1, 2, 3];
console.log(a[Symbol.iterator] === Array.prototype.values, a[Symbol.iterator] === Array.prototype[Symbol.iterator]);
Array.prototype[Symbol.iterator] = function* () { yield this[0]; yield 42; };
var [x, y, z] = a;
console.log(x, y, z);
function f([p, q]) { return p + "," + q; }
console.log(f([5, 6]), [...a].join(), Math.max(...a), Array.from(a).join());
for (const v of [7, 8]) console.log("of", v);
class C { m([u, w]) { return u + w; } static s([u] = [9]) { return u; } }
console.log(new C().m([1, 2]), C.s());
var pairs = [["k", 1], ["l", 2], ["m", 3]];
function sizes() { const s = new Set(a); try { new Map(pairs); } catch (e) { return s.size + "/" + e.message; } }
console.log(new Set([1, 2, 3]).size, sizes(), new Uint8Array(a).join());
Promise.all(a).then((v) => console.log("all", v.join()));
delete Array.prototype[Symbol.iterator];
try { var [r] = a; console.log("no throw", r); } catch (e) { console.log(e.constructor.name); }
try { for (const v of a) console.log(v); } catch (e) { console.log(e.constructor.name); }
function t(g) { try { g(); } catch (e) { console.log(e.message); } }
var q = 3, o = { xs: [1], n: 5 };
t(() => { var [z] = [1, 2]; });
t(() => { var [z] = [q, "a", 1.5, true, null, undefined]; });
t(() => { var [z] = [[1], {}, -1, q + 1]; });
t(() => { var [z] = o.xs; });
t(() => { [...o.xs]; });
t(() => f(a));
`)
}

func TestE2EJSFileIterateNonIterable(t *testing.T) {
	// A js-lane value of no iterable type throws at run time, with V8's
	// messages (ADR-01819).
	assertJSFileSameAsNode(t, `
var o = { n: 5, u: undefined }; var n = 5, u;
function t(f) { try { f(); } catch (e) { console.log(e.message); } }
t(() => { var [z] = o.n; });
t(() => { var [z] = o.u; });
t(() => { var [z] = n; });
t(() => { var [z] = u; });
t(() => { var [z] = 5; });
t(() => { [...o.n]; });
t(() => { for (const x of o.n); });
t(() => { for (const x of n); });
function g([a]) {} t(() => g(5)); t(() => g());
`)
}

func TestE2EJSFileAwaitNamesFunctionInStaticBlock(t *testing.T) {
	// In a class static block, a function expression's own name and body
	// read `await` as an identifier again (ADR-01817); static initialization
	// runs where the declaration does (ADR-01820).
	assertJSFileSameAsNode(t, `
var await = 7;
console.log("before");
class C {
  static {
    var f = (function* await(x = await) { yield x; yield await; });
    console.log(f.name, [...f()].join());
  }
}
console.log("after", String(function* g(x) { yield g; }));
`)
}

func TestE2EJSFilePrivateForeignReceiver(t *testing.T) {
	// A method run on a receiver its class did not build throws at the
	// private access, with V8's messages (ADR-01822).
	assertJSFileSameAsNode(t, `
class C {
  #x = 1;
  get #a() { return 3; }
  plain() { return "plain:" + this.k; }
  side() { console.log("before"); return this.#x; }
  w() { this.#x = (console.log("rhs"), 5); }
  acc() { return this.#a; }
}
const o = { k: 7 };
for (const r of [o, Object.create(C.prototype)]) {
  for (const m of ["plain", "side", "w", "acc"]) {
    try { console.log(C.prototype[m].call(r)); } catch (e) { console.log(e.constructor.name, e.message); }
  }
}
`)
}

func TestE2EJSFileObjectPrototypeMembers(t *testing.T) {
	// Object.prototype's own members, the __proto__ accessor included,
	// and a chain that never reaches it (ADR-01823).
	assertJSFileSameAsNode(t, `
const P = Object.prototype;
console.log(Object.getOwnPropertyNames(P).sort().join());
const o = {};
o.__defineGetter__("g", function () { return 42; });
o.__defineSetter__("s", function (v) { this.t = v; });
o.s = 3;
console.log(o.g, o.t, typeof o.__lookupGetter__("g"), o.__lookupSetter__("g"), typeof o.__lookupSetter__("s"));
console.log(P.isPrototypeOf.call(Array.prototype, []), P.isPrototypeOf(1), Array.prototype.isPrototypeOf({}));
const d = Object.getOwnPropertyDescriptor(P, "__proto__");
console.log(d.enumerable, d.configurable, d.get.call([]) === Array.prototype);
const k = "__proto__";
console.log(({})[k] === P, Object.create(null).__proto__);
const n = Object.create(null); n.__proto__ = 5; console.log(n.__proto__, Object.keys(n).join());
const a = {}; a[k] = { z: 1 }; console.log(a.z, Object.keys(a).length);
try { o.__defineGetter__("x", 1); } catch (e) { console.log(e.constructor.name, e.message); }
`)
}

func TestE2EJSFileArrayNamedProperties(t *testing.T) {
	// An array's own named properties, descriptors and accessor elements,
	// and an element of another kind stored into a literal (ADR-01824).
	assertJSFileSameAsNode(t, `
const a = [1, 2, 3];
a.foo = "bar"; a.n = 1; a.n += 2;
console.log(a.foo, a.n, a.length, Object.keys(a).join(), JSON.stringify(a), a);
for (const k in a) console.log("in", k);
delete a.n; console.log(a.n, "n" in a, delete a[1], a, 1 in a);
const q = [1]; q.f = function (n) { return this.length + n; }; console.log(q.f(2), q.slice().f);
console.log(JSON.stringify(Object.getOwnPropertyDescriptor(q, "length")), Object.getOwnPropertyNames(q).join(), q.propertyIsEnumerable("length"));
Object.defineProperty(q, "h", { value: 2 }); console.log(q.h, Object.keys(q).join(), q);
try { Object.defineProperty(q, "h", { value: 3 }); } catch (e) { console.log(e.constructor.name, e.message); }
const sy = Symbol("tag"); Object.defineProperty(q, sy, { value: 1 });
try { Object.defineProperty(q, sy, { get() {} }); } catch (e) { console.log(e.message); }
const b = [1, 2, 3];
let store = 0;
Object.defineProperty(b, "1", { get() { return 20 + this.length; }, set(v) { store = v; }, enumerable: true, configurable: true });
b[1] = 42;
console.log(b[1], store, b, Object.keys(b).join(), typeof Object.getOwnPropertyDescriptor(b, "1").get);
console.log(JSON.stringify(b), b.map((x) => x * 2).join(), b.indexOf(23), [...b].join());
Object.defineProperty(b, "1", { value: 5 }); console.log(b[1], b);
const w = [1]; w[1] = "x"; w.push(true); console.log(w, w[1]);
`)
}
