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
const ok = bus.dispatchEvent(new Event("z"))
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
