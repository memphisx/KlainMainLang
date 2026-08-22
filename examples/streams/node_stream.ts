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

// Bridging to and from WHATWG streams.
const web = Readable.from(["bridge"]).toWeb();
for await (const s of web) { console.log("via web stream:", s); }
