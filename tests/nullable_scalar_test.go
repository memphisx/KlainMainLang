package tests

import "testing"

// A `T | undefined` read where flow proves it present is its value, in a
// conditional branch and behind `&&` as in an if; `a || b` is absent only
// when b can be (ADR-01439).
func TestE2EPresenceNarrowedReads(t *testing.T) {
	assertSameAsNode(t, `
let dflt = 10;
function setMax(n?: number): number {
  const value = n === undefined ? dflt : n;
  dflt = value;
  return n !== undefined && n > 2 ? value * 2 : -value;
}
console.log(setMax(), setMax(3), setMax(1), dflt);
function pick(a?: string, b?: string): string {
  const s = a || b || "";
  return s.length > 0 ? s.toUpperCase() : "(none)";
}
console.log(pick(), pick(undefined, "b"), pick("a"));
const one: number[] = [1];
let [x, y] = one;
console.log(x, y);
`)
}

// A nullable instance or function reference the checker narrows present
// reads as present (`const base = this.base` under a null check); an
// array-pattern position past the source's end stays absent, and an absent
// reference converts to "null" or "undefined" (ADR-01442).
func TestE2ENarrowedNullableRefs(t *testing.T) {
	assertSameAsNode(t, `
class R { scheme = 'file'; host = 'h'; }
class P {
  base: R | null = null;
  cb: (() => void) | null = null;
  run(): string {
    let out = '';
    if (this.base !== null && this.base.scheme === 'file') {
      const base = this.base;
      out += base.host + ':' + String(base) + ';';
    }
    if (this.cb !== null) { const cb = this.cb; cb(); out += typeof cb; }
    return out;
  }
}
const p = new P();
console.log(JSON.stringify(p.run()));
p.base = new R(); p.cb = () => console.log('called');
console.log(p.run());
const xs: R[] = [new R()];
const [a, b] = xs;
console.log(a.host, b === undefined, String(b), `+"`${b}`"+`, b + '');
const [c, d] = [new R()] as R[];
console.log(c.host, d === undefined);
let u: R | undefined = Math.random() > 2 ? new R() : undefined;
let n: R | null = Math.random() > 2 ? new R() : null;
console.log(String(u), String(n), u + '', n + '');
`)
}

// A nullable property the checker narrows present reads as present
// (`[this.#cur]` under `this.#cur !== null`) (ADR-01460).
func TestE2ENarrowedNullableMembers(t *testing.T) {
	assertSameAsNode(t, `
class T { name = 't'; }
class D {
  #cur: T | null = null;
  cb: (() => string) | null = null;
  path(): T[] { return this.#cur !== null ? [this.#cur] : []; }
  run(): string { if (this.cb !== null) return this.cb() + typeof this.cb; return 'none'; }
  set(t: T) { this.#cur = t; }
}
const d = new D();
console.log(d.path().length, d.run());
d.set(new T()); d.cb = () => 'x';
console.log(d.path()[0].name, d.run(), String(d.path()[0]));
function f(x: 'x' | 'y' | undefined, y: 'p' | null) { return x ?? y; }
console.log(f('x', null), f(undefined, null), f(undefined, 'p'));
`)
}

// A `null | undefined` value keeps which one it holds (ADR-01948).
func TestE2ENullOrUndefinedValue(t *testing.T) {
	got := compileAndRun(t, `let v: null | undefined = null; console.log(v); v = undefined; console.log(v, v === undefined);
function g(k: boolean): null | undefined { return k ? null : undefined; } console.log(g(true), g(false));
`)
	want := "null\nundefined true\nnull undefined"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// `[undefined, null]` keeps its null element (ADR-01948).
func TestE2ENullishLiteralArrayKeepsNull(t *testing.T) {
	got := compileAndRun(t, `const a = [undefined, null];
console.log(a, a[1], a[0]);
const b = [1, undefined, null];
console.log(b, b[2]);
`)
	want := "[ undefined, null ] null undefined\n[ 1, undefined, null ] null"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// A box holding null unboxes into a three-state scalar as null (ADR-01948).
func TestE2EBoxIntoThreeStateScalar(t *testing.T) {
	got := compileAndRun(t, `const a: any = null; const b: any = undefined; const t: number | null | undefined = a; const u: boolean | null | undefined = b; const w: boolean | null | undefined = a; console.log(t, u, w);
`)
	want := "null undefined null"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// Spreading a generator of nullable numbers (ADR-01948).
func TestE2EGeneratorNullableYieldSpread(t *testing.T) {
	got := compileAndRun(t, `function* gen(): Generator<number | null> { yield 1; yield null; yield 3; }
console.log([...gen()]);
for (const v of gen()) console.log(v);
const it = gen(); console.log(it.next(), it.next());
console.log(Array.from(gen()));
`)
	want := "[ 1, null, 3 ]\n1\nnull\n3\n{ value: 1, done: false } { value: null, done: false }\n[ 1, null, 3 ]"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// JSON.parse into an array or field of a primitive union (ADR-01948).
func TestE2EJSONParsePrimitiveUnion(t *testing.T) {
	got := compileAndRun(t, `const a = JSON.parse("[1,null,3]") as (number | null)[]; console.log(a, a[1] === null);
const b: (string | number)[] = JSON.parse("[\"x\", 2]"); console.log(b, typeof b[0], typeof b[1]);
const o: { v: number | null; w: boolean | null } = JSON.parse("{\"v\":null,\"w\":true}"); console.log(o);
`)
	want := "[ 1, null, 3 ] true\n[ 'x', 2 ] string number\n{ v: null, w: true }"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// Nullable scalars through promises, generators, statics, defaults,
// destructuring, records and sorting (ADR-01948).
func TestE2ENullableScalarsAcrossPositions(t *testing.T) {
	got := compileAndRun(t, `async function af(k: number): Promise<number | null> { return k ? k : null; }
function* gen(): Generator<number | null> { yield 1; yield null; yield 3; }
class St { static v: number | null = null; static w?: boolean; }
function dflt(a: number | null = null, b?: number) { return [a, b]; }
const { x = 5, y }: { x?: number | null; y: number | undefined } = { x: null, y: undefined };
const [p = 7, q]: (number | null | undefined)[] = [undefined, null];
async function main() {
  console.log(await af(0), await af(2), [...gen()], St.v, St.w, dflt(), dflt(null, 2), x, y, p, q);
  const ps = await Promise.all([af(0), af(1)]); console.log(ps);
  const objs: { n: number | null }[] = [{ n: 1 }, { n: null }]; console.log(objs.map(o => o.n), objs.filter(o => o.n !== null).length);
  const r: Record<string, number | null> = { a: null, b: 2 }; console.log(r, Object.values(r), Object.entries(r));
  const opt = (v?: number | null) => v === undefined ? "U" : v === null ? "N" : v; console.log(opt(), opt(null), opt(0));
  let acc: boolean | null = null; for (const b of [true, false]) acc = acc === null ? b : acc && b; console.log(acc);
  console.log(JSON.stringify({ a: null as number | null, b: undefined as number | undefined }), JSON.parse("[1,null]") as (number | null)[]);
  const nn: (number | null)[] = [3, null, 1]; nn.sort((a, b) => (a ?? -1) - (b ?? -1)); console.log(nn, nn.includes(null), nn.indexOf(null), nn.reverse());
}
main();
`)
	want := "null 2 [ 1, null, 3 ] null undefined [ null, undefined ] [ null, 2 ] null undefined 7 null\n[ null, 1 ]\n[ 1, null ] 1\n{ a: null, b: 2 } [ null, 2 ] [ [ 'a', null ], [ 'b', 2 ] ]\nU N 0\nfalse\n{\"a\":null} [ 1, null ]\n[ 3, 1, null ] true 0 [ 3, 1, null ]"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// An array typed `T[] | null | undefined` keeps null and undefined apart
// through declarations, returns, assignments and ternaries, as strings,
// objects and numbers do.
func TestE2EThreeStateArrayNullVsUndefined(t *testing.T) {
	src := "class P { n = 1; }\nfunction pick(k: number): string | null | undefined { return k === 0 ? null : k === 1 ? undefined : \"s\"; }\nfunction po(k: number): P | null | undefined { return k === 0 ? null : k === 1 ? undefined : new P(); }\nfunction pn(k: number): number | null | undefined { return k === 0 ? null : k === 1 ? undefined : 5; }\nfunction pa(k: number): number[] | null | undefined { return k === 0 ? null : k === 1 ? undefined : [1]; }\nfor (const k of [0, 1, 2]) {\n  const s = pick(k), o = po(k), n = pn(k), a = pa(k);\n  console.log(s === null, s === undefined, o === null, o === undefined, n === null, n === undefined, a === null, a === undefined, s, o, n, a);\n  console.log(s == null, typeof s, typeof o, typeof n, typeof a, String(s), JSON.stringify({ s, o, n, a }));\n}\nfunction f2(): number[] | null | undefined { return null; }\nfunction g2(k: boolean): number[] | null | undefined { return k ? null : undefined; }\nfunction h2(k: number): number[] | null | undefined { if (k === 0) return null; if (k === 1) return undefined; return [1]; }\nconsole.log(f2(), g2(true), g2(false), h2(0), h2(1), h2(2), f2() === null);\nlet z2: number[] | null | undefined = [2]; z2 = null; console.log(z2); const o2: { a: number[] | null | undefined } = { a: null }; console.log(o2, o2.a === null);\n"
	want := "true false true false true false true false null null null null\ntrue object object object object null {\"s\":null,\"o\":null,\"n\":null,\"a\":null}\nfalse true false true false true false true undefined undefined undefined undefined\ntrue undefined undefined undefined undefined undefined {}\nfalse false false false false false false false s P { n: 1 } 5 [ 1 ]\nfalse string object number object s {\"s\":\"s\",\"o\":{\"n\":1},\"n\":5,\"a\":[1]}\nnull null undefined null undefined [ 1 ] true\nnull\n{ a: null } true"
	if got := compileAndRun(t, src); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

// Three-state values as parameters, Map values, fields, array elements and
// through `??=` on an array variable.
func TestE2EThreeStateValuesAcrossPositions(t *testing.T) {
	src := "type A = number[] | null | undefined;\nfunction show(x: A, y: string | null | undefined = null): string { return `${x === null}/${x === undefined}/${y === null}/${y}`; }\nconsole.log(show(null), show(undefined), show([1], undefined), show(null, \"z\"));\nconst m = new Map<string, A>(); m.set(\"n\", null); m.set(\"u\", undefined);\nconsole.log(m.get(\"n\") === null, m.get(\"u\") === undefined, m.get(\"zz\") === undefined, m);\nclass C { a: A = null; b?: number[] | null; }\nconst c = new C(); console.log(c.a === null, c.b === undefined, c); c.b = null; console.log(c.b === null, c);\nconst arr: A[] = [null, undefined, [2]]; console.log(arr, arr.map(v => v === null));\nconst o = { v: null as A }; console.log(o.v?.length, o.v ?? \"dflt\", JSON.stringify(arr));\nlet q: A; q ??= null; console.log(q);\nconst ms = new Map<string, string | null | undefined>(); ms.set(\"n\", null); ms.set(\"u\", undefined);\nconst mo = new Map<string, { a: number } | null | undefined>(); mo.set(\"n\", null); mo.set(\"u\", undefined);\nconst mn = new Map<string, number | null | undefined>(); mn.set(\"n\", null); mn.set(\"u\", undefined);\nconsole.log(ms, mo, mn, ms.get(\"n\") === null, mo.get(\"n\") === null, mn.get(\"n\") === null, mn.get(\"u\") === undefined);\n"
	want := "true/false/true/null false/true/true/null false/false/true/null true/false/false/z\ntrue true true Map(2) { 'n' => null, 'u' => undefined }\ntrue true C { a: null, b: undefined }\ntrue C { a: null, b: null }\n[ null, undefined, [ 2 ] ] [ true, false, false ]\nundefined dflt [null,null,[2]]\nnull\nMap(2) { 'n' => null, 'u' => undefined } Map(2) { 'n' => null, 'u' => undefined } Map(2) { 'n' => null, 'u' => undefined } true true true true"
	if got := compileAndRun(t, src); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

// A three-state array's null through constructor, method and static method
// arguments, generic classes and functions, a Promise, JSON.parse and an
// accessor pair.
func TestE2EThreeStateArrayThroughCalls(t *testing.T) {
	src := "class Box<T> { v: T; constructor(v: T) { this.v = v; } get(): T { return this.v; } }\nconst b1 = new Box<number | null | undefined>(null), b2 = new Box<string[] | null | undefined>(null);\nconsole.log(b1.get(), b2.get(), b1.get() === null, b2.get() === null, b1, b2);\nfunction id<T>(x: T): T { return x; }\nconsole.log(id<number | null | undefined>(null), id<number[] | null | undefined>(null), id<number[] | null | undefined>(undefined));\nasync function af(): Promise<number[] | null | undefined> { return null; }\naf().then(v => console.log(\"p\", v, v === null));\nconst parsed = JSON.parse('{\"a\":null,\"b\":[1,null]}') as { a?: number[] | null; b: (number | null)[] };\nconsole.log(parsed.a === null, parsed.a === undefined, parsed.b, parsed);\nclass G { private _x: number[] | null | undefined = null; get x() { return this._x; } set x(v) { this._x = v; } }\nconst g = new G(); console.log(g.x); g.x = undefined; console.log(g.x); g.x = null; console.log(g.x === null);\nclass M { f(v: number[] | null | undefined) { return v === null; } static s(v: number[] | null | undefined) { return v === null; } }\nconsole.log(new M().f(null), M.s(null), new M().f(undefined));\n"
	want := "null null true true Box { v: null } Box { v: null }\nnull null undefined\ntrue false [ 1, null ] { a: null, b: [ 1, null ] }\nnull\nundefined\ntrue\ntrue true false\np null true"
	if got := compileAndRun(t, src); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

// Three-state elements through switch, string conversion, equality and the
// array search and join methods: join renders null and undefined as "".
func TestE2EThreeStateElementsOperators(t *testing.T) {
	src := "const vals: (number[] | null | undefined)[] = [null, undefined, [1]];\nfor (const v of vals) {\n  switch (v) { case null: console.log(\"is null\"); break; case undefined: console.log(\"is undef\"); break; default: console.log(\"arr\", v.length); }\n  console.log(\"s:\" + v, `${v}`, String(v), v == null, v == undefined, v != null, !v, v ? 1 : 0, typeof v);\n}\nconsole.log(vals.indexOf(null), vals.indexOf(undefined), vals.includes(null), vals.findIndex(x => x === undefined), vals.filter(x => x != null).length);\nconsole.log(vals.map(x => x?.length ?? (x === null ? \"N\" : \"U\")), vals.join(\"|\"), vals.toString(), vals.flat());\nconst strs: (string | null | undefined)[] = [null, undefined, \"a\"];\nconsole.log(strs.join(\"|\"), strs.indexOf(null), strs.indexOf(undefined), strs.sort(), JSON.stringify(strs));\n"
	want := "is null\ns:null null null true true false true 0 object\nis undef\ns:undefined undefined undefined true true false true 0 undefined\narr 1\ns:1 1 1 false false true false 1 object\n0 1 true 1 1\n[ 'N', 'U', 1 ] ||1 ,,1 [ null, undefined, 1 ]\n||a 0 1 [ 'a', null, undefined ] [\"a\",null,null]"
	if got := compileAndRun(t, src); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

// Three-state values in a Record, Object.entries, structuredClone, spreads,
// nested objects and through a `keyof` index.
func TestE2EThreeStateInRecordsAndSpreads(t *testing.T) {
	src := "const r: Record<string, number[] | null | undefined> = { a: null, b: undefined, c: [1] };\nconsole.log(r, Object.entries(r), Object.values(r), JSON.stringify(r), \"b\" in r, r.zz === undefined);\nconst cl = structuredClone({ x: null as number[] | null, y: [null, 2] as (number | null)[] });\nconsole.log(cl, cl.x === null);\ntype O = { n: number | null | undefined; s: string | null | undefined; a: number[] | null | undefined };\nconst o1: O = { n: null, s: null, a: null }; const o2: O = { ...o1, n: undefined };\nconsole.log(o1, o2, { ...o2, ...o1 }, Object.assign({}, o1, { a: undefined }));\nconst nest = { inner: { a: null as number[] | null | undefined } }; nest.inner.a = undefined; console.log(nest); nest.inner.a = null; console.log(nest);\nconst keys: (keyof O)[] = [\"n\", \"s\", \"a\"]; for (const k of keys) console.log(k, o1[k] === null, o2[k] === undefined);\n"
	want := "{ a: null, b: undefined, c: [ 1 ] } [ [ 'a', null ], [ 'b', undefined ], [ 'c', [ 1 ] ] ] [ null, undefined, [ 1 ] ] {\"a\":null,\"c\":[1]} true true\n{ x: null, y: [ null, 2 ] } true\n{ n: null, s: null, a: null } { n: undefined, s: null, a: null } { n: null, s: null, a: null } { n: null, s: null, a: undefined }\n{ inner: { a: undefined } }\n{ inner: { a: null } }\nn true true\ns true false\na true false"
	if got := compileAndRun(t, src); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

// A function value that may be null or undefined keeps the two apart: in
// arrays, fields, parameters, Maps, optional calls and `??`.
func TestE2EThreeStateFunctionValues(t *testing.T) {
	src := "type F = ((x: number) => number) | null | undefined;\nconst fs: F[] = [null, undefined, x => x * 2];\nfor (const f of fs) console.log(f === null, f === undefined, f?.(3), typeof f, f ?? \"none\");\nclass H { cb: F = null; run(v: number) { return this.cb?.(v) ?? (this.cb === null ? \"N\" : \"U\"); } }\nconst h = new H(); console.log(h.run(1)); h.cb = undefined; console.log(h.run(1)); h.cb = v => v + 1; console.log(h.run(1));\nfunction call(f?: (() => string) | null) { return f === undefined ? \"omitted\" : f === null ? \"null\" : f(); }\nconsole.log(call(), call(null), call(() => \"ran\"));\nconst m = new Map<string, F>([[\"a\", null], [\"b\", undefined]]); console.log(m.get(\"a\") === null, m.get(\"b\") === undefined, m.size);\n"
	want := "true false undefined object none\nfalse true undefined undefined none\nfalse false 6 function [Function (anonymous)]\nN\nU\n2\nomitted null ran\ntrue true 2"
	if got := compileAndRun(t, src); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

// Nullable callbacks through sort, JSON.stringify, timers, Promise.then and
// an accessor-held handler.
func TestE2EThreeStateFunctionCallbacks(t *testing.T) {
	src := "const cmp: ((a: number, b: number) => number) | null | undefined = undefined;\nconsole.log([3, 1, 2].sort(cmp ?? undefined), [3, 1, 2].sort((a, b) => b - a));\nconst rep: ((k: string, v: unknown) => unknown) | null = null;\nconsole.log(JSON.stringify({ a: 1 }, rep), JSON.stringify({ a: 1 }, null, 2).length);\nlet onDone: (() => void) | null | undefined = null;\nsetTimeout(() => { onDone?.(); console.log(\"tick\", onDone === null); onDone = () => console.log(\"done cb\"); onDone(); }, 1);\nconst p = Promise.resolve(4).then(undefined, null).then(v => console.log(\"then\", v));\nclass Emitter { private h: ((s: string) => void) | null | undefined; on(f: (s: string) => void) { this.h = f; } fire(s: string) { if (this.h) this.h(s); else console.log(\"no handler\", this.h === undefined); } }\nconst em = new Emitter(); em.fire(\"x\"); em.on(s => console.log(\"got\", s)); em.fire(\"y\");\nconst arr: ((() => number) | null | undefined)[] = [() => 1, null, undefined]; console.log(arr.map(f => (f ? f() : f)), arr.filter(Boolean).length);\n"
	want := "[ 1, 2, 3 ] [ 3, 2, 1 ]\n{\"a\":1} 12\nno handler true\ngot y\n[ 1, null, undefined ] 1\nthen 4\ntick true\ndone cb"
	if got := compileAndRun(t, src); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

// An undefined comparator sorts in the default order; a null one throws the
// TypeError Node throws, through a cast too.
func TestE2ESortComparatorAbsent(t *testing.T) {
	src := "const c1: ((a: number, b: number) => number) | null | undefined = undefined;\nconst c2: ((a: number, b: number) => number) | null | undefined = (a, b) => b - a;\nconst c3: ((a: number, b: number) => number) | null = null;\nconsole.log([3, 1, 2].sort(c1), [3, 1, 2].sort(c2), [3, 1, 2].toSorted(c1), [10, 9, 1].sort(undefined));\ntry { [2, 1].sort(c3 as unknown as undefined); } catch (e) { console.log(String(e)); }\ntry { [2, 1].toSorted(c3 as unknown as undefined); } catch (e) { console.log(String(e)); }\n"
	want := "[ 1, 2, 3 ] [ 3, 2, 1 ] [ 1, 2, 3 ] [ 1, 10, 9 ]\nTypeError: The comparison function must be either a function or undefined: null\nTypeError: The comparison function must be either a function or undefined: null"
	if got := compileAndRun(t, src); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

// A three-state function narrowed by truthiness inside a callback keeps
// which absence it holds.
func TestE2EThreeStateFunctionNarrowedInCallback(t *testing.T) {
	src := "const arr: ((() => number) | null | undefined)[] = [() => 1, null, undefined];\nconsole.log(arr.map(f => f));\nconsole.log(arr.map(f => f === undefined));\nconsole.log(arr.map(f => (f ? 1 : f)));\nconsole.log(arr.map((f): number | null | undefined => (f ? f() : f)));\narr.forEach(f => console.log(f ? f() : f));\n"
	want := "[ [Function (anonymous)], null, undefined ]\n[ false, false, true ]\n[ 1, null, undefined ]\n[ 1, null, undefined ]\n1\nnull\nundefined"
	if got := compileAndRun(t, src); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

// Date, bigint, RegExp, tuples, Maps and Sets that may be null or undefined
// keep the two apart, through a generic `T | null | undefined` too.
func TestE2EThreeStateOtherPointerKinds(t *testing.T) {
	src := "const ds: (Date | null | undefined)[] = [null, undefined, new Date(0)];\nconst bs: (bigint | null | undefined)[] = [null, undefined, 5n];\nconst rs: (RegExp | null | undefined)[] = [null, undefined, /x/];\nconst ts: ([number, string] | null | undefined)[] = [null, undefined, [1, \"a\"]];\nconst ms: (Map<string, number> | null | undefined)[] = [null, undefined, new Map([[\"k\", 1]])];\nconst ss: (Set<number> | null | undefined)[] = [null, undefined, new Set([1])];\nconsole.log(ds.map(d => d === null), bs.map(b => b === null), rs.map(r => r === null), ts.map(t => t === null), ms.map(m => m === null), ss.map(s => s === null));\nconsole.log(ds, bs, rs, ts, ms, ss);\nfunction pick<T>(xs: (T | null | undefined)[], i: number): T | null | undefined { return xs[i]; }\nconsole.log(pick(ds, 0), pick(bs, 0), pick(ts, 0), pick(ds, 1), pick(bs, 1));\nlet d1: Date | null | undefined = null; let b1: bigint | null | undefined = null; let t1: [number, string] | null | undefined = null;\nconsole.log(d1, b1, t1, d1 === null, b1 === null, t1 === null); d1 = undefined; b1 = undefined; t1 = undefined; console.log(d1, b1, t1);\n"
	want := "[ true, false, false ] [ true, false, false ] [ true, false, false ] [ true, false, false ] [ true, false, false ] [ true, false, false ]\n[ null, undefined, 1970-01-01T00:00:00.000Z ] [ null, undefined, 5n ] [ null, undefined, /x/ ] [ null, undefined, [ 1, 'a' ] ] [ null, undefined, Map(1) { 'k' => 1 } ] [ null, undefined, Set(1) { 1 } ]\nnull null null undefined undefined\nnull null null true true true\nundefined undefined undefined"
	if got := compileAndRun(t, src); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

// A nullable bigint compares with null and undefined as JS does: `==`
// matches either absence, `===` only the one it holds.
func TestE2ENullableBigIntComparisons(t *testing.T) {
	src := "function t(a: bigint | null, b: bigint | undefined, c: bigint | null | undefined) {\n  console.log(a === null, a === undefined, a == undefined, a != null, b === null, b === undefined, b == null, c === null, c === undefined, c == null, c !== null);\n}\nt(null, undefined, null); t(null, undefined, undefined); t(1n, 2n, 3n);\n"
	want := "true false true false false true true true false true false\ntrue false true false false true true false true true true\nfalse false false true false false false false false false true"
	if got := compileAndRun(t, src); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

// A three-state Date in locals, returns, arrays, JSON and Maps.
func TestE2EThreeStateDate(t *testing.T) {
	src := "const ds: (Date | null | undefined)[] = [null, undefined, new Date(0)];\nlet d1: Date | null | undefined = null; console.log(d1, d1 === null); d1 = undefined; console.log(d1, d1 === undefined);\nfunction pd(k: number): Date | null | undefined { return k === 0 ? null : k === 1 ? undefined : new Date(0); }\nconsole.log(pd(0), pd(1), pd(2), ds, ds.map(d => d === null), JSON.stringify(ds), ds.map(d => d?.getTime()));\nconst m = new Map<string, Date | null | undefined>([[\"n\", null]]); console.log(m.get(\"n\") === null, m);\n"
	want := "null true\nundefined true\nnull undefined 1970-01-01T00:00:00.000Z [ null, undefined, 1970-01-01T00:00:00.000Z ] [ true, false, false ] [null,null,\"1970-01-01T00:00:00.000Z\"] [ undefined, undefined, 0 ]\ntrue Map(1) { 'n' => null }"
	if got := compileAndRun(t, src); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

// An absent Date serializes as null in arrays and objects.
func TestE2EJSONStringifyNullableDate(t *testing.T) {
	src := "const a: (Date | null)[] = [null, new Date(0)]; const b: (Date | undefined)[] = [undefined]; const o: { d: Date | null; u?: Date } = { d: null };\nconsole.log(JSON.stringify(a), JSON.stringify(b), JSON.stringify(o));\n"
	want := "[null,\"1970-01-01T00:00:00.000Z\"] [null] {\"d\":null}"
	if got := compileAndRun(t, src); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

// A generic `T | null | undefined` keeps null and undefined apart for each
// instantiation.
func TestE2EGenericThreeStateReturn(t *testing.T) {
	src := "function pick<T>(xs: (T | null | undefined)[], i: number): T | null | undefined { return xs[i]; }\nconst bs: (bigint | null | undefined)[] = [null, undefined, 5n];\nconst x = pick(bs, 0); console.log(x, x === null, bs[0]);\nconst ns: (number | null | undefined)[] = [null]; console.log(pick(ns, 0));\n"
	want := "null true null\nnull"
	if got := compileAndRun(t, src); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

// An array literal of a `T | null` and a `T | undefined` value holds both
// absences.
func TestE2EArrayLiteralMergesAbsences(t *testing.T) {
	src := "function f(a: string | null, b: string | undefined, c: number | null, d: number | undefined, e: { k: 1 } | null, g: { k: 1 } | undefined) { console.log([a, b], [c, d], [e, g], [a, a], [d, d]); }\nf(null, undefined, null, undefined, null, undefined);\n"
	want := "[ null, undefined ] [ null, undefined ] [ null, undefined ] [ null, null ] [ undefined, undefined ]"
	if got := compileAndRun(t, src); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

// console.log of a nullable Map, Set or RegExp, alone or in an array.
func TestE2EInspectNullableHostCollections(t *testing.T) {
	src := "const m1: (Map<string, number> | null)[] = [null, new Map([[\"k\", 1]])];\nconst s1: (Set<number> | undefined)[] = [undefined, new Set([1])];\nconst r1: (RegExp | null)[] = [null, /x/];\nconst r2: (RegExp | undefined)[] = [undefined, /y/];\nfunction show(r: RegExp | null) { console.log(r); }\nconsole.log(m1, s1, r1, r2, r1[1], r2[0]); show(/z/); show(null);\n"
	want := "[ null, Map(1) { 'k' => 1 } ] [ undefined, Set(1) { 1 } ] [ null, /x/ ] [ undefined, /y/ ] /x/ undefined\n/z/\nnull"
	if got := compileAndRun(t, src); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

// Three-state values through await, Promise.all, allSettled, generators and
// class fields.
func TestE2EThreeStateThroughPromises(t *testing.T) {
	src := "async function s(k: number): Promise<string | null | undefined> { return k === 0 ? null : k === 1 ? undefined : \"x\"; }\nasync function o(k: number): Promise<{ a: number } | null | undefined> { return k === 0 ? null : k === 1 ? undefined : { a: 1 }; }\nasync function n(k: number): Promise<number | null | undefined> { return k === 0 ? null : k === 1 ? undefined : 2; }\nasync function f(k: number): Promise<(() => number) | null | undefined> { return k === 0 ? null : k === 1 ? undefined : () => 3; }\nasync function main() {\n  for (const k of [0, 1, 2]) {\n    const [a, b, c, d] = [await s(k), await o(k), await n(k), await f(k)];\n    console.log(a, b, c, typeof d, a === null, b === null, c === null, d === null);\n  }\n  console.log(await Promise.all([s(0), s(1)]), await Promise.all([n(0), n(1), n(2)]), await Promise.all([o(0), o(1)]));\n  const settled = await Promise.allSettled([n(0), n(1)]); console.log(settled);\n}\nmain();\nfunction* g(): Generator<string | null | undefined> { yield null; yield undefined; yield \"z\"; }\nconsole.log([...g()], Array.from(g()).map(v => v === null));\nclass K { s: string | null | undefined = null; n: number | null | undefined = null; static z: number[] | null | undefined = null; }\nconsole.log(new K(), K.z, K.z === null);\nasync function rej(): Promise<number | null> { throw new Error(\"no\"); }\nPromise.allSettled([rej(), n(2)]).then(r => console.log(r.map(x => x.status), JSON.stringify(r.map(x => x.status === \"fulfilled\" ? x.value : 0))));\n"
	want := "[ null, undefined, 'z' ] [ true, false, false ]\nK { s: null, n: null } null true\n[ 'rejected', 'fulfilled' ] [0,2]\nnull null null object true true true true\nundefined undefined undefined undefined false false false false\nx { a: 1 } 2 function false false false false\n[ null, undefined ] [ null, undefined, 2 ] [ null, undefined ]\n[\n  { status: 'fulfilled', value: null },\n  { status: 'fulfilled', value: undefined }\n]"
	if got := compileAndRun(t, src); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

// A conditional of null, undefined and a function, Map, Set, bigint or Date
// value keeps each absence.
func TestE2ETernaryThreeStatePointerKinds(t *testing.T) {
	src := "const g = () => 3;\nfor (const k of [0, 1, 2]) {\n  const d: (() => number) | null | undefined = k === 0 ? null : k === 1 ? undefined : g;\n  const e = k === 0 ? null : g;\n  const m: Map<string, number> | null | undefined = k === 0 ? null : k === 1 ? undefined : new Map();\n  const b: bigint | null | undefined = k === 0 ? null : k === 1 ? undefined : 7n;\n  const t: Date | null | undefined = k === 0 ? null : k === 1 ? undefined : new Date(0);\n  const s = k === 0 ? null : new Set([1]);\n  console.log(typeof d, d === null, typeof e, m, b, t, s, b === null, t === undefined);\n}\n"
	want := "object true object null null null null true false\nundefined false function undefined undefined undefined Set(1) { 1 } false true\nfunction false function Map(0) {} 7n 1970-01-01T00:00:00.000Z Set(1) { 1 } false false"
	if got := compileAndRun(t, src); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

// Optional record fields that may be null: functions, Maps, bigints, Dates
// and tuples, read through optional chains and spreads.
func TestE2EThreeStateOptionalRecordFields(t *testing.T) {
	src := "type R = { f?: (() => number) | null; m?: Map<string, number> | null; b?: bigint | null; d?: Date | null; t?: [number, string] | null };\nconst rs: R[] = [{}, { f: null, m: null, b: null, d: null, t: null }, { f: () => 1, m: new Map([[\"a\", 1]]), b: 2n, d: new Date(0), t: [1, \"x\"] }];\nfor (const r of rs) {\n  console.log(r, r.f === null, r.f === undefined, r.f?.(), r.m?.get(\"a\"), r.b ?? \"nb\", r.d?.getTime() ?? \"nd\", r.t?.[1]);\n  const c = { ...r }; console.log(c.b === null, c.m === undefined);\n}\n"
	want := "{} false true undefined undefined nb nd undefined\nfalse true\n{ f: null, m: null, b: null, d: null, t: null } true false undefined undefined nb nd undefined\ntrue false\n{\n  f: [Function: f],\n  m: Map(1) { 'a' => 1 },\n  b: 2n,\n  d: 1970-01-01T00:00:00.000Z,\n  t: [ 1, 'x' ]\n} false false 1 1 2n 0 x\nfalse false"
	if got := compileAndRun(t, src); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

// Three-state elements through search, filter, join, sort, switch, templates
// and Record entries.
func TestE2EThreeStateArrayMethods(t *testing.T) {
	src := "const ss: (string | null | undefined)[] = [\"b\", null, undefined, \"a\"];\nconst ns: (number | null | undefined)[] = [2, null, undefined, 1];\nconst os: ({ k: number } | null | undefined)[] = [{ k: 1 }, null, undefined];\nconsole.log(ss.indexOf(null), ss.indexOf(undefined), ss.includes(null), ns.includes(undefined), ns.indexOf(null), os.indexOf(null), os.lastIndexOf(undefined));\nconsole.log(ss.filter(Boolean), ns.filter(x => x != null), ss.find(x => x === undefined), ns.findIndex(x => x === null), os.some(o => o === undefined));\nconsole.log(`${ss[1]}-${ss[2]}-${ns[1]}-${ns[2]}`, ss.join(\"|\"), ns.join(\"|\"), String(ss), ns.toString());\nconsole.log([...ss].reverse(), ss.slice(1, 3), ns.concat([null]), ss.map(s => s?.toUpperCase()), ns.map(n => (n ?? -1) * 2));\nfor (const s of ss) { switch (s) { case null: console.log(\"N\"); break; case undefined: console.log(\"U\"); break; default: console.log(\"S\", s); } }\nconst rec: Record<string, number | null | undefined> = { a: null, b: undefined, c: 1 };\nconsole.log(Object.entries(rec), Object.values(rec), JSON.stringify(rec), \"b\" in rec, rec.a === null, rec.b === undefined);\nconsole.log(ns.sort(), ss.sort(), [3, null, 1, undefined].sort((a, b) => (a ?? 0) - (b ?? 0)));\n"
	want := "1 2 true true 1 1 2\n[ 'b', 'a' ] [ 2, 1 ] undefined 1 true\nnull-undefined-null-undefined b|||a 2|||1 b,,,a 2,,,1\n[ 'a', undefined, null, 'b' ] [ null, undefined ] [ 2, null, undefined, 1, null ] [ 'B', undefined, undefined, 'A' ] [ 4, -2, -2, 2 ]\nS b\nN\nU\nS a\n[ [ 'a', null ], [ 'b', undefined ], [ 'c', 1 ] ] [ null, undefined, 1 ] {\"a\":null,\"c\":1} true true true\n[ 1, 2, null, undefined ] [ 'a', 'b', null, undefined ] [ null, 1, 3, undefined ]"
	if got := compileAndRun(t, src); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}
