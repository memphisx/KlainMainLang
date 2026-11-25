package tests

import "testing"

// Array holes (TDD-00226): the elements a gap write, `new Array(n)`, an
// elided literal element or `delete xs[i]` leave unset, which Node reads as
// undefined, skips in forEach/map/filter/reduce/some/every/indexOf/keys,
// prints as `<n empty items>` and serializes as null.

func TestE2EArrayHolesProducers(t *testing.T) {
	assertSameAsNode(t, `
const a: number[] = [1, 2];
a[5] = 9;
console.log(a.length, a[3], a[5], a[3] === undefined);
a[3] = 4;
console.log(a[3]);
const s: string[] = [];
s[2] = "x";
console.log(s.length, s[0], s[2]);
export {};
`)
}

func TestE2EArrayHolesInspect(t *testing.T) {
	assertSameAsNode(t, `
const a: number[] = [1, 2];
a[5] = 9;
console.log(a);
console.log(JSON.stringify(a));
const s: string[] = [];
s[2] = "x";
console.log(s, JSON.stringify(s));
export {};
`)
}

func TestE2EArrayHolesConstructors(t *testing.T) {
	assertSameAsNode(t, `
const a: number[] = new Array(3);
console.log(a, a.length, a[0]);
a[1] = 5;
console.log(a);
const b = [1, , 3];
console.log(b, JSON.stringify(b), b[1]);
const c = ["x", , "z"];
console.log(c);
const d2 = Array<number>(2);
console.log(d2);
export {};
`)
}

func TestE2EArrayHolesWalkers(t *testing.T) {
	assertSameAsNode(t, `
const a: number[] = [1, 2];
a[4] = 5;
let n = 0;
a.forEach((x) => { n++; });
console.log("forEach", n);
console.log("map", a.map((x) => x * 2));
console.log("filter", a.filter(() => true));
console.log("reduce", a.reduce((s, x) => s + x, 0));
console.log("some", a.some((x) => x === undefined), "every", a.every((x) => x > 0));
console.log("indexOf", a.indexOf(undefined as any), "includes", a.includes(undefined as any));
console.log("join", a.join("-"), String(a));
console.log("keys", Object.keys(a));
console.log("slice", a.slice(1), "concat", a.concat([7]));
let ins = 0;
for (const k in a) ins++;
console.log("forin", ins);
export {};
`)
}

func TestE2EArrayHolesElementKinds(t *testing.T) {
	assertSameAsNode(t, `
const s: string[] = ["a"];
s[2] = "c";
for (const x of s) console.log(x);
console.log([...s], s.find((x) => x === undefined), s.findIndex((x) => x === undefined));
const f: number[] = [1.5];
f[2] = 2.5;
const a: any[] = [1];
a[2] = "z";
for (const x of a) console.log(x);
console.log([...a], a.includes(undefined));
const [p, q] = s;
console.log(p, q);
export {};
`)
}

func TestE2EArrayHolesMutators(t *testing.T) {
	assertSameAsNode(t, `
const a: string[] = ["d"];
a[3] = "b";
a.push("a");
console.log(a, a.length);
console.log(a.pop(), a);
a.unshift("z");
console.log(a);
console.log(a.shift(), a);
const r = a.splice(1, 2, "q");
console.log(r, a);
a.reverse();
console.log(a);
a.length = 6;
console.log(a);
a.sort();
console.log(a);
a.length = 2;
a.push("w");
console.log(a);
const f: string[] = ["x"];
f[3] = "y";
f.fill("k", 1, 3);
console.log(f);
const n: number[] = [5];
n[3] = 1;
n.sort((p, q) => p - q);
console.log(n, n.length);
export {};
`)
}

func TestE2EArrayHolesDeleteAndHas(t *testing.T) {
	assertSameAsNode(t, `
const a: string[] = ["x", "y", "z"];
console.log(delete a[1], a, a.length);
console.log(1 in a, 0 in a, 5 in a);
console.log(a.hasOwnProperty(1), a.hasOwnProperty("0"), Object.hasOwn(a, 1));
const b: number[] = [];
b[2] = 7;
console.log(0 in b, 2 in b, Object.keys(b), b.hasOwnProperty(0));
export {};
`)
}

func TestE2EArrayHolesEdges(t *testing.T) {
	assertSameAsNode(t, `
const a: string[] = [];
a[2] = "x"; a[3] = "y";
console.log(a.reduce((s, v) => s + v));
const b: string[] = ["p"]; b[2] = "q";
b.copyWithin(0, 1);
console.log(b);
const c = ["a", ...["b"], , "d"];
console.log(c);
export {};`)
}

// A write far past the end keeps the array sparse, as V8's dictionary
// elements do: `x[2 ** 32 - 2] = v` neither allocates 2^32 slots nor loses a
// value, and a walk over a sparse array sees what Node does.
func TestE2EArraySparseElements(t *testing.T) {
	assertSameAsNode(t, `
const y: string[] = [];
y[4294967294] = "big";
console.log(y.length, y[4294967294], y[0], 4294967294 in y, 5 in y);
y[4294967293] = "next";
console.log(y.length, y[4294967293]);
y.length = 10;
console.log(y.length, y[4294967294]);
const z: number[] = [1, 2];
z[100000] = 9;
z[100000] += 1;
console.log(z.length, z[100000], z[50000], z.indexOf(10));
z.length = 3;
console.log(z.length, z[100000], z.join("-"));
const w: number[] = [1.5];
w[1e6] = 2.5;
let sum = 0;
w.forEach((v) => { sum += v; });
console.log(w.length, sum, w.filter((v) => v > 2));
const q: string[] = [];
q[3000] = "a";
q.push("b");
console.log(q.length, q[3000], q[3001], q.slice(2999));
let k = 1;
const a: number[] = [];
for (let i = 0; i < 32; i++) { k *= 2; a[k - 2] = k; }
k = 1;
let ok = true;
for (let i = 0; i < 32; i++) { k *= 2; if (a[k - 2] !== k) ok = false; }
console.log("pow2", ok, a.length);
export {};
`)
}

func TestE2EArraySparseElementsJS(t *testing.T) {
	assertSameAsNodeCompatJS(t, `
var x = [];
var k = 1;
for (var i = 0; i < 32; i++) { k = k * 2; x[k - 2] = k; }
k = 1;
var bad = 0;
for (i = 0; i < 32; i++) { k = k * 2; if (x[k - 2] !== k) bad++; }
console.log(x.length, bad, x.hasOwnProperty(4294967294), x.hasOwnProperty(4294967293));
var o = [{ n: 1 }];
o[9000] = { n: 2 };
console.log(o.length, o[9000].n, o.map(function (p) { return p.n; }).filter(Boolean).join());
var mixed = [1, "a"];
mixed[5000] = true;
mixed[5000] = null;
console.log(mixed.length, mixed[5000], mixed.lastIndexOf(null), JSON.stringify(mixed.slice(4999)));
`)
}

// Growing `length` far past the end leaves the buffer as it is: every new
// element is a hole, as in V8, so a length of 2^32-1 allocates nothing.
func TestE2EArrayLengthGrowsSparse(t *testing.T) {
	assertSameAsNodeCLIJS(t, `
var a = [1, 2];
a.length = 4294967295;
console.log(a.length, a[0], a[1], a[2], 5 in a, a[4294967294]);
a[10] = 'x';
console.log(a.length, a[10], 10 in a, Object.keys(a));
for (var k in a) console.log('in', k);
a.length = 3;
console.log(a.length, a[10], Object.keys(a));
a.push('y');
console.log(a.length, a[3], a);
var b = [];
b.length = 5000;
b[4999] = 1;
console.log(b.length, b[0], b[4999], b.indexOf(1));
var c = [];
c[4294967294] = null;
console.log(c.length, c[0], c[4294967294], Object.keys(c));
var d = new Array(4294967295);
d[7] = 'z';
console.log(d.length, d[0], d[7], 7 in d, 0 in d, Object.keys(d));
var e = new Array(5);
console.log(e.length, e, Object.keys(e));
var f: any = [0, 1, 2, 3, 4, 5, 6, 7];
f[987654321] = 987654321;
console.log(f.length, f[987654321], f[500], 500 in f, Object.keys(f).length);
f[3000] = "m";
console.log(f[3000], 3000 in f, Object.keys(f).slice(-3));
f.length = 8;
console.log(f.length, 987654321 in f, f[3000], JSON.stringify(f));
var g: any = ["a"];
g.length = 4294967295;
g[10] = "q";
console.log(g.length, g[10], g[0], Object.keys(g));
var h: number[] = [1, 2];
h[5000] = 7;
h[3000] = 1;
console.log(3000 in h, 4000 in h, h[3000], h.length);
`)
}

// A search over a sparse array walks only the elements present (indexOf,
// lastIndexOf, includes), never the buffer at the full length.
func TestE2EArraySearchSparse(t *testing.T) {
	assertSameAsNodeCLIJS(t, `
const a = [1, 2, NaN];
a[40000000] = 7;
a[30000000] = 5;
console.log(a.indexOf(7), a.indexOf(5), a.indexOf(2), a.indexOf(9), a.indexOf(5, 30000001));
console.log(a.lastIndexOf(7), a.lastIndexOf(5), a.lastIndexOf(1), a.lastIndexOf(7, 35000000));
console.log(a.includes(NaN), a.indexOf(NaN), a.includes(5), a.includes(9), a.includes(1, 5));
const s = ["x", "y"];
s[20000000] = "needle";
console.log(s.indexOf("needle"), s.lastIndexOf("x"), s.includes("needle"), s.includes("z"));
const o = { k: 1 };
const os = [{}];
os[3000000] = o;
console.log(os.indexOf(o), os.includes(o), os.lastIndexOf({}));
const d = [1, "two", undefined];
d[5000000] = "two";
console.log(d.indexOf("two"), d.lastIndexOf("two"), d.includes(undefined), d.includes(null));
`)
}
