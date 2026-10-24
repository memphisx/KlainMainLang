package tests

import "testing"

// A box in a typed array: an any[] bound to or passed as a typed array, and
// an array literal holding a value codegen keeps as a box, keep each
// element as it is (ADR-01743).

// an any[] bound, assigned and passed where string[] is declared, sharing the array.
func TestE2EAnyElementArrayIntoTypedArray(t *testing.T) {
	src := `const five: any = 5
const xs: any[] = [five, "a"]
const ys: string[] = xs
console.log(ys.map(v => typeof v), ys, typeof ys[0])
ys.push("z")
console.log(xs.length, xs === ys)
function len(a: string[]): string { return a.map(v => typeof v).join() + a.length }
console.log(len(xs))
let zs: string[] = ["q"]
zs = xs
console.log(zs, typeof zs[0])
`
	assertSameAsNode(t, src)
	assertSameAsNodeCompatJS(t, src)
}

// array literals holding a boxed call result or a shadowed field.
func TestE2EBoxInTypedArrayLiteral(t *testing.T) {
	src := `function id(s: string): string { return s }
const five: any = 5
const xs = [id(five)]
console.log(xs.map(v => typeof v), xs, typeof xs[0])
const ys: string[] = [id(five), "a"]
console.log(ys.map(v => typeof v))
class C { n: number = 1 }
const c = new C(); const a: any = c; a.n = "str"
const zs = [c.n, 2]
console.log(zs, zs.map(v => typeof v))
function kinds(p: number[]): string { return p.map(v => typeof v).join() }
console.log(kinds([c.n]))
`
	assertSameAsNode(t, src)
	assertSameAsNodeCompatJS(t, src)
}
