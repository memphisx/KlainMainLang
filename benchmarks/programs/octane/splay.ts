// Allocator path: splay-tree churn — the canonical Octane/JetStream GC stressor,
// ported to typed TS. A splay tree of ~kTreeSize nodes is held live while a long
// insert/remove stream churns it: every insert allocates a fresh node plus a
// deep `Payload` tree (the garbage generator), and every remove drops one, so a
// large live set is continuously scanned while short-lived garbage is produced —
// the shape that separates a pause-free collector from a stop-the-world one.
// Splay rotations themselves mutate pointers in place, so the allocation is all
// in the payloads and node churn, exactly as in the original.
//
// Faithful to Octane's SplayTree/SplayUpdate, with one deliberate change: the
// PRNG is a deterministic LCG (not Math.random), so the checksum is identical
// across every engine — the suite doubles as a differential oracle and a
// diverging random stream would read as a false correctness mismatch.

// --- deterministic RNG ------------------------------------------------------
// A 31-bit LCG (glibc's constants). seed*a stays under i64 (< 2^61), so it never
// overflows the default number→i64 lowering; the mask keeps it 31-bit.
let seed = 49734321;
function rnd(): number {
  seed = (seed * 1103515245 + 12345) & 0x7fffffff;
  return seed;
}

const kKeyRange = 1000000000;
const kPayloadDepth = 5;

// --- payload: the per-insert garbage generator ------------------------------
class Payload {
  left: Payload | null;
  right: Payload | null;
  value: number;
  constructor(left: Payload | null, right: Payload | null, value: number) {
    this.left = left;
    this.right = right;
    this.value = value;
  }
}

function generatePayload(depth: number, tag: number): Payload | null {
  if (depth <= 0) return null;
  return new Payload(
    generatePayload(depth - 1, tag * 2),
    generatePayload(depth - 1, tag * 2 + 1),
    tag,
  );
}

// --- splay tree -------------------------------------------------------------
class SplayNode {
  key: number;
  payload: Payload | null;
  left: SplayNode | null;
  right: SplayNode | null;
  constructor(key: number, payload: Payload | null) {
    this.key = key;
    this.payload = payload;
    this.left = null;
    this.right = null;
  }
}

let root: SplayNode | null = null;

// Top-down splay (Sleator–Tarjan): move the node with `key` (or the last one on
// the search path) to the root by relinking, allocating nothing but the one
// reused header. Mirrors Octane's splay_.
function splay(key: number): void {
  if (root === null) return;
  const header = new SplayNode(0, null);
  let l = header;
  let r = header;
  let cur = root as SplayNode;
  while (true) {
    if (key < cur.key) {
      if (cur.left === null) break;
      if (key < (cur.left as SplayNode).key) {
        const tmp = cur.left as SplayNode;
        cur.left = tmp.right;
        tmp.right = cur;
        cur = tmp;
        if (cur.left === null) break;
      }
      r.left = cur;
      r = cur;
      cur = cur.left as SplayNode;
    } else if (key > cur.key) {
      if (cur.right === null) break;
      if (key > (cur.right as SplayNode).key) {
        const tmp = cur.right as SplayNode;
        cur.right = tmp.left;
        tmp.left = cur;
        cur = tmp;
        if (cur.right === null) break;
      }
      l.right = cur;
      l = cur;
      cur = cur.right as SplayNode;
    } else {
      break;
    }
  }
  l.right = cur.left;
  r.left = cur.right;
  cur.left = header.right;
  cur.right = header.left;
  root = cur;
}

function insert(key: number, payload: Payload | null): void {
  if (root === null) {
    root = new SplayNode(key, payload);
    return;
  }
  splay(key);
  const cur = root as SplayNode;
  if (cur.key === key) return; // already present — no-op
  const node = new SplayNode(key, payload);
  if (key > cur.key) {
    node.left = cur;
    node.right = cur.right;
    cur.right = null;
  } else {
    node.right = cur;
    node.left = cur.left;
    cur.left = null;
  }
  root = node;
}

function find(key: number): boolean {
  if (root === null) return false;
  splay(key);
  return (root as SplayNode).key === key;
}

// The greatest key strictly less than `key`, or -1 if none — the node
// SplayUpdate removes to keep the tree bounded.
function greatestLessThan(key: number): number {
  if (root === null) return -1;
  splay(key);
  const cur = root as SplayNode;
  if (cur.key < key) return cur.key;
  if (cur.left === null) return -1;
  let tmp = cur.left as SplayNode;
  while (tmp.right !== null) tmp = tmp.right as SplayNode;
  return tmp.key;
}

function remove(key: number): void {
  if (root === null) return;
  splay(key);
  const cur = root as SplayNode;
  if (cur.key !== key) return; // not present
  if (cur.left === null) {
    root = cur.right;
  } else {
    const right = cur.right;
    root = cur.left;
    splay(key); // splay the max of the left subtree to the root
    (root as SplayNode).right = right;
  }
}

// A fresh random key not already in the tree, inserted with a deep payload.
function insertNewNode(): number {
  let key = rnd() % kKeyRange;
  while (find(key)) key = rnd() % kKeyRange;
  insert(key, generatePayload(kPayloadDepth, key));
  return key;
}

function checksum(n: SplayNode | null): number {
  if (n === null) return 0;
  const node = n as SplayNode;
  const p = node.payload === null ? 0 : (node.payload as Payload).value;
  return (node.key % 100000) + p + checksum(node.left) + checksum(node.right);
}

// BENCH_SCALE (default 1) scales the churn identically across every engine and is
// opaque to the optimizer, so the loops can't be constant-folded away.
const scale = parseInt(process.env.BENCH_SCALE ?? "1");
const kTreeSize = 8000;
const kModifications = 40000 * scale;

// Build the live tree, then churn it: each round inserts a fresh keyed+payloaded
// node and removes the greatest node below it, holding the size near kTreeSize.
for (let i = 0; i < kTreeSize; i++) insertNewNode();
for (let i = 0; i < kModifications; i++) {
  const key = insertNewNode();
  const g = greatestLessThan(key);
  if (g < 0) remove(key);
  else remove(g);
}

console.log("splay checksum: " + checksum(root));
