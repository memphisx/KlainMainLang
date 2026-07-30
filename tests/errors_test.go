package tests

import (
	"os/exec"
	"strings"
	"testing"
)

// --- Error subtypes / tagged errors (TDD-00013 Option A) ---

func TestE2EErrorSubtypeMessageAndName(t *testing.T) {
	assertOutput(t, `
const e = new TypeError('bad type')
console.log(e.message)
console.log(e.name)
`, "bad type\nTypeError")
}

func TestE2EErrorSubtypeDefaultMessage(t *testing.T) {
	// No-arg new XError() defaults .message to the kind's own name, the same
	// way plain new Error() has always defaulted .message to "Error".
	assertOutput(t, `
const e = new RangeError()
console.log(e.message)
console.log(e.name)
`, "RangeError\nRangeError")
}

func TestE2EPlainErrorNameField(t *testing.T) {
	assertOutput(t, `
const e = new Error('oops')
console.log(e.name)
`, "Error")
}

func TestE2EInstanceOfMatchingKind(t *testing.T) {
	// This compiler's console.log(bool) convention prints 0/1, not
	// "true"/"false" — see TestE2EInstanceOfStaticTrue (classes_test.go) for
	// the existing precedent this follows.
	assertOutput(t, `
const e = new TypeError('bad')
console.log(e instanceof TypeError)
`, "1")
}

func TestE2EInstanceOfMismatchedKind(t *testing.T) {
	assertOutput(t, `
const e = new TypeError('bad')
console.log(e instanceof RangeError)
`, "0")
}

func TestE2EInstanceOfBaseErrorMatchesEveryKind(t *testing.T) {
	assertOutput(t, `
console.log(new Error('x') instanceof Error)
console.log(new TypeError('x') instanceof Error)
console.log(new RangeError('x') instanceof Error)
console.log(new SyntaxError('x') instanceof Error)
console.log(new EvalError('x') instanceof Error)
console.log(new URIError('x') instanceof Error)
console.log(new ReferenceError('x') instanceof Error)
`, "1\n1\n1\n1\n1\n1\n1")
}

func TestE2ECatchNarrowingByInstanceOf(t *testing.T) {
	assertOutput(t, `
function risky(kind: number): void {
  if (kind === 0) { throw new TypeError('type problem') }
  throw new RangeError('range problem')
}
try {
  risky(0)
} catch (e) {
  if (e instanceof TypeError) {
    console.log('type: ' + e.message)
  } else if (e instanceof RangeError) {
    console.log('range: ' + e.message)
  }
}
try {
  risky(1)
} catch (e) {
  if (e instanceof TypeError) {
    console.log('type: ' + e.message)
  } else if (e instanceof RangeError) {
    console.log('range: ' + e.message)
  }
}
`, "type: type problem\nrange: range problem")
}

func TestE2EThrownPrimitiveIsInstanceOfErrorNotSubtype(t *testing.T) {
	// A thrown non-object value is wrapped as a base Error (kind 0), never a
	// specific subtype — emitThrow's manual wrap path always tags kind 0.
	assertOutput(t, `
try {
  throw 'plain string'
} catch (e) {
  console.log(e instanceof Error)
  console.log(e instanceof TypeError)
  console.log(e.message)
  console.log(e.name)
}
`, "1\n0\nplain string\nError")
}

func TestE2EUncaughtErrorSubtypePrintsMessage(t *testing.T) {
	// Regression guard for runtime_exceptions.go's hand-written uncaught-path
	// GEP, which reads errorObjType's message field by hardcoded index —
	// easy to silently desync from errorObjType's layout. The uncaught
	// message goes to stdout (@printf, not @dprintf) — existing behavior,
	// unrelated to this change.
	bin := buildBinary(t, `throw new TypeError('boom')`)
	cmd := exec.Command(bin)
	var stdout strings.Builder
	cmd.Stdout = &stdout
	err := cmd.Run()
	if _, ok := err.(*exec.ExitError); !ok {
		t.Fatalf("expected a non-zero exit for an uncaught error, got: %v", err)
	}
	want := "Uncaught: boom\n"
	if stdout.String() != want {
		t.Fatalf("stdout = %q, want %q", stdout.String(), want)
	}
}
