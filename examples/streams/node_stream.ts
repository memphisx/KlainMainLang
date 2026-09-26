// Node's stream module: Readable/Writable/Transform with 'data'/'end'/
// 'finish' events, .pipe(), the stream/promises pipeline, and the bridges
// to web streams.
import { Readable, Writable, Transform } from 'stream';
import { pipeline, finished } from 'stream/promises';

// A pull-driven Readable: read() pushes through `this`, the stream.
let n = 0;
const numbers = new Readable({
  objectMode: true,
  read() {
    n = n + 1;
    if (n > 4) { this.push(null); } else { this.push(n); }
  }
});

const square = new Transform({
  objectMode: true,
  transform(v: number, _enc, cb) { cb(null, v * v); }
});

const seen: number[] = [];
const sink = new Writable({ objectMode: true, write(v: number, _enc, cb) { seen.push(v); cb(); } });

await pipeline(numbers, square, sink);
console.log("squares:", seen.join(" "));

// Flowing mode: attaching 'data' starts the flow.
const words = Readable.from(["kalimera", "kosme"]);
words.on("data", (w) => { console.log("word:", w); });
words.once("end", () => { console.log("all words delivered"); });
await finished(words);

// write(chunk[, encoding][, callback]) and end([chunk][, encoding][, callback]):
// each callback runs once its chunk is written, after the synchronous code.
const captured: string[] = [];
const log = new Writable({
  decodeStrings: false,
  write(line: string, _enc, cb) { captured.push(line); cb(); }
});
log.write("first", "utf8", () => { console.log("wrote first"); });
log.write("second", () => { console.log("wrote second"); });
log.end("last", () => { console.log("captured: " + captured.join(",")); });

// Bridging to and from web streams.
const web = Readable.toWeb(Readable.from(["bridge"]));
for await (const s of web) { console.log("via web stream:", s); }
