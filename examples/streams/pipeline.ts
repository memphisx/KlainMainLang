// A full WHATWG pipeline: source → transform → sink, with backpressure.
const source = new ReadableStream<string>({
  start: (c) => {
    c.enqueue("kalimera");
    c.enqueue("thessaloniki");
    c.close();
  }
});

const upper = new TransformStream<string, string>({
  transform: (chunk, controller) => { controller.enqueue(chunk.toUpperCase()); }
});

const printed: string[] = [];
const sink = new WritableStream<string>({
  write: (chunk) => { printed.push(chunk); }
});

await source.pipeThrough(upper).pipeTo(sink);
console.log(printed.join(" "));

// tee: one source, two independent consumers.
const [left, right] = ReadableStream.from([1, 2, 3]).tee();
let sum = 0;
for await (const n of left) { sum = sum + n; }
let product = 1;
for await (const n of right) { product = product * n; }
console.log("sum:", sum, "product:", product);
