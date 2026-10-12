package tests

import "testing"

// indexOf / lastIndexOf / includes over a string array holding null compare
// the null by identity and never pass it to strcmp (ADR-01585).
func TestE2EArraySearchNullString(t *testing.T) {
	assertOutput(t, `
console.log([null].indexOf(null), [null].lastIndexOf(null), [null].includes(null))
const s: (string | null)[] = ["a", null, "b"]
console.log(s.indexOf("b"), s.indexOf(null), s.lastIndexOf("a"), s.includes("c"))
`, "0 0 true\n2 1 0 false")
}
