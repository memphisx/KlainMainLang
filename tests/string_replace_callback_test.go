package tests

import (
	"testing"
)

// --- replace()/replaceAll() replacer callback with untyped parameters
// (ADR-00695). The callback signature is (match, cap1..capN, offset, string)
// where N is the pattern's capture-group count (ADR-00922); with no capture
// groups it degenerates to (match, offset, string). The matched substring and
// every capture are strings, offset a number, and the last the whole subject
// string. Untyped arrow-function parameters must default to those types, not
// to `number`. ---

func TestE2EReplaceCallbackUntypedMatchIdentity(t *testing.T) {
	assertOutput(t, `
console.log("hello".replace(/l/g, (m) => m))
`, "hello")
}

func TestE2EReplaceCallbackUntypedConstantString(t *testing.T) {
	assertOutput(t, `
console.log("hello".replace(/l/g, (m) => "X"))
`, "heXXo")
}

func TestE2EReplaceCallbackUntypedStringMethod(t *testing.T) {
	assertOutput(t, `
console.log("hello".replace(/l/g, (m) => m.toUpperCase()))
`, "heLLo")
}

func TestE2EReplaceCallbackUntypedMatchAndOffset(t *testing.T) {
	assertOutput(t, `
console.log("hello".replace(/l/g, (m, o) => `+"`"+`${m}@${o}`+"`"+`))
`, "hel@2l@3o")
}

func TestE2EReplaceCallbackUntypedAllThreeParams(t *testing.T) {
	assertOutput(t, `
console.log("a1b2".replace(/[0-9]/g, (m, o, s) => `+"`"+`[${m}/${o}/${s.length}]`+"`"+`))
`, "a[1/1/4]b[2/3/4]")
}

func TestE2EReplaceAllCallbackUntypedMatch(t *testing.T) {
	assertOutput(t, `
console.log("a.b.c".replaceAll(/\./g, (m) => m + "-"))
`, "a.-b.-c")
}

// --- Capture groups passed positionally to the replacer (ADR-00922). Real JS
// invokes it as (match, cap1..capN, offset, string) where N is the pattern's
// capture count; when the pattern is a regex literal (inline or const-bound) N
// is statically known. (Test262 built-ins/String/prototype/replace/
// S15.5.4.11_A4_T*.) ---

func TestE2EReplaceCallbackCaptureGroups(t *testing.T) {
	assertOutput(t, `
console.log("abc12 def34".replace(/([a-z]+)([0-9]+)/, (m: string, g1: string, g2: string) => g2 + g1))
`, "12abc def34")
}

func TestE2EReplaceAllCallbackCaptureGroups(t *testing.T) {
	assertOutput(t, `
console.log("abc12 def34".replace(/([a-z]+)([0-9]+)/g, (m: string, g1: string, g2: string) => g2 + g1))
`, "12abc 34def")
}

// A const-bound regex literal is traced to its capture count.
func TestE2EReplaceCallbackCaptureGroupsConstPattern(t *testing.T) {
	assertOutput(t, `
const p = /([a-z]+)([0-9]+)/;
console.log("abc12".replace(p, (m: string, g1: string, g2: string) => g1 + "-" + g2))
`, "abc-12")
}

// After the capture groups come offset and the whole string, in that order.
func TestE2EReplaceCallbackCaptureThenOffset(t *testing.T) {
	assertOutput(t, `
console.log("x5".replace(/(\d)/, (m: string, d: string, off: number) => d + "@" + off))
`, "x5@1")
}

// A replacer that returns a non-string has its result ToString'd (ADR-00922) —
// previously a number result was reinterpreted as a pointer (invalid IR).
func TestE2EReplaceCallbackNumericReturn(t *testing.T) {
	assertOutput(t, `
console.log("a1b".replace(/1/, () => 9))
`, "a9b")
}
