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
