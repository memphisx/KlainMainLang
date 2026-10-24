package tests

import "testing"

// JSON parsed into a declared type keeps the keys that type does not
// declare, as the untyped value Node produces does; `in` sees them.
func TestE2EJSONParseTypedKeepsExtraKeys(t *testing.T) {
	assertSameAsNode(t, `
interface In { x: number }
interface P { a: number; n: In; list: In[] }
const p: P = JSON.parse('{"a":1,"n":{"x":2,"y":3},"list":[{"x":1,"w":"q"}],"b":"x"}')
console.log(p)
console.log(JSON.stringify(p), Object.keys(p.n), "y" in p.n, "b" in p, "q" in p, (p as any).b)
const arr: In[] = JSON.parse('[{"x":5,"k":true}]')
console.log(arr, JSON.stringify(arr))
const o: In = { x: 1 };
(o as any).extra = 2
console.log("extra" in o, "nope" in o)
`)
}
