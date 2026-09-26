package tests

import "testing"

// --- `&&`/`||` value-preserving under -compat=js (TDD-00075/ADR-00220):
// `a && b` yields `b` or the falsy `a`; `a || b` yields `a` or `b` — the actual
// operand values, not a bool — in both lanes (ADR-01169). ---

func TestE2ELogicalValuePreservingCompatJS(t *testing.T) {
	assertOutputCompatJS(t, `
console.log(5 && 3)
console.log(0 || 7)
console.log(5 || 3)
console.log(0 && 3)
console.log("" || "fallback")
console.log("hi" && "world")
const timeout = 0
console.log(timeout || 30)
console.log(true && false)
`, "3\n7\n5\n0\nfallback\nworld\n30\nfalse")
}

func TestE2ELogicalValuePreservingStrict(t *testing.T) {
	// -compat=strict (default) keeps the operand too, as TypeScript types it.
	assertOutput(t, `
console.log(5 && 3)
console.log(0 || 7)
console.log(true && false)
`, "3\n7\nfalse")
}

// Operands of different kinds (ADR-01054): the result is their union `L | R`
// — the same box `??` yields — never a bool. Truthiness comes from the operand
// itself; a union operand chained again widens the union; `undefined` on
// either side contributes nullability. Every line matches Node.
func TestE2ELogicalMixedKindUnionCompatJS(t *testing.T) {
	assertOutputCompatJS(t, `
var s = "str", e = "", n = 0, m = 7, t = true, f = false;
var u;
console.log(s && n, s && m, e && m, n && s, m && s);
console.log(s || n, e || m, n || s, m || s, e || n);
console.log(s && !e, e && !s, !e && s, !s || m);
console.log(u && s, u || s, s && u, e || u);
var r = (s && s && !e && s);
console.log(r, typeof r);
var q = (m && s) || n;
console.log(q, typeof q);
console.log(typeof (s && m), typeof (s || m), typeof (e && m), typeof (e || m));
if (!(s && s && !u && s)) throw new Error("bad");
console.log("ok");
`, "0 7  0 str\nstr 7 str 7 0\ntrue  str 7\nundefined str undefined undefined\nstr string\nstr string\nnumber string string number\nok")
}

// The Test262 shape that surfaced it (do-while/S12.6.1_A4_T2): a `var`
// hoisted from behind a `break` is `undefined`, and the string/bool `&&` chain
// over it was inferred as a string while the emitter produced an i1 — an
// `i1` stored through a `ptr` slot, invalid IR.
func TestE2ELogicalChainOverHoistedUndefinedCompatJS(t *testing.T) {
	assertOutputCompatJS(t, `
do_out : do {
    var a = "black";
    do_in : do {
        var b = "hole";
        break do_in;
        var c = "sun";
    } while (0);
    var d = "won't you come";
} while (2 == 1);
if (!(a && b && !c && d)) {
    throw new Error('#1: Actual: ' + (a && b && !c && d));
}
console.log(a && b && !c && d);
`, "won't you come")
}
