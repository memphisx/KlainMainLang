// TDD-00195 Stage 2: `req.pipe(res)` — a server `req` is a Node Readable and a
// `res` is a Node Writable, so piping one into the other echoes the request body
// straight back to the client with no explicit chunk handling. The request body
// streams in and the response streams out, chunk for chunk.
import http from 'http';
import type { AddressInfo } from 'net';

async function send(port: number): Promise<void> {
  const res = await fetch("http://127.0.0.1:" + port + "/", {
    method: "POST",
    body: "ping-pong payload",
  });
  const text = await res.text();
  console.log("echoed:", text);
  process.exit(0);
}

const server = http.createServer((req: http.IncomingMessage, res: http.ServerResponse) => {
  res.writeHead(200, { "Content-Type": "application/octet-stream" });
  req.pipe(res);
});

server.listen(0, () => {
  const port: number = (server.address() as AddressInfo).port;
  console.log("listening:", port > 0);
  setTimeout(() => { send(port); }, 50);
});
