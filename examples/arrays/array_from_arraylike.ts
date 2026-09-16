// Array.from over an array-like `{ length: n }` (ADR-00957). The object's
// `length` is read and coerced to a non-negative integer; the result has that
// many `undefined` elements. With a map callback, `Array.from({ length: n },
// (_, i) => …)` fills each slot from its index — the idiomatic "range" builder.
// Runs the same under Node.js.

// The classic range/sequence builder: element value derived from the index.
const squares = Array.from({ length: 5 }, (_, i) => i * i);
console.log("squares:", squares.join(", ")); // 0, 1, 4, 9, 16

// A string sequence works the same way — the callback's return type drives the
// element type of the resulting array.
const labels = Array.from({ length: 3 }, (_, i) => "row-" + i);
console.log("labels:", labels.join(", ")); // row-0, row-1, row-2

// Without a map callback the elements are a genuine `undefined` hole.
const holes = Array.from({ length: 4 });
console.log("length:", holes.length, "| first is undefined:", holes[0] === undefined);

// A length that is zero or negative yields an empty array (per the spec's
// ToIntegerOrInfinity + max(0, …) clamp).
console.log("negative length:", Array.from({ length: -3 }).length);

// The length may come from a runtime value, not just a literal.
const size = squares.length;
const grid = Array.from({ length: size }, (_, i) => size - i);
console.log("countdown:", grid.join(", ")); // 5, 4, 3, 2, 1
