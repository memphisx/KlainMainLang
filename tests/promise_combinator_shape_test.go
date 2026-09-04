package tests

import (
	"testing"
)

// --- ADR-00689: Promise combinator result-value/shape fidelity ---
//
// Two combinator bugs fixed here:
//
//  1. Promise.any over a reject-first array decoded a numeric winner with the
//     wrong value shape (the array's element type was keyed off the leading
//     Promise.reject, whose resolved type is `never` and carries no encoding),
//     so a `double` winner slot was read back as a raw i64 bit pattern.
//  2. Promise.allSettled produced the wrong per-element object shape: every
//     element carried both `value` and `reason`, and a rejected `reason` was a
//     synthetic Error wrapper. Real JS gives a fulfilled entry only `value`, a
//     rejected entry only `reason`, and reports the raw rejection value.

func TestE2EPromiseAnyRejectFirstNumericWinner(t *testing.T) {
	// The winner (index 1) is a number; the leading Promise.reject must not make
	// it decode as a raw i64 bit pattern. A string winner already worked.
	assertOutput(t, `
async function m(): Promise<number> {
    return await Promise.any([Promise.reject("x"), Promise.resolve(2)])
}
m().then((v) => { console.log(v) })
`, "2")

	assertOutput(t, `
async function m(): Promise<string> {
    return await Promise.any([Promise.reject("x"), Promise.resolve("hi")])
}
m().then((v) => { console.log(v) })
`, "hi")
}

func TestE2EPromiseAllSettledJSONShape(t *testing.T) {
	// Each element carries ONLY value (fulfilled) or ONLY reason (rejected), and
	// a rejected reason preserves the raw thrown value, not an Error wrapper.
	assertOutput(t, `
async function m(): Promise<string> {
    return JSON.stringify(await Promise.allSettled([Promise.resolve(1), Promise.reject("e")]))
}
m().then((v) => { console.log(v) })
`, `[{"status":"fulfilled","value":1},{"status":"rejected","reason":"e"}]`)
}

func TestE2EPromiseAllSettledPropertyAccessStillWorks(t *testing.T) {
	// The shape fix must not regress direct property access on settlements.
	assertOutput(t, `
const r = await Promise.allSettled([Promise.resolve(7), Promise.reject("boom")])
console.log(r[0].status)
console.log(r[0].value)
console.log(r[1].status)
`, "fulfilled\n7\nrejected")
}
