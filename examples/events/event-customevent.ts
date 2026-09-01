// WHATWG Event / CustomEvent objects (TDD-00081 Stage 1) — the base event types,
// distinct from Node's EventEmitter. The EventTarget bus that dispatches them
// (addEventListener/dispatchEvent) is a later stage. See
// docs/status/EVENTS-CANCELLATION.md.

// A plain Event carries its type and a cancellation flag. preventDefault only
// takes effect on a cancelable event (WHATWG) — a non-cancelable one is a no-op.
const clicked = new Event("click", { cancelable: true })
console.log(clicked.type)                 // click
console.log(clicked.defaultPrevented)     // false
clicked.preventDefault()
console.log(clicked.defaultPrevented)     // true

const scroll = new Event("scroll")        // not cancelable
scroll.preventDefault()
console.log(scroll.defaultPrevented)      // false (no-op)

// CustomEvent adds a `detail` payload of any type.
const greeting = new CustomEvent("greet", { detail: "hello" })
console.log(greeting.type)                // greet
console.log(greeting.detail)              // hello

const scored = new CustomEvent("score", { detail: 42 })
console.log(scored.detail)                // 42

// An object detail works too.
interface Point { x: number; y: number }
const moved = new CustomEvent("move", { detail: { x: 3, y: 4 } })
console.log(moved.detail.x + moved.detail.y)  // 7

// `Event` is usable as a parameter type; the event is mutated in place.
function cancel(e: Event): void {
	e.preventDefault()
}
const submit = new Event("submit", { cancelable: true })
cancel(submit)
console.log(submit.defaultPrevented)      // true
