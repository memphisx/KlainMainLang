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

// TestE2EAsyncPromiseArrayReturnLengthAndContents is a regression test for
// a bug found while implementing Promise.all (ADR-00073): emitAsyncPrologue
// and emitAwait's generic branch used to size/load a Promise<T> slot via
// promiseTy.Align()/.IR directly, which for an array-typed T is the bare
// 8-byte ptr — silently dropping the length half of the {ptr, i64} array
// value arrays are stored as everywhere else in this codebase. Fixed by
// using StructFieldSize/StructFieldIR instead — this test would have
// printed a garbage or zero length before that fix.
func TestE2EAsyncPromiseArrayReturnLengthAndContents(t *testing.T) {
	assertOutput(t, `
async function makeArr(): Promise<number[]> {
    const arr: number[] = [1, 2, 3, 4, 5]
    return arr
}
const arr = await makeArr()
console.log(arr.length)
for (const x of arr) {
    console.log(x)
}
`, "5\n1\n2\n3\n4\n5")
}

// --- Promise.all / .race / .allSettled over ordinary (non-fetch) promises ---
//
// Every ordinary async function's Promise is already fulfilled by the time
// its call returns (this compiler has no real suspension outside
// fetch()'s Promise<Response> — see emit_promise.go's own header comment
// and TDD-00016) — so there's nothing to parallelize here. These tests
// cover the honest, documented behavior for that case: .all collects in
// order, .race takes the first element, .allSettled always reports every
// entry fulfilled. The real-concurrency case (arrays of fetch()) is
// covered in tests/fetch_test.go instead.

func TestE2EPromiseAllOrdinaryPromises(t *testing.T) {
	assertOutput(t, `
async function double(n: number): Promise<number> {
    return n * 2
}
const arr: Array<Promise<number>> = []
arr.push(double(1))
arr.push(double(2))
arr.push(double(3))
const results = await Promise.all(arr)
console.log(results.length)
for (const x of results) {
    console.log(x)
}
`, "3\n2\n4\n6")
}

func TestE2EPromiseRaceOrdinaryPromises(t *testing.T) {
	assertOutput(t, `
async function double(n: number): Promise<number> {
    return n * 2
}
const arr: Array<Promise<number>> = []
arr.push(double(10))
arr.push(double(20))
const winner = await Promise.race(arr)
console.log(winner)
`, "20")
}

func TestE2EPromiseAllSettledOrdinaryPromises(t *testing.T) {
	assertOutput(t, `
async function double(n: number): Promise<number> {
    return n * 2
}
const arr: Array<Promise<number>> = []
arr.push(double(1))
arr.push(double(2))
const settled = await Promise.allSettled(arr)
for (const s of settled) {
    console.log(s.status)
    console.log(s.value)
}
`, "fulfilled\n2\nfulfilled\n4")
}
