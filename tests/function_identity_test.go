package tests

import "testing"

// A function boxed into `any` more than once is one function: ===, indexOf
// and includes see the same value (ADR-01201).
func TestE2EFunctionIdentityThroughAny(t *testing.T) {
	assertSameAsNode(t, `
const f = (x: number) => console.log(x)
const g = (x: number) => console.log(x + 1)
const a: any = f
const b: any = f
console.log(a === b, a !== b, a === (g as any))
const arr: any[] = []
arr.push(f)
console.log(arr.indexOf(f), arr.indexOf(a), arr.indexOf(g), arr.includes(f))
function has(l: any): boolean { return arr.indexOf(l) >= 0 }
console.log(has(f), has(g))
const m = new Map<any, number>()
m.set(f, 1)
console.log(m.get(a), m.has(g))
`)
}
