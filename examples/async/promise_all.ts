// Promise.all / .race / .allSettled — ADR-00073, TDD-00016.
//
// Two genuinely different cases:
//
// 1. Ordinary async functions: every one of them already ran to completion
//    synchronously by the time its call returns (see examples/async/async.ts)
//    — so there's nothing to parallelize; .all/.race/.allSettled just give
//    an honest, order-preserving read of already-resolved values.
// 2. Array<Promise<Response>> (fetch()'s own Promise type): real
//    concurrency — N in-flight HTTP requests waited on together via this
//    compiler's event loop (docs/tdd/TDD-00006.md), not one at a time.
//    Like examples/fetch/fetch.ts, this half talks to a local fixture
//    server (tools/httpbin-lite/, started by `make examples` — see
//    ADR-00096) instead of a real external website, so it needs no real
//    network access and gives deterministic results.
//
// Note: like a plain `await`, each combinator call consumes (frees) every
// element's own Promise slot — the same array variable can't be fed to a
// second combinator call afterward. Each section below uses its own array.

async function double(n: number): Promise<number> {
    return n * 2
}

// ── Promise.all: collect every value, in order ──────────────────────────────
const forAll: Array<Promise<number>> = []
forAll.push(double(1))
forAll.push(double(2))
forAll.push(double(3))
const allResults = await Promise.all(forAll)
console.log(allResults.length)     // 3
for (const x of allResults) {
    console.log(x)                  // 2, 4, 6
}

// ── Promise.race: the first element, honestly reported ──────────────────────
// (nothing to actually race — every ordinary promise is already resolved)
const forRace: Array<Promise<number>> = []
forRace.push(double(10))
forRace.push(double(20))
const winner = await Promise.race(forRace)
console.log(winner)                 // 20

// ── Promise.allSettled: every ordinary promise is always "fulfilled" ───────
const forSettled: Array<Promise<number>> = []
forSettled.push(double(5))
forSettled.push(double(6))
const settled = await Promise.allSettled(forSettled)
for (const s of settled) {
    console.log(s.status)           // fulfilled, fulfilled
    console.log(s.value)            // 10, 12
}

// ── real concurrency: N in-flight fetches waited on together ───────────────
const forFetchAll: Array<Promise<Response>> = []
forFetchAll.push(fetch('http://127.0.0.1:8765/get'))
forFetchAll.push(fetch('http://127.0.0.1:8765/status/404'))
const responses = await Promise.all(forFetchAll)
console.log(responses[0].status)    // 200
console.log(responses[1].status)    // 404

// Promise.race genuinely settles to whichever fetch finishes first — the
// slow one is listed first in the array, so a passing result here can only
// mean a real race, not "always the first array element."
const forFetchRace: Array<Promise<Response>> = []
forFetchRace.push(fetch('http://127.0.0.1:8765/delay/1'))
forFetchRace.push(fetch('http://127.0.0.1:8765/get'))
const fastest = await Promise.race(forFetchRace)
console.log(fastest.status)         // 200 — the fast one wins

// Promise.allSettled never throws on an individual member's transport
// failure — an unreachable host settles "rejected" instead of aborting the
// whole combinator.
const forFetchSettled: Array<Promise<Response>> = []
forFetchSettled.push(fetch('http://127.0.0.1:8765/get'))
forFetchSettled.push(fetch('https://this-domain-absolutely-does-not-exist-12345.invalid/'))
const fetchSettled = await Promise.allSettled(forFetchSettled)
console.log(fetchSettled[0].status) // fulfilled
console.log(fetchSettled[1].status) // rejected
