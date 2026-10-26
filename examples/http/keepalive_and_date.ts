// http.createServer response fidelity (ADR-00691): every response is stamped
// with an RFC 7231 `Date` header, and HTTP/1.1 connections are persistent by
// default. A `curl -D - http://127.0.0.1:<port>/` against this server shows:
//
//   HTTP/1.1 200 OK
//   Date: Thu, 04 Sep 2026 23:19:21 GMT
//   Content-Length: 2
//   Connection: keep-alive
//   Keep-Alive: timeout=5
//
// and `curl -v` two-URL run reports "Re-using existing connection" — the socket
// is reused for the second request. A client that sends `Connection: close`
// (or a handler that sets it) gets `Connection: close` and the socket closes
// after one exchange.
//
// This example serves one in-process request to prove the server is up, then
// exits (a real server omits the self-request and the shutdown timer).
import http from 'http';

const server = http.createServer((req: http.IncomingMessage, res: http.ServerResponse) => {
  res.end("ok");
});
server.listen(0, () => {
  const port = (server.address() as any).port; // bound port: listen(0) picks a free one
  http.get(`http://127.0.0.1:${port}/`, (res) => {
    let body = "";
    res.on('data', (chunk: string) => { body = body + chunk; });
    res.on('end', () => {
      console.log("status:", res.statusCode);
      console.log("body:", body);
      process.exit(0);
    });
  });
});
