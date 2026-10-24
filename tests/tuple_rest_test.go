package tests

import "testing"

// A rest parameter of a tuple type (fixed, optional or rest elements) or
// of an alias for one collects the arguments as JavaScript does.
func TestE2ETupleRestParameters(t *testing.T) {
	assertSameAsNode(t, `function f(...a: [number, string]) { console.log(a, a.length, a[0] + 1, a[1].toUpperCase()); }
f(1, "s");
function g(...a: [x: number, y?: string]) { console.log(a.length, a); }
g(2); g(3, "t");
function h(...a: [number, ...string[]]) { console.log(a); }
h(4, "a", "b");
function k(head: string, ...a: []) { console.log(head, a); }
k("only");
type AnyRest = [...args: any[]];
function m(...a: AnyRest) { console.log(a.length); }
m(1, "2", [3]);
const arrow = (...xs: [boolean, number]) => xs[1];
console.log(arrow(true, 9));
class C { meth(...p: [string, number]) { return p[0].repeat(p[1]); } }
console.log(new C().meth("ab", 2));
`)
}

// A tuple as an array: a non-constant index, compound assignment, the
// non-mutating array methods, spread (into an array or a fixed-arity
// call), for...of, Array.from and rest destructuring.
func TestE2ETupleAsArray(t *testing.T) {
	assertSameAsNode(t, `const t: [number, string, boolean] = [1, "a", true];
for (let i = 0; i < 4; i++) console.log(i, t[i], typeof t[i]);
const c: [number, string] = [1, "a"];
c[0] += 5; c[1] += "b"; c[0] *= 2;
console.log(c, c.map(x => typeof x), c.join("-"), c.includes("ab"), c.indexOf(12), c.slice(1), c.at(-1));
console.log(c.reduce((acc: string, x) => acc + String(x), ""), [...c.entries()], c.toString());
c.forEach((x, i) => console.log(i, x));
console.log([...c], [0, ...c, 9], Array.from(c));
for (const x of c) console.log(x);
const n: [number, number, number] = [1, 2, 3];
const [h, ...tl] = n;
const sum = (xs: number[]) => xs.reduce((a, b) => a + b, 0);
console.log(h, tl, sum(tl), sum([...n]), Math.max(...n));
function f(a: number, b: string) { return a + ":" + b; }
const args: [number, string] = [7, "x"];
console.log(f(...args));
const [, ...[p, q]] = n;
console.log(p, q);
const pairs: [string, number][] = [["a", 1], ["b", 2]];
for (const [k, ...vs] of pairs) console.log(k, vs);
const arr = [1, 2, 3];
const { length, 0: first, 5: none = 42 } = arr;
const { 1: second } = c;
console.log(length, first, none, second);
const [x0, ...[[i1], ...more]] = [[0], [5], [6]];
console.log(x0, i1, more);
`)
}

// A tuple in a union: built from an array literal, narrowed by typeof or
// Array.isArray, chosen by a conditional or ?? into a tuple slot.
func TestE2ETupleInUnion(t *testing.T) {
	assertSameAsNode(t, `const pick = (b: boolean) => b;
const u: [number, string] | null = pick(false) ? null : [1, "a"];
console.log(u);
const v: [number, string] | string = pick(false) ? "s" : [1, "a"];
console.log(v, typeof v === "string" ? v : v[1]);
function f(x: [number, string] | string) { return typeof x === "string" ? x : x[1]; }
console.log(f([1, "b"]), f("s"));
function h(x: [number, string] | number) { return Array.isArray(x) ? x[1] + "!" : x * 2; }
console.log(h([1, "b"]), h(4));
const o: { p?: [number, string] } = {};
const w: [number, string] = o.p ?? [3, "z"];
const o2: { p?: [number, string] } = { p: [1, "q"] };
const w2: [number, string] = o2.p || [3, "z"];
console.log(w, w2);
`)
}

// A type parameter's default may name a declared type; inference keeps a
// literal where a union parameter of its kind decides it.
func TestE2ETypeParamDefaultsAndLiteralInference(t *testing.T) {
	assertSameAsNode(t, `type S = string;
interface I { a: number }
class C6<T = S> { v?: T; show(x: T) { return x; } }
console.log(new C6().show("d"));
function f<T = S>(): T[] { return []; }
const xs = f();
xs.push("q");
console.log(xs);
type Al<T = I> = { v: T };
const al: Al = { v: { a: 2 } };
console.log(al.v.a);
function w2<K>(k: K | "z", cb: K extends "a" ? () => number : never) { return cb(); }
console.log(w2("a", () => 7));
`)
	mustCompileError(t, `function w1<K>(k: K, cb: K extends "a" ? 1 : 2) {}
w1("a", 1);`, "argument of type '1' is not assignable to parameter of type '2'")
	mustCompileError(t, `type C1<T> = T extends [never] ? 1 : 2;
const q: C1<{ a: [number, string] }> = 1;`, "type '1' is not assignable to type '2'")
}
