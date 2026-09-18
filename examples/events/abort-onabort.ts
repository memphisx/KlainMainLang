// AbortSignal.onabort (ADR-00978) — the event-handler property. It is a
// listener slot fired alongside the addEventListener("abort", …) listeners when
// controller.abort() runs, sharing one "abort" event. Setting it to null clears
// it; re-assigning replaces the previous handler.

const controller = new AbortController()
const signal = controller.signal

// The onabort property: assign a single handler.
signal.onabort = (e: Event) => {
	console.log("onabort:", e.type, "aborted=" + signal.aborted)  // onabort: abort aborted=true
}

// It coexists with addEventListener("abort", …) — both fire.
signal.addEventListener("abort", () => {
	console.log("addEventListener also fired")                    // addEventListener also fired
})

console.log("before:", signal.aborted)                          // before: false
controller.abort()
console.log("after:", signal.aborted)                           // after: true
