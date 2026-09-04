package tests

import "testing"

// String-literal-search replace()/replaceAll() with a function replacer
// (ADR-00697): the callback receives (match, offset, string), matching Node.
func TestE2EStringLiteralReplaceCallback(t *testing.T) {
	assertOutput(t, `
console.log("hello".replace("l", (m) => m.toUpperCase()))
console.log("hello".replaceAll("l", (m) => m.toUpperCase()))
console.log("a-b-c".replaceAll("-", (m, o) => "/" + o + "/"))
console.log("cat".replace("a", (m, o, s) => "[" + s + "]"))
console.log("hello".replace("l", "L"))
console.log("hello".replaceAll("l", "L"))
`, "heLlo\nheLLo\na/1/b/3/c\nc[cat]t\nheLlo\nheLLo")
}

// A named untyped callback: params default to (string, number, string).
func TestE2EStringLiteralReplaceCallbackNamed(t *testing.T) {
	assertOutput(t, `
const shout = (m: string) => m + "!"
console.log("one two one".replaceAll("one", shout))
console.log("no match".replaceAll("zzz", (m) => m.toUpperCase()))
`, "one! two one!\nno match")
}
