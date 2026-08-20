// Concurrent async functions — TDD-00083 Stage 2.
//
// A "may-suspend" async function (one whose body awaits a fetch, or another
// such function) is compiled as a coroutine task: calling it runs the body up
// to its first `await`, suspends, and returns a still-pending promise. So two
// such calls made before either is awaited start both fetches and run them
// concurrently — unlike an ordinary async function, which runs to completion
// synchronously at call time (see examples/async/async.ts).
//
// Talks to the local httpbin-lite fixture server (tools/httpbin-lite/, started
// by `make examples` — ADR-00096), so it needs no real network and is
// deterministic.

async function fetchStatus(url: string): Promise<number> {
    const r = await fetch(url)
    return r.status
}

// ── Composition concurrency: both fetches are in flight before either await ──
const p1 = fetchStatus('http://127.0.0.1:8765/get')
const p2 = fetchStatus('http://127.0.0.1:8765/status/404')
console.log(await p1)   // 200
console.log(await p2)   // 404

// ── Promise.all over task promises: real concurrency, results in order ───────
const all: number[] = await Promise.all([
    fetchStatus('http://127.0.0.1:8765/get'),
    fetchStatus('http://127.0.0.1:8765/status/404'),
    fetchStatus('http://127.0.0.1:8765/get')
])
console.log(all[0] + ' ' + all[1] + ' ' + all[2])   // 200 404 200

// ── Promise.race / .any: the first member to settle wins ─────────────────────
// The slow one (delay/1) is listed first, so a fast result proves a real race.
const winner: number = await Promise.race([
    fetchStatus('http://127.0.0.1:8765/delay/1'),
    fetchStatus('http://127.0.0.1:8765/get')
])
console.log(winner)   // 200 — the fast one

const firstOk: number = await Promise.any([
    fetchStatus('http://127.0.0.1:8765/delay/1'),
    fetchStatus('http://127.0.0.1:8765/get')
])
console.log(firstOk)   // 200

// ── Promise.any over *raw* fetches (TDD-00084 Part C) ────────────────────────
// The array holds fetch() promises directly (not wrapped in an async fn). An
// unreachable host fails at the transport level and is skipped; the reachable
// one wins. If every fetch failed at the transport level, .any would throw an
// AggregateError whose .errors holds one Error per failed fetch.
const anyResp: Response = await Promise.any([
    fetch('http://127.0.0.1:1/unreachable'),
    fetch('http://127.0.0.1:8765/get')
])
console.log(anyResp.status)   // 200

// ── A nullable-scalar parameter in a genuinely-suspending fn (Part C) ────────
async function statusOr(url: string, fallback: number | null): Promise<number> {
    const r = await fetch(url)
    return r.ok ? r.status : (fallback ?? -1)
}
console.log(await statusOr('http://127.0.0.1:8765/get', null))        // 200
console.log(await statusOr('http://127.0.0.1:8765/status/404', 999)) // 999
