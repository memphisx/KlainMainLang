// fetch — reading response headers.
//
// `res.headers` parses the raw response-header text libcurl captured into a
// Map<string, string>. Keys are lowercased (fetch's Headers rule), so look
// them up as 'content-type', not 'Content-Type'. Uses the same local
// fixture server as examples/fetch/fetch.ts — no real network access.

const r = await fetch('http://127.0.0.1:8765/get')
const h: Map<string, string> = r.headers

console.log(r.status)                    // 200
console.log(h.get('content-type'))       // text/plain; charset=utf-8
console.log(h.has('content-length'))     // true
console.log(h.has('x-does-not-exist'))   // false
