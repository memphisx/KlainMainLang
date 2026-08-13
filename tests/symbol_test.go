package tests

import (
	"testing"
)

// --- symbol V1 (TDD-00044): Symbol()/Symbol("desc"), ===/!==, typeof,
// .description, .toString(), console.log/template-literal formatting ---

func TestE2ESymbolUniqueness(t *testing.T) {
	assertOutput(t, `
const a = Symbol("x")
const b = Symbol("x")
const c = a
console.log(a === b)
console.log(a !== b)
console.log(a === c)
`, "false\ntrue\ntrue")
}

func TestE2ESymbolTypeofAndDescription(t *testing.T) {
	assertOutput(t, `
const a: symbol = Symbol("hello")
console.log(typeof a)
console.log(a.description)
const b = Symbol()
console.log(b.description)
`, "symbol\nhello\n")
}

func TestE2ESymbolToStringAndFormatting(t *testing.T) {
	assertOutput(t, `
const a = Symbol("x")
console.log(a.toString())
console.log(`+"`sym: ${a}`"+`)
const b = Symbol()
console.log(b.toString())
`, "Symbol(x)\nsym: Symbol(x)\nSymbol()")
}

func TestE2ESymbolArithmeticRejected(t *testing.T) {
	_, err := parseAndCompile(`
const a = Symbol("x")
const b = Symbol("y")
console.log(a < b)
`)
	if err == nil {
		t.Fatal("expected a compile error for '<' on symbol values, got none")
	}
}

func TestE2ESymbolDescriptionMustBeString(t *testing.T) {
	_, err := parseAndCompile(`
const a = Symbol(5)
`)
	if err == nil {
		t.Fatal("expected a compile error for a non-string Symbol() description, got none")
	}
}

func TestE2ESymbolTooManyArgs(t *testing.T) {
	_, err := parseAndCompile(`
const a = Symbol("x", "y")
`)
	if err == nil {
		t.Fatal("expected a compile error for Symbol() with more than 1 argument, got none")
	}
}

func TestE2ESymbolNewIsNotAConstructor(t *testing.T) {
	_, err := parseAndCompile(`
const a = new Symbol("x")
`)
	if err == nil {
		t.Fatal("expected a compile error for 'new Symbol(...)', got none")
	}
}

func TestE2ESymbolJSONStringifyRejected(t *testing.T) {
	_, err := parseAndCompile(`
const a = Symbol("x")
console.log(JSON.stringify(a))
`)
	if err == nil {
		t.Fatal("expected a compile error for JSON.stringify(symbol), got none")
	}
}

func TestE2ESymbolStructuredCloneRejected(t *testing.T) {
	_, err := parseAndCompile(`
const a = Symbol("x")
const b = structuredClone(a)
`)
	if err == nil {
		t.Fatal("expected a compile error for structuredClone(symbol), got none")
	}
}

func TestE2ESymbolMemoryFree(t *testing.T) {
	assertOutputImports(t, `
import Memory from 'memory'
const a = Symbol("x")
console.log(a.toString())
Memory.free(a)
console.log("freed ok")
`, "Symbol(x)\nfreed ok")
}
