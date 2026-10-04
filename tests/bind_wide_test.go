package tests

import "testing"

// `.bind` on a function with a rest, array or nullable-scalar parameter
// (ADR-01334), with Node's output.
func TestE2EBindRestArrayNullable(t *testing.T) {
	shadowCase(t, `
function sum(base: number, ...xs: number[]): number { return xs.reduce((a, b) => a + b, base) }
const s1 = sum.bind(null, 10)
const s2 = sum.bind(null, 10, 1, 2)
console.log(s1(1, 2, 3), s2(), s2(100), s1.name, s1.length, s2.length)
const max = Math.max.bind(null, 5)
console.log(max(1, 9), max())
function head(xs: number[], fallback?: number): number { return xs.length ? xs[0] : (fallback ?? -1) }
const h = head.bind(null, [7, 8])
console.log(h(), h(3))
function opt(a: number, b?: number): string { return a + "/" + b }
const o = opt.bind(null, 1)
const n: number = s1(5)
console.log(o(), o(2), n + 1)
`, "16 13 113 bound sum 0 0\n9 5\n7 7\n1/undefined 1/2 16")
}
