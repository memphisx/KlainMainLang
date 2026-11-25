// Array.fromAsync: an array from an async iterable, an iterable of
// promises, or an array-like, each value awaited and optionally mapped.
async function* readings(): AsyncGenerator<number> {
  yield 21;
  yield await Promise.resolve(23);
}

async function main(): Promise<void> {
  console.log(await Array.fromAsync(readings()));
  console.log(await Array.fromAsync([Promise.resolve(3), 4], (x, i) => x * 2 + i));
  console.log(await Array.fromAsync(new Set(["a", "b"]), async (s) => s.toUpperCase()));
  try {
    await Array.fromAsync([Promise.reject(new Error("boom"))]);
  } catch (e) {
    console.log("rejected:", (e as Error).message);
  }
  console.log(typeof Array.fromAsync, Array.fromAsync.length, Array.fromAsync.name);
}

main();
