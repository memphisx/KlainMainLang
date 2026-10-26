// An http.listen server that transparently serves HTTP/2 cleartext (h2c) as
// well as HTTP/1.1 (TDD-00111 Stage 3). No API change: a connection whose first
// bytes are the HTTP/2 preface is driven by the nghttp2 session driver and
// dispatched through the same handler; everything else is HTTP/1.1. Needs
// libnghttp2 installed on the build machine (linked only for http.listen).
//
//   curl --http2-prior-knowledge http://localhost:<port>/hello   # served over h2
//   curl http://localhost:<port>/hello                            # served over 1.1
//
// Self-closes after a short delay so it runs to completion unattended.

import http from 'klain:http'  // bespoke handler⇒response server (Node's faithful shape is http.createServer)

interface Res {
  status: number
  body: string
}

setTimeout(() => {
  console.log('auto-closing after demo delay')
  http.close()
}, 300)

http.listen(0, (req: HttpRequest): Res => {
  return { status: 200, body: 'hello (' + req.method + ' ' + req.path + ')' }
}, { onListening: (port: number) => { console.log('listening on :' + port + ' (HTTP/1.1 + h2c)') } })

console.log('server closed, exiting')
