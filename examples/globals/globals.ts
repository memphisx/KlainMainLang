// Bare globals and small pure-C-stdlib builtins: NaN / Infinity,
// performance.now(), btoa/atob, encodeURI(Component)/decodeURI(Component).

// ── NaN / Infinity ───────────────────────────────────────────────────────────
// Bare globals, same as Number.NaN/Number.POSITIVE_INFINITY, but usable
// directly without the Number. prefix (real JS has both forms too). A local
// variable of the same name still shadows these, checked before falling
// back to the built-in constant.
console.log(isNaN(NaN))            // 1 (true)
console.log(isFinite(Infinity))    // 0 (false)
console.log(-Infinity < 0)         // 1 (true)
console.log(Infinity > 1000000)    // 1 (true)

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
console.log(t2 >= t1)      // 1 (true)

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
