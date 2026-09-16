// Self-referential structural types: an interface (or object-shape `type`
// alias) whose fields reference the type itself — the tree and linked-list
// shapes. A self-ref field captures a placeholder snapshot before the type's
// own fields exist; it is re-resolved on demand at each drilling access, so
// `root.children[0].children[0].val` and a full list traversal work.
// See ADR-00949.

function check(label: string, cond: boolean): void {
  console.log((cond ? "ok   " : "FAIL ") + label);
  if (!cond) throw new Error("assertion failed: " + label);
}

// 1. Self-referential interface array field (a tree).
interface TreeNode {
  children: TreeNode[];
  val: number;
}
const grandchild: TreeNode = { children: [], val: 3 };
const child: TreeNode = { children: [grandchild], val: 2 };
const root: TreeNode = { children: [child], val: 1 };
check("tree drills one level", root.children[0].val === 2);
check("tree drills two levels", root.children[0].children[0].val === 3);

// 2. Self-referential interface pointer field (a linked list) + traversal.
interface ListNode {
  next: ListNode | null;
  val: number;
}
const n3: ListNode = { next: null, val: 3 };
const n2: ListNode = { next: n3, val: 2 };
const n1: ListNode = { next: n2, val: 1 };
let cur: ListNode | null = n1;
let sum = 0;
while (cur !== null) {
  sum += cur.val;
  cur = cur.next;
}
check("linked-list traversal sums the whole chain", sum === 6);

// 3. Mutually-referential interfaces.
interface Parent {
  child: Child | null;
  name: number;
}
interface Child {
  parent: Parent | null;
  age: number;
}
const kid: Child = { parent: null, age: 7 };
const dad: Parent = { child: kid, name: 42 };
check("mutual recursion drills across types", dad.child!.age === 7);

// 4. Object-shape `type` alias, same self-reference power.
type Cons = { tail: Cons | null; head: number };
const t: Cons = { tail: null, head: 2 };
const h: Cons = { tail: t, head: 1 };
check("self-ref type alias drills", h.tail!.head === 2);

console.log("all recursive-type checks passed");
