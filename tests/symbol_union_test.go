package tests

import "testing"

// A symbol or a bigint in a union keeps its own kind: the box says symbol or
// bigint, a narrowing reads the symbol itself, a conditional mixing kinds is
// the union, and a union array holds them.
func TestE2ESymbolAndBigIntInUnions(t *testing.T) {
	assertSameAsNode(t, `
function k(x: string | symbol) { if (typeof x === "symbol") { console.log(x.description, String(x), x.toString()); } }
k(Symbol('s'));
function h(x: string | symbol | number): string { if (typeof x !== "symbol") return String(x); return x.toString(); }
console.log(h(1), h('a'), h(Symbol('z')));
const v: string | symbol = Math.random() > 2 ? 'q' : Symbol('w'); console.log(typeof v);
const v2 = Math.random() > 2 ? 'q' : Symbol('w'); console.log(typeof v2);
const w = Math.random() > 2 ? 1n : 'a'; console.log(typeof w, w);
const z: bigint | string = Math.random() < 2 ? 5n : 'b'; console.log(typeof z, z);
for (const x of ['s', Symbol('t')] as (string | symbol)[]) console.log(typeof x, String(x));
const a: (string | symbol)[] = ['s', Symbol('t')]; console.log(a.length, typeof a[1]);
`)
}
