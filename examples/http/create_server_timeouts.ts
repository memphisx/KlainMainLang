// http.createServer connection timeouts (TDD-00217): a positive
// headersTimeout / requestTimeout / keepAliveTimeout is enforced by the reactor,
// closing a slow or idle connection at the deadline instead of letting it tie up
// a connection fiber forever — the standard slowloris-hardening configuration.
//
//   • headersTimeout   — connection → request headers fully received
//   • requestTimeout   — connection → whole message (headers + body) received
//   • keepAliveTimeout — idle gap between a finished response and the next request
//
// 0 (or an omitted option) disables that timeout, exactly as in Node. V1 closes a
// timed-out connection silently (no 408 body); the connection is still bounded.
import http from 'http';
import type { AddressInfo } from 'net';

const server = http.createServer(
  {
    headersTimeout: 5000,
    requestTimeout: 30000,
    keepAliveTimeout: 5000,
  },
  (req: http.IncomingMessage, res: http.ServerResponse) => {
    res.writeHead(200, { "Content-Type": "text/plain" });
    res.end("ok from " + req.url);
  },
);

server.listen(0, () => {
  const port: number = (server.address() as AddressInfo).port;
  console.log("listening with connection timeouts on port", port);
  setTimeout(() => { server.close(); }, 100);
});

console.log("timeouts configured, main continues");
