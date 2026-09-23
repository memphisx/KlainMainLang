package tests

import "testing"

// ADR-01067: util.inspect's line layout. Entries share a line when they fit
// Node's 80-column breakLength and the value nests fewer than 3 levels below
// (compact: 3); otherwise one entry per line, indented 2 per level. An array
// of more than 6 short entries is regrouped into aligned columns (numbers
// right-aligned, everything else left-aligned). Default depth is 2 with
// `[Object]`/`[Array]` beyond it — but an empty container still prints as
// `{}`/`[]`. Strings are escaped and quoted as Node does. Every case here is
// a Node-oracle twin.

func TestE2EInspectLayoutArrays(t *testing.T) {
	src := `
console.log([1, 2, 3, 4, 5, 6, 7, 8, 9, 10])
console.log([1, 2, 3])
console.log(["alpha", "beta", "gamma", "delta", "epsilon", "zeta", "eta", "theta"])
console.log([["name", "b"], ["age", "3"], ["nick", "bb"], ["tags", "x"]])
console.log([[1, 2, 3], [4, 5, 6], [7, 8, 9], [10, 11, 12], [13, 14, 15], [16, 17, 18], [19, 20, 21]])
console.log([0.1, 0.25, 1000000, -5, 3.14159, 42, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20])
console.log(Array.from({ length: 30 }, (_, i) => i * 1000))
console.log([true, false, true, false, true, false, true, false])
const t: [number, string] = [1, "a"]
console.log(t, [1.5, -2, 100, 3, 4, 5, 6, 7], [] as number[])
`
	assertSameAsNode(t, src)
}

func TestE2EInspectLayoutObjects(t *testing.T) {
	src := `
class Point { x: number; y: number; constructor(x: number, y: number) { this.x = x; this.y = y } }
console.log([new Point(1, 2), new Point(3, 4), new Point(5, 6), new Point(7, 8)])
console.log(new Point(1, 2))
interface U { name: string; age?: number; tags: string[]; pos: Point }
const us: U[] = [{ name: "a", tags: ["x", "y"], pos: new Point(0, 0) }, { name: "b", age: 3, tags: [], pos: new Point(9, 9) }]
console.log(us)
console.log(new Map<string, Point>([["p", new Point(1, 1)], ["q", new Point(2, 2)]]))
console.log({ text: "line1\nline2", n: 1 })
console.log({ s: new Set(["a", "b", "c", "d", "e", "f", "g"]), arr: [true, false, true, false, true, false, true, false] })
interface Big { alpha: string; beta: string; gamma: string; delta: string; epsilon: string }
const big: Big = { alpha: "aaaaaaaaaaaaaaaaaa", beta: "bbbbbbbbbbbbbbbbbb", gamma: "cccccccccccccccccc", delta: "dddddddd", epsilon: "eeeeeeeeeee" }
console.log(big)
console.log({ list: [1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27] })
console.log({ a: { b: { c: { d: 1 } } } })
console.log({ a: { b: { c: {} } } })
console.log({ a: [[1, 2], [3, 4]], deep: { x: { y: { z: [] } } }, e: {} }, new Map<string, number>(), {})
`
	assertSameAsNode(t, src)
}

func TestE2EInspectStringQuoting(t *testing.T) {
	src := `
console.log({ a: "it's", b: 'say "hi"', c: "tab\there", d: "both ' and \"", e: ["x\u0000y", "\u0001", "back\\slash"] })
`
	assertOutput(t, src, "{\n  a: \"it's\",\n  b: 'say \"hi\"',\n  c: 'tab\\there',\n  d: `both ' and \"`,\n  e: [ 'x\\x00y', '\\x01', 'back\\\\slash' ]\n}")
	assertSameAsNode(t, src)
}

// The dynamic (any) inspector shares the layout, including when nested inside
// a static container (depth carried through).
func TestE2EInspectLayoutDynamic(t *testing.T) {
	src := `
const o: any = JSON.parse('{"a":[1,2,3,4,5,6,7,8,9,10],"b":{"c":{"d":{"e":1}}},"k-1":true,"e":{},"w":["alpha","beta","gamma","delta","epsilon","zeta","eta","theta"]}')
o.s = "x" + String.fromCharCode(10) + "y"
console.log(o)
const arr: any = [1, "two", true, null, [3, 4], { z: 1 }]
console.log(arr)
const big: any = [1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30]
console.log(big)
const boxed: any = [1.5, 2, 3, 4, 5, 6, 7, 8]
console.log(boxed, { boxed })
const strs: any = ["it's", "a" + String.fromCharCode(9) + "b"]
console.log(strs)
`
	assertSameAsNode(t, src)
}
