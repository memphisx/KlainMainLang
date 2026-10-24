package tests

import "testing"

// push grows an array's data geometrically, into the room its heap block
// already has (ADR-01744).

// pushes interleaved with the mutators that shrink or move the data, seen
// through an alias, an object field and a nested element.
func TestE2EArrayPushGrowsIntoSpareRoom(t *testing.T) {
	src := `const xs: number[] = []
for (let i = 0; i < 100000; i++) xs.push(i)
const alias = xs
xs.splice(10, 99980)
alias.push(-1, -2)
xs.length = 5
xs.push(7)
xs.unshift(9)
alias.push(8)
console.log(xs.length, xs.join(), xs === alias)
const o = { list: [] as string[] }
for (let i = 0; i < 1000; i++) o.list.push("s" + i)
o.list.splice(0, 998)
o.list.push("t")
const rows: number[][] = [[], [1]]
for (let i = 0; i < 1000; i++) rows[0].push(i)
rows[0].length = 2
rows[0].push(5)
const sorted = [3, 1, 2]
sorted.sort()
sorted.push(4)
console.log(o.list, rows, sorted, xs.slice(-3))
`
	assertSameAsNode(t, src)
}
