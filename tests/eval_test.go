package tests

import (
	"testing"
)

// --- Static-string eval (TDD-00046 static subset / ADR-00198) ---
//
// A compile-time-constant `eval(src)` is compiled through this compiler's
// own parser and code generator, in place: one expression is spliced in,
// statements run with JavaScript's completion value and declaration
// scoping, and source that does not parse throws SyntaxError when the
// eval runs. A dynamic argument is a clean compile error.

func TestE2EEvalStaticExpression(t *testing.T) {
	assertOutput(t, `
console.log(eval("2 ** 10"))
console.log(eval("1 + 2 * 3"))
console.log(eval("'a' + 'b'"))
console.log(eval("1 === 1"))
`, "1024\n7\nab\ntrue")
}

func TestE2EEvalStaticExpressionAsValue(t *testing.T) {
	assertOutput(t, `
const n: number = eval("6 * 7")
console.log(n + 1)
`, "43")
}

// Statements: the completion value, sloppy `var` and function
// declarations bound in the caller's function, strict ones the eval's own.
func TestE2EEvalStatements(t *testing.T) {
	assertSameAsNodeCLIJS(t, `console.log(eval("var a = 1; a + 1"));
console.log(typeof a, a);
function f() { eval("var b = 2; function h() { return b * 10; }"); return h(); }
console.log(f(), typeof b);
console.log(eval("if (true) { 'yes' } else { 'no' }"));
console.log(eval("for (var i = 0; i < 3; i++) i * 2;"));
function g() { "use strict"; eval("var c = 3"); return typeof c; }
console.log(g());
try { eval("return 1"); } catch (e) { console.log(e instanceof SyntaxError); }
function s() { "use strict"; try { eval("eval = 42"); } catch (e) { return e instanceof SyntaxError; } }
console.log(s());
var o = { m() { return eval("this.k = 4; this.k + 1"); }, k: 0 };
console.log(o.m(), o.k);
function args() { return eval("arguments.length; arguments[1]"); }
console.log(args(5, 6));
console.log(eval("let q = 9; q"), typeof q);
`)
}

// A static string that does not parse throws SyntaxError when the eval
// runs, as in JavaScript.
func TestE2EEvalInvalidSyntaxThrows(t *testing.T) {
	assertSameAsNode(t, `try { eval("this is not valid @#"); console.log("no"); } catch (e) { console.log(e instanceof SyntaxError); }
console.log(eval("1 + 2"));
`)
}

// A `const` bound to a string literal is constant source too.
func TestE2EEvalOfConstSource(t *testing.T) {
	assertSameAsNode(t, `const s = "1+2"; console.log(eval(s));
function f() { const t = "[4, 5]"; return eval(t); }
console.log(f());
`)
}

// A void call's result is undefined in every operator: a string, typeof,
// strict and loose equality; a comparison whose kinds never match still
// runs its operand.
func TestE2EVoidResultAsValue(t *testing.T) {
	assertSameAsNodeCLIJS(t, `let n = 0;
function f() { n++; }
function g() { n += 10; return 1; }
console.log(f() === undefined, undefined === f(), f() !== undefined, f() === null, f() == undefined, g() === null, n);
console.log(f() + 1, f() * 2, f() == null, f() === undefined, typeof f(), [f()].length, !f());
var o = { v: f() }; console.log(o.v, JSON.stringify([f()]));
`)
}

// for...in over a function, a class or any other value enumerates its
// own enumerable keys at run time.
func TestE2EForInOverFunction(t *testing.T) {
	assertSameAsNodeCLIJS(t, `function foo() {}
foo.extra = 1;
for (var k in foo) console.log(k);
const arrow = () => 1;
for (const k in arrow) console.log("a", k);
class C { static s = 2; }
for (const k in C) console.log("c", k);
console.log("done");
`)
}

// Indirect eval runs at global scope: no caller locals, its sloppy
// declarations the program's, its result the completion value.
func TestE2EIndirectEval(t *testing.T) {
	assertSameAsNodeCLIJS(t, `console.log((0, eval)("switch(1){}"), (0, eval)("1 + 2"));
var loc = "outer";
function f() { var loc = "inner"; return (0, eval)("typeof loc"); }
console.log(f());
(0, eval)("var gv = 7; function gf() { return gv * 2; }");
console.log(gv, gf());
try { (0, eval)("return 1"); } catch (e) { console.log(e instanceof SyntaxError); }
console.log((0, eval)("'use strict'; var sv = 1; typeof sv"), typeof sv);
`)
}

// A computed class member keyed by a let or var holding a literal that
// nothing writes; an instance read and written by a number key.
func TestE2EComputedMemberUnwrittenLet(t *testing.T) {
	assertSameAsNodeCLIJS(t, `let x = 1;
var y = "k";
let C = class { get [x]() { return "2"; } static get [x]() { return "s"; } [y]() { return "m"; } };
let c = new C();
console.log(c[x], C[x], c.k(), c[1]);
class D2 { get [1]() { return "2"; } }
let d2 = new D2();
let i2 = 1;
console.log(d2[1], d2[i2]);
let e = new D2(); e[7] = "w"; console.log(e[7], e["7"]);
`)
}

// Array destructuring assignment: a rest target of any kind (a property,
// a nested pattern, a variable of another type) and holes.
func TestE2EDestructAssignRestTargets(t *testing.T) {
	assertSameAsNodeCLIJS(t, `var x = {};
var result;
var vals = [4, 3, 2];
result = [...x.y] = vals;
console.log(x.y.length, x.y[0], x.y[2], result === vals);
var a, b, c;
[a, ...[b, c]] = [1, 2, 3];
console.log(a, b, c);
var o = {};
[, ...o["k"]] = "xyz";
console.log(o.k);
var s = "str";
[...s] = [9, 8];
console.log(s);
var b2;
[, b2] = [1, 2];
console.log(b2);
var o2 = {};
[...o2["k"]] = "xyz";
console.log(o2.k);
`)
}

// A tuple source with a rest target and a hole.
func TestE2EDestructAssignTupleRest(t *testing.T) {
	assertSameAsNode(t, `
let a: number = 0;
let r: number[] = [];
[a, ...r] = [1, 2, 3];
let t2: [number, string, boolean] = [1, "s", true];
let r2: (string | boolean)[] = [];
[a, ...r2] = t2;
let b: boolean = false;
[, , b] = t2;
console.log(a, r, r2, b);
`)
}

// A generator expression capturing an array variable shares the binding:
// a push, or a reassignment on either side, is seen by the other.
func TestE2EGeneratorExpressionCapturesArray(t *testing.T) {
	assertSameAsNode(t, `function f(): string {
  let arr: number[] = [1];
  const g = function* (): Generator<number> { yield arr.length; arr = [7, 8]; yield arr.length; };
  const it = g();
  arr = [1, 2, 3];
  const a = it.next().value;
  const b = it.next().value;
  return a + " " + b + " " + arr.join(",");
}
console.log(f());
`)
	assertSameAsNodeCLIJS(t, `var value = [[22]];
var x = {};
var iter = (function*() {
  var vals = value;
  var result = [[x[yield]]] = vals;
  console.log(result === vals);
}());
console.log(iter.next().done);
console.log(iter.next('prop').done, x.prop);
function outer() {
  const arr = [1, 2, 3];
  const g = function* () { for (const v of arr) yield v * 2; arr.push(4); };
  const out = [...g()];
  return out.join(",") + " " + arr.length;
}
console.log(outer());
`)
}

// A var read before its declaration (a statement, a closure written
// before it, or a nested function declaration) is undefined until the
// declaration runs, in both lanes.
func TestE2EVarReadBeforeDeclaration(t *testing.T) {
	assertSameAsNodeCLIJS(t, `function k() { let r = zq; var zq = 1; return [r, zq, typeof r].join(); }
function k2(c) { r = typeof w; if (c) { var w = "s"; } var r; return r + "," + w; }
function k3() { x += 1; var x = 10; return x; }
console.log(k(), k2(true), k2(false), k3());
function k() { function g() { return v; } const a = g(); var v = 1; return [a, g()].join(); }
function k2() { const f = () => typeof w; const a = f(); var w = "x"; return a + "," + f(); }
let top1 = typeof later; var later = 3; console.log(top1, later);
console.log(k(), k2());
`)
	assertSameAsNode(t, `function k(): string {
  function g(): number { return v; }
  const a = g();
  var v = 1;
  return [String(a), typeof a, a === undefined, g() + 1].join(",");
}
function k2(): string {
  const f = (): string => w;
  const b = f();
  var w = "x";
  return b + "|" + f() + "|" + typeof b;
}
console.log(k(), k2());
`)
}

// A direct eval in a parameter initializer declares into a scope between
// the parameters and the body: later parameters and the body see its vars,
// and a body declaration of the same name shadows it. A parameter default
// writing a name only the body declares writes the global object.
func TestE2EEvalInParameterScope(t *testing.T) {
	assertSameAsNodeCLIJS(t, `async function f(p = eval("var arguments")) { let arguments; return typeof arguments; }
f().then(v => console.log("ok", v), e => console.log("rej", e.name, e.message));
function g(p = eval("var y = 2")) { let y = 5; return y; }
console.log(g());
function k(p = eval("var z = 2"), q = () => z) { var z = 7; return [z, q()].join(); }
console.log(k());
function h(p = eval("var x = 1")) { return x; }
console.log(h());
function m(p = eval("var w = 3"), q = () => w) { return q(); }
console.log(m());
function m2(p = eval("var w2 = 3"), q = w2) { return q; }
console.log(m2());
function k(p = (zq = 2)) { var zq; return [zq, globalThis.zq].join(); }
console.log(k());
function s2(p = (yq += 1)) { var yq; return 1; }
try { s2(); } catch (e) { console.log(e.name, e.message); }
`)
}

// eval returns an argument that is not a string unchanged (and undefined
// with none); it is a function value (length 1, name "eval"), and an
// optional call of it is an indirect eval. A string reaching it at run
// time, which needs an embedded engine, throws EvalError.
func TestE2EEvalNonStringAndAsValue(t *testing.T) {
	assertSameAsNodeCLIJS(t, `console.log(eval(42), eval(), eval(true, 1), eval({a: 1}).a, eval(null), typeof eval([1]));
var o = { k: 3 }; var d = eval(o); console.log(d === o);
function f(v) { try { return eval(v); } catch (e) { return e.name; } }
console.log(f(5), f(null));
console.log(eval.length, eval.name, typeof eval, eval === globalThis.eval);
var ev = eval; console.log(ev(7), ev());
console.log([1, 2].map(eval).join());
var x = 5; globalThis.y = 9; function f3() { var x = 1; var y = 2; return [eval?.("y"), eval("y"), (0, eval)("typeof x")].join(); }
console.log(f3());
`)
	assertOutputCompatJS(t, "var ev = eval;\ntry { ev(String(1)); } catch (e) { console.log(e.name); }\n", "EvalError")
}

// A builtin namespace object (Math, JSON, Reflect, Atomics) called or
// constructed throws TypeError once its arguments are evaluated; tsc
// rejects both (TS2349, TS2351).
func TestE2ENamespaceObjectCalled(t *testing.T) {
	assertSameAsNodeCLIJS(t, `var n = 0;
for (const f of [() => Math(n++), () => JSON(n++), () => Reflect(), () => new Math(), () => Atomics()]) { try { f(); } catch (e) { console.log(e.constructor === TypeError, e.message); } }
console.log(n);
`)
	assertCLICompileError(t, false, "const r = Math();\nconsole.log(r);\n", "This expression is not callable.")
	assertCLICompileError(t, false, "const q = new JSON();\nconsole.log(q);\n", "This expression is not constructable.")
}

// Function of source known only at run time converts its arguments to
// strings in order, then throws EvalError: compiling them needs an embedded
// JavaScript engine. Constant source still compiles.
func TestE2EFunctionOfRuntimeSourceJS(t *testing.T) {
	assertOutputCLIJS(t, `
var n = 0; var k = { toString() { n++; return "x"; } };
try { new Function(k, "return " + "1")(); } catch (e) { console.log(e.constructor.name, n); }
try { Function(k)(); } catch (e) { console.log(e.constructor.name, n); }
console.log(new Function("a", "b", "return a + b")(1, 2));
`, "EvalError 1\nEvalError 2\n3")
}

// A direct eval of a string computed at run time evaluates its argument,
// then throws EvalError.
func TestE2EDirectEvalOfRuntimeStringJS(t *testing.T) {
	assertOutputCLIJS(t, `
var n = 0;
function s() { n++; return "1 + " + n; }
try { console.log(eval(s())); } catch (e) { console.log(e.name, n); }
`, "EvalError 1")
}

// A Function's text is script code: import.meta in its body or parameters
// is a SyntaxError when it is constructed.
func TestE2EFunctionTextImportMetaIsSyntaxError(t *testing.T) {
	assertJSFileSameAsNode(t, `
try { Function("import.meta"); } catch (e) { console.log(e.constructor.name, e.message); }
try { Function("a = import.meta", ""); } catch (e) { console.log(e.constructor.name, e.message); }
`)
}
