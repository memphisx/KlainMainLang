package tests

import (
	"testing"
)

// --- async/await ---

func TestE2EAsyncAwaitNumber(t *testing.T) {
	assertOutput(t, `
async function add(a: number, b: number): Promise<number> {
    return a + b
}
const result = await add(3, 4)
console.log(result)
`, "7")
}
func TestE2EAsyncAwaitString(t *testing.T) {
	assertOutput(t, `
async function greet(name: string): Promise<string> {
    return "Hello, " + name + "!"
}
const msg = await greet("world")
console.log(msg)
`, "Hello, world!")
}
func TestE2EAsyncAwaitVoid(t *testing.T) {
	assertOutput(t, `
async function doNothing(): Promise<void> {
    console.log("doing nothing")
}
await doNothing()
`, "doing nothing")
}
func TestE2EAsyncChained(t *testing.T) {
	assertOutput(t, `
async function double(n: number): Promise<number> {
    return n * 2
}
async function addOne(n: number): Promise<number> {
    return n + 1
}
const a = await double(5)
const b = await addOne(a)
console.log(b)
`, "11")
}

// TestE2EAsyncArrowFunctionBlockBody covers a bug found while wiring
// http.listen's own async-handler support (ADR-00050): async *arrow*
// functions never got the Promise-wrapping treatment named async function
// declarations already had — emitClosureFunc never set up the async
// prologue/epilogue, so `return X` inside one returned X directly instead
// of wrapping it in the malloc'd Promise slot every caller expects. This
// was invisible before since named top-level functions can't be passed by
// reference (a separate, already-tracked limitation), so an async arrow
// function used to be the only way to get an async *callback* at all, and
// nothing had ever exercised one.
func TestE2EAsyncArrowFunctionBlockBody(t *testing.T) {
	assertOutput(t, `
const addAsync = async (a: number, b: number): Promise<number> => {
    return a + b
}
const result = await addAsync(3, 4)
console.log(result)
`, "7")
}

func TestE2EAsyncArrowFunctionExpressionBody(t *testing.T) {
	assertOutput(t, `
const doubleAsync = async (n: number): Promise<number> => n * 2
console.log(await doubleAsync(5))
`, "10")
}
