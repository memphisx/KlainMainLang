package tests

import (
	"testing"
)

// --- structuredClone (see docs/status/GLOBAL-FUNCTIONS.md) ---

func TestE2EStructuredCloneScalarsPassThrough(t *testing.T) {
	assertOutput(t, `
const n = 42
const s = "hi"
const b = true
console.log(structuredClone(n))
console.log(structuredClone(s))
console.log(structuredClone(b))
`, "42\nhi\n1")
}

func TestE2EStructuredCloneArrayIsIndependentCopy(t *testing.T) {
	assertOutput(t, `
const nums: number[] = [1, 2, 3]
const clone = structuredClone(nums)
clone[0] = 99
console.log(nums[0])
console.log(clone[0])
`, "1\n99")
}

func TestE2EStructuredCloneObjectIsIndependentCopy(t *testing.T) {
	assertOutput(t, `
interface Point {
  x: number
  y: number
}
const p: Point = { x: 1, y: 2 }
const clone = structuredClone(p)
clone.x = 42
console.log(p.x)
console.log(clone.x)
`, "1\n42")
}

func TestE2EStructuredCloneRecursesIntoNestedFieldsAndArrays(t *testing.T) {
	assertOutput(t, `
interface Point {
  x: number
  y: number
}
interface Shape {
  name: string
  points: Point[]
}
const shape: Shape = { name: "tri", points: [{ x: 0, y: 0 }, { x: 1, y: 1 }] }
const clone = structuredClone(shape)
clone.points[0].x = 500
clone.name = "changed"
console.log(shape.points[0].x)
console.log(clone.points[0].x)
console.log(shape.name)
console.log(clone.name)
`, "0\n500\ntri\nchanged")
}

func TestE2EStructuredCloneNestedArrayIsIndependentCopy(t *testing.T) {
	assertOutput(t, `
const nested: number[][] = [[1, 2], [3, 4]]
const clone = structuredClone(nested)
clone[0][0] = 777
console.log(nested[0][0])
console.log(clone[0][0])
`, "1\n777")
}

func TestE2EStructuredCloneMapIsError(t *testing.T) {
	_, err := parseAndCompile(`
const m = new Map<string, number>()
const clone = structuredClone(m)
console.log(clone.size)
`)
	if err == nil {
		t.Fatal("expected a compile error for structuredClone(Map) — not yet supported")
	}
}

func TestE2EStructuredCloneClassInstanceIsError(t *testing.T) {
	_, err := parseAndCompile(`
class Foo {
  x: number = 1
}
const f = new Foo()
const clone = structuredClone(f)
console.log(clone.x)
`)
	if err == nil {
		t.Fatal("expected a compile error for structuredClone(class instance) — not yet supported")
	}
}
