// Allocator path: hash-table (Map) backing storage built up and thrown away in
// full each round — stresses the Map/Set allocator and rehash growth, a
// different shape from the object-graph and array-buffer paths.

function bench(n: number): number {
  let total = 0;
  for (let r = 0; r < 80; r++) {
    const m = new Map<number, number>();
    for (let i = 0; i < n; i++) m.set(i, i * i);
    for (let i = 0; i < n; i++) total += m.get(i) as number;
  }
  return total;
}

// BENCH_SCALE (default 1) scales the workload identically across every engine and
// is opaque to the optimizer, so the loops can't be constant-folded away.
const scale = parseInt(process.env.BENCH_SCALE ?? "1");
console.log("map_churn checksum: " + bench(3000 * scale));
