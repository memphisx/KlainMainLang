// --- An anonymous function expression assigned to a variable ---
const add = function (x: number, y: number): number {
    return x + y;
};
console.log(add(2, 3));  // 5

// --- A function expression closes over its enclosing scope, same as an
// arrow function --
const base = 10;
const addBase = function (x: number): number {
    return base + x;
};
console.log(addBase(5));  // 15

// --- A function expression passed directly as a callback argument ---
const arr: number[] = [1, 2, 3];
const doubled = arr.map(function (x: number): number {
    return x * 2;
});
console.log(doubled[0], doubled[1], doubled[2]);  // 2 4 6

// --- Untyped params infer number, same as a top-level function ---
const triple = function (x) {
    return x * 3;
};
console.log(triple(7));  // 21
