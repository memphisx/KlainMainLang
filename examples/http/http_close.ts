// A minimal HTTP server that shuts itself down cleanly via http.close()
// (TDD-00027) instead of http_server.ts's setTimeout + process.exit()
// workaround — http.close() lets the http.listen() call itself return once
// every already-accepted connection has finished, so the rest of the
// program (here, just a closing console.log) runs for real afterward. A
// self-closing setTimeout is used here too, just to close the listener
// instead of killing the whole process outright, so `make examples` can
// still verify this file runs to completion unattended.
//
//   curl "http://localhost:8081/hello"
//   curl "http://localhost:8081/shutdown"   # closes the listener immediately

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
  return { status: 200, body: 'hello (' + req.method + ' ' + req.path + ')' }
})

console.log('server closed, exiting')
