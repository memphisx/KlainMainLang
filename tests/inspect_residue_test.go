package tests

import "testing"

// ADR-01076: util.inspect's maxArrayLength (100 elements, then `... n more
// items` — left out of the column grouping), `-0` inside a container, and
// Node's getStringWidth for the column layout (full-width 2, combining 0).
func TestE2EInspectMaxArrayLengthNegZeroWidth(t *testing.T) {
	const src = `
const big: number[] = [];
for (let i = 0; i < 105; i++) big.push(i);
console.log(big);
const one: number[] = [];
for (let i = 0; i < 101; i++) one.push(i);
console.log(one);
const strs: string[] = [];
for (let i = 0; i < 120; i++) strs.push("s" + i);
console.log(strs);
console.log([-0, 0, 1.5, -0]);
console.log({ z: -0 });
console.log(["日本語", "ab", "é", "éx"]);
const wide: string[] = [];
for (let i = 0; i < 12; i++) wide.push(i % 2 ? "日本" : "ab");
console.log(wide);
const anyArr: any = [1, "x", true];
console.log(anyArr);
`
	assertSameAsNode(t, src)
}
