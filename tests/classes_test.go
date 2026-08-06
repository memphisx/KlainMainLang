package tests

import (
	"strings"
	"testing"
)

// --- TDD-00009 Stage 1: methods, constructors, this, new ClassName(args) ---

func TestE2EClassFieldsConstructorMethod(t *testing.T) {
	assertOutput(t, `
class Point {
  x: number;
  y: number;
  constructor(x: number, y: number) {
    this.x = x;
    this.y = y;
  }
  distanceFromOrigin(): number {
    return Math.floor(Math.sqrt(this.x * this.x + this.y * this.y))
  }
}
const p = new Point(3, 4);
console.log(p.distanceFromOrigin())
`, "5")
}

func TestE2EClassMethodMutatesField(t *testing.T) {
	assertOutput(t, `
class Point {
  x: number;
  y: number;
  constructor(x: number, y: number) {
    this.x = x;
    this.y = y;
  }
  moveBy(dx: number, dy: number): void {
    this.x = this.x + dx;
    this.y = this.y + dy;
  }
}
const p = new Point(1, 1);
p.moveBy(2, 3);
console.log(p.x)
console.log(p.y)
`, "3\n4")
}

func TestE2EClassMethodCallsAnotherMethod(t *testing.T) {
	assertOutput(t, `
class Point {
  x: number;
  y: number;
  constructor(x: number, y: number) {
    this.x = x;
    this.y = y;
  }
  squaredLength(): number {
    return this.x * this.x + this.y * this.y
  }
  length(): number {
    return Math.floor(Math.sqrt(this.squaredLength()))
  }
}
console.log(new Point(3, 4).length())
`, "5")
}

func TestE2ETwoInstancesIndependentState(t *testing.T) {
	assertOutput(t, `
class Counter {
  count: number;
  constructor(start: number) {
    this.count = start;
  }
  increment(): void {
    this.count = this.count + 1;
  }
}
const a = new Counter(0);
const b = new Counter(100);
a.increment();
a.increment();
b.increment();
console.log(a.count)
console.log(b.count)
`, "2\n101")
}

func TestE2EClassInstanceAsFunctionParamAndReturn(t *testing.T) {
	assertOutput(t, `
class Point {
  x: number;
  y: number;
  constructor(x: number, y: number) {
    this.x = x;
    this.y = y;
  }
}
function midpoint(a: Point, b: Point): Point {
  return new Point((a.x + b.x) / 2, (a.y + b.y) / 2);
}
const m = midpoint(new Point(0, 0), new Point(10, 20));
console.log(m.x)
console.log(m.y)
`, "5\n10")
}

func TestE2ENewExpressionChainedMethodCall(t *testing.T) {
	assertOutput(t, `
class Point {
  x: number;
  y: number;
  constructor(x: number, y: number) {
    this.x = x;
    this.y = y;
  }
  length(): number {
    return Math.floor(Math.sqrt(this.x * this.x + this.y * this.y))
  }
}
console.log(new Point(6, 8).length())
`, "10")
}

func TestE2EZeroFieldClassNoConstructor(t *testing.T) {
	assertOutput(t, `
class Utils {
  double(n: number): number { return n * 2 }
}
const u = new Utils();
console.log(u.double(21))
`, "42")
}

func TestE2EClassFieldsWithoutConstructorIsError(t *testing.T) {
	_, err := parseAndCompile(`
class Foo {
  x: number;
}
`)
	if err == nil {
		t.Fatal("expected a compile error for a class with fields but no constructor")
	}
	if !strings.Contains(err.Error(), "has fields but no constructor") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestE2EDuplicateMethodNameIsError(t *testing.T) {
	_, err := parseAndCompile(`
class Foo {
  greet(): void { console.log("a") }
  greet(): void { console.log("b") }
}
`)
	if err == nil {
		t.Fatal("expected a compile error for two methods with the same name")
	}
	if !strings.Contains(err.Error(), "more than one method named") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestE2ESelfReferentialClassField(t *testing.T) {
	// A class field typed as the class itself (or a chain of them) is the
	// classic linked-list/tree shape. Also exercises destructuring a
	// class-typed field into a local binding and drilling further into it —
	// see docs/adr/ADR-00064.md for the bug this guards against.
	assertOutput(t, `
class Node {
  value: number;
  nextNode: Node | null;
  constructor(value: number, nextNode: Node | null) {
    this.value = value;
    this.nextNode = nextNode;
  }
}
const c = new Node(3, null);
const b = new Node(2, c);
const a = new Node(1, b);
console.log(a.nextNode.nextNode.value)
const { nextNode } = b;
console.log(nextNode.value)
`, "3\n3")
}

// --- TDD-00009 Stage 1a: for...of over a class-based iterator ---

func TestE2EForOfClassIteratorNumeric(t *testing.T) {
	assertOutput(t, `
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
for (const x of new Range(1, 6)) {
  console.log(x)
}
`, "1\n2\n3\n4\n5")
}

func TestE2EForOfClassIteratorObjectElement(t *testing.T) {
	assertOutput(t, `
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
const c = new Node(3, null);
const b = new Node(2, c);
const a = new Node(1, b);
for (const n of new NodeIter(a)) {
  console.log(n.value)
}
`, "1\n2\n3")
}

// --- TDD-00009 Stage 2: runtime type tags + instanceof ---

func TestE2EInstanceOfStaticTrue(t *testing.T) {
	assertOutput(t, `
class Point {
  x: number;
  constructor(x: number) {
    this.x = x;
  }
}
const p = new Point(1);
console.log(p instanceof Point)
`, "1")
}

func TestE2EInstanceOfStaticFalseDifferentClass(t *testing.T) {
	assertOutput(t, `
class Point {
  x: number;
  constructor(x: number) {
    this.x = x;
  }
}
class Circle {
  r: number;
  constructor(r: number) {
    this.r = r;
  }
}
const c = new Circle(2);
console.log(c instanceof Point)
`, "0")
}

func TestE2EInstanceOfNullableClassReducesToNullCheck(t *testing.T) {
	assertOutput(t, `
class Node {
  value: number;
  nextNode: Node | null;
  constructor(value: number, nextNode: Node | null) {
    this.value = value;
    this.nextNode = nextNode;
  }
}
const tail = new Node(2, null);
const head = new Node(1, tail);
console.log(head.nextNode instanceof Node)
console.log(tail.nextNode instanceof Node)
`, "1\n0")
}

func TestE2EInstanceOfAnyNarrowsBetweenClasses(t *testing.T) {
	assertOutput(t, `
class Point {
  x: number;
  constructor(x: number) {
    this.x = x;
  }
}
class Circle {
  r: number;
  constructor(r: number) {
    this.r = r;
  }
}
const p: any = new Point(1);
const c: any = new Circle(2);
console.log(p instanceof Point)
console.log(p instanceof Circle)
console.log(c instanceof Circle)
console.log(c instanceof Point)
`, "1\n0\n1\n0")
}

func TestE2EInstanceOfReservedFieldNameIsError(t *testing.T) {
	_, err := parseAndCompile(`
class Foo {
  __kml_tag: number;
  constructor(__kml_tag: number) {
    this.__kml_tag = __kml_tag;
  }
}
`)
	if err == nil {
		t.Fatal("expected a compile error for a field named __kml_tag")
	}
	if !strings.Contains(err.Error(), "reserved for the compiler's internal runtime type tag") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestE2EInstanceOfUnknownClassIsError(t *testing.T) {
	_, err := parseAndCompile(`
class Foo {
  x: number;
  constructor(x: number) {
    this.x = x;
  }
}
const f = new Foo(1);
console.log(f instanceof Bar)
`)
	if err == nil {
		t.Fatal("expected a compile error for instanceof against an unregistered class")
	}
	if !strings.Contains(err.Error(), "not a registered class") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// TestE2EClassTagDoesNotLeakIntoReflection guards against the hidden
// instanceof tag field (always struct index 0 on a class instance) leaking
// out as a fake user-visible field through any of the reflection APIs that
// enumerate an object's fields — see docs/tdd/TDD-00009.md Stage 2 and the
// VisibleFields() call sites this depends on.
func TestE2EClassTagDoesNotLeakIntoReflection(t *testing.T) {
	assertOutput(t, `
class Point {
  x: number;
  y: number;
  constructor(x: number, y: number) {
    this.x = x;
    this.y = y;
  }
}
class Empty {
  greet(): string { return "hi" }
}
const p = new Point(1, 2);
console.log(Object.keys(p).join(","))
console.log(Object.values(p).join(","))
for (const k in p) {
  console.log(k)
}
console.log(JSON.stringify(p))
const clone = { ...p };
console.log(Object.keys(clone).join(","))
const target = { x: 0, y: 0 };
Object.assign(target, p);
console.log(target.x)
console.log(target.y)
const e = new Empty();
console.log(Object.keys(e).length)
`, "x,y\n1,2\nx\ny\n{\"x\":1,\"y\":2}\nx,y\n1\n2\n0")
}

// --- TDD-00009 Stage 3: extends, super, dynamic dispatch ---

func TestE2EClassInheritsFieldsAndMethods(t *testing.T) {
	assertOutput(t, `
class Animal {
  name: string;
  constructor(name: string) {
    this.name = name;
  }
  speak(): string {
    return this.name + " makes a sound";
  }
}
class Dog extends Animal {
}
const d = new Dog("Rex");
console.log(d.name)
console.log(d.speak())
`, "Rex\nRex makes a sound")
}

func TestE2EClassMethodOverrideDirectCall(t *testing.T) {
	assertOutput(t, `
class Animal {
  speak(): string { return "..." }
}
class Dog extends Animal {
  speak(): string { return "bark" }
}
const d = new Dog();
console.log(d.speak())
`, "bark")
}

// TestE2EClassMethodOverrideDynamicDispatch is the core Stage 3 correctness
// case: a base-typed parameter holding a subclass instance must call the
// subclass's override, not the base's own implementation, even though the
// call site only knows the base's static type.
func TestE2EClassMethodOverrideDynamicDispatch(t *testing.T) {
	assertOutput(t, `
class Shape {
  area(): number { return 0 }
}
class Circle extends Shape {
  radius: number;
  constructor(radius: number) {
    this.radius = radius;
  }
  area(): number { return 3 * this.radius * this.radius }
}
class Square extends Shape {
  side: number;
  constructor(side: number) {
    this.side = side;
  }
  area(): number { return this.side * this.side }
}
function printArea(s: Shape): void {
  console.log(s.area())
}
printArea(new Circle(2));
printArea(new Square(3));
`, "12\n9")
}

// TestE2EClassSiblingWithNoOverrideStaysDirectCall covers a sibling class in
// the same (virtual-dispatch-needing) tree that does NOT itself override
// the method — it must still resolve to the base's own implementation via
// the shared vtable slot, not error or read garbage.
func TestE2EClassSiblingWithNoOverrideStaysDirectCall(t *testing.T) {
	assertOutput(t, `
class Shape {
  label: string;
  constructor(label: string) {
    this.label = label;
  }
  area(): number { return 0 }
  describe(): string { return this.label + ":" + this.area() }
}
class Circle extends Shape {
  area(): number { return 99 }
}
class Blob extends Shape {
}
function printIt(s: Shape): void {
  console.log(s.describe())
}
printIt(new Circle("circle"));
printIt(new Blob("blob"));
`, "circle:99\nblob:0")
}

// TestE2EClassThreeLevelHierarchyDispatch checks dispatch through every
// static level of a 3-class chain when only the leaf overrides.
func TestE2EClassThreeLevelHierarchyDispatch(t *testing.T) {
	assertOutput(t, `
class Base {
  value(): number { return 1 }
}
class Mid extends Base {
}
class Leaf extends Mid {
  value(): number { return 999 }
}
function viaBase(x: Base): void { console.log(x.value()) }
function viaMid(x: Mid): void { console.log(x.value()) }
const leaf = new Leaf();
viaBase(leaf);
viaMid(leaf);
console.log(leaf.value())
`, "999\n999\n999")
}

func TestE2EClassSuperConstructorCall(t *testing.T) {
	assertOutput(t, `
class Base {
  x: number;
  constructor(x: number) {
    this.x = x;
  }
}
class Derived extends Base {
  y: number;
  constructor(x: number, y: number) {
    super(x);
    this.y = y;
  }
}
const d = new Derived(1, 2);
console.log(d.x)
console.log(d.y)
`, "1\n2")
}

// TestE2EClassImplicitPassThroughConstructor covers a derived class that
// adds no fields of its own and declares no explicit constructor: it must
// get a synthesized constructor forwarding every argument to super(...).
func TestE2EClassImplicitPassThroughConstructor(t *testing.T) {
	assertOutput(t, `
class Base {
  x: number;
  y: number;
  constructor(x: number, y: number) {
    this.x = x;
    this.y = y;
  }
}
class Derived extends Base {
}
const d = new Derived(3, 4);
console.log(d.x)
console.log(d.y)
`, "3\n4")
}

func TestE2EClassSuperMethodCall(t *testing.T) {
	assertOutput(t, `
class Animal {
  name: string;
  constructor(name: string) {
    this.name = name;
  }
  speak(): string { return this.name + " makes a sound" }
}
class Dog extends Animal {
  speak(): string { return super.speak() + ", specifically a bark" }
}
const d = new Dog("Rex");
console.log(d.speak())
`, "Rex makes a sound, specifically a bark")
}

func TestE2EInstanceOfStaticThroughInheritance(t *testing.T) {
	assertOutput(t, `
class Shape { }
class Circle extends Shape { }
class Square extends Shape { }
const c = new Circle();
console.log(c instanceof Shape)
console.log(c instanceof Circle)
console.log(c instanceof Square)
`, "1\n1\n0")
}

// TestE2EInstanceOfStaticAncestorTypedHoldingDescendant guards against a
// real bug found while building the inheritance example: a variable
// statically typed as an ancestor class (e.g. `const s: Shape = new
// Circle(...)`) but holding a concrete descendant instance must still
// answer `s instanceof Circle` correctly at runtime — this is NOT decidable
// from the static type alone (unlike case 2's "T is C or an ancestor of C"
// shape), so it needs an actual tag read, not a compile-time constant.
func TestE2EInstanceOfStaticAncestorTypedHoldingDescendant(t *testing.T) {
	assertOutput(t, `
class Shape { }
class Circle extends Shape { }
class Square extends Shape { }
const s: Shape = new Circle();
console.log(s instanceof Shape)
console.log(s instanceof Circle)
console.log(s instanceof Square)
`, "1\n1\n0")
}

func TestE2EInstanceOfDynamicThroughInheritance(t *testing.T) {
	assertOutput(t, `
class Shape { }
class Circle extends Shape { }
class Square extends Shape { }
const anyVal: any = new Square();
console.log(anyVal instanceof Shape)
console.log(anyVal instanceof Square)
console.log(anyVal instanceof Circle)
`, "1\n1\n0")
}

func TestE2EClassMissingSuperCallIsError(t *testing.T) {
	_, err := parseAndCompile(`
class Base {
  x: number;
  constructor(x: number) { this.x = x; }
}
class Derived extends Base {
  y: number;
  constructor(y: number) { this.y = y; }
}
`)
	if err == nil {
		t.Fatal("expected a compile error for a derived constructor missing super(...)")
	}
	if !strings.Contains(err.Error(), "must call super(...)") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestE2EClassExtendsUnknownClassIsError(t *testing.T) {
	_, err := parseAndCompile(`
class Derived extends Ghost {
}
`)
	if err == nil {
		t.Fatal("expected a compile error for extending an unknown class")
	}
	if !strings.Contains(err.Error(), "extends unknown class") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestE2EClassExtendsBuiltinIsError(t *testing.T) {
	_, err := parseAndCompile(`
class Derived extends Error {
}
`)
	if err == nil {
		t.Fatal("expected a compile error for extending a built-in")
	}
	if !strings.Contains(err.Error(), "extends unknown class") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestE2EClassOverrideIncompatibleSignatureIsError(t *testing.T) {
	_, err := parseAndCompile(`
class Base {
  f(): number { return 1 }
}
class Derived extends Base {
  f(): string { return "x" }
}
`)
	if err == nil {
		t.Fatal("expected a compile error for an incompatible override signature")
	}
	if !strings.Contains(err.Error(), "incompatible signature") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestE2EClassCollidingFieldNameIsError(t *testing.T) {
	_, err := parseAndCompile(`
class Base {
  x: number;
  constructor(x: number) { this.x = x; }
}
class Derived extends Base {
  x: number;
  constructor(x: number) { super(x); this.x = x; }
}
`)
	if err == nil {
		t.Fatal("expected a compile error for a field colliding with an inherited one")
	}
	if !strings.Contains(err.Error(), "redeclares inherited field") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// --- TDD-00009 Stage 4: static, private/protected, abstract, implements ---

func TestE2EClassStaticFieldAndMethod(t *testing.T) {
	assertOutput(t, `
class Counter {
  static count: number;
  static { Counter.count = 0; }
  static increment(): number {
    Counter.count = Counter.count + 1;
    return Counter.count;
  }
}
console.log(Counter.increment())
console.log(Counter.increment())
console.log(Counter.count)
`, "1\n2\n2")
}

func TestE2EClassStaticMemberInheritance(t *testing.T) {
	assertOutput(t, `
class Base {
  static tag: string;
  static { Base.tag = "base"; }
  static whoAmI(): string { return "I am " + Base.tag }
}
class Derived extends Base {
}
console.log(Derived.tag)
console.log(Derived.whoAmI())
Derived.tag = "changed-via-derived"
console.log(Base.tag)
`, "base\nI am base\nchanged-via-derived")
}

func TestE2EClassStaticMemberOverride(t *testing.T) {
	assertOutput(t, `
class Base {
  static whoAmI(): string { return "base" }
}
class Derived extends Base {
  static whoAmI(): string { return "derived" }
}
console.log(Base.whoAmI())
console.log(Derived.whoAmI())
`, "base\nderived")
}

func TestE2EClassPrivateProtectedPositive(t *testing.T) {
	assertOutput(t, `
class Account {
  private balance: number;
  protected owner: string;
  constructor(balance: number, owner: string) {
    this.balance = balance;
    this.owner = owner;
  }
  private describeBalance(): string {
    return "balance is " + this.balance.toString()
  }
  publicDescribe(): string {
    return this.describeBalance() + " for " + this.owner
  }
}
class SavingsAccount extends Account {
  constructor(balance: number, owner: string) { super(balance, owner) }
  describeOwner(): string { return "owner is " + this.owner }
}
const a = new Account(100, "Alice");
console.log(a.publicDescribe())
const s = new SavingsAccount(50, "Bob");
console.log(s.describeOwner())
`, "balance is 100 for Alice\nowner is Bob")
}

func TestE2EClassPrivateFieldFromUnrelatedClassIsError(t *testing.T) {
	_, err := parseAndCompile(`
class Account {
  private balance: number;
  constructor(balance: number) { this.balance = balance; }
}
class Other {
  peek(a: Account): number { return a.balance }
}
`)
	if err == nil {
		t.Fatal("expected a compile error for private field access from an unrelated class")
	}
	if !strings.Contains(err.Error(), "is private and not accessible") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestE2EClassProtectedFieldFromUnrelatedClassIsError(t *testing.T) {
	_, err := parseAndCompile(`
class Account {
  protected balance: number;
  constructor(balance: number) { this.balance = balance; }
}
class Other {
  peek(a: Account): number { return a.balance }
}
`)
	if err == nil {
		t.Fatal("expected a compile error for protected field access from an unrelated class")
	}
	if !strings.Contains(err.Error(), "is protected and not accessible") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestE2EClassPrivateFieldFromTopLevelIsError(t *testing.T) {
	_, err := parseAndCompile(`
class Account {
  private balance: number;
  constructor(balance: number) { this.balance = balance; }
}
const a = new Account(5);
console.log(a.balance)
`)
	if err == nil {
		t.Fatal("expected a compile error for private field access from top-level code")
	}
	if !strings.Contains(err.Error(), "is private and not accessible") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// TestE2ESuperPrivateMethodIsError guards a subtle correctness point: an
// explicit super.method() call still goes through checkMemberVisibility
// even though it always dispatches directly (never virtual) — the
// enclosing class there is the *subclass*, which a private check on the
// *base*'s own method must still refuse, matching real JS/TS (private
// members are never accessible from a subclass, only the exact declaring
// class).
func TestE2ESuperPrivateMethodIsError(t *testing.T) {
	_, err := parseAndCompile(`
class Base {
  private secret(): string { return "s" }
}
class Derived extends Base {
  reveal(): string { return super.secret() }
}
`)
	if err == nil {
		t.Fatal("expected a compile error for super.privateMethod()")
	}
	if !strings.Contains(err.Error(), "is private and not accessible") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestE2EClassPrivateConstructor(t *testing.T) {
	assertOutput(t, `
class Singleton {
  private constructor() {}
  static instance(): Singleton { return new Singleton() }
}
const s = Singleton.instance();
console.log("got singleton")
`, "got singleton")
}

func TestE2EClassPrivateConstructorFromOutsideIsError(t *testing.T) {
	_, err := parseAndCompile(`
class Singleton {
  private constructor() {}
}
const s = new Singleton();
`)
	if err == nil {
		t.Fatal("expected a compile error for a private constructor called from outside the class")
	}
	if !strings.Contains(err.Error(), "is private and not accessible") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestE2EClassAbstractMethodOverride(t *testing.T) {
	assertOutput(t, `
abstract class Shape {
  abstract area(): number;
  describe(): string { return "area is " + this.area().toString() }
}
class Circle extends Shape {
  radius: number;
  constructor(radius: number) { this.radius = radius }
  area(): number { return 3 * this.radius * this.radius }
}
const c = new Circle(2);
console.log(c.describe())
`, "area is 12")
}

func TestE2EClassAbstractDirectInstantiationIsError(t *testing.T) {
	_, err := parseAndCompile(`
abstract class Shape {
  abstract area(): number;
}
const s = new Shape();
`)
	if err == nil {
		t.Fatal("expected a compile error for instantiating an abstract class directly")
	}
	if !strings.Contains(err.Error(), "cannot create an instance of abstract class") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestE2EClassMissingAbstractOverrideIsError(t *testing.T) {
	_, err := parseAndCompile(`
abstract class Shape {
  abstract area(): number;
}
class Circle extends Shape {
  radius: number;
  constructor(radius: number) { this.radius = radius }
}
`)
	if err == nil {
		t.Fatal("expected a compile error for a concrete class missing an abstract override")
	}
	if !strings.Contains(err.Error(), "does not implement abstract method") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestE2EClassImplementsInterface(t *testing.T) {
	assertOutput(t, `
interface Greeter {
  name: string;
  greet(): string;
}
class Dog implements Greeter {
  name: string;
  constructor(name: string) { this.name = name }
  greet(): string { return "Woof, I am " + this.name }
}
const d = new Dog("Rex");
console.log(d.greet())
`, "Woof, I am Rex")
}

func TestE2EClassImplementsMissingMethodIsError(t *testing.T) {
	_, err := parseAndCompile(`
interface Greeter {
  name: string;
  greet(): string;
}
class Cat implements Greeter {
  name: string;
  constructor(name: string) { this.name = name }
}
`)
	if err == nil {
		t.Fatal("expected a compile error for a class missing an interface method")
	}
	if !strings.Contains(err.Error(), "does not satisfy interface") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestE2EClassImplementsMissingFieldIsError(t *testing.T) {
	_, err := parseAndCompile(`
interface Greeter {
  name: string;
  greet(): string;
}
class Cat implements Greeter {
  greet(): string { return "meow" }
}
`)
	if err == nil {
		t.Fatal("expected a compile error for a class missing an interface field")
	}
	if !strings.Contains(err.Error(), "does not satisfy interface") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// TestE2EClassMethodArrayArgument is a regression test for a real,
// pre-existing bug found while wiring getters/setters: emitClassCall (the
// single, shared codegen path behind every `obj.method(args)` call) never
// decomposed an array-typed argument into the (ptr, i64) pair its own
// callee ABI (emitClassMember's parameter-binding loop) already expected —
// any class method call passing an array argument was a hard clang-stage
// crash. Covers both dispatch paths: a direct call (never-overridden
// method) and the vtable-indirect call (a method overridden somewhere in
// the class tree, ADR-00083).
func TestE2EClassMethodArrayArgument(t *testing.T) {
	assertOutput(t, `
class Foo {
  total: number
  constructor() { this.total = 0 }
  addAll(items: number[]): void {
    for (const x of items) {
      this.total = this.total + x
    }
  }
}
const f = new Foo()
f.addAll([1, 2, 3])
console.log(f.total)
`, "6")
}

func TestE2EClassOverriddenMethodArrayArgumentVtableDispatch(t *testing.T) {
	assertOutput(t, `
class Base {
  sum(items: number[]): number { return 0 }
}
class Derived extends Base {
  sum(items: number[]): number {
    let total: number = 0
    for (const x of items) {
      total = total + x
    }
    return total
  }
}
function callIt(b: Base, items: number[]): number {
  return b.sum(items)
}
const d = new Derived()
console.log(callIt(d, [10, 20, 30]))
`, "60")
}

// --- Getters / setters (TDD-00030) ---

func TestE2EClassGetterSetterBasic(t *testing.T) {
	assertOutput(t, `
class Temperature {
  private _celsius: number
  constructor(c: number) { this._celsius = c }
  get celsius(): number { return this._celsius }
  set celsius(v: number) { this._celsius = v }
}
const t = new Temperature(0)
console.log(t.celsius)
t.celsius = 100
console.log(t.celsius)
`, "0\n100")
}

func TestE2EClassGetterOnlyComputedFromOtherFields(t *testing.T) {
	assertOutput(t, `
class Rect {
  width: number
  height: number
  constructor(w: number, h: number) { this.width = w; this.height = h }
  get area(): number { return this.width * this.height }
}
const r = new Rect(3, 4)
console.log(r.area)
`, "12")
}

func TestE2EClassSetterDerivesOtherField(t *testing.T) {
	assertOutput(t, `
class Temperature {
  private _celsius: number
  constructor(c: number) { this._celsius = c }
  get celsius(): number { return this._celsius }
  get fahrenheit(): number { return this._celsius * 9 / 5 + 32 }
  set fahrenheit(f: number) { this._celsius = (f - 32) * 5 / 9 }
}
const t = new Temperature(0)
console.log(t.fahrenheit)
t.fahrenheit = 32
console.log(t.celsius)
`, "32\n0")
}

func TestE2EClassAccessorCompoundAssign(t *testing.T) {
	assertOutput(t, `
class Box {
  private _v: number
  constructor() { this._v = 5 }
  get v(): number { return this._v }
  set v(x: number) { this._v = x }
}
const b = new Box()
b.v += 10
console.log(b.v)
b.v *= 2
console.log(b.v)
`, "15\n30")
}

func TestE2EClassAccessorArrayTyped(t *testing.T) {
	assertOutput(t, `
class Box {
  private _items: number[]
  constructor() { this._items = [] }
  get items(): number[] { return this._items }
  set items(v: number[]) { this._items = v }
}
const b = new Box()
b.items = [1, 2, 3, 4]
console.log(b.items.length)
console.log(b.items[2])
`, "4\n3")
}

func TestE2EClassAccessorUsedFromAnotherMethod(t *testing.T) {
	// Real JS semantics: even internal this.x access (from a method other
	// than the accessor's own body) goes through the getter/setter, not a
	// raw field read — falls out for free from emitMember treating `this`
	// as an ordinary receiver, no special-casing needed.
	assertOutput(t, `
class Counter {
  private count: number
  constructor() { this.count = 0 }
  get value(): number { return this.count }
  bump(): void {
    console.log(this.value)
    this.count = this.count + 1
  }
}
const c = new Counter()
c.bump()
c.bump()
console.log(c.value)
`, "0\n1\n2")
}

func TestE2EClassAccessorOptionalChaining(t *testing.T) {
	assertOutput(t, `
class Circle {
  radius: number
  constructor(r: number) { this.radius = r }
  get area(): number { return this.radius * this.radius * 3 }
}
let maybe: Circle | null = new Circle(5)
console.log(maybe?.area)
maybe = null
console.log(maybe?.area)
`, "75\n0")
}

func TestE2EClassAccessorInheritanceOverrideVtableDispatch(t *testing.T) {
	assertOutput(t, `
class Base {
  get label(): string { return "base" }
}
class Derived extends Base {
  get label(): string { return "derived" }
}
function show(b: Base): string { return b.label }
console.log(show(new Derived()))
console.log(show(new Base()))
`, "derived\nbase")
}

func TestE2EClassGetSetAsPlainMethodNamesStillWork(t *testing.T) {
	// `get`/`set` are contextual, not reserved — a method literally named
	// get()/set() (no property name following, so the parser's lookahead
	// correctly falls back to treating it as an ordinary method) must keep
	// working.
	assertOutput(t, `
class Container {
  get(): number { return 99 }
  set(): number { return 88 }
}
const c = new Container()
console.log(c.get())
console.log(c.set())
`, "99\n88")
}

func TestE2EClassSetterOnlyPropertyReadIsError(t *testing.T) {
	_, err := parseAndCompile(`
class Foo { set x(v: number) {} }
const f = new Foo()
console.log(f.x)
`)
	if err == nil {
		t.Fatal("expected a compile error reading a setter-only property")
	}
	if !strings.Contains(err.Error(), "no getter") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestE2EClassGetterOnlyPropertyWriteIsError(t *testing.T) {
	_, err := parseAndCompile(`
class Foo { get x(): number { return 1 } }
const f = new Foo()
f.x = 5
`)
	if err == nil {
		t.Fatal("expected a compile error writing a getter-only property")
	}
	if !strings.Contains(err.Error(), "no setter") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestE2EClassGetterOnlyPropertyCompoundAssignIsError(t *testing.T) {
	_, err := parseAndCompile(`
class Foo { get x(): number { return 1 } }
const f = new Foo()
f.x += 5
`)
	if err == nil {
		t.Fatal("expected a compile error compound-assigning a getter-only property")
	}
}

func TestE2EClassAccessorLogicalAssignIsError(t *testing.T) {
	_, err := parseAndCompile(`
class Foo {
  private _x: number
  constructor() { this._x = 1 }
  get x(): number { return this._x }
  set x(v: number) { this._x = v }
}
const f = new Foo()
f.x ??= 5
`)
	if err == nil {
		t.Fatal("expected a compile error for logical assignment on an accessor")
	}
}

func TestE2EClassAccessorFieldCollisionIsError(t *testing.T) {
	_, err := parseAndCompile(`
class Foo {
  x: number
  get x(): number { return 1 }
  constructor() { this.x = 1 }
}
`)
	if err == nil {
		t.Fatal("expected a compile error for an accessor colliding with a field name")
	}
}

func TestE2EClassAccessorMethodCollisionIsError(t *testing.T) {
	_, err := parseAndCompile(`
class Foo {
  get x(): number { return 1 }
  x(): void {}
}
`)
	if err == nil {
		t.Fatal("expected a compile error for an accessor colliding with a method name")
	}
}

func TestE2EClassStaticAccessorIsError(t *testing.T) {
	_, err := parseAndCompile(`
class Foo {
  static get x(): number { return 1 }
}
`)
	if err == nil {
		t.Fatal("expected a compile error for a static accessor (not yet supported)")
	}
}

func TestE2EClassGetSetTypeDisagreementIsError(t *testing.T) {
	_, err := parseAndCompile(`
class Foo {
  get x(): number { return 1 }
  set x(v: string) {}
}
`)
	if err == nil {
		t.Fatal("expected a compile error for a getter/setter disagreeing on type")
	}
}

func TestE2EClassGetterWithParamsIsError(t *testing.T) {
	_, err := parseAndCompile(`class Foo { get x(y: number): number { return y } }`)
	if err == nil {
		t.Fatal("expected a compile error for a getter taking parameters")
	}
}

func TestE2EClassSetterWithoutExactlyOneParamIsError(t *testing.T) {
	cases := []string{
		`class Foo { set x(): void {} }`,
		`class Foo { set x(a: number, b: number): void {} }`,
	}
	for _, src := range cases {
		if _, err := parseAndCompile(src); err == nil {
			t.Fatalf("expected a compile error for a setter with the wrong parameter count: %s", src)
		}
	}
}

func TestE2EClassDuplicateGetterIsError(t *testing.T) {
	_, err := parseAndCompile(`
class Foo {
  get x(): number { return 1 }
  get x(): number { return 2 }
}
`)
	if err == nil {
		t.Fatal("expected a compile error for a duplicate getter")
	}
}
