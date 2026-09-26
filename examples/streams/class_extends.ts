// Node streams as classes: `class X extends Readable` with a `_read()`
// override calling `this.push`, and Writable, Duplex and Transform subclasses
// overriding `_write` / `_transform` — Node's own subclassing model.
import { Readable, Writable, Duplex, Transform } from 'stream';
import type { TransformCallback } from 'stream';

// A pull-driven Readable subclass: the stream calls _read() when it wants
// more data; object mode keeps each pushed string a chunk of its own.
class Greeter extends Readable {
  words: string[] = ["kalimera", "kosme", "apo", "thessaloniki"];
  i: number = 0;
  constructor() {
    super({ objectMode: true, highWaterMark: 8 });
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

// A Writable subclass: each written string arrives as a Buffer.
class Collector extends Writable {
  items: string[] = [];
  _write(chunk: Buffer, _enc: BufferEncoding, cb: (error?: Error | null) => void) {
    this.items.push(chunk.toString());
    cb();
  }
}

const sink = new Collector();
sink.on("finish", () => { console.log("collected:", sink.items.join(",")); });
sink.write("alpha");
sink.write("beta");
sink.end();

// A Duplex subclass: two independent sides on one instance.
class Echo extends Duplex {
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
  _write(chunk: Buffer, _enc: BufferEncoding, cb: (error?: Error | null) => void) {
    this.received.push(chunk.toString());
    cb();
  }
}

const echo = new Echo();
const echoed: string[] = [];
echo.on("data", (c) => { echoed.push(c.toString()); });
echo.once("end", () => { console.log("echo read:", echoed.join(" ")); });
echo.on("finish", () => { console.log("echo wrote:", echo.received.join(",")); });
echo.write("alpha");
echo.write("beta");
echo.end();

// A Transform subclass: _transform rewrites each chunk onto the readable side.
class Upper extends Transform {
  constructor() { super({ objectMode: true }); }
  _transform(chunk: string, _enc: BufferEncoding, cb: TransformCallback) {
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
