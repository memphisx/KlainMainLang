// A standalone WebSocket client (TDD-00039 Stage 3, `ws://` only):
// `new WebSocket(url)` performs its TCP connect + HTTP upgrade handshake
// *synchronously* (a documented V1 simplification — see WebSocketClientType
// in codegen/llvm/types.go), so `.onopen`/`.onmessage`/`.onclose`/`.onerror`
// only ever fire once the event loop gets a chance to run, which is why
// they're deferred to the very first loop iteration after construction —
// giving this script time to assign them before anything fires. One real
// consequence of "synchronous": a client can never connect to a server
// running in this *same* process/event loop (the blocking connect would
// need that very loop, which it's blocking, to ever accept and answer) —
// always a separate process, exactly like `websocket_server.ts` in this
// same directory.
//
// Run websocket_server.ts in one terminal, then this file in another, to
// see a real round trip — comment out websocket_server.ts's own setTimeout
// first, since it self-closes after 300ms for `make examples`' own
// unattended run, too short a window to switch terminals:
//   make run FILE=examples/websocket/websocket_server.ts &
//   make run FILE=examples/websocket/websocket_client.ts
//
// Run on its own (as `make examples` does, unattended, with no server
// actually listening), the connection is refused — a real, honestly
// reported outcome via onerror/onclose (never a thrown exception; real
// WebSocket never throws synchronously for a network-level failure
// either), not a hang or a crash.

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
ws.onclose = () => {
  console.log('closed (readyState=' + ws.readyState + ')')
  process.exit(0)
}
ws.onerror = () => {
  console.log('connection failed — is websocket_server.ts running on :8083?')
}

setTimeout(() => {
  console.log('exiting')
  process.exit(0)
}, 2000)
