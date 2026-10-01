package tests

import "testing"

// A string or object that may hold null and undefined both keeps them apart
// (TDD-00230 P3.3's three states): printing, JSON, equality, ??, ?., typeof,
// truthiness, destructuring defaults, parameters, returns, fields and delete.

func TestE2EThreeStateStrings(t *testing.T) {
	assertSameAsNode(t, `
interface Dto { id: number; name?: string | null }
const a: Dto = { id: 1 };
const b: Dto = { id: 2, name: null };
const c: Dto = { id: 3, name: "x" };
console.log(a, b, c);
console.log(JSON.stringify([a, b, c]));
console.log(a.name, b.name, c.name);
console.log(String(a.name), `+"`"+`${b.name}`+"`"+`, "n=" + c.name);
console.log(a.name === undefined, a.name === null, b.name === null, b.name === undefined, c.name === null);
console.log(a.name == null, b.name == null, c.name == null, b.name != undefined);
console.log(a.name ?? "d1", b.name ?? "d2", c.name ?? "d3");
console.log(!a.name, !b.name, !c.name, typeof a.name, typeof b.name, typeof c.name);
console.log(a.name?.length, b.name?.length, c.name?.length);
const { name: n1 = "def" } = a; const { name: n2 = "def" } = b;
console.log(n1, n2);
let v: string | null | undefined = null;
console.log(v, v === null);
v = undefined; console.log(v, v === undefined);
v = "s"; console.log(v, v.length);
function f(x: string | null | undefined): string { return x === null ? "N" : x === undefined ? "U" : x; }
console.log(f(null), f(undefined), f("q"), f(b.name), f(a.name));
const any1: any = b.name; const any2: any = a.name;
console.log(any1, any2, any1 === null, any2 === undefined);
console.log(Object.keys(a), Object.keys(b), "name" in a, "name" in b);
const d: Dto = JSON.parse('{"id":4,"name":null}'); const e: Dto = JSON.parse('{"id":5}');
console.log(d, e, JSON.stringify(d), JSON.stringify(e));
const sp = { ...b }; console.log(sp);
if (c.name) console.log(c.name.toUpperCase());
if (b.name !== null && b.name !== undefined) console.log("never"); else console.log("absent");
`)
}

func TestE2EThreeStateObjects(t *testing.T) {
	assertSameAsNode(t, `
interface P { x: number }
class Node1 { next?: Node1 | null; val: number; constructor(v: number) { this.val = v; } }
const n = new Node1(1); const m = new Node1(2); m.next = null; const k = new Node1(3); k.next = n;
console.log(n, m, k.next.val);
for (const q of [n, m, k]) { if (q.next) console.log("has", q.next.val); else console.log("no", q.next === null, q.next === undefined, !q.next); }
let o: P | null | undefined = null;
if (o) console.log("truthy"); else console.log("falsy", o);
o = { x: 5 }; if (o) console.log(o.x);
function g(p?: string | null): string { return p === undefined ? "missing" : p === null ? "null" : p; }
console.log(g(), g(null), g("v"));
const arr: (string | null | undefined)[] = [null, undefined, "a"];
console.log(arr, arr.map(v => v === null ? "N" : v === undefined ? "U" : v), JSON.stringify(arr));
const mp = new Map<string, string | null | undefined>([["a", null], ["b", undefined]]);
console.log(mp, mp.get("a") === null, mp.get("b") === undefined, mp.get("zz"));
function r(i: number): string | null | undefined { return i === 0 ? null : i === 1 ? undefined : "s"; }
console.log(r(0), r(1), r(2), [r(0), r(1)]);
const obj = { a: null as string | null | undefined, b: undefined as string | null | undefined };
console.log(obj, JSON.stringify(obj));
let s: string | null | undefined; console.log(s); s = s ?? null; console.log(s);
let q: { v: number } | null | undefined = { v: 1 };
q = null; console.log(q, q === null);
q = undefined; console.log(q, q === undefined);
const holder: { r?: string | null } = {};
holder.r = null; console.log(holder, holder.r === null);
delete holder.r; console.log(holder);
`)
}

func TestE2EThreeStateDto(t *testing.T) {
	assertSameAsNode(t, `
interface Dto { id: number; name?: string | null; note?: string }
const a: Dto = { id: 1 };
const b: Dto = { id: 2, name: null };
const c: Dto = JSON.parse('{"id":3}');
const d: Dto = JSON.parse('{"id":4,"name":null}');
console.log(JSON.stringify([a, b, c, d]));
console.log(a, b, c, d);
console.log(a.name === undefined, b.name === null, c.name === undefined, d.name === null);
`)
}

func TestE2EThreeStateOptionalNullableField(t *testing.T) {
	assertSameAsNode(t, `
interface User { name?: string | null }
let w: User = {};
console.log(w.name, w.name === undefined, w.name === null);
let { name = "anon" } = w;
console.log(name);
interface U2 { name?: string }
let w2: U2 = {};
let { name: n2 = "anon" } = w2;
console.log(n2);
`)
}

func TestE2EThreeStateScalars(t *testing.T) {
	assertSameAsNode(t, `
let x: number | null | undefined = null;
console.log(x, x === null, x === undefined, x == null, typeof x, String(x), `+"`"+`${x}`+"`"+`);
x = undefined; console.log(x, x === null, x === undefined, typeof x);
x = 0; console.log(x, x === null, x ?? 9, !x, typeof x);
x = 5; if (x !== null && x !== undefined) console.log(x + 1);
function f(p: number | null | undefined): string { return p === null ? "N" : p === undefined ? "U" : "v" + p; }
console.log(f(null), f(undefined), f(0), f(x));
function g(p?: boolean | null): string { return p === null ? "N" : p === undefined ? "U" : String(p); }
console.log(g(), g(null), g(false), g(true));
function r(i: number): number | null | undefined { return i === 0 ? null : i === 1 ? undefined : i; }
console.log(r(0), r(1), r(2), r(0) ?? "d", [r(0), r(1), r(2)]);
const arr: (number | null | undefined)[] = [null, undefined, 3];
console.log(arr, JSON.stringify(arr), arr.map(v => v === null ? "N" : v === undefined ? "U" : v));
const mp = new Map<string, number | null | undefined>([["a", null], ["b", undefined], ["c", 1]]);
console.log(mp, mp.get("a") === null, mp.get("b") === undefined, mp.get("zz") === undefined);
interface D { n?: number | null; b?: boolean | null }
const ds: D[] = JSON.parse('[{"n":null,"b":null},{},{"n":2,"b":true}]');
console.log(ds, JSON.stringify(ds), ds.map(d => [d.n === null, d.n === undefined]));
const any1: any = r(0); const any2: any = r(1);
console.log(any1, any2, any1 === null, any2 === undefined);
const { n: dn = 7 } = ds[0]; const { n: dm = 7 } = ds[1];
console.log(dn, dm);
`)
}

func TestE2EThreeStateScalarFields(t *testing.T) {
	assertSameAsNode(t, `
interface I { n?: number | null }
const a: I = { n: null }; const b: I = {};
console.log(a, b, JSON.stringify(a), a.n === null, b.n === undefined);
class C { f?: number; constructor() { this.f = undefined; } }
console.log(new C());
`)
}

func TestE2EThreeStateScalarLocals(t *testing.T) {
	assertSameAsNode(t, `
let x: number | null | undefined = null;
console.log(x, x === null);
function f() { let y: number | null | undefined = null; console.log(y, y === null); y = undefined; console.log(y); y = null; console.log(y); }
f();
x = undefined; console.log(x); x = null; console.log(x);
`)
}

func TestE2EThreeStateClassField(t *testing.T) {
	assertSameAsNode(t, `
class N { next?: N | null; v = 1; }
const m = new N(); m.next = null;
console.log(m.next === null, !m.next, m);
if (m.next) console.log("has"); else console.log("no");
const arr = [m];
for (const q of arr) { console.log(q.next === null); if (q.next) console.log("has2"); }
`)
}

func TestE2EThreeStateArrayOfCalls(t *testing.T) {
	assertSameAsNode(t, `
function r(i: number): number | null | undefined { return i === 0 ? null : i === 1 ? undefined : i; }
const a1: (number | null | undefined)[] = [r(0), r(1), r(2)];
const a2 = [r(0), r(1), r(2)];
console.log(a1, a2);
const x = r(0); console.log(x, [x]);
`)
}
