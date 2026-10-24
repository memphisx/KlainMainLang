package tests

import "testing"

func TestE2EObjectPatternInheritedKey(t *testing.T) {
	// A key the object's layout does not hold is read as any property
	// read is, through its prototype (ADR-01765).
	assertSameAsNode(t, `
class P { x = 1; }
const { constructor } = new P();
const e = new TypeError("m");
const { constructor: k, message } = e;
Promise.reject(new RangeError("r")).then(null, ({ constructor: c, message: m }: Error) => console.log(c === RangeError, m));
console.log(constructor === P, k === TypeError, message);
`)
}

func TestE2EArrayPatternOverIterables(t *testing.T) {
	// An array pattern over a Set, a Map, a tuple element and an iterable
	// loop element (ADR-01766).
	assertSameAsNode(t, `
const s = new Set<string>(["a", "b"]);
const [p, q] = s;
const m = new Map<string, number>([["k", 1]]);
const [[k, v]] = m;
const es: [string, number][] = [];
let msg = "";
try { const [[x, y]] = es; msg = x + y; } catch (e) { msg = (e as Error).constructor.name; }
const xs = [new Set([1, 2]), new Map([[3, 4]])];
for (const e of xs) console.log([...e], e.size);
for (const [a, b] of [new Set([5, 6])]) console.log(a, b);
function* g(ps: [string, number][]) { for (const [n, c] of ps) yield n + c; }
async function* h() { for await (const [z] of [new Set(["s"])]) yield z; }
(async () => { for await (const w of h()) console.log(w); })();
console.log(p, q, k, v, msg, [...g([["a", 1]])]);
`)
}

// An array destructuring assignment's value is its right-hand side, which
// is evaluated once; a `var` initialized with null that a function later
// assigns holds whatever is stored.
func TestE2EArrayDestructAssignValue(t *testing.T) {
	assertSameAsNode(t, `
let a = 0, b = 0, evals = 0;
const src = [1, 2, 3];
function get() { evals++; return src; }
const r = [a, b] = get();
console.log(r === src, a, b, evals);
let seen: any = null;
function remember(v: any) { seen = v; }
remember({ k: 1 });
console.log(seen.k);
`)
}

// A destructuring declaration's pattern may carry a type annotation: the
// bindings are typed from it and the initializer is checked against it.
func TestE2EAnnotatedDestructuringDeclaration(t *testing.T) {
	assertSameAsNode(t, `
interface P { x: number; y: string }
const src: any = { x: 3, y: "s" };
const { x, y }: P = src;
console.log(x + 1, y.toUpperCase());
const [m, n]: [number, string] = JSON.parse('[4,"t"]');
console.log(m * 2, n.length);
const { a, b }: { a: number; b: string } = { a: 1, b: "x" };
console.log(a, b);
`)
	mustCompileError(t, `const { a }: { a: number } = { a: "x" };`, "type 'string' is not assignable to type 'number'")
	mustCompileError(t, `const [b]: [string] = [1];`, "type 'number' is not assignable to type 'string'")
}

// An array pattern reading past a tuple's end is rejected as tsc rejects
// it (TS2493), in a declaration, an assignment, a parameter and a for-of.
func TestE2EArrayPatternPastTupleEnd(t *testing.T) {
	for _, src := range []string{
		"let a = 0, b = 0;\n[a, b] = [9];",
		"const [c, d] = [9];",
		"const t: [number] = [1];\nconst [e, f] = t;",
		"const [g, , h] = [1, 2];",
		"const [e1, [f1, g1]] = [1, [2]];",
		"function fn([m, n]: [number]) {}",
		"for (const [o, p] of [[1]] as [number][]) {}",
	} {
		mustCompileError(t, src, "has no element at index")
	}
	assertSameAsNode(t, `
const [a1, b1 = 1] = [9];
const [c1, ...r] = [9];
const [d1, , ] = [9];
const arr: number[] = [];
const [k, l] = arr;
console.log(a1, b1, c1, r.length, d1, k, l);
`)
}

func TestE2EUnannotatedPatternParams(t *testing.T) {
	// An unannotated destructured parameter takes the type its pattern
	// gives it (`[a = 1]` is `[(number | undefined)?]`), in functions,
	// methods, private methods and constructors (ADR-01929).
	assertSameAsNode(t, `
function f([a = 1, b = "s"]) { return a + b; }
console.log(f([]), f([7]));
class C {
  constructor([n = 3]) { console.log(n); }
  m([gen = function* () {}, x = 2]) { return gen.name + x; }
  #p([y = 4]) { return y; }
  go() { return this.#p([]); }
}
const c = new C([]);
console.log(c.m([]), c.go());
var D = class {
  #method([g = function* () {}, xg = function* x() {}]) { console.log(g.name, xg.name); }
  get method() { return this.#method; }
};
new D().method([]);
`)
}
