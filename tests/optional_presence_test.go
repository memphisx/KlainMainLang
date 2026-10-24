package tests

import "testing"

// An optional member holding undefined is still an own property when it was
// written (TDD-00256): literals, assignment, spread, JSON.parse and
// Partial<T> keep the key; JSON.stringify drops it, as in Node.
func TestE2EOptionalFieldPresence(t *testing.T) {
	assertOutput(t, `
interface O { a: number; b?: number; s?: string }
const q: O = { a: 1, b: undefined };
console.log(q, Object.keys(q), 'b' in q, 's' in q, JSON.stringify(q));
const w: O = { a: 1 };
console.log(Object.keys(w), 'b' in w);
w.b = undefined;
console.log(w, Object.keys(w), Object.hasOwn(w, 'b'));
console.log({ ...q }, Object.entries(q));
const x: any = q;
console.log(Object.keys(x), 's' in x);
function f(o: O) { return Object.keys(o).join(','); }
console.log(f({ a: 2, s: undefined }), f({ a: 3 }));
const p: O = JSON.parse('{"a":1,"b":null}');
console.log(Object.keys(p));
for (const k in q) console.log('k', k);
const pa: Partial<{ a: number; b: string }> = { a: undefined };
console.log(pa, pa.a === undefined, 'b' in pa);
const fromAny: O = JSON.parse('{"a":1}') as any;
const dyn: any = { a: 1, b: undefined };
const y: O = dyn;
console.log(Object.keys(fromAny), Object.keys(y));
`, `{ a: 1, b: undefined } [ 'a', 'b' ] true false {"a":1}
[ 'a' ] false
{ a: 1, b: undefined } [ 'a', 'b' ] true
{ a: 1, b: undefined } [ [ 'a', 1 ], [ 'b', undefined ] ]
[ 'a', 'b' ] false
a,s a
[ 'a', 'b' ]
k a
k b
{ a: undefined } true false
[ 'a' ] [ 'a', 'b' ]`)
}

// A class's optional field is an own property from construction (every
// declared field is); `delete` removes it, through subclasses whose
// layouts extend the base's presence word (TDD-00256 Stage 4).
func TestE2EClassOptionalFieldDelete(t *testing.T) {
	assertOutput(t, `
class C { a = 1; f?: number; g?: string = undefined; }
const c = new C();
console.log(c, Object.keys(c), 'f' in c, 'g' in c);
delete c.f;
console.log(c, Object.keys(c), 'f' in c, c.f);
class D extends C { h?: boolean }
const d = new D();
delete d.g;
console.log(d, Object.keys(d));
class G extends D { a2 = 2; z?: number }
const g = new G();
delete g.h; delete g.z;
console.log(g, Object.keys(g));
`, `C { a: 1, f: undefined, g: undefined } [ 'a', 'f', 'g' ] true true
C { a: 1, g: undefined } [ 'a', 'g' ] false undefined
D { a: 1, f: undefined, h: undefined } [ 'a', 'f', 'h' ]
G { a: 1, f: undefined, g: undefined, a2: 2 } [ 'a', 'f', 'g', 'a2' ]`)
}
