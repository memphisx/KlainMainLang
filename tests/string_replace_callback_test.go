package tests

import (
	"testing"
)

// --- replace()/replaceAll() replacer callback with untyped parameters
// (ADR-00695). The callback signature is (match, offset, string): the
// first argument is the matched substring (a string), the middle offset a
// number, and the last the whole subject string. Untyped arrow-function
// parameters must default to those types, not to `number`. ---

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
console.log("hello".replace(/l/g, (m, o) => ` + "`" + `${m}@${o}` + "`" + `))
`, "hel@2l@3o")
}

func TestE2EReplaceCallbackUntypedAllThreeParams(t *testing.T) {
	assertOutput(t, `
console.log("a1b2".replace(/[0-9]/g, (m, o, s) => ` + "`" + `[${m}/${o}/${s.length}]` + "`" + `))
`, "a[1/1/4]b[2/3/4]")
}

func TestE2EReplaceAllCallbackUntypedMatch(t *testing.T) {
	assertOutput(t, `
console.log("a.b.c".replaceAll(/\./g, (m) => m + "-"))
`, "a.-b.-c")
}
