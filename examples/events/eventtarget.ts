// The WHATWG EventTarget bus (TDD-00081 Stage 2): addEventListener /
// removeEventListener / dispatchEvent, distinct from Node's EventEmitter. Single-
// target dispatch (no DOM capture/bubble tree). See
// docs/status/EVENTS-CANCELLATION.md.

const bus = new EventTarget()

// Register a listener and fire the event twice.
let pings = 0
bus.addEventListener("ping", (e: Event) => {
	pings = pings + 1
	console.log("ping " + pings)      // ping 1 / ping 2
})
bus.dispatchEvent(new Event("ping"))
bus.dispatchEvent(new Event("ping"))

// A CustomEvent's detail reaches the listener.
bus.addEventListener("message", (e: CustomEvent) => {
	console.log("got: " + e.detail)   // got: hello
})
bus.dispatchEvent(new CustomEvent("message", { detail: "hello" }))

// removeEventListener stops future dispatches (the listener must be a named
// value so it matches by identity).
const onTick = (e: Event) => console.log("tick")
bus.addEventListener("tick", onTick)
bus.dispatchEvent(new Event("tick"))    // tick
bus.removeEventListener("tick", onTick)
bus.dispatchEvent(new Event("tick"))    // (nothing)

// { once: true } auto-removes after the first dispatch.
bus.addEventListener("boot", (e: Event) => console.log("boot once"), { once: true })
bus.dispatchEvent(new Event("boot"))    // boot once
bus.dispatchEvent(new Event("boot"))    // (nothing)

// dispatchEvent returns false when a listener calls preventDefault().
bus.addEventListener("save", (e: Event) => e.preventDefault())
const proceeded = bus.dispatchEvent(new Event("save"))
console.log("proceeded: " + proceeded)  // proceeded: false
