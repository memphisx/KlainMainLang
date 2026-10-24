package tests

import "testing"

// A `T[]` binding holding null from `any` or JSON is null, not undefined
// and not an empty array; one holding undefined stays undefined. Both
// short-circuit `?.`.
func TestE2EArrayNullFromAny(t *testing.T) {
	assertOutput(t, `
const x: number[] = null as any;
console.log(x, x === null, x === undefined, x == null);
let y: string[] = undefined as any;
console.log(y, y === null, y === undefined);
function f(a: number[]) { return a === null ? 'N' : a === undefined ? 'U' : a.length; }
console.log(f(null as any), f(undefined as any), f([1, 2]));
const z: number[] = JSON.parse('null');
console.log(z, JSON.stringify({ z }));
console.log(JSON.stringify(x), JSON.stringify([x, z]), JSON.stringify(y));
const o: { a: number[] } = { a: null as any };
console.log(o, JSON.stringify(o), o.a?.length, o.a ?? 'dflt', o.a?.[0]);
const arr: number[] = [1, 2, 3];
console.log(arr?.length, arr?.[1]);
`, `null true false true
undefined false true
N U 2
null {"z":null}
null [null,null] undefined
{ a: null } {"a":null} undefined dflt undefined
3 2`)
}
