package tests

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// ADR-01064: a literal-only array literal is built from a static image (one
// constant global + malloc + memcpy; a table of literal rows through a runtime
// row loop) instead of a store per element. The array is still an ordinary
// heap array — every mutator and alias behaves exactly as on the per-element
// path.

func TestE2EStaticArrayLiteralFlat(t *testing.T) {
	src := `
const nums = [1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, -18]
const strs = ["a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l", "m", "n", "o", "p", "q"]
const fl = [1.5, 2.25, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 1e3]
const bs = [true, false, true, true, false, true, true, false, true, true, false, true, true, false, true, true]
nums.push(19)
nums[0] = 100
strs.reverse()
console.log(nums.length, nums[0], nums[17], nums[18], nums.reduce((a, b) => a + b, 0))
console.log(strs.join(""), strs.length, strs.indexOf("q"))
console.log(fl[0] + fl[1], fl[16], fl.length, bs.filter(b => b).length)
function f() { return [9, 8, 7, 6, 5, 4, 3, 2, 1, 0, 9, 8, 7, 6, 5, 4, 3] }
console.log(f().sort((a, b) => a - b).join(","))
`
	assertOutput(t, src, "19 100 -18 19 253\nqponmlkjihgfedcba 17 0\n3.75 1000 17 11\n0,1,2,3,3,4,4,5,5,6,6,7,7,8,8,9,9")
	assertSameAsNode(t, src)
}

func TestE2EStaticArrayLiteralRows(t *testing.T) {
	src := `
const tbl = [["a", "A"], ["b", "B"], ["c", "C"], ["", "x"], []]
const row = tbl[1]
row.push("extra")
tbl[0][1] = "Z"
tbl.push(["d", "D"])
console.log(tbl.length, tbl[0][1], tbl[1], tbl[3][0].length, tbl[4].length, tbl[5][1])
const grid = [[1, 2, 3], [4, 5, 6], [7, 8, 9]]
console.log(grid.map(r => r.reduce((a, b) => a + b, 0)).join(","), grid[2][2])
`
	assertOutput(t, src, "6 Z [ 'b', 'B', 'extra' ] 0 0 D\n6,15,24 9")
	assertSameAsNode(t, src)
}

// Typed element widths and the folding boundary: an exact integer folds into
// an `intN`/`uintN` slot; a fraction or an out-of-range value takes the
// runtime coercion (same result either way — the point is that the two paths
// agree, checked against the small-literal general path in the same program).
func TestE2EStaticArrayLiteralTypedElements(t *testing.T) {
	src := `
/** @type {int8[]} */
const i8 = [1, -1, 127, -128, 255, 300, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11]
/** @type {int8[]} */
const i8s = [255, 300]
/** @type {uint8[]} */
const u8 = [0, 255, 300, -1, 2.5, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11]
/** @type {uint8[]} */
const u8s = [300, -1, 2.5]
/** @type {float32[]} */
const f32 = [0.1, 1e3, -2.5, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13]
/** @type {float32[]} */
const f32s = [0.1]
console.log(i8[4] === i8s[0], i8[5] === i8s[1], i8[4], i8[5])
console.log(u8[2] === u8s[0], u8[3] === u8s[1], u8[4] === u8s[2], u8[2], u8[3], u8[4])
console.log(f32[0] === f32s[0], f32[0], f32[2])
const hex = [0x1f, 0b101, 0o17, 1e3, -0, 2.5, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10]
console.log(hex[0], hex[1], hex[2], hex[3], 1 / hex[4], hex[5])
`
	assertOutput(t, src, "true true -1 44\ntrue true true 44 255 2\ntrue 0.10000000149011612 -2.5\n31 5 15 1000 -Infinity 2.5")
}

// The IR shape: a 16+ literal flat array and a table of literal rows are
// static images, not per-element stores; a literal with a non-constant
// element is not.
func TestStaticArrayLiteralIRShape(t *testing.T) {
	ir, err := parseAndCompile(`
const xs = [1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16]
const tbl = [["a", "b"], ["c"]]
console.log(xs.length, tbl.length)
`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ir, "@.arrlit0 = private unnamed_addr constant [16 x double]") {
		t.Errorf("flat literal was not lowered to a static image:\n%s", ir)
	}
	if !strings.Contains(ir, "call void @__kml_arr_lit_rows(") || !strings.Contains(ir, "constant [3 x ptr]") || !strings.Contains(ir, "constant [2 x i64] [i64 2, i64 1]") {
		t.Errorf("row table was not lowered to a flat image + row lengths:\n%s", ir)
	}
	ir, err = parseAndCompile(`
let y = 3
const xs = [1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, y]
console.log(xs.length)
`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(ir, "@.arrlit") {
		t.Errorf("a literal with a non-constant element must stay on the per-element path:\n%s", ir)
	}
}

// The motivating case: a 20 K-row `[string, string]` table took clang 21 s
// (65 K rows never finished). It now compiles as one constant.
func TestE2EStaticArrayLiteralLargeTableCompileTime(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("const mapping = [\n")
	for i := 0; i < 20000; i++ {
		fmt.Fprintf(&sb, "  [\"%c%d\", \"%d\"],\n", 'a'+rune(i%26), i, i*2)
	}
	sb.WriteString("]\nlet ok = 0\nfor (let i = 0; i < mapping.length; i++) { if (mapping[i][1] === String(i * 2)) ok++ }\nconsole.log(mapping.length, ok, mapping[19999][0])\n")
	start := time.Now()
	assertOutput(t, sb.String(), "20000 20000 f19999")
	if d := time.Since(start); d > 30*time.Second {
		t.Errorf("20K-row literal table took %v to compile and run (expected seconds)", d)
	}
}
