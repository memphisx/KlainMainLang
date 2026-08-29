// Node http client + server, self-contained (TDD-00138): a server responds, and
// http.get fetches from it — in the *same* process, on the single event loop.
// The client registers a completion reaction the loop fires once the transfer
// is done, so it never blocks the server. Runs the same way under Node.js.
import http from 'http';

http.createServer((req: IncomingMessage, res: ServerResponse) => {
  res.writeHead(200, { "Content-Type": "text/plain" });
  res.end("kalimera from the server");
}).listen(18521, () => {
  http.get("http://127.0.0.1:18521/", (res) => {
    let body = "";
    res.on('data', (chunk: string) => { body = body + chunk; });
    res.on('end', () => {
      console.log("status:", res.statusCode);
      console.log("body:", body);
      // Options-object form with method + headers: the request rides the
      // same client; GET stays the default when method is omitted.
      const req = http.request({ port: 18521, path: "/", method: "HEAD", headers: { "X-Client": "kml" } }, (res2) => {
        console.log("head status:", res2.statusCode);
        process.exit(0);
      });
      req.end();
    });
  });
});
