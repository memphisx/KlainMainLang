package tests

import "testing"

// Default abort() reason and the static AbortSignal.abort()/any() constructors
// (TDD-00081 Stage 3, ADR-00693). A no-argument abort() defaults its reason to
// an "AbortError" DOMException, matching Node; AbortSignal.abort(reason?) yields
// an already-aborted signal, and AbortSignal.any(signals) a signal aborted when
// any input is aborted.

func TestE2EAbortDefaultReasonIsAbortError(t *testing.T) {
	assertOutput(t, `
const c = new AbortController()
c.abort()
console.log(c.signal.aborted)
console.log(c.signal.reason?.name)
`, "true\nAbortError")
}

func TestE2EAbortSignalStaticAbort(t *testing.T) {
	assertOutput(t, `
const s = AbortSignal.abort()
console.log(s.aborted)
console.log(s.reason?.name)
const s2 = AbortSignal.abort("custom")
console.log(s2.reason)
`, "true\nAbortError\ncustom")
}

func TestE2EAbortSignalAny(t *testing.T) {
	assertOutput(t, `
const a = new AbortController()
const b = new AbortController()
b.abort()
const any = AbortSignal.any([a.signal, b.signal])
console.log(any.aborted)
console.log(any.reason?.name)
const none = AbortSignal.any([a.signal])
console.log(none.aborted)
`, "true\nAbortError\nfalse")
}
