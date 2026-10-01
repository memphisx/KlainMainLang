package tests

import "testing"

// A string iterates its code points (ADR-01188): `for…of`, spread,
// Array.from and destructuring, over a typed string and one held in `any`.
// Node is the oracle.
func TestE2EStringIteratesCodePoints(t *testing.T) {
	assertSameAsNode(t, `
const s: string = "aé😀b"
const a = [...s]
console.log(a.length, a.join("|"))
for (const c of s) console.log(c)
console.log(Array.from("xé€").join("+"))
const [p, q] = "éz"
console.log(p, q)
const d: any = "ñ😀"
for (const c of d) console.log(c)
`)
}
