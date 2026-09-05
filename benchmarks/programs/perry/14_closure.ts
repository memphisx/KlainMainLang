// Fetched from PerryTS/perry benchmarks/suite @ 6dcd1a5 (2026-09-04), MIT-licensed.
// Verbatim except the self-timing elapsed print is disabled below — this
// harness measures wall time itself, and the oracle compares stdout across engines.
// Benchmark: Function call overhead
// Measures function invocation cost
const ITERATIONS = 50000000;

function compute(x: number): number {
    return x * 2 + 1;
}

let sum = 0;
const start = Date.now();
for (let i = 0; i < ITERATIONS; i++) {
    sum = sum + compute(i);
}
const elapsed = Date.now() - start;

// console.log("function_calls:" + elapsed);
console.log("sum:" + sum);
