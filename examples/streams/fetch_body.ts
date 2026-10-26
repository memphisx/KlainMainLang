// fetch Response.body as a ReadableStream<Uint8Array>: the response resolves
// when headers arrive, and the body streams in chunk by chunk as the local
// fixture server (tools/httpbin-lite, started by `make examples`) flushes it.

// The httpbin-lite fixture `make examples` starts on a free port; run by
// hand, the public httpbin.org it stands in for.
const HTTPBIN = process.env.HTTPBIN_URL ?? 'https://httpbin.org'

const res = await fetch(HTTPBIN + "/chunked");
console.log("status:", res.status);

const decoder = new TextDecoder();
let text = "";
let chunks = 0;
for await (const chunk of res.body!) {
  chunks = chunks + 1;
  text = text + decoder.decode(chunk);
}
console.log("streamed in multiple chunks:", chunks >= 2);
console.log("body:", text);

// Buffered accessors return a real Promise<T> — await drives the transfer.
const whole = await fetch(HTTPBIN + "/get");
console.log("text() length > 0:", (await whole.text()).length > 0);
