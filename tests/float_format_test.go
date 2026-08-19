package tests

import "testing"

// JS-faithful float-to-string: shortest round-trip per ECMAScript
// Number::toString (TDD-00080). Previously bare %g truncated to 6 significant
// digits, so 1.1 + 2.2 printed "3.3".

func TestE2EFloatShortestRoundTrip(t *testing.T) {
	assertOutput(t, `
/** @type {float64} */
const a = 1.1 + 2.2
console.log(a)
/** @type {float64} */
const b = 0.1
console.log(b)
/** @type {float64} */
const c = 123456789.123
console.log(c)
`, "3.3000000000000003\n0.1\n123456789.123")
}

// Integer-valued floats print without a decimal point; small/plain decimals
// print exactly.
func TestE2EFloatIntegerValued(t *testing.T) {
	assertOutput(t, `
/** @type {float64} */
const a = 100.0
console.log(a)
/** @type {float64} */
const b = 2.0
console.log(b)
/** @type {float64} */
const c = 1000000.0
console.log(c)
/** @type {float64} */
const d = 0.000001
console.log(d)
`, "100\n2\n1000000\n0.000001")
}

// NaN / Infinity / -Infinity and a negative value.
func TestE2EFloatSpecialValues(t *testing.T) {
	assertOutput(t, `
console.log(NaN)
console.log(Infinity)
console.log(-Infinity)
/** @type {float64} */
const n = -3.5
console.log(n)
`, "NaN\nInfinity\n-Infinity\n-3.5")
}

// Exponential notation past the ECMAScript thresholds (n > 21, n <= -6).
func TestE2EFloatExponential(t *testing.T) {
	assertOutput(t, `
/** @type {float64} */
const huge = 1000000000000.0 * 1000000000000.0
console.log(huge)
/** @type {float64} */
const tiny = 1.0 / 10000000.0
console.log(tiny)
`, "1e+24\n1e-7")
}

// Template-literal interpolation of a float uses the same formatter (full
// precision, not the old 6-digit truncation).
func TestE2EFloatTemplateLiteral(t *testing.T) {
	assertOutput(t, `
/** @type {float64} */
const pi = 3.141592653589793
console.log(`+"`pi is ${pi}`"+`)
`, "pi is 3.141592653589793")
}
