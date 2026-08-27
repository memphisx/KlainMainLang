// A class with declared-but-uninitialized fields and no explicit constructor.
// The instance is calloc'd, so every unassigned field reads as its deterministic
// zero value (0 / false / null) — the same ADR-00157 convention a class *with* a
// constructor already relies on — and any field initializers that ARE present
// still run. See docs/status/LANGUAGE-CONSTRUCTS.md.

// --- bare fields, no constructor: all zero-filled ---
class Counters {
  hits: number
  misses: number
}
const c = new Counters()
console.log(c.hits)      // 0
console.log(c.misses)    // 0

// --- mix of initialized and bare fields ---
class Config {
  retries = 3
  timeout: number        // bare → 0
  verbose = true
  level: number          // bare → 0
}
const cfg = new Config()
console.log(cfg.retries) // 3
console.log(cfg.timeout) // 0
console.log(cfg.verbose) // true
console.log(cfg.level)   // 0

// --- a nullable reference field defaults to null; an initialized one runs ---
class ListNode {
  value: number = 42
  next: ListNode | null   // bare → null
}
const n = new ListNode()
console.log(n.value)         // 42
console.log(n.next === null) // true

// --- works for a generic class too (generic classes can't `extends`) ---
class Box<T> {
  value: T
  count: number
}
const b = new Box<number>()
console.log(b.value)     // 0
console.log(b.count)     // 0
