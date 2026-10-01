// A WebSocket client — the global WebSocket, as Node's: `new WebSocket(url)`
// returns CONNECTING at once and connects on the event loop, so
// `.onopen`/`.onmessage`/`.onclose`/`.onerror` can be assigned right after
// construction. It can talk to a server in this same process, or another.
//
// Run websocket_server.ts in one terminal, then this file in another, to
// see a real round trip — comment out websocket_server.ts's own setTimeout
// first, since it self-closes after 300ms for `make examples`' own
// unattended run, too short a window to switch terminals:
//   make run FILE=examples/websocket/websocket_server.ts &
//   make run FILE=examples/websocket/websocket_client.ts
//
// Run on its own (as `make examples` does, unattended, with no server
// actually listening), the connection is refused — reported through
// onerror and then onclose (code 1006), never a thrown exception.

const ws = new WebSocket('ws://127.0.0.1:8083/')
console.log('readyState right after construction: ' + ws.readyState)

ws.onopen = () => {
  console.log('connected, sending a message')
  ws.send('ping')
}
ws.onmessage = (ev) => {
  console.log('received: ' + ev.data)
  ws.close()
}
ws.onclose = (ev) => {
  console.log('closed (code=' + ev.code + ', readyState=' + ws.readyState + ')')
  process.exit(0)
}
ws.onerror = () => {
  console.log('connection failed — is websocket_server.ts running on :8083?')
}

setTimeout(() => {
  console.log('exiting')
  process.exit(0)
}, 2000)
