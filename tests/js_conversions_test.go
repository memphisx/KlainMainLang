package tests

import "testing"

// -compat=js: a typed array stores each element's ToNumber (after
// ToPrimitive), as JavaScript does.
func TestE2ETypedArrayElementToNumberCompatJS(t *testing.T) {
	assertJSFileSameAsNode(t, `
const seq = [0xC0];
const a = "7";
console.log(new Uint8Array([seq]));
console.log(new Uint8Array([a, true, null, [5], "x"]));
console.log(new Float64Array(["1.5", false]));
`)
}

// console.count's label is converted with ToString, as Node's
// `${label}` does.
func TestE2EConsoleCountLabelToStringCompatJS(t *testing.T) {
	assertJSFileSameAsNode(t, `
console.count();
console.count(1);
console.count(1);
console.count(null);
console.count('null');
console.count(undefined);
console.count({});
`)
}

// A top-level binding holding a user-function call, passed to another
// function, types that parameter by the call's result (ADR-01517).
func TestJSTopLevelCallResultArgument(t *testing.T) {
	assertSameAsNodeCompatJS(t, `
function k(a) { return a === 1; }
function s2() { return "s"; }
function n2(x) { return x * 1.5; }
function o2() { return { a: 1 }; }
function show(v) { return "v=" + v + " " + typeof v; }
function num(v) { return v + 1; }
console.log(show(k(1)), show(s2()), num(n2(3)), show(o2().a));
const a = n2(2), b = o2();
console.log(num(a), show(b.a), show(a));
const z = k(1), w = s2();
console.log(show(z), show(w));
`)
}

// `X["k"]` on a builtin global reads as `X.k` (ADR-01525).
func TestBuiltinGlobalLiteralKeyMember(t *testing.T) {
	assertSameAsNodeCompatJS(t, `
console.log(typeof Date["parse"], typeof Math["max"], typeof Number["parseFloat"], Math["max"](1, 3), Date["parse"]("2020-01-01T00:00:00Z"), typeof Math["PI"], Math["PI"] === Math.PI);
const f = Date["UTC"];
console.log(f(2020), Date["UTC"] === Date.UTC);
`)
}
