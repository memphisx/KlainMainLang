// Arrays as reference values living inside other containers: Map values,
// Promise combinator/`.then` results, self-referential class array fields, and
// nested named-object array types, and reference-type Map keys / Set elements.
// Each of these previously produced invalid IR or a spurious type error before
// arrays/objects became header-backed reference values (TDD-00213); this example
// exercises all of them and self-checks.
// See ADR-00943 / ADR-00944 / ADR-00945 / ADR-00946 / ADR-00947 / ADR-00948.

function check(label: string, cond: boolean): void {
  console.log((cond ? "ok   " : "FAIL ") + label);
  if (!cond) throw new Error("assertion failed: " + label);
}

// 1. Map<K, T[]> — the array value round-trips AND keeps reference identity.
const groups = new Map<string, number[]>();
const evens = [2, 4, 6];
groups.set("evens", evens);
groups.set("empty", []);
const got = groups.get("evens")!;
check("map array value round-trips", got.length === 3 && got[0] === 2);
check("map array value is the same reference", got === evens);
evens.push(8);
check("mutation is visible through the map", groups.get("evens")!.length === 4);
check("map miss is undefined", groups.get("nope") === undefined);

// 2. Promise.all over Promise<T[]> members — a T[][] result.
async function row(n: number): Promise<number[]> {
  return [n, n + 1, n + 2];
}

async function run(): Promise<void> {
  const grid = await Promise.all([row(1), row(10)]);
  check("Promise.all<T[]> shape", grid.length === 2 && grid[0][2] === 3 && grid[1][1] === 11);

  // 3. `.then` with an array argument and an array return.
  await Promise.resolve([5, 6]).then((a) => a.map((x) => x * 10)).then((a2) => {
    check(".then array arg + array return", a2[0] === 50 && a2[1] === 60);
  });
}

// 4. Self-referential class array field (the tree shape).
class TreeNode {
  children: TreeNode[] = [];
  val: number = 0;
}
const root = new TreeNode();
root.val = 1;
const child = new TreeNode();
child.val = 2;
const grandchild = new TreeNode();
grandchild.val = 3;
child.children.push(grandchild);
root.children.push(child);
check(
  "self-ref class array field drills",
  root.children[0].val === 2 && root.children[0].children[0].val === 3,
);

// 5. Nested array of a named object type (`Point[][]`).
interface Point {
  x: number;
  y: number;
}
const board: Point[][] = [[{ x: 0, y: 0 }], [{ x: 1, y: 2 }, { x: 3, y: 4 }]];
check("nested named-object array", board[1][1].x === 3 && board[1][0].y === 2);

// 6. Reference-type Map keys / Set elements (ADR-00948): an object/array key
// carries reference identity — the same reference round-trips, a distinct
// reference of equal content is a different key. Iteration surfaces the live
// references. (Previously invalid IR: a non-scalar key stored raw into the
// numeric runtime's i64 key slot.)
const seen = new Map<Point, string>();
const origin = { x: 0, y: 0 };
seen.set(origin, "origin");
check(
  "object map key identity",
  seen.get(origin) === "origin" &&
    seen.has(origin) &&
    !seen.has({ x: 0, y: 0 }),
);
let keySum = 0;
for (const k of seen.keys()) keySum += k.x + k.y;
check("object map key iteration surfaces the live object", keySum === 0);

const nodes = new Set<Point>();
nodes.add(origin);
nodes.add({ x: 1, y: 1 });
nodes.add(origin);
check("object set element identity", nodes.size === 2 && nodes.has(origin));

run();
