package tests

import (
	"strings"
	"testing"
)

// A function declared in a callback reads (and writes) a binding the
// callback captures from the top level.
func TestE2ENestedDeclInCallbackCapturesOuter(t *testing.T) {
	assertSameAsNode(t, `
const c = 5;
[1].forEach((v) => { function f() { return c + v; } console.log(f()); });
let n = 1;
function run(g: () => void) { g(); }
run(function () { function bump() { n++; return n; } console.log(bump()); });
console.log(n);
`)
}

// A labelled function declaration is the declaration; as the body of a
// loop or an if it is an early SyntaxError.
func TestE2ELabelledFunctionDeclaration(t *testing.T) {
	assertSameAsNode(t, `
a: b: function g() { return 2; }
console.log(g());
function h() { x: function k() { return 3; } return k(); }
console.log(h());
`)
	for _, src := range []string{"while (false) l: function f() {}\n", "if (true) l: function f() {}\n", "do a: b: function f() {} while (false)\n"} {
		if _, err := parseAndCompile(src); err == nil || !strings.Contains(err.Error(), "can only be declared inside a block") {
			t.Errorf("%q: want a syntax error, got %v", src, err)
		}
	}
}

// A destructuring assignment's undeclared targets in sloppy code are the
// global object's properties, as a plain assignment's are; a method, an
// accessor or `void` is no target (an early SyntaxError).
func TestE2ESloppyDestructuringCreatesGlobals(t *testing.T) {
	assertJSFileSameAsNode(t, `
[q, [w = 3]] = [5, []];
({ a, b: k = 2, d = 4, ...rest } = { a: 1, z: 9 });
({ m: [n] } = { m: [7] });
console.log(q, w, a, k, d, rest.z, n, globalThis.q);
`)
	for _, src := range []string{"void 1 = 1;\n", "0, [{ get x() {} }] = [{}];\n", "({ a: { m() {} } } = {});\n"} {
		if _, err := parseAndCompile(src); err == nil {
			t.Errorf("%q: want a syntax error", src)
		}
	}
}

// A reduce callback with no result leaves undefined as each step's
// accumulator; a function nested in a block whose callback reads the
// enclosing loop's helper types without recursing forever.
func TestE2EReduceVoidCallbackAndBlockFunction(t *testing.T) {
	assertJSFileSameAsNode(t, `
const vs = [];
console.log([1, 2, 3].reduce((acc, n) => { vs.push(n); }, "init"), vs);
const ws = [];
console.log(new Int8Array([1, 2, 3]).reduceRight((acc, n) => { ws.push(acc); }, 5), ws);
function T(array) { let r = []; for (let i = 0; i < array.length; i++) r.push(array[i]); return r; }
for (let c of [1]) {
  function C(array) { const xs = []; array.forEach(n => { xs.push(n); }); return T(xs); }
  console.log(C([4, 5]));
}
function g([a, ...r]) { return r.length; }
console.log(g([1, 2, 3]), [].join(","));
`)
}

// A function reaching a top-level let or const before its declaration ran
// throws JavaScript's ReferenceError, for a read, a write, an update and a
// destructuring target; after it, the binding.
func TestE2EModuleLevelTemporalDeadZone(t *testing.T) {
	assertJSFileSameAsNode(t, `
function t(fn) { try { fn(); } catch (e) { console.log(e.name, e.message); } }
function r() { return x; }
t(r);
t(function () { x = 5; });
t(function () { x++; });
t(function () { 0, [x] = []; });
t(function () { ({ x } = {}); });
let x;
t(function () { x = 6; });
console.log(r());
`)
}

// Calling a value that is no function throws JavaScript's TypeError, named
// as V8 names the callee.
func TestE2ECallNonCallableValue(t *testing.T) {
	assertJSFileSameAsNode(t, `
try { true(); } catch (e) { console.log(e.name, e.message); }
var x = 1; try { x(); } catch (e) { console.log(e.name, e.message); }
try { (1, "s")(2); } catch (e) { console.log(e.name, e.message); }
var u; try { u(); } catch (e) { console.log(e.name, e.message); }
`)
}

// A bitwise operand that is a string or an object is its ToNumber; an
// object assignment pattern's rest or missing key reads off any value.
func TestE2EBitwiseObjectOperandsAndObjectRestAssign(t *testing.T) {
	assertJSFileSameAsNode(t, `
console.log("1" | 1, 1 | "1", new String("1") | 1, 1 ^ new Number(3), "x" & 7, {valueOf(){return 6}} & 3, [5] << 1, "8" >>> 1);
var a, b, rest;
({ a, b = 3 } = { a: 1 }); console.log(a, b);
({ a, ...rest } = { a: 5, c: 6 }); console.log(a, rest);
({ ...rest } = 51); console.log(rest instanceof Object);
try { ({ ...rest } = null); } catch (e) { console.log(e.name); }
({ x: a } = 42); console.log(a);
`)
}

// Assigning or updating a const throws JavaScript's TypeError, a target
// of a destructuring assignment included; a write to Math's read-only
// constant is ignored in sloppy code, and a new member of it is kept.
func TestE2EConstAssignAndNamespaceMemberWrite(t *testing.T) {
	assertJSFileSameAsNode(t, `
const c = 1;
function f() { c = 2; }
try { f(); } catch (e) { console.log(e.name, e.message); }
try { c++; } catch (e) { console.log(e.name, e.message); }
try { [c] = [3]; } catch (e) { console.log(e.name, e.message); }
try { ({ c } = { c: 4 }); } catch (e) { console.log(e.name, e.message); }
console.log(c);
Math.PI = 20; console.log(Math.PI);
Math.foo = 3; console.log(Math.foo);
`)
}

// An assignment or update through a base always null or undefined
// evaluates the base, the key and (for =) the right-hand side, then throws
// the TypeError V8 throws, without converting the key.
func TestE2ENullishBaseAssignment(t *testing.T) {
	assertJSFileSameAsNode(t, `
function D() {}
function t(f) { try { f(); console.log("none"); } catch (e) { console.log(e instanceof D ? "D" : e.constructor.name, e.message || ""); } }
t(() => { var base = null; base[(() => { throw new D(); })()] = (() => { throw new Error("rhs"); })(); });
t(() => { var base = null; var prop = { toString() { throw new Error("key"); } }; base[prop] = (() => { throw new D(); })(); });
t(() => { var base = null; var prop = { toString() { throw new Error("key"); } }; base[prop] = 1; });
t(() => { var base = undefined; base[(() => { throw new D(); })()] ^= (() => { throw new Error("rhs"); })(); });
t(() => { var base = null; var prop = { toString() { throw new Error("key"); } }; base[prop] ^= (() => { throw new Error("rhs"); })(); });
t(() => { var base = null; base.x = (() => { throw new D(); })(); });
t(() => { var base = null; base.x += 1; });
t(() => { var base = null; base["y"]++; });
`)
}

// A bigint compared with a string, a boolean, an object or a symbol, and
// a symbol under an arithmetic or string operator, behave as in JavaScript.
func TestE2EBigIntMixedCompareAndSymbolOperators(t *testing.T) {
	assertJSFileSameAsNode(t, `
function id(x) { return x; }
const a = id(1n), s = id("1"), t = id(true), o = id({ valueOf() { return 1n; } }), u = id(undefined), z = id(Symbol("q"));
console.log(a == s, a != s, a == t, a < s, a > s, a <= "x", a >= "2", a < t, a == o, a == u, a == z, a < "1.5");
try { console.log(a < z); } catch (e) { console.log(e.name); }
console.log(a == "1e0", a == " 1 ", a == "0x1", a < "0x2", a > "-1", a == "");
console.log(1n == "1", 2n > "1", 1n == true, 0n == false);
const sy = Symbol("q");
for (const f of [() => sy + 1, () => sy + "x", () => "x" + sy, () => sy < 1, () => 1n > sy, () => sy * 2, () => sy | 0, () => sy ** 2, () => `+"`${sy}`"+`, () => sy in {}]) {
  try { console.log(f()); } catch (e) { console.log(e.name, e.message); }
}
`)
}

// An update of an undeclared name in sloppy code reads it (the
// ReferenceError when the global object lacks it) and writes the global
// object's property, as an assignment does.
func TestE2ESloppyUndeclaredUpdate(t *testing.T) {
	assertJSFileSameAsNode(t, `
if (false) { arguments++; zz--; }
try { qq++; } catch (e) { console.log(e.name, e.message); }
function f() { ww--; }
try { f(); } catch (e) { console.log(e.message); }
ww = 5; f(); ++ww; console.log(ww, ww++, ww);
`)
}

// Strict code writes an existing global object property through an
// undeclared name; the write throws ReferenceError, after the right-hand
// side, when the property is absent.
func TestE2EStrictUndeclaredGlobalWrite(t *testing.T) {
	assertJSFileSameAsNode(t, `
"use strict";
globalThis.g = 1;
g = 2; g++; ++g; console.log(g);
try { nope = 1; } catch (e) { console.log(e.name, e.message); }
try { nope2++; } catch (e) { console.log(e.name, e.message); }
let n = 0;
try { missing = (n++, 5); } catch (e) { console.log(e.message, n); }
`)
}

// An async function or generator reads the receiver its caller supplies,
// and stays no constructor.
func TestE2EAsyncFunctionOwnThis(t *testing.T) {
	assertJSFileSameAsNode(t, `
async function foo(a) { return [this === a, arguments.length, await Promise.resolve(this.v)]; }
const o = { v: 4 };
foo.call(o, o, 2).then(console.log);
const fe = async function () { return typeof this; };
fe.call(7).then(console.log);
async function thisFn() { return await this?.a; }
thisFn.call({ a: Promise.resolve(33) }).then(console.log);
async function thrower() { this.x; }
thrower().then(() => console.log("no"), (e) => console.log("rejected", e instanceof TypeError));
console.log(foo.name, foo.length, typeof foo.call(o, o).then);
try { new foo(); } catch (e) { console.log(e.name); }

function* gg() { yield this; }
try { new gg(); } catch (e) { console.log(e.name, e.message); }
`)
}

// A this-reading function declared inside another one that reads its
// own `this` takes the receiver its caller supplies.
func TestE2ENestedThisFunctionDeclarations(t *testing.T) {
	assertJSFileSameAsNode(t, `
"use strict";
function f1() { function f() { return typeof this; } return [f(), typeof this, f.call(1)]; }
console.log(f1(), f1.call("s"));
`)
}

// `in` with a right operand that is no object (a primitive, null or
// undefined, statically or in an any) throws V8's TypeError naming the
// key and the value searched.
func TestE2EInOperatorPrimitiveThrows(t *testing.T) {
	assertJSFileSameAsNode(t, `
function id(x) { return x; }
for (const f of [() => "a" in 1, () => "a" in "str", () => "length" in null, () => 0 in true, () => "x" in Symbol()]) {
  try { console.log(f()); } catch (e) { console.log(e.name, e.message); }
}
const o = { a: 1 }; console.log("a" in o, 1 in [5, 6], "b" in o);
for (const v of [1, "str", null, undefined, true, Symbol("s"), { a: 1 }, [1]]) {
  try { console.log("a" in id(v), 0 in id(v)); } catch (e) { console.log(e.name, e.message); }
}
console.log((() => { try { return "a" in id(2n); } catch (e) { return e.message; } })());
`)
}

// A destructuring default that only throws (a `never` value) throws
// when the element is undefined, in TypeScript as in JavaScript.
func TestE2EDestructuringThrowingDefault(t *testing.T) {
	out := compileAndRun(t, `
function t(f: () => void) { try { f(); console.log("none"); } catch (e) { console.log((e as Error).constructor.name); } }
t(function () { const [x = (function () { throw new RangeError(); })()] = [undefined]; });
t(function () { const [y = 2] = [undefined]; console.log(y); });
`)
	if want := "RangeError\n2\nnone"; out != want {
		t.Fatalf("got %q, want %q", out, want)
	}
}

// In sloppy JavaScript a function's `arguments` is a binding like any
// other: assigned (also from a parameter's eval code or a closure), or
// redeclared with `var`, which keeps the arguments object.
func TestE2ESloppyArgumentsRebinding(t *testing.T) {
	assertJSFileSameAsNode(t, `
function fa() { arguments = "x"; return arguments; }
console.log(fa(1));
function g(a) { var arguments; return arguments.length; }
console.log(g(1, 2));
function h(a) { var arguments = 3; return arguments; }
console.log(h(1));
function k() { arguments = [9]; return arguments[0]; }
console.log(k(1));
function fb(p = eval("var arguments = 'param'")) { return [typeof arguments, arguments.length]; }
console.log(fb(), fb(1, 2));
function f2(p = eval("var arguments = 'param'"), q = arguments) { return [q, arguments]; }
console.log(f2());
function f3(p = eval("var arguments"), q = arguments) { return [typeof q, q.length]; }
console.log(f3(7));
console.log("arguments" in globalThis);
function f4(p = (() => { arguments = 1; })()) { return typeof arguments; }
console.log(f4(1, 2), f4());
function fc(a) { var b = 1, arguments = 3, c; return [b, arguments, c]; }
console.log(fc(9));
function g(a) { var z, arguments; return arguments.length; }
console.log(g(1, 2, 3));
`)
}

// A class whose heritage reads its own binding, or that of a class
// declared after it, throws the dead zone's ReferenceError.
func TestE2EClassHeritageTemporalDeadZone(t *testing.T) {
	assertJSFileSameAsNode(t, `
function t(f) { try { f(); console.log("none"); } catch (e) { console.log(e.name, e.message); } }
t(() => { class A extends B {} class B {} });
t(() => { class x extends x {} });
t(() => { var x = (class x extends x {}); });
t(() => { (class x extends x {}); });
t(() => { class P extends Q {} class Q extends P {} });
t(() => { class R {} class S extends R {} console.log(new S() instanceof R); });
`)
}

// `super` reaches the home object from arrows, accessors, statics and a
// direct eval in a method or derived constructor; a plain function's eval
// code may not use it (SyntaxError).
func TestE2ESuperInEvalAndArrows(t *testing.T) {
	assertJSFileSameAsNode(t, `
class A { m() { return "A.m"; } static s() { return "A.s"; } get g() { return "A.g"; } }
class B extends A {
  m() { const f = () => super.m(); return f(); }
  static s() { return [super.s(), (() => super.s())()]; }
  get g() { return super.g + "!"; }
  k() { return super["m"](); }
  e() { return eval("super.m()"); }
}
const b = new B();
console.log(b.m(), B.s(), b.g, b.k(), b.e());
const o = { __proto__: { h() { return "p.h"; } }, h() { return super.h() + "+"; } };
console.log(o.h());
class C extends A { constructor() { eval("super()"); this.v = eval("super.m()"); } }
console.log(new C().v);
function plain() { try { return eval("super.x"); } catch (e) { return e.name; } }
console.log(plain());
`)
}

// An object literal's super[k]() calls on this; an async or generator
// method's parameter default reads its this and super.
func TestE2EObjectLiteralSuperCallAndDefaultThis(t *testing.T) {
	assertJSFileSameAsNode(t, `
var parent = { getThis: function () { return this; }, get This() { return this; }, n: "p" };
var obj = {
  tag: "obj",
  a() { return super.getThis().tag; },
  b() { return super["getThis"]().tag; },
  c() { return super.This.tag; },
  d() { return super["This"].tag; },
  e() { const k = "getThis"; return super[k]().tag; },
};
Object.setPrototypeOf(obj, parent);
console.log(obj.a(), obj.b(), obj.c(), obj.d(), obj.e());
var sup = { method() { return "sup"; } };
var child = { async method(x = super.method()) { return await x; } };
Object.setPrototypeOf(child, sup);
child.method().then(v => console.log(v));
var c2 = { m(x = this.tag) { return x; }, tag: "t" };
console.log(c2.m());
var c3 = { async m(x = this.tag) { return x; }, tag: "t3" };
c3.m().then(v => console.log(v));
var c5 = { *g(x = this.tag) { yield x; }, tag: "t5" };
console.log(c5.g().next().value);
class K { tag = "k"; async m(x = this.tag) { return x; } }
new K().m().then(v => console.log(v));
`)
}

// A function declared in a callback, reading only a binding the callback
// captures, closes over it.
func TestE2ENestedDeclInDynCallbackCapturesOuter(t *testing.T) {
	assertJSFileSameAsNode(t, `
var c = 1;
let d = { k: "d" };
function foo(f) { f(1); }
foo(function (v) {
  function a() { console.log(c, d.k); }
  a();
  c = 2;
  a();
});
var C = class { async *m() { return 42; } b = 42; };
var inst = new C();
inst.m().next().then(function (v) {
  function assertions() { console.log(inst.b, v.value); }
  return Promise.resolve(assertions());
}).then(() => console.log("done"));
`)
}

// Destructuring assignment over an observable iterator steps it element
// by element, assigning (setters, defaults) between steps, and closes it.
func TestE2EDestructuringAssignIteratorProtocol(t *testing.T) {
	assertJSFileSameAsNode(t, `
var log = [];
function mk(n) { return { [Symbol.iterator]() { let i = 0; return { next() { log.push("next" + i); return { value: i++, done: i > n }; }, return() { log.push("ret"); return {}; } }; } }; }
var o = { set v(x) { log.push("set" + x); } };
var a, b, c, r;
[a, b] = mk(5); console.log(a, b, log.join()); log = [];
[a, , b = 9, ...r] = mk(3); console.log(a, b, r, log.join()); log = [];
[a, b = "d", c = "e"] = mk(1); console.log(a, b, c, log.join()); log = [];
[o.v, o["w"]] = mk(5); console.log(o.w, log.join()); log = [];
[[a, b], c] = [[1, 2], 3]; console.log(a, b, c);
function* g() { yield 1; yield 2; yield 3; }
[a, ...r] = g(); console.log(a, r);
var x = ([a] = mk(2)); console.log(typeof x[Symbol.iterator], log.join()); log = [];
try { [a] = { [Symbol.iterator]() { return { next() { return 5; } }; } }; } catch (e) { console.log(e.name + ": " + e.message); }
var thrower = { set v(x) { throw new Error("boom"); } };
try { [thrower.v] = mk(3); } catch (e) { console.log(e.message, log.join()); } log = [];
var log = [];
var it = { [Symbol.iterator]() { let i = 0; return { next() { log.push("next" + i); return { value: i++, done: i > 3 }; }, return() { log.push("ret"); return {}; } }; } };
var o = { set v(x) { log.push("set" + x); throw new Error("boom"); } };
try { [o.v, o.w] = it; } catch (e) { log.push(e.message); }
console.log(log.join());
log = [];
var p = { set v(x) { log.push("set" + x); } };
[p.v, p.w] = it;
console.log(log.join());
`)
}

// A destructuring assignment target no scope declares writes a global in
// sloppy code, and throws ReferenceError in strict code; a computed key in
// a callback's pattern reads the enclosing binding.
func TestE2EDestructuringAssignUndeclaredTargets(t *testing.T) {
	assertJSFileSameAsNode(t, `
function t(f) { try { f(); console.log("none"); } catch (e) { console.log(e.name); } }
t(function () { [ sloppyG ] = [5]; });
t(function () { ({ p: sloppyH, sloppyI, sloppyJ = 7, ...sloppyR } = { p: 1, sloppyI: 2, z: 3 }); });
console.log(globalThis.sloppyG, sloppyH, sloppyI, sloppyJ, sloppyR);
t(function () { [ , ...sloppyK ] = [1, 2, 3]; });
console.log(sloppyK);
var a, x;
t(function () { 0, { [a.b]: x } = {}; });
t(function () { ({ [a.b]: x } = {}); });
`)
	assertJSFileSameAsNode(t, `
"use strict";
function t(f) { try { f(); console.log("none"); } catch (e) { console.log(e.name); } }
t(function () { 0, [ unresolvable ] = []; });
t(function () { 0, [ ...unresolvable2 ] = []; });
t(function () { ({ p: unresolvable3 } = {}); });
globalThis.present = 0;
t(function () { [ present ] = [9]; });
console.log(present);
var log = [];
var it = { [Symbol.iterator]() { let i = 0; return { next() { log.push("next" + i); return { value: i++, done: i > 3 }; } }; } };
t(function () { [ missingA, missingB ] = it; });
console.log(log.join());
`)
}

// eval's arguments after the source are evaluated and ignored; a sloppy
// script may assign the global eval, its calls then calling what it holds
// until the intrinsic is assigned back.
func TestE2EEvalExtraArgsAndReassignment(t *testing.T) {
	assertJSFileSameAsNode(t, `
var x;
var log = [];
var r = eval("x = 1", "x = 2", log.push("extra"));
console.log(x, r, log.join());
function f() { var y = 5; return eval("y + 1", y = 100); }
console.log(f());
`)
	assertJSFileSameAsNode(t, `
var callCount = 0;
function f(n) {
  "use strict";
  if (n === 0) { callCount += 1; return "end"; }
  return eval(n - 1);
}
var x = 0;
console.log(eval("x = 7; x + 1"), typeof eval);
var orig = eval;
eval = f;
console.log(f(5), callCount, eval === f, typeof eval);
console.log(eval(0), eval(3));
eval = orig;
console.log(eval("x * 2"), eval === orig);
`)
}

// A function declaration's name is a variable a JavaScript program may
// assign (from inside it too); a global holding another kind of value than
// its initializer's answers typeof by its value.
func TestE2EReassignedFunctionDeclarations(t *testing.T) {
	assertJSFileSameAsNode(t, `
function target() { return target = { q: 1 }; }
console.log(typeof target, target().q, typeof target, target.q);
function g() { g = 5; return 1; }
g();
console.log(g);
console.log(early.name, early());
function early() { early = null; return "e"; }
console.log(early);
function outer() { inner(); return typeof inner; function inner() { inner = 1; } }
console.log(outer());
function* gen() { yield 1; }
var it = gen(); gen = "x"; console.log(it.next().value, gen);
`)
	assertJSFileSameAsNode(t, `
var t = function () { return 1; };
function h() { t = { q: 1 }; }
h();
console.log(typeof t, t.q);
var u = function () { u = { q: 2 }; return 0; };
u();
console.log(typeof u, u.q);
var w = function () { return 1; };
w = { q: 3 };
console.log(typeof w, w.q);
var gen2 = function* () { yield 1; };
var it = gen2(); gen2 = "x"; console.log(it.next().value, gen2);
`)
}

// Class members keyed at run time in class expressions, string and
// numeric accessor names, static accessors through a class held as a
// value, and ToPrimitive of a null-prototype object.
func TestE2EClassRuntimeKeysAndStaticAccessors(t *testing.T) {
	assertJSFileSameAsNode(t, `
var empty = Object.create(null);
var C, value, D;
for (C = class { get ['x' in empty]() { return 'via get'; } }; ; ) { value = C.prototype.false; break; }
console.log(value, C.name);
for (C = class { set ['x' in empty](param) { value = param; } }; ; ) { C.prototype.false = 'via set'; break; }
console.log(value);
var badKey = Object.create(null);
try { 0, class { get [badKey]() {} }; } catch (e) { console.log(e.name); }
try { 0, class { static set [test262unresolvable](_) {} }; } catch (e) { console.log(e.name); }
var k = "dyn";
D = (0, class { [k + 1]() { return 1; } static [k + 2]() { return 2; } });
console.log(new D().dyn1(), D.dyn2(), D.name === "" || D.name);
function mk(n) { return class { [n]() { return n; } }; }
console.log(new (mk("zz"))().zz());
var v2;
class L {
  get ''() { return 'g0'; } set ''(p) { v2 = p; }
  get 0b10() { return 'g2'; } static get 0x10() { return 's16'; }
  get 1E+9() { return 'e'; } get .1() { return 'd'; }
  get 'ab'() { return 'ab'; } static set 'q'(x) { v2 = "sq" + x; }
}
var c = new L();
console.log(c[''], c[2], L[16], c['1000000000'], c['0.1'], c.ab);
L.prototype[""] = "x"; console.log(v2); L.q = 1; console.log(v2);
D = class { get 'k'() { return 1; } };
console.log(new D().k, Object.getOwnPropertyNames(L.prototype));
class C4 { static m() {} static get g() { return 1; } static f = 1; }
console.log(Object.getOwnPropertyNames(C4));
var A = C4; console.log(Object.getOwnPropertyNames(A), Object.keys(A));
console.log(typeof Object.getOwnPropertyDescriptor(C4, 'g'), typeof Object.getOwnPropertyDescriptor(C4, 'm'));
class Q { static set s(x) { this.v = x; } static get s() { return 'S'; } }
var d = Object.getOwnPropertyDescriptor(Q, 's'); console.log(d.enumerable, d.configurable, d.get.call(Q)); d.set.call(Q, 3); console.log(Q.v);
var vv; var o = Object.create(null);
try { console.log(String(o)); } catch (e) { console.log(e.name, e.message); }
try { console.log(o + ""); } catch (e) { console.log(e.name, e.message); }
var x = {}; try { x[o] = 1; } catch (e) { console.log(e.name); }
var p = Object.create(o); try { String(p); } catch (e) { console.log(e.name); }
var q = Object.create(null); q.toString = () => "q"; console.log(String(q), String(Object.create(q)));

class C3 { static set q(p) { vv = p; } }
Object.defineProperty(C3, 'z', { set: function (x) { this.q = x; }, configurable: true });
C3.z = 4; console.log(vv);
var A = C3; A.z = 5; console.log(vv);
var U; try { console.log(new U().x); } catch (e) { console.log(e.name); }
var W;
W = class { a() { return 1; } };
console.log(new W().a());
var Y = class { a() { return 1; } };
console.log(new Y().a());
Y = class { b() { return 2; } };
console.log(new Y().b());
function* g() {
  class C { [yield 9]() { return 9; } static [yield 8]() { return 8; } get [yield 7]() { return 7; } }
  var c = new C();
  console.log(c[9](), C[8](), c[7]);
}
var it = g(); var r = it.next(); while (!r.done) { console.log("y", r.value); r = it.next(r.value); }
async function a() {
  var D = class { [await 5]() { return 5; } static async [await 6]() { return 6; } };
  console.log(new D()[5](), await D[6]());
}
a();
var rx = [];
var ry = Symbol();
class RC { [ry] = (rx.push("a"), "old"); z = (rx.push("z"), 1); [ry] = (rx.push("b"), "new"); a = 1; a = 2; static s = 1; static s = 2; }
var rc = new RC();
console.log(rc[ry], rc.a, RC.s, rx, Object.keys(rc), Object.getOwnPropertySymbols(rc).length);
class R4 { a = 1; z = 3; a = 2; }
var r4 = new R4(); console.log(Object.keys(r4), JSON.stringify(r4), r4);
for (var rk4 in r4) console.log(rk4);
var sy = Symbol("sd3");
class SD3 { [sy] = 1; }
var sd3 = new SD3();
console.log(sd3.propertyIsEnumerable(sy), Object.prototype.propertyIsEnumerable.call(sd3, sy));
console.log(delete sd3[sy], sd3[sy], Object.getOwnPropertySymbols(sd3).length, sy in sd3);
var so3 = { [sy]: 1 }; console.log(so3.propertyIsEnumerable(sy)); delete so3[sy]; console.log(Object.getOwnPropertySymbols(so3).length);
function NP(a) { this.a = a; this.nt = new.target; }
class NC extends NP { constructor() { super(1); } }
class NC2 extends NP {}
var nc = new NC(), nd = new NC2(3), np = new NP(2);
console.log(nc.nt === NC, nd.nt === NC2, np.nt === NP);
var no = {}; NP.call(no, 5); console.log(no.nt, no.a);
function NG() { return new.target === undefined ? "plain" : "new"; }
new NG(); console.log(NG());
var ft;
function FP() { ft = this; }
class FC extends FP { constructor() { super(1); } }
var fi = Reflect.construct(FC, [4]); console.log(ft === fi);
`)
}

// Static members named `constructor` are methods, and identifiers may be
// written with Unicode escapes (ID_Start / ID_Continue, ZWNJ, ZWJ); an
// object rest reads a source literal's getters; an arrow's `super()` run
// after the constructor's own constructs the base again, then throws; a
// JavaScript constructor may return another object (the return override);
// a computed key's yield or await is the enclosing function's.
func TestE2EStaticConstructorNamesAndIdentifierEscapes(t *testing.T) {
	assertJSFileSameAsNode(t, `
var A = class { static constructor() { return 1; } constructor() { this.x = 2; } };
console.log(A.constructor(), new A().x, A.hasOwnProperty("constructor"));
class B { static get constructor() { return "g"; } static *["gen"]() {} }
console.log(B.constructor);
class E { static async constructor() { return 3; } }
E.constructor().then(v => console.log(v));
class F { static *constructor() { yield 4; } }
console.log(F.constructor().next().value);
var C = class { $ = 1; _ = 2; \u{6F} = 3; \u2118 = 4; ZW_\u200C_NJ = 5; ZW_\u200D_J = 6;
  #\u{6F} = 7; #\u2118 = 8; get p() { return this.#o + this.#\u2118; } };
var c = new C();
console.log(c.o, c["\u2118"], c.ZW_\u200C_NJ, c["ZW_\u200D_J"], c.p, Object.keys(c).length);
var v\u0061r1 = 9; console.log(var1);
var o = { \u0069f: 1 }; console.log(o.if);
var ℘x = 2; console.log(\u2118x, \u{2118}x);
var count = 0;
const {...x} = { get v() { count++; return 2; } };
console.log(x.v, count, Object.getOwnPropertyDescriptor(x, 'v').writable);
let {a, ...r} = { a: 1, get b() { return 3; }, c: 4 };
console.log(a, r);
function f({...p}) { return p; }
console.log(f({ get z() { return 'z'; } }));
var scount = 0;
class SA { constructor() { scount++; this.k = scount; } }
class SB extends SA { constructor() { (_ => super())(); this.after = this.k; } }
var sb = new SB(); console.log(scount, sb.after, sb instanceof SA);
class SC extends SA { constructor() { super(); this.af = _ => super(); } }
var sc = new SC();
try { sc.af(); console.log("no"); } catch (e) { console.log(e.name, scount); }
class SD extends SA { constructor() { var f = () => super(); f(); console.log(this.k); } }
new SD();
class RA { constructor() { return { tag: "obj" }; } }
var ra = new RA(); console.log(ra.tag, ra instanceof RA);


class RP { constructor(v) { if (v) return [1, 2]; this.y = 3; } }
console.log(new RP(true).length, new RP(false).y);
class RQ { constructor() { return 5; } }
console.log(new RQ() instanceof RQ);
function RF() { return { f: 1 }; }
console.log(new RF().f);
class RD extends RA { constructor() { return undefined; } }
try { new RD(); } catch (e) { console.log(e.name); }
class RE2 extends Object { constructor() { super(); return 7; } }
try { new RE2(); } catch (e) { console.log(e.name); }
class RG { constructor(x) { this.own = 1; return x; } }
console.log(new RG({ z: 9 }).z, new RG(5).own, new RG(null).own, new RG(undefined).own);
class RH { constructor() { return new Map([[1, 2]]); } m() {} }
console.log(new RH().get(1), new RH() instanceof Map);
var yieldSet, C, iter;
function* g() {
  C = class { get [yield]() { return 'get yield'; } set [yield](param) { yieldSet = param; } };
  return 'end';
}
iter = g(); iter.next(); iter.next('first'); console.log(iter.next('second'));
console.log(C.prototype.first); C.prototype.second = 'set yield'; console.log(yieldSet, C.name);
async function h() { var D = (0, class { static [await 'w']() { return 'aw'; } }); return D.w(); }
h().then(v => console.log(v));
`)
}

// super in an arrow on a field (instance and static) uses the field's
// receiver; super[k] resolves this before evaluating k, also in a static
// method and as a call; a derived constructor ending or returning before
// super() throws the ReferenceError.
func TestE2ESuperInFieldArrowsAndComputedSuperKeys(t *testing.T) {
	assertJSFileSameAsNode(t, `
var FC = class { v = 1; f = () => this.v; static s = 2; static g = () => this.s; };
console.log(new FC().f(), FC.g());
class FD { w = 3; h = () => { this.w = 4; return this.w; }; static k = () => this; }
console.log(new FD().h(), FD.k() === FD);
var FE = class { a = () => { super.p = 5; }; static b = () => { super.q = 6; } };
var fe = new FE(); fe.a(); FE.b(); console.log(fe.p, FE.q);
class KB { constructor() { console.log("base"); } get g() { return 7; } m() { return 1; } static s() { return 2; } }
class KD extends KB { constructor() { var k = "m"; try { super[(console.log("key"), k)]; } catch (e) { console.log(e.constructor.name); } super(); console.log(typeof super[k], super["g"]); } static t() { var k = "s"; return super[k](); } }
new KD(); console.log(KD.t());
class KE extends Object { constructor() { try { super[super()]; } catch (e) { console.log(e.constructor.name); } } }
try { new KE(); } catch (e) { console.log("outer", e.constructor.name); }
class NB {}
class ND extends NB { constructor(f) { if (f) super(); else if (f === 0) return; } }
for (const v of [1, 0, false]) { try { new ND(v); console.log("ok", v); } catch (e) { console.log(e.constructor.name, e.message); } }
`)
}

// A class member keyed by a string spelled like a private name (`["#m"]`,
// `"#n"() {}`) is an ordinary property, and `o["#m"]` reads or writes it,
// never the class's private #m.
func TestE2EHashSpelledStringKeysAreNotPrivate(t *testing.T) {
	assertJSFileSameAsNode(t, `
class C {
  get #m() { return "T"; }
  ["#m"] = 0;
  "#n"() { return 5; }
  check() { return [this.hasOwnProperty("#m"), "#m" in this, this["#m"], this.#m, this["#n"]()]; }
}
console.log(new C().check(), Object.keys(new C()));
class D { #p = 1; q() { this["#p"] = 7; return [this.#p, this["#p"], Object.keys(this)]; } }
console.log(new D().q());
`)
}

// A private name is the innermost enclosing class's that declares it: a
// subclass's #f and its base's are two members, an inner class's does not
// reach the outer's. o.#f on another receiver checks o's brand; a write to
// a private method throws.
func TestE2EPrivateNamesAreLexicallyScoped(t *testing.T) {
	assertJSFileSameAsNode(t, `
class A { #f = "A"; set #g(v) { console.log("A set", v); } getA(o) { return o.#f; } hasA(o) { return #f in o; } setG(v) { this.#g = v; } }
class B extends A { #f = "B"; #g() { return "Bg"; } getB() { return [this.#f, this.getA(this), #f in this, this.hasA(this)]; } tryG() { try { this.#g = 1; } catch (e) { console.log(e.constructor.name); } return this.#g(); } }
var b = new B(); console.log(b.getB(), b.tryG()); b.setG(5);
try { new A().getA({}); } catch (e) { console.log(e.message); }
class D { #f() { return 1; } Inner = class { set #f(v) {} }; setAccess() { try { this.#f = 2; } catch (e) { console.log(e.constructor.name); } } }
new D().setAccess();
class C { #x = 1; Inner = class { #x = 2; read(o) { return o.#x; } }; }
var c = new C(); try { new c.Inner().read(c); } catch (e) { console.log(e.message); }
`)
}

// A static field is a configurable own property of the class: delete
// removes it, and it can be defined again by assignment.
func TestE2EDeleteStaticField(t *testing.T) {
	assertJSFileSameAsNode(t, `
var C = class { static f = 'test262'; static 'g'; static 0 = 'bar'; };
console.log(JSON.stringify(Object.getOwnPropertyDescriptor(C, 'f')), JSON.stringify(Object.getOwnPropertyDescriptor(C, 0)));
console.log(delete C.f, Object.prototype.hasOwnProperty.call(C, 'f'), C.f);
console.log(delete C[0], C[0], delete C.g, 'g' in C);
class D { static x = 1; }
console.log(delete D.x, D.x, Object.keys(D));
class E { static a = 1; static b = E.a + 1; static m() { return E.b; } }
console.log(E.m(), delete E.a, E.a, E.b, Object.keys(E));
E.a = 5; console.log(E.a, Object.keys(E));
`)
}

// C.caller and C.arguments on a class that declares no such static are
// Function.prototype's accessors, which throw for a class.
func TestE2EClassRestrictedProperties(t *testing.T) {
	assertJSFileSameAsNode(t, `
var B = class {};
class S extends B {}
function t(f) { try { f(); console.log("none"); } catch (e) { console.log(e.constructor.name); } }
t(() => B.caller); t(() => { B.caller = {}; }); t(() => B.arguments); t(() => S.arguments); t(() => { S.arguments = 1; });
console.log(B.hasOwnProperty("caller"), S.hasOwnProperty("arguments"));
class K { static caller = 3; } console.log(K.caller);
`)
}

// Reading a private accessor that has only a setter is V8's TypeError; a
// public one reads undefined.
func TestE2ESetterOnlyAccessorRead(t *testing.T) {
	assertJSFileSameAsNode(t, `
class C { set #f(v) {} set g(v) {} read() { try { return this.#f; } catch (e) { return e.constructor.name + ": " + e.message; } } upd() { try { this.#f++; } catch (e) { return e.message; } } }
var c = new C(); console.log(c.read(), c.g, c.upd());
`)
}

// A static async method reading its own class's private method compiles
// through every chain of its result, a class expression's too.
func TestE2EStaticAsyncPrivateChain(t *testing.T) {
	assertJSFileSameAsNode(t, `
function $DONE(e) { console.log(e === undefined ? "done" : "failed"); }
class C {
  static #x(v) { return v; }
  static async x(v) { return this.#x(v); }
  static async ZW_‌_NJ(v) { return await this.#x(v + 1); }
}
Promise.all([C.x(1), C.ZW_‌_NJ(2)]).then(r => { console.log(r[0], r[1]); }).then($DONE, $DONE);
var D = class {
  m() { return 42; }
  static #y(v) { return v * 2; }
  static async y(v) { return this.#y(v); }
};
var d = new D();
console.log(d.m());
D.y(4).then(v => console.log(v));
class E { static self() { const g = this; return g; } static n = 7; }
console.log(E.self().n);
`)
}

// A class extending a function a variable holds: the heritage is checked
// and its prototype read where the class is defined, in a block or a
// function too, and instanceof walks to it.
func TestE2EClassExtendsValue(t *testing.T) {
	assertJSFileSameAsNode(t, `
var Base = function (x) { this.x = x; };
Base.prototype.hi = function () { return "hi " + this.x; };
class C extends Base { constructor() { super(5); this.y = 1; } }
var c = new C();
console.log(c.x, c.y, c.hi(), c instanceof C, c instanceof Base, Object.getPrototypeOf(C.prototype) === Base.prototype);
var bound = function () {}.bind();
bound.prototype = {};
class D extends bound {}
console.log(Object.getPrototypeOf(new D()) === D.prototype);
var arrow = () => {};
try { (function () { class A extends arrow {} })(); console.log("no throw"); } catch (e) { console.log(e.constructor.name); }
var calls = 0;
var B2 = function () {}.bind();
Object.defineProperty(B2, "prototype", { get: function () { calls++; return 42; }, configurable: true });
try { { class E extends B2 {} } } catch (e) { console.log(e.constructor.name, calls); }
function* g() {}
var gv = g;
try { (function () { class F extends gv {} })(); } catch (e) { console.log(e.constructor.name); }
`)
}

// A closure's capture of a name read only in a for-of or catch pattern's
// default, a `new obj.m()` qualifier, or an uninitialized `let` a hoisted
// function reads before its declaration runs (TDZ).
func TestE2ECapturePatternDefaultsAndTDZ(t *testing.T) {
	assertJSFileSameAsNode(t, `
var d = 7;
function f() { for (let [a = d] of [[]]) console.log(a); }
f();
function g() { try { throw {}; } catch ({ b = d }) { console.log(b); } }
g();
function h() { try { throw [] } catch ([c = d]) { return () => c; } }
console.log(h()());
var obj = { method() {} };
(function () { try { new obj.method(); } catch (e) { console.log(e.constructor.name); } })();
function readEarly() { return z; }
function writeEarly() { z = 1; }
try { readEarly(); } catch (e) { console.log(e.constructor.name); }
try { writeEarly(); } catch (e) { console.log(e.constructor.name); }
let z;
console.log(readEarly());
`)
}

// Assigning a builtin global, a function declaration (exported too), or a
// class binding: inside the class body its name is constant.
func TestE2EAssignGlobalsAndClassNames(t *testing.T) {
	assertJSFileSameAsNode(t, `
parseInt = 5;
console.log(parseInt);
isNaN++;
console.log(isNaN);
function g() { return 23; }
console.log(g());
g = null;
console.log(g);
class C { m() { C = 42; } n() { return C; } }
var keep = C;
try { new C().m(); } catch (e) { console.log(e.constructor.name, e.message); }
C = null;
console.log(C, new keep().n() === keep);
var k = class D { m() { D = 1; } };
try { new k().m(); } catch (e) { console.log(e.constructor.name); }
function p(q, r = eval("var q = 1")) { return q; }
try { p(5); } catch (e) { console.log(e.constructor.name, e.message); }
`)
}

// `super` in static blocks and static field initializers, and computed,
// compound and update `super` writes: `this` checked first, the read
// through the base, the store on `this`.
func TestE2ESuperStaticInitsAndWrites(t *testing.T) {
	assertJSFileSameAsNode(t, `
function Parent() {}
Parent.t = "t262";
var value;
class C extends Parent { static { value = super.t; } }
console.log(value);
class P { static s = 4; static m() { return 5; } }
class C2 extends P { static x = super.s + 1; static { console.log(super.s, super.m()); } }
console.log(C2.x);
class Base { constructor() { throw new Error("base"); } }
class D extends Base { constructor() { super[super()] = 0; } }
try { new D(); } catch (e) { console.log(e.constructor.name); }
class D2 extends Base { constructor() { super[super()]++; } }
try { new D2(); } catch (e) { console.log(e.constructor.name); }
class B2 { constructor() { this.n = 1; } }
B2.prototype.n = 10;
class G extends B2 { constructor() { super(); var k = "n"; console.log(super[k]++, this.n, ++super[k], super[k] += 5, this.n, super.n += 1, super.n--, this.n); } }
new G();
`)
}

// A builtin constructor as heritage (`extends Symbol`: valid, never
// built), and a class constructor's [[Prototype]] being its function or
// value base.
func TestE2EExtendsBuiltinValueAndCtorProto(t *testing.T) {
	assertJSFileSameAsNode(t, `
class S1 extends Symbol {}
try { new S1(); } catch (e) { console.log(e.constructor.name, e.message); }
class S2 extends Symbol { constructor() { super(); } }
try { new S2(); } catch (e) { console.log(e.constructor.name); }
console.log(Object.getPrototypeOf(S1) === Symbol, typeof S1.prototype);
class B1 extends BigInt {}
try { new B1(1); } catch (e) { console.log(e.constructor.name, e.message); }
var B = function () {};
class C extends B {}
function F() {}
class G extends F {}
console.log(Object.getPrototypeOf(C) === B, Object.getPrototypeOf(G) === F);
async function af() {}
try { (function () { class A extends af {} })(); console.log("no throw"); } catch (e) { console.log(e.constructor.name); }
function* gf() {}
try { (function () { class A2 extends gf {} })(); console.log("no throw"); } catch (e) { console.log(e.constructor.name); }
`)
}

// A `const` for-in/for-of variable is constant: a write throws.
func TestE2EConstLoopVarWrites(t *testing.T) {
	assertJSFileSameAsNode(t, `
try { for (const x of [1]) { x++; } } catch (e) { console.log(e.message); }
try { for (const x of [1]) { x = 2; } } catch (e) { console.log(e.message); }
try { for (const k in { a: 1 }) { k = "b"; } } catch (e) { console.log(e.message); }
try { for (const k in { a: 1 }) { k += "b"; } } catch (e) { console.log(e.message); }
for (const x of [1, 2].map(x => { x++; return x; })) { console.log(x); }
for (let y of [1]) { y++; console.log(y); }
`)
}

// A method call on a caught value: its own member, `this` the value.
func TestE2ECaughtMethodCall(t *testing.T) {
	assertJSFileSameAsNode(t, `
var o = { eval: function () { return "E"; }, parseInt: function () { return "P"; }, n: 4, get() { return this.n; } };
try { throw o; } catch (e) { console.log(e.parseInt(), e.eval(), e.get()); }
try { throw new Error("m"); } catch (e) { console.log(e.toString(), e.hasOwnProperty("message")); }
`)
	assertSameAsNode(t, `
try { throw new RangeError("r"); } catch (e: any) { console.log(e.toString(), String(e)); }
`)
}

// A later class accessor of the same kind and name replaces an earlier one.
func TestE2EReplacedClassAccessors(t *testing.T) {
	assertJSFileSameAsNode(t, `
class C2 { get b() { throw new Error("first"); } get ['b']() { return 'B'; } }
console.log(new C2().b);
class C4 { get ['d']() { throw new Error("first"); } get d() { return 'D'; } set d(v) { this.v = v; } }
var c4 = new C4(); c4.d = 3;
console.log(c4.d, c4.v);
class S { static get s() { return 1; } static get s() { return 2; } }
console.log(S.s);
`)
}

// Eval code is a Script: an import or export declaration is a SyntaxError.
func TestE2EEvalModuleDeclarations(t *testing.T) {
	assertJSFileSameAsNode(t, `
try { eval("export default null;"); } catch (e) { console.log(e.constructor.name, e.message); }
try { (0, eval)('import v from "./x.js";'); } catch (e) { console.log(e.constructor.name, e.message); }
`)
}

// `new f(f = 1)` in a function widens f as its other assignments do, and
// `new Promise()` with no executor throws the constructor's TypeError.
func TestE2ENewArgsAssignAndPromiseNoExecutor(t *testing.T) {
	assertJSFileSameAsNode(t, `
function fn() { var x = function () { this.foo = 42; }; var result = new x(x = 1); console.log(x, result.foo); }
fn();
try { new Promise(); } catch (e) { console.log(e.constructor.name, e.message); }
let called;
async function foo() { called = true; await new Promise(); }
foo().catch(e => console.log(e.constructor.name));
console.log(called);
async function fa() {
  var p = Promise.resolve(0);
  for await (var x of [p]) { console.log("loop", x); }
  console.log("post", x);
}
fa();
`)
}

// A getter returning only null holds what its setter is given (`??=`).
func TestE2ENullGetterLogicalAssign(t *testing.T) {
	assertJSFileSameAsNode(t, `
class C {
  #s;
  get #field() { return null; }
  set #field(value) { this.#s = value; }
  ca() { return this.#field ??= 1; }
  sv() { return this.#s; }
}
const o = new C();
console.log(o.ca(), o.sv());
class D { get u() { return undefined; } get n() { return null; } }
var d = new D(); console.log(d.u, d.n, d.n ?? 3);
`)
}

// A class extending Proxy throws at its definition: no prototype.
func TestE2EExtendsProxyThrows(t *testing.T) {
	assertJSFileSameAsNode(t, `
try { (function () { class P extends Proxy {} })(); console.log("no"); } catch (e) { console.log(e.constructor.name, e.message); }
`)
}

// An override declaring defaulted parameters past the inherited method's
// takes them on every call path: a call on its class, a call the virtual
// slot dispatches to it, its own body through a dynamic value, and an
// async override reading super in a default.
func TestE2EOverrideAddsDefaultedParams(t *testing.T) {
	assertSameAsNode(t, `
class C { m() { return 1; } }
class D extends C { m(x = 2) { return x; } }
class E extends D { m(x = 3, y = 4) { return x * 10 + y; } }
class F extends D { m() { return 7; } }
const os: C[] = [new C(), new D(), new E(), new F()];
for (const o of os) console.log(o.m());
const d: D = new E();
console.log(d.m(5), d.m());
const f: D = new F();
console.log(f.m(5));
console.log((d as any).m(5), (d as any).m(), (d as any).m.length);
console.log(new E().m(5, 6), new D().m(9));
`)
	assertJSFileSameAsNode(t, `
class A { async method() { return 'sup'; } }
class B extends A { async method(x = super.method()) { console.log(await x); } }
new B().method();
class C { m() { return 1; } }
class D extends C { m(x = 2) { return x; } }
function side() { console.log("side"); return 9; }
class F extends D { m() { return 7; } }
for (const o of [new C(), new D(), new F()]) console.log(o.m(side()), o.m());
`)
}

// eval's arguments past its source are evaluated, a spread iterated.
func TestE2EEvalSpreadExtraArgs(t *testing.T) {
	assertJSFileSameAsNode(t, `
var nextCount = 0;
var iter = {};
iter[Symbol.iterator] = function () { return { next: function () { nextCount++; return { done: true, value: undefined }; } }; };
var x = "global";
(function () { var x = "local"; eval("x = 0;", ...iter); console.log(x); })();
console.log(x, nextCount);
`)
}

// A method called on null or undefined throws the read's TypeError, inside
// any expression (`"a" + undefined.toString()`).
func TestE2ENullishMethodCallInExpr(t *testing.T) {
	assertJSFileSameAsNode(t, `
class T { constructor(m) { this.m = m; } }
try { throw new T("a" + (undefined.toString())); } catch (e) { console.log(e instanceof TypeError, e.message); }
try { var q = "a" + null.toString(); console.log(q); } catch (e) { console.log(e.message); }
`)
}

// An object's own `with`/`at` methods, called in an argument, are its own;
// an array's `at()`/`with(i)` take undefined for an omitted argument.
func TestE2EOwnArrayNamedMethods(t *testing.T) {
	assertJSFileSameAsNode(t, `
var obj = { with() { return 42; } };
function f(a) { return a; }
console.log(f(obj["with"]()), f(obj.with()));
var o2 = { at() { return 7; } }; console.log(f(o2.at()));
var y = f([5, 6]);
console.log(y.at(), y.with(1, 9), y.with(0));
var z = [5, 6];
console.log(z.at(), z.with(0));
`)
}

// A base constructor's returned object is the derived class's `this`; the
// class's private names live on it.
func TestE2EReturnOverrideThisAndPrivates(t *testing.T) {
	assertJSFileSameAsNode(t, `
class Base { constructor(a, b) { var o = new Object(); o.prp = a + b; return o; } }
class Sub extends Base {
  constructor(a, b) {
    var exn;
    try { this.prp1 = 3; } catch (e) { exn = e; }
    console.log(exn instanceof ReferenceError);
    super(a, b);
    console.log(this.prp, this.prp1, this.hasOwnProperty("prp1"));
    return this;
  }
}
var s = new Sub(2, -1);
console.log(s.prp, s.prp1, s instanceof Sub);
class Sub2 extends Base {
  constructor(x) {
    super(1, 2);
    if (x < 0) return;
    var called = false;
    function tmp() { called = true; return 3; }
    var exn = null;
    try { super(tmp(), 4); } catch (e) { exn = e; }
    console.log(exn instanceof ReferenceError, called);
  }
}
console.log(new Sub2(1).prp, new Sub2(-1).prp);
class T { constructor(o) { return o; } }
class F extends T { #v; constructor(o) { super(o); this.#v = 42; } static val(o) { return o.#v; } static has(o) { return #v in o; } }
var o = {};
new F(o);
console.log(F.val(o), F.has(o), F.has({}));
try { new F(o); } catch (e) { console.log(e.constructor.name, e.message); }
try { F.val({}); } catch (e) { console.log(e.constructor.name, e.message); }
class M extends T { #m() { return 41; } get #a() { return 43; } static val(o) { return o.#m() + o.#a; } }
var p = {};
new M(p);
console.log(M.val(p));
try { new M(p); } catch (e) { console.log(e.constructor.name, e.message); }
let arr = [];
class PB { constructor() { return new Proxy(this, { get(obj, prop) { arr.push(String(prop)); return obj[prop]; } }); } }
class PT extends PB { #f = 3; method() { return this.#f; } }
let t = new PT();
console.log(t.method(), arr.join(), t instanceof PT, t instanceof Object);
`)
}

// A class extending a constructor held in a value constructs it; a bound
// function constructs its target.
func TestE2EValueBaseConstructsAndBoundNew(t *testing.T) {
	assertJSFileSameAsNode(t, `
function t(Constructor) {
  class Typed extends Constructor {}
  var arr = new Typed(2);
  console.log(arr.length, arr instanceof Constructor);
}
[Uint8Array, Float64Array].forEach(t);
function Plain(n) { this.n = n; }
t(Plain);
class B { constructor(x, y) { this.x = x; this.y = y; } }
class S extends B {}
var f = S.bind({}, 3, 4);
var s = new f();
console.log(s.x, s.y, Object.getPrototypeOf(s) === S.prototype);
function g(a) { this.a = a; }
var h = g.bind(null, 5);
console.log(new h().a);
`)
}

// A var a hoisted function writes inside a try reads the write after the
// catch.
func TestE2ECapturedVarWriteAcrossCatch(t *testing.T) {
	assertJSFileSameAsNode(t, `
function f() {
  var called = false;
  function tmp() { called = true; return 3; }
  try { tmp(); throw 1; } catch (e) {}
  console.log(called);
}
f();
class A { m() { var c = 5; function r() { return c; } console.log(r()); } }
new A().m();
`)
}

// A static method the program replaces (`C.m = f`) is what later reads
// and calls of C.m reach, as a writable property is in JavaScript.
func TestE2EStaticMethodReassignment(t *testing.T) {
	assertJSFileSameAsNode(t, `
class C { static m(x) { return "orig " + x; } }
const o = C.m;
C.m = function (x) { return "new " + x + " " + o.call(this, x); };
console.log(C.m(1));
const g = C.m;
console.log(g.call(C, 2), C.m === g);
`)
}

// A property write on a primitive goes through its wrapper's prototype
// chain: a Proxy's set trap there runs.
func TestE2EPrimitivePropertyWriteThroughProxyProto(t *testing.T) {
	assertJSFileSameAsNode(t, `
var n = 0;
var spy = new Proxy({}, { set: function () { n += 1; return true; } });
Object.setPrototypeOf(Number.prototype, spy);
(0).a = null;
Object.setPrototypeOf(String.prototype, spy);
"".a = null;
Object.setPrototypeOf(Boolean.prototype, spy);
true.a = null;
Object.setPrototypeOf(Symbol.prototype, spy);
Symbol().a = null;
console.log(n, (1).a);
`)
}

// Untyped JavaScript: awaiting a plain function's construction, a call of
// a hoisted var function from a generator, a primitive narrowed by
// Array.isArray, and a mixed array element passed where an array is
// declared all compile.
func TestE2EJSUntypedCallsAndNarrowing(t *testing.T) {
	assertJSFileSameAsNode(t, `
function Ctor() { return 7; }
async function f() { const v = await new Ctor(); console.log(typeof v); }
f();
var abrupt = function () { throw 1; };
async function* gen() { yield* abrupt(); }
var iter = gen();
var q = iter.next().then(() => {}, v => { iter.next().then(r => console.log("done", r.done)); });
q.catch(e => console.log("caught", e));
for (let v of new Uint8Array([4])) { if (Array.isArray(v)) console.log(v[0]); else console.log(v); }
function first(a) { return a[0]; }
var mixed = [];
mixed.push([5, 6]);
mixed.push(3);
console.log(first(mixed[0]));
`)
}

// An accessor literal built as a dynamic object (an any[] element) whose
// getter reads a local of the enclosing function: no instance of its
// synthetic class exists, so nothing emits that class's prototype, whose
// builder once emitted the getter outside the literal's scope (invalid IR).
func TestE2EAccessorLiteralInAnyArrayReadsLocal(t *testing.T) {
	t.Parallel()
	assertSameAsNode(t, `
function f(): void {
  let called = false;
  const a: any[] = [{ get x() { called = true; return 5; } }, 1];
  console.log(a[0].x, called, Object.getPrototypeOf(a[0]) === Object.prototype);
  const o: any = a[0];
  console.log(Object.keys(o), typeof Object.getOwnPropertyDescriptor(o, "x")!.get);
}
f();
`)
}
