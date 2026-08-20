package tests

import "testing"

// WHATWG Event / CustomEvent objects (TDD-00081 Stage 1). The EventTarget bus
// that dispatches them is Stage 2.

func TestE2EEventBasics(t *testing.T) {
	assertOutput(t, `
const e = new Event("click")
console.log(e.type)
console.log(e.defaultPrevented)
e.preventDefault()
console.log(e.defaultPrevented)
`, "click\nfalse\ntrue")
}

func TestE2ECustomEventDetail(t *testing.T) {
	assertOutput(t, `
const ce = new CustomEvent("greet", { detail: "hello" })
console.log(ce.type)
console.log(ce.detail)
const n = new CustomEvent("count", { detail: 42 })
console.log(n.detail)
`, "greet\nhello\n42")
}

// An Event passed to a function is mutated in place (preventDefault sets the
// caller's defaultPrevented), and `Event` works as a parameter annotation.
func TestE2EEventAsParameter(t *testing.T) {
	assertOutput(t, `
function handle(e: Event): string {
  e.preventDefault()
  return e.type
}
const ev = new Event("submit")
console.log(handle(ev))
console.log(ev.defaultPrevented)
`, "submit\ntrue")
}

// stopPropagation is an accepted no-op; stopImmediatePropagation is accepted.
func TestE2EEventStopMethods(t *testing.T) {
	assertOutput(t, `
const e = new Event("x")
e.stopPropagation()
e.stopImmediatePropagation()
console.log(e.type)
`, "x")
}
