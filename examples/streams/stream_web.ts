// stream/web — Node's module home for the WHATWG stream classes. The names
// re-export the same ambient constructors (`ReadableStream`, `WritableStream`,
// `TransformStream`, `CompressionStream`, `DecompressionStream`), so importing
// them is interchangeable with using the globals directly.
import { ReadableStream, TransformStream } from 'stream/web';

const source = new ReadableStream<number>({
  start: (c) => { c.enqueue(3); c.enqueue(4); c.close(); }
});
const doubler = new TransformStream<number, number>({
  transform: (v, c) => { c.enqueue(v * 2); }
});

const rd = source.pipeThrough(doubler).getReader();
let next = await rd.read();
while (!next.done) {
  console.log("out:", next.value);
  next = await rd.read();
}
