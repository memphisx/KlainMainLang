package tests

import "testing"

// A typed Map or Set read through a view whose key or value is `any` runs
// its own layout's operations, and writes through the view reach the
// original object (TDD-00258).
func TestE2EMapSetAnyView(t *testing.T) {
	assertSameAsNode(t, `
function show(m: Map<any, any>, s: Set<any>) {
  console.log(m, s, m.get('a'), m.has('b'), s.has(2), m.size, s.size);
  for (const [k, v] of m) console.log('kv', k, v);
  for (const x of s) console.log('x', x);
  m.forEach((v, k) => console.log('fe', k, v));
  console.log(Array.from(m.keys()), [...m.values()], [...m], [...s]);
  m.set('z', 26); s.add(9); m.delete('a'); s.delete(1);
}
const m = new Map<string, number>([['a', 1], ['b', 2]]);
const s = new Set<number>([1, 2]);
show(m, s);
console.log(m, s, m.get('z'), s.has(9));
const box: any = m;
const g = box as Map<any, any>;
g.set('c', 'x');
console.log(g.get('b'), [...g.keys()]);
const mine = new Map<any, any>([[1, 'one']]);
show(mine, new Set<any>(['w']));
`)
}

// A typed view of a Map<any, any> or Set<any> (an assertion through `any`)
// reads and writes the object's own entries.
func TestE2EMapSetTypedView(t *testing.T) {
	assertSameAsNode(t, `
const m = new Map<any, any>([['a', 1], ['b', 2]]);
const box: any = m;
const t = box as Map<string, number>;
console.log(t.get('a'), t.has('b'), t.has('zz'), t.size, t);
t.set('c', 7);
for (const [k, v] of t) console.log(k, v + 1);
console.log(m.get('c'), [...t.keys()], [...t.values()]);
const s = new Set<any>(['p', 'r']);
const ts = (s as any) as Set<string>;
console.log(ts.has('p'), [...ts]);
ts.add('q'); ts.delete('r'); console.log(s, ts);
`)
}
