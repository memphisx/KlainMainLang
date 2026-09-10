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
console.log(`+"`${t}`"+`)
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

// Tuple .length (a compile-time constant) and constant-index element assignment
// (TDD-00066 caveat reductions).
func TestE2ETupleLengthAndElementAssign(t *testing.T) {
	assertOutput(t, `
const t: [string, number] = ["a", 1]
console.log(t.length)
t[0] = "b"
t[1] = 99
console.log(t[0])
console.log(t[1])
`, "2\nb\n99")
}

// Assigning past the tuple's arity is a clean compile error.
func TestE2ETupleElementAssignOutOfRange(t *testing.T) {
	mustCompileError(t, `
const t: [string, number] = ["a", 1]
t[5] = "x"
`, "out of range")
}

// A named interface as a tuple element. Regression for the rewriteType gap
// (fixed alongside TDD-00078): TupleElems was never descended into during the
// resolver's type-name rename pass, so a named member of a tuple stayed
// unmangled while its interface registered under a mangled key — resolving the
// element to the unknown-name default instead of the object type.
func TestE2ETupleNamedElement(t *testing.T) {
	assertOutput(t, `
interface User { name: string }
const pair: [User, number] = [{ name: "Zoe" }, 5]
console.log(pair[0].name)
console.log(pair[1])
`, "Zoe\n5")
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

// Destructured callback parameters (`arr.map(([k, v]) => …)`, TDD-00199) —
// the dominant Object.entries/Map-entries/pair-array idiom. Covers the
// un-annotated form (type supplied by contextual typing from the HOF element
// type), the explicitly-annotated heterogeneous tuple form, string- and
// number-returning bodies, holes, nested patterns, and filter/reduce/some.
func TestE2EDestructuredCallbackParam(t *testing.T) {
	assertOutput(t, `
const o = { a: 1, b: 2, c: 3 }
console.log(Object.entries(o).map(([k, v]) => k + "=" + v).join(","))
console.log(Object.entries(o).map(([k, v]) => v * 2).join(","))

const pairs: [string, number][] = [["x", 10], ["y", 20]]
console.log(pairs.map(([k, v]) => k).join(","))
console.log(pairs.filter(([k, v]) => v > 10).map(([k, v]) => k).join(","))
console.log(pairs.reduce((acc, [k, v]) => acc + v, 0))
console.log(pairs.some(([k, v]) => v > 15))

let sum = 0
pairs.forEach(([, v]) => { sum += v })
console.log(sum)

const m = new Map<string, number>()
m.set("p", 1)
m.set("q", 2)
console.log([...m.entries()].map(([k, v]) => k + ":" + v).join(","))

const nested: [[number, number], number][] = [[[1, 2], 3]]
console.log(nested.map(([[x, y], z]) => x + y + z).join(","))

const three: [string, number, boolean][] = [["a", 1, true]]
console.log(three.map(([s, n, b]) => s + n + b).join(","))

// Assigned to a variable: the HOF result's element type must flow from the
// callback's destructured-leaf return type (not default to a number) so the
// assigned variable renders correctly — a bug the inline forms above hid.
const keys = Object.entries(o).map(([k, v]) => k)
console.log(keys.join(","))
const bigKeys = Object.entries(o).filter(([k, v]) => v >= 2).map(([k, v]) => k)
console.log(bigKeys.join(","))
`, "a=1,b=2,c=3\n2,4,6\nx,y\ny\n30\ntrue\n30\np:1,q:2\n6\na1true\na,b,c\nb,c")
}
