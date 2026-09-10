// reader.read() returns a real `{ value: undefined, done: true }` record once
// the stream is closed — not the chunk type's zero value — and a controller's
// desiredSize is `null` once the stream has errored (vs `0` once merely
// closed). These are the last `T | undefined` adopters (TDD-00196, the residue
// of TDD-00187's sentinel work), reusing the shared presence-flagged optional.

const rs = new ReadableStream<number>({
  start: (c) => {
    c.enqueue(1);
    c.enqueue(2);
    c.close();
  },
});

const reader = rs.getReader();

// Drain via destructuring. On the closed read, `value` is a real `undefined`.
while (true) {
  const { value, done } = await reader.read();
  if (done) {
    console.log("done — value is undefined:", value === undefined);
    break;
  }
  // `value` is `number | undefined` here; `done === false` guarantees it is
  // present, so assert with `!` (or narrow with `if (value !== undefined)`).
  console.log("chunk:", value! * 10);
}

// desiredSize is `0` once closed, but `null` once errored.
new ReadableStream<number>({
  start: (c) => {
    c.close();
    console.log("desiredSize once closed:", c.desiredSize);
  },
});
new ReadableStream<number>({
  start: (c) => {
    c.error(new Error("stop"));
    console.log("desiredSize once errored:", c.desiredSize);
  },
});
