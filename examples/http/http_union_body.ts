// A handler whose response `body` is a union `string | ReadableStream<Uint8Array>`
// (TDD-00119): one route answers with a plain string, another streams the request
// body straight back (chunked transfer). The response writer picks the buffered
// or chunked path from the value's runtime tag — the handler just returns either
// shape.
//
//   curl "http://localhost:<port>/hello"                       # string body
//   curl --data-binary @somefile "http://localhost:<port>/echo"  # streamed body
//
// A self-closing setTimeout keeps `make examples` able to run this to completion
// unattended (same shape as http_close.ts).

import http from 'klain:http'  // bespoke handler⇒response server (Node's faithful shape is http.createServer)

interface Res {
  status: number
  body: string | ReadableStream<Uint8Array>
}

setTimeout(() => {
  console.log('auto-closing after demo delay')
  http.close()
}, 300)

http.listen(0, (req: HttpRequest): Res => {
  if (req.path === '/echo') {
    // Stream the request body back without ever buffering it whole.
    return { status: 200, body: req.stream() }
  }
  return { status: 200, body: 'hello (' + req.method + ' ' + req.path + ')' }
}, { onListening: (port: number) => { console.log('listening on :' + port) } })

console.log('server closed, exiting')
