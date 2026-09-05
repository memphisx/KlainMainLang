// Allocator path: red-black tree insertion — the canonical Perceus/Koka
// benchmark. Every insert rebuilds the nodes along the path, so it allocates
// heavily and is the textbook target for reuse-in-place (a rebuilt node of the
// same shape as the one just dropped). A good yardstick for a future reuse pass:
// the gap between this compiler's modes and a GC'd engine here is the prize.

const RED = 0;
const BLACK = 1;

class Node {
  color: number;
  key: number;
  value: boolean;
  left: Node | null;
  right: Node | null;
  constructor(color: number, key: number, value: boolean, left: Node | null, right: Node | null) {
    this.color = color;
    this.key = key;
    this.value = value;
    this.left = left;
    this.right = right;
  }
}

function isRed(n: Node | null): boolean {
  return n !== null && (n as Node).color === RED;
}

// Full Okasaki balance — all four red-red cases. A one-case version
// degenerated to an O(N)-depth tree on sequential keys (O(N^2) allocation,
// stack overflow under Node's default stack), defeating the benchmark.
function balance(color: number, key: number, value: boolean, left: Node | null, right: Node | null): Node {
  if (color === BLACK) {
    if (isRed(left) && isRed((left as Node).left)) {
      const l = left as Node; const ll = l.left as Node;
      return new Node(RED, l.key, l.value,
        new Node(BLACK, ll.key, ll.value, ll.left, ll.right),
        new Node(BLACK, key, value, l.right, right));
    }
    if (isRed(left) && isRed((left as Node).right)) {
      const l = left as Node; const lr = l.right as Node;
      return new Node(RED, lr.key, lr.value,
        new Node(BLACK, l.key, l.value, l.left, lr.left),
        new Node(BLACK, key, value, lr.right, right));
    }
    if (isRed(right) && isRed((right as Node).left)) {
      const r = right as Node; const rl = r.left as Node;
      return new Node(RED, rl.key, rl.value,
        new Node(BLACK, key, value, left, rl.left),
        new Node(BLACK, r.key, r.value, rl.right, r.right));
    }
    if (isRed(right) && isRed((right as Node).right)) {
      const r = right as Node; const rr = r.right as Node;
      return new Node(RED, r.key, r.value,
        new Node(BLACK, key, value, left, r.left),
        new Node(BLACK, rr.key, rr.value, rr.left, rr.right));
    }
  }
  return new Node(color, key, value, left, right);
}

function ins(n: Node | null, key: number, value: boolean): Node {
  if (n === null) return new Node(RED, key, value, null, null);
  const node = n as Node;
  if (key < node.key) return balance(node.color, node.key, node.value, ins(node.left, key, value), node.right);
  if (key > node.key) return balance(node.color, node.key, node.value, node.left, ins(node.right, key, value));
  return new Node(node.color, node.key, value, node.left, node.right);
}

function insert(root: Node | null, key: number, value: boolean): Node {
  const n = ins(root, key, value);
  return new Node(BLACK, n.key, n.value, n.left, n.right);
}

function countTrue(n: Node | null): number {
  if (n === null) return 0;
  const node = n as Node;
  return (node.value ? 1 : 0) + countTrue(node.left) + countTrue(node.right);
}

// BENCH_SCALE (default 1) scales the workload identically across every engine and
// is opaque to the optimizer, so the loops can't be constant-folded away.
const scale = parseInt(process.env.BENCH_SCALE ?? "1");
const n = 40000 * scale;
let root: Node | null = null;
for (let i = 0; i < n; i++) {
  root = insert(root, i, i % 10 === 0);
}
console.log("rbtree checksum: " + countTrue(root));
