// Streaming http request bodies (TDD-00097 Stage 5b): the handler receives
// its request at headers-complete and consumes the body chunk-at-a-time via
// req.stream() — bodies larger than the buffered path's 10 MiB cap flow
// through without ever being held in memory whole. The in-process client
// uploads 12 MiB through fetch to prove it end to end.
import http from 'http';

async function tally(req: HttpRequest): Promise<string> {
  let total = 0;
  let chunks = 0;
  for await (const chunk of req.stream()) {
    total = total + chunk.length;
    chunks = chunks + 1;
  }
  return total + " bytes in " + (chunks >= 2 ? "many chunks" : "one chunk");
}

async function upload(): Promise<void> {
  let payload = "";
  for (let i = 0; i < 20; i = i + 1) {
    payload = payload + payload + "abcdefgh";
  }
  const res = await fetch("http://127.0.0.1:8089/", { method: "POST", body: payload });
  console.log("server counted:", await res.text());
  process.exit(0);
}
setTimeout(() => { upload(); }, 50);

http.listen(8089, async (req: HttpRequest) => {
  return { status: 200, body: await tally(req) };
});
