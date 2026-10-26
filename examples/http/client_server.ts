// Node http client + server, self-contained (TDD-00138): a server responds, and
// http.get fetches from it — in the *same* process, on the single event loop.
// The client registers a completion reaction the loop fires once the transfer
// is done, so it never blocks the server. Runs the same way under Node.js.
import http from 'http';

const server = http.createServer((req: http.IncomingMessage, res: http.ServerResponse) => {
  res.writeHead(200, { "Content-Type": "text/plain" });
  res.end("kalimera from the server");
});
server.listen(0, () => {
  const port = (server.address() as any).port; // bound port: listen(0) picks a free one
  http.get(`http://127.0.0.1:${port}/`, (res) => {
    let body = "";
    res.on('data', (chunk: string) => { body = body + chunk; });
    res.on('end', () => {
      console.log("status:", res.statusCode);
      console.log("body:", body);
      // Options-object form with method + headers: the request rides the
      // same client; GET stays the default when method is omitted.
      const req = http.request({ port, path: "/", method: "HEAD", headers: { "X-Client": "kml" } }, (res2) => {
        console.log("head status:", res2.statusCode);
        process.exit(0);
      });
      req.end();
    });
  });
});
