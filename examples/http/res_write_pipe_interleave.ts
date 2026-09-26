// TDD-00195 Stage 2 (ADR-00964): a raw res.write and req.pipe(res) on ONE
// response share a single per-response output queue, so their bytes leave the
// socket in call order. The handler writes a prefix directly with res.write, then
// pipes the request body straight back; the client sees the prefix before the
// echoed body. (Both paths used to have independent output paths that could
// interleave out of order.)
import http from 'http';
import type { AddressInfo } from 'net';

async function consume(port: number): Promise<void> {
  const res = await fetch("http://127.0.0.1:" + port + "/", {
    method: "POST",
    body: "the quick brown fox",
  });
  const text = await res.text();
  console.log("body:", text);
  console.log("ordered:", text === "PREFIX:the quick brown fox");
  process.exit(0);
}

const server = http.createServer((req: http.IncomingMessage, res: http.ServerResponse) => {
  res.writeHead(200, { "Content-Type": "application/octet-stream" });
  res.write("PREFIX:");
  req.pipe(res);
});

server.listen(0, () => {
  const port: number = (server.address() as AddressInfo).port;
  console.log("listening:", port > 0);
  setTimeout(() => { consume(port); }, 50);
});
