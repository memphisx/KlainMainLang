package tests

import (
	"strings"
	"testing"
)

// --- try / catch / throw ---

func TestE2ETryCatchBasic(t *testing.T) {
	assertOutput(t, `
function divide(a: number, b: number): number {
  if (b === 0) { throw new Error('division by zero') }
  return a / b
}
try {
  console.log(divide(10, 2))
} catch (e) {
  console.log('err: ' + e.message)
}
try {
  console.log(divide(10, 0))
} catch (e) {
  console.log('err: ' + e.message)
}
`, "5\nerr: division by zero")
}

func TestE2EDivisionByZeroThrows(t *testing.T) {
	// `number` is an IEEE-754 double (TDD-00123), so number division by zero
	// yields Infinity/NaN as in JS — it does NOT throw. Integer-typed division
	// (the explicit escape-hatch types) still traps on a zero divisor.
	assertOutput(t, `
console.log(10 / 0)
console.log(-10 / 0)
console.log(10 % 0)
let a: int64 = 10
let b: int64 = 0
try {
  console.log(a / b)
} catch (e) {
  console.log('err: ' + e.message)
}
try {
  console.log(a % b)
} catch (e) {
  console.log('err: ' + e.message)
}
`, "Infinity\n-Infinity\nNaN\nerr: Division by zero\nerr: Division by zero")
}

// TestE2EDivisionOverflowThrows covers LLVM sdiv/srem's second documented
// UB case (distinct from a zero divisor): dividing a signed integer type's
// minimum representable value by -1, whose mathematical result overflows
// back into the same width (i64 MIN / -1 == 2^63, which doesn't fit in an
// i64). Unsigned division has no equivalent case (no negative divisor to
// trigger it), so only /, %, and /= on a signed value are exercised here.
func TestE2EDivisionOverflowThrows(t *testing.T) {
	// This trap is an integer-division concern — `number` is now a float
	// (TDD-00123) with no such overflow — so it's exercised with the explicit
	// `int64` escape-hatch type.
	assertOutput(t, `
let minVal: int64 = -9223372036854775808
let negOne: int64 = -1
try {
  console.log(minVal / negOne)
} catch (e) {
  console.log('err: ' + e.message)
}
try {
  console.log(minVal % negOne)
} catch (e) {
  console.log('err: ' + e.message)
}
let x: int64 = -9223372036854775808
try {
  x /= negOne
} catch (e) {
  console.log('err: ' + e.message)
}
let y: int64 = 10
console.log(y / negOne)
let z: int64 = -100
console.log(z / negOne)
`, "err: Division overflow\nerr: Division overflow\nerr: Division overflow\n-10\n100")
}

func TestE2EOptionalCatchBinding(t *testing.T) {
	assertOutput(t, `
try {
  throw new Error('boom')
} catch {
  console.log('caught, no binding')
}
try {
  console.log(42)
} catch {
  console.log('should not reach')
}
`, "caught, no binding\n42")
}

func TestE2EDestructuredCatchBinding(t *testing.T) {
	assertOutput(t, `
try {
  throw new Error('boom')
} catch ({ message, name }) {
  console.log(message)
  console.log(name)
}
`, "boom\nError")
}

func TestE2EDestructuredCatchBindingRenamed(t *testing.T) {
	assertOutput(t, `
try {
  throw new TypeError('bad type')
} catch ({ message: msg, name: kind }) {
  console.log(msg)
  console.log(kind)
}
`, "bad type\nTypeError")
}

func TestE2EDestructuredCatchBindingScopedCorrectly(t *testing.T) {
	// The destructured local must not leak into, or shadow across, the
	// enclosing scope — same as any other object destructuring.
	assertOutput(t, `
const message: string = 'outer'
try {
  throw new Error('inner boom')
} catch ({ message }) {
  console.log(message)
}
console.log(message)
`, "inner boom\nouter")
}

func TestE2EDestructuredCatchBindingUnknownFieldRejected(t *testing.T) {
	_, err := parseAndCompile(`
try {
  throw new Error('boom')
} catch ({ notAField }) {
  console.log(notAField)
}
`)
	if err == nil {
		t.Fatal("expected a compile error for an unknown field in a destructured catch binding, got none")
	}
	if !strings.Contains(err.Error(), "object has no field 'notAField'") {
		t.Fatalf("expected \"object has no field 'notAField'\", got: %v", err)
	}
}

func TestE2ETryCatchNoThrow(t *testing.T) {
	assertOutput(t, `
try {
  const x: number = 42
  console.log(x)
} catch (e) {
  console.log('should not reach')
}
`, "42")
}

func TestE2ETryCatchNested(t *testing.T) {
	assertOutput(t, `
function inner(): void {
  throw new Error('from inner')
}
function outer(): void {
  try {
    inner()
  } catch (e) {
    console.log('outer caught: ' + e.message)
    throw new Error('rethrown')
  }
}
try {
  outer()
} catch (e) {
  console.log('top caught: ' + e.message)
}
`, "outer caught: from inner\ntop caught: rethrown")
}

func TestE2ETryFinally(t *testing.T) {
	assertOutput(t, `
let ran: number = 0
try {
  ran = 1
} catch (e) {
  ran = 2
} finally {
  console.log(ran)
}
`, "1")
}

// TestE2EFinallyRunsOnReturn: a `return` inside try/catch must still run the
// finally block (before ADR-00191 the return's `ret` bypassed it entirely).
func TestE2EFinallyRunsOnReturn(t *testing.T) {
	assertOutput(t, `
function f(): number {
  try {
    console.log("try")
    return 1
  } finally {
    console.log("finally")
  }
}
console.log(f())
`, "try\nfinally\n1")
}

// TestE2EFinallyRunsOnReturnInCatch: a return from the catch block runs finally.
func TestE2EFinallyRunsOnReturnInCatch(t *testing.T) {
	assertOutput(t, `
function f(): number {
  try {
    throw new Error("x")
  } catch (e) {
    return 2
  } finally {
    console.log("finally")
  }
}
console.log(f())
`, "finally\n2")
}

// TestE2EFinallyReturnOverridesTryReturn: a return in finally wins over the
// try's pending return, matching JS.
func TestE2EFinallyReturnOverridesTryReturn(t *testing.T) {
	assertOutput(t, `
function f(): number {
  try {
    return 1
  } finally {
    return 9
  }
}
console.log(f())
`, "9")
}

// TestE2ENestedFinallyRunInnermostFirstOnReturn: a return from an inner try
// runs the inner finally, then the outer.
func TestE2ENestedFinallyRunInnermostFirstOnReturn(t *testing.T) {
	assertOutput(t, `
function f(): number {
  try {
    try {
      return 5
    } finally {
      console.log("inner")
    }
  } finally {
    console.log("outer")
  }
}
console.log(f())
`, "inner\nouter\n5")
}

// TestE2EFinallyValueCapturedBeforeFinally: the returned scalar is captured
// before finally runs, so a finally mutation of the source variable doesn't
// change the returned value.
func TestE2EFinallyValueCapturedBeforeFinally(t *testing.T) {
	assertOutput(t, `
function f(): number {
  let x = 1
  try {
    return x
  } finally {
    x = 99
  }
}
console.log(f())
`, "1")
}

// TestE2EFinallyRunsOnBreak: a break out of a loop runs the loop-nested finally
// but not a finally outside the loop.
func TestE2EFinallyRunsOnBreak(t *testing.T) {
	assertOutput(t, `
try {
  for (let i = 0; i < 3; i++) {
    try {
      if (i === 1) { break }
      console.log("body " + i)
    } finally {
      console.log("innerfin " + i)
    }
  }
} finally {
  console.log("outerfin")
}
`, "body 0\ninnerfin 0\ninnerfin 1\nouterfin")
}

// TestE2EFinallyRunsOnContinue: continue runs the inner finally each iteration,
// not the outer one.
func TestE2EFinallyRunsOnContinue(t *testing.T) {
	assertOutput(t, `
for (let i = 0; i < 3; i++) {
  try {
    if (i === 1) { continue }
    console.log("body " + i)
  } finally {
    console.log("fin " + i)
  }
}
`, "body 0\nfin 0\nfin 1\nbody 2\nfin 2")
}

// TestE2EFinallyRunsOnLabeledBreak: a labeled break through a finally runs it.
func TestE2EFinallyRunsOnLabeledBreak(t *testing.T) {
	assertOutput(t, `
outer: for (let i = 0; i < 2; i++) {
  try {
    if (i === 0) { break outer }
  } finally {
    console.log("fin " + i)
  }
}
console.log("done")
`, "fin 0\ndone")
}

func TestE2EThrowInCatch(t *testing.T) {
	assertOutput(t, `
try {
  throw new Error('original')
} catch (e) {
  console.log('caught: ' + e.message)
}
console.log('done')
`, "caught: original\ndone")
}

// --- new Error() without a type annotation ---

func TestE2EUntypedNewError(t *testing.T) {
	// Regression guard: found alongside the Date work — an untyped `const`
	// initialized from `new Error(...)` previously fell back to a plain i64
	// default (the same missing-case bug that affected Date), so `.message`
	// access failed with "field access on non-object".
	assertOutput(t, `
const e = new Error('oops')
console.log(e.message)
`, "oops")
}
