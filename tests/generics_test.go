package tests

import (
	"strings"
	"testing"
)

// --- User-defined generics: functions, interfaces, classes (TDD-00010 V1) ---

func TestE2EGenericFunctionMultipleInstantiations(t *testing.T) {
	assertOutput(t, `
function identity<T>(x: T): T {
  return x;
}
console.log(identity(5));
console.log(identity("hello"));
console.log(identity(true));
`, "5\nhello\n1")
}

// A generic function called twice with the *same* concrete type must be
// specialized once, not twice — inspects --emit-llvm-equivalent IR directly
// (via parseAndCompile) rather than just checking output, since two
// identical, wrongly-duplicated `define`s for the same mangled name would
// otherwise still produce correct output but be a real, silent codegen bug
// (a duplicate LLVM symbol, which clang would actually reject at link time).
func TestE2EGenericFunctionMemoizedAcrossSameTypeCalls(t *testing.T) {
	ir, err := parseAndCompile(`
function identity<T>(x: T): T {
  return x;
}
console.log(identity(1));
console.log(identity(2));
console.log(identity(3));
`)
	if err != nil {
		t.Fatalf("codegen: %v", err)
	}
	if n := strings.Count(ir, "define i64 @identity__num("); n != 1 {
		t.Fatalf("expected exactly one specialization of identity for number, got %d\n%s", n, ir)
	}
}

func TestE2EGenericFunctionResultInUnannotatedConst(t *testing.T) {
	assertOutput(t, `
function identity<T>(x: T): T {
  return x;
}
const y = identity("hi");
console.log(y);
`, "hi")
}

func TestE2EGenericFunctionArrayOfScalar(t *testing.T) {
	assertOutput(t, `
function first<T>(arr: T[]): T {
  return arr[0];
}
const nums: number[] = [1, 2, 3];
const strs: string[] = ["a", "b"];
console.log(first(nums));
console.log(first(strs));
`, "1\na")
}

func TestE2EGenericFunctionRecursion(t *testing.T) {
	assertOutput(t, `
function count<T>(x: T, n: number): number {
  if (n <= 0) return 0;
  return 1 + count(x, n - 1);
}
console.log(count("a", 3));
console.log(count(1, 2));
`, "3\n2")
}

func TestE2EGenericFunctionNoInferableParamRejected(t *testing.T) {
	_, err := parseAndCompile(`
function make<T>(): T {
  return 0 as any;
}
console.log(make());
`)
	if err == nil {
		t.Fatal("expected a compile error for a generic function with no parameter to infer T from, got none")
	}
}

func TestE2EGenericFunctionUnsupportedConcreteTypeRejected(t *testing.T) {
	_, err := parseAndCompile(`
function identity<T>(x: T): T { return x; }
interface Foo { a: number }
const f: Foo = { a: 1 };
console.log(identity(f));
`)
	if err == nil {
		t.Fatal("expected a compile error instantiating a generic function with an unsupported (object) type argument, got none")
	}
}

func TestE2EGenericMultipleTypeParamsRejected(t *testing.T) {
	_, err := parseAndCompile(`function pair<T, U>(a: T, b: U): T { return a; }`)
	if err == nil {
		t.Fatal("expected a parse error for a multi-parameter generic function (out of V1 scope), got none")
	}
}

func TestE2EGenericInterfaceMultipleInstantiations(t *testing.T) {
	assertOutput(t, `
interface Box<T> {
  value: T;
}
const bn: Box<number> = { value: 42 };
const bs: Box<string> = { value: "hi" };
console.log(bn.value);
console.log(bs.value);
`, "42\nhi")
}

func TestE2EGenericClassMultipleInstantiations(t *testing.T) {
	assertOutput(t, `
class Box<T> {
  value: T;
  constructor(v: T) {
    this.value = v;
  }
  get(): T {
    return this.value;
  }
}
const a = new Box<number>(1);
const b = new Box<number>(2);
const c = new Box<string>("x");
console.log(a.get());
console.log(b.get());
console.log(c.get());
`, "1\n2\nx")
}

// A generic class instantiated twice with the *same* concrete type must be
// specialized once — same reasoning/risk as
// TestE2EGenericFunctionMemoizedAcrossSameTypeCalls.
func TestE2EGenericClassMemoizedAcrossSameTypeConstructions(t *testing.T) {
	ir, err := parseAndCompile(`
class Box<T> {
  value: T;
  constructor(v: T) { this.value = v; }
}
const a = new Box<number>(1);
const b = new Box<number>(2);
console.log(a.value);
console.log(b.value);
`)
	if err != nil {
		t.Fatalf("codegen: %v", err)
	}
	if n := strings.Count(ir, "define void @Box__num_constructor("); n != 1 {
		t.Fatalf("expected exactly one specialization of Box's constructor for number, got %d\n%s", n, ir)
	}
}

func TestE2EGenericClassRequiresExplicitTypeArgument(t *testing.T) {
	_, err := parseAndCompile(`
class Box<T> {
  value: T;
  constructor(v: T) { this.value = v; }
}
const b = new Box(5);
`)
	if err == nil {
		t.Fatal("expected a compile error constructing a generic class with no explicit type argument, got none")
	}
}

func TestE2EGenericClassExtendsRejected(t *testing.T) {
	_, err := parseAndCompile(`
class Base {}
class Box<T> extends Base {
  value: T;
  constructor(v: T) { this.value = v; }
}
`)
	if err == nil {
		t.Fatal("expected a compile error for a generic class using 'extends' (out of V1 scope), got none")
	}
}

// --- User-defined generics: `@erased` type erasure (TDD-00010 V2) ---

func TestE2EErasedGenericFunctionPassThrough(t *testing.T) {
	assertOutput(t, `
/** @erased */
function identity<T>(x: T): T {
  return x;
}
console.log(identity(5));
console.log(identity("hello"));
console.log(identity(true));
`, "5\nhello\ntrue")
}

// Unlike a V1 (monomorphized) generic, an `@erased` function compiles its
// body exactly once regardless of how many distinct concrete types call it —
// inspects --emit-llvm-equivalent IR directly (via parseAndCompile), same
// reasoning as TestE2EGenericFunctionMemoizedAcrossSameTypeCalls, but here
// checking for exactly one *unmangled* symbol rather than one memoized
// instantiation per type.
func TestE2EErasedGenericFunctionSingleCompiledSymbol(t *testing.T) {
	ir, err := parseAndCompile(`
/** @erased */
function identity<T>(x: T): T {
  return x;
}
console.log(identity(1));
console.log(identity("a"));
console.log(identity(true));
`)
	if err != nil {
		t.Fatalf("codegen: %v", err)
	}
	if n := strings.Count(ir, "define { i8, i64 } @identity("); n != 1 {
		t.Fatalf("expected exactly one compiled symbol for identity, got %d\n%s", n, ir)
	}
	if strings.Contains(ir, "@identity__") {
		t.Fatalf("expected no V1-style mangled instantiation of an @erased function, got one\n%s", ir)
	}
}

func TestE2EErasedGenericFunctionMixedConcreteAndErasedParams(t *testing.T) {
	assertOutput(t, `
/** @erased */
function wrap<T>(label: string, x: T): T {
  console.log(label);
  return x;
}
console.log(wrap("num:", 42));
console.log(wrap("str:", "hi"));
`, "num:\n42\nstr:\nhi")
}

func TestE2EErasedGenericFunctionResultInUnannotatedConst(t *testing.T) {
	assertOutput(t, `
/** @erased */
function identity<T>(x: T): T {
  return x;
}
const y = identity("hi");
console.log(y);
`, "hi")
}

func TestE2EErasedOnNonGenericFunctionRejected(t *testing.T) {
	_, err := parseAndCompile(`
/** @erased */
function plain(x: number): number { return x; }
`)
	if err == nil {
		t.Fatal("expected a parse error for @erased on a function with no type parameter, got none")
	}
}

// T[] is deliberately out of V2's minimal scope (only a bare T parameter/
// return position is substituted to TypeAny) — must be a clean compile error,
// not a silent miscompile of a dynamic-element array.
func TestE2EErasedGenericFunctionArrayOfTRejected(t *testing.T) {
	_, err := parseAndCompile(`
/** @erased */
function first<T>(arr: T[]): T { return arr[0]; }
`)
	if err == nil {
		t.Fatal("expected a compile error for @erased with a T[] parameter (out of V2 scope), got none")
	}
}

// Arithmetic on an erased T hits the same, pre-existing "operators on any/
// unknown are rejected" wall the TDD calls out as V2's real ceiling — not a
// new check, just confirming it actually fires for this new position.
func TestE2EErasedGenericFunctionArithmeticOnTRejected(t *testing.T) {
	_, err := parseAndCompile(`
/** @erased */
function add<T>(a: T, b: T): T { return a + b; }
`)
	if err == nil {
		t.Fatal("expected a compile error for arithmetic on an erased T, got none")
	}
}
