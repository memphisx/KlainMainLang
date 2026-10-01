package tests

import "testing"

// A parameter default applies whenever the argument is undefined at run
// time, not only when it is omitted or a literal `undefined`: functions,
// methods, constructors and closures (ADR-01279).
func TestE2EDefaultParamRuntimeUndefined(t *testing.T) {
	assertOutput(t, `
function f(o: number = 5): number { return o }
function h(s: string = 'd'): string { return s }
function k(a?: number, o: any = {}): string { return typeof o }
let v: number | undefined = undefined
let w: string | undefined
const u: any = undefined
console.log(f(v), h(w), f(u), h(u), k(1, u), f(3))
class A {
  n: number
  constructor(n: number = 7) { this.n = n }
  m(o: number = 5): number { return o }
  static s(o: string = 'd'): string { return o }
}
const a = new A(v)
console.log(a.n, a.m(v), A.s(w), a.m(2))
const c = (o: number = 9): number => o
const g = function (o: any = 'x') { return o }
console.log(c(v), c(undefined), g(u), g(0))
let calls = 0
function side(): number { calls++; return 1 }
function once(o: number = side()): number { return o }
console.log(once(4), calls, once(v), calls)
`, `5 d 5 d object 3
7 5 d 2
9 9 x 0
4 0 1 1`)
}

// A default referencing a captured variable is filled in the closure's body;
// an undefined argument clears its presence bit at run time.
func TestE2EDefaultParamRuntimeUndefinedBodyFilled(t *testing.T) {
	assertOutput(t, `
function mk() {
  const base = 10
  return (a: number, b: number = base + 1): number => a + b
}
const f = mk()
let v: number | undefined
const u: any = undefined
console.log(f(1), f(1, undefined), f(1, u), f(1, 2), f(1, v))
function mk2() { const d = 'dd'; return (s: string = d): string => s }
const g = mk2()
let w: string | undefined
console.log(g(), g(w), g('x'))
`, "12 12 12 3 12\ndd dd x")
}
