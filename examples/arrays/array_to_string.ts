// Array → string coercion: an array renders as its elements joined by commas,
// the same way real JS's Array.prototype.toString does. This works through
// String(arr), a `${arr}` template interpolation, and .join() — including for
// nested arrays, where each nested element renders as its own comma-joined
// string (recursively).

const nums = [1, 2, 3]
console.log(String(nums))       // 1,2,3
console.log(`nums = ${nums}`)   // nums = 1,2,3

const words = ["red", "green", "blue"]
console.log(words.join(" | "))  // red | green | blue
console.log(`${words}`)         // red,green,blue

// Nested arrays: each row is String()-ed first, then joined by the separator.
const matrix = [[1, 2], [3, 4], [5, 6]]
console.log(matrix.join("; "))  // 1,2; 3,4; 5,6
console.log(String(matrix))     // 1,2,3,4,5,6

// Arbitrary nesting depth flattens to a single comma-joined string.
const deep = [[[1, 2], [3]], [[4, 5]]]
console.log(String(deep))       // 1,2,3,4,5

// An empty array is the empty string.
const empty: number[] = []
console.log(`[${empty}]`)       // []
