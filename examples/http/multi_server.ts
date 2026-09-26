// Two HTTP servers in one program (TDD-00191 Stage 1). The first
// http.createServer is the primary; each additional one listens on its own port
// and serves its own handler concurrently on the shared event loop — an app port
// plus a separate health/metrics port, say. A self-closing timer keeps the
// example runnable to completion under `make examples`.
import http from 'http';
import type { AddressInfo } from 'net';

const app = http.createServer((req: http.IncomingMessage, res: http.ServerResponse) => {
  res.writeHead(200, { "Content-Type": "text/plain" });
  res.end("app: " + req.url);
});

const health = http.createServer((req: http.IncomingMessage, res: http.ServerResponse) => {
  res.writeHead(200, { "Content-Type": "application/json" });
  res.end('{"status":"ok"}');
});

app.listen(0, () => {
  health.listen(0, () => {
    console.log("app on port", (app.address() as AddressInfo).port > 0);
    console.log("health on port", (health.address() as AddressInfo).port > 0);
    // Both servers are live and independent; shut down for the demo.
    app.close();
    health.close();
  });
});

console.log("two servers listening");
