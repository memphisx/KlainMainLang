package tests

import "testing"

// A destructuring default replaces undefined only: a `T | null` field keeps
// its null, and an array field binds the field's own array. A null or
// undefined literal (or a value) asserted to a type widened with them takes
// the asserted type.
func TestE2EDestructuringDefaultOnlyOnUndefined(t *testing.T) {
	assertSameAsNode(t, `
const o = { v: undefined as number | undefined, w: null as number[] | null, s: undefined as string | undefined };
const { v = 5, w = [1], s = 'd' } = o; console.log(v, w, s);
const { items = [1] } = { items: null as number[] | null }; console.log(items);
const p = { n: null as number | null }; const { n = 7 } = p; console.log(n, p.n === null);
interface Opt { a?: string; b?: number; d: number | null }
function f(o: Opt) { const { a = "A", b = 2, d = 4 } = o; console.log(a, b, d); }
f({ d: null }); f({ a: "x", b: 0, d: 1 });
const q = { arr: [1, 2] as number[] | undefined, arr2: [3] }; const { arr = [9], arr2 } = q;
arr.push(3); arr2.push(4); console.log(q.arr, q.arr2, arr, arr2);
const { zz = [7] } = { zz: undefined as number[] | undefined }; console.log(zz, zz.length);
const [z = 9] = [null as number | null]; const [y = 8] = [undefined as number | undefined]; console.log(z, y);
const { x = 1, yy = 'q' } = { x: 5, yy: 'z' }; console.log(x, yy);
let a2 = 0, b2 = ''; ({ a2 = 3, b2 = 'd' } = { a2: 7, b2: 'k' }); console.log(a2, b2);
let c2: string | null = 'c'; ({ c2 = 'D' } = { c2: null as string | null }); console.log(c2);
`)
}

// An unannotated destructured parameter takes its type from its default,
// an array pattern over an array literal as a tuple, as tsc types it.
func TestE2EPatternParameterTypedFromDefault(t *testing.T) {
	assertSameAsNode(t, `
function f([x, y, z] = [1, 2, 3]) { return x + y + z; }
console.log(f(), f([4, 5, 6]));
const g = ({ a } = { a: 7 }) => a;
console.log(g(), g({ a: 8 }));
function m([n, s] = [1, "a"]) { return s + n; }
console.log(m(), m([2, "b"]));
class C { static async *p([x, y] = [1, 2]) { yield x + y; } }
C.p().next().then((r) => console.log(r.value));
`)
}

// An empty default typed by its pattern: `{ a = 1 } = {}` and
// `[[x] = [4]] = []` take each element's default when the argument omits it.
func TestE2EPatternParameterEmptyDefault(t *testing.T) {
	assertSameAsNode(t, `
function g2({ a = 1, b = "s" } = {}) { return b + a; }
console.log(g2(), g2({ a: 2, b: "t" }), g2({ b: "u" }));
const g = ({ a = 1, b = "s" } = {}) => b + a;
console.log(g(), g({ a: 2, b: "t" }));
const f = ([[x, y, z] = [4, 5, 6]] = []) => x + y + z;
console.log(f(), f([[1, 2, 3]]));
function h([p = 7] = []) { return p; }
console.log(h(), h([8]));
`)
}
