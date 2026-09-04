package tests

import "testing"

// --- console/util formatting fidelity: printf-style substitution specifiers,
// undefined rendering, Map/Set inspection, and the Buffer console form — all
// matching Node.js (ADR-00690). ---

func TestE2EConsoleFormatSpecifiers(t *testing.T) {
	// A string-literal first argument is a util.format format string: %s/%d/%i
	// substitute the following arguments, %% collapses to a literal %, and
	// unconsumed arguments are appended space-separated.
	assertOutput(t, `
console.log("%s!", "hi")
console.log("%d + %d = %d", 2, 3, 5)
console.log("%% literal", 1)
console.log("no fmt", "extra", 42)
console.log("val is %s and count %d", "x", 7)
`, "hi!\n2 + 3 = 5\n% literal 1\nno fmt extra 42\nval is x and count 7")
}

func TestE2EConsoleFormatCAndInspect(t *testing.T) {
	// %c consumes its argument and emits nothing (browser CSS); %o inspects.
	assertOutput(t, `
console.log("%c styled", "color:red", "after")
console.log("%o", { a: 1 })
`, " styled after\n{ a: 1 }")
}

func TestE2EConsoleUndefinedRendering(t *testing.T) {
	// console.log(undefined) prints the keyword, not a blank line; null and
	// undefined stay distinct.
	assertOutput(t, `
console.log(undefined)
console.log(null, undefined)
`, "undefined\nnull undefined")
}

func TestE2EConsoleMapSetInspect(t *testing.T) {
	assertOutput(t, `
console.log(new Map([["a", 1]]))
console.log(new Set([1, 2]))
console.log(new Map())
console.log(new Set())
`, "Map(1) { 'a' => 1 }\nSet(2) { 1, 2 }\nMap(0) {}\nSet(0) {}")
}

func TestE2EConsoleBufferForm(t *testing.T) {
	// Node's console form for a Buffer is `<Buffer 68 69>` (lowercase hex),
	// distinct from the `[ 104, 105 ]` a plain Uint8Array shows.
	assertOutput(t, `
console.log(Buffer.from("hi"))
console.log(Buffer.from([]))
`, "<Buffer 68 69>\n<Buffer >")
}
