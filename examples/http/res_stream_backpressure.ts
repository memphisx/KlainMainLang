// TDD-00195 Stage 2 (Layer A backpressure): a streamed `res` uses non-blocking
// writes. When res.write fills the socket's send buffer it hits EAGAIN and the
// handler's fiber PARKS on socket writability — the reactor keeps running and
// resumes the fiber when the client drains, instead of a synchronous write()
// blocking the whole server on one slow consumer. Here the handler streams
// ~2 MB in 1 KB chunks; the client consumes it through fetch's streaming body
// and the full byte count comes back intact across all the park/resume cycles.
import http from 'http';

const CHUNK = "x".repeat(1024);

async function consume(port: number): Promise<void> {
  const res = await fetch("http://127.0.0.1:" + port + "/");
  let total = 0;
  for await (const piece of res.body) {
    total = total + piece.length;
  }
  console.log("streamed-bytes:", total);
  process.exit(0);
}

const server = http.createServer((req: IncomingMessage, res: ServerResponse) => {
  res.writeHead(200, { "Content-Type": "application/octet-stream" });
  let i = 0;
  while (i < 2048) { // ~2 MB, well past the socket send buffer
    res.write(CHUNK);
    i = i + 1;
  }
  res.end();
});

server.listen(0, () => {
  const port: number = server.address().port;
  console.log("listening:", port > 0);
  setTimeout(() => { consume(port); }, 50);
});
