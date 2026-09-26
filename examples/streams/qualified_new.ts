// Qualified constructor form: `new stream.Readable(...)` through the default
// import — the shape most Node code uses — behaves exactly like the bare
// `new Readable(...)`. `read()` pushes through `this`, the stream.
import stream from 'stream';

let n = 0;
const numbers = new stream.Readable({
  objectMode: true,
  read() {
    n = n + 1;
    if (n > 3) { this.push(null); } else { this.push(n * 7); }
  }
});

numbers.on("data", (v) => { console.log("chunk:", v); });
numbers.on("end", () => { console.log("done"); });
