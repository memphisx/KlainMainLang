// --- Named function expressions (TDD-00060/ADR-00178) ---
// A function expression may carry a name. Unlike a function *declaration*,
// that name is visible only inside the expression's own body — for
// self-reference/recursion — and never leaks to the enclosing scope.

// Recursion via the expression's own name.
const factorial = function fact(n: number): number {
    if (n <= 1) {
        return 1;
    }
    return n * fact(n - 1);
};
console.log(factorial(5));  // 120

// The name coexists with variables captured from the enclosing scope.
const offset = 100;
const sumTo = function rec(n: number): number {
    if (n === 0) {
        return offset;
    }
    return n + rec(n - 1);
};
console.log(sumTo(4));  // 100 + 4 + 3 + 2 + 1 = 110

// The name is private to the body: outside, only `factorial`/`sumTo` exist,
// not `fact`/`rec`. (Referencing `fact` here would be a compile error.)

// A decorative name (not used for recursion) is fine too.
const greet = function greeter(): string {
    return "hello";
};
console.log(greet());  // hello

// The expression's name may even shadow a top-level function of the same name
// (ADR-00601). Inside the body, `fib` is the expression itself; the outer
// `fib` call reaches the top-level function.
function fib(n: number): number {
    return -1;  // a distinct top-level function that happens to share the name
}
const realFib = function fib(n: number): number {
    return n < 2 ? n : fib(n - 1) + fib(n - 2);
};
console.log(realFib(10));  // 55 — self-recursion through the expression
console.log(fib(10));      // -1 — the shadowed top-level function
