// TDD-00214 (Layer B, observable backpressure): a Node-faithful streaming
// producer. res.write() returns FALSE once the unsent, queued bytes cross the
// highWaterMark; a well-behaved producer stops and waits for the 'drain' event
// before writing more. Here `pump` writes 64 KiB chunks until res.write reports
// backpressure, then re-arms itself with res.once('drain', pump); the reactor
// drains the per-response output queue to the socket and fires 'drain'
// asynchronously to resume it. The full 8 MiB body comes back intact across all
// the false/'drain' cycles — proving the producer never stalls.
import http from 'http';

const CHUNK = "0123456789ABCDEF".repeat(4096); // 64 KiB
const TOTAL = 128; // 8 MiB

async function consume(port: number): Promise<void> {
  const res = await fetch("http://127.0.0.1:" + port + "/");
  let total = 0;
  for await (const piece of res.body) {
    total = total + piece.length;
  }
  console.log("streamed-bytes:", total);
  console.log("intact:", total === 8388608); // 64 KiB * 128
  process.exit(0);
}

const server = http.createServer((req: IncomingMessage, res: ServerResponse) => {
  res.writeHead(200, { "Content-Type": "application/octet-stream" });
  let n = 0;
  const pump = () => {
    let ok = true;
    while (n < TOTAL && ok) {
      ok = res.write(CHUNK);
      n = n + 1;
    }
    if (n >= TOTAL) {
      res.end();
      return;
    }
    res.once('drain', pump); // resume only once the buffer drains
  };
  pump();
});

server.listen(0, () => {
  const port: number = server.address().port;
  console.log("listening:", port > 0);
  setTimeout(() => { consume(port); }, 50);
});
