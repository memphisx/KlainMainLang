// Declared fields nothing initializes. A `name!: T` field (the definite
// assignment assertion) is an own property holding undefined until
// something assigns it, as in Node; a field with an initializer runs it.
// Without the `!`, the strict lane rejects such a field (TS2564), as tsc
// does.

// --- definite fields, no constructor: all undefined until assigned ---
class Counters {
  hits!: number
  misses!: number
  bump() { this.hits = (this.hits ?? 0) + 1 }
}
const c = new Counters()
console.log(c.hits)      // undefined
console.log(c.misses)    // undefined
c.bump()
c.bump()
console.log(c.hits)      // 2

// --- mix of initialized and definite fields ---
class Config {
  retries = 3
  timeout!: number
  verbose = true
  level!: number
}
const cfg = new Config()
console.log(cfg.retries) // 3
console.log(cfg.timeout) // undefined
console.log(cfg.verbose) // true
console.log(JSON.stringify(cfg)) // {"retries":3,"verbose":true}

// --- a nullable reference field reads undefined, not null ---
class ListNode {
  value: number = 42
  next!: ListNode | null
}
const n = new ListNode()
console.log(n.value)              // 42
console.log(n.next === undefined) // true
