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

// signal.reason is `any` (TDD-00081 follow-on to TDD-00169/TDD-00222): a
// never-aborted signal reads undefined, abort(reason) preserves any value's
// type, and an Error reason keeps its shape (instanceof/message/name/String).
func TestE2EAbortReasonAnyValue(t *testing.T) {
	assertOutput(t, `
const p = new AbortController()
console.log(p.signal.reason, typeof p.signal.reason)
const n = new AbortController()
n.abort(42)
console.log(n.signal.reason, typeof n.signal.reason)
const s = new AbortController()
s.abort("stop it")
console.log(s.signal.reason, typeof s.signal.reason)
const o = AbortSignal.abort(7)
const any = AbortSignal.any([o])
console.log(any.reason, typeof any.reason)
`, "undefined undefined\n42 number\nstop it string\n7 number")
}

func TestE2EAbortReasonErrorShape(t *testing.T) {
	assertOutput(t, `
const c = new AbortController()
c.abort(new Error("boom"))
console.log(c.signal.reason instanceof Error)
console.log(c.signal.reason.message)
console.log(c.signal.reason.name)
console.log(String(c.signal.reason))
const d = new AbortController()
d.abort()
console.log(d.signal.reason instanceof Error)
console.log(d.signal.reason.message)
`, "true\nboom\nError\nError: boom\ntrue\nThis operation was aborted")
}

// AbortSignal.any live propagation (ADR-01006): a source aborted AFTER the
// composite is built latches the composite — flag, reason, onabort +
// addEventListener listeners — and propagates through nested composites.
func TestE2EAbortSignalAnyLatePropagation(t *testing.T) {
	assertOutput(t, `
const c1 = new AbortController()
const c2 = new AbortController()
const combined = AbortSignal.any([c1.signal, c2.signal])
let fired = 0
combined.addEventListener("abort", () => { fired++ })
combined.onabort = () => { fired += 10 }
console.log(combined.aborted)
c2.abort("late-reason")
console.log(combined.aborted)
console.log(combined.reason)
console.log(fired)
c1.abort("second")
console.log(combined.reason)
console.log(fired)
const c3 = new AbortController()
const inner = AbortSignal.any([c3.signal])
const outer = AbortSignal.any([inner])
c3.abort(7)
console.log(outer.aborted, outer.reason)
`, "false\ntrue\nlate-reason\n11\nlate-reason\n11\ntrue 7")
}

// TDD-00222 subclass closure: an error-subclass instance as a custom
// abort(reason) recovers .message/.name and renders `Name: message` — same
// boxed-error probe relaxation as the allSettled subclass reason.
func TestE2EAbortReasonErrorSubclass(t *testing.T) {
	assertOutput(t, `
class HaltErr extends Error {
    constructor(msg: string) { super(msg); this.name = "HaltErr" }
}
const c = new AbortController()
c.abort(new HaltErr("halt"))
console.log(c.signal.reason.message)
console.log(c.signal.reason.name)
console.log(String(c.signal.reason))
console.log(c.signal.reason instanceof HaltErr)
`, "halt\nHaltErr\nHaltErr: halt\ntrue")
}
