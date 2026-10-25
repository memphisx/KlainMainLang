package tests

import "testing"

// An empty object literal is typed `{}`, which holds any value but null and
// undefined: its binding is a box, so a later store of another kind is valid
// TypeScript and runs as JavaScript does.
func TestE2EEmptyObjectLiteralHoldsAnyValue(t *testing.T) {
	assertSameAsNode(t, `var v = {};
console.log(v, typeof v, v instanceof Object, JSON.stringify(v));
v = 5;
console.log(v);
let w = {};
w = "s";
console.log(w);
const o = {};
console.log(Object.keys(o).length, String(o), o === o);
function mk() { return {}; }
const m = mk();
console.log(m, typeof m);
function f({} = { a: 1 }) { return "called"; }
console.log(f());
`)
}

// In JavaScript a computed key absent from a destructured parameter's
// default binds undefined, and the function returns it as it is.
func TestE2EComputedKeyPatternParamDefaultJS(t *testing.T) {
	assertSameAsNodeCompatJS(t, `const k = String(1);
function g({ [k]: x } = {}) { return x; }
function h({ [k]: x, b } = { b: 2 }) { return [x, b]; }
console.log(g(), g({ 1: "one" }), h());
`)
}
