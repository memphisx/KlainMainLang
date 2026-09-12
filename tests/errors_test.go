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

// AggregateError construction (both an array literal and an array variable — the
// latter exercises the resolver rewriting the new `Errors` argument), its
// .name/.message/.errors surface, instanceof, and catch round-trip (ADR-00248).
func TestE2EAggregateErrorConstruction(t *testing.T) {
	assertOutput(t, `
const errs = [new Error("a"), new Error("b")]
const agg = new AggregateError(errs, "boom")
console.log(agg.name)
console.log(agg.message)
console.log(agg.errors.length)
console.log(agg.errors[0].message)
console.log(agg instanceof AggregateError)
console.log(agg instanceof Error)
const plain = new Error("p")
console.log(plain instanceof AggregateError)
try {
  throw new AggregateError([new Error("x")], "w")
} catch (e) {
  console.log(e.name + ":" + e.errors[0].message)
}
`, "AggregateError\nboom\n2\na\ntrue\ntrue\nfalse\nAggregateError:x")
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
`, "true")
}

func TestE2EInstanceOfMismatchedKind(t *testing.T) {
	assertOutput(t, `
const e = new TypeError('bad')
console.log(e instanceof RangeError)
`, "false")
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
`, "true\ntrue\ntrue\ntrue\ntrue\ntrue\ntrue")
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

func TestE2EThrownPrimitiveKeepsItsType(t *testing.T) {
	// TDD-00202: a thrown primitive is caught as that primitive (not wrapped in
	// an Error) — matching Node: `throw 'x'` → e is the string "x", not an
	// Error, so `e instanceof Error` is false and `typeof e` is "string".
	assertOutput(t, `
try {
  throw 'plain string'
} catch (e) {
  console.log(e instanceof Error)
  console.log(e instanceof TypeError)
  console.log(typeof e)
  console.log(e === 'plain string')
}
`, "false\nfalse\nstring\ntrue")
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

func TestE2EErrorToString(t *testing.T) {
	// error.toString() / String(err) / `${err}` render "name: message" (JS's
	// Error.prototype.toString), or just the name when the message is empty
	// (ADR-00846).
	assertOutput(t, `
console.log(new Error("x").toString())
console.log(String(new RangeError("r")))
console.log(` + "`" + `err: ${new TypeError("bad")}` + "`" + `)
const e = new Error("boom")
console.log("" + e)
console.log(new Error("").toString())
`, "Error: x\nRangeError: r\nerr: TypeError: bad\nError: boom\nError")
}
