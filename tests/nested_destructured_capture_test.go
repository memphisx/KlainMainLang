package tests

import "testing"

func TestE2ENestedFunctionReadsDestructuredLocal(t *testing.T) {
	// A nested function declaration reading a destructured local of its
	// enclosing function, also from a parameter default.
	assertSameAsNode(t, `
function o(opts: { size: number }) { const { size } = opts; function d() { return 8 * size; } return d(); }
function p(v: [number, { b: number }]) { const [a, { b }] = v; function d(n = a) { return n + b; } return d(); }
console.log(o({ size: 2 }), p([1, { b: 2 }]));
`)
}

func TestE2ENestedFunctionReadsDestructuredLocalJS(t *testing.T) {
	// The same in JavaScript, where a function's own body is no Annex B
	// block, and a variable reassigned with another type keeps the value.
	assertSameAsNodeCLIJS(t, `
function o(opts) { const { size } = opts; function d() { return 8 * size; } return d(); }
function g() { g = 5; return typeof g; }
function b() { { function f() { return 1; } } return f(); }
function q() { let h = 1; h = "u"; return h; }
function r() { var f = function () {}; f = 3; return f; }
console.log(o({ size: 2 }), g(), typeof g, b(), q(), r());
`)
}

func TestE2EClassLocalReadInNestedFunction(t *testing.T) {
	// A class declared in a function, naming the function's locals from
	// functions nested in its members (their `this` is not the instance),
	// including a local written after the class.
	assertSameAsNode(t, `
function f(seed: number) {
  let b = seed;
  const tag = "t" + seed;
  class C {
    constructor() { [1].forEach(function () { console.log("ctor", tag); }); }
    m() { setTimeout(function () { console.log(tag, b); }, 0); return function () { return tag.length + b; }; }
    static s() { return [0].map(function () { return tag; })[0]; }
  }
  const c = new C();
  b = seed * 10;
  console.log(c.m()(), C.s());
}
f(1); f(2);
`)
}

func TestE2ECallBeforeDestructuringNestedFunctionReads(t *testing.T) {
	// A function built before a destructuring declaration that it reads:
	// called before it, the read is the dead zone's ReferenceError; after,
	// the destructured value, written values included.
	assertSameAsNode(t, `
function g(o: { size: number, name: string, inner: { k: boolean } }, arr: [string, number]) {
  const show = () => [size, name, k, first, second];
  try { show(); } catch (e: any) { console.log(e.constructor.name, e.message); }
  try { d(); } catch (e: any) { console.log(e.constructor.name, e.message); }
  const { size, name, inner: { k } } = o;
  let [first, second = 9] = arr;
  function d() { return name + first; }
  console.log(show(), d());
  first = "z";
  console.log(show());
}
g({ size: 3, name: "n", inner: { k: true } }, ["a", 2]);
`)
}
