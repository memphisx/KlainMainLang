package tests

import "testing"

// The WHATWG EventTarget bus (TDD-00081 Stage 2): addEventListener /
// removeEventListener / dispatchEvent over a Map<string, listener-list>
// registry, single-target dispatch.

func TestE2EEventTargetDispatch(t *testing.T) {
	assertOutput(t, `
const bus = new EventTarget()
let count = 0
bus.addEventListener("ping", (e: Event) => { count = count + 1 })
bus.dispatchEvent(new Event("ping"))
bus.dispatchEvent(new Event("ping"))
console.log(count)
`, "2")
}

// A zero-argument listener (ADR-00967) is valid — WHATWG calls every listener
// with the event as its sole argument, and one that declares no parameter simply
// ignores it. Covers a plain arrow, `once`, a const-bound removable handler, and
// AbortSignal (which shares the listener resolver).
func TestE2EEventTargetZeroArgListener(t *testing.T) {
	assertOutput(t, `
const bus = new EventTarget()
let n = 0
bus.addEventListener("x", () => { n = n + 1 })
bus.dispatchEvent(new Event("x"))
bus.dispatchEvent(new Event("x"))
console.log(n)

let once = 0
bus.addEventListener("y", () => { once = once + 1 }, { once: true })
bus.dispatchEvent(new Event("y"))
bus.dispatchEvent(new Event("y"))
console.log(once)

let kept = 0
const h = () => { kept = kept + 1 }
bus.addEventListener("z", h)
bus.removeEventListener("z", h)
bus.dispatchEvent(new Event("z"))
console.log(kept)

const ac = new AbortController()
ac.signal.addEventListener("abort", () => { console.log("aborted") })
ac.abort()
`, "2\n1\n0\naborted")
}

// A CustomEvent's detail reaches a listener typed as CustomEvent.
func TestE2EEventTargetCustomDetail(t *testing.T) {
	assertOutput(t, `
const bus = new EventTarget()
bus.addEventListener("data", (e: CustomEvent) => { console.log(e.detail) })
bus.dispatchEvent(new CustomEvent("data", { detail: "payload" }))
`, "payload")
}

func TestE2EEventTargetRemove(t *testing.T) {
	assertOutput(t, `
const bus = new EventTarget()
let n = 0
const inc = (e: Event) => { n = n + 1 }
bus.addEventListener("x", inc)
bus.dispatchEvent(new Event("x"))
bus.removeEventListener("x", inc)
bus.dispatchEvent(new Event("x"))
console.log(n)
`, "1")
}

// removeEventListener must match a listener passed by a named-function
// reference (not only a const-bound arrow) — a stable static header per named
// function makes the pointer comparison succeed. Regressed when each reference
// malloc'd a fresh header.
func TestE2EEventTargetRemoveByNamedFunctionRef(t *testing.T) {
	assertOutput(t, `
let hits = 0
function onClick(e: Event): void { hits = hits + 1 }
const bus = new EventTarget()
bus.addEventListener("click", onClick)
bus.dispatchEvent(new Event("click"))
bus.removeEventListener("click", onClick)
bus.dispatchEvent(new Event("click"))
console.log(hits)
`, "1")
}

func TestE2EEventTargetOnce(t *testing.T) {
	assertOutput(t, `
const bus = new EventTarget()
let m = 0
bus.addEventListener("y", (e: Event) => { m = m + 1 }, { once: true })
bus.dispatchEvent(new Event("y"))
bus.dispatchEvent(new Event("y"))
console.log(m)
`, "1")
}

// dispatchEvent returns false when a listener called preventDefault.
func TestE2EEventTargetDispatchReturn(t *testing.T) {
	assertOutput(t, `
const bus = new EventTarget()
bus.addEventListener("z", (e: Event) => { e.preventDefault() })
const ok = bus.dispatchEvent(new Event("z", { cancelable: true }))
console.log(ok)
`, "false")
}

// stopImmediatePropagation halts the remaining listeners for that dispatch.
func TestE2EEventTargetStopImmediate(t *testing.T) {
	assertOutput(t, `
const bus = new EventTarget()
let order = ""
bus.addEventListener("s", (e: Event) => { order = order + "A"; e.stopImmediatePropagation() })
bus.addEventListener("s", (e: Event) => { order = order + "B" })
bus.dispatchEvent(new Event("s"))
console.log(order)
`, "A")
}
