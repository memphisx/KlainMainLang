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
