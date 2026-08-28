// Qualified constructor form: `new stream.Readable(...)` through a namespace
// import — the shape most Node code uses — behaves exactly like the bare
// `new Readable(...)`.
import stream from 'stream';

let n = 0;
const numbers = new stream.Readable<number>({
  read: (self) => {
    n = n + 1;
    if (n > 3) { self.push(null); } else { self.push(n * 7); }
  }
});

numbers.on("data", (v) => { console.log("chunk:", v); });
numbers.on("end", () => { console.log("done"); });
