// Every async function returns a real promise now — even one that never awaits.
// So .then/.catch/.finally chaining, and Promise combinators, work on plain
// async results, and a throwing async function rejects (it doesn't throw at the
// call site). None of this touches the network, so no libcurl is linked.

async function compute(n: number): Promise<number> {
  return n * n
}

async function mightFail(fail: boolean): Promise<string> {
  if (fail) {
    throw new Error("computation failed")
  }
  return "ok"
}

// .then value-chaining: each callback's result flows into the next promise.
compute(6)
  .then((sq: number) => sq + 1)
  .then((r: number) => { console.log("chained: " + r) }) // chained: 37

// .catch recovers a rejection into a value the following .then observes.
mightFail(true)
  .catch((e) => "recovered")
  .then((s: string) => { console.log("after catch: " + s) }) // after catch: recovered

// Combinators over ordinary async results (no fetch involved).
async function run(): Promise<void> {
  const all: number[] = await Promise.all([compute(2), compute(3), compute(4)])
  console.log("all: " + all[0] + " " + all[1] + " " + all[2]) // all: 4 9 16

  // Promise.any skips a rejecting member and resolves to the first fulfilled.
  async function boom(n: number): Promise<number> { throw new Error("no" + n) }
  const first: number = await Promise.any([boom(1), compute(9), boom(2)])
  console.log("any: " + first) // any: 81

  // await re-throws a rejected promise into a surrounding try/catch.
  try {
    await mightFail(true)
  } catch (e) {
    console.log("awaited reject: " + e.message) // awaited reject: computation failed
  }
}
run()

// .then/.catch reactions run as microtasks, after the synchronous script.
console.log("sync done")
