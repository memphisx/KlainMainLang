package tests

import (
	"testing"
)

// --- any / unknown (Staged V1: declare/assign/reassign, print, typeof, ===/!==) ---

func TestE2EAnyReassignAcrossTypes(t *testing.T) {
	assertOutput(t, `
let x: any = 5
console.log(x)
x = "hello"
console.log(x)
x = true
console.log(x)
x = null
console.log(x)
`, "5\nhello\ntrue\nnull")
}

func TestE2EAnyTemplateLiteral(t *testing.T) {
	assertOutput(t, `
let x: any = 42
console.log(`+"`value: ${x}`"+`)
x = "world"
console.log(`+"`value: ${x}`"+`)
`, "value: 42\nvalue: world")
}

func TestE2EAnyTypeofRuntime(t *testing.T) {
	assertOutput(t, `
let x: any = 5
console.log(typeof x)
x = "hi"
console.log(typeof x)
x = true
console.log(typeof x)
x = null
console.log(typeof x)
let y: any
console.log(typeof y)
`, "number\nstring\nboolean\nobject\nundefined")
}

func TestE2EAnyEquality(t *testing.T) {
	assertOutput(t, `
let a: any = 5
let b: any = 5
console.log(a === b)
let c: any = "5"
console.log(a === c)
console.log(a !== c)
let d: any = 5.0
console.log(a === d)
console.log(a === 5)
`, "1\n0\n1\n1\n1")
}

func TestE2EUnknownFloat(t *testing.T) {
	assertOutput(t, `
let y: unknown = 3.14
console.log(y)
console.log(typeof y)
`, "3.14\nnumber")
}

func TestE2EAnyArithmeticRejected(t *testing.T) {
	_, err := parseAndCompile(`
let x: any = 5
console.log(x + 1)
`)
	if err == nil {
		t.Fatal("expected a compile error for arithmetic on an any-typed value, got none")
	}
}

func TestE2EAnyAsFunctionParamRejected(t *testing.T) {
	_, err := parseAndCompile(`
function f(x: any): void { console.log(x) }
f(5)
`)
	if err == nil {
		t.Fatal("expected a compile error for any as a function parameter type, got none")
	}
}

func TestE2EAnyArrayElementRejected(t *testing.T) {
	_, err := parseAndCompile(`
let arr: any[] = [1, 2, 3]
console.log(arr.length)
`)
	if err == nil {
		t.Fatal("expected a compile error for any as an array element type, got none")
	}
}
