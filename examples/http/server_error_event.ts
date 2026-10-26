// TDD-00215 Stage 2: the HTTP server 'error' event. It fires on a server-level
// failure — in practice a bind failure such as EADDRINUSE (the port is already
// in use) or EACCES (a privileged port without permission). Without a listener
// the failure surfaces as an uncaught, process-aborting error (Node parity);
// with one, the listener receives the Error and the process keeps running.
//
// Here one server binds a port successfully, then a second server tries to bind
// the first one's actual port (listen(0) picked it) and its 'error' listener
// catches the EADDRINUSE, on the event loop after the failed listen().
import http from 'http';

const first = http.createServer((req: http.IncomingMessage, res: http.ServerResponse) => {
  res.end("first");
});

const second = http.createServer((req: http.IncomingMessage, res: http.ServerResponse) => {
  res.end("second");
});

second.on('error', (err: NodeJS.ErrnoException) => {
  console.log("server error:", err.code);
  console.log("message:", err.message);
  process.exit(0);
});

first.listen(0, () => {
  second.listen((first.address() as any).port);
});
