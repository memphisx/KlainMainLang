package tests

import "testing"

// Common TypeScript surface syntax that this compiler parses and erases rather
// than rejecting — surfaced as high-frequency false-rejects by the TS
// accept/reject oracle (ADR-00372): the `debugger` statement, the `readonly`
// array/tuple type modifier, and a leading `this` parameter.

func TestE2EDebuggerStatement(t *testing.T) {
	// `debugger;` is a no-op in AOT-compiled native output (no attached
	// inspector), matching real JS with no debugger attached.
	assertOutput(t, `
const x = 1
debugger
console.log(x)
if (x) { debugger }
console.log(x + 1)
`, "1\n2")
}

func TestE2EReadonlyArrayType(t *testing.T) {
	assertOutput(t, `
const a: readonly number[] = [1, 2, 3]
console.log(a[0] + a[2])
`, "4")
}

func TestE2EReadonlyArrayParam(t *testing.T) {
	assertOutput(t, `
function sum(xs: readonly number[]): number {
  let s = 0
  for (const x of xs) s += x
  return s
}
console.log(sum([4, 5, 6]))
`, "15")
}

func TestE2EReadonlyTupleType(t *testing.T) {
	assertOutput(t, `
const t: readonly [number, string] = [7, "z"]
console.log(t[0])
`, "7")
}

func TestE2EThisParameter(t *testing.T) {
	// A leading `this` parameter is erased; the remaining parameters bind by
	// their real positions.
	assertOutput(t, `
function describe(this: void, name: string, n: number): string {
  return name + ":" + n
}
console.log(describe("x", 5))
`, "x:5")
}

func TestE2EThisParameterInExpression(t *testing.T) {
	assertOutput(t, `
const f = function (this: unknown, a: number): number { return a * 2 }
console.log(f(21))
`, "42")
}
