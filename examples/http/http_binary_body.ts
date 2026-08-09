// Binary-safe request/response bodies (TDD-00026/ADR-00106).
//
// req.body: string / Res.body: string are plain null-terminated C strings —
// a body containing an embedded null byte silently truncates through them.
// req.bodyBytes(): ArrayBuffer and an optional Res.bodyBytes: ArrayBuffer
// field are the binary-safe counterparts, carrying the real byte count
// (the same ArrayBuffer type TypedArrays already use, TDD-00018) instead of
// relying on strlen. When both `body` and `bodyBytes` are set on a response,
// `bodyBytes` wins.
//
// This example echoes the request body back through bodyBytes, so an
// embedded null byte survives the round trip. http.listen never returns on
// its own, so this schedules a setTimeout that exits the process after a
// short delay — the same trick http_server.ts uses so `make examples` can
// verify this file runs to completion without needing a real client.
// Point a real client at it while it's running to see the binary round trip
// actually happen, e.g. (the \x00 embeds a real null byte in the body):
//   printf 'AB\x00CD' | curl --data-binary @- http://localhost:8081/echo | xxd

interface Res {
  status: number
  body: string
  bodyBytes: ArrayBuffer
}

setTimeout(() => {
  console.log('shutting down')
  process.exit(0)
}, 300)

console.log('listening on :8081')

http.listen(8081, (req: HttpRequest): Res => {
  // .bodyBytes() exposes the exact byte range the server already buffered
  // (Content-Length-aware, ADR-00072) — .byteLength reflects the real
  // count even when the body contains an embedded null.
  let buf: ArrayBuffer = req.bodyBytes()
  console.log('received ' + buf.byteLength + ' byte(s)')

  // body is still required (this compiler has no optional object fields),
  // but is ignored on the wire once bodyBytes is non-null.
  return { status: 200, body: '', bodyBytes: buf }
})
