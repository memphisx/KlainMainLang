// A chunked-transfer-encoded http response streamed from a ReadableStream
// body (TDD-00097 Stage 5): the connection stays open while the pull-driven
// source produces chunks; the server then fetches its own endpoint and
// consumes the chunks incrementally through fetch's streaming Response.body.
import http from 'http';

async function consume(): Promise<void> {
  const res = await fetch("http://127.0.0.1:8087/");
  const decoder = new TextDecoder();
  let text = "";
  for await (const piece of res.body) {
    text = text + decoder.decode(piece);
  }
  console.log("streamed:", text);
  process.exit(0);
}
// http.listen enters the event loop and never returns — schedule the
// client from a timer that fires inside the loop.
setTimeout(() => { consume(); }, 50);

http.listen(8087, (req: HttpRequest) => {
  let n = 0;
  const body = new ReadableStream<string>({
    pull: async (c) => {
      n = n + 1;
      if (n > 3) { c.close(); return; }
      await new Promise<void>((r) => setTimeout(() => r(), 10));
      c.enqueue("chunk" + n + " ");
    }
  });
  return { status: 200, body: body };
});


