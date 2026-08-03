// setTimeout / clearTimeout / setInterval / clearInterval
//
// Bare global functions, matching real JS (not a namespace). Delays are in
// milliseconds. No general-purpose event loop needed for these — just a
// sleep-until-next-due queue that drains after this program's own
// synchronous top-level code finishes (see docs/tdd/TDD-00002.md for the
// full design, and docs/status/TIMERS.md for current status).
//
// V1 scope: the callback must be an arrow function / closure value —
// passing a bare reference to a top-level named function (e.g.
// `setTimeout(myFunction, 10)`) doesn't work yet, since named top-level
// functions aren't first-class values anywhere in this compiler (a
// pre-existing, general limitation, not specific to timers).
//
// IMPORTANT: an active setInterval that nothing ever clears keeps this
// program running forever, matching real Node's own behavior — every other
// example in this repo runs and exits immediately, but this one doesn't,
// until every timer has either fired-and-not-repeated or been cleared.

console.log("start")

// ── setTimeout fires once, after the delay ──────────────────────────────────
setTimeout(() => {
    console.log("timeout fired")
}, 10)

// ── multiple timeouts fire in delay order, not registration order ──────────
setTimeout(() => { console.log("third (30ms)") }, 30)
setTimeout(() => { console.log("first (5ms)") }, 5)
setTimeout(() => { console.log("second (15ms)") }, 15)

// ── clearTimeout cancels a timeout before it ever fires ─────────────────────
const cancelledId = setTimeout(() => {
    console.log("this should never print")
}, 8)
clearTimeout(cancelledId)

// ── setInterval repeats; the idiomatic self-cancelling pattern ─────────────
// The callback reads `id` — the very variable its own declaration is still
// in the middle of producing. This works correctly (closures capture by
// reference, matching real JS), but is worth calling out: it's exactly the
// pattern that surfaced a real bug in this compiler while this feature was
// being built (a variable's initializer creating a closure that captures
// that same variable) — see docs/adr/ for the fix.
let count: number = 0
const id = setInterval(() => {
    count = count + 1
    console.log("interval tick " + count)
    if (count >= 3) {
        clearInterval(id)
        console.log("interval cleared, program will exit once all timers are done")
    }
}, 5)

// ── setImmediate/clearImmediate — schedule for "as soon as possible",     ──
// reusing the exact same timer queue as delay-0 setTimeout. Known scope
// narrowing: real Node guarantees setImmediate fires before a same-tick
// setTimeout(fn, 0) when scheduled from inside an I/O callback (distinct
// event-loop phases); this compiler's single flat fire-time-ordered queue
// has no phase concept, so the two are genuinely indistinguishable here —
// see docs/status/TIMERS.md.
setImmediate(() => {
    console.log("immediate fired")
})
const cancelledImmediateId = setImmediate(() => {
    console.log("this should never print either")
})
clearImmediate(cancelledImmediateId)

console.log("end of synchronous code — timers fire below")
