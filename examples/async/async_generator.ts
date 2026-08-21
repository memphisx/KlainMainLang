// Async generators (`async function*`) and `for await...of` — TDD-00085.
//
// An async generator is a coroutine that both yields values to its consumer and
// awaits between yields. Its .next() returns a Promise<{value, done}>, which a
// `for await...of` loop awaits each step. No network here — it awaits an ordinary
// async function — so this runs offline and deterministically.

async function slowSquare(n: number): Promise<number> {
  return n * n
}

// Yields the running squares 1, 4, 9, 16, awaiting each computation.
async function* squares(limit: number): number {
  let i: number = 1
  while (i <= limit) {
    yield await slowSquare(i)
    i = i + 1
  }
}

// A throwing async generator rejects the outstanding .next() promise.
async function* boom(): number {
  yield 1
  throw new Error("stop")
}

// An async generator can yield objects (or tuples), and `for await...of` can
// destructure each yielded element straight into its own bindings.
async function* points(): { x: number; y: number } {
  yield { x: 1, y: 2 }
  yield { x: 3, y: 4 }
}

async function main(): Promise<void> {
  for await (const sq of squares(4)) {
    console.log(sq)          // 1, 4, 9, 16
  }

  // Destructuring loop variable: bind x and y from each yielded point.
  for await (const { x, y } of points()) {
    console.log(x + y)       // 3, 7
  }

  // Manual iteration: .next() returns a Promise<{value, done}>.
  const it = squares(1)
  const first = await it.next()
  console.log(first.value + " done=" + first.done)   // 1 done=false
  const end = await it.next()
  console.log("done=" + end.done)                      // done=true

  // .next() returns a genuinely-pending promise: the generator body runs as a
  // microtask, not synchronously at the .next() call. So a side effect before the
  // first yield is deferred past the code right after .next() — matching JS's
  // "before, after, body" ordering (ADR-00275).
  const d = deferredGen()
  console.log("before")                                // before
  const dp = d.next()
  console.log("after")                                 // after
  console.log((await dp).value)                        // body (from the gen), then 1

  const b = boom()
  console.log((await b.next()).value)                  // 1
  try {
    await b.next()
  } catch (e) {
    console.log("caught " + e.message)                 // caught stop
  }

  // .throw(e) injects an error at the suspension point; a body try/catch handles
  // it (and may await), so the .throw() promise fulfils with the recovered value.
  const g = guardedGen()
  console.log((await g.next()).value)                  // 1
  console.log((await g.throw(new Error("oops"))).value) // 42 (resumed in catch)

  // .return(v) completes an async generator early, running its finally block.
  const c = cleanupGen()
  console.log((await c.next()).value)                  // 1
  const r = await c.return(7)
  console.log(r.value + " done=" + r.done)             // 7 done=true

  // yield* delegates to an inner async generator: each inner step is awaited,
  // its values re-yielded, and its return value becomes the yield* result.
  for await (const v of composed()) {
    console.log(v)                                     // 1, 4, then 0
  }
}

async function* firstTwoSquares(): number {
  yield await slowSquare(1)                            // 1
  yield await slowSquare(2)                            // 4
  return 2                                             // count produced
}

async function* composed(): number {
  const count = yield* firstTwoSquares()
  console.log("delegated " + count + " values")        // delegated 2 values
  yield 0
}

async function* guardedGen(): number {
  try {
    yield await slowSquare(1)
    yield await slowSquare(2)
  } catch (e) {
    console.log("gen caught " + e.message)             // gen caught oops
    yield 42
  }
}

async function* cleanupGen(): number {
  try {
    yield 1
    yield 2
  } finally {
    console.log("gen cleanup")                         // runs on .return()
  }
}

// A side effect before the first yield: with a deferred .next(), it prints only
// once the microtask step runs — after "before"/"after" (see main()).
async function* deferredGen(): number {
  console.log("body")
  yield 1
}

main()
