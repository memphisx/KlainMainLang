package tests

import (
	"strings"
	"testing"
)

// Regression: inferExprType must be linear in expression size. It used to
// re-infer both operands of a binary expression once for the bigint check and
// again in each operator branch, so a deep left-associative chain like
// `a + b + c + ...` cost O(2^depth) — enough to peg a core for minutes on a
// ~40-term chain (found as an in-process hang while running the Test262
// corpus). This compiles a 64-term concatenation; pre-fix it would not finish,
// so the test itself is the guard (it would time out), and the runtime result
// confirms the folded type is still correct. See ADR-00226.
func TestE2EDeepConcatInferenceLinear(t *testing.T) {
	terms := make([]string, 64)
	for i := range terms {
		terms[i] = `"x"`
	}
	src := "const s = " + strings.Join(terms, " + ") + "\nconsole.log(s.length)"
	assertOutput(t, src, "64")
}

// The same shape for a numeric `+` chain, which infers i64 rather than string.
func TestE2EDeepNumericInferenceLinear(t *testing.T) {
	terms := make([]string, 64)
	for i := range terms {
		terms[i] = "1"
	}
	src := "const n = " + strings.Join(terms, " + ") + "\nconsole.log(n)"
	assertOutput(t, src, "64")
}
