package tests

import (
	"strings"
	"testing"
)

// TestE2EArrayCalledAsFunction: `Array(…)` without `new` constructs as
// `new Array(…)` does; a local binding named Array is still that binding.
func TestE2EArrayCalledAsFunction(t *testing.T) {
	assertOutput(t, `
const a = Array<number>(3);
console.log(a.length);
const b: number[] = Array();
b.push(4);
console.log(b);
function f(Array: (n: number) => number) { return Array(2); }
console.log(f(n => n * 10));
`, "3\n[ 4 ]\n20\n")
}

// TestE2EMutuallyDependentUntypedLocals: untyped locals assigned from each
// other in a loop (an md5 round) check in linear memory under -compat=js.
func TestE2EMutuallyDependentUntypedLocals(t *testing.T) {
	var b strings.Builder
	b.WriteString("function F(a, b, c, d) { return 0; }\nfunction md5() {\n  var k, a, b, c, d;\n  a = 1; b = 2; c = 3; d = 4;\n  for (k = 0; k < 1; k++) {\n")
	for i := 0; i < 16; i++ {
		b.WriteString("    a = F(a, b, c, d); d = F(d, a, b, c); c = F(c, d, a, b); b = F(b, c, d, a);\n")
	}
	b.WriteString("  }\n  return a + b + c + d;\n}\nconsole.log(md5());\n")
	assertOutputCompatJS(t, b.String(), "0\n")
}
