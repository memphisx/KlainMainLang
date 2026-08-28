// Node streams as real classes (TDD-00132): the idiomatic `class X extends
// Readable` shape, with a `this`-based `_read()` override calling `this.push`,
// and a `class Y extends Writable` with a `_write(chunk)` override — matching
// Node's own subclassing model rather than the options-object form. String
// chunks are used throughout so it runs the same way under Node.js (Node's
// default, non-objectMode streams carry string/Buffer chunks).
import { Readable, Writable } from 'stream';

// A pull-driven Readable subclass: the runtime calls _read() when it wants
// more data, and `this` is the stream, so `this.push(...)` works directly.
class Greeter extends Readable<string> {
  words: string[] = ["kalimera", "kosme", "apo", "thessaloniki"];
  i: number = 0;
  constructor() {
    // super(options) threads the stream options into the base — here a larger
    // queue watermark. objectMode is accepted too (no effect on typed chunks).
    super({ highWaterMark: 8 });
  }
  _read() {
    if (this.i >= this.words.length) {
      this.push(null);
    } else {
      this.push(this.words[this.i]);
      this.i = this.i + 1;
    }
  }
}

const seen: string[] = [];
const greeter = new Greeter();
greeter.on("data", (w) => { seen.push(w); });
greeter.once("end", () => { console.log("read:", seen.join(" ")); });

// A Writable subclass: _write(chunk) receives each written chunk via `this`.
class Collector extends Writable<string> {
  items: string[] = [];
  _write(chunk: string, enc: string, cb: () => void) {
    this.items.push(chunk);
    cb();
  }
}

const sink = new Collector();
sink.on("finish", () => { console.log("collected:", sink.items.join(",")); });
sink.write("alpha");
sink.write("beta");
sink.end();
