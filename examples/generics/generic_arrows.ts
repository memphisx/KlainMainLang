// Generic arrow functions and function expressions: `<T>(x: T) => …`.
// The type parameters are checked like any other generic's, and the
// function is compiled once, with T erased (like an `@erased` function).

const identity = <T>(x: T): T => x;
console.log(identity(42));      // 42
console.log(identity("hello")); // hello

// A trailing comma, several parameters, a constraint and a default.
const first = <T,>(xs: T[]): T | undefined => xs[0];
const pick = <K extends string, V = number>(key: K, value: V) => key;
console.log(first([7, 8, 9]));  // 7
console.log(pick("name", 1));   // name

// A generic function expression.
const wrap = function <U>(value: U): U { return value; };
console.log(wrap(true));        // true
