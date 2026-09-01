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
`, "42\nhi\ntrue")
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

// ADR-00591: a plain ArrayBuffer is byte-copied (independent of the source).
func TestE2EStructuredCloneArrayBuffer(t *testing.T) {
	assertOutput(t, `
const ab = new ArrayBuffer(4)
const view = new Uint8Array(ab)
view[0] = 42
view[1] = 7
const clone = structuredClone(ab)
const cv = new Uint8Array(clone)
console.log(cv[0], cv[1], clone.byteLength)
cv[0] = 99
console.log(view[0])
`, "42 7 4\n42")
}

// ADR-00591: an Error (or subtype) clones its message/name and keeps its type.
func TestE2EStructuredCloneError(t *testing.T) {
	assertOutput(t, `
const e = new TypeError("boom")
const c = structuredClone(e)
console.log(c.message)
console.log(c.name)
console.log(c instanceof TypeError)
`, "boom\nTypeError\ntrue")
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

func TestE2EStructuredCloneMapIsIndependentCopy(t *testing.T) {
	// structuredClone deep-copies a Map (ADR-00574): the clone is independent,
	// and an object value is itself deep-copied.
	assertOutput(t, `
const m = new Map<string, number>()
m.set("a", 1)
m.set("b", 2)
const c = structuredClone(m)
c.set("d", 9)
console.log(m.size, c.size, c.get("a"), c.get("d"))
interface P { x: number }
const mo = new Map<string, P>()
mo.set("p", { x: 1 })
const mo2 = structuredClone(mo)
mo2.get("p").x = 99
console.log(mo.get("p").x, mo2.get("p").x)
`, "2 3 1 9\n1 99")
}

func TestE2EStructuredCloneSetIsIndependentCopy(t *testing.T) {
	assertOutput(t, `
const s = new Set<number>([1, 2, 3])
const c = structuredClone(s)
c.add(4)
console.log(s.size, c.size, c.has(1), c.has(4))
`, "3 4 true true")
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
