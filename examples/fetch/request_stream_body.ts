// A ReadableStream as a request body (with `duplex: "half"`, as undici
// requires): the Request keeps the stream, its Body methods read it, and
// fetch uploads its bytes — here to a local server on a free port.
import http from "node:http";

const encoder = new TextEncoder();
const lines = () =>
  new ReadableStream<Uint8Array>({
    start(c) {
      for (const city of ["Thessaloniki", "Kavala", "Volos"]) c.enqueue(encoder.encode(city + "\n"));
      c.close();
    },
  });

const req = new Request("http://localhost/", { method: "POST", body: lines(), duplex: "half" });
console.log("body is a stream:", req.body instanceof ReadableStream);
console.log("read back:", JSON.stringify(await req.text()));

const server = http.createServer((inc, res) => {
  let got = "";
  inc.on("data", (chunk) => (got += chunk));
  inc.on("end", () => res.end(`${inc.method} received ${got.split("\n").length - 1} lines`));
});
server.listen(0, async () => {
  const url = `http://127.0.0.1:${(server.address() as any).port}/upload`;
  const res = await fetch(url, { method: "PUT", body: lines(), duplex: "half" });
  console.log(await res.text());
  server.close();
});
