// AbortSignal default reason and the static constructors (TDD-00081 Stage 3,
// ADR-00693). A no-argument abort() defaults its reason to an "AbortError"
// DOMException; AbortSignal.abort(reason?) makes an already-aborted signal, and
// AbortSignal.any(signals) a signal aborted as soon as any input is aborted.

// A bare abort() gets the default "AbortError" DOMException reason.
const c = new AbortController()
c.abort()
console.log(c.signal.reason?.name)       // AbortError

// AbortSignal.abort() — an already-aborted signal.
const s = AbortSignal.abort()
console.log(s.aborted)                    // true
console.log(s.reason?.name)               // AbortError

// A custom reason is preserved verbatim, with its type — reason is `any`, and
// a never-aborted signal's reason reads undefined.
const s2 = AbortSignal.abort("cancelled")
console.log(s2.reason)                     // cancelled
const s3 = AbortSignal.abort(42)
console.log(s3.reason, typeof s3.reason)   // 42 number
const fresh = new AbortController()
console.log(typeof fresh.signal.reason)    // undefined

// AbortSignal.any([...]) aborts when any input signal is aborted, inheriting
// that signal's reason.
const a = new AbortController()
const b = new AbortController()
b.abort()
const any = AbortSignal.any([a.signal, b.signal])
console.log(any.aborted)                   // true
console.log(any.reason?.name)              // AbortError

// None aborted yet → the composite is not aborted.
const pending = AbortSignal.any([a.signal])
console.log(pending.aborted)               // false

// Live propagation: a source aborted AFTER the composite is built still
// latches it — flag, reason, and its listeners fire (ADR-01010).
a.abort("late")
console.log(pending.aborted)               // true
console.log(pending.reason)                // late
