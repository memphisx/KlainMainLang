package tests

import (
	"testing"
)

// --- Functions and closures ---

func TestE2ERecursion(t *testing.T) {
	assertOutput(t, `
function fib(n: number): number {
    if (n <= 1) { return n; }
    return fib(n - 1) + fib(n - 2)
}
console.log(fib(10))
`, "55")
}

func TestE2EClosure(t *testing.T) {
	assertOutput(t, `
function makeCounter(): () => number {
    let count: number = 0
    return (): number => { count++; return count; }
}
const c = makeCounter()
console.log(c())
console.log(c())
console.log(c())
`, "1\n2\n3")
}

func TestE2EParenthesizedFunctionTypeReturnAnnotation(t *testing.T) {
	assertOutput(t, `
function makeCounter(): (() => number) {
    let count: number = 0
    return (): number => { count++; return count; }
}
const c = makeCounter()
console.log(c())
console.log(c())
`, "1\n2")
}

func TestE2EClosureMutatesOuterScope(t *testing.T) {
	assertOutput(t, `
let sum: number = 0
const inc = (n: number): void => { sum += n; }
inc(1)
inc(2)
inc(3)
console.log(sum)
`, "6")
}

func TestE2EClosureIndependentInstances(t *testing.T) {
	assertOutput(t, `
function makeCounter(): () => number {
    let count: number = 0
    return (): number => { count++; return count; }
}
const c1 = makeCounter()
const c2 = makeCounter()
console.log(c1())
console.log(c1())
console.log(c2())
`, "1\n2\n1")
}

func TestE2ENestedClosureCapture(t *testing.T) {
	assertOutput(t, `
let total: number = 0
const outer = (): void => {
    const inner = (): void => { total += 10; }
    inner()
    inner()
}
outer()
outer()
console.log(total)
`, "40")
}

// --- Arrow function returning a callable closure ---

func TestE2EArrowFunctionReturnedClosureCallable(t *testing.T) {
	assertOutput(t, `
const middle = (): (() => void) => {
  let n = 0
  return () => { n = n + 1; console.log(n) }
}
const inner = middle()
inner()
inner()
`, "1\n2")
}

// --- (FuncType)[] array-of-function-type annotations ---

func TestE2EArrayOfFunctionTypeDeclaresAndTracksLength(t *testing.T) {
	assertOutput(t, `
let counters: (() => number)[] = []
console.log(counters.length)
`, "0")
}

func TestE2EArrayOfFunctionTypePushAndCallByIndex(t *testing.T) {
	assertOutput(t, `
function makeCounter(start: number): () => number {
  let n = start
  return () => { n = n + 1; return n }
}
let counters: (() => number)[] = []
counters.push(makeCounter(0))
counters.push(makeCounter(100))
console.log(counters[0]())
console.log(counters[0]())
console.log(counters[1]())
`, "1\n2\n101")
}

func TestE2EParenGroupedArrayTypeAnnotation(t *testing.T) {
	assertOutput(t, `
let nums: (number)[] = [1, 2, 3]
console.log(nums[0])
console.log(nums[2])
`, "1\n3")
}

func TestE2EFunctionTypedObjectFieldCallable(t *testing.T) {
	assertOutput(t, `
interface Handler { callback: () => number }
function makeCounter(start: number): () => number {
  let n = start
  return () => { n = n + 1; return n }
}
const h: Handler = { callback: makeCounter(10) }
console.log(h.callback())
console.log(h.callback())
`, "11\n12")
}

func TestE2EUnannotatedFunctionReturnsScalar(t *testing.T) {
	assertOutput(t, `
function addOne(n) { return n + 1 }
console.log(addOne(5))
`, "6")
}

func TestE2EUnannotatedRecursiveFunctionReturnsScalar(t *testing.T) {
	assertOutput(t, `
function factorial(n) {
  if (n <= 1) { return 1 }
  return n * factorial(n - 1)
}
console.log(factorial(5))
`, "120")
}

// --- default parameter values ---

func TestE2EDefaultParamNumber(t *testing.T) {
	assertOutput(t, `
function add(a: number, b: number = 10): number { return a + b }
console.log(add(5))
console.log(add(5, 3))
`, "15\n8")
}

func TestE2EDefaultParamString(t *testing.T) {
	assertOutput(t, `
function greet(name: string = 'World'): string { return 'Hello, ' + name }
console.log(greet())
console.log(greet('Alice'))
`, "Hello, World\nHello, Alice")
}

func TestE2EDefaultParamMultiple(t *testing.T) {
	assertOutput(t, `
function box(w: number = 1, h: number = 1, d: number = 1): number { return w * h * d }
console.log(box())
console.log(box(2))
console.log(box(2, 3))
console.log(box(2, 3, 4))
`, "1\n2\n6\n24")
}

// --- void return type ---

func TestE2EVoidReturn(t *testing.T) {
	assertOutput(t, `
function greet(name: string): void {
    console.log(name)
}
function clamp(x: number): void {
    if (x < 0) { return }
    console.log(x)
}
const printIt = (n: number): void => {
    console.log(n)
}
greet("hello")
clamp(-1)
clamp(5)
printIt(42)
`, "hello\n5\n42")
}

// --- Unannotated parameter typing (numeric-only inference) ---

func TestE2EUnannotatedParamNonNumericArgRejected(t *testing.T) {
	_, err := parseAndCompile(`
function log(msg) { console.log(msg) }
log("hello")
`)
	if err == nil {
		t.Fatal("expected a compile error for a non-numeric argument to an unannotated parameter, got none")
	}
}
func TestE2EUnannotatedArrowParamNonNumericArgRejected(t *testing.T) {
	_, err := parseAndCompile(`
const log = (msg) => { console.log(msg) }
log("hello")
`)
	if err == nil {
		t.Fatal("expected a compile error for a non-numeric argument to an unannotated arrow function parameter, got none")
	}
}
func TestE2EUnannotatedParamNumericArgStillWorks(t *testing.T) {
	assertOutput(t, `
function addOne(n) { return n + 1 }
console.log(addOne(5))
`, "6")
}
func TestE2EUnannotatedArrowParamNumericArgStillWorks(t *testing.T) {
	assertOutput(t, `
const addOne = (n) => n + 1
console.log(addOne(5))
`, "6")
}
func TestE2EAnnotatedParamNonNumericArgStillWorks(t *testing.T) {
	assertOutput(t, `
function log(msg: string) { console.log(msg) }
log("hello")
`, "hello")
}
