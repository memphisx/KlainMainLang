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

// Stage 3: inheritance (extends/super) + dynamic dispatch. Shape's own
// area() is overridden by both Rectangle and RightTriangle; describe() —
// declared once on Shape, never overridden — calls this.area() and must
// resolve to whichever concrete override actually applies, even when called
// through a Shape-typed parameter that doesn't statically know which
// subclass it's holding.
class Shape {
  label: string;
  constructor(label: string) {
    this.label = label;
  }
  area(): number {
    return 0;
  }
  describe(): string {
    return this.label + " has area " + this.area().toString();
  }
}

class Rectangle extends Shape {
  width: number;
  height: number;
  constructor(width: number, height: number) {
    super("rectangle");
    this.width = width;
    this.height = height;
  }
  area(): number {
    return this.width * this.height;
  }
}

class RightTriangle extends Shape {
  base: number;
  height: number;
  constructor(base: number, height: number) {
    super("triangle");
    this.base = base;
    this.height = height;
  }
  area(): number {
    return Math.floor((this.base * this.height) / 2);
  }
  // super.method(): explicitly extend rather than replace the base's own
  // behavior — the vtable dispatch describe() itself uses is bypassed here
  // on purpose, since this call already knows exactly which implementation
  // it wants.
  describe(): string {
    return super.describe() + " (a right triangle)";
  }
}

function printShape(s: Shape): void {
  // s's static type is Shape — area() is only resolved to the right
  // concrete override at runtime, via each instance's own vtable.
  console.log(s.describe());
}
printShape(new Rectangle(3, 4));      // rectangle has area 12
printShape(new RightTriangle(6, 5));  // triangle has area 15 (a right triangle)

const rect: Shape = new Rectangle(2, 5);
console.log(rect instanceof Shape);      // 1 — true through inheritance
console.log(rect instanceof Rectangle);  // 1
console.log(rect instanceof RightTriangle); // 0

const anyShape: any = new RightTriangle(3, 4);
console.log(anyShape instanceof Shape);        // 1
console.log(anyShape instanceof RightTriangle); // 1
console.log(anyShape instanceof Rectangle);     // 0

// Stage 4: static members + static {} blocks. A static field/method
// belongs to the class itself, not an instance — no `this`, accessed only
// via ClassName.member. static {} runs once, before any top-level
// statement, the closest thing this compiler has to a field initializer.
class Registry {
  static count: number;
  static {
    Registry.count = 0;
  }
  static register(): number {
    Registry.count = Registry.count + 1;
    return Registry.count;
  }
}
console.log(Registry.register()); // 1
console.log(Registry.register()); // 2

// Stage 4: private/protected — compile-time-only visibility, matching real
// TypeScript's own erasure (no runtime check ever emitted). A private
// member is only accessible from inside its own declaring class; protected
// additionally allows subclasses.
class BankAccount {
  private balance: number;
  protected owner: string;
  constructor(balance: number, owner: string) {
    this.balance = balance;
    this.owner = owner;
  }
  private describeBalance(): string {
    return "balance is " + this.balance.toString();
  }
  summary(): string {
    return this.describeBalance() + " (owner: " + this.owner + ")";
  }
}
class NamedSavingsAccount extends BankAccount {
  constructor(balance: number, owner: string) {
    super(balance, owner);
  }
  // `owner` is protected on BankAccount — readable from a subclass, unlike
  // `balance`, which is private and only readable inside BankAccount itself.
  greetOwner(): string {
    return "Hello, " + this.owner;
  }
}
const acct = new BankAccount(100, "Alice");
console.log(acct.summary()); // balance is 100 (owner: Alice)
const savings = new NamedSavingsAccount(50, "Bob");
console.log(savings.greetOwner()); // Hello, Bob

// Stage 4: abstract classes/methods — compile-time-only, reusing Stage 3's
// override machinery: an abstract method behaves exactly like an
// overridden virtual method the moment any concrete subclass provides a
// real implementation, with zero new dispatch logic. `new Shape2()` itself
// would be a compile error (no direct instances of an abstract class), and
// so would a concrete subclass that forgot to override area().
abstract class Shape2 {
  abstract area(): number;
  describe(): string {
    return "area is " + this.area().toString();
  }
}
class Square extends Shape2 {
  side: number;
  constructor(side: number) {
    this.side = side;
  }
  area(): number {
    return this.side * this.side;
  }
}
console.log(new Square(4).describe()); // area is 16

// Stage 4: implements — a compile-time-only self-check that a class
// already has the shape an interface declares (fields and method
// signatures). Purely declarative: it doesn't make Greeter-typed variables
// polymorphically dispatch to a class's methods, just validates Dog's own
// shape against Greeter at declaration time.
interface Greeter {
  name: string;
  greet(): string;
}
class Dog implements Greeter {
  name: string;
  constructor(name: string) {
    this.name = name;
  }
  greet(): string {
    return "Woof, I am " + this.name;
  }
}
console.log(new Dog("Rex").greet()); // Woof, I am Rex
