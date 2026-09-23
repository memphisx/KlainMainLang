// JS number coercion of the two nullish values, natively: ToNumber(undefined)
// is NaN and ToNumber(null) is 0, so an absent `T | undefined` result poisons
// arithmetic and comparisons exactly as in Node, while a null still reads as
// zero. A reduce whose callback falls off the end hands `undefined` to the next
// step — the whole fold is NaN (or `undefined` if every step fell off).
// Strict mode rejects each of these at compile time, as tsc does.
// Run with:  klainmain -compat=js undefined_arithmetic.js

function positive(x) { if (x > 0) return x; }
function orNull(x) { if (x > 0) return x; return null; }

console.log(positive(-1) + 2, positive(3) + 2);   // NaN 5
console.log(positive(-1) < 1, positive(-1) | 0);  // false 0
console.log(orNull(-1) + 2, orNull(-1) * 3);      // 2 0
console.log("v=" + positive(-1));                 // v=undefined

let u = positive(-1);
console.log(u * 3, u > 0);                        // NaN false

console.log([1, 2, 3].reduce((a, x) => { if (x > 1) return a + x }, 0)); // NaN
console.log([1, 2, 3].reduce((a, x) => { if (x > 5) return a + x }, 0)); // undefined
console.log([1, 2, 3].reduce((a, x) => a + x, 0));                       // 6
