package tests

import (
	"strings"
	"testing"
)

// Error options-bag cause: `new Error(msg, { cause })` stores the cause as
// `any` — a primitive keeps its value/typeof, an Error cause keeps its shape,
// an absent cause reads undefined, and the cause rides through throw/catch.
// Verified byte-exact against Node.
func TestE2EErrorCauseBasics(t *testing.T) {
	assertOutput(t, `
const e = new Error("boom", { cause: "root" })
console.log(e.message)
console.log(e.cause)
const e2 = new TypeError("t", { cause: 42 })
console.log(e2.cause)
const inner = new Error("inner")
const e3 = new Error("outer", { cause: inner })
console.log(e3.cause instanceof Error)
console.log(String(e3.cause))
const plain = new Error("nocause")
console.log(plain.cause)
try { throw e } catch (err) { console.log((err as Error).cause) }
`, "boom\nroot\n42\ntrue\nError: inner\nundefined\nroot")
}

// A non-`{ cause }` second argument is a clean parse-time rejection.
func TestE2EErrorCauseRejectsNonLiteral(t *testing.T) {
	_, err := parseAndCompile(`
const opts = { cause: 1 }
const e = new Error("x", opts)
`)
	if err == nil {
		t.Fatal("expected a rejection for a non-literal error-options argument, got none")
	}
	if !strings.Contains(err.Error(), "{ cause: <expr> }") {
		t.Fatalf("expected the { cause } options error, got: %v", err)
	}
}

// An AggregateError never carries a cause here; reading `.cause` on one is
// bounds-safe and reads undefined (the shared errorObjType default).
func TestE2EErrorCauseAggregateReadsUndefined(t *testing.T) {
	assertOutput(t, `
const agg = new AggregateError([new Error("a")], "many")
console.log(agg.cause)
console.log(agg.errors.length)
`, "undefined\n1")
}
