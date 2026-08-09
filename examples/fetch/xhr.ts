// XMLHttpRequest (TDD-00040) — a legacy synchronous-style client. .send()
// looks synchronous from TS code, but is built on the exact same
// non-blocking primitive fetch() itself uses underneath (see
// runtime_fetch.go's ensureFetchAwaitSettled); it never blocks the whole
// event loop when called from inside an http.listen connection handler.
//
// Like examples/fetch/fetch_init.ts, this talks to a local fixture server
// (tools/httpbin-lite/, started by `make examples` before this file runs —
// see ADR-00096) instead of a real external website.

const xhr = new XMLHttpRequest()
let callCount = 0
xhr.onreadystatechange = () => {
    callCount = callCount + 1
    console.log(xhr.readyState)  // 1 (OPENED) on the first call, 4 (DONE) on the second
}
xhr.onload = () => {
    console.log('loaded')
}
xhr.open('GET', 'http://127.0.0.1:8765/get')
xhr.send()
console.log(callCount)   // 2
console.log(xhr.status)  // 200

// POST with a custom request header and a body
const posted = new XMLHttpRequest()
posted.open('POST', 'http://127.0.0.1:8765/post')
posted.setRequestHeader('X-Example-Header', 'kml-value')
posted.send(JSON.stringify({ hello: 'world' }))
console.log(posted.status)  // 200
console.log(posted.responseText.indexOf('"hello":"world"') > -1)  // 1 (true)

// A network-level failure fires onerror instead of throwing
const failed = new XMLHttpRequest()
let sawError = false
failed.onerror = () => { sawError = true }
failed.open('GET', 'http://127.0.0.1:1/unreachable')
failed.send()
console.log(sawError)     // 1 (true)
console.log(failed.status)  // 0
