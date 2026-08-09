// symbol V1 (TDD-00044) — a guaranteed-unique opaque value. Constructed via
// bare Symbol()/Symbol("desc") (no `new` — Symbol is not a constructor, same
// as real JS). Uniqueness/=== come from the value's own heap-pointer
// identity, not from any stored id: two Symbol() calls with the same
// description are still never equal to each other. Dynamic property keys and
// well-known symbols (Symbol.iterator, Symbol.for) are explicitly out of
// scope — see docs/status/TYPE-SYSTEM.md.

// --- every Symbol() call is unique, even with the same description ---
const a = Symbol("id")
const b = Symbol("id")
const c = a
console.log(a === b)   // 0
console.log(a === c)   // 1
console.log(a !== b)   // 1

// --- typeof, .description, .toString() ---
const named: symbol = Symbol("hello")
console.log(typeof named)      // symbol
console.log(named.description) // hello
console.log(named.toString())  // Symbol(hello)

// --- an omitted description reads back as "" ---
const anon = Symbol()
console.log(anon.description) // (blank line)
console.log(anon.toString())  // Symbol()

// --- console.log and template literals both format the same way ---
console.log(`token: ${named}`) // token: Symbol(hello)

// --- only ===/!== are meaningful; anything else (arithmetic, <, +, ...) is a
// clean compile error, same as real JS throwing TypeError on a Symbol
// operand. Not runnable here since it wouldn't compile:
//   console.log(a < b) // error: operator '<' is not supported on symbol
