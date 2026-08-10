// --- A helper function declared inside another function's body ---
function outer(x: number): number {
    function double(y: number): number {
        return y * 2;
    }
    return double(x) + 1;
}
console.log(outer(5));  // 11

// --- A nested function can forward-reference (call itself, or a sibling
// declared later in the same body) exactly like a top-level function can ---
function withFib(): number {
    const r = fib(6);
    return r;

    function fib(n: number): number {
        if (n <= 1) { return n; }
        return fib(n - 1) + fib(n - 2);
    }
}
console.log(withFib());  // 8

// --- Nested function declarations also work inside an arrow function's
// block body, and can call each other ---
const withHelpers = (x: number): number => {
    function double(n: number): number { return n * 2; }
    function triple(n: number): number { return double(n) + n; }
    return triple(x);
};
console.log(withHelpers(4));  // 12

// --- Visible to further-nested functions, not just its own direct siblings
// — a grandchild can call something declared in its grandparent's body ---
function grand(): number {
    function helper(): number { return 100; }
    function middle(): number {
        function inner(): number {
            return helper();
        }
        return inner();
    }
    return middle();
}
console.log(grand());  // 100

// --- Scoped to its own enclosing function: two unrelated functions can each
// declare a nested function with the same name without colliding ---
function a(): number {
    function helper(): number { return 1; }
    return helper();
}
function b(): number {
    function helper(): number { return 2; }
    return helper();
}
console.log(a());  // 1
console.log(b());  // 2

// --- V1 scope note: a nested function declaration does NOT close over its
// enclosing function's locals the way an arrow function would — it gets its
// own clean scope, just like a top-level function. Referencing an outer
// local (e.g. `x` below) is a compile error, not silently-wrong behavior.
// See docs/tdd/TDD-00057.md for the reasoning and what's deliberately
// deferred:
//
// function withCapture(x: number): number {
//     function inner(): number { return x; }  // compile error
//     return inner();
// }
