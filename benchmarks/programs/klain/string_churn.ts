// Allocator path: string concatenation — each `+=` allocates a fresh buffer and
// copies, so this is quadratic allocation pressure. Isolates the string-runtime
// allocation path from the object/array/Map paths.

function bench(n: number): number {
  let longest = 0;
  for (let r = 0; r < 30; r++) {
    let s = "";
    for (let i = 0; i < n; i++) s += "x";
    if (s.length > longest) longest = s.length;
  }
  return longest;
}

// BENCH_SCALE (default 1) scales the workload identically across every engine and
// is opaque to the optimizer, so the loops can't be constant-folded away.
const scale = parseInt(process.env.BENCH_SCALE ?? "1");
console.log("string_churn checksum: " + bench(800 * scale));
