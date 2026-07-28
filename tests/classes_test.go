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
