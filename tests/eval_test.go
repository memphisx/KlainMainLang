package tests

import (
	"testing"
)

// --- Static-string eval (TDD-00046 static subset / ADR-00198) ---
//
// A compile-time-constant `eval("<expression>")` is compiled through this
// compiler's own parser + codegen, in place — its value is eval's result.
// Anything outside that subset (a dynamic argument, a non-expression, or a
// string that doesn't parse) is a clean compile error, never a runtime
// throw — see the ADR for why (the conformance harness would otherwise mistake
// any throw for the SyntaxError a negative test expects).

func TestE2EEvalStaticExpression(t *testing.T) {
	assertOutput(t, `
console.log(eval("2 ** 10"))
console.log(eval("1 + 2 * 3"))
console.log(eval("'a' + 'b'"))
console.log(eval("1 === 1"))
`, "1024\n7\nab\ntrue")
}

func TestE2EEvalStaticExpressionAsValue(t *testing.T) {
	assertOutput(t, `
const n: number = eval("6 * 7")
console.log(n + 1)
`, "43")
}

func TestE2EEvalNonExpressionRejected(t *testing.T) {
	// A statement/declaration string is not a supported eval (would need
	// scope injection); a clean compile error, not a runtime throw.
	if _, err := parseAndCompile(`eval("var x = 1")`); err == nil {
		t.Fatal("expected a compile error for eval of a statement, got none")
	}
}

func TestE2EEvalInvalidSyntaxRejected(t *testing.T) {
	if _, err := parseAndCompile(`eval("this is not valid @#")`); err == nil {
		t.Fatal("expected a compile error for eval of invalid syntax, got none")
	}
}

func TestE2EEvalDynamicArgumentRejected(t *testing.T) {
	// A non-constant argument needs the embedded-engine path (TDD-00046),
	// still a clean compile error.
	if _, err := parseAndCompile(`const s = "1+2"; eval(s)`); err == nil {
		t.Fatal("expected a compile error for eval of a dynamic string, got none")
	}
}
