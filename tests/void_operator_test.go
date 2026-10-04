package tests

import "testing"

// `void e` and bindings named undefined, with Node's output (ADR-01334).

func TestE2EVoidOperator(t *testing.T) {
	shadowCase(t, `
let n = 0
function bump(): number { n++; return n }
const x = void 0
console.log(x, void "a", typeof void 1, void bump(), n, void bump() === undefined, n)
`, "undefined undefined undefined undefined 1 true 2")
}

func TestE2EUndefinedBinding(t *testing.T) {
	shadowCase(t, `
function f(undefined: number): number { return undefined + 1 }
function g(): string { var undefined = "x"; return undefined + void 0 }
console.log(f(1), g(), typeof undefined)
`, "2 xundefined undefined")
}

func TestE2EUndefinedBindingUntypedJS(t *testing.T) {
	assertMultiFileOutputPermissive(t, map[string]string{"main.ts": `
function f(undefined) { return undefined }
console.log(f(7), f())
`}, "main.ts", "7 undefined")
}
