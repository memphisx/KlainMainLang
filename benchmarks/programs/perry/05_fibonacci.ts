// Fetched from PerryTS/perry benchmarks/suite @ 6dcd1a5 (2026-09-04), MIT-licensed.
// Verbatim except the self-timing elapsed print is disabled below — this
// harness measures wall time itself, and the oracle compares stdout across engines.
// Benchmark: Recursive Fibonacci
// Measures function call overhead and recursion
function fib(n: number): number {
    if (n <= 1) return n;
    return fib(n - 1) + fib(n - 2);
}

const N = 40;
const start = Date.now();
const result = fib(N);
const elapsed = Date.now() - start;

// console.log("fibonacci:" + elapsed);
console.log("fib(" + N + "):" + result);
