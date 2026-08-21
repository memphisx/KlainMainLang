// .then / .catch / .finally directly on a raw fetch() Promise<Response> — no
// surrounding async function. The fetch is driven to a settled promise and each
// chain runs as a microtask after the synchronous script (ADR-00258).
//
// Needs a reachable host: `make examples` starts the local httpbin-lite fixture
// on :8765, same as the other examples/fetch/*.ts files.

// A value chain: the unannotated `r` is hinted to Response, so `r.status` works.
fetch('http://127.0.0.1:8765/get')
  .then((r) => r.status)
  .then((s: number) => { console.log('status ' + s) })      // status 200

// An HTTP 404 is a *fulfilled* Response (per WHATWG) — .then sees it; .finally
// runs for its side effect and passes the source value through unchanged.
fetch('http://127.0.0.1:8765/status/404')
  .then((r) => { console.log(r.status + ' ok=' + r.ok) })    // 404 ok=false
  .finally(() => { console.log('done checking') })

// A transport-level failure (connection refused) rejects the chain — .catch
// recovers it, exactly like a rejected async-function promise.
fetch('http://127.0.0.1:1/nope')
  .then((r) => { console.log('unexpected ' + r.status) })
  .catch((e) => { console.log('unreachable: ' + e.message) })

console.log('sync first')
