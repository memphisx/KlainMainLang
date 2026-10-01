// Bare globals and small pure-C-stdlib builtins: NaN / Infinity,
// performance.now(), btoa/atob, encodeURI(Component)/decodeURI(Component).

// ── NaN / Infinity ───────────────────────────────────────────────────────────
// Bare globals, same as Number.NaN/Number.POSITIVE_INFINITY, but usable
// directly without the Number. prefix (real JS has both forms too). A local
// variable of the same name still shadows these, checked before falling
// back to the built-in constant.
console.log(isNaN(NaN))            // true
console.log(isFinite(Infinity))    // false
console.log(-Infinity < 0)         // true
console.log(Infinity > 1000000)    // true

// NaN is not equal to anything, including itself — so `!==` against NaN is
// true and every other comparison (`===`, `<`, `>`, `<=`, `>=`) is false.
const nan: float64 = NaN
console.log(nan === nan)           // false
console.log(nan !== nan)           // true  (the defining property of NaN)
console.log(nan < 1.0)             // false
console.log(nan >= 1.0)            // false

// ── performance.now() ────────────────────────────────────────────────────────
// A CLOCK_MONOTONIC-based timestamp in milliseconds (a double, sub-
// millisecond precision) — unlike Date.now(), not tied to wall-clock time,
// so it's the right choice for measuring elapsed time. No fixed "time
// origin" like the browser spec (process/page start) — just the raw
// monotonic clock reading, which is exactly as valid for subtracting two
// calls to measure elapsed time.
const t1: number = performance.now()
let arr: number[] = []
for (let i = 0; i < 200000; i++) { arr.push(i) }
const t2: number = performance.now()
console.log(arr.length)    // 200000
console.log(t2 >= t1)      // true

// ── performance.mark() / performance.measure() ──────────────────────────────
// Named timing marks on top of performance.now() above. mark(name) records a
// PerformanceMark; measure(name, startMark, endMark?) returns a
// PerformanceMeasure whose duration spans the two marks (through now when
// endMark is omitted).
performance.mark("loop-start")
let arr2: number[] = []
for (let i = 0; i < 200000; i++) { arr2.push(i) }
performance.mark("loop-end")
const loopMs: number = performance.measure("loop", "loop-start", "loop-end").duration
console.log(loopMs >= 0)                                  // true
const sinceStart = performance.measure("since-start", "loop-start")
console.log(sinceStart.duration >= loopMs, sinceStart.entryType)   // true measure

// A measure from a mark name uses that name's latest mark.
performance.mark("loop-start")
const freshMs: number = performance.measure("fresh", "loop-start").duration
console.log(freshMs < loopMs)                               // true — measured from the newer mark

// Measuring against a name that was never marked throws a SyntaxError
// DOMException.
try {
  performance.measure("bad", "never-marked")
} catch (e) {
  console.log((e as Error).message)    // The "never-marked" performance mark has not been set
}

// ── btoa / atob — base64 ─────────────────────────────────────────────────────
console.log(btoa("hello"))                      // aGVsbG8=
console.log(btoa("hi"))                         // aGk=
console.log(atob("aGVsbG8="))                   // hello
console.log(atob(btoa("round trip 123!@#")))    // round trip 123!@#

// ── encodeURIComponent / decodeURIComponent ─────────────────────────────────
// Escapes everything except the unreserved set (letters, digits,
// - _ . ! ~ * ' ( )) — meant for encoding a single query-string value or
// path segment, not a whole URI.
console.log(encodeURIComponent("hello world"))         // hello%20world
console.log(encodeURIComponent("a=b&c=d"))             // a%3Db%26c%3Dd
console.log(decodeURIComponent("hello%20world"))       // hello world
console.log(decodeURIComponent("a%3Db%26c%3Dd"))       // a=b&c=d

// ── encodeURI / decodeURI ────────────────────────────────────────────────────
// Also leaves the *reserved* URI characters (; / ? : @ & = + $ , #) alone —
// meant for encoding a whole URI without breaking its own structure.
// decodeURI's one real behavioral difference from decodeURIComponent: it
// will NOT decode a "%XX" escape that represents one of those reserved
// characters, leaving it as literal "%XX" text instead.
console.log(encodeURI("http://example.com/path?a=1&b=2 space"))
// http://example.com/path?a=1&b=2%20space (only the space got escaped)
console.log(decodeURI("http://example.com/path%3Fa=1&b=2%20space"))
// http://example.com/path%3Fa=1&b=2 space (%3F stays literal — it's '?', reserved)

// ── globalThis ───────────────────────────────────────────────────────────────
// The standard alias for the global object: `globalThis.X` reaches the same
// ambient global X as writing X directly. Native single-file builds have no
// dynamic global record, so only *known* globals resolve — an unknown
// `globalThis.foo` is a compile error, not a runtime lookup. Works for global
// functions, namespaces, and nested member/call chains.
console.log(globalThis.parseInt("42", 10))              // 42
console.log(globalThis.Math.max(3, 7, 5))               // 7
console.log(globalThis.JSON.stringify({ a: 1, b: 2 }))  // {"a":1,"b":2}
globalThis.console.log("via globalThis.console")        // via globalThis.console
globalThis.setTimeout(() => {
  console.log("globalThis.setTimeout fired")            // globalThis.setTimeout fired
}, 0)
