// (FuncType)[] — an array of closures, each independently tracking its own state.

function makeCounter(start: number): () => number {
  let n = start
  return () => { n = n + 1; return n }
}

let counters: (() => number)[] = []
counters.push(makeCounter(0))
counters.push(makeCounter(100))

console.log(counters[0]())   // 1
console.log(counters[0]())   // 2
console.log(counters[1]())   // 101
console.log(counters.length) // 2

// A function-typed value stored in an object field is callable the same way.
interface Handler { callback: () => number }
const h: Handler = { callback: makeCounter(10) }
console.log(h.callback())    // 11
console.log(h.callback())    // 12
