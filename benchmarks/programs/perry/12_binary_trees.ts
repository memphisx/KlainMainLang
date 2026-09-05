// Fetched from PerryTS/perry benchmarks/suite @ 6dcd1a5 (2026-09-04), MIT-licensed.
// Verbatim except the self-timing elapsed print is disabled below — this
// harness measures wall time itself, and the oracle compares stdout across engines.
// Benchmark: Object allocation
// Measures object creation and field access
const ITERATIONS = 1000000;

class Point3D {
    x: number;
    y: number;
    z: number;

    constructor(x: number, y: number, z: number) {
        this.x = x;
        this.y = y;
        this.z = z;
    }
}

let sum = 0;
const start = Date.now();

for (let i = 0; i < ITERATIONS; i++) {
    const p = new Point3D(i, i + 1, i + 2);
    sum = sum + p.x + p.y + p.z;
}

const elapsed = Date.now() - start;

// console.log("object_alloc:" + elapsed);
console.log("sum:" + sum);
