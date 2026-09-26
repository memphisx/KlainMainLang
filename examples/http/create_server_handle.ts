// Variable-bound http.createServer handle — the standard Node idiom.
// The server binds an ephemeral port (listen(0)); (server.address() as AddressInfo).port
// reports the real one; server.close() from inside the event loop lets
// control continue past the blocking listen call.
import http from 'http';
import type { AddressInfo } from 'net';

const server = http.createServer((req: http.IncomingMessage, res: http.ServerResponse) => {
  res.writeHead(200, { "Content-Type": "text/plain" });
  res.end("hello from " + req.url);
});

// 'listening' can also be registered as an event before listen() —
// it fires right after the bind, with address() already usable.
server.on('listening', () => {
  console.log("listening event fired:", (server.address() as AddressInfo).port > 0);
});

server.listen(0, () => {
  const port: number = (server.address() as AddressInfo).port;
  console.log("listening on port", port);
  // Shut down shortly after: this example exercises the handle lifecycle,
  // not a long-running service.
  setTimeout(() => { server.close(); }, 100);
});

console.log("server closed, main continues");
