// fetch Response.body as a ReadableStream<Uint8Array>: the response resolves
// when headers arrive, and the body streams in chunk by chunk as the local
// fixture server (tools/httpbin-lite, started by `make examples`) flushes it.
const res = await fetch("http://127.0.0.1:8765/chunked");
console.log("status:", res.status);

const decoder = new TextDecoder();
let text = "";
let chunks = 0;
for await (const chunk of res.body) {
  chunks = chunks + 1;
  text = text + decoder.decode(chunk);
}
console.log("streamed in multiple chunks:", chunks >= 2);
console.log("body:", text);

// Buffered accessors still work — they drive the transfer to completion.
const whole = await fetch("http://127.0.0.1:8765/get");
console.log("text() length > 0:", whole.text().length > 0);
