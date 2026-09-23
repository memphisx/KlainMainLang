package tests

import "testing"

// ADR-01072: a generic function's type parameters are in scope for its whole
// instantiation, so `T` inside `Promise<T>`, `T[]`, `T[][]`, `Map<string, T>`,
// `T | null` and a `(x: T) => T` parameter substitutes — not only a bare `T`
// in a parameter position. The backlog case was `new Promise<T>` resolving to
// Promise<number> for every T.
func TestE2EGenericTypeParamScope(t *testing.T) {
	const src = `
function later<T>(ms: number, v: T): Promise<T> {
  return new Promise<T>((resolve) => setTimeout(() => resolve(v), ms));
}
function pair<T>(a: T, b: T): T[] { const xs: T[] = [a, b]; return xs; }
function nest<T>(a: T): T[][] { const m: T[][] = [[a], [a, a]]; return m; }
function toMap<T>(k: string, v: T): Map<string, T> { const m = new Map<string, T>(); m.set(k, v); return m; }
function maybe<T>(v: T, ok: boolean): T | null { if (ok) return v; return null; }
async function delayed<T>(v: T): Promise<T> { const p = new Promise<T>((res) => res(v)); return await p; }
function apply<T>(v: T, f: (x: T) => T): T { return f(v); }
async function run(): Promise<void> {
  console.log(await later(1, "x"), await later(1, 2));
  console.log(pair("a", "b"), pair(1, 2));
  console.log(nest(3).length, nest("s")[1].length);
  console.log(toMap("k", 42).get("k"), toMap("k", "v").get("k"));
  console.log(maybe("x", false), maybe(5, true));
  console.log(await delayed("d"), await delayed(7));
  console.log(apply(2, (x) => x * 3), apply("q", (x) => x + "!"));
}
run();
`
	assertOutput(t, src, `x 2
[ 'a', 'b' ] [ 1, 2 ]
2 2
42 v
null 5
d 7
6 q!`)
	assertSameAsNode(t, src)
}
