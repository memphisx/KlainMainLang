// Arrays are reference types: binding one array to another name (or reassigning)
// makes both names refer to the SAME array object, so a mutation through either
// is visible through the other, and `===` is object identity — not a contents
// comparison (TDD-00213 Stage 1).

const b = [1, 2, 3]
const a = b // a and b are the same array
b.push(4)
console.log(a) // [ 1, 2, 3, 4 ]
console.log(a === b) // true

a.push(5)
console.log(b) // [ 1, 2, 3, 4, 5 ] — a mutation through a is visible through b

// A distinct array with equal contents is a different object.
const c = [1, 2, 3, 4, 5]
console.log(a === c) // false

// Reassigning an alias to a NEW array (spread copy) detaches it.
let x = [7, 8]
const y = x
x = [...x] // x is now a fresh array
x.push(9)
console.log(y) // [ 7, 8 ] — y still the original
console.log(x === y) // false
