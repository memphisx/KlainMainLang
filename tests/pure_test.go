package tests

import "testing"

// TDD-00128: `/** @pure */` enforcement — a genuinely pure function (local
// mutation, fresh objects, calling other @pure functions, mapping with a pure
// callback) compiles and runs unchanged (enforcement-only, zero codegen).
func TestE2EPurePositive(t *testing.T) {
	assertOutput(t, `
/** @pure */
function sq(n: number): number { let r = n * n; return r }
/** @pure */
function sumOf(xs: number[]): number {
  let t = 0
  for (const x of xs) { t = t + x }
  return t
}
/** @pure */
const scale = (n: number, by: number): number => n * by
/** @pure */
function dbl(n: number): number { return n * 2 }
/** @pure */
function twiceEach(xs: number[]): number[] { return xs.map((x) => dbl(x)) }
console.log(sq(6), sumOf([10, 20, 12]), scale(7, 3))
console.log(twiceEach([1, 2, 3]).join(","))
`, "36 42 21\n2,4,6")
}

func TestE2EPureRejectsParamReassign(t *testing.T) {
	mustCompileError(t, `
/** @pure */
function f(x: number): number { x = 5; return x }
`, "reassigns parameter 'x'")
}

func TestE2EPureRejectsParamFieldMutation(t *testing.T) {
	mustCompileError(t, `
/** @pure */
function f(o: { a: number }): number { o.a = 1; return o.a }
`, "mutates a location reachable from parameter 'o'")
}

func TestE2EPureRejectsParamMutatingMethod(t *testing.T) {
	mustCompileError(t, `
/** @pure */
function f(xs: number[]): number { xs.push(1); return 0 }
`, "mutating method '.push()' on parameter 'xs'")
}

func TestE2EPureRejectsGlobalMutation(t *testing.T) {
	mustCompileError(t, `
let g: number = 0
/** @pure */
function f(): number { g = 1; return g }
`, "assigns to 'g', which is not declared inside it")
}

func TestE2EPureRejectsIO(t *testing.T) {
	mustCompileError(t, `
/** @pure */
function f(n: number): number { console.log(n); return n }
`, "calls 'console.log', which performs I/O")
}

func TestE2EPureRejectsNondeterminism(t *testing.T) {
	mustCompileError(t, `
/** @pure */
function f(): number { return Math.random() }
`, "Math.random' (nondeterministic)")
}

func TestE2EPureRejectsNewDate(t *testing.T) {
	mustCompileError(t, `
/** @pure */
function f(): number { const d = new Date(); return 0 }
`, "new Date()` reads the current time")
}

func TestE2EPureContagion(t *testing.T) {
	mustCompileError(t, `
function imp(): number { console.log(1); return 1 }
/** @pure */
function f(): number { return imp() }
`, "calls 'imp', which is not @pure")
}

// Contagion is transitive: a @pure function calling a @pure function that is
// itself impure surfaces the inner violation.
func TestE2EPureContagionTransitive(t *testing.T) {
	mustCompileError(t, `
/** @pure */
function inner(): number { console.log(1); return 1 }
/** @pure */
function outer(): number { return inner() }
`, "calls 'console.log', which performs I/O")
}

func TestE2EPureRejectsOnAsync(t *testing.T) {
	mustCompileError(t, `
/** @pure */
async function f(n: number): Promise<number> { return n }
`, "@pure cannot be applied to an async")
}
