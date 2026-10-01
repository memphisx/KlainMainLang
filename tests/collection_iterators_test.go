package tests

import "testing"

// Map and Set keep insertion order across delete; iteration is live.
func TestE2EMapDeleteKeepsInsertionOrder(t *testing.T) {
	assertSameAsNode(t, `
const m = new Map<string, number>([["a", 1], ["b", 2], ["c", 3]]);
m.delete("a");
console.log([...m.keys()].join());
const n = new Map<number, number>([[1, 1], [2, 2], [3, 3]]);
n.delete(1);
console.log([...n.keys()].join());
const s = new Set<string>(["a", "b", "c"]);
s.delete("a");
s.add("a");
console.log([...s].join());
const d: Record<string, number> = { a: 1, b: 2, c: 3 };
delete d.a;
d.a = 9;
console.log(Object.keys(d).join());
`)
}

func TestE2EMapForOfIsLive(t *testing.T) {
	assertSameAsNode(t, `
const m = new Map<string, number>([["a", 1], ["b", 2], ["c", 3], ["d", 4]]);
for (const [k, v] of m) {
  if (k === "b") m.delete("b");
  if (k === "a") m.delete("c");
  console.log("1", k, v);
}
console.log([...m.keys()].join());
const s = new Set<number>([1, 2, 3]);
for (const v of s) { console.log("2", v); if (v === 2) s.clear(); }
console.log(s.size);
const g = new Set<number>([1]);
for (const v of g) { if (v < 3) g.add(v + 1); console.log("3", v); }
let n = 0;
for (const [k] of m) { if (n++ < 3) m.set(k + "!", 0); }
console.log([...m.keys()].join());
function find(mm: Map<number, string>, x: number): string {
  for (const [k, v] of mm) { if (k === x) return v; }
  return "none";
}
const nm = new Map<number, string>([[1.5, "p"], [2.5, "q"]]);
console.log(find(nm, 2.5), find(nm, 9));
nm.delete(1.5);
console.log([...nm.entries()]);
`)
}

func TestE2EMapForOfBindsEntries(t *testing.T) {
	assertSameAsNode(t, `
const m = new Map<string, number>([["x", 1], ["y", 2]]);
for (const e of m) console.log(e);
for (const [k] of m) console.log(k);
for (const k of m.keys()) console.log(k);
for (const e of m.entries()) console.log(e[0], e[1]);
const o1 = { id: 1 }, o2 = { id: 2 };
const om = new Map<{ id: number }, string>([[o1, "x"], [o2, "y"]]);
for (const [k, v] of om) console.log(k.id, v);
om.delete(o1);
for (const k of om.keys()) console.log(k.id);
const bm = new Map<bigint, number>([[1n, 1], [2n, 2]]);
for (const k of bm.keys()) console.log(k);
const ss = new Set<string>(["p", "q"]);
for (const [a, b] of ss.entries()) console.log(a, b);
for (const e of ss.entries()) console.log(e);
for (const x of m.values()) for (const y of m.keys()) console.log(x, y);
`)
}

func TestE2EMapArrayValuesAlias(t *testing.T) {
	assertSameAsNode(t, `
const am = new Map<string, number[]>([["z", [1, 2]]]);
for (const [k, arr] of am) console.log(k, arr.length, arr);
for (const arr of am.values()) arr.push(3);
console.log(am.get("z"));
const g = am.get("z")!;
g.push(4);
console.log(am.get("z"));
const xs: number[][] = [[1], [2]];
for (const a of xs) { a.push(9); a.push(8); }
console.log(xs);
const o = { l: [[1]] as number[][] };
for (const a of o.l) a.push(5);
console.log(o.l);
`)
}

func TestE2EMapSetIteratorObjects(t *testing.T) {
	assertSameAsNode(t, `
const m = new Map<string, number>([["a", 1], ["b", 2], ["c", 3]]);
const it = m.keys();
console.log(it.next());
m.delete("a");
console.log(it.next());
m.set("d", 4);
for (const k of it) { console.log("loop", k); break; }
for (const k of it) console.log("rest", k);
console.log(it.next());
m.set("e", 5);
console.log(it.next());
const s = new Set<number>([7, 8]);
const e = s.entries();
console.log(e.next().value, e.toArray());
const vi = m.values();
console.log(vi.next().value, typeof vi);
const en = m.entries();
const first = en.next();
if (!first.done) console.log(first.value[0], first.value[1]);
console.log(Array.from(m.entries()).length);
const sk = s.keys();
console.log([...sk], [...sk]);
const n = new Map<number, number>([[1, 1], [2, 2]]);
console.log([...n.keys()], Array.from(n.values()));
`)
}

func TestE2EMapSetIteratorInspect(t *testing.T) {
	assertSameAsNode(t, `
const m = new Map<string, number>([["a", 1], ["b", 2]]);
const it = m.keys();
console.log(it);
it.next();
console.log(it, m.values(), m.entries(), new Set([1]).values(), new Set([1]).entries());
const e = m.keys(); e.next(); e.next();
console.log(e);
console.log(`+"`${m.keys()}`"+`);
`)
}

func TestE2EArrayIteratorObjects(t *testing.T) {
	assertSameAsNode(t, `
const a = [5, 6];
const it = a.values();
console.log(it.next());
a.push(7);
console.log([...it]);
console.log(it.next().done);
for (const [i, v] of a.entries()) console.log(i, v);
console.log([...a.keys()], Array.from(a.entries()));
for (const v of a.values()) console.log(v);
console.log(a.values(), `+"`${a.keys()}`"+`, typeof a.keys());
const nested: number[][] = [[1], [2]];
for (const x of nested.values()) x.push(0);
console.log(nested);
const ki = ["x", "y"].keys();
console.log(ki.next().value, ki.toArray());
`)
}

func TestE2EGeneratorSpread(t *testing.T) {
	assertSameAsNode(t, `
function* g(): Generator<number> { yield 1; yield 2; }
console.log([...g()]);
const s = new Set<number>([1, 2, 3]);
function* h(): Generator<number> { for (const v of s) yield v; }
console.log([...h()], Array.from(h()));
`)
}

// A generic alias of the builtin declarations instantiates like a program's
// own (IteratorResult<T> is a union of two generic interfaces).
func TestE2EBuiltinGenericAliasIteratorResult(t *testing.T) {
	assertSameAsNode(t, `
function f(): IteratorResult<number> { return { value: 1, done: false }; }
console.log(f());
const r: IteratorResult<string> = { value: "a", done: false };
console.log(r.value, r.done);
async function g(): Promise<IteratorResult<number>> { return { value: 2, done: false }; }
g().then(x => console.log(x));
`)
}

// A Map or Set constructor takes any iterable: another Map or Set, an
// iterator, a generator.
func TestE2EMapSetConstructFromIterables(t *testing.T) {
	assertSameAsNode(t, `
const m = new Map<string, number>([["a", 1], ["b", 2]]);
const s = new Set(m.keys());
console.log(s);
console.log(new Map(m), new Map(m.entries()));
console.log(new Set(s), new Set([1, 2].values()));
function* g(): Generator<number> { yield 3; yield 3; yield 4; }
console.log(new Set(g()));
const nums = new Map<number, string>([[1, "x"]]);
console.log(new Set(nums.keys()), new Set(nums.values()));
class R { *[Symbol.iterator](): Generator<number> { yield 1; yield 2; } }
console.log(new Set(new R()));
const it = { *[Symbol.iterator]() { yield ["k", 1] as [string, number]; } };
console.log(new Map(it));
`)
}

// A finished generator's next() reads value undefined, unless the step
// completed with a return value.
func TestE2EGeneratorDoneValue(t *testing.T) {
	assertSameAsNode(t, `
function* g(): Generator<number> { yield 1; yield 2; }
const a = g(); console.log(a.next(), a.next(), a.next(), a.next());
function* h(): Generator<number, number> { yield 1; return 9; }
const b = h(); console.log(b.next(), b.next(), b.next());
function* s(): Generator<string> { yield "x"; }
const c = s(); console.log(c.next(), c.next());
const d = g(); console.log(d.next(), d.return(5), d.next());
const e2 = g(); console.log(e2.return(), e2.next());
let total = 0; for (const v of g()) total += v; console.log(total);
const f = g(); let r = f.next(); while (!r.done) { console.log("v", r.value); r = f.next(); }
function* arr(): Generator<number[]> { yield [1, 2]; }
const q = arr(); console.log(q.next(), q.next());
`)
}
