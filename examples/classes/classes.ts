class Point {
  x: number;
  y: number;

  constructor(x: number, y: number) {
    this.x = x;
    this.y = y;
  }

  squaredLength(): number {
    return this.x * this.x + this.y * this.y;
  }

  length(): number {
    return Math.floor(Math.sqrt(this.squaredLength()));
  }

  moveBy(dx: number, dy: number): void {
    this.x = this.x + dx;
    this.y = this.y + dy;
  }
}

const p = new Point(3, 4);
console.log(p.length());       // 5
p.moveBy(3, 4);
console.log(p.x);              // 6
console.log(p.y);              // 8
console.log(new Point(6, 8).length()); // 10 — new ClassName(...) chained directly into a method call

// Two instances keep independent state.
const a = new Point(0, 0);
const b = new Point(0, 0);
a.moveBy(1, 0);
console.log(a.x);              // 1
console.log(b.x);               // 0

// A class instance works as an ordinary function parameter/return value —
// no special casing needed beyond the shared object machinery.
function midpoint(p1: Point, p2: Point): Point {
  return new Point((p1.x + p2.x) / 2, (p1.y + p2.y) / 2);
}
const m = midpoint(new Point(0, 0), new Point(10, 20));
console.log(m.x);               // 5
console.log(m.y);               // 10

// A methods-only class needs no constructor at all.
class Utils {
  double(n: number): number {
    return n * 2;
  }
}
console.log(new Utils().double(21)); // 42

// Stage 1a: for...of over a class implementing a sentinel next(): T | null
// iterator method — dispatched structurally at compile time, no runtime
// Symbol.iterator lookup.
class Range {
  current: number;
  max: number;
  constructor(start: number, max: number) {
    this.current = start;
    this.max = max;
  }
  next(): number | null {
    if (this.current >= this.max) {
      return null;
    }
    const v = this.current;
    this.current = this.current + 1;
    return v;
  }
}
for (const n of new Range(1, 6)) {
  console.log(n); // 1 2 3 4 5
}

// The same protocol works for an object-element iterator too (a real null,
// not the 0-ambiguous numeric sentinel) — e.g. walking a linked list.
class Node {
  value: number;
  nextNode: Node | null;
  constructor(value: number, nextNode: Node | null) {
    this.value = value;
    this.nextNode = nextNode;
  }
}
class NodeIter {
  cursor: Node | null;
  constructor(head: Node | null) {
    this.cursor = head;
  }
  next(): Node | null {
    const n = this.cursor;
    if (n === null) {
      return null;
    }
    this.cursor = n.nextNode;
    return n;
  }
}
const tail = new Node(3, null);
const mid = new Node(2, tail);
const head = new Node(1, mid);
for (const n of new NodeIter(head)) {
  console.log(n.value); // 1 2 3
}

// Stage 2: runtime type tags + instanceof. Every instance carries a hidden
// tag identifying its class, so instanceof can tell two classes apart even
// through an any-typed value — the one case where the check does real work,
// since a statically class-typed variable's concrete class is already known
// at compile time before inheritance exists.
class Circle {
  r: number;
  constructor(r: number) {
    this.r = r;
  }
}
console.log(head instanceof Node);   // 1 (true)
console.log(head instanceof Circle); // 0 (false)
console.log(tail.nextNode instanceof Node); // 0 — a null field is never an instance of anything

// The any/unknown case is where instanceof does real runtime work: the
// concrete class isn't known until the tag is actually read back.
const shapeA: any = new Circle(5);
const shapeB: any = head;
console.log(shapeA instanceof Circle); // 1
console.log(shapeA instanceof Node);   // 0
console.log(shapeB instanceof Node);   // 1
console.log(shapeB instanceof Circle); // 0
