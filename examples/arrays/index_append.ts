// Append-by-index: `arr[i] = v` at i == arr.length grows the array by one —
// the standard JS population idiom (`const a = []; for (...) a[i] = ...`).
// An index past the end still throws: the typed element model has no
// representation for the undefined holes real JS would materialize.
const squares: number[] = [];
for (let i = 0; i < 8; i++) squares[i] = i * i;
console.log(squares.join(" "));
squares[squares.length] = 64;
console.log(squares.length, squares[8]);
