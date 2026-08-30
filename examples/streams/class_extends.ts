// Node streams as real classes (TDD-00132): the idiomatic `class X extends
// Readable` shape, with a `this`-based `_read()` override calling `this.push`,
// and a `class Y extends Writable` with a `_write(chunk)` override — matching
// Node's own subclassing model rather than the options-object form. String
// chunks are used throughout so it runs the same way under Node.js (Node's
// default, non-objectMode streams carry string/Buffer chunks).
import { Readable, Writable, Duplex, Transform } from 'stream';

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

// A Duplex subclass: one instance with two independent sides — a pull-driven
// readable (_read/this.push) and a writable sink (_write). `<T>` names both the
// readable-out and writable-in chunk type.
class Echo extends Duplex<string> {
  queue: string[] = ["one", "two", "three"];
  i: number = 0;
  received: string[] = [];
  _read() {
    if (this.i >= this.queue.length) {
      this.push(null);
    } else {
      this.push(this.queue[this.i]);
      this.i = this.i + 1;
    }
  }
  _write(chunk: string, enc: string, cb: () => void) {
    this.received.push(chunk);
    cb();
  }
}

const echo = new Echo();
const echoed: string[] = [];
echo.on("data", (c) => { echoed.push(c); });
echo.once("end", () => { console.log("echo read:", echoed.join(" ")); });
echo.on("finish", () => { console.log("echo wrote:", echo.received.join(",")); });
echo.write("alpha");
echo.write("beta");
echo.end();

// A Transform subclass: _transform(chunk, enc, cb) rewrites each written chunk
// and pushes the result to the readable side via `this.push`. objectMode keeps
// the chunks as strings under Node (its default non-objectMode chunk is a
// Buffer); this compiler's chunks are already typed by `<T>`.
class Upper extends Transform<string> {
  constructor() { super({ objectMode: true }); }
  _transform(chunk: string, enc: string, cb: () => void) {
    this.push(chunk.toUpperCase());
    cb();
  }
}

const up = new Upper();
const upped: string[] = [];
up.on("data", (c) => { upped.push(c); });
up.once("end", () => { console.log("upper:", upped.join(" ")); });
up.write("kalimera");
up.write("thessaloniki");
up.end();
