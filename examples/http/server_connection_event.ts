// The server 'connection' event (TDD-00198) fires once per accepted TCP
// connection — before any HTTP parsing — handing the listener a net.Socket over
// the connection. Two requests reusing one keep-alive socket fire it once; a
// fresh socket fires it again. Useful for connection counting, logging, and
// inspecting/writing to the raw socket.
//
// The socket is a handle for `write`/`destroy` and inspection; its own
// `'data'`/`'close'` listeners aren't driven here — the HTTP parser owns the
// byte stream (a documented V1 limitation).

import http from 'http'

let open = 0

const server = http.createServer((req, res) => {
  res.end('served (open connections seen: ' + open + ')')
})

server.on('connection', (socket) => {
  open = open + 1
  console.log('new connection — total so far: ' + open)
})

server.listen(8091, () => {
  console.log('listening on :8091')
})

// Self-close so `make examples` can run this unattended.
setTimeout(() => {
  console.log('closing')
  server.close()
}, 200)
