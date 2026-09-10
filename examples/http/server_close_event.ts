// The server 'close' event and a drain-accurate server.close(cb) (TDD-00197).
// Both fire once the server has stopped listening AND every in-flight
// connection has finished — not synchronously when close() is called. Here the
// server closes itself from its 'listening' callback (no connections in flight),
// so it drains immediately, fires both handlers in registration order, and the
// process exits cleanly with no lingering event loop.
//
//   'close' event first, then the close(cb) one-shot — the order Node uses.

import http from 'http'

const server = http.createServer((req, res) => {
  res.end('hello from ' + req.url)
})

server.on('close', () => {
  console.log('server close event')
})

server.listen(8090, () => {
  console.log('listening on :8090')
  // Graceful shutdown: the callback settles once the server has fully drained.
  server.close(() => {
    console.log('server fully closed (callback)')
  })
})
