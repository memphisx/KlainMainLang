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
