package tests

import "testing"

// ADR-01070: under -compat=js an absent `T | undefined` numeric operand is NaN
// (ToNumber(undefined)), a `T | null` one stays 0 (ToNumber(null)), bitwise
// operators keep ToInt32(NaN) = 0, and string concatenation renders
// "undefined". Call results and locals alike.
func TestE2EJSUndefinedArithmeticIsNaN(t *testing.T) {
	const src = `
function f(x: number): number | undefined { if (x > 0) return x; }
function g(x: number): number | null { if (x > 0) return x; return null; }
console.log(f(-1) + 2, f(3) + 2, f(-1) < 1, f(-1) | 0, f(-1) * 2, f(-1) - 1, f(-1) ** 2);
let u: number | undefined = f(-1);
console.log(u + 2, u * 3, u > 0, u >= 0, u & 1);
u = 4;
console.log(u + 2, u * 3);
console.log(g(-1) + 2, g(-1) * 3);
console.log("s" + f(-1), f(-1) + "s", "s" + g(-1));
`
	assertOutputCompatJS(t, src, `NaN 5 false 0 NaN NaN NaN
NaN NaN false false 0
6 12
2 0
sundefined undefineds snull`)
	assertSameAsNodeCompatJS(t, src)
}

// ADR-01070: a reduce callback that can fall off the end makes the accumulator
// `T | undefined` in the js lane — the next step sees `undefined`, and the fold's
// result is `undefined` when every step fell off.
func TestE2EJSReduceFallOffAccumulator(t *testing.T) {
	const src = `
const r0 = [1, 2, 3].reduce((a, x) => { if (x > 1) return a + x }, 0);
console.log(r0);
const r1 = [1, 2, 3].reduce((a, x) => { if (x > 5) return a + x }, 0);
console.log(r1, typeof r1, r1 === undefined);
const r2 = [1, 2, 3].reduceRight((a, x) => { if (x < 3) return a + x }, 0);
console.log(r2);
const r3 = [1, 2, 3].reduce((a, x) => a + x, 0);
console.log(r3);
const r4 = ["a", "b"].reduce((a, x) => { if (x === "a") return a + x }, "");
console.log(r4);
const r5 = [1, 2].reduce((a: number | undefined, x) => (a ?? 10) + x, 0);
console.log(r5);
`
	assertOutputCompatJS(t, src, `NaN
undefined undefined true
NaN
6
undefined
3`)
	assertSameAsNodeCompatJS(t, src)
}

// An omitted optional scalar parameter is undefined, not its zero, for
// equality too: `copy === false` is false when `copy?: boolean` is omitted.
func TestE2EOmittedOptionalScalarEquality(t *testing.T) {
	assertOutput(t, `
function f(a: number, b: number | bigint, copy?: boolean, n?: number): string {
  return String(copy) + ' ' + (copy === false) + ' ' + (copy === undefined) + ' ' + (n === 0) + ' ' + (n !== 0);
}
console.log(f(1, 2), f(1, 2, false, 0), f(1, 2, true, 3));
`, "undefined false true false true false true false true false true false false false true")
}

// A Date operand converts as JavaScript does: `+` concatenates its date
// string, `-`/`*`/`/`/`%` and the relational operators read its time value.
func TestE2EDateOperandsConvertAsJS(t *testing.T) {
	assertSameAsNodeCompatJS(t, `
const d = new Date(0), e = new Date(1000);
console.log(typeof (d + 1), typeof (d - 1), d - 1, e - d, 1 - e, e * 2, e / 10, e % 7, d < 0.5, e > 999.5, (d + '').length > 10);
console.log(d + 1 === String(d) + '1', d + e === String(d) + String(e));
`)
}
