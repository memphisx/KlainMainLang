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
`, "5\nhello\ntrue")
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
	if n := strings.Count(ir, "define double @identity__num("); n != 1 {
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

func TestE2EGenericObjectArgInterfaceClassAndArray(t *testing.T) {
	// Named interface arg, T[] arg, and a generic class over an object arg
	// (TDD-00069), alongside scalar instantiations of the same function.
	assertOutput(t, `
interface Point { x: number; y: number; }
class Box<T> { v: T; constructor(v: T) { this.v = v; } get(): T { return this.v; } }
function identity<T>(x: T): T { return x; }
function first<T>(xs: T[]): T { return xs[0]; }
const p: Point = { x: 3, y: 4 };
console.log(identity(p).x + identity(p).y);
const pts: Point[] = [{ x: 1, y: 2 }, { x: 5, y: 6 }];
console.log(first(pts).x);
const b = new Box<Point>({ x: 10, y: 20 });
console.log(b.get().y);
console.log(identity(99));
console.log(identity("hi"));
`, "7\n1\n20\n99\nhi")
}

func TestE2EGenericObjectRestInBody(t *testing.T) {
	// Object rest over a generic-T source (TDD-00065 Stage 3c, generic-T case)
	// now works: object type args monomorphize T to a concrete object, so
	// `{ a, ...rest }` rides Stage 3b's bindObjectRest.
	assertOutput(t, `
interface Rec { a: number; b: string; c: number; }
function pluckRest<T>(o: T): void {
  const { a, ...rest } = o;
  console.log(a);
  console.log(rest.b);
  console.log(rest.c);
}
const r: Rec = { a: 1, b: "hello", c: 3 };
pluckRest(r);
`, "1\nhello\n3")
}

func TestE2EGenericObjectArgAnonymousLiteral(t *testing.T) {
	// An anonymous object-literal argument works via structural mangling.
	assertOutput(t, `
function pluck<T>(x: T): number { return x.id; }
console.log(pluck({ id: 5, name: "a" }));
`, "5")
}

func TestE2EGenericConstraintSatisfied(t *testing.T) {
	assertOutput(t, `
interface HasId { id: number; }
function pluck<T extends HasId>(x: T): number { return x.id; }
class Box<T extends HasId> { v: T; constructor(v: T) { this.v = v; } id(): number { return this.v.id; } }
const a = { id: 42, name: "x" };
console.log(pluck(a));
const b = new Box<HasId>({ id: 7 });
console.log(b.id());
`, "42\n7")
}

func TestE2EGenericConstraintViolatedFunction(t *testing.T) {
	mustCompileError(t, `
interface HasId { id: number; }
function pluck<T extends HasId>(x: T): number { return x.id; }
const bad = { name: "no id" };
console.log(pluck(bad));
`, "does not satisfy the constraint")
}

func TestE2EGenericConstraintViolatedClass(t *testing.T) {
	mustCompileError(t, `
interface HasId { id: number; }
interface NoId { name: string; }
class Box<T extends HasId> { v: T; constructor(v: T) { this.v = v; } }
const b = new Box<NoId>({ name: "x" });
`, "does not satisfy the constraint")
}

func TestE2EGenericFunctionObjectTypeArgument(t *testing.T) {
	// An object/interface type argument is supported (TDD-00069): the generic
	// body is monomorphized against the concrete shape, so field access on a
	// T-typed value resolves.
	assertOutput(t, `
interface Foo { a: number }
function pluckA<T>(x: T): number { return x.a; }
const f: Foo = { a: 1 };
console.log(pluckA(f));
`, "1")
}

func TestE2EGenericFunctionUnsupportedConcreteTypeRejected(t *testing.T) {
	// A Map (and other non-object heap handles) is still an unsupported type
	// argument — a clean compile error, not a miscompile.
	_, err := parseAndCompile(`
function identity<T>(x: T): T { return x; }
const m = new Map<string, number>();
console.log(identity(m));
`)
	if err == nil {
		t.Fatal("expected a compile error instantiating a generic function with an unsupported (Map) type argument, got none")
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
	if n := strings.Count(ir, "define i64 @identity("); n != 1 {
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

func TestE2EErasedGenericFunctionForwardReferenceUnannotatedObjectReturn(t *testing.T) {
	// TDD-00058's fixed-point re-inference sweep now also covers an
	// @erased generic function's own unannotated-return-type inference —
	// found not to be covered when first fixing the plain-function case
	// (it uses a separate signature-building path, buildErasedFunctionSig)
	// and confirmed broken with this exact repro before extending the fix.
	assertOutput(t, `
/** @erased */
function makeA<T>(x: T) { return makeB() }
function makeB() { return { val: 1 } }
console.log(makeA(1).val)
`, "1")
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

// --- Multiple type parameters: <K, V> (TDD-00037) ---

func TestE2EGenericFunctionMultipleTypeParams(t *testing.T) {
	assertOutput(t, `
function firstOf<K, V>(k: K, v: V): K {
  return k;
}
console.log(firstOf(1, "x"));
console.log(firstOf("y", true));
`, "1\ny")
}

// Each type parameter must infer independently from its own designated
// parameter position — a mangled name mixing number and string confirms K
// and V weren't accidentally unified to the same concrete type.
func TestE2EGenericFunctionMultipleTypeParamsMangledNameOrder(t *testing.T) {
	ir, err := parseAndCompile(`
function firstOf<K, V>(k: K, v: V): K {
  return k;
}
console.log(firstOf(1, "x"));
`)
	if err != nil {
		t.Fatalf("codegen: %v", err)
	}
	if !strings.Contains(ir, "@firstOf__num_str(") {
		t.Fatalf("expected a declared-order-mangled instantiation @firstOf__num_str, got:\n%s", ir)
	}
}

// A type parameter with no inferable parameter position must be named
// specifically in the error, not just "a" type argument — the N-ary
// generalization of the existing single-param rejection.
func TestE2EGenericFunctionUninferableTypeParamNamed(t *testing.T) {
	_, err := parseAndCompile(`
function make<K, V>(k: K): K {
  return k;
}
console.log(make(1));
`)
	if err == nil {
		t.Fatal("expected a compile error for an uninferable type parameter 'V', got none")
	}
	if !strings.Contains(err.Error(), "'V'") {
		t.Fatalf("expected the error to name the specific uninferable type parameter 'V', got: %v", err)
	}
}

func TestE2EGenericInterfaceMultipleTypeParams(t *testing.T) {
	assertOutput(t, `
interface Pair<K, V> {
  first: K;
  second: V;
}
const p: Pair<number, string> = { first: 1, second: "a" };
console.log(p.first);
console.log(p.second);
`, "1\na")
}

func TestE2EGenericClassMultipleTypeParams(t *testing.T) {
	assertOutput(t, `
class Pair<K, V> {
  first: K;
  second: V;
  constructor(a: K, b: V) {
    this.first = a;
    this.second = b;
  }
}
const p = new Pair<number, string>(1, "a");
console.log(p.first);
console.log(p.second);
`, "1\na")
}

func TestE2EGenericClassMultipleTypeParamsArityMismatchRejected(t *testing.T) {
	_, err := parseAndCompile(`
class Pair<K, V> {
  first: K;
  second: V;
  constructor(a: K, b: V) { this.first = a; this.second = b; }
}
const p = new Pair<number>(1);
`)
	if err == nil {
		t.Fatal("expected a compile error constructing a 2-type-param generic class with only one type argument, got none")
	}
}

func TestE2EErasedGenericFunctionMultipleTypeParams(t *testing.T) {
	ir, err := parseAndCompile(`
/** @erased */
function firstOf<K, V>(k: K, v: V): K {
  return k;
}
console.log(firstOf(1, "x"));
console.log(firstOf("y", true));
`)
	if err != nil {
		t.Fatalf("codegen: %v", err)
	}
	if strings.Contains(ir, "@firstOf__") {
		t.Fatalf("expected no V1-style mangled instantiation of an @erased function, got one\n%s", ir)
	}
}

// --- explicit call-site type arguments (ADR-00473) ---

func TestE2EExplicitCallSiteTypeArguments(t *testing.T) {
	assertOutput(t, `
function id<T>(x: T): T { return x; }
console.log(id<string>("hi"));
console.log(id<number>(41) + 1);
function make<T>(): T[] { return []; }
const xs = make<number>();
console.log(xs.length);
`, "hi\n42\n0")
}

func TestE2ELessThanStillParsesAsComparison(t *testing.T) {
	assertOutput(t, `
let a = 3; let b = 2; let c = 4;
console.log(a < b);
console.log(b < c && c > a);
console.log(a > (b));
`, "false\ntrue\ntrue")
}
