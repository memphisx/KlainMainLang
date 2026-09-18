// TDD-00215: the HTTP server 'clientError' event. It fires when a client
// connection produces a protocol error before it could be dispatched to a
// request handler — here a request truncated by the peer closing mid-parse.
// Without a listener the server sends Node's default (a 431 for an oversized
// header block, or a silent close for a reset); with one, the listener receives
// the Error (err.code) and a net.Socket it can write()/end()/destroy() to answer
// the client itself (e.g. a custom 400 on the header-overflow path, where the
// peer is still connected).
import http from 'http';
import net from 'net';

const server = http.createServer((req: IncomingMessage, res: ServerResponse) => {
  res.writeHead(200);
  res.end("ok");
});

server.on('clientError', (err: Error, socket) => {
  // err.code is 'ECONNRESET' for a truncated request, 'HPE_HEADER_OVERFLOW' for
  // an oversized header block — a small honest subset of Node's HPE_* set.
  console.log("clientError:", err.code);
  server.close();
});

// listen(0) lets the OS pick a free port; address().port reads it back.
server.listen(0, () => {
  const port = server.address().port;
  const client = net.connect({ port: port, host: "127.0.0.1" }, () => {
    // A truncated request: a partial request with no blank-line terminator,
    // then end() — the peer closes mid-parse, so the server never sees a
    // complete request and raises 'clientError'.
    client.write("GET / HTTP/1.1\r\nHost: x");
    client.end();
  });
});
