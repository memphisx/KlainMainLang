package tests

import "testing"

// Tuple types `[T0, T1, ...]` (TDD-00066): declaration, constant-index read,
// and rendering.
func TestE2ETupleBasics(t *testing.T) {
	assertOutput(t, `
const t: [string, number] = ["hello", 42]
console.log(t[0])
console.log(t[1])
console.log(JSON.stringify(t))
console.log(`+"`${t}`"+`)
`, "hello\n42\n"+`["hello",42]`+"\nhello,42")
}

func TestE2ETupleDestructuring(t *testing.T) {
	assertOutput(t, `
const t: [string, number] = ["hi", 7]
const [a, b] = t
console.log(a)
console.log(b)
`, "hi\n7")
}

func TestE2ETupleForOf(t *testing.T) {
	assertOutput(t, `
const pairs: [string, number][] = [["a", 1], ["b", 2]]
for (const [k, v] of pairs) {
  console.log(k + "=" + v)
}
`, "a=1\nb=2")
}

// A tuple as a function parameter and return value.
func TestE2ETupleParamAndReturn(t *testing.T) {
	assertOutput(t, `
function swap(p: [string, number]): [number, string] { return [p[1], p[0]] }
const s = swap(["a", 1])
console.log(s[0])
console.log(s[1])
`, "1\na")
}

// Nested tuples, indexed and destructured.
func TestE2ETupleNested(t *testing.T) {
	assertOutput(t, `
const n: [number, [string, boolean]] = [1, ["x", true]]
console.log(n[0])
console.log(n[1][0])
console.log(n[1][1])
const [a, [b, c]] = n
console.log(a + "," + b + "," + c)
`, "1\nx\ntrue\n1,x,true")
}

// Tuple .length (a compile-time constant) and constant-index element assignment
// (TDD-00066 caveat reductions).
func TestE2ETupleLengthAndElementAssign(t *testing.T) {
	assertOutput(t, `
const t: [string, number] = ["a", 1]
console.log(t.length)
t[0] = "b"
t[1] = 99
console.log(t[0])
console.log(t[1])
`, "2\nb\n99")
}

// Assigning past the tuple's arity is a clean compile error.
func TestE2ETupleElementAssignOutOfRange(t *testing.T) {
	mustCompileError(t, `
const t: [string, number] = ["a", 1]
t[5] = "x"
`, "has no element at index '5'")
}

// A named interface as a tuple element. Regression for the rewriteType gap
// (fixed alongside TDD-00078): TupleElems was never descended into during the
// resolver's type-name rename pass, so a named member of a tuple stayed
// unmangled while its interface registered under a mangled key — resolving the
// element to the unknown-name default instead of the object type.
func TestE2ETupleNamedElement(t *testing.T) {
	assertOutput(t, `
interface User { name: string }
const pair: [User, number] = [{ name: "Zoe" }, 5]
console.log(pair[0].name)
console.log(pair[1])
`, "Zoe\n5")
}

// Labelled elements (`[name: T, …]`) are documentation only: the tuple is the
// same positional value as without labels.
func TestE2ETupleLabelledElements(t *testing.T) {
	assertOutput(t, `
type Reading = [city: string, celsius: number]
const r: Reading = ["Thessaloniki", 24]
const [city, c] = r
console.log(city, c, r[1], r.length)
function span(xs: number[]): [min: number, max: number] {
  return [Math.min(...xs), Math.max(...xs)]
}
const [lo, hi] = span([4, 9, 2])
console.log(lo, hi)
`, "Thessaloniki 24 24 2\n2 9")
}

// A tuple with an optional or rest element is held as an array of boxed
// elements, as JavaScript holds it.
func TestE2ETupleOptionalRestElements(t *testing.T) {
	assertSameAsNode(t, `const t: [a: number, b?: string] = [1];
const u: [number, string?] = [1, "x"];
const v: [number, ...string[]] = [1, "a", "b"];
console.log(t, t.length, u, u[1], v, v.length);
for (const x of v) console.log(typeof x);
const [h, ...tail] = v;
console.log(h, tail);
`)
}

// A tuple element may itself be an array or a nullable scalar.
func TestE2ETupleArrayAndNullableElements(t *testing.T) {
	assertOutput(t, `
const t: [string, number[]] = ["nums", [1, 2, 3]]
console.log(t[0])
console.log(t[1][2])
const q: [string, number | null] = ["k", null]
console.log(q[1] ?? -1)
const q2: [string, number | null] = ["k", 0]
console.log(q2[1] ?? -1)
`, "nums\n3\n-1\n0")
}

// Map/Array/Object .entries() return real [K, V] tuples, destructurable with
// the standard `for (const [k, v] of ...)` idiom (TDD-00066).
func TestE2EEntriesReturnTuples(t *testing.T) {
	assertOutput(t, `
const m = new Map<string, number>()
m.set("a", 1)
m.set("b", 2)
for (const [k, v] of m.entries()) { console.log(k + "=" + v) }

const arr = [10, 20, 30]
for (const [i, x] of arr.entries()) { console.log(i + ":" + x) }

const obj = { name: "Al", city: "NYC" }
for (const [key, val] of Object.entries(obj)) { console.log(key + "->" + val) }
`, "a=1\nb=2\n0:10\n1:20\n2:30\nname->Al\ncity->NYC")
}

// Destructured callback parameters (`arr.map(([k, v]) => …)`, TDD-00199) —
// the dominant Object.entries/Map-entries/pair-array idiom. Covers the
// un-annotated form (type supplied by contextual typing from the HOF element
// type), the explicitly-annotated heterogeneous tuple form, string- and
// number-returning bodies, holes, nested patterns, and filter/reduce/some.
func TestE2EDestructuredCallbackParam(t *testing.T) {
	assertOutput(t, `
const o = { a: 1, b: 2, c: 3 }
console.log(Object.entries(o).map(([k, v]) => k + "=" + v).join(","))
console.log(Object.entries(o).map(([k, v]) => v * 2).join(","))

const pairs: [string, number][] = [["x", 10], ["y", 20]]
console.log(pairs.map(([k, v]) => k).join(","))
console.log(pairs.filter(([k, v]) => v > 10).map(([k, v]) => k).join(","))
console.log(pairs.reduce((acc, [k, v]) => acc + v, 0))
console.log(pairs.some(([k, v]) => v > 15))

let sum = 0
pairs.forEach(([, v]) => { sum += v })
console.log(sum)

const m = new Map<string, number>()
m.set("p", 1)
m.set("q", 2)
console.log([...m.entries()].map(([k, v]) => k + ":" + v).join(","))

const nested: [[number, number], number][] = [[[1, 2], 3]]
console.log(nested.map(([[x, y], z]) => x + y + z).join(","))

const three: [string, number, boolean][] = [["a", 1, true]]
console.log(three.map(([s, n, b]) => s + n + b).join(","))

// Assigned to a variable: the HOF result's element type must flow from the
// callback's destructured-leaf return type (not default to a number) so the
// assigned variable renders correctly — a bug the inline forms above hid.
const keys = Object.entries(o).map(([k, v]) => k)
console.log(keys.join(","))
const bigKeys = Object.entries(o).filter(([k, v]) => v >= 2).map(([k, v]) => k)
console.log(bigKeys.join(","))
`, "a=1,b=2,c=3\n2,4,6\nx,y\ny\n30\ntrue\n30\np:1,q:2\n6\na1true\na,b,c\nb,c")
}

// An array literal asserted to an array of tuples is built as those tuples,
// whether their elements share one kind or not, and reads back the same
// typed, through a field and through `any`.
func TestE2EAssertedTupleArrayLiteral(t *testing.T) {
	assertSameAsNode(t, `
export {}
const a = [[1, "x"]] as [number, string][]
console.log(a[0][0], a[0][1], Object.fromEntries(a))
const ha: any = a
console.log(ha[0][0], ha[0][1])
const b = [["X", "abc"]] as [string, string][]
console.log(b[0][0], b[0][1], b.length, b[0].length, Object.fromEntries(b))
const hb: any = b
console.log(hb[0][0], hb[0][1])
const o = { h: [["X", "abc"]] as [string, string][], n: 3 }
const ho: any = o.h
console.log(o.h[0][1], ho[0][1], o.n)
`)
}

// A Map whose value type is a tuple stores each set value as that tuple,
// read back through get, `!` and destructuring.
func TestE2EMapOfTuples(t *testing.T) {
	assertSameAsNode(t, `
import * as util from 'node:util';
const t = new Map<number, [string, string]>();
t.set(1, ['a', 'b']);
const v = t.get(1)!;
console.log(v[0], v[1], v.length);
const w = t.get(1);
if (w) console.log(w[0], w);
const [p, q] = t.get(1)!;
console.log(p, q, t.get(2));
const m = util.getSystemErrorMap();
console.log(m.get(-2), util.getSystemErrorName(-2));
`)
}

// An arrow typed by a signature returning a tuple returns its array literal
// as that tuple, a generic signature's held boxed and relaid out where the
// instantiation's tuple is declared.
func TestE2ETupleContextualReturn(t *testing.T) {
	assertOutput(t, `
type P = (a: number, b: string) => [number, string];
const pair: P = (a, b) => [a, b];
console.log(pair(1, "x"));
type G = <A, B>(a: A, b: B) => [A, B];
const gen: G = (a, b) => [a, b];
const p: [number, string] = gen(1, "x");
console.log(p, gen("y", 2), p[0] + 1, p[1].length);
const idf: <T>(x: T) => T = (x) => x;
const n: number = idf(3);
console.log(n, idf("a"));
function f() {
  let q: [number, string];
  q = gen(2, "z");
  const r: [number, string] = gen(4, "w");
  console.log(q[0] + r[0]);
}
f();
const fs: Array<(n: number) => [number, number]> = [(n) => [n, n * 2]];
console.log(fs[0](3));
`, "[ 1, 'x' ]\n[ 1, 'x' ] [ 'y', 2 ] 2 1\n3 a\n6\n[ 3, 6 ]")
}

// A tuple is an array: an alias, an array-typed binding and a box are the
// same object, and its length and contents follow what is done through
// them (ADR-01977).
func TestE2ETupleArrayIdentity(t *testing.T) {
	assertSameAsNode(t, `const pair: [number, string] = [1, "a"];
const q = pair; q[0] = 5;
function f(t: [number, string]) { t[0] = 7; }
f(pair);
const o = { t: pair }; o.t[1] = "z";
const r: (number | string)[] = pair; r[0] = 8;
console.log(pair[0], pair[1], q === pair, o.t === pair, r === pair);
const nums: [number, number] = [3, 4];
const arr: number[] = nums; arr.push(9);
console.log(nums.length, nums, arr === nums, Array.isArray(nums));
const boxed: any = pair;
const back: [number, string] = boxed;
console.log(boxed === pair, back === pair, back[1], JSON.stringify(pair), String(pair));
const sorted: [number, number, number] = [3, 1, 2];
const s2: number[] = sorted; s2.sort();
console.log(sorted, sorted.length);
`)
}

// Array.isArray narrowing a class instance to its intersection with any[]:
// a member both declare takes both signatures (ADR-01982).
func TestE2EArrayIsArrayIntersectionMembers(t *testing.T) {
	assertSameAsNode(t, `let u: Set<number> | number[] = new Set([1]);
console.log(Array.isArray(u) ? u.length : u.size);
const s = new Set([1, 2]);
console.log(Array.isArray(s) ? s.length : s.size);
`)
}

// An array or a function carrying properties through an intersection
// annotation (ADR-01983).
func TestE2EArrayAndFunctionIntersections(t *testing.T) {
	assertSameAsNode(t, `type T = string[] & { tag: string };
const a = ["a"] as T;
a.tag = "t";
console.log(a.length, a.tag, a[0]);
type F = ((x: number) => number) & { id: string };
const f = Object.assign((x: number) => x * 2, { id: "d" }) as F;
console.log(f(2), f.id);
`)
}

// A ternary of a nullable scalar and a scalar of another kind is their
// boxed union (ADR-01983).
func TestE2ETernaryNullableScalarMixedKind(t *testing.T) {
	assertSameAsNode(t, `function f(b: boolean, x: number | undefined) { return b ? x : true; }
console.log(f(true, 1), f(false, 1), f(true, undefined));
function g(v: Map<string, number> | Set<number>) { return v instanceof Map ? v.get("a") : v.has(1); }
console.log(g(new Map([["a", 1]])), g(new Set([1])));
`)
}

// A dynamic function's tuple parameter binds as the array it is (ADR-01984).
func TestE2EDynamicFunctionTupleParam(t *testing.T) {
	assertSameAsNode(t, `const o: any = { f: (p: [number, string]): string => p[1] + p[0], g: (p: [number, number]): number => p[0] + p[1] };
console.log(o.f([1, "a"]), o.g([2, 3]));
`)
}

// A ternary of calls returning nothing, as a statement and as a value; a
// method call on a union narrowed to an object; a mixed tuple in a union
// callback; `new` of a narrowed constructor; a nullable generic context
// (ADR-01985).
func TestE2ENarrowedUnionsAndVoidTernary(t *testing.T) {
	assertSameAsNode(t, `export {};
function a(): void { console.log("a"); }
function b(): void { console.log("b"); }
const t = Math.random() < 2;
t ? a() : b();
const r = t ? a() : b();
console.log(r, t ? 1 : a());
type R = { run(): number; n: number };
function f(v: (() => number) | R | number) {
  if (typeof v === "number") return v;
  return typeof v === "function" ? v() : v.n + v.run();
}
console.log(f(() => 1), f({ n: 2, run() { return 3; } }), f(4));
const xs: ([number, string] | string[])[] = [[1, "a"], ["b", "c", "d"]];
console.log(xs.map(x => x.length));
class K { k = 7 }
function g(c: typeof K | null) { return c ? new c().k : -1; }
console.log(g(K), g(null));
let w: WeakMap<object, number> | null = new WeakMap();
const key = {};
w.set(key, 9);
console.log(w.get(key));
`)
}

// An empty array property with no context is never[], as tsc types it,
// and a for-of destructuring over elements of several shapes walks each
// by the iteration protocol.
func TestE2EEmptyArrayPropertyAndUnionDestructuring(t *testing.T) {
	assertSameAsNode(t, `const xs = [{ s: 1, ports: [] }, { s: 2, ports: [5] }];
console.log(xs.length, xs[1].ports.length, JSON.stringify(xs));
for (const [a, e] of [
  [["m"], { t: "x", ports: [] }],
  [["m", { d: 1 }], { t: "y", ports: [] }],
]) {
  console.log(JSON.stringify(a), JSON.stringify(e));
}
`)
}
