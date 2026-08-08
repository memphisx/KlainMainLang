// A minimal WebSocket echo server (TDD-00039 Stages 1-2): http.listen's
// third argument gains an optional `ws` handler, called once per
// successfully upgraded connection with a WSConnection —
// `.onmessage`/`.send()`/`.close()`. A client `ping` gets an automatic
// `pong` reply (identical payload, RFC 6455 §5.5.2) with no code needed
// here at all, and a client `close` frame — or this server calling
// `.close()` itself, as it does below for the "bye" message — gets echoed
// back with a proper Close frame before the connection actually ends
// (RFC 6455 §5.5.1), instead of Stage 1's silent drop. The same listener
// still serves ordinary HTTP requests exactly as before — an upgrade
// request is detected and diverted before it ever reaches the normal
// handler below.
//
// Point a real WebSocket client at it while it's running to see it echo —
// websocket_client.ts in this same directory is one (comment out the
// setTimeout below first: it self-closes after 300ms for `make examples`'
// own unattended run, too short a window to switch terminals and connect
// manually), or:
//   (Node) node -e "const ws=new (require('ws'))('ws://localhost:8083'); \
//     ws.on('open',()=>ws.send('hello')); \
//     ws.on('message',m=>console.log('got:',m.toString()))"
//   curl "http://localhost:8083/hello"   # the listener still serves plain HTTP too

interface Res {
  status: number
  body: string
}

setTimeout(() => {
  console.log('auto-closing after demo delay')
  http.close()
}, 300)

console.log('listening on :8083 (HTTP + WebSocket upgrade)')

http.listen(8083, (req: Request): Res => {
  return { status: 200, body: 'plain HTTP: ' + req.method + ' ' + req.path }
}, {
  ws: (socket: WSConnection) => {
    console.log('client connected')
    socket.onmessage = (ev) => {
      console.log('received: ' + ev.data)
      if (ev.data === 'bye') {
        socket.close()
      } else {
        socket.send('echo: ' + ev.data)
      }
    }
  }
})

console.log('server closed, exiting')
