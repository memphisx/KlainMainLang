// A thenable whose `then` is overloaded: `await` resolves to what every
// signature's onfulfilled callback is given (here a number).
interface Thenable<T> {
  then<U>(ok?: (value: T) => Thenable<U>): Thenable<U>;
  then<U>(ok?: (value: T) => U): Thenable<U>;
}

function make(v: number): Thenable<number> {
  return { then(ok?: (value: number) => any) { ok?.(v); return make(v); } } as Thenable<number>;
}

async function main() {
  const n = await make(42);
  console.log(n + 1);
}

main();
