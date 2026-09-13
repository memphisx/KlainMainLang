// Promise rejection reasons carry the real thrown value — a string, a number, an
// object, or an Error — exactly as Node does (TDD-00207). A rejection is the
// async twin of a `throw`: the reason rides the same channel as `try/catch`.

// A string reason arrives verbatim at the reject handler.
Promise.reject("bad input").then(
  () => {},
  (e) => console.log("string reason:", e),
);

// A numeric reason (which used to lose its value entirely).
Promise.reject(404).catch((e) => console.log("number reason:", e));

// A real Error keeps its message and `instanceof Error`.
Promise.reject(new Error("disk full")).catch((e: Error) =>
  console.log("error reason:", e.message, "| isError:", e instanceof Error),
);

// The `new Promise` executor's reject() carries arbitrary values too.
new Promise<number>((_, reject) => reject(-1)).then(
  (v) => console.log("resolved", v),
  (e) => console.log("executor reason:", e),
);

// A rejection with no onRejected propagates to the next .catch untouched.
Promise.reject("propagated")
  .then((v) => v)
  .catch((e) => console.log("after propagation:", e));

// An unannotated async function still returns a Promise, so .then/.catch/await
// all work on its result.
async function loadUser() {
  throw new Error("not found");
}
loadUser().catch((e: Error) => console.log("async throw:", e.message));

// await re-throws a rejection as the real value into a surrounding try/catch.
async function main() {
  try {
    await Promise.reject("timeout");
  } catch (e) {
    console.log("awaited rejection:", e, "| typeof:", typeof e);
  }
}
main();
