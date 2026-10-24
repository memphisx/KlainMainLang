package tests

import "testing"

// TestE2EClassExtendsArray: the instance is the array (TDD-00247 stage 5): an override reached
// through `super` and through a plain `number[]`, a subclass of a subclass,
// species results, in-place methods returning the instance, destructuring.
func TestE2EClassExtendsArray(t *testing.T) {
	assertOutput(t, `class Nums extends Array<number> {
  get total(): number { return this.reduce((a, b) => a + b, 0); }
  push(...xs: number[]): number { return super.push(...xs.map(x => x * 10)); }
}
class Pos extends Nums {
  tag = "pos";
}
const p = new Pos(3, 1, 2);
p.push(5);
console.log(p, p.total, p.length);
const sorted = p.sort((a, b) => a - b);
console.log(sorted === p, sorted, p.reverse());
console.log(p.toSorted(), p.toSorted() instanceof Pos);
const [x, y, ...rest] = p;
console.log(x, y, rest);
console.log(Object.keys(p), JSON.stringify({ p }));
const m = p.map(v => v + 1);
console.log(m instanceof Pos, m instanceof Nums, m);
console.log(p.slice(1) instanceof Pos, p.filter(v => v > 2).length);
const f = p.fill(7);
console.log(f === p, p);
function takes(a: number[]) { a.push(1); }
takes(p);
console.log(p.length, String(p), `+"`"+`${p}`+"`"+`);
`, `Pos(4) [ 3, 1, 2, 50, tag: 'pos' ] 56 4
true Pos(4) [ 50, 3, 2, 1, tag: 'pos' ] Pos(4) [ 50, 3, 2, 1, tag: 'pos' ]
[ 1, 2, 3, 50 ] false
50 3 [ 2, 1 ]
[ '0', '1', '2', '3', 'tag' ] {"p":[50,3,2,1]}
true true Pos(4) [ 51, 4, 3, 2, tag: 'pos' ]
true 2
true Pos(4) [ 7, 7, 7, 7, tag: 'pos' ]
5 7,7,7,7,10 7,7,7,7,10`)
}

// TestE2EClassExtendsArrayMembers: constructor arguments, fields, index and length writes,
// species (`map`/`filter`), statics and `length` truncation.
func TestE2EClassExtendsArrayMembers(t *testing.T) {
	assertOutput(t, `class Bag extends Array<string> {
  label = "bag";
  constructor(...items: string[]) { super(...items); }
  add(x: string): this { this.push(x); return this; }
  first(): string { return this[0]; }
  setAt(i: number, v: string) { this[i] = v; }
}
const b = new Bag("a", "b");
b.add("c").add("d");
b.setAt(0, "z");
b[1] = "y";
console.log(b, b.length, b.first(), b.label);
console.log(b.join("-"), b.indexOf("c"), b.includes("d"));
const m = b.map(s => s.toUpperCase());
console.log(m, m instanceof Bag);
const f = b.filter(s => s > "c");
console.log(f, f.length);
const anyB: any = b;
console.log(Array.isArray(anyB), anyB.length, anyB instanceof Bag);
class N extends Array<number> {}
const n = new N(3);
console.log(n.length, new N(1, 2));
b.length = 1;
console.log(b);
`, `Bag(4) [ 'z', 'y', 'c', 'd', label: 'bag' ] 4 z bag
z-y-c-d 2 true
Bag(4) [ 'Z', 'Y', 'C', 'D', label: 'bag' ] true
Bag(3) [ 'z', 'y', 'd', label: 'bag' ] 3
true 4 true
3 N(2) [ 1, 2 ]
Bag(1) [ 'z', label: 'bag' ]`)
}

// TestE2EClassExtendsArrayDynamic: the instance through `any` (length, methods, spread,
// inspect, instanceof), keys, values, entries and for-in.
func TestE2EClassExtendsArrayDynamic(t *testing.T) {
	assertOutput(t, `class Bag extends Array<string> {
  label = "bag";
}
const b = new Bag("a", "b");
const anyB: any = b;
const p: any = [1];
const t: any = new Uint8Array(2);
console.log(anyB.length, Array.isArray(anyB), anyB instanceof Bag, anyB instanceof Array);
console.log(p instanceof Array, t instanceof Array, p instanceof Bag);
console.log(anyB, anyB[0], anyB.join("+"), [...anyB]);
console.log(Object.keys(b), JSON.stringify(b));
for (const k in b) console.log(k);
class Pos extends Array<number> { n = 4; }
const q = new Pos(3, 1);
console.log(Object.values(q), Object.entries(q));
`, `2 true true true
true false false
Bag(2) [ 'a', 'b', label: 'bag' ] a a+b [ 'a', 'b' ]
[ '0', '1', 'label' ] ["a","b"]
0
1
label
[ 3, 1, 4 ] [ [ '0', 3 ], [ '1', 1 ], [ 'n', 4 ] ]`)
}

// TestE2EClassExtendsTypedArray: a class extending a typed array: its methods, species
// (`subarray`, `map`), `from`, inspect and `ArrayBuffer.isView`.
func TestE2EClassExtendsTypedArray(t *testing.T) {
	assertOutput(t, `class Bytes extends Uint8Array {
  hex(): string { return Array.from(this, b => b.toString(16).padStart(2, "0")).join(""); }
}
const b = new Bytes(3);
b[0] = 255; b.set([1, 2], 1);
console.log(b, b.length, b.hex(), b instanceof Uint8Array, b instanceof Bytes);
const s = b.subarray(1);
console.log(s, s instanceof Bytes, b.map(x => x + 1));
const from = Bytes.from([7, 8]);
console.log(from, from instanceof Bytes);
const ab: any = b;
console.log(ab.length, ab instanceof Bytes, ArrayBuffer.isView(b));
`, `Bytes(3) [Uint8Array] [ 255, 1, 2 ] 3 ff0102 true true
Bytes(2) [Uint8Array] [ 1, 2 ] true Bytes(3) [Uint8Array] [ 0, 2, 3 ]
Bytes(2) [Uint8Array] [ 7, 8 ] true
3 true true`)
}

// TestE2EArrayReceiverIdentity: an in-place array method returns its receiver;
// Object.keys/values/entries and for-in on an array; a binding named like a
// lowered member (`from`) or a type keyword (`any`).
func TestE2EArrayReceiverIdentity(t *testing.T) {
	assertOutput(t, `const a = [3, 1, 2];
console.log(a.sort() === a, a.reverse() === a, a.fill(0) === a, a.copyWithin(0, 1) === a);
const s = a.sort(); s.push(9); console.log(a.length);
console.log(Object.keys(a), Object.values(a), Object.entries(["x"]));
for (const k in a) console.log(k);
const from = Uint8Array.from([1, 2]);
const any: any = [from];
console.log(from, any);
`, `true true true true
4
[ '0', '1', '2', '3' ] [ 0, 0, 0, 9 ] [ [ '0', 'x' ] ]
0
1
2
3
Uint8Array(2) [ 1, 2 ] [ Uint8Array(2) [ 1, 2 ] ]`)
}

// A species result keeps the class when its elements change kind, when the
// receiver is typed as the plain array a species result is, and through
// `any`.
func TestE2EArraySpeciesAcrossLayouts(t *testing.T) {
	assertSameAsNode(t, `class Q extends Array<number> { tag = "q" }
const q = new Q(); q.push(1, 2, 3)
const s = q.map(x => "v" + x)
console.log(s, s instanceof Q, s.length, s[1], s.join("|"))
const f = s.filter(x => x !== "v2")
console.log(f, f instanceof Q, f.map(x => x.length), q.map(x => x > 1))
const a: any = q
console.log(a.map((x: number) => x + 1), a.filter((x: number) => x > 2), a.slice(1), a.slice(1, 2))
console.log(a.concat([9]), a.flat(), a.flatMap((x: number) => [x, x]), a.map(String))
const c: any = new Q(); c.push(1, 2, 3)
console.log(c.splice(1), c, c.splice())
`)
}
