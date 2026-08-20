package tests

import "testing"

// AbortController / AbortSignal (TDD-00081 Stage 3a): the cancellation token —
// an AbortSignal is an EventTarget that fires "abort", plus aborted/reason state.
// (Wiring signal into fetch/timers is a follow-on.)

func TestE2EAbortControllerBasics(t *testing.T) {
	assertOutput(t, `
const ctrl = new AbortController()
const signal = ctrl.signal
console.log(signal.aborted)
let fired = ""
signal.addEventListener("abort", (e: Event) => { fired = "aborted!" })
ctrl.abort()
console.log(signal.aborted)
console.log(fired)
`, "false\ntrue\naborted!")
}

func TestE2EAbortWithReason(t *testing.T) {
	assertOutput(t, `
const c = new AbortController()
c.abort("user cancelled")
console.log(c.signal.aborted)
console.log(c.signal.reason)
`, "true\nuser cancelled")
}

// fetch(url, { signal }) with an already-aborted signal throws an AbortError at
// the await instead of performing the request (TDD-00081 Stage 3b). No network
// is reached — the abort fires before the wait.
func TestE2EFetchAbortedSignalThrows(t *testing.T) {
	assertOutput(t, `
async function run() {
  const ctrl = new AbortController()
  ctrl.abort()
  try {
    await fetch("http://example.com", { signal: ctrl.signal })
    console.log("no throw")
  } catch (e) {
    console.log(e.name)
  }
}
run()
`, "AbortError")
}

// Aborting after fetch starts but before the await also rejects (the signal is
// aborted by the time we await).
func TestE2EFetchAbortBeforeAwait(t *testing.T) {
	assertOutput(t, `
async function run() {
  const ctrl = new AbortController()
  const p = fetch("http://example.com", { signal: ctrl.signal })
  ctrl.abort()
  try {
    await p
    console.log("no throw")
  } catch (e) {
    console.log(e.name)
  }
}
run()
`, "AbortError")
}

// AbortSignal.timeout(ms) cancels a fetch once its deadline elapses (TDD-00081
// Stage 3c). timeout(0) is already past its deadline by the await, so it throws
// deterministically without depending on network timing. Per the WHATWG spec a
// timeout aborts with a "TimeoutError" DOMException (distinct from the
// "AbortError" a manual controller.abort() produces).
func TestE2EFetchTimeoutSignal(t *testing.T) {
	assertOutput(t, `
async function run() {
  try {
    await fetch("http://example.com", { signal: AbortSignal.timeout(0) })
    console.log("completed")
  } catch (e) {
    console.log(e.name)
  }
}
run()
`, "TimeoutError")
}

// The error a fetch abort throws is a DOMException (which, per the modern
// WebIDL spec, inherits from Error), so both instanceof checks hold and the
// name discriminates the cause (TDD-00081, ADR-00240).
func TestE2EFetchAbortErrorIsDOMException(t *testing.T) {
	assertOutput(t, `
async function run() {
  const ctrl = new AbortController()
  ctrl.abort()
  try {
    await fetch("http://example.com", { signal: ctrl.signal })
  } catch (e) {
    console.log(e.name + " " + (e instanceof DOMException) + " " + (e instanceof Error))
  }
}
run()
`, "AbortError true true")
}

// new DOMException(message?, name?): name is the 2nd arg (default "Error"),
// message the 1st (default ""); it is instanceof both DOMException and Error,
// while a different Error kind is not instanceof DOMException.
func TestE2EDOMExceptionConstruction(t *testing.T) {
	assertOutput(t, `
const d = new DOMException("boom", "NotFoundError")
console.log(d.name + " " + d.message)
console.log((d instanceof DOMException) + " " + (d instanceof Error))
const bare = new DOMException()
console.log(bare.name + " [" + bare.message + "]")
const te = new TypeError("x")
console.log(te instanceof DOMException)
`, "NotFoundError boom\ntrue true\nError []\nfalse")
}

// A function can take an AbortSignal and check it / register a listener.
func TestE2EAbortSignalAsParameter(t *testing.T) {
	assertOutput(t, `
function watch(sig: AbortSignal): void {
  sig.addEventListener("abort", (e: Event) => { console.log("cancelled") })
}
const ctrl = new AbortController()
watch(ctrl.signal)
console.log(ctrl.signal.aborted)
ctrl.abort()
`, "false\ncancelled")
}
