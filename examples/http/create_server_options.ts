// http.createServer(options, listener) — the Node options-object form.
// Every option below is accepted because its value states this dispatcher's
// fixed behavior: TCP_NODELAY is already on (noDelay: true), accepted sockets
// carry no SO_KEEPALIVE probes (keepAlive: false), and no request/headers
// timeout or per-socket request cap is enforced (the timeout options at 0,
// Node's "disabled" value). Accepting them is a portable no-op — a Node app
// that disables these compiles and behaves identically. A value that would
// change behavior (e.g. noDelay: false, requestTimeout: 30000) is a clean
// compile-time rejection rather than a silent ignore.
//
// highWaterMark is the exception: it genuinely changes behavior (the res.write
// backpressure threshold), so any positive value is accepted and threaded into
// every response this server mints (default 16384).
import http from 'http';

const server = http.createServer(
  {
    highWaterMark: 8192,
    noDelay: true,
    keepAlive: false,
    requestTimeout: 0,
    headersTimeout: 0,
    maxRequestsPerSocket: 0,
    insecureHTTPParser: false,
    requireHostHeader: false,
  },
  (req: IncomingMessage, res: ServerResponse) => {
    res.writeHead(200, { "Content-Type": "text/plain" });
    res.end("ok from " + req.path);
  },
);

server.listen(0, () => {
  const port: number = server.address().port;
  console.log("listening on port", port);
  setTimeout(() => { server.close(); }, 100);
});

console.log("server closed, main continues");
