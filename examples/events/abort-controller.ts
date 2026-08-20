// AbortController / AbortSignal (TDD-00081 Stage 3a) — the WHATWG cancellation
// token. An AbortSignal is an EventTarget that fires an "abort" event, plus an
// `aborted` flag and a `reason`. Wiring the signal into fetch/timers to actually
// cancel in-flight work is a follow-on. See docs/status/EVENTS-CANCELLATION.md.

const controller = new AbortController()
const signal = controller.signal

console.log(signal.aborted)              // false

// Listen for the abort.
signal.addEventListener("abort", (e: Event) => {
	console.log("operation cancelled")   // operation cancelled
})

// The token pattern: pass the signal around and check it.
function step(sig: AbortSignal, name: string): void {
	if (sig.aborted) {
		console.log("skip " + name)
	} else {
		console.log("run " + name)
	}
}
step(signal, "one")                      // run one

// Fire the abort with a reason.
controller.abort("timed out")
console.log(signal.aborted)              // true
console.log(signal.reason)               // timed out

step(signal, "two")                      // skip two
