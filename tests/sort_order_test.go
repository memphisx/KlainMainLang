package tests

import "testing"

// Array.prototype.sort is V8's TimSort (ADR-01620): stable, and for an
// inconsistent or random comparator the same result and the same comparator
// calls as Node. A deterministic generator drives every array shape (ties,
// runs, reversed runs, sizes on both sides of the 64-element minimum run)
// through a consistent, two inconsistent and a random comparator; the hash
// of every result and call count is Node 24's.
func TestE2ESortMatchesV8(t *testing.T) {
	assertOutputCompatJS(t, `
let seed = 12345;
function rnd() { seed = (seed * 1103515245 + 12345) % 2147483648; return seed; }
let h = 0;
function mix(x) { h = (h * 31 + x) % 1000000007; }
const sizes = [0, 1, 2, 3, 7, 31, 63, 64, 65, 100, 127, 128, 200, 257, 500, 1000, 3000];
for (let si = 0; si < sizes.length; si++) {
  const n = sizes[si];
  for (let mode = 0; mode < 5; mode++) {
    const a = [];
    for (let i = 0; i < n; i++) {
      let k = 0;
      if (mode === 0) k = rnd() % 10;
      else if (mode === 1) k = i;
      else if (mode === 2) k = n - i;
      else if (mode === 3) k = (i % 50 < 25) ? i : n - i;
      else k = rnd() % 1000;
      a.push({ k: k, i: i });
    }
    const s1 = a.slice().sort((x, y) => x.k - y.k);
    for (let i = 0; i < s1.length; i++) mix(s1[i].i);
    const s2 = a.slice().sort((x, y) => (x.k < y.k ? -1 : 1));
    for (let i = 0; i < s2.length; i++) mix(s2[i].i);
    const s3 = a.slice().sort((x, y) => (x.k > y.k ? 1 : -1));
    for (let i = 0; i < s3.length; i++) mix(s3[i].i);
    let calls = 0;
    const s4 = a.slice().sort((x, y) => { calls++; return (rnd() % 3) - 1; });
    for (let i = 0; i < s4.length; i++) mix(s4[i].i);
    mix(calls);
    const nums = a.map(o => o.k);
    const s5 = nums.slice().sort((x, y) => y - x);
    for (let i = 0; i < s5.length; i++) mix(s5[i]);
  }
  console.log(n, h);
}
`, "0 0\n1 861127885\n2 293702859\n3 568523221\n7 572047507\n31 29323050\n63 55869448\n64 625630051\n65 473065956\n100 224189539\n127 445368529\n128 558496957\n200 841703442\n257 964014581\n500 390637172\n1000 962055466\n3000 284227672")
}

// Ties keep their order (the C library's qsort, used before, is not stable
// on Windows or macOS), and a comparator's result is read as its own return
// type: integer literals from a ternary over object fields sorted as garbage
// when the trampoline read them as a double.
func TestE2ESortStableAndComparatorResultTypes(t *testing.T) {
	assertOutput(t, `
class E { n: string; t: number; constructor(n: string, t: number) { this.n = n; this.t = t } }
const a: E[] = []
for (let i = 0; i < 12; i++) a.push(new E("e" + i, i % 3 === 0 ? 0 : 1))
console.log(a.slice().sort((x, y) => x.t - y.t).map(e => e.n).join(","))
console.log(a.slice().sort((x, y) => (x.t < y.t ? -1 : x.t > y.t ? 1 : 0)).map(e => e.n).join(","))
const n = [{ k: 0, v: "a" }, { k: 0, v: "b" }, { k: 0, v: "c" }]
console.log(n.slice().sort((x, y) => x.k - y.k).map(e => e.v).join(","))
`, "e0,e3,e6,e9,e1,e2,e4,e5,e7,e8,e10,e11\ne0,e3,e6,e9,e1,e2,e4,e5,e7,e8,e10,e11\na,b,c")
}

// undefined elements go last and are never passed to the comparator; a
// three-state string array's null sorts as "null" (SortIndexedProperties,
// SortCompare).
func TestE2ESortUndefinedLastNullAsString(t *testing.T) {
	assertOutput(t, `
const a: (string | undefined)[] = [undefined, "b", "a"]
console.log(a.sort().map(String).join(","))
const b: (string | null)[] = [null, "b", "a"]
console.log(b.sort().map(String).join(","))
const c: (string | null | undefined)[] = [null, undefined, "b", "nulx", "a"]
console.log(c.sort().map(String).join(","))
const d: (string | undefined)[] = ["b", undefined, "a"]
console.log(d.sort((x, y) => (x! < y! ? -1 : 1)).map(String).join(","))
const xs: any[] = [3, undefined, 1, undefined, 2]
const calls: string[] = []
console.log(xs.sort((x: any, y: any) => { calls.push(String(x) + "/" + String(y)); return x - y }).map(String).join(","), calls.join(" "))
const ys: any[] = [undefined, "b", "a"]
console.log(ys.sort().map(String).join(","))
`, "a,b,undefined\na,b,null\na,b,null,nulx,undefined\na,b,undefined\n1,2,3,undefined,undefined 1/3 2/1 2/3 2/1\na,b,undefined")
}

// With no comparator, objects sort by their strings — each side converted
// once per comparison, so a toString() decides and is called as often as in
// Node, and plain objects ("[object Object]") keep their order — and a Date
// by its toString().
func TestE2ESortObjectsByString(t *testing.T) {
	assertOutput(t, `
class P { n: string; constructor(n: string) { this.n = n } toString() { return "P:" + this.n } }
console.log([new P("c"), new P("a"), new P("b")].sort().map(p => p.n).join(","))
console.log([{ v: 3 }, { v: 1 }, { v: 2 }].sort().map(o => o.v).join(","))
class Q { n: number; constructor(n: number) { this.n = n } }
console.log([new Q(3), new Q(1), new Q(2)].toSorted().map(q => q.n).join(","))
let calls = 0
class R { n: number; constructor(n: number) { this.n = n } toString() { calls++; return String(this.n) } }
console.log([new R(10), new R(9), new R(1)].sort().map(r => r.n).join(","), calls)
console.log([new Date(2020, 1, 1), new Date(2019, 1, 1)].sort().map(d => d.getFullYear()).join(","))
`, "a,b,c\n3,1,2\n3,1,2\n1,10,9 8\n2019,2020")
}

// Strings order by UTF-16 code units, where U+E000..U+FFFF come after the
// supplementary characters (whose first unit is a surrogate); UTF-8's byte
// order, used before, is code-point order.
func TestE2EStringOrderUTF16(t *testing.T) {
	assertOutput(t, `
console.log(["￿", "\u{10000}", "a", "", "퟿"].sort().map(s => s.codePointAt(0)!.toString(16)).join(","))
console.log("￿" < "\u{10000}", "\u{10000}" < "￿", "" > "\u{1F600}", "ab" < "ac", "a" < "ab", "" < "a")
const xs: any[] = ["￿", "\u{10000}", 5, "a"]
console.log(xs.sort().map((s: any) => String(s).codePointAt(0)!.toString(16)).join(","))
const p: any = "￿", q: any = "\u{10000}"
console.log(p < q, q < p)
`, "61,d7ff,10000,e000,ffff\nfalse true true true true true\n35,61,10000,ffff\nfalse true")
}

// A Date read from an array is Date | undefined: String() of it is the date,
// and of a missing element "undefined" — it was read as a string before.
func TestE2EDateElementToString(t *testing.T) {
	assertOutput(t, `
const ds: Date[] = [new Date(Date.UTC(2020, 1, 1))]
const e = ds[0]
console.log(String(e) === e.toString(), `+"`${ds[0]}`"+` === ds[0].toString(), String(ds[5]))
const n: Date | null = null
console.log(String(n))
`, "true true undefined\nnull")
}

// globalThis.x for a global a global module implements (performance) is that
// global, as the bare name is; and globalThis as a value carries those
// globals as Node's does (performance enumerable, the classes not, no Worker).
func TestE2EGlobalThisModuleGlobals(t *testing.T) {
	assertOutput(t, `
console.log(globalThis.performance.now() > 0, typeof globalThis.performance.now, typeof globalThis.performance)
const p = globalThis.performance
console.log(p === performance)
function f(performance: number) { return typeof globalThis.performance.mark }
console.log(f(1))
const g: any = globalThis
console.log(typeof g.performance.mark, typeof g.PerformanceObserver, g === globalThis)
console.log(Object.keys(globalThis).includes("performance"), Object.keys(globalThis).includes("PerformanceObserver"), typeof g.Worker)
`, "true function object\ntrue\nfunction\nfunction function true\ntrue false undefined")
}

// A comparator returning a boolean (true is 1, false 0, never negative) or
// nothing (undefined, NaN, read as 0) leaves the order to V8's algorithm,
// as in Node: here, unchanged.
func TestE2ESortBooleanAndVoidComparators(t *testing.T) {
	assertOutputCompatJS(t, `
const a = [];
for (let i = 0; i < 12; i++) a.push({ n: "e" + i, t: i % 3 === 0 ? 0 : 1 });
console.log(a.slice().sort((x, y) => x.t > y.t).map(e => e.n).join(","));
console.log([3, 1, 2].sort((x, y) => x > y).join(","));
console.log([3, 1, 2].sort(() => {}).join(","));
`, "e0,e1,e2,e3,e4,e5,e6,e7,e8,e9,e10,e11\n3,1,2\n3,1,2")
}

// concat with an `any` argument spreads it when it holds an array
// (IsConcatSpreadable) and appends it otherwise, so the result is an any[]
// whatever the receiver's elements (they were coerced to the receiver's
// element type, an array becoming NaN); an empty literal receiver, never[]
// to tsc, takes its arguments' element type (ADR-01621).
func TestE2EConcatAnyAndEmptyLiteral(t *testing.T) {
	assertOutput(t, `
const v: any = [1, 2, 3]
let a: any[] = []
a = a.concat(v)
console.log(a.length, JSON.stringify(a))
const n: number[] = [0]
console.log(JSON.stringify(n.concat(v as any)))
const s: any = "str"
console.log(JSON.stringify([9].concat(s, v, 4 as any)))
`, "3 [1,2,3]\n[0,1,2,3]\n[9,\"str\",1,2,3,4]")
}

// An untyped [] reassigned from a callback passed to a constructor (`new
// PerformanceObserver(t.step_func(…))`) takes the element type of what it
// is assigned: the usage pass did not look into a new expression's
// arguments, so it stayed number[] and the entries were read as numbers.
func TestE2EEmptyArrayEvolvesThroughNewArgs(t *testing.T) {
	assertOutputCompatJS(t, `
class T {
  step_func(f) { return (...args) => { f(...args); }; }
}
function async_test(fn) { fn(new T()); }
async_test(function (t) {
  var stored = [];
  var observer = new PerformanceObserver(t.step_func(function (entryList, obs) {
    stored = stored.concat(entryList.getEntries());
    console.log(typeof stored[0], stored[0].name, stored.length);
    observer.disconnect();
  }));
  observer.observe({ entryTypes: ["mark"] });
  performance.mark("q");
});
function check(list) { list.forEach(function (x) { console.log(typeof x, x.name); }); }
check([].concat(performance.getEntries()));
`, "object q\nobject q 1")
}

// A function passed where nothing types it (a dynamic function) gets its
// own body's analyses, as every other function does: an untyped [] typed by
// its later use, a Map's key and value types, a binding assigned two kinds.
// It ran with whatever its enclosing function had left (ADR-01621).
func TestE2EDynamicFunctionBodyAnalyses(t *testing.T) {
	assertOutputCompatJS(t, `
function run(fn) { fn(1); }
run(function (t) {
  var stored = [];
  stored = stored.concat([{ name: "z" }]);
  console.log(stored[0].name);
  let x = 5;
  x = "hi";
  console.log(x, typeof x);
  const m = new Map();
  m.set("k", { v: 1 });
  console.log(m.get("k").v);
});
`, "z\nhi string\n1")
}

// An array of arrays sorts, by a comparator or by the rows' strings (which
// join their elements with commas), as in Node (ADR-01620).
func TestE2ESortArrayOfArrays(t *testing.T) {
	assertSameAsNode(t, `
const m: number[][] = [[3, 1], [1, 2, 3], [2], [10], [1, 10]]
const byLen = m.slice().sort((a: number[], b: number[]) => a.length - b.length)
console.log(JSON.stringify(byLen))
console.log(JSON.stringify(m.slice().sort()))
`)
}
