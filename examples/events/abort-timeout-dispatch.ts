// AbortSignal.timeout background dispatch (ADR-00979). A timeout signal aborts on
// its own at the deadline: `aborted` flips to true and the abort listeners +
// onabort fire with a TimeoutError reason — driven by the event loop / timer
// system while it is alive for other work. It never keeps the process alive on
// its own (Node unref parity), so a lone timeout with nothing else pending would
// simply exit. Here a later timer keeps the loop alive long enough to observe it.

const sig = AbortSignal.timeout(50)

sig.addEventListener("abort", () => {
	console.log("addEventListener: aborted, reason =", sig.reason.name)  // ... TimeoutError
})
sig.onabort = () => {
	console.log("onabort: aborted =", sig.aborted)                       // onabort: aborted = true
}

// A later timer keeps the event loop alive past the 50ms deadline so the abort
// fires in the background before this runs.
setTimeout(() => {
	console.log("after deadline, aborted =", sig.aborted)               // after deadline, aborted = true
}, 150)
