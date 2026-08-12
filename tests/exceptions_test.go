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
	assertOutput(t, `
try {
  console.log(10 / 0)
} catch (e) {
  console.log('err: ' + e.message)
}
try {
  console.log(10 % 0)
} catch (e) {
  console.log('err: ' + e.message)
}
let x: number = 10
try {
  x /= 0
} catch (e) {
  console.log('err: ' + e.message)
}
`, "err: Division by zero\nerr: Division by zero\nerr: Division by zero")
}

// TestE2EDivisionOverflowThrows covers LLVM sdiv/srem's second documented
// UB case (distinct from a zero divisor): dividing a signed integer type's
// minimum representable value by -1, whose mathematical result overflows
// back into the same width (i64 MIN / -1 == 2^63, which doesn't fit in an
// i64). Unsigned division has no equivalent case (no negative divisor to
// trigger it), so only /, %, and /= on a signed value are exercised here.
func TestE2EDivisionOverflowThrows(t *testing.T) {
	assertOutput(t, `
const minVal: number = -9223372036854775808
const negOne: number = -1
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
let x: number = -9223372036854775808
try {
  x /= negOne
} catch (e) {
  console.log('err: ' + e.message)
}
console.log(10 / -1)
console.log(-9223372036854775807 / -1)
`, "err: Division overflow\nerr: Division overflow\nerr: Division overflow\n-10\n9223372036854775807")
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
