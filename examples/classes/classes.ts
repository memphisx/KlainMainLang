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
console.log(head instanceof Node);   // true
console.log(head instanceof Circle); // false
console.log(tail.nextNode instanceof Node); // false — a null field is never an instance of anything

// The any/unknown case is where instanceof does real runtime work: the
// concrete class isn't known until the tag is actually read back.
const shapeA: any = new Circle(5);
const shapeB: any = head;
console.log(shapeA instanceof Circle); // true
console.log(shapeA instanceof Node);   // false
console.log(shapeB instanceof Node);   // true
console.log(shapeB instanceof Circle); // false

// instanceof against a built-in type (ADR-00162) — a compile-time
// constant (this compiler's static typing already knows the answer; no
// runtime tag to check, unlike the user-defined-class case above).
const numbers: number[] = [1, 2, 3];
console.log(numbers instanceof Array); // true
console.log(shapeA instanceof Array);  // false — shapeA is a Circle, not an array

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
console.log(rect instanceof Shape);      // true — true through inheritance
console.log(rect instanceof Rectangle);  // true
console.log(rect instanceof RightTriangle); // false

const anyShape: any = new RightTriangle(3, 4);
console.log(anyShape instanceof Shape);        // true
console.log(anyShape instanceof RightTriangle); // true
console.log(anyShape instanceof Rectangle);     // false

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

// Getters/setters (TDD-00030). `get x()`/`set x(v)` are ordinary property
// syntax that routes through a method call instead of a plain field
// read/write — a getter can compute a value from other fields, a setter
// can derive a different backing field, and `obj.x`/`obj.x = v` both work
// exactly like a plain field would from the caller's own perspective.
class Temperature {
  private _celsius: number;
  constructor(c: number) {
    this._celsius = c;
  }
  get celsius(): number {
    return this._celsius;
  }
  set celsius(v: number) {
    this._celsius = v;
  }
  // A getter/setter pair need not share a backing field 1:1 — fahrenheit
  // has no field of its own at all, it's purely derived from celsius.
  get fahrenheit(): number {
    return (this._celsius * 9) / 5 + 32;
  }
  set fahrenheit(f: number) {
    this._celsius = ((f - 32) * 5) / 9;
  }
}
const temp = new Temperature(0);
console.log(temp.celsius);     // 0
console.log(temp.fahrenheit);  // 32
temp.fahrenheit = 212;
console.log(temp.celsius);     // 100
temp.celsius += 10;            // compound assignment: read via getter, write via setter
console.log(temp.celsius);     // 110

// Even internal `this.x` access (from a method other than the accessor's
// own body) goes through the getter/setter, matching real JS — falls out
// for free from `this` being an ordinary receiver, no special-casing.
class Counter {
  private count: number;
  constructor() {
    this.count = 0;
  }
  get value(): number {
    return this.count;
  }
  bump(): void {
    console.log(this.value); // reads through the getter
    this.count = this.count + 1;
  }
}
const counter = new Counter();
counter.bump(); // 0
counter.bump(); // 1
console.log(counter.value); // 2

// Getters/setters are ordinary, overridable methods under the hood — real
// inheritance/polymorphic dispatch through a base-typed parameter works
// exactly like it does for any other method (Stage 3 above).
class Shape3 {
  get label(): string {
    return "shape";
  }
}
class Circle2 extends Shape3 {
  get label(): string {
    return "circle";
  }
}
function showLabel(s: Shape3): string {
  return s.label;
}
console.log(showLabel(new Circle2())); // circle
console.log(showLabel(new Shape3()));  // shape

// TDD-00021: `#x` private names — real ECMAScript's own privacy mechanism
// (not TypeScript-specific like the `private` keyword modifier above). The
// access-control rule is identical to `private` (exact declaring-class
// only, no subclass access) — the real difference is reflection: a
// `#`-named field is genuinely invisible to Object.keys/JSON.stringify/
// spread, not just erased at the type level.
class BankAccount2 {
  #balance: number;
  constructor(balance: number) {
    this.#balance = balance;
  }
  #describeBalance(): string {
    return "balance is " + this.#balance.toString();
  }
  summary(): string {
    return this.#describeBalance();
  }
  deposit(amount: number): void {
    this.#balance = this.#balance + amount;
  }
}
const acct2 = new BankAccount2(100);
acct2.deposit(50);
console.log(acct2.summary()); // balance is 150

// Unlike `private`, `#balance` never shows up in reflection — real JS's
// `[object Object]`/JSON default only ever sees declared, non-private keys.
console.log(JSON.stringify(acct2)); // {}

// A `#x` name and a same-spelled `x` name are entirely different fields —
// no collision, matching real JS.
class Box2 {
  x: number;
  #x: number;
  constructor() {
    this.x = 1;
    this.#x = 2;
  }
  sum(): number {
    return this.x + this.#x;
  }
}
console.log(new Box2().sum()); // 3

// `#x` also works on `static` fields/methods and on get/set accessors —
// each independently combining with a feature `#x` itself doesn't change.
class IdPool {
  static #next: number;
  static take(): number {
    IdPool.#next = IdPool.#next + 1;
    return IdPool.#next;
  }
}
console.log(IdPool.take()); // 1
console.log(IdPool.take()); // 2

class PrivateRect {
  #w: number;
  #h: number;
  constructor(w: number, h: number) {
    this.#w = w;
    this.#h = h;
  }
  get area(): number {
    return this.#w * this.#h;
  }
}
console.log(new PrivateRect(3, 4).area); // 12

// TDD-00063 Stage 1: class field initializers. A field may carry an `= expr`
// default (`x = 5`, or annotated `n: number = 5`); it runs at construction
// time, in declaration order, right after super() in a derived class. A class
// whose every field initializes itself needs no explicit constructor at all.
class Config {
  retries = 3;
  timeout: number = 30;
  name = "default";
}
const cfg = new Config();
console.log(cfg.retries);          // 3
console.log(cfg.timeout);          // 30
console.log(cfg.name);             // default

// Initializers run before the constructor body, so a constructor assignment
// to the same field overrides the initializer (spec order: init, then body).
class Widget {
  id = 0;
  clicks = 0;
  constructor(id: number) {
    this.id = id; // overrides the `id = 0` initializer
  }
}
const w = new Widget(7);
console.log(w.id);                 // 7
console.log(w.clicks);             // 0 — from the initializer, no constructor assignment

// An initializer may reference `this` (an earlier field's already-set value).
class Circle3 {
  radius = 10;
  diameter = this.radius * 2;
}
console.log(new Circle3().diameter); // 20

// In a derived class, own field initializers run right after super() returns.
// Here Labeled has no explicit constructor, so the synthesized one forwards
// to super(...) and then runs `tag = "labeled"`.
class Base2 {
  kind: string;
  constructor(kind: string) {
    this.kind = kind;
  }
}
class Labeled extends Base2 {
  tag = "labeled";
}
const lab = new Labeled("box");
console.log(lab.kind);             // box
console.log(lab.tag);              // labeled

// Private fields (TDD-00021) may carry an initializer too — the syntax
// TDD-00021 itself deferred, now filled in.
class Ticker {
  #count = 0;
  tick(): number {
    this.#count = this.#count + 1;
    return this.#count;
  }
}
const ticker = new Ticker();
console.log(ticker.tick());        // 1
console.log(ticker.tick());        // 2

// TDD-00063 Stage 2a: async methods. `async` on a class method works exactly
// like an async top-level function — it returns a Promise<T> the caller
// awaits, and its body can `await` other async work (including another async
// method on the same instance via `this`). Static async methods work too.
// (Generator methods — `*m()` / `async *m()` — parse but are a clean
// "not yet supported" rejection; that's Stage 2b.)
class Account {
  private balance: number;
  constructor(balance: number) {
    this.balance = balance;
  }
  async currentBalance(): Promise<number> {
    return this.balance;
  }
  async deposit(amount: number): Promise<number> {
    const current = await this.currentBalance(); // await another async method via `this`
    this.balance = current + amount;
    return this.balance;
  }
  static async openWith(amount: number): Promise<Account> {
    return new Account(amount); // a static async factory
  }
}
const account = await Account.openWith(100);
console.log(await account.deposit(50));  // 150
console.log(await account.currentBalance()); // 150

// TDD-00063 Stage 2b: generator methods. A `*method()` on a class is a real
// generator — calling it constructs a generator instance (it does not run the
// body), and the body can `yield` values and read `this`. Drive it with
// for...of or explicit .next(). (Static and `async *` generator methods are a
// clean "not yet supported" rejection in V1.)
class NumberRange {
  private lo: number;
  private hi: number;
  constructor(lo: number, hi: number) {
    this.lo = lo;
    this.hi = hi;
  }
  *values(): number {
    for (let i = this.lo; i < this.hi; i = i + 1) {
      yield i;
    }
  }
}
const range = new NumberRange(1, 5);
for (const v of range.values()) {
  console.log(v); // 1 2 3 4
}
// Explicit .next() drive, including the {value, done} shape.
const it = new NumberRange(10, 12).values();
const step = it.next();
console.log(step.value); // 10
console.log(step.done);  // false
console.log(it.next().value); // 11
console.log(it.next().done);  // true

// TDD-00063 Stage 3: computed member names. A compile-time-constant string or
// numeric key in brackets is desugared to the equivalent named member —
// `['area']()` is exactly `area()`. (A dynamic key — an identifier, a call, a
// Symbol, or an interpolated template — is a clean rejection in V1, since
// member names are resolved statically here.)
class Metrics {
  private total: number = 0;
  ["record"](n: number): void {
    this.total = this.total + n;
  }
  get ["sum"](): number {
    return this.total;
  }
}
const metrics = new Metrics();
metrics.record(3);   // called by the plain name the computed key desugars to
metrics.record(4);
console.log(metrics.sum); // 7

// TDD-00063 Stage 4: class expressions. `class { ... }` in expression position
// binds a nominal class under the left-hand-side name — classes are
// compile-time types here (not first-class runtime values), so V1 supports a
// class expression only as a top-level `const/let/var X = class {...}` binding
// (usable as `new X()` and as a type annotation, inheriting every Stage 1-3
// member). A class expression used as a value — an argument, a return, or a
// nested binding — is a clean rejection.
const Accumulator = class {
  private total: number = 0;
  private count: number = 0;
  add(n: number): void {
    this.total = this.total + n;
    this.count = this.count + 1;
  }
  average(): number {
    return this.total / this.count;
  }
};
const acc = new Accumulator();
acc.add(10);
acc.add(20);
console.log(acc.average()); // 15

// A named class expression binds under the LHS name (the internal name is
// dropped in V1); either way the class is usable as a type annotation.
const Money = class Currency {
  cents: number;
  constructor(cents: number) {
    this.cents = cents;
  }
};
function dollars(m: Money): number {
  return m.cents / 100;
}
console.log(dollars(new Money(500))); // 5
