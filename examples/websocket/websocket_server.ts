// A minimal WebSocket echo server on the Node-faithful surface (TDD-00158):
// a real `http.createServer(...)` server, with the WebSocket layer attached
// through `klain:ws`'s `WebSocketServer` — this project's own ergonomic
// re-imagining of the `ws` npm package (Node core has no built-in WebSocket
// server; it exposes the raw `server.on('upgrade', …)` event, over which the
// `ws` package builds exactly this). Under `klain:ws` you get a `WSConnection`
// per upgraded socket — `.onmessage`/`.send()`/`.close()` — with automatic
// ping→pong replies (RFC 6455 §5.5.2) and a proper Close-frame handshake
// (§5.5.1), no framing code here at all. `wss://` needs only
// `https.createServer({ cert, key })` in place of `http.createServer` — the
// same WebSocketServer attaches, and the frame I/O rides the TLS connection.
//
// The same server still serves ordinary HTTP: an upgrade request is detected
// and diverted before it reaches the (req, res) handler below.
//
// Point a real WebSocket client at it while it's running to see it echo —
// websocket_client.ts in this same directory is one (comment out the
// setTimeout below first: it self-closes after 300ms for `make examples`'
// own unattended run, too short a window to switch terminals and connect
// manually), or:
//   (Node) node -e "const ws=new (require('ws'))('ws://localhost:8083'); \
//     ws.on('open',()=>ws.send('hello')); \
//     ws.on('message',m=>console.log('got:',m.toString()))"
//   curl "http://localhost:8083/hello"   # the server still serves plain HTTP too

import http from 'http'
import { WebSocketServer } from 'klain:ws'

const server = http.createServer((req: IncomingMessage, res: ServerResponse) => {
  res.writeHead(200)
  res.end('plain HTTP: ' + req.method + ' ' + req.path)
})

const wss = new WebSocketServer({ server })
wss.on('connection', (socket: WSConnection) => {
  console.log('client connected')
  socket.onmessage = (ev) => {
    console.log('received: ' + ev.data)
    if (ev.data === 'bye') {
      socket.close()
    } else {
      socket.send('echo: ' + ev.data)
    }
  }
})

setTimeout(() => {
  console.log('auto-closing after demo delay')
  server.close()
}, 300)

console.log('listening on :8083 (HTTP + WebSocket upgrade)')
server.listen(8083)
console.log('server closed, exiting')
