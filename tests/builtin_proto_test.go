package tests

import "testing"

// A builtin class's prototype is a value: its methods are the ones an
// instance reads, `.call` reaches the receiver's own method, another
// class's receiver is Node's TypeError, and Object.getPrototypeOf of an
// instance is it.
func TestE2EBuiltinPrototypeValues(t *testing.T) {
	assertOutput(t, `const m = new Map<string, number>([["a", 1]]);
const m2 = new Map<number, string>([[1, "x"]]);
console.log(Map.prototype.get === m.get, m.get === m2.get);
console.log(typeof Map.prototype.get, typeof Map.prototype);
const g = Map.prototype.get;
console.log(g.call(m, "a"), g.call(m2, 1));
console.log(Map.prototype.constructor === Map, Object.keys(Map.prototype).length);
const s = new Set([1]);
console.log(Set.prototype.has.call(s, 1), Set.prototype.has === s.has);
try { Map.prototype.get.call(s, 1); } catch (e) { console.log((e as Error).constructor.name, (e as Error).message); }
try { (Map.prototype.get as any).call(1, 1); } catch (e) { console.log((e as Error).message); }
const d = new Date(0);
console.log(Date.prototype.getTime === d.getTime, Date.prototype.getTime.call(d));
const r = /a/g;
console.log(RegExp.prototype.test === r.test, RegExp.prototype.test.call(r, "a"));
const p = Promise.resolve(1);
console.log(typeof Promise.prototype.then, Promise.prototype.then === p.then);
const any: any = new Map();
console.log(Object.getPrototypeOf(any) === Map.prototype, Object.getPrototypeOf(m2) === Map.prototype);
console.log(Object.getPrototypeOf(p) === Promise.prototype, Object.getPrototypeOf(d) === Date.prototype);
console.log(String(Map.prototype), Object.prototype.toString.call(Map.prototype));
console.log(Map.prototype, Date.prototype);
Promise.prototype.then.call(p, (v: number) => console.log("got", v));`, `true true
function object
1 x
true 0
true true
TypeError Method Map.prototype.get called on incompatible receiver #<Set>
Method Map.prototype.get called on incompatible receiver 1
true 0
true true
function true
true true
true true
[object Map] [object Map]
Object [Map] {} {}
got 1`)
}

// Array.prototype's methods are values: an array's method read is one, `.call`
// applies it to any array or array-like, and Object.getPrototypeOf of an
// array is Array.prototype.
func TestE2EArrayPrototypeValues(t *testing.T) {
	assertOutput(t, `{
const a: any = [1, 2];
const f = a.map;
console.log(typeof f, f === a.map, f === [3].map, Array.prototype.map === f);
console.log(f.call(a, (x: number) => x * 2));
function g(...xs: any[]) { return Array.prototype.slice.call(xs, 1); }
console.log(g(1, 2, 3));
const b = [5, 6, 7];
console.log(Array.prototype.join.call(b, "-"), Array.prototype.push.call(b, 8, 9), b);
console.log(typeof Array.prototype.forEach, Array.prototype.constructor === Array);
console.log(Object.keys(Array.prototype).length);
const typed = [1, 2, 3];
const m = typed.map;
console.log(m === Array.prototype.map, m.call(typed, (x: number) => x + 1));
console.log(Array.prototype.indexOf.call(b, 6), Array.prototype.includes.call(b, 10));
const c: any = [1, 2, 3, 4];
console.log(Array.prototype.concat.call(c, [9], 8), Array.prototype.unshift.call(c, "a", "b"), c);
console.log(Array.prototype.splice.call(c, 1, 2, "x", "y", "z"), c);
console.log(Array.prototype.splice.call(c, -2, 1), c);
const dj: any = JSON.parse("[1,2,3]");
Array.prototype.push.call(dj, 4, 5); Array.prototype.splice.call(dj, 0, 1, "s", "t");
console.log(dj);
}
{
console.log(Array.prototype.map.call("abc", (c: string) => c + c));
function f(a: number, b: number) { return Array.prototype.slice.call(arguments); }
console.log(f(1, 2));
try { Array.prototype.map.call(null, (x: any) => x); } catch (e) { console.log((e as Error).message); }
console.log(Array.prototype.join.call({ length: 2, 0: "a", 1: "b" } as any, "+"));
}
{
console.log(Object.getPrototypeOf([]) === Array.prototype);
const a: any = [1];
console.log(Object.getPrototypeOf(a) === Array.prototype, Object.getPrototypeOf(JSON.parse("[1]")) === Array.prototype);
console.log(Object.getPrototypeOf(new Uint8Array(2)) === Array.prototype);
}`, `function true true true
[ 2, 4 ]
[ 2, 3 ]
5-6-7 5 [ 5, 6, 7, 8, 9 ]
function true
0
true [ 2, 3, 4 ]
1 false
[ 1, 2, 3, 4, 9, 8 ] 6 [ 'a', 'b', 1, 2, 3, 4 ]
[ 'b', 1 ] [
  'a', 'x', 'y', 'z',
  2,   3,   4
]
[ 3 ] [ 'a', 'x', 'y', 'z', 2, 4 ]
[ 's', 't', 2, 3, 4, 5 ]
[ 'aa', 'bb', 'cc' ]
[ 1, 2 ]
Array.prototype.map called on null or undefined
a+b
true
true true
false`)
}

// An `any` callback to an array's HOF is called through its box, its result a
// box.
func TestE2EAnyCallbackToArrayHOF(t *testing.T) {
	assertOutput(t, `const cb: any = (x: number) => x * 2; const a: any = [1, 2]; console.log(a.map(cb), [1, 2].map(cb));
const red: any = (s: number, x: number) => s + x; console.log(a.reduce(red, 0));
const gt: any = (x: number) => x > 1; console.log([1, 2, 3].filter(gt), [1, 2].some(gt), [3, 4].every(gt), [1, 5].find(gt));`, `[ 2, 4 ] [ 2, 4 ]
3
[ 2, 3 ] true true 5`)
}

// String.prototype's methods are values: a string's method read is one (a
// binding of it keeps the receiver `.call` passes), and the prototype's
// constructor is String itself.
func TestE2EStringPrototypeValues(t *testing.T) {
	assertOutput(t, `const s = "abc";
const sl = s.slice;
console.log(typeof sl, sl === "x".slice, String.prototype.slice === sl);
console.log(sl.call(s, 1), String.prototype.toUpperCase.call("hi"), String.prototype.concat.call("a", "b", "1"));
const a: any = "xyz";
console.log(typeof a.slice, a.slice === String.prototype.slice, a.indexOf === s.indexOf);
console.log(String.prototype.trim.call(42 as any), String.prototype.padStart.call("5", 3, "0"));
try { String.prototype.trim.call(undefined as any); } catch (e) { console.log((e as Error).message); }
console.log(String.prototype.constructor === String, Object.getPrototypeOf("q") === String.prototype);
console.log(String.prototype.split.call("a,b", ","), String.prototype.replace.call("aa", "a", "b"));
console.log(String.prototype.constructor === String, Array.prototype.constructor === Array, typeof String.prototype.constructor, String === String);const SC: any = String; console.log(SC === String.prototype.constructor);
function g() { const s = "abc"; const sl = s.slice; return sl.call("abc", 1); } console.log(g());`, `function true true
bc HI ab1
function true true
42 005
String.prototype.trim called on null or undefined
true true
[ 'a', 'b' ] ba
true true function true
true
bc`)
}

// A user class's prototype is a value: its own methods (each the one
// function every instance reads), accessors and constructor, linked to its
// base's; getPrototypeOf and instanceof walk it, and a method called on a
// receiver of another kind runs its body on that receiver.
func TestE2EClassPrototypeValues(t *testing.T) {
	assertOutput(t, `{
class C { x = 1; m() { return this.x + 1; } get g() { return 5; } static s() { return 2; } }
class D extends C { n() { return 3; } }
const c = new C(), d = new D();
console.log(typeof C.prototype, C.prototype.m === c.m, D.prototype.m === c.m, D.prototype.n === d.n);
console.log(Object.getPrototypeOf(c) === C.prototype, Object.getPrototypeOf(D.prototype) === C.prototype);
console.log(C.prototype.constructor === C, c.constructor === C, d.constructor === D);
console.log(C.prototype.m.call(d), Object.keys(C.prototype), C.prototype);
console.log(Object.getOwnPropertyNames(C.prototype));
const a: any = new D();
console.log(Object.getPrototypeOf(a) === D.prototype, a.m === C.prototype.m, a.constructor === D);
try { (C.prototype.m as any).call(new Map()); } catch (e) { console.log((e as Error).message); }
console.log(D.prototype instanceof C, C.prototype.hasOwnProperty("m"), D.prototype.hasOwnProperty("m"));
}
{
class C { x = 1; m(k: number) { return this.x + k; } s() { return "v" + this.x; } }
console.log(C.prototype.m.call({ x: 10 }, 5), (C.prototype.s as any).call(new Map()), C.prototype.m.call(new C(), 2));
}`, `object true true true
true true
true true true
2 [] {}
[ 'constructor', 'm', 'g' ]
true true true
true true false
15 vundefined 3`)
}

// A sloppy function's caller and arguments are live during its call and
// null outside one; a strict function's (an arrow, a method, a generator,
// an async function, a builtin) throw.
func TestE2EFunctionCallerArguments(t *testing.T) {
	assertOutput(t, `function f(a: number) { const fa: any = f; console.log(typeof fa.caller, fa.caller === h, fa.arguments, fa.arguments?.length); return a; }
function h() { return f(7); }
h();
f(1);
const fo: any = f;
console.log(fo.caller, fo.arguments);
const g = (x: number) => x;
class K { m() { return 1; } }
function* gen() {}
async function af() {}
for (const [n, fn] of [["g", g], ["parseInt", parseInt], ["m", new K().m], ["gen", gen], ["af", af]] as [string, any][]) {
  for (const k of ["caller", "arguments"]) {
    try { console.log(n, k, fn[k]); } catch (e) { console.log(n, k, (e as Error).constructor.name, (e as Error).message); }
  }
}`, `function true [Arguments] { '0': 7 } 1
function false [Arguments] { '0': 1 } 1
null null
g caller TypeError 'caller', 'callee', and 'arguments' properties may not be accessed on strict mode functions or the arguments objects for calls to them
g arguments TypeError 'caller', 'callee', and 'arguments' properties may not be accessed on strict mode functions or the arguments objects for calls to them
parseInt caller TypeError 'caller', 'callee', and 'arguments' properties may not be accessed on strict mode functions or the arguments objects for calls to them
parseInt arguments TypeError 'caller', 'callee', and 'arguments' properties may not be accessed on strict mode functions or the arguments objects for calls to them
m caller TypeError 'caller', 'callee', and 'arguments' properties may not be accessed on strict mode functions or the arguments objects for calls to them
m arguments TypeError 'caller', 'callee', and 'arguments' properties may not be accessed on strict mode functions or the arguments objects for calls to them
gen caller TypeError 'caller', 'callee', and 'arguments' properties may not be accessed on strict mode functions or the arguments objects for calls to them
gen arguments TypeError 'caller', 'callee', and 'arguments' properties may not be accessed on strict mode functions or the arguments objects for calls to them
af caller TypeError 'caller', 'callee', and 'arguments' properties may not be accessed on strict mode functions or the arguments objects for calls to them
af arguments TypeError 'caller', 'callee', and 'arguments' properties may not be accessed on strict mode functions or the arguments objects for calls to them`)
}

// arguments is an Arguments object, not an Array: Array.isArray, its
// constructor, its tag, inspection, JSON and String, typed or boxed.
func TestE2EArgumentsObject(t *testing.T) {
	assertOutput(t, `{
function f(a: number, b?: number) {
  console.log(arguments, arguments.length, arguments[0]);
  console.log(Array.isArray(arguments), arguments.constructor === Object, Object.prototype.toString.call(arguments));
  console.log(typeof arguments, [...arguments], Array.from(arguments), Array.prototype.slice.call(arguments, 1));
  const ar: any = arguments;
  console.log(Array.isArray(ar), ar.constructor === Array, JSON.stringify(ar), Object.keys(ar), String(ar));
  for (const x of arguments) console.log("of", x);
  for (const k in arguments) console.log("in", k);
}
f(1, 2);
f(3);
function g() { console.log(arguments); }
g();
}
{
function show(xs: any[]) { console.log(xs, Array.isArray(xs), JSON.stringify(xs), String(xs)); }
function f3(a: number, b?: any) {
  console.log(JSON.stringify(arguments), String(arguments), `+"`${arguments}`"+`, arguments + "");
  show(arguments as any);
  console.log([arguments], { a: arguments });
  console.log(arguments.length, arguments[1], Object.keys(arguments));
}
f3(1, undefined);
f3(2, "x");
console.log(Array.isArray([1]), [1, 2], JSON.stringify([1, undefined]), String([1, 2]));
}`, `[Arguments] { '0': 1, '1': 2 } 2 1
false true [object Arguments]
object [ 1, 2 ] [ 1, 2 ] [ 2 ]
false false {"0":1,"1":2} [ '0', '1' ] [object Arguments]
of 1
of 2
in 0
in 1
[Arguments] { '0': 3 } 1 3
false true [object Arguments]
object [ 3 ] [ 3 ] []
false false {"0":3} [ '0' ] [object Arguments]
of 3
in 0
[Arguments] {}
{"0":1} [object Arguments] [object Arguments] [object Arguments]
[Arguments] { '0': 1, '1': undefined } false {"0":1} [object Arguments]
[ [Arguments] { '0': 1, '1': undefined } ] { a: [Arguments] { '0': 1, '1': undefined } }
2 undefined [ '0', '1' ]
{"0":2,"1":"x"} [object Arguments] [object Arguments] [object Arguments]
[Arguments] { '0': 2, '1': 'x' } false {"0":2,"1":"x"} [object Arguments]
[ [Arguments] { '0': 2, '1': 'x' } ] { a: [Arguments] { '0': 2, '1': 'x' } }
2 x [ '0', '1' ]
true [ 1, 2 ] [1,null] 1,2`)
}

// A function constructed through a cast (`new (F as any)(…)`) builds an
// object linked to F.prototype; an object is named by its prototype's
// constructor (`Object.create(C.prototype)` prints as a C).
func TestE2EFunctionConstructorTS(t *testing.T) {
	assertOutput(t, `function F(this: any, v: number) { this.v = v; }
const x = new (F as any)(3);
console.log(x, x.v, x instanceof F, x.constructor === F, Object.getPrototypeOf(x) === F.prototype);
console.log(typeof F.prototype, F.prototype.constructor === F, Object.keys(F.prototype));
(F.prototype as any).get = function () { return this.v * 2; };
console.log(x.get());
function G() { return 1; }
const y = new (G as any)();
console.log(y, y instanceof G);
{
class C { x = 1; }
const o = Object.create(C.prototype);
o.y = 2;
console.log(o, {}, { a: 1 }, Object.create(null), [{ b: 2 }]);
const p: any = { constructor: function Q() {} };
console.log(p);
}`, `F { v: 3 } 3 true true true
object true []
6
G {} true
C { y: 2 } {} { a: 1 } [Object: null prototype] {} [ { b: 2 } ]
{ constructor: [Function: Q] }`)
}

// A primitive's method read as a value, statically or through `any`, or off
// its wrapper, is its prototype's own function; a wrapper has no such own
// property.
func TestE2EPrimitiveMethodValuesAreThePrototypes(t *testing.T) {
	assertSameAsNode(t, `
const c = Number.prototype.toFixed;
console.log((5).toFixed === c, "ab".slice === String.prototype.slice, true.valueOf === Boolean.prototype.valueOf);
const n = 3;
const f = n.toPrecision;
console.log(f.call(3.14159, 3));
const x: any = 255;
const s: any = "ab";
console.log(typeof x.toFixed, x.toFixed === c, x.toString === Number.prototype.toString, s.slice === String.prototype.slice);
const w: any = new String("abc");
console.log(w.slice(1), w.slice === String.prototype.slice, Object.prototype.hasOwnProperty.call(w, "slice"));
const nw: any = new Number(5);
console.log(nw.toFixed(2), nw.toFixed === c);
`)
}

// An accessor defined on an array element is read through its getter (a
// program that never builds Object.prototype's methods included), and a Map
// built from an untyped source keeps each entry's value; a throwing entry
// closes an endless iterator.
func TestE2EArrayElementAccessorAndUntypedMapSource(t *testing.T) {
	assertSameAsNodeCompatJS(t, `
var item: any = ['foo', 'bar'];
Object.defineProperty(item, "1", { get: function () { return "GOT"; } });
console.log(item[1]);
var src: any = [['k', 'v']];
console.log(new Map(src).get("k"));
var count = 0;
var bad: any = ['x', 'y'];
Object.defineProperty(bad, 0, { get: function () { throw new Error("entry"); } });
var iterable: any = {};
iterable[Symbol.iterator] = function () {
  return { next: function () { return { value: bad, done: false }; }, return: function () { count++; return {}; } };
};
try { new Map(iterable); } catch (e) { console.log((e as Error).message, count); }
`)
}

// A primitive `this` boxed to its wrapper reads its class prototype's
// methods and Object.prototype's, and those methods accept the wrapper.
func TestE2EWrapperReceiverReadsItsPrototype(t *testing.T) {
	assertSameAsNodeCompatJS(t, `
function cb(this: any) { return [this.valueOf(), this.toString(), typeof this.hasOwnProperty]; }
console.log(cb.call(false), cb.call(7), cb.call("s"));
console.log(Boolean.prototype.valueOf.call(Object(true)), Number.prototype.toFixed.call(Object(2.5), 2));
try { Boolean.prototype.valueOf.call({}); } catch (e) { console.log((e as Error).message); }
`)
}
