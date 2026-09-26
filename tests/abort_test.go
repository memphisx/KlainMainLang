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

// ADR-00978: the `signal.onabort` event-handler property fires alongside the
// addEventListener('abort') listeners on controller.abort(), sees aborted=true,
// clears with `= null`, and replaces on re-assignment.
func TestE2EAbortSignalOnabort(t *testing.T) {
	assertOutput(t, `
const c = new AbortController()
const sig = c.signal
sig.onabort = (e: Event) => { console.log("onabort", e.type, sig.aborted) }
let viaListener = false
sig.addEventListener("abort", () => { viaListener = true })
c.abort()
console.log("listener", viaListener)
`, "onabort abort true\nlistener true")
}

func TestE2EAbortSignalOnabortNullAndReplace(t *testing.T) {
	assertOutput(t, `
const c1 = new AbortController()
let f1 = false
c1.signal.onabort = () => { f1 = true }
c1.signal.onabort = null
c1.abort()
console.log("cleared", f1)

const c2 = new AbortController()
let which = ""
c2.signal.onabort = () => { which = "first" }
c2.signal.onabort = () => { which = "second" }
c2.abort()
console.log("replaced", which)

const c3 = new AbortController()
let f3 = false
c3.signal.onabort = () => { f3 = true }
console.log("noabort", f3)
`, "cleared false\nreplaced second\nnoabort false")
}

// TDD-00216: AbortSignal.timeout fires its abort in the background at the
// deadline — aborted becomes true and onabort + addEventListener('abort')
// listeners run — while the loop is alive for other work (here a later timer).
// onabort fires before the addEventListener listeners (shared event).
func TestE2EAbortSignalTimeoutBackgroundDispatch(t *testing.T) {
	assertOutput(t, `
const sig = AbortSignal.timeout(20)
let order = ""
sig.addEventListener("abort", () => { order += "L" })
sig.onabort = () => { order += "O" }
setTimeout(() => { console.log(order + " aborted=" + sig.aborted) }, 150)
`, "OL aborted=true")
}

// TDD-00216: a lone AbortSignal.timeout keeps nothing alive (Node unref parity) —
// with no other pending work the program exits immediately and the abort never
// fires, rather than hanging for the timeout.
func TestE2EAbortSignalTimeoutUnref(t *testing.T) {
	assertOutput(t, `
const sig = AbortSignal.timeout(10000)
sig.onabort = () => { console.log("SHOULD NOT FIRE") }
console.log("immediate")
`, "immediate")
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
    console.log((e as Error).name)
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
    console.log((e as Error).name)
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
    console.log((e as Error).name)
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
    console.log((e as Error).name + " " + (e instanceof DOMException) + " " + (e instanceof Error))
  }
}
run()
`, "AbortError true true")
}

// A fetch aborted with a custom reason rejects with that reason value itself
// (Node/WHATWG: fetch rejects with signal.reason) — any value's type is
// preserved, and an Error reason keeps its full shape in the catch handler.
func TestE2EFetchAbortCustomReasonRethrown(t *testing.T) {
	assertOutput(t, `
async function run() {
  const c = new AbortController()
  c.abort(42)
  try {
    await fetch("http://example.com", { signal: c.signal })
    console.log("no throw")
  } catch (e) {
    console.log(e, typeof e)
  }
  const c2 = new AbortController()
  c2.abort(new Error("cancelled by user"))
  try {
    await fetch("http://example.com", { signal: c2.signal })
    console.log("no throw")
  } catch (e) {
    console.log(e instanceof Error, (e as Error).message)
  }
}
run()
`, "42 number\ntrue cancelled by user")
}

// Throwing an `any` that holds a boxed Error restores the Error shape at the
// catch site (the throw runtime consults the boxed-object type-id): .name,
// .message, and instanceof all behave as if the Error were thrown unboxed.
func TestE2EThrowBoxedErrorAnyKeepsShape(t *testing.T) {
	assertOutput(t, `
const x: any = new Error("boom")
try {
  throw x
} catch (e) {
  console.log((e as Error).name, (e as Error).message, e instanceof Error)
}
`, "Error boom true")
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
