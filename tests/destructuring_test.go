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

func TestE2EDependentDestructuring(t *testing.T) {
	// The leaves of a pattern over a discriminated union narrow together: a
	// test of the discriminant leaf narrows the others (a const, a parameter
	// never reassigned, a callback's contextually typed one).
	assertSameAsNode(t, `
type S = { kind: 'a', v: number } | { kind: 'b', v: string }
function f({ kind, v }: S): string {
  if (kind === 'a') return "num " + (v + 1)
  return "str " + v.toUpperCase()
}
console.log(f({ kind: 'a', v: 41 }), f({ kind: 'b', v: 'x' }))
const r: IteratorResult<number, string> = { done: true, value: "end" }
const { done, value } = r
console.log(done, done ? value.length : value + 1)
async function* gen() { yield 5; }
gen().next().then(({ done, value }) => { if (!done) console.log(value * 2) })
`)
}

func TestE2EPatternParamDefaultedToBoxedValue(t *testing.T) {
	// A destructuring parameter defaulted to a value held in a box (not an
	// object literal) reads its keys from whatever is passed, the default
	// included: its getter runs, and a key it lacks is undefined.
	assertOutput(t, `
const p: any = Object.defineProperty({}, 'x', { get() { console.log('get'); return 7 } })
function f({ x } = p) { console.log(x) }
f()
f({ x: 3 })
function g({ y, z } = p) { console.log(y, z) }
g()
`, "get\n7\n3\nundefined undefined")
}

// Names a top-level destructuring binds are module globals a function
// declaration reads and writes (ADR-01948).
func TestE2ETopLevelPatternBindingsReadByFunctions(t *testing.T) {
	got := compileAndRun(t, `const { x, y } = { x: 1, y: "two" };
const [a, b = 5, ...rest] = [1, undefined, 3, 4];
let { m, n: { k } } = { m: [1, 2], n: { k: true } };
const { o = null }: { o?: number | null } = {};
function f() { return [x, y, a, b, rest, m, k, o]; }
function bump() { m.push(9); k = !k; }
console.log(f()); bump(); console.log(m, k, f());
class C { g() { return x + (a ?? 0); } }
console.log(new C().g());
const [p = 7, q]: (number | null | undefined)[] = [undefined, null];
function main() { console.log(p, q); }
main();
`)
	want := "[ 1, 'two', 1, 5, [ 3, 4 ], [ 1, 2 ], true, null ]\n[ 1, 2, 9 ] false [ 1, 'two', 1, 5, [ 3, 4 ], [ 1, 2, 9 ], false, null ]\n2\n7 null"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// A destructuring default applies only to undefined: a null element of a
// `T | null | undefined` array keeps its null.
func TestE2EDestructuringDefaultSkipsNull(t *testing.T) {
	src := "function f({ a = 1, b = \"x\", c = [0] }: { a?: number | null; b?: string | null; c?: number[] | null }) { return [a, b, c]; }\nconsole.log(f({}), f({ a: null, b: null, c: null }), f({ a: undefined, b: \"y\", c: [3] }));\nconst [p = 5, q = 6] = [null, undefined] as (number | null | undefined)[];\nconsole.log(p, q);\nconst o: { x?: { y?: number[] | null } | null } = { x: { y: null } };\nconsole.log(o.x?.y, o.x?.y?.length, ({ x: null } as typeof o).x?.y);\nconst src = { k: null as string | null, m: undefined as number | undefined };\nconst cp = { ...src, z: 1 }; console.log(cp, JSON.stringify(cp), Object.keys(cp), Object.values(cp));\nfunction opt(x?: number[] | null) { return x === undefined ? \"U\" : x === null ? \"N\" : x.length; }\nconsole.log(opt(), opt(null), opt([1, 2]));\nconst xs2 = [null, undefined, 3] as (number | null | undefined)[];\nconst v = xs2[0]; console.log(v === null, v === undefined, xs2[1] === null, xs2[1] === undefined, typeof xs2[0], xs2.map(x => x ?? -1), xs2.filter(x => x === null).length);\n"
	want := "[ 1, 'x', [ 0 ] ] [ null, null, null ] [ 1, 'y', [ 3 ] ]\nnull 6\nnull undefined undefined\n{ k: null, m: undefined, z: 1 } {\"k\":null,\"z\":1} [ 'k', 'm', 'z' ] [ null, undefined, 1 ]\nU N 2\ntrue false false true object [ -1, -1, 3 ] 1"
	if got := compileAndRun(t, src); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestE2EArrayAssignNotIterable(t *testing.T) {
	assertSameAsNodeCLIJS(t, `
var s = Symbol(); try { 0, [,] = s; } catch (e) { console.log(e.constructor.name, e.message); }
var a; try { var n = 1; [a] = n; } catch (e) { console.log(e.message); }
try { [a] = null; } catch (e) { console.log(e.message); }
try { [a] = true; } catch (e) { console.log(e.message); }
try { [a] = 5n; } catch (e) { console.log(e.message); }
[a] = "xy"; console.log(a);
try { var z = null; [a] = z; } catch (e) { console.log(e.message); }
`)
}

func TestE2EArrayPatternOverGenerator(t *testing.T) {
	src := `
let first = 0, second = 0;
function* g() { try { first += 1; yield 5; second += 1; yield 6; } finally { console.log("closed"); } }
const [x] = g(); console.log(x, first, second);
const [,] = g(); console.log(first, second);
const [] = g(); console.log(first, second);
const [a, ...r] = g(); console.log(a, r, first, second);
let y: number | undefined; [y] = g(); console.log(y);
function* n() { yield 1; yield 2; }
const it = n(); const [p, q] = it; console.log(p, q);
`
	assertSameAsNode(t, src)
	assertSameAsNodeCLIJS(t, src)
}

// An array-typed pattern parameter whose default reads a captured variable.
func TestE2EArrayPatternParamCapturedDefault(t *testing.T) {
	assertSameAsNode(t, `
var values = [1, 2, 3];
var obj = { method([a, ...x] = values) { console.log(a, x.length, x !== values); } };
obj.method();
obj.method([9, 8]);
const f = ([q] = values) => q;
console.log(f(), f([7]));
`)
}

// An array pattern as the catch parameter, typed `any` or untyped JS.
func TestE2ECatchArrayPattern(t *testing.T) {
	assertSameAsNode(t, `
try { throw [1, 2]; } catch ([a, b]: any) { console.log(a, b); }
try { throw [3, 4]; } catch (e: any) { let [c, d] = e; console.log(c, d); }
`)
	assertJSFileSameAsNode(t, `
var d = 7;
try { throw [] } catch ([c = d, ...r]) { console.log(c, r.length); }
try { throw "xy"; } catch ([p, q]) { console.log(p, q); }
try { try { throw null; } catch ([z]) { console.log(z); } } catch (e) { console.log(e.constructor.name); }
`)
}

// A top-level destructured binding holding an object is one module
// global, so a function reads it (TypeScript: the field's static type;
// JavaScript: any, hoisted for a function declared above it).
func TestE2ETopLevelObjectPatternReadByFunction(t *testing.T) {
	t.Parallel()
	assertSameAsNode(t, `
const o = { pk: { a: 1 }, n: 2 };
const { pk, n } = o;
function f(): number { return pk.a + n; }
console.log(f());
`)
	assertSameAsNodeCLIJS(t, `
function use(k) { return [pk[k], sp[k]]; }
function fx() { return { pk: { a: 1 }, sp: { a: 2 } }; }
var { pk, sp } = fx();
console.log(use('a'));
`)
}

// An object literal's empty-string key is the property "", in both lanes
// (a function returning one is typed, not rejected).
func TestE2EObjectLiteralEmptyKey(t *testing.T) {
	t.Parallel()
	assertSameAsNode(t, `
const o = { "": 1, a: 2 };
console.log(o[""], Object.keys(o), o);
const f = () => ({ "": { x: 1 } });
console.log(f()[""].x);
`)
	assertSameAsNodeCLIJS(t, `
const tests = [
  ["a", function() { return { args: [{ "": { fn: function() {}, g: 0 } }] }; }],
  ["b", function() { return { args: [{ x: { fn: function() {}, "": 0 } }] }; }],
];
for (const [n, f] of tests) console.log(n, Object.keys(f().args[0]), Object.keys(Object.values(f().args[0])[0]));
`)
}
