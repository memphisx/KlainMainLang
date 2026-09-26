// TDD-00195 Stage 2: a binary chunk (Uint8Array/Buffer) passed to res.write /
// res.end is framed as its raw bytes, not stringified — matching Node, where
// res.write(buf) sends the buffer's exact byte range. The handler streams two
// binary chunks (the first via res.write, the second via res.end), including an
// embedded NUL byte that a stringified path would truncate. The server fetches
// its own endpoint and reads the bytes back through Response.arrayBuffer().
import http from 'http';
import type { AddressInfo } from 'net';

async function consume(port: number): Promise<void> {
  const res = await fetch("http://127.0.0.1:" + port + "/");
  const buf = new Uint8Array(await res.arrayBuffer());
  let sum = 0;
  for (let i = 0; i < buf.length; i++) sum = sum + buf[i];
  console.log("len:", buf.length, "sum:", sum, "embeddedNul:", buf[1] === 0);
  process.exit(0);
}

const server = http.createServer((req: http.IncomingMessage, res: http.ServerResponse) => {
  res.writeHead(200, { "Content-Type": "application/octet-stream" });
  const a = new Uint8Array(3);
  a[0] = 5; a[1] = 0; a[2] = 200; // embedded NUL survives
  res.write(a);
  const b = new Uint8Array(2);
  b[0] = 1; b[1] = 255;
  res.end(b);
});

server.listen(0, () => {
  const port: number = (server.address() as AddressInfo).port;
  console.log("listening:", port > 0);
  setTimeout(() => { consume(port); }, 50);
});
