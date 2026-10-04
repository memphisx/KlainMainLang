package tests

import "testing"

// undefined held in a `number` slot (TDD-00241), with Node's output.
func TestE2EUndefinedInNumberSlot(t *testing.T) {
	shadowCase(t, `
const o: any = {}
const n: number = o.missing
const m: number = o.other
const k: number = 3
console.log(n, String(n), n + 1, -n, Math.abs(n), [n], { n })
console.log(typeof n, typeof k, n === undefined, n == null, k === undefined, n === m, n === k)
console.log(JSON.stringify({ n, k }), JSON.stringify([n, k]), n ? "t" : "f")
console.log(+n, Number(n), n * 1, n / 1, n - 0, n ** 1, Math.max(n, 1), isNaN(n))
const f: any = (a: number, c: number) => console.log(a, c, c + 1, typeof c)
f(1)
const arr: number[] = [1, 2]
const p = arr.find((x) => x > 5)
console.log(p!, p ?? 9)
try { console.log(n.toFixed(2)) } catch (e) { console.log((e as Error).name, (e as Error).message) }
let t: number = o.x
t ??= 5
class C { v: number = o.q }
const c = new C()
c.v ??= 7
console.log(t, c.v, n ?? 30, n?.toFixed(1), (n ?? 2) * 2)
`, `undefined undefined NaN NaN NaN [ undefined ] { n: undefined }
undefined number true true false true false
{"k":3} [null,3] f
NaN NaN NaN NaN NaN NaN NaN true
1 undefined NaN undefined
undefined 9
TypeError Cannot read properties of undefined (reading 'toFixed')
5 7 30 undefined 4`)
}
