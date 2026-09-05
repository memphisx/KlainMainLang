// Allocator path: closure environments. Each makeAdder call heap-allocates a
// {fn, env} pair plus the captured cell. The closures escape into an array (so
// the optimizer can't elide the allocation), then the array is discarded each
// round — bounded live-set, heavy churn. This is the shape where -mm=manual
// leaks every round while gc/auto reclaim between them.
//
// The `* process.argv.length` factor is always 1 at run time but opaque to the
// optimizer, so the loops can't be constant-folded away.

function makeAdder(x: number): () => number {
  return () => x * 2 + 1;
}

function bench(n: number, rounds: number): number {
  let sum = 0;
  for (let r = 0; r < rounds; r++) {
    const fns: Array<() => number> = [];
    for (let i = 0; i < n; i++) fns.push(makeAdder(i));
    for (let i = 0; i < fns.length; i++) sum += fns[i]();
  }
  return sum;
}

// BENCH_SCALE (default 1) scales the workload identically across every engine and
// is opaque to the optimizer, so the loops can't be constant-folded away.
const scale = parseInt(process.env.BENCH_SCALE ?? "1");
console.log("closure_churn checksum: " + bench(15000, 40 * scale));
