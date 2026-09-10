// server.listen(options) — Node's options-object form of listen.
// `{ port, host, backlog, exclusive }` is accepted in place of the positional
// (port, callback) arguments. `host` binds a specific interface (here loopback),
// which server.address().address reflects back via getsockname; `backlog` sets
// the listen(2) queue depth. A numeric IPv4 (or "localhost") is required —
// hostname resolution is not supported.
import http from 'http';

const server = http.createServer((req: IncomingMessage, res: ServerResponse) => {
  res.writeHead(200, { "Content-Type": "text/plain" });
  res.end("hello from " + req.path);
});

server.listen({ port: 0, host: "127.0.0.1", backlog: 128 }, () => {
  const a = server.address();
  console.log("bound host:", a.address);       // 127.0.0.1
  console.log("ephemeral port:", a.port > 0);  // true
  server.close();
});

console.log("server closed, main continues");
