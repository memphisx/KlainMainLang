package tests

import (
	"testing"
)

// A string-typed bracket index into an array (legal in JS, where `a["2"]`
// addresses the same slot as `a[2]`) used to reach clang as invalid IR: the
// string data pointer was left where an i64 index was required, producing
// `icmp uge i64 <ptr>` / a GEP indexed by a pointer constant. The same defect
// hit every slice-style index argument (copyWithin/fill/at/…), which coerce
// their bounds through the same path. arrayIndexToI64 now parses a string index
// to its integer value at runtime (ADR-00694), so these emit valid IR and
// resolve to the canonical slot.

// A non-numeric, non-string index/count argument (e.g. a Symbol or object) has
// no sound conversion to an integer slot — arrayIndexToI64 / the splice & search
// numeric-arg sites now reject it cleanly instead of leaving a `ptr` where an
// i64 is required (the A1 invalid-IR cluster; same fix as ADR-00882/00883). The
// rejection matches tsc, which refuses a non-number index type.
func TestE2EArrayNonNumericIndexRejected(t *testing.T) {
	cases := []struct{ src, want string }{
		{`const a=[1,2,3]; a.slice(Symbol("x"))`, "array index"},
		{`const a=[1,2,3]; a.at(Symbol("x"))`, "array index"},
		{`const a=[1,2,3]; a.fill(0, Symbol("x"))`, "array index"},
		{`const a=[1,2,3]; a.copyWithin(Symbol("x"), 0)`, "array index"},
		{`const a=[1,2,3]; const o={v:1}; a[o]`, "array index"},
		{`const a=[1,2,3]; a.splice(0, Symbol("x"))`, "splice deleteCount"},
		{`const a=[1,2,3]; a.indexOf(1, Symbol("x"))`, "indexOf fromIndex"},
	}
	for _, c := range cases {
		mustCompileError(t, c.src, c.want)
	}
}

func TestE2EArrayStringIndexRead(t *testing.T) {
	assertOutputCompatJS(t, `
const a = [10, 20, 30, 40];
const i = "2";
console.log(a[i]);
console.log(a["1"]);
`, "30\n20")
}

func TestE2EArrayStringIndexWrite(t *testing.T) {
	assertOutputCompatJS(t, `
const a = [10, 20, 30, 40];
a["1"] = 99;
const k = "3";
a[k] = 77;
console.log(a[1]);
console.log(a[3]);
`, "99\n77")
}

func TestE2EArrayCopyWithinStringArgs(t *testing.T) {
	assertOutputCompatJS(t, `
const b = [0, 1, 2, 3];
console.log(b.copyWithin("0" as any, 2).join(","));
`, "2,3,2,3")
}

func TestE2EArrayFillStringArgs(t *testing.T) {
	assertOutputCompatJS(t, `
const c = [1, 2, 3, 4, 5];
console.log(c.fill(9, "1" as any, "3" as any).join(","));
`, "1,9,9,4,5")
}

func TestE2EArrayAtStringArg(t *testing.T) {
	assertOutputCompatJS(t, `
const a = [10, 20, 30, 40];
console.log(a.at("0" as any));
console.log(a.at("2" as any));
`, "10\n30")
}

func TestE2EArraySliceStringArgs(t *testing.T) {
	assertOutputCompatJS(t, `
const a = [10, 20, 30, 40, 50];
console.log(a.slice("1" as any, "4" as any).join(","));
`, "20,30,40")
}
