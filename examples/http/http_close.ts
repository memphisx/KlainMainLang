// A minimal HTTP server that shuts itself down cleanly via http.close()
// (TDD-00027) instead of http_server.ts's setTimeout + process.exit()
// workaround — http.close() lets the http.listen() call itself return once
// every already-accepted connection has finished, so the rest of the
// program (here, just a closing console.log) runs for real afterward. A
// self-closing setTimeout is used here too, just to close the listener
// instead of killing the whole process outright, so `make examples` can
// still verify this file runs to completion unattended.
//
// The /drain route shows the forceful shutdown pattern: http.close() stops
// accepting new connections, then http.closeAllConnections() (TDD-00118)
// force-terminates any that are still in flight instead of waiting for them.
//
//   curl "http://localhost:8081/hello"
//   curl "http://localhost:8081/shutdown"   # graceful: stop accepting, let in-flight finish
//   curl "http://localhost:8081/drain"      # forceful: stop accepting + drop in-flight now

import http from 'http'

interface Res {
  status: number
  body: string
}

let requestCount = 0

setTimeout(() => {
  console.log('auto-closing after demo delay')
  http.close()
}, 300)

console.log('listening on :8081')

http.listen(8081, (req: HttpRequest): Res => {
  requestCount = requestCount + 1
  if (req.path === '/shutdown') {
    http.close()
    return { status: 200, body: 'shutting down after ' + requestCount + ' request(s)' }
  }
  if (req.path === '/drain') {
    // Forceful shutdown: stop accepting, then drop every in-flight connection.
    // closeAllConnections() shuts down this request's own socket too, so the
    // returned response below won't reach the client — the caller sees the
    // connection close instead. That's the forceful contract (Node parity).
    http.close()
    http.closeAllConnections()
    return { status: 200, body: 'draining all connections' }
  }
  return { status: 200, body: 'hello (' + req.method + ' ' + req.path + ')' }
})

console.log('server closed, exiting')
