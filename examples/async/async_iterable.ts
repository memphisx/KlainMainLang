// User-defined async iterables via Symbol.asyncIterator — TDD-00089.
//
// `for await...of` consumes not only async generators but any class that
// implements the async-iteration protocol by hand: a [Symbol.asyncIterator]()
// method returning an iterator whose `async next()` resolves to {value, done}.
// The iterator can be a separate object or the iterable itself (`return this`).

// A separate iterator object: Range hands out a fresh RangeIter each iteration.
class RangeIter {
  private cur: number
  private end: number
  constructor(end: number) { this.cur = 0; this.end = end }
  async next(): Promise<{ value: number; done: boolean }> {
    if (this.cur >= this.end) { return { value: 0, done: true } }
    const v = this.cur
    this.cur = this.cur + 1
    return { value: v, done: false }
  }
}

class Range {
  private end: number
  constructor(end: number) { this.end = end }
  [Symbol.asyncIterator](): RangeIter { return new RangeIter(this.end) }
}

// A self-iterator (`return this`) whose next() awaits between elements.
async function fetchTick(n: number): Promise<number> {
  return n * 100
}

class Ticker {
  private i: number
  private n: number
  constructor(n: number) { this.i = 0; this.n = n }
  [Symbol.asyncIterator](): Ticker { return this }
  async next(): Promise<{ value: number; done: boolean }> {
    if (this.i >= this.n) { return { value: -1, done: true } }
    const t = await fetchTick(this.i)
    this.i = this.i + 1
    return { value: t, done: false }
  }
}

// The yielded value can be an object; the loop variable destructures it.
class PairIter {
  private i: number
  constructor() { this.i = 0 }
  [Symbol.asyncIterator](): PairIter { return this }
  async next(): Promise<{ value: { a: number; b: number }; done: boolean }> {
    if (this.i >= 3) { return { value: { a: 0, b: 0 }, done: true } }
    const j = this.i
    this.i = this.i + 1
    return { value: { a: j, b: j * j }, done: false }
  }
}

// An iterator whose next() throws rejects the awaited step, catchable normally.
class Boom {
  [Symbol.asyncIterator](): Boom { return this }
  async next(): Promise<{ value: number; done: boolean }> {
    throw new Error("stream failed")
  }
}

async function main(): Promise<void> {
  for await (const x of new Range(4)) {
    console.log(x)                      // 0, 1, 2, 3
  }

  for await (const t of new Ticker(3)) {
    console.log(t)                      // 0, 100, 200
  }

  for await (const { a, b } of new PairIter()) {
    console.log(a + " -> " + b)         // 0 -> 0, 1 -> 1, 2 -> 4
  }

  try {
    for await (const v of new Boom()) {
      console.log(v)
    }
  } catch (e) {
    console.log("caught " + e.message)  // caught stream failed
  }

  // for await also works over a sync array — each element is awaited, so an
  // array of promises is consumed sequentially (TDD-00092).
  const jobs: Promise<number>[] = [fetchTick(1), fetchTick(2), fetchTick(3)]
  for await (const n of jobs) {
    console.log("job " + n)             // job 100, job 200, job 300
  }
}

main()
