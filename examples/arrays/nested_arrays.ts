// --- Array-of-arrays (nested arrays, TDD-00029) ---
// A nested array literal, indexing (read and write), destructuring,
// for...of, and the copy/insert-based methods all work. .flat(depth?)/
// .flatMap(fn) (TDD-00029's own follow-on) also work, including an
// explicit compile-time-constant depth and Infinity. Callback-invoking
// HOFs that would receive a nested array *as an element* (map/filter/
// forEach/reduce/find*/some/every/sort/indexOf/includes/join) are still a
// deliberate, clean compile error — see docs/tdd/TDD-00029.md.

let matrix: number[][] = [[1, 2, 3], [4, 5, 6]]

console.log(matrix.length)      // 2
console.log(matrix[0].length)   // 3
console.log(matrix[0][0])       // 1
console.log(matrix[1][2])       // 6

// Index write, one level deep
matrix[0][1] = 99
console.log(matrix[0][1])       // 99

// Index write, replacing a whole inner array
matrix[1] = [7, 8, 9]
console.log(matrix[1][0])       // 7
console.log(matrix[1].length)   // 3

// A row extracted by indexing is a normal array from there on
let row: number[] = matrix[0]
console.log(row[2])             // 3

// for...of over the outer array yields inner arrays
for (const r of matrix) {
    let total: number = 0
    for (const v of r) {
        total = total + v
    }
    console.log(total)          // 103, then 24
}

// Array destructuring
const [first, second] = matrix
console.log(first[0])           // 1
console.log(second[0])          // 7

// .at()/.with() on the outer array
console.log(matrix.at(0)[0])    // 1
console.log(matrix.at(-1)[1])   // 8
const replaced: number[][] = matrix.with(0, [0, 0])
console.log(replaced[0][0])     // 0
console.log(matrix[0][0])       // 1 (with() doesn't mutate)

// push/pop of a whole inner array
matrix.push([10, 11])
console.log(matrix.length)      // 3
console.log(matrix.pop()[0])    // 10
console.log(matrix.length)      // 2

// JSON.stringify recurses into nested arrays
console.log(JSON.stringify(matrix))              // [[1,99,3],[7,8,9]]
console.log(JSON.stringify({ grid: [[1, 2], [3, 4]] }))  // {"grid":[[1,2],[3,4]]}

// Nested arrays of strings work the same way
const board: string[][] = [["x", "o"], ["o", "x"]]
console.log(board[0][0])        // x
console.log(board[1][1])        // x

// --- .flat(depth?) / .flatMap(fn) ---
// depth defaults to 1 and must be a compile-time constant (a literal, or
// Infinity) — this compiler's arrays have a fixed nesting depth at the
// type level, so the result's element type has to be knowable at compile
// time, not just its runtime value.
const cube: number[][][] = [[[1, 2], [3]], [[4, 5, 6]]]

const flatOnce: number[][] = cube.flat()
console.log(flatOnce.length)    // 3 (one level: 2 inner arrays from the
                                 // first group, 1 from the second)
console.log(flatOnce[0][0])     // 1

const flatAll: number[] = cube.flat(2)
console.log(flatAll.length)     // 6
console.log(flatAll[0])         // 1
console.log(flatAll[5])         // 6

const flatInf: number[] = cube.flat(Infinity)
console.log(flatInf.length)     // 6, same as flat(2) here — Infinity means
                                 // "as deep as the type actually nests"

const flatNone: number[][][] = cube.flat(0)
console.log(flatNone.length)    // 2 (a fresh copy, unflattened)

// flatMap: map then flatten by exactly one level (no depth argument in
// real JS's own flatMap either)
const nums: number[] = [1, 2, 3]
const pairs: number[] = nums.flatMap((x) => [x, x * 10])
console.log(pairs.length)       // 6
console.log(pairs[0])           // 1
console.log(pairs[1])           // 10

// A callback that doesn't return an array is just a plain map — nothing
// to flatten, matching real JS exactly
const doubled: number[] = nums.flatMap((x) => x * 2)
console.log(doubled.length)     // 3
console.log(doubled[2])         // 6
