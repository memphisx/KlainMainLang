package tests

import "testing"

// Tuple types `[T0, T1, ...]` (TDD-00066): declaration, constant-index read,
// and rendering.
func TestE2ETupleBasics(t *testing.T) {
	assertOutput(t, `
const t: [string, number] = ["hello", 42]
console.log(t[0])
console.log(t[1])
console.log(JSON.stringify(t))
console.log(` + "`${t}`" + `)
`, "hello\n42\n"+`["hello",42]`+"\nhello,42")
}

func TestE2ETupleDestructuring(t *testing.T) {
	assertOutput(t, `
const t: [string, number] = ["hi", 7]
const [a, b] = t
console.log(a)
console.log(b)
`, "hi\n7")
}

func TestE2ETupleForOf(t *testing.T) {
	assertOutput(t, `
const pairs: [string, number][] = [["a", 1], ["b", 2]]
for (const [k, v] of pairs) {
  console.log(k + "=" + v)
}
`, "a=1\nb=2")
}

// A tuple as a function parameter and return value.
func TestE2ETupleParamAndReturn(t *testing.T) {
	assertOutput(t, `
function swap(p: [string, number]): [number, string] { return [p[1], p[0]] }
const s = swap(["a", 1])
console.log(s[0])
console.log(s[1])
`, "1\na")
}

// Nested tuples, indexed and destructured.
func TestE2ETupleNested(t *testing.T) {
	assertOutput(t, `
const n: [number, [string, boolean]] = [1, ["x", true]]
console.log(n[0])
console.log(n[1][0])
console.log(n[1][1])
const [a, [b, c]] = n
console.log(a + "," + b + "," + c)
`, "1\nx\ntrue\n1,x,true")
}

// A tuple element may itself be an array or a nullable scalar.
func TestE2ETupleArrayAndNullableElements(t *testing.T) {
	assertOutput(t, `
const t: [string, number[]] = ["nums", [1, 2, 3]]
console.log(t[0])
console.log(t[1][2])
const q: [string, number | null] = ["k", null]
console.log(q[1] ?? -1)
const q2: [string, number | null] = ["k", 0]
console.log(q2[1] ?? -1)
`, "nums\n3\n-1\n0")
}

// Map/Array/Object .entries() return real [K, V] tuples, destructurable with
// the standard `for (const [k, v] of ...)` idiom (TDD-00066).
func TestE2EEntriesReturnTuples(t *testing.T) {
	assertOutput(t, `
const m = new Map<string, number>()
m.set("a", 1)
m.set("b", 2)
for (const [k, v] of m.entries()) { console.log(k + "=" + v) }

const arr = [10, 20, 30]
for (const [i, x] of arr.entries()) { console.log(i + ":" + x) }

const obj = { name: "Al", city: "NYC" }
for (const [key, val] of Object.entries(obj)) { console.log(key + "->" + val) }
`, "a=1\nb=2\n0:10\n1:20\n2:30\nname->Al\ncity->NYC")
}
