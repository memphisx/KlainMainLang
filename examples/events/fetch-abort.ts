// Cancelling a fetch with an AbortSignal (TDD-00081 Stage 3b). Passing
// `{ signal }` to fetch makes the await throw an AbortError once the signal is
// aborted — no request is performed when the signal is already aborted. (This
// example aborts before awaiting, so it never touches the network.) See
// docs/status/EVENTS-CANCELLATION.md.

async function run(): Promise<void> {
	const controller = new AbortController()

	// Abort up front — the guarded fetch then rejects instead of running.
	controller.abort()

	try {
		await fetch("http://example.com", { signal: controller.signal })
		console.log("request completed")
	} catch (e) {
		console.log("fetch failed: " + e.name)   // fetch failed: AbortError
		// The thrown value is a real DOMException (which, per the modern spec,
		// inherits from Error), so both instanceof checks below print true.
		console.log("is DOMException: " + (e instanceof DOMException))
		console.log("is Error: " + (e instanceof Error))
	}

	// The same works when the abort lands after fetch starts but before await:
	const c2 = new AbortController()
	const pending = fetch("http://example.com", { signal: c2.signal })
	c2.abort()
	try {
		await pending
	} catch (e) {
		console.log("also aborted: " + e.name)    // also aborted: AbortError
	}

	// AbortSignal.timeout(ms): the fetch is cancelled once ms elapse — the real
	// "give up on a slow request" pattern. (timeout(0) is already past its
	// deadline here, so it aborts immediately without waiting on the network.)
	// A timeout aborts with a "TimeoutError", distinct from the "AbortError" a
	// manual controller.abort() produces — matching the WHATWG spec.
	try {
		await fetch("http://example.com", { signal: AbortSignal.timeout(0) })
	} catch (e) {
		console.log("timed out: " + e.name)        // timed out: TimeoutError
	}
}

run()
