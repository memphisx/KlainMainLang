// Allocator path: many small, short-lived heap objects (tree nodes) built and
// discarded in waves — the classic GC/allocator stressor. Sensitive to
// free-insertion quality (auto) and to a future reuse pass (the tree is rebuilt
// each stretch, so its nodes are prime reuse-in-place candidates).

class Node {
  left: Node | null;
  right: Node | null;
  constructor(left: Node | null, right: Node | null) {
    this.left = left;
    this.right = right;
  }
}

function make(depth: number): Node {
  if (depth === 0) return new Node(null, null);
  return new Node(make(depth - 1), make(depth - 1));
}

function check(node: Node): number {
  if (node.left === null) return 1;
  return 1 + check(node.left as Node) + check(node.right as Node);
}

// BENCH_SCALE (default 1) scales the workload identically across every engine and
// is opaque to the optimizer, so the loops can't be constant-folded away.
const scale = parseInt(process.env.BENCH_SCALE ?? "1");
const maxDepth = 16;
let total = 0;
for (let stretch = 0; stretch < 4 * scale; stretch++) {
  const t = make(maxDepth);
  total += check(t);
}
console.log("binary_trees checksum: " + total);
