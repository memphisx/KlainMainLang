package tests

import "testing"

// Node parity for a class declared in a function, typeof's operand, Date's
// time and locale strings, array literals stored as any, the constructor and
// Symbol.toStringTag of values held in any, JSON.stringify's replacer, space,
// toJSON and cycle messages, JSON.parse's reviver, the namespace objects,
// crypto and the global object as values, DOMException codes, and builtin
// names a program declares itself; each expectation is Node v24's output
// (TZ=UTC, en-US).

func TestE2ELocalClassDeclarations1(t *testing.T) {
	assertOutputImportsEnv(t, `function make(n: number) {
  class P { x: number; constructor(x: number) { this.x = x; } get d(): number { return this.x * 2; } }
  const p = new P(n);
  return p.d;
}
console.log(make(21));
function f(): string { class Q { hi() { return "hi"; } } return new Q().hi(); }
console.log(f());
`, `42
hi`, "TZ=UTC")
}

func TestE2ELocalClassDeclarations2(t *testing.T) {
	assertOutputImportsEnv(t, `function a(): string {
  class A { who(): string { return "A"; } }
  class B extends A { who(): string { return "B<" + super.who(); } }
  const b: A = new B();
  console.log(b instanceof A, b instanceof B, B.name, A.name);
  console.log(b);
  return b.who();
}
function c(): number {
  class A { v = 7; }
  return new A().v;
}
function g<T>(x: T): T {
  class Box<U> { u: U; constructor(u: U) { this.u = u; } get(): U { return this.u; } }
  return new Box<T>(x).get();
}
function shadow(): string {
  class K { k(): string { return "outer"; } }
  {
    class K { k(): string { return "inner"; } }
    console.log(new K().k());
  }
  return new K().k();
}
console.log(a(), c(), g("gen"), shadow());
function factory() {
  class Counter { static made = 0; n = 0; constructor() { Counter.made++; } inc() { return ++this.n; } }
  return new Counter();
}
const f1 = factory(); f1.inc(); console.log(f1.inc());
`, `true true B A
B {}
inner
B<A 7 gen outer
2`, "TZ=UTC")
}

func TestE2ELocalClassStaticsPerEvaluation(t *testing.T) {
	assertOutputImportsEnv(t, `function factory() {
  class Counter { static made = 0; constructor() { Counter.made++; } }
  new Counter();
  return Counter.made;
}
console.log(factory(), factory());
function fx() { const C = class { static n = 0; }; C.n++; return C.n; }
console.log(fx(), fx());
`, `1 1
1 1`, "TZ=UTC")
}

func TestE2ETypeofEvaluatesOperand(t *testing.T) {
	assertOutputImportsEnv(t, `let n = 0;
function f(): number { n++; return 1; }
function g(): boolean { n += 10; return true; }
function h(): number[] { n += 100; return []; }
console.log(typeof f(), typeof g(), typeof h(), n);
`, `number boolean object 111`, "TZ=UTC")
}

func TestE2EDateTimeAndLocaleStrings(t *testing.T) {
	assertOutputImportsEnv(t, `const d = new Date(Date.UTC(2020, 0, 5, 13, 4, 9));
console.log(d.toTimeString(), "|", d.toLocaleString(), "|", d.toLocaleTimeString(), "|", d.toJSON());
const z = new Date(0);
console.log(z.toLocaleTimeString(), new Date(NaN).toJSON(), new Date(NaN).toLocaleString(), new Date(NaN).toTimeString());
const j = new Date(NaN).toJSON();
console.log(j === null, typeof j);
`, `13:04:09 GMT+0000 (Coordinated Universal Time) | 1/5/2020, 1:04:09 PM | 1:04:09 PM | 2020-01-05T13:04:09.000Z
12:00:00 AM null Invalid Date Invalid Date
true object`, "TZ=UTC")
}

func TestE2EAnyArrayLiteralIsArrayOfAny1(t *testing.T) {
	assertOutputImportsEnv(t, `const arr: any = [1];
const o1: any = { x: arr };
console.log("lit", o1.x === arr);
const o2: any = {}; o2.x = arr;
console.log("set", o2.x === arr);
const a2: any = [arr];
console.log("arrlit", a2[0] === arr);
arr.push(o1);
console.log("push", arr[1] === o1, arr[1].x === arr);
const plain: any = { y: 1 };
const o3: any = { p: plain };
console.log("objlit", o3.p === plain);
`, `lit true
set true
arrlit true
push true true
objlit true`, "TZ=UTC")
}

func TestE2EAnyArrayLiteralIsArrayOfAny2(t *testing.T) {
	assertOutputImportsEnv(t, `const o: any = {}; o.list = [1]; o.list.push("x"); console.log(o.list);
function f(a: any) { a.push({ k: 1 }); return a; }
console.log(f([1, 2]));
const n: any = { inner: [1] }; n.inner.push(true); console.log(n);
let r: any; r = [3]; r.push("s"); console.log(r);
const m: any[] = [[1]]; m[0].push("q"); console.log(m);
`, `[ 1, 'x' ]
[ 1, 2, { k: 1 } ]
{ inner: [ 1, true ] }
[ 3, 's' ]
[ [ 1, 'q' ] ]`, "TZ=UTC")
}

func TestE2EConstructorThroughAny(t *testing.T) {
	assertOutputImportsEnv(t, `class K { v = 1; }
const vals: any[] = [{}, [], new K(), new Map(), new Date(0), new Set(), /x/, new Uint8Array(2), Object.create(null), JSON.parse("[1]"), JSON.parse("{}")];
for (const v of vals) { const c: any = v.constructor; console.log(typeof c, c === undefined ? "-" : c.name); }
`, `function Object
function Array
function K
function Map
function Date
function Set
function RegExp
function Uint8Array
undefined -
function Array
function Object`, "TZ=UTC")
}

func TestE2EJSONStringifyReplacerAndSpace(t *testing.T) {
	assertOutputImportsEnv(t, `const o = { a: 1, b: "x", c: [1, 2, { d: true }], e: new Date(0), u: undefined as any, n: null };
console.log(JSON.stringify(o, (k, v) => (typeof v === "number" ? v * 10 : v)));
console.log(JSON.stringify(o, ["a", "c", "d"]));
const sp = 2;
console.log(JSON.stringify(o, null, sp));
console.log(JSON.stringify(o, null, "--"));
console.log(JSON.stringify([1, 2], null, 5n as any));
console.log(JSON.stringify({ a: 1 }, function (this: any, k: string, v: any) { return k === "" ? v : (this === undefined ? "?" : "ok"); }));
const f = JSON.stringify;
console.log(f({ z: [1] }), f([3], null, 1));
console.log(JSON.stringify("top", (k, v) => v + "!"), JSON.stringify(1, () => undefined));
const cy: any = { a: { b: { c: {} } } }; cy.a.b.c.back = cy;
try { JSON.stringify(cy, (k, v) => v); } catch (e: any) { console.log(e.message); }
const arr: any = [1]; arr.push({ x: arr });
try { JSON.stringify(arr, null, sp); } catch (e: any) { console.log(e.message); }
class K { v: number; constructor(v: number) { this.v = v; } toJSON() { return { kv: this.v }; } }
console.log(JSON.stringify({ k: new K(3) }, null, sp));
try { JSON.stringify({ big: 1n }, (k, v) => v); } catch (e: any) { console.log(e.name, e.message); }
`, `{"a":10,"b":"x","c":[10,20,{"d":true}],"e":"1970-01-01T00:00:00.000Z","n":null}
{"a":1,"c":[1,2,{"d":true}]}
{
  "a": 1,
  "b": "x",
  "c": [
    1,
    2,
    {
      "d": true
    }
  ],
  "e": "1970-01-01T00:00:00.000Z",
  "n": null
}
{
--"a": 1,
--"b": "x",
--"c": [
----1,
----2,
----{
------"d": true
----}
--],
--"e": "1970-01-01T00:00:00.000Z",
--"n": null
}
[1,2]
{"a":"ok"}
{"z":[1]} [
 3
]
"top!" undefined
Converting circular structure to JSON
    --> starting at object with constructor 'Object'
    |     property 'a' -> object with constructor 'Object'
    |     property 'b' -> object with constructor 'Object'
    |     property 'c' -> object with constructor 'Object'
    --- property 'back' closes the circle
Converting circular structure to JSON
    --> starting at object with constructor 'Array'
    |     index 1 -> object with constructor 'Object'
    --- property 'x' closes the circle
{
  "k": {
    "kv": 3
  }
}
TypeError Do not know how to serialize a BigInt`, "TZ=UTC")
}

func TestE2EJSONStringifyAnyFieldUndefined(t *testing.T) {
	assertOutputImportsEnv(t, `const o = { a: 1, u: undefined as any, s: undefined as string | undefined };
console.log(JSON.stringify(o));
console.log(JSON.stringify(o, null, 1));
const p = { f: (() => 1) as any, n: null as any, d: { x: [1, undefined] } as any, s: "q" as any };
console.log(JSON.stringify(p), JSON.stringify(p, null, 2));
`, `{"a":1}
{
 "a": 1
}
{"n":null,"d":{"x":[1,null]},"s":"q"} {
  "n": null,
  "d": {
    "x": [
      1,
      null
    ]
  },
  "s": "q"
}`, "TZ=UTC")
}

func TestE2EJSONStringifyToJSONAndCycles(t *testing.T) {
	assertOutputImportsEnv(t, `const a: any = [1]; a.push(a);
try { console.log(JSON.stringify(a)); } catch (e: any) { console.log(e.message); }
const b: any = []; b.push({ k: b });
try { console.log(JSON.stringify({ top: b })); } catch (e: any) { console.log(e.message); }
console.log(a.length, a[1] === a);
const t: any = { toJSON() { return { via: "toJSON" }; } };
console.log(JSON.stringify(t), JSON.stringify({ t }));
class W { x = 1; toJSON() { return "W!"; } }
const w: any = new W();
console.log(JSON.stringify(w), JSON.stringify([w]));
const inner: any = { toJSON(k: string) { return "inner@" + k; } };
const outer: any = { toJSON() { return { wrapped: inner, again: { toJSON() { return 2; } } }; } };
console.log(JSON.stringify(outer), JSON.stringify({ o: outer }));
const self: any = { toJSON() { return self2; } }; const self2: any = { v: 1, toJSON() { return "no"; } };
console.log(JSON.stringify(self));
const arrT: any = [inner, inner];
console.log(JSON.stringify(arrT));
`, `Converting circular structure to JSON
    --> starting at object with constructor 'Array'
    --- index 1 closes the circle
Converting circular structure to JSON
    --> starting at object with constructor 'Array'
    |     index 0 -> object with constructor 'Object'
    --- property 'k' closes the circle
2 true
{"via":"toJSON"} {"t":{"via":"toJSON"}}
"W!" ["W!"]
{"wrapped":"inner@wrapped","again":2} {"o":{"wrapped":"inner@wrapped","again":2}}
{"v":1}
["inner@0","inner@1"]`, "TZ=UTC")
}

func TestE2EJSONParseReviver(t *testing.T) {
	assertOutputImportsEnv(t, `const r = JSON.parse('{"a":1,"b":[1,2,{"c":3}],"d":"x"}', (k, v) => (typeof v === "number" ? v * 10 : (k === "d" ? undefined : v)));
console.log(r);
const keys: string[] = [];
JSON.parse('{"p":{"q":1},"r":[5]}', function (this: any, k: string, v: any) { keys.push(k); return v; });
console.log(keys);
console.log(JSON.parse("[1,2]", null as any), JSON.parse('"s"', (k, v) => v + "!"));
const p = JSON.parse; console.log(p('{"z":1}'));
`, `{ a: 10, b: [ 10, 20, { c: 30 } ] }
[ 'q', 'p', '0', 'r', '' ]
[ 1, 2 ] s!
{ z: 1 }`, "TZ=UTC")
}

func TestE2ENamespaceObjectsAsValues1(t *testing.T) {
	assertOutputImportsEnv(t, `const m = Math;
console.log(m.max(1, 5), m.PI > 3);
const j = JSON;
console.log(j.stringify({ a: 1 }), j.parse("[1]"));
const c = console;
c.log("via alias");
function use(k: Console) { k.log("passed"); }
use(console);
console.log(typeof m, typeof j, typeof c, Math, JSON, Object.keys(Math).length);
const anyM: any = Math;
console.log(anyM.floor(2.5), anyM.SQRT2, Object.prototype.toString.call(anyM), String(anyM));
const R = Reflect; console.log(R.ownKeys({ q: 1 }), R.has({ q: 1 }, "q"), Reflect);
const fns: any[] = [Math, JSON, Reflect];
console.log(fns.map((x: any) => Object.prototype.toString.call(x)));
`, `5 true
{"a":1} [ 1 ]
via alias
passed
object object object Object [Math] {} Object [JSON] {} 0
2 1.4142135623730951 [object Math] [object Math]
[ 'q' ] true Object [Reflect] {}
[ '[object Math]', '[object JSON]', '[object Reflect]' ]`, "TZ=UTC")
}

func TestE2ENamespaceObjectsAsValues2(t *testing.T) {
	assertOutputImportsEnv(t, `const o: any = {};
o.log = console.log; o.error = console.error;
o.max = Math.max; o.floor = Math.floor; o.PI = Math.PI;
o.stringify = JSON.stringify; o.parse = JSON.parse;
o.ownKeys = Reflect.ownKeys;
o.log("a", 1, [2]);
console.log(o.max(1, 9, 3), o.floor(2.7), o.PI, o.stringify({ a: [1] }), o.parse("[1,2]").length, o.ownKeys({ z: 1 }));
`, `a 1 [ 2 ]
9 2 3.141592653589793 {"a":[1]} 2 [ 'z' ]`, "TZ=UTC")
}

func TestE2ENamespaceObjectsAsValues3(t *testing.T) {
	assertOutputImportsEnv(t, `const k = Reflect.ownKeys;
console.log(k({ a: 1 }));
const h = Reflect.has;
console.log(h({ a: 1 }, "a"));
`, `[ 'a' ]
true`, "TZ=UTC")
}

func TestE2EReflectMembersAsValues1(t *testing.T) {
	assertOutputImportsEnv(t, `class P { x: number; constructor(x: number) { this.x = x; } }
const p: any = Reflect.construct(P, [7]);
console.log(p.x, p instanceof P, Reflect.getOwnPropertyDescriptor({ a: 1 }, "a"));
const M: any = Map; const m: any = Reflect.construct(M, []); m.set("k", 1); console.log(m.size);
`, `7 true { value: 1, writable: true, enumerable: true, configurable: true }
1`, "TZ=UTC")
}

func TestE2EReflectMembersAsValues2(t *testing.T) {
	assertOutputImportsEnv(t, `const g: any = Reflect.get, s: any = Reflect.set, a: any = Reflect.apply, c: any = Reflect.construct;
const o: any = { x: 1 };
console.log(g(o, "x"), s(o, "y", 2), o.y, a(Math.max, undefined, [3, 9, 4]));
class P { v: number; constructor(v: number) { this.v = v; } }
console.log(c(P, [5]).v);
`, `1 true 2 9
5`, "TZ=UTC")
}

func TestE2EGlobalObjectAsValue(t *testing.T) {
	assertOutputImportsEnv(t, `(globalThis as any).myGlobal = 5;
const g: any = globalThis;
console.log(typeof g, g.myGlobal, typeof g.setTimeout, g.Math === Math, g.globalThis === g);
g.other = 7;
console.log((globalThis as any).other);
console.log(globalThis.Math.max(1, 2), typeof globalThis, Object.prototype.toString.call(globalThis));
setTimeout(() => console.log("t"), 0);
globalThis.setTimeout(() => console.log("t2"), 0);
`, `object 5 function true true
7
2 object [object global]
t
t2`, "TZ=UTC")
}

func TestE2EObjectAndFunctionAsValues(t *testing.T) {
	assertOutputImportsEnv(t, `const O: any = Object, F: any = Function;
console.log(typeof O, O.name, typeof F, F.name);
const o: any = {}; console.log(o.constructor === Object, ([] as any).constructor === Array, o.constructor === O);
console.log(Object.keys({ a: 1 }));
`, `function Object function Function
true true true
[ 'a' ]`, "TZ=UTC")
}

func TestE2EToStringTagOnDynamicObjects(t *testing.T) {
	assertOutputImportsEnv(t, `const o: any = {};
Object.defineProperty(o, "max", { value: Math.max, writable: true, enumerable: false, configurable: true });
Object.defineProperty(o, Symbol.toStringTag, { value: "Math", writable: false, enumerable: false, configurable: true });
console.log(o, Object.keys(o), o.max(1, 4), Object.prototype.toString.call(o), String(o));
const p: any = { a: 1 }; p[Symbol.toStringTag] = "Thing";
console.log(p, `+"`"+`${p}`+"`"+`, Object.prototype.toString.call(p));
`, `Object [Math] {} [] 4 [object Math] [object Math]
{ a: 1, Symbol(Symbol.toStringTag): 'Thing' } [object Thing] [object Thing]`, "TZ=UTC")
}

func TestE2ESymbolDescriptions1(t *testing.T) {
	assertOutputImportsEnv(t, `const a = Symbol();
const b = Symbol("");
const c = Symbol(undefined);
console.log(a.description, b.description, c.description, a.toString(), String(b));
const d = Symbol(42 as any);
console.log(d.description);
const s: any = Symbol(); console.log(s.description, String(Symbol("k")), Symbol.for("r").description);
const mk: any = Symbol; console.log(mk("v").toString(), typeof mk());
console.log([Symbol("x")], { k: Symbol() });
`, `undefined  undefined Symbol() Symbol()
42
undefined Symbol(k) r
Symbol(v) symbol
[ Symbol(x) ] { k: Symbol() }`, "TZ=UTC")
}

func TestE2ESymbolDescriptions2(t *testing.T) {
	assertOutputImportsEnv(t, `console.log([Symbol("x")], { k: Symbol("y") });
const s = Symbol("z"); console.log(s, [s]);
`, `[ Symbol(x) ] { k: Symbol(y) }
Symbol(z) [ Symbol(z) ]`, "TZ=UTC")
}

func TestE2ECryptoAsValue(t *testing.T) {
	assertOutputImportsEnv(t, `const g = crypto.getRandomValues;
try { g(new Uint8Array(1)); } catch (e: any) { console.log(e.name, e.code, JSON.stringify(e.message)); }
console.log(crypto, Object.prototype.toString.call(crypto), Object.keys(crypto));
try { crypto.getRandomValues(new Float64Array(1) as any); } catch (e: any) { console.log(e.name, e.code, e.message); }
try { console.log(crypto.getRandomValues(new DataView(new ArrayBuffer(2)) as any).byteLength); } catch (e: any) { console.log(e.name, e.code, e.message); }
try { crypto.getRandomValues([1, 2] as any); } catch (e: any) { console.log(e.name, e.code, e.message); }
const r = crypto.randomUUID; try { r(); } catch (e: any) { console.log(e.name, e.code); }
console.log(typeof g.call(crypto, new Uint8Array(1)), g.name, g.length, r.length);
`, `TypeError ERR_INVALID_THIS "Value of \"this\" must be of type Crypto"
Crypto {} [object Crypto] []
TypeMismatchError 17 The data argument must be an integer-type TypedArray
TypeMismatchError 17 The data argument must be an integer-type TypedArray
TypeMismatchError 17 The data argument must be an integer-type TypedArray
TypeError ERR_INVALID_THIS
object getRandomValues 1 0`, "TZ=UTC")
}

func TestE2EDOMExceptionCode(t *testing.T) {
	assertOutputImportsEnv(t, `for (const n of ["AbortError","TypeMismatchError","QuotaExceededError","DataCloneError","NotSupportedError","InvalidStateError","SyntaxError","Foo"]) { const e = new DOMException("m", n); console.log(n, e.code); }
`, `AbortError 20
TypeMismatchError 17
QuotaExceededError 22
DataCloneError 25
NotSupportedError 9
InvalidStateError 11
SyntaxError 12
Foo 0`, "TZ=UTC")
}

func TestE2EBuiltinNamesShadowed1(t *testing.T) {
	assertOutputImportsEnv(t, `function f() { const String = (x: any) => "s:" + x; return String(1); }
function g() { const Math = { max: (a: number, b: number) => a * b }; return Math.max(2, 3); }
function h() { const console = { log: (x: any) => "intercepted " + x }; return console.log("x"); }
function k() { const JSON = { stringify: (x: any) => "custom" }; return JSON.stringify({}); }
function m() { const parseInt = (s: string) => 42; return parseInt("1"); }
function n() { const fetch = (u: string) => "fetched " + u; return fetch("u"); }
console.log(f(), g(), h(), k(), m(), n());
`, `s:1 6 intercepted x custom 42 fetched u`, "TZ=UTC")
}

func TestE2EBuiltinNamesShadowed2(t *testing.T) {
	assertOutputImportsEnv(t, `const Math = { max: (a: number, b: number) => a - b };
const console2 = console;
console2.log(Math.max(5, 3));
const setTimeout = (f: () => void, ms: number) => { console2.log("my timer", ms); f(); };
setTimeout(() => console2.log("ran"), 10);
`, `2
my timer 10
ran`, "TZ=UTC")
}

func TestE2EBuiltinNamesShadowed3(t *testing.T) {
	assertOutputImportsEnv(t, `class Map { size = 99; get(k: string) { return "mine:" + k; } }
const m = new Map();
console.log(m.size, m.get("a"));
function f() { class Set { has() { return "local set"; } } return new Set().has(); }
console.log(f());
function g() { const Date = function () { return 1; }; return typeof Date; }
console.log(g());
`, `99 mine:a
local set
function`, "TZ=UTC")
}

func TestE2EBuiltinNamesShadowed4(t *testing.T) {
	assertOutputImportsEnv(t, `class Request { url: string; constructor(u: string) { this.url = "mine:" + u; } }
console.log(new Request("x").url);
function f() { class Error { msg = "local error"; } return new Error().msg; }
console.log(f(), new Error("real").message);
function g() { const Date = class { now() { return 7; } }; return new Date().now(); }
console.log(g(), typeof new Date().getTime());
function h(Uint8Array: number) { return Uint8Array + 1; }
console.log(h(1), new Uint8Array(2).length);
for (const String of ["a"]) console.log(String);
try { throw 1; } catch (Map) { console.log("caught", Map); }
`, `mine:x
local error real
7 number
2 2
a
caught 1`, "TZ=UTC")
}

func TestE2EBuiltinNamesShadowed5(t *testing.T) {
	assertOutputImportsEnv(t, `function f() { let NaN = 99; return NaN + 1; }
function g() { const Infinity = 5; return Infinity; }
console.log(f(), g(), NaN, Infinity);
`, `100 5 NaN Infinity`, "TZ=UTC")
}

func TestE2EThisInstanceofUnbound(t *testing.T) {
	assertOutputImportsEnv(t, `class K { v = 1; m(): string { return this instanceof K ? "bound" : "unbound"; } n(): any { return this; } }
const k: any = new K();
const f: any = k.m;
console.log(f.call(k));
console.log(f());
const n: any = k.n;
console.log(n());
`, `bound
unbound
undefined`, "TZ=UTC")
}

func TestE2EConsoleDirRuntimeOptions(t *testing.T) {
	assertOutputImportsEnv(t, `import { inspect } from "util";
const o: any = { depth: 0 };
console.log(inspect({ a: { b: 1 } }, o));
const opts = { colors: false, depth: null as any };
console.dir({ a: { b: { c: { d: 1 } } } }, opts);
console.dir({ a: { b: { c: { d: 1 } } } }, o);
`, `{ a: [Object] }
{
  a: { b: { c: { d: 1 } } }
}
{ a: [Object] }`, "TZ=UTC")
}

func TestE2ENewDateFromAny(t *testing.T) {
	assertOutputImportsEnv(t, `const vals: any[] = ["2026-01-02T03:04:05.678Z", 86400000, new Date(5), "garbage", null, true, undefined];
for (const v of vals) { const d = new Date(v); console.log(isNaN(d.getTime()) ? "Invalid" : d.toISOString()); }
`, `2026-01-02T03:04:05.678Z
1970-01-02T00:00:00.000Z
1970-01-01T00:00:00.005Z
Invalid
1970-01-01T00:00:00.000Z
1970-01-01T00:00:00.001Z
Invalid`, "TZ=UTC")
}

func TestE2EDOMExceptionConstants(t *testing.T) {
	assertOutputImportsEnv(t, `const e = new DOMException("m", "AbortError");
console.log(DOMException.ABORT_ERR, e.TIMEOUT_ERR, e.code === DOMException.ABORT_ERR, DOMException.DATA_CLONE_ERR);
`, `20 23 true 25`, "TZ=UTC")
}
