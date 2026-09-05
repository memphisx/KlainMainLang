// Fetched from PerryTS/perry benchmarks/suite @ 6dcd1a5 (2026-09-04), MIT-licensed.
// Verbatim except the self-timing elapsed print is disabled below — this
// harness measures wall time itself, and the oracle compares stdout across engines.
// Benchmark: Nested loop performance
// Measures cache behavior and loop optimization
const SIZE = 3000;
const arr: number[] = [];
for (let i = 0; i < SIZE; i++) {
    arr[i] = i;
}

let sum = 0;
const start = Date.now();
for (let i = 0; i < arr.length; i++) {
    for (let j = 0; j < arr.length; j++) {
        sum = sum + arr[i] + arr[j];
    }
}
const elapsed = Date.now() - start;

// console.log("nested_loops:" + elapsed);
console.log("sum:" + sum);
