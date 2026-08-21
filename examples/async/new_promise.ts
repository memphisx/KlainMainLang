// new Promise((resolve, reject) => …) — the executor constructor (TDD-00087).
// The executor receives resolve/reject and settles the promise, either
// synchronously or later from a callback. Passing a promise to resolve adopts
// that thenable (TDD-00091). The executor may be an arrow, a function
// expression, a closure variable, or a top-level-function reference.

async function main(): Promise<void> {
  // Synchronous resolve.
  const answer = await new Promise<number>((resolve) => {
    resolve(42)
  })
  console.log(answer)                              // 42

  // Reject → await re-throws into a surrounding try/catch.
  try {
    await new Promise<number>((resolve, reject) => {
      reject(new Error("denied"))
    })
  } catch (e) {
    console.log("caught " + e.message)             // caught denied
  }

  // First settle wins: the later resolve and the reject are ignored.
  const once = await new Promise<number>((resolve, reject) => {
    resolve(1)
    resolve(2)
    reject(new Error("too late"))
  })
  console.log(once)                                // 1

  // The canonical use: wrap a callback/timer API. `resolve` is captured into the
  // setTimeout callback and called when the timer fires; awaiting drives it.
  console.log("waiting...")
  const delayed = await new Promise<string>((resolve) => {
    setTimeout(() => { resolve("done") }, 20)
  })
  console.log(delayed)                             // done

  // A chain on a new Promise runs its callback as a microtask.
  new Promise<number>((resolve) => resolve(10))
    .then((n: number) => n * 2)
    .then((m: number) => { console.log("chained " + m) })  // chained 20

  // Thenable adoption: resolving with a promise makes the outer promise mirror
  // it — the outer settles with the inner's value (or reason) when it settles.
  const adopted = await new Promise<number>((resolve) => {
    resolve(fetchAnswer())                         // resolve(aPromise)
  })
  console.log("adopted " + adopted)                // adopted 7
}

async function fetchAnswer(): Promise<number> {
  return 7
}
main()
