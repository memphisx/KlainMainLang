// A plain primary HTTP server plus a WebSocket server on a SECOND port
// (TDD-00191 Stage 3 / ADR-00805). The WebSocket server is not the first
// http.createServer, yet its klain:ws connection handler routes to its own
// per-server handler global — WebSocket/upgrade handling works on any server,
// not just the primary. A common shape: a plain REST/health server on one port
// and a realtime WebSocket endpoint on another, in one process.
//
// A self-closing sequence keeps the example runnable to completion.
import http from 'http';
import { WebSocketServer } from 'klain:ws';

const api = http.createServer((req: IncomingMessage, res: ServerResponse) => {
  res.writeHead(200, { "Content-Type": "application/json" });
  res.end('{"status":"ok"}');
});

const realtime = http.createServer((req: IncomingMessage, res: ServerResponse) => {
  res.writeHead(426); res.end("upgrade required");
});
const wss = new WebSocketServer({ server: realtime });
wss.on('connection', (socket: WSConnection) => {
  socket.onmessage = (ev) => { socket.send("echo: " + ev.data); };
});

api.listen(0, () => {
  realtime.listen(0, () => {
    console.log("api (plain) on port", api.address().port > 0);
    console.log("realtime (websocket) on port", realtime.address().port > 0);
    api.close();
    realtime.close();
  });
});

console.log("plain api + websocket server on a second port");
