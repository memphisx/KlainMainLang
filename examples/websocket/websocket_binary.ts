// Binary WebSocket frames (TDD-00160): a `klain:ws` echo server that handles
// *binary* messages, not just text. Each `WSConnection` message event now
// carries `ev.isBinary` (the Node `ws` `(data, isBinary)` discriminant) and a
// byte-exact `ev.dataBytes(): ArrayBuffer` accessor — unlike the strlen-based
// `ev.data` string view, `dataBytes()` survives an embedded NUL, so it's the
// correct reader for any framed binary protocol (protobuf, MessagePack, image
// or audio chunks, …).
//
// `.send()` is correspondingly overloaded: a `string` still sends a text
// frame (opcode 1), while an `ArrayBuffer` or a `Uint8Array` sends a binary
// frame (opcode 2). Here the server echoes text as text and binary as binary,
// round-tripping the raw bytes untouched.
//
// Drive it with a real client while it runs (comment out the setTimeout
// first — it self-closes after 300ms for `make examples`' unattended run):
//   (Node) node -e "const WS=require('ws'); const ws=new WS('ws://localhost:8087'); \
//     ws.on('open',()=>ws.send(Buffer.from([1,0,2,255]))); \
//     ws.on('message',(m,isBin)=>console.log('got',isBin,[...m]))"

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
    if (ev.isBinary) {
      const bytes = new Uint8Array(ev.dataBytes())
      console.log('binary frame: ' + bytes.length + ' bytes')
      socket.send(ev.dataBytes()) // echo the raw bytes back, unchanged
    } else {
      console.log('text frame: ' + ev.data)
      socket.send('echo: ' + ev.data)
    }
  }
})

setTimeout(() => {
  console.log('auto-closing after demo delay')
  server.close()
}, 300)

console.log('listening on :8087 (binary + text WebSocket echo)')
server.listen(8087)
console.log('server closed, exiting')
