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

func TestE2EClassFieldsWithoutConstructorCompiles(t *testing.T) {
	// A typed field nothing initializes is TS2564 under the strict lane, as
	// in tsc; `x!: number` waives it. Runtime behavior is covered by the
	// TestE2EClassBareField* tests above.
	if _, err := parseAndCompile(`
class Foo {
  x: number;
}
`); err == nil || !strings.Contains(err.Error(), "has no initializer and is not definitely assigned") {
		t.Fatalf("expected TS2564 for a bare typed field, got: %v", err)
	}
	if _, err := parseAndCompile(`
class Foo {
  x!: number;
}
`); err != nil {
		t.Fatalf("expected a definite field to compile, got: %v", err)
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
	if !strings.Contains(err.Error(), "duplicate function implementation of 'greet'") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestE2ESelfReferentialClassField(t *testing.T) {
	// A class field typed as the class itself (or a chain of them) is the
	// classic linked-list/tree shape. Also exercises destructuring a
	// class-typed field into a local binding and drilling further into it —
	// see docs/adr/00/ADR-00064.md for the bug this guards against.
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
console.log(a.nextNode!.nextNode!.value)
const { nextNode } = b;
console.log(nextNode!.value)
`, "3\n3")
}

func TestE2ESelfReferentialClassArrayField(t *testing.T) {
	// A self-referential ARRAY field (`children: Node[]`, the tree shape) kept a
	// stale placeholder in the array's ElemType, so drilling through an indexed
	// element (`root.children[0].val`) failed with "no field". canonicalizeClassTy
	// now re-resolves a class array element too. See ADR-00946.
	assertOutput(t, `
class Node {
  children: Node[] = [];
  val: number = 0;
}
const root = new Node(); root.val = 1;
const a = new Node(); a.val = 2;
const b = new Node(); b.val = 3;
a.children.push(b);
root.children.push(a);
console.log(root.children[0].val);
console.log(root.children[0].children[0].val);
`, "2\n3")
}

// --- for...of over a class whose [Symbol.iterator] drains its next() ---

func TestE2EForOfClassIteratorNumeric(t *testing.T) {
	assertOutput(t, `
class Range {
  current: number;
  max: number;
  constructor(start: number, max: number) {
    this.current = start;
    this.max = max;
  }
  *[Symbol.iterator]() {
    for (let v = this.next(); v !== null; v = this.next()) yield v;
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

// Bug #2 (TDD-00064): a class iterator whose first legitimate value is exactly
// 0 must not terminate immediately. Before Stage 3/4 the `next(): number | null`
// result used a bare 0 as both the value zero and the "done" sentinel, so this
// loop produced nothing; now `next()` returns a presence-flagged aggregate and
// 0 iterates normally.
func TestE2EForOfClassIteratorZeroFirst(t *testing.T) {
	assertOutput(t, `
class CountTo3 {
  current: number = 0;
  *[Symbol.iterator]() {
    for (let v = this.next(); v !== null; v = this.next()) yield v;
  }
  next(): number | null {
    if (this.current > 2) { return null; }
    const v = this.current;
    this.current = this.current + 1;
    return v;
  }
}
for (const x of new CountTo3()) {
  console.log(x)
}
`, "0\n1\n2")
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
  *[Symbol.iterator]() {
    for (let v = this.next(); v !== null; v = this.next()) yield v;
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
`, "true")
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
`, "false")
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
`, "true\nfalse")
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
`, "true\nfalse\ntrue\nfalse")
}

// --- instanceof against built-in types (ADR-00162) ---
//
// A compile-time constant, not a runtime tag check — this compiler's
// static typing already answers "is this an Array/Map/Set/Date/RegExp" at
// compile time, and there's no dynamic prototype-chain trickery here for a
// runtime check to ever disagree with.

func TestE2EInstanceOfBuiltinArrayTrue(t *testing.T) {
	assertOutput(t, `
let arr: number[] = [1, 2, 3];
console.log(arr instanceof Array);
`, "true")
}

func TestE2EInstanceOfBuiltinArrayFalseForNonArray(t *testing.T) {
	assertOutput(t, `
let n: number = 5;
console.log(n instanceof Array);
`, "false")
}

func TestE2EInstanceOfBuiltinMapAndSet(t *testing.T) {
	assertOutput(t, `
let m = new Map<string, number>();
let s = new Set<number>();
console.log(m instanceof Map);
console.log(m instanceof Set);
console.log(s instanceof Set);
console.log(s instanceof Map);
`, "true\nfalse\ntrue\nfalse")
}

func TestE2EInstanceOfBuiltinDate(t *testing.T) {
	assertOutput(t, `
let d = new Date();
console.log(d instanceof Date);
let s: string = "x";
console.log(s instanceof Date);
`, "true\nfalse")
}

func TestE2EInstanceOfBuiltinRegExp(t *testing.T) {
	assertOutput(t, `
let r = /abc/;
console.log(r instanceof RegExp);
console.log(r instanceof Array);
`, "true\nfalse")
}

func TestE2EInstanceOfBuiltinEvaluatesSideEffects(t *testing.T) {
	assertOutput(t, `
function makeArr(): number[] {
  console.log("called");
  return [1, 2, 3];
}
console.log(makeArr() instanceof Array);
`, "called\ntrue")
}

// ADR-00605: `x instanceof Object` is true for any non-primitive, false for a
// primitive — decided at compile time by negating the closed primitive set.
func TestE2EInstanceOfObject(t *testing.T) {
	assertOutput(t, `
class C {}
console.log(new C() instanceof Object);
console.log([1, 2] instanceof Object);
console.log(({ a: 1 }) instanceof Object);
console.log(new Date() instanceof Object);
console.log("hi" instanceof Object);
console.log(42 instanceof Object);
console.log(true instanceof Object);
console.log(10n instanceof Object);
`, "true\ntrue\ntrue\ntrue\nfalse\nfalse\nfalse\nfalse")
}

// A value whose static type can't be decided (any/union/nullable) keeps a
// clean rejection rather than a wrong constant.
func TestE2EInstanceOfObjectOnAny(t *testing.T) {
	// A value held in any is decided at run time (ADR-01763).
	assertSameAsNode(t, `
function f(x: any): boolean { return x instanceof Object; }
console.log(f(5), f({}), f([]), f(null), f(Object.create(null)), f(() => 1));
`)
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
	if !strings.Contains(err.Error(), "cannot find name 'Bar'") { // tsc's TS2304
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
    super();
    this.radius = radius;
  }
  area(): number { return 3 * this.radius * this.radius }
}
class Square extends Shape {
  side: number;
  constructor(side: number) {
    super();
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
`, "true\ntrue\nfalse")
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
`, "true\ntrue\nfalse")
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
`, "true\ntrue\nfalse")
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
	if !strings.Contains(err.Error(), "must contain a 'super' call") {
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
	if !strings.Contains(err.Error(), "cannot find name 'Ghost'") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestE2EClassExtendsBuiltinIsError(t *testing.T) {
	// `extends Error` (TDD-00155 Stage 6), `extends Map`/`Set`/`RegExp`/
	// `Promise`/`Date`/`Array`/a typed array (TDD-00247) work; extending
	// another host builtin is a clean rejection.
	for _, base := range []string{"ArrayBuffer", "Response"} {
		_, err := parseAndCompile("class Derived extends " + base + " {}\n")
		if err == nil {
			t.Fatalf("%s: expected a compile error for extending a built-in", base)
		}
		if !strings.Contains(err.Error(), "extending a builtin other than Error is not supported yet") {
			t.Errorf("%s: unexpected error message: %v", base, err)
		}
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
	if !strings.Contains(err.Error(), "is not assignable to the same property in base type") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// A subclass redeclaring an inherited field with an incompatible type is an
// error (tsc's TS2416); the same type is ordinary TypeScript
// (TestE2ESubclassRedeclaresInheritedField).
func TestE2EClassCollidingFieldNameIsError(t *testing.T) {
	_, err := parseAndCompile(`
class Base {
  x: number;
  constructor(x: number) { this.x = x; }
}
class Derived extends Base {
  x: string;
  constructor(x: number) { super(x); this.x = "s"; }
}
`)
	if err == nil {
		t.Fatal("expected a compile error for a field redeclared with an incompatible type")
	}
	if !strings.Contains(err.Error(), "redeclares inherited field") && !strings.Contains(err.Error(), "TS2416") {
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

// Assigning an inherited static through the subclass gives the subclass its
// own (ADR-01318), as in JavaScript.
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
console.log(Base.tag, Derived.tag)
`, "base\nI am base\nbase changed-via-derived")
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
	if !strings.Contains(err.Error(), "is private and only accessible within class") {
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
	if !strings.Contains(err.Error(), "is protected and only accessible within class") {
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
	if !strings.Contains(err.Error(), "is private and only accessible within class") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// --- `#x` private names (TDD-00021) ---

func TestE2EClassPrivateNameField(t *testing.T) {
	assertOutput(t, `
class Counter {
  #count: number;
  constructor() { this.#count = 0; }
  increment(): void { this.#count = this.#count + 1; }
  getCount(): number { return this.#count; }
}
const c = new Counter();
c.increment();
c.increment();
console.log(c.getCount());
`, "2")
}

func TestE2EClassPrivateNameMethod(t *testing.T) {
	assertOutput(t, `
class Greeter {
  #name: string;
  constructor(name: string) { this.#name = name; }
  #shout(): string { return this.#name + "!"; }
  greet(): string { return this.#shout(); }
}
const g = new Greeter("Ada");
console.log(g.greet());
`, "Ada!")
}

func TestE2EClassPrivateNameStaticField(t *testing.T) {
	assertOutput(t, `
class Box {
  static #count = 0;
  static increment(): void { Box.#count = Box.#count + 1; }
  static getCount(): number { return Box.#count; }
}
Box.increment();
Box.increment();
console.log(Box.getCount());
`, "2")
}

func TestE2EClassPrivateNameAccessor(t *testing.T) {
	assertOutput(t, `
class Box {
  #v: number;
  constructor(v: number) { this.#v = v; }
  get #doubled(): number { return this.#v * 2; }
  reveal(): number { return this.#doubled; }
}
const b = new Box(10);
console.log(b.reveal());
`, "20")
}

// A `#x` field and a same-named `x` field are entirely different names
// (real JS: no collision) — same-class coexistence should just fall out of
// the existing name-uniqueness checks, since they're different strings.
func TestE2EClassPrivateNameCoexistsWithPublicSameStem(t *testing.T) {
	assertOutput(t, `
class Box {
  x: number;
  #x: number;
  constructor() { this.x = 1; this.#x = 2; }
  sum(): number { return this.x + this.#x; }
}
const b = new Box();
console.log(b.sum());
`, "3")
}

// Privacy is per-*class*, not per-*instance* — a method can read another
// instance's own private field of the same class, matching real JS.
func TestE2EClassPrivateNameCrossInstanceSameClassAllowed(t *testing.T) {
	assertOutput(t, `
class Box {
  #v: number;
  constructor(v: number) { this.#v = v; }
  equals(other: Box): boolean { return this.#v == other.#v; }
}
const a = new Box(5);
const b = new Box(5);
console.log(a.equals(b));
`, "true")
}

// Reflection (JSON.stringify, Object.keys) never sees a private name at
// all — a real, observably different guarantee from `private`/`protected`
// (a TypeScript-only, erased concept), the actual reason TDD-00021 exists
// as a separate mechanism instead of just reusing the keyword modifier.
func TestE2EClassPrivateNameHiddenFromReflection(t *testing.T) {
	assertOutput(t, `
class Box {
  #v: number;
  pub: number;
  constructor() { this.#v = 1; this.pub = 2; }
}
const b = new Box();
console.log(JSON.stringify(b));
console.log(Object.keys(b).length);
`, "{\"pub\":2}\n1")
}

func TestE2EClassPrivateNameFromOutsideIsError(t *testing.T) {
	_, err := parseAndCompile(`
class Box {
  #v: number;
  constructor() { this.#v = 1; }
}
const b = new Box();
console.log(b.#v);
`)
	if err == nil {
		t.Fatal("expected a compile error for private-name field access from outside the class")
	}
	if !strings.Contains(err.Error(), "is not declared in an enclosing class") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestE2EClassPrivateNameFromSubclassIsError(t *testing.T) {
	// Exact-class-only, no protected-style relaxation — real JS has no
	// `#`-equivalent of `protected`.
	_, err := parseAndCompile(`
class Base {
  #secret: number;
  constructor() { this.#secret = 1; }
}
class Derived extends Base {
  reveal(): number { return this.#secret; }
}
`)
	if err == nil {
		t.Fatal("expected a compile error for a subclass reading a base class's private-name field")
	}
	if !strings.Contains(err.Error(), "is not declared in an enclosing class") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestE2EClassPrivateNameUndeclaredIsError(t *testing.T) {
	_, err := parseAndCompile(`
class Box {
  #v: number;
  constructor() { this.#v = 1; }
  bad(): number { return this.#nope; }
}
`)
	if err == nil {
		t.Fatal("expected a compile error for referencing an undeclared private name")
	}
	if !strings.Contains(err.Error(), "the private name '#nope' is not declared in an enclosing class") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestE2EClassPrivateNameCombinedWithModifierIsError(t *testing.T) {
	// `#` alone already determines visibility — combining it with an
	// explicit accessibility modifier is rejected, matching real
	// TypeScript ("An accessibility modifier cannot be used with a
	// private identifier").
	_, err := parseAndCompile(`
class Box {
  private #v: number;
}
`)
	if err == nil {
		t.Fatal("expected a compile error for combining an accessibility modifier with a private name")
	}
}

// TestE2ESuperPrivateMethodIsError guards a subtle correctness point: an
// explicit super.method() call is still checked (TS2341) even though it
// always dispatches directly (never virtual) — the
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
	if !strings.Contains(err.Error(), "is private and only accessible within class") {
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
	if !strings.Contains(err.Error(), "is private and only accessible within the class declaration") {
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
  constructor(radius: number) { super(); this.radius = radius }
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
  constructor(radius: number) { super(); this.radius = radius }
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
	if !strings.Contains(err.Error(), "incorrectly implements interface 'Greeter'") {
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
	if !strings.Contains(err.Error(), "incorrectly implements interface 'Greeter'") {
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
console.log(maybe?.area === undefined)
`, "75\nundefined\ntrue")
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

// Static accessors: `C.x` reads the getter, `C.x = v` (and a compound
// assignment) calls the setter.
func TestE2EClassStaticAccessors(t *testing.T) {
	assertSameAsNode(t, `
let store = 3;
class K {
  static get level(): number { return store; }
  static set level(v: number) { if (v < 0) throw new RangeError("neg"); store = v; }
}
console.log(K.level);
K.level = 7;
K.level += 2;
console.log(K.level, store);
try { K.level = -1; } catch (e) { console.log((e as Error).message); }
`)
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

// --- Rest parameters on class methods (found broken while scoping tagged
// template literals, TDD-00059/ADR-00151: emitClassCall had no notion of
// sig.HasRest at all, so a class method with a rest parameter either hit a
// wrong arg-count check or misread the rest slot's own array type as
// applying to a single positional scalar argument) ---

func TestE2EClassMethodRestParam(t *testing.T) {
	assertOutput(t, `
class Sum {
    total(...values: number[]): number {
        let s = 0;
        for (const v of values) { s += v; }
        return s;
    }
}
const sm = new Sum();
console.log(sm.total(1, 2, 3, 4));
console.log(sm.total());
`, "10\n0")
}

func TestE2EClassMethodArrayParamThenRestParam(t *testing.T) {
	assertOutput(t, `
class Fmt {
    build(strings: string[], ...values: number[]): string {
        let r = strings[0];
        for (const v of values) { r += v; }
        return r;
    }
}
const f = new Fmt();
console.log(f.build(["n="], 7, 8, 9));
`, "n=789")
}

func TestE2EClassMethodRestParamVirtualDispatch(t *testing.T) {
	// Calling an overridden rest-param method through a base-typed
	// reference exercises emitClassCall's vtable-indirect-call path
	// (distinct from the direct-call path the tests above exercise).
	assertOutput(t, `
class Base {
    total(...values: number[]): number {
        let s = 0;
        for (const v of values) { s += v; }
        return s;
    }
}
class Derived extends Base {
    total(...values: number[]): number {
        let s = 0;
        for (const v of values) { s += v; }
        return s * 10;
    }
}
function useIt(b: Base): number {
    return b.total(1, 2, 3);
}
const d = new Derived();
console.log(useIt(d));
`, "60")
}

// --- Static method calls (found broken while running the test262
// conformance harness, TDD-00008 Design V2: emitStaticMethodCall is a
// wholly separate function from emitClassCall's instance-method dispatch,
// and had none of the same fixes — no default-value fallback, no rest
// params, no array-typed-parameter decomposition) ---

func TestE2EStaticMethodDefaultParam(t *testing.T) {
	assertOutput(t, `
class Foo {
    static bar(x: number, message: string = "default"): string {
        return message + x;
    }
}
console.log(Foo.bar(1));
console.log(Foo.bar(2, "hi"));
`, "default1\nhi2")
}

func TestE2EStaticMethodRestParam(t *testing.T) {
	assertOutput(t, `
class Sum {
    static total(...values: number[]): number {
        let s = 0;
        for (const v of values) { s += v; }
        return s;
    }
}
console.log(Sum.total(1, 2, 3));
console.log(Sum.total());
`, "6\n0")
}

func TestE2EStaticMethodArrayParam(t *testing.T) {
	assertOutput(t, `
class Sum {
    static first(arr: number[]): number {
        return arr[0];
    }
}
console.log(Sum.first([9, 8]));
`, "9")
}

// A static method whose parameter is a constrained union / dynamic box
// ({ i8, i64 }) must have its scalar argument *boxed*, not run through
// coerce: the static-call path previously used a bare coerce, and because
// IsInteger() reports true for the box's aggregate IR that misfired into an
// invalid `trunc i64 ... to { i8, i64 }` (clang-stage failure). This is the
// exact shape the Test262 harness shim's `assert.sameValue` relies on.
func TestE2EStaticMethodUnionParam(t *testing.T) {
	assertOutput(t, `
class Check {
    static eq(actual: number | string | boolean, expected: number | string | boolean): void {
        if (actual === expected) { console.log("ok"); return; }
        console.log("no");
    }
}
Check.eq(1 + 1, 2);
Check.eq("a" + "b", "ab");
Check.eq(true, false);
`, "ok\nok\nno")
}

// The `| null` member of a static method's union parameter accepts both
// `null` and `undefined` arguments (union V1's Nullable branch) — the
// `assert.sameValue(x, undefined)` / `..., null)` comparison shape. tsc
// rejects the `undefined` argument (TS2345), so this is JavaScript.
func TestE2EStaticMethodUnionParamNullMember(t *testing.T) {
	assertOutputCompatJS(t, `
class Check {
    static eq(actual: number | string | boolean | null, expected: number | string | boolean | null): void {
        if (actual === expected) { console.log("same"); return; }
        console.log("diff");
    }
}
Check.eq(1, undefined);
Check.eq(2, 2);
`, "diff\nsame")
}

// --- TDD-00063 Stage 1: class field initializers ---

// A field with an `= expr` initializer and no constructor is now legal —
// the initializers run at construction time (in declaration order).
func TestE2EFieldInitializerNoConstructor(t *testing.T) {
	assertOutput(t, `
class C {
  x = 5;
  y = 10;
  label = "pt";
}
const c = new C();
console.log(c.x + c.y);
console.log(c.label);
`, "15\npt")
}

// An explicit type annotation may accompany the initializer (`x: number = v`).
func TestE2EFieldInitializerAnnotated(t *testing.T) {
	assertOutput(t, `
class C {
  n: number = 7;
  s: string = "hi";
}
const c = new C();
console.log(c.n);
console.log(c.s);
`, "7\nhi")
}

// A field initializer runs before the constructor body, so a constructor
// assignment to the same field overrides it (spec order: init, then body).
func TestE2EFieldInitializerThenConstructorOverrides(t *testing.T) {
	assertOutput(t, `
class C {
  x = 1;
  y = 2;
  constructor(v: number) { this.y = v; }
}
const c = new C(50);
console.log(c.x);
console.log(c.y);
`, "1\n50")
}

// In a derived class, own field initializers run right after super() returns.
func TestE2EFieldInitializerAfterSuper(t *testing.T) {
	assertOutput(t, `
class B {
  b: number;
  constructor(n: number) { this.b = n; }
}
class D extends B {
  d = 9;
  constructor() { super(4); }
}
const o = new D();
console.log(o.b);
console.log(o.d);
`, "4\n9")
}

// A derived class whose every own field initializes itself needs no explicit
// constructor — the synthesized one forwards to super() then runs the inits.
func TestE2EFieldInitializerDerivedSynthesizedConstructor(t *testing.T) {
	assertOutput(t, `
class B {
  b: number;
  constructor(n: number) { this.b = n; }
}
class D extends B {
  d = 9;
}
const o = new D(5);
console.log(o.b);
console.log(o.d);
`, "5\n9")
}

// A private field may carry an initializer too (TDD-00021 deferred this).
func TestE2EPrivateFieldInitializer(t *testing.T) {
	assertOutput(t, `
class Counter {
  #n = 100;
  value(): number { return this.#n; }
}
console.log(new Counter().value());
`, "100")
}

// A field initializer may reference `this` (an earlier field / method result).
func TestE2EFieldInitializerReferencesThis(t *testing.T) {
	assertOutput(t, `
class C {
  x = 5;
  y = this.x + 1;
}
console.log(new C().y);
`, "6")
}

// A generic class supports field initializers, including across multiple
// distinct instantiations of the same template.
func TestE2EGenericClassFieldInitializer(t *testing.T) {
	assertOutput(t, `
class Box<T> {
  count = 7;
  value: T;
  constructor(v: T) { this.value = v; }
}
console.log(new Box<string>("a").count);
console.log(new Box<number>(3).count);
`, "7\n7")
}

// ADR-00375: a static field with an initializer now compiles (it used to be a
// clean rejection); the initializer runs in the class's staticinit.
func TestE2EStaticFieldInitializerWithConstructorCompiles(t *testing.T) {
	if _, err := parseAndCompile(`
class C {
  static x = 5;
  constructor() {}
}
`); err != nil {
		t.Fatalf("expected a static field initializer to compile, got: %v", err)
	}
}

// --- TDD-00063 Stage 2a: async methods ---

// An async instance method returning Promise<T>, awaited by the caller.
func TestE2EAsyncInstanceMethod(t *testing.T) {
	assertOutput(t, `
class C {
  async m(): Promise<number> { return 42; }
}
const v = await new C().m();
console.log(v);
`, "42")
}

// One async method awaiting another async method on the same instance (`this`).
func TestE2EAsyncMethodAwaitsThisMethod(t *testing.T) {
	assertOutput(t, `
class Svc {
  async fetchVal(): Promise<number> { return 7; }
  async total(): Promise<number> {
    const a = await this.fetchVal();
    return a + 1;
  }
}
console.log(await new Svc().total());
`, "8")
}

// A static async method.
func TestE2EStaticAsyncMethod(t *testing.T) {
	assertOutput(t, `
class M {
  static async compute(): Promise<number> { return 9; }
}
console.log(await M.compute());
`, "9")
}

// An async method returning Promise<void>.
func TestE2EAsyncVoidMethod(t *testing.T) {
	assertOutput(t, `
class Logger {
  async log(): Promise<void> { console.log("logged"); }
}
await new Logger().log();
`, "logged")
}

// --- TDD-00063 Stage 2b: generator methods ---

// A generator method iterated with for...of, yielding from a `this` field.
func TestE2EGeneratorMethodForOf(t *testing.T) {
	assertOutput(t, `
class Range {
  lo: number;
  hi: number;
  constructor(lo: number, hi: number) { this.lo = lo; this.hi = hi; }
  *values(): Generator<number> {
    for (let i = this.lo; i < this.hi; i = i + 1) { yield i; }
  }
}
for (const x of new Range(1, 5).values()) { console.log(x); }
`, "1\n2\n3\n4")
}

// A generator method driven by explicit .next(), including the done flag.
func TestE2EGeneratorMethodNext(t *testing.T) {
	assertOutput(t, `
class Counter {
  n: number;
  constructor(start: number) { this.n = start; }
  *count(): Generator<number> { yield this.n; yield this.n + 1; }
}
const g = new Counter(10).count();
const a = g.next();
console.log(a.value);
console.log(a.done);
const b = g.next();
console.log(b.value);
const c = g.next();
console.log(c.done);
`, "10\nfalse\n11\ntrue")
}

// A generator method with its own parameter, alongside `this`.
func TestE2EGeneratorMethodWithParam(t *testing.T) {
	assertOutput(t, `
class Stepper {
  base: number;
  constructor(base: number) { this.base = base; }
  *upTo(limit: number): Generator<number> {
    let i = this.base;
    while (i <= limit) { yield i; i = i + 1; }
  }
}
for (const x of new Stepper(3).upTo(6)) { console.log(x); }
`, "3\n4\n5\n6")
}

// A generator method inherited/used in a derived class (super + this fields).
func TestE2EGeneratorMethodDerived(t *testing.T) {
	assertOutput(t, `
class Base {
  label: string;
  constructor(l: string) { this.label = l; }
}
class Seq extends Base {
  n: number;
  constructor(l: string, n: number) { super(l); this.n = n; }
  *nums(): Generator<number> { let i = 0; while (i < this.n) { yield i; i = i + 1; } }
}
const s = new Seq("x", 3);
for (const v of s.nums()) { console.log(v); }
console.log(s.label);
`, "0\n1\n2\nx")
}

// A static generator method is a clean, specific rejection (V1 is instance
// methods only).
func TestE2EStaticGeneratorMethodRejected(t *testing.T) {
	_, err := parseAndCompile(`
class C {
  static *g(): Generator<number> { yield arguments.length; }
}
`)
	if err == nil {
		t.Fatal("expected a compile error for a static generator method reading arguments")
	}
	if !strings.Contains(err.Error(), "static generator method") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// An async generator method (`async *m()`) now works (TDD-00085 Stage 4) — see
// TestE2EAsyncGeneratorMethod / …ThrowRejects in generators_test.go for the full
// behavioral coverage; this just guards that it compiles and runs.
func TestE2EAsyncGeneratorMethodCompiles(t *testing.T) {
	assertOutput(t, `
class C {
  async *g(): AsyncGenerator<number> { yield 1; yield 2 }
}
async function main2(): Promise<void> {
  for await (const x of new C().g()) { console.log(x) }
}
main2()
`, "1\n2")
}

// A class constructor accepting an array argument (`new C([1,2,3])`) — a
// pre-existing bug fixed alongside generator methods (ADR-00182).
func TestE2EConstructorArrayArgument(t *testing.T) {
	assertOutput(t, `
class Acc {
  items: number[];
  constructor(items: number[]) { this.items = items; }
  first(): number { return this.items[0]; }
  count(): number { return this.items.length; }
}
const a = new Acc([10, 20, 30]);
console.log(a.first());
console.log(a.count());
`, "10\n3")
}

// --- TDD-00063 Stage 3: computed member names (constant string/number) ---

// A constant string computed method name desugars to a named method.
func TestE2EComputedStringMethodName(t *testing.T) {
	assertOutput(t, `
class C {
  a(): string { return "A"; }
  ["b"](): string { return "B"; }
  c(): string { return "C"; }
}
const o = new C();
console.log(o.a() + o.b() + o.c());
`, "ABC")
}

// A constant string computed field name, read back by its plain name.
func TestE2EComputedStringFieldName(t *testing.T) {
	assertOutput(t, `
class C {
  ["count"]: number = 5;
  bump(): number { this.count = this.count + 1; return this.count; }
}
const o = new C();
console.log(o.count);
console.log(o.bump());
`, "5\n6")
}

// A computed name on a getter (`get ["value"]()`).
func TestE2EComputedGetterName(t *testing.T) {
	assertOutput(t, `
class C {
  _v: number = 7;
  get ["value"](): number { return this._v; }
}
console.log(new C().value);
`, "7")
}

// A computed member name bound to anything but a const symbol or a const
// string/number literal (a `let`, a call) is a clean, specific rejection.
// `[Symbol.iterator]` / `[Symbol.asyncIterator]` are the recognized
// well-known-symbol exceptions; a const literal names the member by value
// and a const symbol keys it (TDD-00246).
func TestE2EComputedNonConstantNameRejected(t *testing.T) {
	_, err := parseAndCompile(`
let k = "dyn";
class C {
  [k]() { return 1; }
}
`)
	if err == nil {
		t.Fatal("expected a compile error for a non-constant computed member name")
	}
	if !strings.Contains(err.Error(), "computed class member name must be a constant") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// --- TDD-00063 Stage 4: class expressions (nominal binding) ---

// An anonymous class expression bound to a const, used via `new`.
func TestE2EClassExpressionAnonymous(t *testing.T) {
	assertOutput(t, `
const Counter = class {
  n: number = 0;
  bump(): number { this.n = this.n + 1; return this.n; }
};
const c = new Counter();
console.log(c.bump());
console.log(c.bump());
`, "1\n2")
}

// A class expression inherits Stage 1-3 members (fields, constructor, methods).
func TestE2EClassExpressionWithConstructor(t *testing.T) {
	assertOutput(t, `
const Point = class {
  x: number;
  y: number;
  constructor(x: number, y: number) { this.x = x; this.y = y; }
  sum(): number { return this.x + this.y; }
};
console.log(new Point(3, 4).sum());
`, "7")
}

// A named class expression binds under the LHS name, keeps its own name for
// `.name` and its self-references; the class is usable as a type annotation
// too.
func TestE2EClassExpressionNamedAndAsType(t *testing.T) {
	assertOutput(t, `
const Box = class Impl {
  v: number;
  constructor(v: number) { this.v = v; }
};
function make(n: number): InstanceType<typeof Box> { return new Box(n); }
console.log(make(9).v);
`, "9")
}

// A class expression with a generator method (Stage 2b machinery reused).
func TestE2EClassExpressionGeneratorMethod(t *testing.T) {
	assertOutput(t, `
const Seq = class {
  *nums(): Generator<number> { yield 1; yield 2; yield 3; }
};
for (const n of new Seq().nums()) { console.log(n); }
`, "1\n2\n3")
}

// A class expression that reads a local of its enclosing function is a
// clean, specific rejection.
// A class expression reading a local is a class per evaluation (ADR-01336).
func TestE2EClassExpressionCapturingLocal(t *testing.T) {
	assertSameAsNode(t, `
function f(n: number) { return class { v = n; }; }
console.log(new (f(3))().v, new (f(4))().v, f(1) === f(1));
`)
}

// ADR-01318: a class expression as a value — an argument, a return value, a
// nested binding, an array element — named after itself or its binding.
func TestE2EClassExpressionAsValue(t *testing.T) {
	assertSameAsNode(t, `
function make(C: new () => { x: number }) { return new C().x }
console.log(make(class { x = 5 }))
function f() {
  const K = class { static tag = "k"; v = 1; static make() { return new K() } }
  return K
}
const K1 = f()
console.log(K1.name, new K1().v, K1.make().v, K1)
const Named = class Inner { static self() { return Inner.name } }
console.log(Named.name, Named.self(), new Named())
const list = [class A1 {}, class {}]
console.log(list[0].name, JSON.stringify(list[1].name), list)
function g() { return class Gx extends Error { kode = 7 } }
const E = g()
const e = new E("m")
console.log(e instanceof Error, e.kode, e.message)
`)
}

// --- ADR-00373: a class with declared-but-uninitialized fields and no explicit
// constructor. calloc zero-fills, so `class C { x: number }` is accepted; any
// field initializers that DO exist still run. ---

func TestE2EClassBareFieldNoConstructor(t *testing.T) {
	assertOutputCompatJS(t, `
class C { x: number; y: number; }
const c = new C();
console.log(c.x);
console.log(c.y);
`, "undefined\nundefined")
}

func TestE2EClassMixedInitAndBareFields(t *testing.T) {
	assertOutputCompatJS(t, `
class Config { retries = 3; timeout: number; verbose = true; level: number; }
const cfg = new Config();
console.log(cfg.retries);
console.log(cfg.timeout);
console.log(cfg.verbose);
console.log(cfg.level);
`, "3\nundefined\ntrue\nundefined")
}

func TestE2EClassBareNullableRefField(t *testing.T) {
	assertOutputCompatJS(t, `
class Node { value: number = 7; next: Node | null; }
const n = new Node();
console.log(n.value);
console.log(n.next === null, n.next);
`, "7\nfalse undefined")
}

func TestE2EGenericClassBareField(t *testing.T) {
	assertOutput(t, `
class Box<T> { value: T; count: number; }
const b = new Box<number>();
console.log(b.value);
console.log(b.count);
`, "undefined\nundefined")
}

// --- ADR-00375: static field initializers (`static x = expr`). Lowered to
// `ClassName.x = expr` assignments run once in the class's staticinit, ahead of
// any `static {}` block. ---

func TestE2EStaticFieldInitializerBasic(t *testing.T) {
	assertOutput(t, `
class C { static count = 5; }
console.log(C.count);
`, "5")
}

func TestE2EStaticFieldInitializerTypedAndExpr(t *testing.T) {
	assertOutput(t, `
class C {
  static n: number = 3 * 4;
  static label = "hi";
}
console.log(C.n);
console.log(C.label);
`, "12\nhi")
}

func TestE2EStaticFieldInitializerReadInMethod(t *testing.T) {
	assertOutput(t, `
class C {
  static base = 10;
  static describe(): number { return C.base + 5; }
}
console.log(C.describe());
`, "15")
}

func TestE2EStaticFieldInitializerWithStaticBlock(t *testing.T) {
	// Field initializers run first (declaration order), then the static block.
	assertOutput(t, `
class C {
  static a = 1;
  static b = 2;
  static { C.a = C.a + C.b; }
}
console.log(C.a);
console.log(C.b);
`, "3\n2")
}

func TestE2EStaticFieldInitializerMutableViaCompound(t *testing.T) {
	assertOutput(t, `
class C {
  static count = 0;
  static inc(): void { C.count += 1; }
}
C.inc();
C.inc();
console.log(C.count);
`, "2")
}

// --- arrow functions capture the enclosing method's `this` (ADR-00460) ---

func TestE2EArrowCapturesLexicalThis(t *testing.T) {
	assertOutput(t, `
class Counter {
    n: number = 0;
    bump(times: number): void {
        const inc = (): void => { this.n = this.n + 1; };
        for (let i = 0; i < times; i++) { inc(); }
        const nested = (): number => (() => this.n * 10)();
        console.log(nested());
    }
}
const c = new Counter();
c.bump(3);
console.log(c.n);
`, "30\n3")
}

func TestE2EAsyncArrowCapturesThis(t *testing.T) {
	assertOutput(t, `
class C {
    v: number = 7;
    async get(): Promise<number> {
        const fn = async (): Promise<number> => this.v;
        return await fn();
    }
}
async function run(): Promise<void> {
    console.log(await new C().get());
}
run();
`, "7")
}

func TestE2EFunctionExpressionThisStillRejected(t *testing.T) {
	// A `function(){}` body keeps its own dynamic `this` — no lexical
	// capture, still a clean rejection (ADR-00460).
	_, err := parseAndCompile(`
class C { m(): void { const f = function(): number { return this.x; }; } }
`)
	if err == nil {
		t.Fatal("expected a compile error for 'this' inside a function expression")
	}
}

func TestE2EArgumentsObjectInMethod(t *testing.T) {
	// ADR-00464: the synthesized `arguments` array (ADR-00387) works in
	// class method bodies too, same same-typed-parameters scope.
	assertOutput(t, `
class C {
    sum(a: number, b: number): number {
        let t = 0;
        for (let i = 0; i < arguments.length; i++) { t += arguments[i]; }
        return t;
    }
    join(a: string, b: string): string {
        let out = "";
        for (const s of arguments) { out += s; }
        return out;
    }
}
console.log(new C().sum(3, 4));
console.log(new C().join("x", "y"));
`, "7\nxy")
}

func TestE2EReadonlyFieldModifier(t *testing.T) {
	// ADR-00480: `readonly` field modifier parses (recorded, not enforced —
	// the ADR-00447 ctor-param stance); a field literally named `readonly`
	// still works.
	assertOutput(t, `
class C {
    readonly id: number = 7;
    static readonly tag: string = "C";
    readonly = 5;
}
const c = new C();
console.log(c.id, C.tag, c.readonly);
`, "7 C 5")
}

// TDD-00154: the `readonly` field modifier is enforced as a compile-time
// immutability check — a write is allowed only in the declaring class's
// constructor (and field initializers, spliced into it).
func TestE2EReadonlyFieldConstructorWriteOK(t *testing.T) {
	assertOutput(t, `
class Point {
  readonly x: number;
  y: number;
  constructor(x: number, y: number) { this.x = x; this.y = y; }
  move(dy: number) { this.y += dy; }
}
const p = new Point(1, 2);
console.log(p.x, p.y);
p.move(3);
console.log(p.y);
`, "1 2\n5")
}

func TestE2EReadonlyFieldOutsideWriteRejected(t *testing.T) {
	_, err := parseAndCompile(`
class Point { readonly x: number; constructor(x: number) { this.x = x; } }
const p = new Point(1);
p.x = 5;
`)
	if err == nil {
		t.Fatal("expected a compile error writing a readonly field outside the constructor, got none")
	}
	if !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("expected 'read-only', got: %v", err)
	}
}

func TestE2EReadonlyFieldMethodWriteRejected(t *testing.T) {
	_, err := parseAndCompile(`
class Point { readonly x: number; constructor(x: number) { this.x = x; } bad() { this.x = 9; } }
console.log(new Point(1).x);
`)
	if err == nil {
		t.Fatal("expected a compile error writing a readonly field in a method, got none")
	}
	if !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("expected 'read-only', got: %v", err)
	}
}

// A `readonly` constructor parameter property is enforced the same way.
func TestE2EReadonlyParameterPropertyWriteRejected(t *testing.T) {
	_, err := parseAndCompile(`
class User { constructor(readonly name: string) {} rename(n: string) { this.name = n; } }
console.log(new User("Ada").name);
`)
	if err == nil {
		t.Fatal("expected a compile error writing a readonly parameter property, got none")
	}
	if !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("expected 'read-only', got: %v", err)
	}
}

// A subclass constructor cannot write a base class's readonly field (only the
// declaring class's constructor may).
func TestE2EReadonlyInheritedFieldSubclassWriteRejected(t *testing.T) {
	_, err := parseAndCompile(`
class Base { readonly id: number; constructor(id: number) { this.id = id; } }
class Derived extends Base { constructor(id: number) { super(id); this.id = id + 1; } }
console.log(new Derived(5).id);
`)
	if err == nil {
		t.Fatal("expected a compile error writing an inherited readonly field in a subclass, got none")
	}
	if !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("expected 'read-only', got: %v", err)
	}
}

// A function-typed method parameter contextually types an untyped arrow
// argument's parameters from its own declared signature — mirroring the
// free-function call path. Regression: previously a class method's callback
// argument left its untyped params to self-infer to the numeric default,
// silently mis-decoding the callback's arguments (surfaced by TDD-00157's
// EventEmitter method overrides, but reproduced here with a plain class).
func TestE2EClassMethodCallbackParamContextualTyping(t *testing.T) {
	assertOutput(t, `
class Runner {
  go(cb: (name: string, value: number) => void): void {
    cb("cpu", 42);
  }
}
new Runner().go((name, value) => console.log(name + "=" + value));
`, "cpu=42")
}

// The same function-typed-parameter contextual-typing fix (ADR-00632)
// applies to the static-method call path, array .push into a function-typed
// element, and Map.set with a function-typed value — every site that emits a
// value against a known target type and could receive an untyped arrow.
func TestE2ECallbackParamContextualTypingSweep(t *testing.T) {
	assertOutput(t, `
class Util {
  static run(cb: (name: string, value: number) => void): void { cb("cpu", 1); }
}
Util.run((name, value) => console.log("static " + name + "=" + value));

const fns: ((name: string, value: number) => void)[] = [];
fns.push((name, value) => console.log("push " + name + "=" + value));
fns[0]("mem", 2);

const m = new Map<string, (name: string, value: number) => void>();
m.set("k", (name, value) => console.log("map " + name + "=" + value));
const g = m.get("k");
g!("disk", 3);
`, "static cpu=1\npush mem=2\nmap disk=3")
}

// An unannotated class field initialized with a sibling `new C()` takes C's
// object type (ADR-00991): registration is topological, so at the time the
// field's type is inferred the sibling's finalized ClassInfo.Ty may not exist
// yet — the nominal class type is used and re-resolved on access. Regression
// for a "field access on non-object" on `obj.p.x`.
func TestE2EUnannotatedFieldSiblingClassType(t *testing.T) {
	assertOutput(t, `
class Point { x = 1; y = 2; }
class Box { p = new Point(); n = 42; }
const b = new Box();
console.log(b.p.x, b.p.y, b.n);
`, "1 2 42")
}

// A `;` between class members is an empty class element.
func TestE2EClassEmptyElements(t *testing.T) {
	assertOutput(t, `
class A { ; m() { return 1 }; n() { return 2 };; x = 3; }
const a = new A()
console.log(a.m() + a.n() + a.x)
`, "6")
}

// A computed `["constructor"]` key names an ordinary method, not the
// constructor: the class keeps its real constructor, and the method is
// callable by that name.
func TestE2EClassComputedConstructorKeyIsMethod(t *testing.T) {
	assertOutput(t, `
let r = 0
class B {
  constructor() { r += 2 }
  ["constructor"]() { r += 1 }
}
const b = new B()
console.log(r)
b.constructor()
console.log(r)
`, "2\n3")
}

// A definite assignment assertion (`name!: T`) declares a field set outside
// the constructor; it parses and the field reads what the method stored.
func TestE2EClassDefiniteAssignmentField(t *testing.T) {
	assertOutput(t, `
class Conn {
  host!: string;
  port!: number;
  constructor(url: string) { this.init(url); }
  init(url: string): void {
    const i = url.indexOf(":");
    this.host = url.slice(0, i);
    this.port = Number(url.slice(i + 1));
  }
}
const c = new Conn("thessaloniki.example:8080");
console.log(c.host, c.port);
`, "thessaloniki.example 8080")
}

// An optional parameter property (`public p?: T`) declares an optional
// field: called without the argument, it reads undefined, not a zero or
// null stand-in.
func TestE2EOptionalParameterProperty(t *testing.T) {
	assertOutput(t, `
class C { constructor(public p?: number, public s?: string) {} }
const c = new C()
console.log(c.p, c.s, c.p === undefined, c.s === undefined)
const d = new C(2, "x")
console.log(d.p, d.s)
`, "undefined undefined true true\n2 x")
}

// A derived class calls super() even when its base has no constructor; the
// `this` type in a method; a closure with fixed parameters passed where a
// rest signature is expected; the empty tuple type.
func TestE2ESuperThisTypeRestSpread(t *testing.T) {
	assertOutput(t, `
class Base { x = 5 }
class Derived extends Base {
  y: number
  constructor() { super(); this.y = 7 }
  self(): this { return this }
}
const d = new Derived()
console.log(d.self().y, d.x)
function call(f: (...args: any[]) => void) { f("a", 2) }
call((s: string, n: number) => console.log(s, n))
function run(cb: (...args: any[]) => void) { cb("u", 9) }
run((a, b) => console.log(typeof a, a, b))
type NoArgs = []
const none: NoArgs = []
console.log(none.length)
`, "7 5\na 2\nstring u 9\n0")
}

// A parameter property's default may hold a closure over an earlier
// parameter.
func TestE2EParameterPropertyDefaultClosure(t *testing.T) {
	assertOutput(t, `
class C {
    constructor(y: number, public x = ((z: number) => z + y)(1)) {}
}
console.log(new C(10).x)
console.log(new C(1, 7).x)
`, "11\n7")
}

// An override may declare fewer parameters than the method it overrides,
// as TypeScript allows; a call through the base passes the rest unused.
func TestE2EOverrideWithFewerParameters(t *testing.T) {
	assertOutput(t, `
class Base {
  step(size: number, tag: string): string { return "base " + size + tag }
  run(): string { return this.step(16, "!") }
}
class Derived extends Base {
  step(): string { return "derived" }
}
const b: Base = new Derived();
console.log(b.run(), new Base().run())
`, "derived base 16!")
}

// An arrow inside a method reads the class's private members, and an
// element access by name reaches a private member, as TypeScript allows.
func TestE2EPrivateMemberFromArrowAndElementAccess(t *testing.T) {
	assertOutput(t, `
class Counter {
  private n = 0;
  private bump(): number { return ++this.n }
  twice(): number { const f = () => this.bump(); f(); return f() }
}
const c = new Counter();
console.log(c.twice(), c["n"], c["bump"]())
`, "2 2 3")
}

func TestE2EProtectedMemberThroughOtherInstanceIsError(t *testing.T) {
	_, err := parseAndCompile(`
class A { protected q = 1 }
class B extends A { peek(a: A): number { return a.q } }
`)
	if err == nil || !strings.Contains(err.Error(), "is protected and only accessible through an instance of class 'B'") {
		t.Fatalf("want TS2446, got %v", err)
	}
}

// An object literal passed to a method's, a static method's or a
// constructor's `any` parameter is the object, as it is for a function's.
func TestE2EObjectLiteralIntoAnyParameterOfMethod(t *testing.T) {
	assertOutput(t, `
class U {
  static show(c: any): void { console.log(c) }
  constructor(c: any) { console.log(c) }
  put(c: any): void { console.log(c) }
}
U.show({ k: 1 });
new U({ k: 2 }).put({ k: 3 });
`, "{ k: 1 }\n{ k: 2 }\n{ k: 3 }")
}

// A local a method's closure captures in one branch is still a shared cell
// in the other (the eager capture boxing functions already had).
func TestE2EMethodClosureCapturedInOneBranch(t *testing.T) {
	assertOutput(t, `
class W {
  private hook?: (cb: () => void) => void;
  constructor(hook?: (cb: () => void) => void) { this.hook = hook }
  finish(): void {
    const done = () => { console.log("done") };
    if (this.hook) this.hook(() => { done() });
    else done();
  }
}
new W().finish();
new W((cb) => { console.log("hooked"); cb() }).finish();
`, "done\nhooked\ndone")
}

// `const self = this` in a method takes the receiver's type.
func TestE2EThisAliasLocal(t *testing.T) {
	assertOutput(t, `
class R {
  v = 5;
  get(): number { const self = this; return self.v; }
  later(): () => number { const self = this; return () => self.v + 1; }
}
console.log(new R().get(), new R().later()());
`, "5 6")
}

// A class method call leaving out the optional parameter before a rest
// parameter passes an empty rest.
func TestE2EOptionalBeforeRestOmitted(t *testing.T) {
	assertOutput(t, `
class C {
  run(fn: (...a: any[]) => any, thisArg?: any, ...args: any[]): number { return args.length + fn(); }
}
console.log(new C().run(() => 10), new C().run(() => 10, undefined, 1, 2));
`, "10 12")
}

// A dictionary or Map field of a class whose layout completes later (here
// an EventEmitter subclass, its base from another module) reads the
// finished layout, not the placeholder the field type captured.
func TestE2EClassDictFieldOfLaterClass(t *testing.T) {
	assertSameAsNodeImports(t, `
import { EventEmitter } from 'events'
class H { ws?: NodeJS.Dict<W>; m = new Map<string, W>(); constructor() { this.ws = {} } }
class W extends EventEmitter { id: number; state = 'none'; constructor(id: number) { super(); this.id = id } }
const h = new H()
h.ws!['1'] = new W(1)
h.m.set('2', new W(2))
const ws = h.ws!
for (const k in ws) console.log(k, ws[k]!.id, ws[k]!.state)
console.log(h.m.get('2')!.id, h.m.get('2')!.state)
`)
}

// ADR-01317: a static method read as a value (`const f = C.m`), and an
// unannotated defaulted parameter of a method (`p = '!'` is a string), whose
// default fills an omitted argument through a function value too.
func TestE2EStaticMethodValueAndDefaultParams(t *testing.T) {
	assertSameAsNode(t, `
class Ev {
  static b(t: number): number { return t * 2 }
  static greet(name: string, p = '!'): string { return 'hi ' + name + p }
  hello(name: string, p = '.'): string { return name + p }
}
class Sub extends Ev {}
const f = Ev.b
const g = Ev.greet
console.log(f(21), g('a'), g('b', '?'), f === Ev.b, f.name, f.length, typeof f, Sub.b === Ev.b)
console.log([1, 2].map(Ev.b), Ev.greet('c'), new Ev().hello('d'), new Ev().hello('e', '!'))
function h(name: string, p = '!'): string { return name + p }
const k = h
console.log(k('x'), k('y', '?'))
`)
}

// ADR-01317: a class through constants bound to it (`const K = C; new K()`),
// a class's `name` and `length`, and util.inspect's `[class B extends A]`.
func TestE2EClassConstAliasAndInspect(t *testing.T) {
	assertSameAsNode(t, `
class C { n: number; constructor(n: number, m = 2) { this.n = n + m } static s = 5 }
class D extends C { }
const K = C
const J = K
const c = new J(3)
console.log(c.n, c instanceof C, K === C, K.name, C.name, C.length, D.name, J.s, typeof K)
const A: any = D
console.log(A, K, [K], { K }, TypeError)
class E { static t = 'x'; static #p = 1; static o = { a: [1] } }
const B: any = E
console.log(B, [B])
E.t = 'y'
console.log(B)
`)
}

// ADR-01318: `new` through a class held in a value — a `typeof C` binding,
// a parameter, an array element, a member, a call — and builtin
// constructors as values.
func TestE2ENewThroughClassValue(t *testing.T) {
	assertSameAsNode(t, `
class A { n: number; constructor(n: number) { this.n = n } hi() { return "A" + this.n } }
class B extends A { hi() { return "B" + this.n } }
let K: typeof A = A
console.log(new K(1).hi())
K = B
console.log(new K(2).hi())
function make(C: typeof A, n: number) { return new C(n) }
console.log(make(B, 3).hi())
const anyK: any = A
console.log(new anyK(4).hi())
const ks: (typeof A)[] = [A, B]
for (const k of ks) console.log(new k(5).hi())
class F { P: typeof A = B; m() { return new this.P(6) } }
console.log(new F().m().hi())
const reg = { A, B }
console.log(new reg.B(7).hi(), new ks[0](8).hi())
const pick = (b: boolean) => (b ? A : B)
console.log(new (pick(false))(9).hi())
function gen<T>(ctor: new () => T): T { return new ctor() }
console.log(gen(Map).size, gen(Set) instanceof Set)
try { const z: any = 5; new z() } catch (e) { console.log((e as Error).message) }
try { const o: any = {}; new o.missing() } catch (e) { console.log((e as Error).message) }
`)
}

// ADR-01318: builtin constructors as values, constructed through any.
func TestE2EBuiltinConstructorValues(t *testing.T) {
	assertSameAsNode(t, `
const M = Map
const m = new M<string, number>([["a", 1]])
console.log(m.get("a"), M.name, typeof M)
const ctors: any[] = [Map, Set, Date, Error, URL, TypeError]
console.log(new ctors[0]([["k", 2]]).get("k"), new ctors[1]([1, 2, 2]).size)
console.log(new ctors[2](0).toISOString(), new ctors[3]("boom").message)
console.log(new ctors[4]("https://example.com/p").hostname, new ctors[5]("t") instanceof TypeError)
const P: any = Promise
new P((res: any) => res(5)).then((v: any) => console.log("p", v))
`)
}

// ADR-01318: statics through a class value, and an inherited static field
// assigned through a subclass becoming the subclass's own.
func TestE2EStaticsThroughClassValueAndInheritance(t *testing.T) {
	assertSameAsNode(t, `
class S { static count = 0; static m = "x"; static make() { return new S() } constructor(a = 1, b?: number) { S.count++ } }
class T extends S {}
class U extends T {}
const K: typeof S = T
console.log(K.make() instanceof S, K.count, K.length, S.length)
K.count = 10
console.log(S.count, T.count, U.count)
U.count += 5
S.count ||= 9
T.count &&= 7
console.log(S.count, T.count, U.count)
console.log(S, T, U)
console.log(Object.keys(S), Object.keys(T), Object.keys(U))
`)
}

func TestE2EClassValueThroughAny(t *testing.T) {
	// A class held in any: instanceof, new, statics, inspect, and an
	// instance's `constructor` read through any; `new` of a non-constructor
	// names the callee with its type assertion erased.
	assertSameAsNode(t, `
class Animal { static count = 0; name: string; constructor(name: string) { this.name = name; Animal.count++; } }
class Dog extends Animal { static legs = 4; }
const C: any = Dog;
const A: any = Animal;
const d = new C("rex");
console.log(d instanceof A, d instanceof C, new A("y") instanceof C, C.name, C.length, C.legs, A.count);
A.count = 10;
console.log(Animal.count, C, A, Object.keys(C), d.constructor === C, d.constructor.name);
const E: any = TypeError;
const e = new E("bad");
console.log(e instanceof E, e instanceof (Error as any), E.length, e.message);
try { new (5 as any)(); } catch (x: any) { console.log(x instanceof TypeError, x.message); }
const M: any = Map;
console.log(new M([["a", 1]]).get("a"));
`)
}

// A subclass instance behind its base class's type serializes and inspects
// as the object it is, with its own fields (Node renders the runtime class).
func TestE2ESubclassInstanceThroughBaseType(t *testing.T) {
	out := compileAndRun(t, `
class Point { x: number; y: number; constructor(x: number, y: number) { this.x = x; this.y = y; } }
class Tagged extends Point { tag: string; constructor(x: number, tag: string) { super(x, x); this.tag = tag; } }
const made: Point[] = [new Tagged(3, 'a'), new Point(1, 2)];
const p: Point = new Tagged(4, 'b');
console.log(JSON.stringify(made));
console.log(JSON.stringify(p), JSON.stringify(made[0]));
console.log(p);
console.log(made);
console.log(made[0]);
console.log(JSON.stringify({ first: made[0] }));
`)
	want := `[{"x":3,"y":3,"tag":"a"},{"x":1,"y":2}]
{"x":4,"y":4,"tag":"b"} {"x":3,"y":3,"tag":"a"}
Tagged { x: 4, y: 4, tag: 'b' }
[ Tagged { x: 3, y: 3, tag: 'a' }, Point { x: 1, y: 2 } ]
Tagged { x: 3, y: 3, tag: 'a' }
{"first":{"x":3,"y":3,"tag":"a"}}`
	if out != want {
		t.Errorf("got:\n%s\nwant:\n%s", out, want)
	}
}

// `new obj.K()` reads K as a property of obj, even when a top-level class
// is also named K.
func TestE2ENewQualifiedPropertyNotClassName(t *testing.T) {
	assertSameAsNode(t, `
class Square { name = "square" }
class Circle { name = "circle" }
const reg = { Square: Circle };
console.log(new reg.Square().name, new Square().name);
`)
}

// TDD-00246: a class field keyed by a symbol the program declares is a field
// of the class: read, written and compound-assigned through `obj[k]`,
// inherited, hidden from Object.keys and JSON, shown by inspect as
// `Symbol(desc)`, and found through `any` by the symbol's own key.
func TestE2EClassSymbolKeyedFields(t *testing.T) {
	const src = `
const kRefed = Symbol('refed')
const kCount = Symbol('count')
class T {
  _idleTimeout: number = 5;
  [kRefed]: boolean | null = true;
  [kCount]: number = 7
  hasRef(): boolean | null { return this[kRefed] }
}
class U extends T { extra = 'u' }
const t = new T()
console.log(t.hasRef(), t[kCount])
t[kRefed] = null
t[kCount] += 1
console.log(t.hasRef(), t[kCount], Object.keys(t), JSON.stringify(t))
console.log(t)
const u = new U()
u[kCount] = 9
console.log(u, u[kCount])
const a: any = t
console.log(a[kCount], kRefed in a, Object.keys(a))
a[kCount] = 11
console.log(t[kCount])
`
	assertOutput(t, src, `true 7
null 8 [ '_idleTimeout' ] {"_idleTimeout":5}
T { _idleTimeout: 5, Symbol(refed): null, Symbol(count): 8 }
U {
  _idleTimeout: 5,
  extra: 'u',
  Symbol(refed): true,
  Symbol(count): 9
} 9
8 true [ '_idleTimeout' ]
11`)
	assertSameAsNode(t, src)
}

// A computed member name bound to a const string or number literal names
// the member by its value; one bound to a const symbol keys a method.
func TestE2EComputedConstKeyedMembers(t *testing.T) {
	const src = `
const k = "dyn"
const n = 2
const kDescribe = Symbol('describe')
class Box {
  v = 3;
  [k](): number { return 1 }
  [n]: string = 'two';
  [kDescribe](prefix: string): string { return prefix + this.v }
}
class Big extends Box {
  [kDescribe](prefix: string): string { return 'big ' + super[kDescribe](prefix) }
}
const b = new Box()
console.log(b.dyn(), b[2], b[kDescribe]('v='), new Big()[kDescribe]('w='))
console.log(b, Object.keys(b))
const a: any = new Big()
console.log(a[kDescribe]('any '), typeof a[kDescribe])
`
	assertOutput(t, src, "1 two v=3 big w=3\nBox { '2': 'two', v: 3 } [ '2', 'v' ]\nbig any 3 function")
	assertSameAsNode(t, src)
}

func TestE2ELiteralClassMemberNames(t *testing.T) {
	// String and numeric literal member names, numeric keys named by their
	// value as JavaScript writes it, a const literal key, and a member
	// called through a literal index (TDD-00246).
	const src = `
class T { "a-b" = 1; 0 = 3; 1() { return 5 } "m x"() { return 6 } static 2 = 4; 0x10 = 16 }
const t = new T()
console.log(t["a-b"], t[0], t[1](), t["m x"](), T[2], t[16], Object.keys(t))
const o = { 0x10: 1, 1.50: 2, 1e3: 3 }
console.log(Object.keys(o), o[16], o[1.5], o["1000"])
console.log(t)
const k = "a-b"
function f(k: string) { return k.length }
console.log(t[k], f("xyz"))
`
	assertOutput(t, src, "1 3 5 6 4 16 [ '0', '16', 'a-b' ]\n[ '16', '1000', '1.5' ] 1 2 3\nT { '0': 3, '16': 16, 'a-b': 1 }\n1 3")
	assertSameAsNode(t, src)
}

func TestE2ESymbolReflection(t *testing.T) {
	// Symbol-keyed properties in Object.getOwnPropertySymbols,
	// Reflect.ownKeys, spread and Object.assign (TDD-00246, ADR-01421).
	const src = `
import { EventEmitter } from "events"
const kA = Symbol('a')
const kB = Symbol('b')
class C { x = 1; [kA]: number = 2; [kB] = "b" }
const c = new C()
const syms = Object.getOwnPropertySymbols(c)
console.log(syms.length, syms.map(s => s.toString()), syms[0] === kA)
const copy: any = { ...c }
console.log(copy, copy[kA], Object.keys(copy))
console.log(Reflect.ownKeys(c))
const o: any = Object.assign({}, c)
console.log(o[kB])
const k = Symbol('k')
const d: any = { a: 1 }
d[k] = 2
d.b = 3
console.log(Reflect.ownKeys(d), Object.getOwnPropertySymbols(d).length)
for (const key of Reflect.ownKeys(d)) console.log(typeof key)
const e = new EventEmitter()
e.on("a", () => {})
e.on(k, () => {})
console.log(e.eventNames())
`
	assertOutput(t, src, "2 [ 'Symbol(a)', 'Symbol(b)' ] true\n{ x: 1, Symbol(a): 2, Symbol(b): 'b' } 2 [ 'x' ]\n[ 'x', Symbol(a), Symbol(b) ]\nb\n[ 'a', 'b', Symbol(k) ] 1\nstring\nstring\nsymbol\n[ 'a', Symbol(k) ]")
	assertSameAsNode(t, src)
}

func TestE2ECompatJSMissingLiteralKey(t *testing.T) {
	// A constant key naming no field reads the property through the boxed
	// object: undefined, or the class's method (ADR-01420).
	assertOutputCompatJS(t, `
class T { a = 1; m() { return this.a + 1 } }
const t = new T()
console.log(t["nope"], typeof t["m"], t["a"])
const o = { x: 1 }
console.log(o["y"])
`, "undefined function 1\nundefined")
}

// A class extends a builtin error through its declaration's layout
// (`@lowerType error`): Error, its kinds and DOMException, each taking its
// constructor's second argument (options.cause; DOMException's name).
func TestE2EExtendsBuiltinErrorLayouts(t *testing.T) {
	assertSameAsNode(t, `
class AppError extends Error {
  code: number;
  constructor(m: string, code: number) { super(m); this.name = "AppError"; this.code = code; }
}
class Bad extends TypeError {}
class Quota extends DOMException {}
class Wrapped extends Error {}
const a = new AppError("boom", 7);
console.log(a instanceof Error, a instanceof AppError, a.message, a.code, a.name);
const b = new Bad("t");
console.log(b instanceof TypeError, b instanceof Error, b.message, b.name);
const q = new Quota("full", "QuotaExceededError");
console.log(q instanceof DOMException, q instanceof Error, q.name, q.message);
const q2 = new Quota("plain");
console.log(q2.name, q2.message);
const w = new Wrapped("outer", { cause: "inner" });
console.log(w.message, w.cause);
`)
}

// Builtin generics resolve through their declarations' layouts, an annotated
// module-level `new Set([…])` is readable from a function, and a program's own
// class named like a builtin shadows it.
func TestE2EBuiltinGenericLayouts(t *testing.T) {
	assertSameAsNode(t, `
const m: Map<string, number> = new Map([["a", 1]]);
const s: Set<number> = new Set([1, 2, 2]);
async function f(): Promise<number> { return 41; }
const p: Promise<number> = f();
const rs: ReadableStream<string> = new ReadableStream<string>({ start(c) { c.enqueue("x"); c.close(); } });
async function main(): Promise<void> {
  console.log(m.get("a"), s.size, s.has(2), (await p) + 1);
  const r = rs.getReader();
  const { value } = await r.read();
  console.log(value);
}
main();
`)
	assertSameAsNode(t, `
class Map { size = 7; get(k: string): string { return "mine:" + k; } }
const m: Map = new Map();
console.log(m.get("a"), m.size);
`)
}

// Classes extending Map and Set (TDD-00247): the instance is the host
// collection, through its own type, `any`, a Map parameter and console.log.
func TestE2EClassExtendsMapAndSet(t *testing.T) {
	assertSameAsNode(t, `
class Counter extends Map<string, number> {
  bump(k: string): number {
    const n = (this.get(k) ?? 0) + 1;
    this.set(k, n);
    return n;
  }
  total(): number {
    let t = 0;
    for (const v of this.values()) t += v;
    return t;
  }
}
const c = new Counter([["a", 5]]);
c.bump("a"); c.bump("b");
console.log(c.get("a"), c.get("b"), c.size, c.total(), c.has("b"));
console.log(c instanceof Counter, c instanceof Map);
for (const [k, v] of c) console.log(k, v);
function sizeOf(m: Map<string, number>): number { return m.size; }
console.log(sizeOf(c));
console.log(c);
`)
	assertSameAsNode(t, `
class Tags extends Set<string> {
  label: string;
  constructor(label: string, items: string[]) { super(items); this.label = label; }
  describe(): string { return this.label + ":" + [...this].join(","); }
}
const t = new Tags("t", ["x", "y"]);
t.add("z");
console.log(t.describe(), t.size, t.has("y"), t instanceof Set, t);
class Logged extends Map<string, number> {
  writes = 0;
  set(k: string, v: number): this { this.writes++; return super.set(k, v); }
}
class Deeper extends Logged { peek(): number { return this.get("a") ?? -1; } }
const d = new Deeper();
d.set("a", 1).set("b", 2);
console.log(d.writes, d.peek(), d.size, d instanceof Logged, d instanceof Map, d);
const any1: any = d;
console.log(any1 instanceof Map, any1 instanceof Deeper, any1.get("b"), any1.size);
console.log(any1);
`)
	assertSameAsNode(t, `
class Counter extends Map<string, number> { tag = "c"; }
const c = new Counter([["a", 1]]);
console.log({ c }, [c]);
console.log(JSON.stringify(c), Object.keys(c), String(c), Object.prototype.toString.call(c));
class Bag extends Set<number> {}
const b = new Bag([3, 1]);
console.log(Array.from(b), [...b].length, b.size, Math.max(...b));
const m2 = new Map<string, Counter>([["k", c]]);
console.log(m2);
`)
}

// A class extending Map renders as Node does past util.inspect's depth,
// `[C [Map]]`, statically and through `any`.
func TestE2EClassExtendsMapInspectDepth(t *testing.T) {
	assertSameAsNode(t, `
class C extends Map<string, number> {}
const c = new C([["a", 1]]);
console.log({ a: { b: { c } } });
console.log({ a: { b: { d: { c } } } });
const x: any = { a: { b: { d: { c } } } };
console.log(x);
console.log(new C());
`)
}

// TestE2ESuperPrivateNameIsEarlyError: `super.#x` is JavaScript's early
// SyntaxError, in a class declaration and a class expression alike (ADR-01459).
func TestE2ESuperPrivateNameIsEarlyError(t *testing.T) {
	for _, src := range []string{
		"class B {}\nclass C extends B { #x() {} m() { super.#x(); } }\n",
		"class B {}\nconst C = class extends B { #x = 1; m() { return super.#x; } };\n",
	} {
		_, err := parseAndCompile(src)
		if err == nil || !strings.Contains(err.Error(), "a private name cannot be read through 'super'") {
			t.Errorf("%q: want the super private-name error, got %v", src, err)
		}
	}
}

func TestE2EClassExtendsRegExp(t *testing.T) {
	// The instance is the RegExp (TDD-00247 stage 3): RegExp's methods and
	// properties, string methods taking it, `instanceof`, inspect, ToString,
	// and the same through `any`.
	assertSameAsNode(t, `class R extends RegExp {
  tag = "x";
  constructor(p: string, f?: string) { super(p, f); }
  hi(): string { return this.tag + this.source; }
  test(s: string): boolean { return super.test(s.trim()); }
}
class Plain extends RegExp {}
const r = new R("a+b", "g");
console.log(r);
console.log(new Plain("q"), [new Plain("z", "i")], { r });
console.log(Object.prototype.toString.call(r), String(r), `+"`"+`${r}`+"`"+`);
console.log(r.test(" aab "), r.lastIndex, r.hi(), r.global, r.flags);
r.lastIndex = 0;
console.log("xaabyab".replace(r, "-"), "aab-ab".split(new Plain("-")), "zab".match(new Plain("a(b)"))![1]);
const a: any = r;
console.log(a instanceof R, a instanceof RegExp, a.source, a === r);
function take(x: RegExp) { return x instanceof R; }
console.log(take(r), take(/x/));
`)
	assertSameAsNode(t, `class R extends RegExp {
  n = 1;
}
const a: any = new R("b", "g");
const p: any = /x(y)/;
console.log(a.test("abc"), a.lastIndex, a.flags, "abcb".split(a), "abab".replace(a, "_"));
console.log(p.exec("zxy")[1], p === p, String(a), typeof a, Object.prototype.toString.call(a));
const m = new Map<any, number>([[a, 1], [p, 2]]);
console.log(m.get(a), m.get(p), m.size);
console.log([a, p]);
const re2 = new RegExp(new R("q+", "i"));
console.log(re2, re2.flags);
`)
	assertSameAsNode(t, `class R extends RegExp { n = 1; }
const xs: any[] = [/a+/gi, new R("b", "m"), "c.d", undefined, 12];
for (const x of xs) { const r = new RegExp(x); console.log(r, r.flags, r.source); }
const y: any = /z/g;
console.log(new RegExp(y, "i"));
`)
	// Stored where its host is expected (a binding, an argument, a return,
	// a field, an array element) it is the host's body; narrowed back by
	// instanceof it is the instance.
	assertSameAsNode(t, `class R2 extends RegExp { k = 2; }
class C extends Map<string, number> { tag = "t"; }
function f() { const r: RegExp = new R2("x+"); console.log(r instanceof R2, r.source); }
f();
const g: RegExp = new R2("y");
console.log(g.source, g instanceof R2);
let h: RegExp = /z/;
h = new R2("w");
console.log(h.source, h instanceof R2);
function take(x: RegExp): string { return x.source; }
console.log(take(new R2("arg")));
function give(): RegExp { return new R2("ret"); }
console.log(give().source, give() instanceof R2);
const holder: { re: RegExp; m: Map<string, number> } = { re: new R2("fld"), m: new C() };
console.log(holder.re.source, holder.re instanceof R2, holder.m instanceof C);
const arr: RegExp[] = [new R2("a1"), /a2/];
console.log(arr[0].source, arr[0] instanceof R2, arr[1] instanceof R2);
const mm: Map<string, number> = new C();
mm.set("q", 1);
console.log(mm.size, mm instanceof C);
`)
	assertSameAsNode(t, `class C extends Map<string, number> { tag = "t"; }
class R2 extends RegExp { k = 2; }
const m: Map<string, number> = new C();
m.set("a", 1);
if (m instanceof C) console.log(m.tag, m.size, m.get("a"));
const r: RegExp = new R2("x+");
if (r instanceof R2) { console.log(r.k, r.source); }
function f(x: Map<string, number>): string { if (!(x instanceof C)) return "plain"; return x.tag; }
console.log(f(m), f(new Map()));
const t = m instanceof C ? m.tag : "no";
console.log(t);
`)
}

func TestE2EOverrideModifier(t *testing.T) {
	assertSameAsNode(t, `
class A { m(): number { return 1; } get x(): number { return 0; } }
class B extends A {
  override m(): number { return 2; }
  public override get x(): number { return 3; }
}
class C { override = 1; }
class D { override(): number { return 5; } }
class S { static k(): number { return 1; } }
class T extends S { static override k(): number { return 7; } }
console.log(new B().m(), new B().x, new C().override, new D().override(), T.k());
`)
	// tsc's checks: a base must declare the member (TS4113), and a class
	// without a base takes no `override` (TS4112).
	for src, want := range map[string]string{
		"class A { m(): void {} }\nclass B extends A { override n(): void {} }\n":   "not declared in the base class 'A'",
		"class A {}\nclass B extends A { override x = 1; }\n":                       "not declared in the base class 'A'",
		"class R extends RegExp { override zz(): void {} }\n":                       "not declared in the base class 'RegExp'",
		"class C { override m(): void {} }\n":                                       "does not extend another class",
		"class A { static s = 1; }\nclass B extends A { static override t = 1; }\n": "not declared in the base class 'A'",
	} {
		if _, err := parseAndCompile(src); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: want an error containing %q, got %v", src, want, err)
		}
	}
}

func TestE2EClassExtendsPromise(t *testing.T) {
	// The instance is the promise (TDD-00247 stage 3): await, instanceof,
	// console.log, and Promise's methods and statics making instances of the
	// class (species), through its constructor (a wrapping one included),
	// statically and through any.
	assertSameAsNode(t, `class P extends Promise<number> {
  tag = "p";
  label(): string { return "P:" + this.tag; }
}
class Q extends Promise<string> {}
async function main() {
  const p = new P((res) => res(1));
  console.log(p instanceof Promise, p instanceof P, p.label());
  console.log(await p);
  const q = new Q((_, rej) => rej(new Error("no")));
  try { await q; } catch (e) { console.log("caught", (e as Error).message); }
  function take(x: Promise<number>): boolean { return x instanceof P; }
  console.log(take(p), take(Promise.resolve(1)));
  const viaFn: Promise<number> = p;
  console.log(await viaFn);
}
main();
`)
	assertSameAsNode(t, `class P extends Promise<number> {
  tag = "p";
  label(): string { return "P:" + this.tag; }
}
let made = 0;
class Q extends Promise<number> {
  constructor(ex: (res: (v: number) => void, rej: (e: unknown) => void) => void) {
    made++;
    super((res, rej) => ex((v) => { console.log("resolving", v); res(v * 10); }, rej));
  }
}
async function main() {
  const p = new P((res) => res(1));
  console.log(p instanceof Promise, p instanceof P, p.label(), p);
  const t = p.then((v) => v + 1);
  console.log(t instanceof P, t instanceof Promise);
  console.log(await t, await p);
  const q = new Q((res) => res(2));
  const q2 = q.then((v) => v + 1);
  console.log(made, q2 instanceof Q, await q2);
  const c = new P((_, rej) => rej(new Error("no"))).catch((e: Error) => e.message.length);
  console.log(c instanceof P, await c);
  const all = await Promise.all([p, q, 3]);
  console.log(all);
  function take(x: Promise<number>): boolean { return x instanceof P; }
  console.log(take(p), take(Promise.resolve(1)));
  const r = P.resolve(5);
  console.log(r instanceof P, await r);
}
main();
`)
	assertSameAsNode(t, `let made = 0;
class L extends Promise<number> {
  constructor(ex: (res: (v: number) => void, rej: (e: unknown) => void) => void) {
    made++;
    super(ex);
  }
}
class O extends Promise<number> {
  then<A = number, B = never>(f?: ((v: number) => A) | null, r?: ((e: any) => B) | null): Promise<A | B> {
    console.log("own then");
    return super.then(f, r);
  }
}
async function main() {
  const p = new L((res) => res(2));
  const f = p.finally(() => console.log("fin"));
  console.log(await f, made);
  console.log(await L.all([p, 5]), made);
  console.log(await L.race([p, new L(() => {})]), made);
  console.log(await L.allSettled([p, L.reject("x")]), made);
  console.log(await L.any([L.reject(new Error("a")), p]), made);
  try { await L.any([L.reject(new Error("a"))]); } catch (e) { console.log((e as Error).constructor.name, made); }
  const o = new O((res) => res(9));
  console.log(await o);
  const a: any = p;
  const t = a.then((v: number) => v * 3);
  console.log(t instanceof L, await t, made);
  const fa = a.finally(() => {});
  console.log(fa instanceof L, await fa);
}
main();
`)
	assertSameAsNode(t, `class P extends Promise<number> {}
async function main() {
const p = new P((res) => res(1));
const f = p.finally(() => console.log("fin"));
console.log(f instanceof P, await f);
}
main();
`)
	assertSameAsNode(t, `class P extends Promise<number> { tag = "p"; }
class E extends Promise<string> {}
const p = new P((res) => res(1));
console.log(p);
console.log(new P(() => {}));
const r = new E((_, rej) => rej("bad"));
r.catch(() => {});
console.log(r, [p], { p });
`)
}

// -compat=js: an instance takes properties and methods of its own, and a
// call prefers its own method over its class's (BACKLOG 62).
func TestJSInstanceOwnPropertiesAndMethods(t *testing.T) {
	assertSameAsNodeCompatJS(t, `
class R {
  constructor() { this._read = function () { return "own"; }; }
  _read() { return "proto"; }
}
const r = new R();
console.log(r._read());
r._read = () => "reassigned";
console.log(r._read());
class Q { go() { return 1; } }
const q = new Q();
q.go = function () { return 2; };
console.log(q.go(), new Q().go());
q.extra = 5;
q.fn = function () { return 3; };
q.extra += 1;
console.log(q.extra, q.fn(), Object.keys(q), new Q().extra);
`)
}

// The strict lane: a method assigned on an instance (directly or through
// any) is called, and for...in lists added properties (BACKLOG 91).
func TestInstanceOwnMethodAndForInStrict(t *testing.T) {
	assertSameAsNode(t, `
class P { m(): string { return "proto"; } x = 1; }
const p = new P();
(p as any).m = () => "own";
(p as any).extra = 5;
console.log(p.m(), new P().m());
const keys: string[] = [];
for (const k in p) keys.push(k);
console.log(keys, Object.keys(p));
const q = new P();
q.m = () => "assigned";
console.log(q.m());
`)
}

// `implements` is the checker's (TS2420); a generic interface implemented
// with type arguments compiles, and a mismatch is rejected (BACKLOG 155).
func TestClassImplementsIsCheckedNotEmitted(t *testing.T) {
	assertSameAsNode(t, `
interface G<T> { v: T }
class D implements G<number> { v = 1; }
interface I { a: number }
class E implements I { a = 2; extra = "x"; }
console.log(new D().v, new E().a)
`)
	if _, err := parseAndCompile("interface I { a: number }\nclass C implements I { a = 's' }\n"); err == nil || !strings.Contains(err.Error(), "incorrectly implements interface 'I'") {
		t.Errorf("expected TS2420, got %v", err)
	}
}

// A generic class construction infers its type arguments from its
// arguments or, when none names a parameter, the contextual type (BACKLOG
// 137).
func TestGenericClassConstructionInference(t *testing.T) {
	assertSameAsNode(t, `
class Box<T> { v: T; constructor(v: T) { this.v = v; } get(): T { return this.v; } }
const arr = new Box([1, 2]);
const nested = new Box(new Box(true));
function mk<U>(u: U) { return new Box(u); }
console.log(arr.get().length, nested.get().get(), mk("z").get());
class Stack<T> { items: T[] = []; push(x: T) { this.items.push(x); return this; } }
const st: Stack<number> = new Stack();
st.push(3);
console.log(st.items);
`)
}

// A method returning an array, called where an own method of its name may
// exist: both results are arrays (the httpbin fixture caught invalid IR).
func TestOwnMethodCheckArrayResult(t *testing.T) {
	assertSameAsNode(t, `
class L { items(): number[] { return [1, 2]; } name(): string { return "n"; } }
const a = new L();
const b = new L();
(b as any).items = () => [9];
console.log(a.items(), b.items(), a.items().length, a.name());
`)
}

// A static setter receives a nullable argument as null, not its payload,
// and a class declared in a block initializes its statics there: one that
// throws is caught by the try around it.
func TestE2EStaticSetterNullAndBlockStatics(t *testing.T) {
	assertOutput(t, `class C { static set a(_ = null) { console.log("set", _) } }
C.a = null
class E extends Error {}
function initThrows(): never { throw new E("x") }
try { class D { static f = initThrows() } } catch (e) { console.log("caught", e instanceof E) }
`, "set null\ncaught true")
}

// A method assigned on an instance of a library class is the one the
// library's own code calls (`r._read = fn` on a Readable, `w._write`).
func TestE2ELibraryInstanceOwnMethod(t *testing.T) {
	assertSameAsNodeImports(t, `
import { Readable, Writable } from 'stream'
const r = new Readable()
let i = 0
r._read = function (n) { r.push(i++ < 3 ? Buffer.from('a') : null) }
r.on('data', (d: Buffer) => console.log('data', d.length))
r.on('end', () => console.log('end'))
const w = new Writable()
w._write = (chunk: any, enc: string, cb: () => void) => { console.log('write', String(chunk)); cb() }
w.end('x')
`)
}

// A library call that passes a function literal also honours the
// instance's own method: the agent's own createSocket gets the request's
// callback, and the error it passes is the one the request reports.
func TestE2ELibraryOwnMethodWithFunctionLiteralArg(t *testing.T) {
	assertSameAsNodeImports(t, `
import http from 'http'
const agent = new http.Agent()
const _err = new Error('kaboom')
;(agent as any).createSocket = function (req: any, options: any, cb: (e: Error) => void) { cb(_err) }
const req = http.request({ agent })
req.on('error', (err) => console.log('error same', err === _err))
req.on('close', () => console.log('close destroyed', req.destroyed))
`)
}

func TestE2EClassSpecies(t *testing.T) {
	// A static [Symbol.species] getter picks the constructor of a derived
	// array or promise: the builtin itself, another subclass, or the class.
	assertSameAsNode(t, `class Plain extends Array<number> {
  static get [Symbol.species]() { return Array; }
}
class Other extends Array<number> {
  tag(): string { return "other"; }
}
class ToOther extends Array<number> {
  static get [Symbol.species]() { return Other; }
}
class Self extends Array<number> {
  static get [Symbol.species]() { return this; }
}
class Child extends Plain {}
const p = new Plain(); p.push(1, 2, 3);
const pm = p.map((x) => x * 2);
console.log("Plain", pm instanceof Plain, Array.isArray(pm), pm.length, pm[2]);
const t = new ToOther(); t.push(4, 5);
const tf = t.filter((x) => x > 4);
console.log("ToOther", tf instanceof ToOther, tf instanceof Other, tf.length, tf[0]);
const s = new Self(); s.push(7);
console.log("Self", s.slice(0) instanceof Self);
const c = new Child(); c.push(9);
console.log("Child", c.map((x) => x) instanceof Child, c.map((x) => x) instanceof Plain);
`)
	assertSameAsNode(t, `class P extends Promise<number> {
  static get [Symbol.species]() { return Promise; }
}
class Q extends Promise<number> {}
class R extends Promise<number> {
  static get [Symbol.species]() { return Q; }
}
const p = new P((r) => r(1));
const pt = p.then((x) => x + 1);
console.log("P", pt instanceof P, pt instanceof Promise);
const q = new Q((r) => r(2));
console.log("Q", q.then((x) => x) instanceof Q);
const r = new R((res) => res(3));
const rt = r.then((x) => x * 10);
console.log("R", rt instanceof R, rt instanceof Q);
const pf = p.finally(() => {});
console.log("Pf", pf instanceof P);
pt.then((v) => console.log("value", v));
rt.then((v) => console.log("rvalue", v));
pf.then((v) => console.log("fvalue", v));
`)
	mustCompileError(t, `class A extends Array<number> {
  static get [Symbol.species]() { const x = 1; return x > 0 ? Array : A; }
}
const a = new A(); a.push(1);
console.log(a.map((x) => x).length);
`, "[Symbol.species] must return")
}

// A class with only `next()` is not iterable: tsc's TS2488, and under
// -compat=js Node's TypeError from for…of and an empty array-like read from
// Array.from.
func TestE2EForOfClassNextOnlyNotIterable(t *testing.T) {
	const src = `class C {
  i = 0;
  next(): number | null { return this.i < 2 ? this.i++ : null; }
}
const c = new C();
try { for (const x of c) console.log(x); } catch (e) { console.log(String(e)); }
console.log(Array.from(c as any).length);
`
	mustCompileError(t, src, "must have a '[Symbol.iterator]()' method")
	assertSameAsNodeCompatJS(t, src)
}

// A static field declared without an initializer is undefined until
// assigned: reads, arithmetic and logical assignment see undefined.
func TestE2EClassStaticFieldUninitialized(t *testing.T) {
	assertSameAsNode(t, `class C {
  static n: number;
  static s: string;
  static #p: number;
  static a: number;
  static b: number;
  static c: number;
  static bump(): number { C.#p = C.#p + 1; return C.#p; }
}
console.log(C.n, C.s, C.n + 1, C.bump());
C.a ||= 5; C.b ??= 6; C.c &&= 7;
console.log(C.a, C.b, C.c);
C.n = 3;
console.log(C.n * 2);
`)
}

// A call taking the own-method check with a function literal nested in an
// argument: the instance's own method runs, and the arguments are
// evaluated once, left to right.
func TestE2EOwnMethodNestedFunctionLiteralArg(t *testing.T) {
	assertSameAsNode(t, `
class A { run(n: number, o: { cb: () => number }) { console.log("class", n, o.cb()); } }
const a = new A();
let x = 1;
a.run(x++, { cb: () => x * 10 });
(a as any).run = function (n: number, o: { cb: () => number }) { console.log("own", n, o.cb()); };
a.run(x++, { cb: () => x * 10 });
`)
}

// Object.assign and Object.defineProperties through any read the own
// enumerable symbol keys after the string keys (ADR-01718).
func TestE2ESymbolKeysAssignDefineProperties(t *testing.T) {
	assertSameAsNode(t, `const k = Symbol("k"), j = Symbol("j");
const src: any = { a: 1, [k]: 2 };
const t: any = Object.assign({}, src);
console.log(t[k], Object.getOwnPropertySymbols(t).length);
const o: any = {};
Object.defineProperties(o, { x: { value: 1, enumerable: true }, [j]: { value: 3, enumerable: true } });
console.log(o.x, o[j], Object.getOwnPropertySymbols(o).length);
const props: any = { [k]: { value: 5 } };
const p: any = Object.defineProperties({}, props);
console.log(p[k]);
const typed = Object.assign({ b: 0 }, { [k]: 7 });
console.log((typed as any)[k]);
{
  const k = Symbol("k"), h = Symbol("h");
  const props: any = { [k]: { value: 1, enumerable: true } };
  Object.defineProperty(props, h, { value: { value: 2 }, enumerable: false });
  const o: any = Object.defineProperties({}, props);
  console.log(o[k], o[h], Object.getOwnPropertySymbols(o).length);
  const src: any = { [k]: 1 };
  Object.defineProperty(src, h, { value: 2, enumerable: false });
  const t: any = Object.assign({}, src);
  console.log(t[k], t[h]);
}
`)
}

// new.target: the class new named in a constructor (a derived class through
// super(), before super() too, and in an arrow), undefined in a method, a
// static method and a plain call.
func TestE2ENewTarget(t *testing.T) {
	assertSameAsNode(t, `
export {}
class A {
  kind: string
  constructor() {
    this.kind = new.target.name
    const arrow = () => new.target === A
    console.log("A ctor", new.target.name, arrow())
  }
  m() { return new.target === undefined }
  static s() { return typeof new.target }
}
class B extends A {
  constructor() {
    console.log("before super", new.target.name)
    super()
  }
}
const a = new A(), b = new B()
console.log(a.kind, b.kind, a.m(), A.s())
function f() { return new.target === undefined ? "call" : "new" }
console.log(f())
`)
}

// new.target in a -compat=js prototype constructor: the function through new
// (an arrow sees it), undefined through Base.call(this) and in a nested
// function.
func TestE2ENewTargetPrototypeConstructor(t *testing.T) {
	assertSameAsNodeCompatJS(t, `
function Animal(name) {
  this.name = name
  this.viaNew = new.target === Animal
  const inner = () => new.target === Animal
  this.arrow = inner()
}
function Dog(name) { Animal.call(this, name); this.dogNew = new.target === Dog }
Dog.prototype = Object.create(Animal.prototype)
const a = new Animal("a"), d = new Dog("d")
console.log(a.viaNew, a.arrow, d.viaNew, d.dogNew, d.name)
function plain() { return new.target === undefined }
console.log(plain())
function Outer() {
  this.own = new.target === Outer
  const self = this
  this.inner = function () { return new.target === undefined }
  function nested() { return new.target === undefined }
  this.nested = nested()
}
const o = new Outer()
console.log(o.own, o.inner(), o.nested)
`)
}

func TestE2ENewTargetAtTopLevelRejected(t *testing.T) {
	mustCompileError(t, "console.log(new.target)\n", "new.target expression is not allowed here")
}

// An abstract class constructs through any, as in JavaScript: its fields and
// concrete methods are there, an abstract method is absent.
func TestE2EAbstractClassConstructedThroughAny(t *testing.T) {
	assertSameAsNode(t, `
export {}
abstract class Base {
  x = 1
  abstract area(): number
  hello() { return "hi " + this.x }
}
class Sq extends Base { area() { return 4 } }
const C: any = Base
const b = new C()
console.log(b.x, b.hello(), typeof b.area, b instanceof Base)
try { b.area() } catch (e: any) { console.log(e.constructor.name) }
console.log(new Sq().area())
`)
}

func TestE2EClassExpressionReadsModuleNames(t *testing.T) {
	assertSameAsNode(t, `
function foo(x: number) { return x + 1; }
const bar = (x: number) => x * 2;
const C = class { m() { return foo(1) + bar(2); } };
console.log(new C().m());
function mk() { return class { m() { return foo(10); } }; }
const K = mk();
console.log(new K().m());
var N = class Named { static k = 3; m() { return foo(Named.k); } };
console.log(new N().m());
`)
}

func TestE2EClassInstanceComputedKey(t *testing.T) {
	assertSameAsNode(t, `
class C { x = 1; y = 2; }
const c = new C();
const ks: (keyof C)[] = ["x", "y"];
for (const k of ks) console.log(k, c[k] + 1);
for (const k of ks) c[k] += 10;
console.log(c.x, c.y);
`)
}

func TestE2EUnannotatedAsyncMethodPromise(t *testing.T) {
	// An unannotated async method returns a Promise (ADR-01768).
	assertSameAsNode(t, `
class C { async foo() { return 1; } async bar(n: number) { await 0; return n * 2; } }
const o = new C();
o.foo().then((r) => console.log("then", r));
async function main() { const v = await o.bar(4); console.log("await", v, o.foo() instanceof Promise); }
main();
`)
}

func TestE2EStaticMethodReceiver(t *testing.T) {
	// A static method's `this` is its call's receiver: a subclass, a value
	// passed to .call, the class `super.m()` runs for (ADR-01821).
	assertSameAsNode(t, `
class C {
  static tag = "C";
  static #p = 1;
  static #m() { return "pm"; }
  static who(): string { return this.tag; }
  static make() { return new this(); }
  static priv() { this.#p++; return this.#p + this.#m(); }
  static *gen() { yield this.tag; }
  static get t() { return "get:" + this.tag; }
  name() { return "c"; }
}
class D extends C { static tag = "D"; static who(): string { return "D>" + super.who(); } name() { return "d"; } }
console.log(C.who(), D.who(), C.make().name(), D.make().name(), C.priv(), [...D.gen()].join(), D.t);
for (const f of [() => D.priv(), () => (C.priv as any).call({})]) {
  try { f(); } catch (e) { console.log((e as Error).constructor.name, (e as Error).message); }
}
console.log(C.who.call({ tag: "obj" }), C.who.length);
class E { static n = 0; static inc() { return ++this.n; } }
console.log(E.inc(), E.inc());
`)
}

func TestE2EPrivateNameIn(t *testing.T) {
	// `#x in o` tests the brand C's constructor gave o (ADR-01822).
	assertSameAsNode(t, `
class C {
  #x = 1;
  static #s = 2;
  #m() {}
  static has(o: any) { return #x in o; }
  static hasM(o: object) { return #m in o && !(#s in o); }
  static hasS(o: any) { return #s in o; }
}
class D extends C {}
console.log(C.has(new C()), C.has(new D()), C.has({}), C.has(Object.create(C.prototype)), C.hasM(new C()), C.hasS(C), C.hasS(D));
try { C.has(1); } catch (e) { console.log((e as Error).constructor.name, (e as Error).message); }
`)
}

// A nested class reads its outer class's private field off an object: the
// name is the outer class's, and an object without its brand throws V8's
// TypeError.
func TestE2ENestedClassReadsOuterPrivate(t *testing.T) {
	assertSameAsNodeCompatJS(t, `
var C = class {
  #outer = 'test262';
  B = class { #inner = 42; method(o) { return o.#outer; } get inner() { return this.#inner; } };
};
let c = new C();
let b = new c.B();
console.log(b.method(c), b.inner);
try { b.method({}); } catch (e) { console.log(e.constructor.name, e.message); }
try { b.method(b); } catch (e) { console.log(e.constructor.name, e.message); }
`)
	assertSameAsNode(t, `
class C {
  #outer = 'test262';
  B = class { method(o: C) { return o.#outer; } };
}
const c = new C();
console.log(new c.B().method(c));
`)
	// The outer class declared in a function, and a block class whose
	// field holds a class expression.
	assertSameAsNodeCompatJS(t, `
function make() {
  class C {
    #outer = 'deep';
    B = class { method(o) { return o.#outer; } };
  }
  return new C();
}
var c = make();
var b = new c.B();
console.log(typeof c.B, b.method(c));
try { b.method({}); } catch (e) { console.log(e.constructor.name); }
{ class D { E = class { hi() { return "hi"; } }; } var d = new D(); console.log(typeof d.E, new d.E().hi()); }
`)
}

// A conditional between a derived class's instance and its base's is the
// base (tsc's subtype reduction): its members read through the base, a
// derived member is TS2339, and the value keeps its own class.
func TestE2EConditionalDerivedInstanceReduced(t *testing.T) {
	assertSameAsNode(t, `
class A { a = 1; hi() { return 'A'; } }
class B extends A { b = 2; hi() { return 'B' + this.b; } }
function pick(c: boolean) { return c ? new B() : new A(); }
const x = pick(true), y = pick(false);
console.log(x.a, x.hi(), x instanceof B, x);
console.log(y.a, y.hi(), y instanceof B, y);
const z = Math.random() > 2 ? new A() : new B();
console.log(z.hi(), (z as B).b, z.constructor.name);
`)
	mustCompileError(t, `
class A { a = 1; }
class B extends A { b = 2; }
export const x = Math.random() > 2 ? new B() : new A();
console.log(x.b);
`, "property 'b' does not exist on type 'A'")
}

// A method returning `this` returns TypeScript's polymorphic `this` type:
// a call through a subclass's instance is that subclass, and an object
// literal's method returns the literal.
func TestE2EReturnThisPolymorphic(t *testing.T) {
	assertSameAsNode(t, `
class A { x = 1; m() { return this; } set(v: number) { this.x = v; return this; } }
class B extends A { y = 2; }
const b = new B().m();
console.log(b.y, b.set(5).y, b.x, b instanceof B);
const o = { n: 3, self() { return this; }, inc() { this.n++; return this; } };
console.log(o.self().n, o.inc().inc().n);
const chain = new B().set(7).set(8);
console.log(chain.x, chain.y, chain.constructor.name);
`)
}

// A class extending a generic base without type arguments takes the base's
// defaults; without a constructor of its own it constructs through the
// base's.
func TestE2EExtendsDefaultTypeArgs(t *testing.T) {
	assertSameAsNode(t, `
interface Opts { n: number }
class Base<T extends object = Opts> {
  cfg?: T;
  constructor(cfg?: T) { this.cfg = cfg; }
  has(): boolean { return this.cfg !== undefined; }
}
class Derived extends Base {
  tag = "d";
  describe(): string { return this.tag + ":" + (this.cfg ? this.cfg.n : -1); }
}
const d = new Derived({ n: 3 });
const e = new Derived();
console.log(d.describe(), e.describe(), d.has(), e.has(), d instanceof Base);
`)
}

// An interface field typed `typeof C` for a class declared after the
// interface holds the constructor reference.
func TestE2ETypeofClassFieldBeforeClass(t *testing.T) {
	assertSameAsNode(t, `
interface Opts { make?: typeof Animal; n: number }
function build(o: Opts): Animal {
  const K = o.make ?? Animal;
  return new K(o.n);
}
class Animal {
  legs: number;
  constructor(n: number) { this.legs = n; }
  kind(): string { return "animal"; }
}
class Bird extends Animal {
  kind(): string { return "bird"; }
}
const a = build({ n: 4 });
const b = build({ make: Bird, n: 2 });
console.log(a.kind(), a.legs, b.kind(), b.legs, b instanceof Bird);
`)
}

// TestE2EArgumentsInPatternDefaults: a destructured parameter's defaults
// see the function's `arguments`, in functions, function expressions,
// methods and constructors; a constructor's `arguments` holds the overflow
// too, and `arguments` holds the destructured argument itself.
func TestE2EArgumentsInPatternDefaults(t *testing.T) {
	assertOutputCompatJS(t, `
function f({t = arguments}) { return t.length; }
console.log(f({}, 2));
console.log((function ({t = arguments}, [u = arguments.length]) { return arguments.length + u + t[0].k; })({k: 4}, [], 9));
function g([a, b = arguments[2]]) { return a + b; }
console.log(g([1], 0, 5));
class C { m({t = arguments}, [u = arguments.length]) { return arguments.length + u + t[0].k; } }
console.log(new C().m({k: 4}, [], 9));
class D { constructor(x) { this.a = arguments[1]; this.n = arguments.length; } }
var d = new D({}, 7);
console.log(d.a, d.n);
class G { constructor({a = arguments[1]}) { this.a = a; } }
console.log(new G({}, 7).a);
`, "2\n10\n6\n10\n7 2\n7")
}

// TestE2ENewClassExpressionAndValuelessGetter: `new class {…}()` constructs
// the class expression; a JavaScript getter falling off its end is undefined.
func TestE2ENewClassExpressionAndValuelessGetter(t *testing.T) {
	assertOutputCompatJS(t, `
var g;
var o = new class { m() { return 7; } get a() { g = 1; } }();
console.log(o.m(), o.a, g);
var p = { get b() { g = 2; } };
console.log(p.b, g);
`, "7 undefined 1\nundefined 2")
	assertOutput(t, `
var o = new class { m(): number { return 7; } }();
console.log(o.m());
`, "7")
}

// TestE2EEvalArgumentsInFieldInitializer: a field initializer's eval code
// reading `arguments` throws its SyntaxError when it runs; inside a
// function-valued field it reads that function's arguments.
func TestE2EEvalArgumentsInFieldInitializer(t *testing.T) {
	// Through the resolver, which inlines static eval strings.
	assertMultiFileOutputPermissive(t, map[string]string{"main.js": `
class C {
  x = eval('() => arguments;');
  f = function () { return eval("arguments.length"); };
}
try { new C(); console.log("constructed"); } catch (e) { console.log(e instanceof SyntaxError); }
class D { f = function () { return eval("arguments.length"); }; }
console.log(new D().f(1, 2));
`}, "main.js", "true\n2")
}

// TestE2EMethodAssignedMemberInFieldInit: a member a class only assigns in
// a setter, read by a field initializer, holds whatever was assigned.
func TestE2EMethodAssignedMemberInFieldInit(t *testing.T) {
	assertOutputCompatJS(t, `
class C {
  set m(v) { this._v = v; }
  w = (this.m = "str", this._v);
  put(x) { this._q = x; }
  static f() { return this.#g(); }
  static #g() { return 42.5; }
}
const c = new C();
console.log(c.w);
c.put(2.5); console.log(c._q);
c.put("s"); console.log(c._q);
console.log(C.f());
`, "str\n2.5\ns\n42.5")
}

// A Promise subclass's statics construct the subclass, and its combinators
// take members of mixed kinds: a promise member is adopted, a plain value
// resolved, and an inherited static is typed off the base constructor.
func TestE2EPromiseSubclassCombinatorsMixedMembers(t *testing.T) {
	assertSameAsNode(t, `
class Counted extends Promise<number> {}
async function main() {
  console.log(await Counted.all([1, 2]));
  console.log(await Counted.all([1, Promise.resolve(3)]));
  console.log(await Counted.all([Promise.resolve(3), 1]));
  console.log(await Counted.all(["a", Promise.resolve("b")]));
  console.log(await Counted.race([Promise.resolve(3), 1]));
  console.log(await Counted.allSettled([1, Promise.resolve(3)]));
  const x = Counted.resolve(5);
  console.log(x instanceof Counted, await x);
  const d = Counted.resolve(21).then((n) => n * 2);
  console.log(d instanceof Counted, await d);
}
main();
`)
}
