package tests

import (
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
