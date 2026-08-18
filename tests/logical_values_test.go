package tests

import "testing"

// --- `&&`/`||` value-preserving under -compat=js (TDD-00075/ADR-00220):
// `a && b` yields `b` or the falsy `a`; `a || b` yields `a` or `b` — the actual
// operand values, not a bool. Under -compat=strict (default) they stay bool. ---

func TestE2ELogicalValuePreservingCompatJS(t *testing.T) {
	assertOutputCompatJS(t, `
console.log(5 && 3)
console.log(0 || 7)
console.log(5 || 3)
console.log(0 && 3)
console.log("" || "fallback")
console.log("hi" && "world")
const timeout = 0
console.log(timeout || 30)
console.log(true && false)
`, "3\n7\n5\n0\nfallback\nworld\n30\nfalse")
}

func TestE2ELogicalStayBoolStrict(t *testing.T) {
	// -compat=strict (default) is unchanged: `&&`/`||` yield a bool (ADR-00186).
	assertOutput(t, `
console.log(5 && 3)
console.log(0 || 7)
console.log(true && false)
`, "true\ntrue\nfalse")
}
