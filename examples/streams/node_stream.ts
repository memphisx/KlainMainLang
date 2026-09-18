// Node's stream module (TDD-00097 Stage 8): Readable/Writable/Transform over
// the same WHATWG internals, with 'data'/'end'/'error'/'finish' events,
// .pipe(), the stream/promises pipeline, and the web-stream bridges.
import { Readable, Writable, Transform } from 'stream';
import { pipeline, finished } from 'stream/promises';

// A pull-driven Readable: the read callback receives the stream itself
// (this compiler has no `this` binding in object-literal callbacks).
let n = 0;
const numbers = new Readable<number>({
  read: (self) => {
    n = n + 1;
    if (n > 4) { self.push(null); } else { self.push(n); }
  }
});

const square = new Transform<number, number>({
  transform: (v, out) => { out.enqueue(v * v); }
});

const seen: number[] = [];
const sink = new Writable<number>({ write: (v) => { seen.push(v); } });

await pipeline(numbers, square, sink);
console.log("squares:", seen.join(" "));

// Flowing mode: attaching 'data' starts the flow.
const words = Readable.from(["kalimera", "kosme"]);
words.on("data", (w) => { console.log("word:", w); });
words.once("end", () => { console.log("all words delivered"); });
await finished(words);

// An options-form sink can take Node's full write(chunk, encoding, callback)
// signature — the sink calls cb() when done (completion is on return here).
const captured: string[] = [];
const log = new Writable<string>({
  write: (line: string, enc: string, cb: () => void) => { captured.push(line); cb(); }
});
// write(chunk[, encoding][, callback]) and end([chunk][, encoding][, callback])
// on the instance: the 'utf8' encoding is accepted and the completion callback
// fires after the current synchronous run (Node's next-tick delivery), in order.
log.write("first", "utf8", () => { console.log("wrote first"); });
log.write("second", () => { console.log("wrote second"); });
log.end("last", () => { console.log("captured: " + captured.join(",")); });

// Bridging to and from WHATWG streams.
const web = Readable.from(["bridge"]).toWeb();
for await (const s of web) { console.log("via web stream:", s); }
