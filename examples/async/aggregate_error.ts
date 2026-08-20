// AggregateError — the error Promise.any throws when every input promise
// rejects. Also constructible directly, carrying the aggregated errors on
// its `.errors` array.

const errs = [new Error("disk full"), new Error("timeout")]
const agg = new AggregateError(errs, "all backends failed")

console.log(agg.name)              // AggregateError
console.log(agg.message)           // all backends failed
console.log(agg.errors.length)     // 2
console.log(agg.errors[0].message) // disk full
console.log(agg.errors[1].message) // timeout

console.log(agg instanceof AggregateError) // true
console.log(agg instanceof Error)          // true (AggregateError inherits Error)

// Catching it and reading .errors back
try {
  throw new AggregateError([new Error("only one")], "wrapped")
} catch (e) {
  console.log(e.name + ": " + e.errors.length + " (" + e.errors[0].message + ")")
}
