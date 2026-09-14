// Evolving-any: an unannotated `let`/`var` initialized to `null` (or
// `undefined`) has TypeScript's "evolving any" type — a later assignment gives
// it a concrete value (ADR-00923). It is widened to the dynamic any-box when it
// is reassigned.

// Reassigned to a number, then a string — the binding holds each in turn.
let x = null
x = 5
console.log(x) // 5
x = 'hi'
console.log(x) // hi

// `??=` assigns only when the current value is nullish.
let count = null
count ??= 1
console.log(count) // 1
count ??= 99
console.log(count) // 1  (already non-null)

// `??` on an explicit any consults the runtime value's null/undefined tag.
let maybe: any = null
console.log(maybe ?? 'fallback') // fallback

// A never-reassigned `null` binding simply stays null.
let untouched = null
console.log(untouched) // null
console.log(untouched === null) // true
