// WHATWG ReadableStream: pull-based source with backpressure (HWM 1),
// consumed chunk-at-a-time with for await...of.
let n = 0;
const numbers = new ReadableStream<number>({
  pull: (controller) => {
    n = n + 1;
    if (n > 5) {
      controller.close();
    } else {
      controller.enqueue(n * n);
    }
  }
}, { highWaterMark: 1 });

for await (const square of numbers) {
  console.log("square:", square);
}

// A byte stream of Uint8Array chunks with a byte-length queuing strategy.
const bytes = new ReadableStream<Uint8Array>({
  start: (c) => {
    const hel = new Uint8Array([104, 101, 108]);
    const lo = new Uint8Array([108, 111]);
    c.enqueue(hel);
    c.enqueue(lo);
    c.close();
  }
}, new ByteLengthQueuingStrategy({ highWaterMark: 16 }));

const decoder = new TextDecoder();
let text = "";
for await (const chunk of bytes) {
  text = text + decoder.decode(chunk);
}
console.log("decoded:", text);

// Reader-level access: explicit read() with {value, done} records.
const reader = ReadableStream.from(["Thessaloniki", "streams"]).getReader();
const first = await reader.read();
console.log(first.value, first.done);
const second = await reader.read();
console.log(second.value, second.done);
const end = await reader.read();
console.log("done:", end.done);
