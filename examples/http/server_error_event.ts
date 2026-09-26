// TDD-00215 Stage 2: the HTTP server 'error' event. It fires on a server-level
// failure — in practice a bind failure such as EADDRINUSE (the port is already
// in use) or EACCES (a privileged port without permission). Without a listener
// the failure surfaces as an uncaught, process-aborting error (Node parity);
// with one, the listener receives the Error and the process keeps running.
//
// Here one server binds a port successfully, then a second server tries to bind
// the same port and its 'error' listener catches the EADDRINUSE. The listener
// runs synchronously at listen() time.
import http from 'http';

const first = http.createServer((req: http.IncomingMessage, res: http.ServerResponse) => {
  res.end("first");
});
first.listen(8422);

const second = http.createServer((req: http.IncomingMessage, res: http.ServerResponse) => {
  res.end("second");
});

second.on('error', (err: Error) => {
  console.log("server error:", err.code);
  console.log("message:", err.message);
  process.exit(0);
});

second.listen(8422);
console.log("unreachable — the bind failure fired the error listener");
