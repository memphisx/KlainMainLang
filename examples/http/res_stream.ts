// TDD-00195 Stage 2: an incremental `res` Writable. The handler calls
// res.write() several times mid-execution — each chunk is framed straight to
// the socket with chunked transfer-encoding, rather than buffered until the
// handler returns — then res.end() closes the response. The server fetches its
// own endpoint and consumes the chunks incrementally through fetch's streaming
// Response.body, proving the response streams rather than arriving whole.
import http from 'http';
import type { AddressInfo } from 'net';

async function consume(port: number): Promise<void> {
  const res = await fetch("http://127.0.0.1:" + port + "/");
  const decoder = new TextDecoder();
  let text = "";
  for await (const piece of res.body) {
    text = text + decoder.decode(piece);
  }
  console.log("streamed:", text);
  process.exit(0);
}

const server = http.createServer((req: http.IncomingMessage, res: http.ServerResponse) => {
  res.writeHead(200, { "Content-Type": "text/plain" });
  res.write("alpha ");
  res.write("beta ");
  res.write("gamma");
  res.end();
});

server.listen(0, () => {
  const port: number = (server.address() as AddressInfo).port;
  console.log("listening:", port > 0);
  setTimeout(() => { consume(port); }, 50);
});
