package tests

import "testing"

// The programs below all reached clang as invalid IR (the conformance run's
// CLANG_ERROR bucket, ADR-01043). Each now either runs with Node's output or is
// rejected at compile time the way tsc rejects it.

// A `T | undefined` scalar — an element read that may be out of range, a Map
// lookup, an omitted optional parameter — handed to a Number.* / global
// predicate or to Math.* was passed on as its { i1, T } aggregate. Number.isX
// answers false for an absent value; the global isNaN/isFinite ToNumber it
// first (undefined → NaN); Math.* sees NaN.
func TestE2ENumberAndMathOnAbsentScalar(t *testing.T) {
	assertOutput(t, `
const xs: number[] = [NaN, 1.5, 0 / 0];
for (let i = 0; i < 5; i++) {
  console.log(Number.isNaN(xs[i]), isNaN(xs[i]), Number.isFinite(xs[i]), isFinite(xs[i]), Number.isInteger(xs[i]), Number.isSafeInteger(xs[i]));
}
const zs: number[] = [1, 2];
console.log(Number.isInteger(zs[5]), isNaN(zs[5]), isFinite(zs[5]), Number.isSafeInteger(zs[5]), Number.isFinite(zs[5]));
console.log(Number.isInteger(zs[1]), isNaN(zs[1]), isFinite(zs[1]), Number.isSafeInteger(zs[1]), Number.isFinite(zs[1]));
const m = new Map<string, number>();
m.set("a", 2.5);
console.log(Number.isNaN(m.get("x")), Number.isInteger(m.get("x")), Number.isFinite(m.get("a")), Number.isInteger(m.get("a")));
function f(u?: number): void {
  console.log(Number.isNaN(u), Number.isFinite(u), Number.isInteger(u), Number.isSafeInteger(u));
}
f();
f(3);
f(0.5);
const fs: number[] = [0.5];
console.log(Math.abs(xs[1]), Math.floor(xs[9]), Math.max(xs[1], zs[9]), Math.sqrt(zs[1]));
console.log(Math.round(fs[3]), Math.sign(fs[3]), Math.min(fs[0], fs[3]), Math.hypot(fs[0], fs[0]), Math.trunc(fs[0]), Math.pow(fs[3], 0));
`, `true true false false false false
false false true true false false
true true false false false false
false true false false false false
false true false false false false
false true false false false
true false true true true
false false true false
false false false false
false true true true
false true false false
1.5 NaN NaN 1.4142135623730951
NaN NaN NaN 0.7071067811865476 0 1
`)
}

// The fold's accumulator has one type; a callback result with no conversion to
// it was stored anyway.
func TestE2EReduceAccumulatorTypeMismatchRejected(t *testing.T) {
	assertCodegenError(t, "const xs: number[] = [1, 2, 3]\nconsole.log(xs.reduce((x, y) => `(${x}+${y})`, 0))\n",
		"reduce callback returns string but the accumulator is number")
	assertOutput(t, "const xs: number[] = [1, 2, 3]\n"+
		"console.log(xs.reduce((a, b) => a + b, 0), xs.reduce((a, b) => a + b), xs.reduce((a, b) => a + b * 0.5, 0))\n"+
		"console.log(xs.reduce((s, b) => s + \",\" + b, \"\"), [\"a\", \"bb\"].reduce((n, s) => n + s.length, 0))\n"+
		"console.log(xs.reduceRight((s, b) => `(${s}+${b})`, \"0\"))\n",
		"6 6 3\n,1,2,3 3\n(((0+3)+2)+1)\n")
}

// A variable reassignment of the wrong type was already rejected; the same
// assignment into a field was not.
func TestE2EFieldAssignTypeMismatchRejected(t *testing.T) {
	assertCodegenError(t, `
interface P { n: number }
const p: P = { n: 1 }
p.n = "foo"
`, "type 'string' is not assignable to type 'number' (field 'n')")
	assertCodegenError(t, `
const re = /a/g
re.lastIndex = "1"
`, "type 'string' is not assignable to type 'number' (field 'lastIndex')")
	assertOutput(t, `
interface P { n: number; s: string; f: number }
const p: P = { n: 1, s: "a", f: 0.5 }
p.n = 7
p.f = 2
p.s = "b"
const re = /a/g
re.lastIndex = 2
console.log(p.n, p.f, p.s, "aaa".replace(re, "X"), re.lastIndex)
re.lastIndex = 2
console.log(re.test("aaa"), re.lastIndex, re.test("aaa"), re.lastIndex)
`, "7 2 b XXX 0\ntrue 3 false 0\n")
}

// A binding whose initializer produces no value is undefined; an IIFE callee
// reached `alloca void` instead, a second void result could not be assigned,
// and console.log printed no token for one.
func TestE2EVoidInitializerIsUndefined(t *testing.T) {
	assertOutput(t, `
function nothing(): void {}
const a = nothing();
console.log(a);
console.log(a === undefined, typeof a);
var b = (function foo() { "bogus"; })();
console.log(b === undefined);
let c = nothing();
c = nothing();
`+"console.log(c, `${c}`, String(c));"+`
const d = [1, 2].forEach((x) => x);
console.log(d);
function pass(): void { return nothing(); }
console.log(pass());
console.log(1, nothing(), 2);
`, "undefined\ntrue undefined\ntrue\nundefined undefined undefined\nundefined\nundefined\n1 undefined 2\n")
}
