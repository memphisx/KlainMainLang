// A handler whose response `body` is a union `string | ReadableStream<Uint8Array>`
// (TDD-00119): one route answers with a plain string, another streams the request
// body straight back (chunked transfer). The response writer picks the buffered
// or chunked path from the value's runtime tag — the handler just returns either
// shape.
//
//   curl "http://localhost:8082/hello"                       # string body
//   curl --data-binary @somefile "http://localhost:8082/echo"  # streamed body
//
// A self-closing setTimeout keeps `make examples` able to run this to completion
// unattended (same shape as http_close.ts).

import http from 'http'

interface Res {
  status: number
  body: string | ReadableStream<Uint8Array>
}

setTimeout(() => {
  console.log('auto-closing after demo delay')
  http.close()
}, 300)

console.log('listening on :8082')

http.listen(8082, (req: HttpRequest): Res => {
  if (req.path === '/echo') {
    // Stream the request body back without ever buffering it whole.
    return { status: 200, body: req.stream() }
  }
  return { status: 200, body: 'hello (' + req.method + ' ' + req.path + ')' }
})

console.log('server closed, exiting')
