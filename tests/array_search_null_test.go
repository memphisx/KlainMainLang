package tests

import "testing"

// indexOf / lastIndexOf / includes over a string array holding null compare
// the null by identity and never pass it to strcmp (ADR-01585).
func TestE2EArraySearchNullString(t *testing.T) {
	assertOutput(t, `
console.log([null].indexOf(null), [null].lastIndexOf(null), [null].includes(null))
const s: (string | null)[] = ["a", null, "b"]
console.log(s.indexOf("b"), s.indexOf(null), s.lastIndexOf("a"), s.includes("c"))
`, "0 0 true\n2 1 0 false")
}

// An optional index argument holding undefined: a range's end is the
// length, a start or fromIndex is 0; includes and lastIndexOf take their
// fromIndex; super.slice in an Array subclass.
func TestE2EArrayOptionalIndexArgs(t *testing.T) {
	assertSameAsNode(t, `function g(s?: number, e?: number) {
  const a = [1, 2, 3, 4], u = new Uint8Array([1, 2, 3, 4]), str = "abcd"
  console.log(a.slice(s, e), str.slice(s, e), str.substring(s ?? 0, e), u.slice(s, e), u.subarray(s, e))
  console.log([0, 0, 0].fill(7, s, e), [1, 2, 3, 4].copyWithin(0, 2, e), a.indexOf(3, s), a.lastIndexOf(1, e), a.includes(1, s))
  console.log(str.indexOf("c", s), str.lastIndexOf("a", e), str.substr(s ?? 0, e), a.at(s ?? 0), new Uint8Array([5, 6, 7]).fill(9, s, e))
}
g(); g(1); g(1, 3)
const b = [1, 2, 1, 2]
console.log(b.lastIndexOf(2, -2), b.lastIndexOf(2, 99), b.lastIndexOf(1, -9), b.includes(1, -1), b.includes(2, -1), b.includes(1, 9), b.indexOf(2, -1))
`)
	assertSameAsNode(t, `function f(s?: number, e?: number) { return ["a","b","c"].slice(s, e) }
console.log(f(1), f(), f(1, 2))
class A extends Array<string> { sl(s?: number, e?: number): string[] { return super.slice(s, e) } }
const a = new A(); a.push("p", "q")
console.log(a.sl(1), a.sl(), super_ok())
function super_ok() { return 1 }
`)
}
