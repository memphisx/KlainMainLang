package tests

import "testing"

// Iterator.prototype's ES2025 helpers over a generator and the Array, Map
// and Set iterators: lazy, closing the source early, the same through `any`.
func TestE2EIteratorHelpers(t *testing.T) {
	src := `
function* g() { console.log("a"); yield 1; console.log("b"); yield -1; console.log("c"); yield 2; }
console.log(g().every((x) => x > 0));
console.log(g().some((x) => x < 0));
console.log(g().find((x) => x > 1));
console.log(g().reduce((a: number, b) => a + b, 10));
g().forEach((x, i) => console.log("f", x, i));
console.log(g().toArray());
const m = g().map((x) => x * 10);
console.log("made");
console.log(m.next());
console.log(m.toArray());
console.log(g().filter((x) => x > 0).take(1).toArray());
console.log(g().drop(1).toArray());
console.log([1, 2].values().flatMap((x) => [x, x * 100]).toArray());
console.log(new Map([[1, "a"], [2, "bb"]]).values().map((s) => s.length).toArray());
console.log(new Set(["x", "yy"]).values().reduce((a, s) => a + s.length, 0));
for (const v of [5, 6].values().map((x) => -x)) console.log("for", v);
const h = g().take(5);
console.log(h.return?.(), h.next());
console.log(Object.prototype.toString.call(g().map((x) => x)));
try { g().take(-1); } catch (e) { console.log((e as Error).name); }
try { [1].values().reduce((a, b) => a + b, 0); [0].values().drop(1).reduce((a, b) => a + b); } catch (e) { console.log((e as Error).message); }
const anyGen: any = g();
console.log(anyGen.map((x: number) => x * 2).toArray());
const anyIt: any = [1, 2, 3].values();
console.log(anyIt.every((x: number) => x > 0), anyIt.toArray());
`
	assertSameAsNode(t, src)
	assertSameAsNodeCompatJS(t, src)
}
