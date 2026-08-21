// `for await...of` over every accepted iterable shape, and the sync
// [Symbol.iterator] protocol. No network — runs offline and deterministically.

// A sync generator in `for await` (the spec's CreateAsyncFromSyncIterator):
// plain yields are identity-awaited, promise yields await to their values.
function* nums(): number {
  yield 1;
  yield 2;
  yield 3;
}
async function work(n: number): Promise<number> {
  return n * 10;
}
function* jobs(): Promise<number> {
  yield work(1);
  yield work(2);
}

// A class implementing the *sync* iteration protocol by hand —
// `[Symbol.iterator]()` returning an iterator whose `next()` yields the
// spec's `{value, done}` shape. Works in plain `for...of` too.
class Countdown {
  n: number;
  constructor(start: number) { this.n = start; }
  [Symbol.iterator](): Countdown { return this; }
  next(): { value: number; done: boolean } {
    if (this.n <= 0) { return { value: 0, done: true }; }
    const v = this.n;
    this.n = this.n - 1;
    return { value: v, done: false };
  }
}

async function* agen(): number {
  yield await work(4);
  yield await work(5);
}

async function main(): Promise<void> {
  for await (const n of nums()) { console.log(n); }   // 1 2 3
  for await (const v of jobs()) { console.log(v); }   // 10 20

  // Map values / Set elements, same shapes as sync for-of.
  const m = new Map<string, number>();
  m.set("a", 5);
  m.set("b", 7);
  for await (const v of m) { console.log(v); }        // 5 7
  const s = new Set<string>();
  s.add("hi");
  s.add("yo");
  for await (const t of s) { console.log(t); }        // hi yo

  // An object literal with a [Symbol.asyncIterator] member — arrow-valued or
  // method shorthand — is a for-await iterable.
  const obj = { [Symbol.asyncIterator]: () => agen() };
  for await (const x of obj) { console.log(x); }      // 40 50

  for (const x of new Countdown(3)) { console.log(x); }       // 3 2 1
  for await (const x of new Countdown(2)) { console.log(x); } // 2 1
}
main();
