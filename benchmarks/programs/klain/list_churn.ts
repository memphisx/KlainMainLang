// Allocator path: functional array pipelines (map/filter/reduce) that allocate a
// fresh backing buffer per stage, per iteration. This is the workload a Perceus
// reuse pass targets most directly — each `.map` result is unique and dead by the
// next line, so in-place reuse would make the whole pipeline near-allocation-free.

function bench(n: number): number {
  const arr: number[] = [];
  for (let i = 0; i < n; i++) arr.push(i);

  let sum = 0;
  for (let r = 0; r < 150; r++) {
    const doubled = arr.map((x) => x * 2);
    const evens = doubled.filter((x) => x % 4 === 0);
    sum += evens.reduce((a, b) => a + b, 0);
  }
  return sum;
}

// BENCH_SCALE (default 1) scales the workload identically across every engine and
// is opaque to the optimizer, so the loops can't be constant-folded away.
const scale = parseInt(process.env.BENCH_SCALE ?? "1");
console.log("list_churn checksum: " + bench(3000 * scale));
