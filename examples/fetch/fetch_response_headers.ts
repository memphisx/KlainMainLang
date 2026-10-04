// fetch — reading response headers.
//
// `res.headers` is the Headers of the response, filled from the header text
// libcurl captured. Lookups are case-insensitive, and the headers of a
// fetched response are immutable. Uses the same local fixture server as
// examples/fetch/fetch.ts — no real network access.

const r = await fetch('http://127.0.0.1:8765/get')
const h: Headers = r.headers

console.log(r.status)                    // 200
console.log(h.get('Content-Type'))       // text/plain; charset=utf-8
console.log(h.has('content-length'))     // true
console.log(h.has('x-does-not-exist'))   // false
try {
    h.set('x-mine', '1')
} catch (e) {
    console.log((e as Error).message)    // immutable
}
