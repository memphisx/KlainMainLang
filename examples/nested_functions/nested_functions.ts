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

// --- A nested function CAN close over its enclosing function's locals and
// parameters — it is emitted as a closure value when it does (TDD-00129) ---
function withCapture(x: number): number {
    function inner(): number { return x + 1; }
    return inner();
}
console.log(withCapture(10));  // 11

// --- Declarations inside a lexical block (an if/for/while body, one or more
// blocks deeper than the enclosing body) are supported too, and are scoped to
// that block (TDD-00152) ---
function classify(n: number): string {
    if (n < 0) {
        function label(): string { return "negative"; }
        return label();
    }
    let sum = 0;
    for (let i = 0; i < n; i++) {
        function step(): number { return 2; }  // non-capturing: fine
        sum += step();
    }
    return "sum=" + sum;
}
console.log(classify(-1));  // negative
console.log(classify(3));   // sum=6

// --- One deliberate limit: a block-nested function cannot capture a C-style
// for-loop's own variable (it is a single per-iteration cell) — copy it to a
// `const` inside the loop body and capture that instead:
//
//   for (let i = 0; i < 3; i++) {
//       const cur = i;
//       function useCur(): number { return cur; }  // ok
//   }
