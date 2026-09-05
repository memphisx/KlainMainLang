// Fetched from PerryTS/perry benchmarks/suite @ 6dcd1a5 (2026-09-04), MIT-licensed.
// Verbatim except the self-timing elapsed print is disabled below — this
// harness measures wall time itself, and the oracle compares stdout across engines.
// Benchmark: Large number computation
// Measures numeric computation with overflow handling
const ITERATIONS = 100000000;
let sum = 0;

const start = Date.now();
for (let i = 0; i < ITERATIONS; i++) {
    // Simulate factorial-like accumulation pattern
    sum = sum + (i % 1000);
}
const elapsed = Date.now() - start;

// console.log("accumulate:" + elapsed);
console.log("sum:" + sum);
