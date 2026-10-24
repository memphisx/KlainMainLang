package tests

import "testing"

// A primitive's wrapper object (`new Number(3)`, `new String("a")`, `new
// Boolean(false)`, `Object(1)`): an object with its own identity that
// converts to its primitive, reads its primitive's properties and takes
// properties of its own.
func TestE2EPrimitiveWrapperObjects(t *testing.T) {
	assertSameAsNode(t, `
const n = new Number(2.5);
const s = new String("hello");
const b = new Boolean(false);
console.log(typeof n, typeof s, typeof b);
console.log(new Number(1) === new Number(1), n === n, n == (2.5 as any), new String("a") == "a" as any, new String("a") === "a" as any);
console.log(Object.prototype.toString.call(n), Object.prototype.toString.call(s), Object.prototype.toString.call(b));
console.log(n.constructor === Number, s instanceof String, b instanceof Boolean, (n as any) instanceof String);
console.log(`+"`${n}|${s}|${b}`"+`, String(n), String(b), s + "!", n.toString(), n.toFixed(2), b.toString());
console.log(Boolean(b), !b, b ? 1 : 2, typeof b.valueOf(), n.valueOf() + 1, Number(n) + 1);
console.log(s[1], s.charAt(0), s.toUpperCase(), s.slice(1, 3), s.length);
console.log(n, s, b, [n, s, b], { n });
console.log(JSON.stringify(s), JSON.stringify([n, b]), JSON.stringify(new Number(NaN)), JSON.stringify({ n, s, b }));
console.log(new Number(), new String(), new Boolean(), new Number("12"), new Boolean(""), new Boolean("x"));
const arr: any[] = [new Number(3), 1, new Number(2)];
console.log(arr.map(x => +x));
console.log(Object.keys(s), Object.keys(new Number(1)), Object.getOwnPropertyNames(s));
console.log("0" in s, "length" in s, "5" in s);
const w: any = new Number(4);
w.tag = "x";
console.log(w.tag, w, Object.keys(w));
try { (s as any)[0] = "z"; } catch (e) { console.log((e as Error).message); }
for (const c of s) console.log(c);
const o1 = Object(1), o2 = Object("xy"), o3 = Object(true), o4 = new Object(2);
console.log(o1, o2, o3, o4, typeof o1, o1 instanceof Number, o2.length);
const u: any = 5;
console.log(Object(u), Object(null), Object(undefined), Object(u) === u, Object(o1) === o1);
console.log((3).valueOf(), "a".valueOf(), true.valueOf(), u.valueOf());
export {};
`)
}

// An Array.prototype method called on a String wrapper reads its primitive
// (shared library code reaches the wrapper through its weak hook).
func TestE2EArrayMethodOnStringWrapper(t *testing.T) {
	assertSameAsNodeCompatJS(t, `
var obj = new String("hi")
var a = obj
console.log(Array.prototype.join.call(a, "-"))
console.log(Array.prototype.indexOf.call(a, "i"))
var n = 0
Array.prototype.forEach.call(a, function (v, i) { if (n++ < 5) console.log("cb", v, i) })
console.log("n", n)
`)
}

// A void call read as a value is undefined (a String wrapper method's
// argument); a bound function given inline to an Array method is its callback.
func TestE2EVoidValueAndInlineBoundCallback(t *testing.T) {
	assertSameAsNodeCompatJS(t, `
var s = new String("abcb")
console.log(s.indexOf("b"), s.lastIndexOf("b"), s.indexOf(undefined), new String("xundefinedx").indexOf(undefined))
console.log(s.split("b"), s.replace("b", "X"))
function f() {}
console.log(new String("undefined").indexOf(f()), "aundefined".indexOf(f()))
var a = [0, 0];
console.log(JSON.stringify(a.map(function() { return this; }.bind([1, 2]))))
var g = function (x) { return x + this.k }.bind({ k: 1 })
console.log(a.map(g), a.forEach(g), a.filter(g))
`)
}
