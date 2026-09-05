// Allocator path: JSON.parse/stringify round-trips — each parse builds a fresh
// object tree and each stringify walks it into a fresh string, both discarded
// every iteration. Exercises the dynamic-object and string allocators together,
// a very common real-workload shape (config/request bodies) and a good cross-
// engine row since every runtime has a fast native JSON path.

interface Record {
  id: number;
  name: string;
  active: boolean;
  scores: number[];
}

function build(n: number): Record[] {
  const out: Record[] = [];
  for (let i = 0; i < n; i++) {
    out.push({ id: i, name: "row-" + i, active: i % 2 === 0, scores: [i, i * 2, i * 3] });
  }
  return out;
}

function bench(n: number, rounds: number): number {
  const data = build(n);
  let total = 0;
  for (let r = 0; r < rounds; r++) {
    const text = JSON.stringify(data);
    total += text.length;
    const parsed = JSON.parse(text) as Record[];
    total += parsed.length;
  }
  return total;
}

// BENCH_SCALE (default 1) scales the workload identically across every engine and
// is opaque to the optimizer, so the loops can't be constant-folded away.
const scale = parseInt(process.env.BENCH_SCALE ?? "1");
console.log("json_churn checksum: " + bench(800, 60 * scale));
