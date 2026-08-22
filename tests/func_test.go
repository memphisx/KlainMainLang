package tests

import (
	"strings"
	"testing"
)

// --- Functions and closures ---

func TestE2ERecursion(t *testing.T) {
	assertOutput(t, `
function fib(n: number): number {
    if (n <= 1) { return n; }
    return fib(n - 1) + fib(n - 2)
}
console.log(fib(10))
`, "55")
}

func TestE2EClosure(t *testing.T) {
	assertOutput(t, `
function makeCounter(): () => number {
    let count: number = 0
    return (): number => { count++; return count; }
}
const c = makeCounter()
console.log(c())
console.log(c())
console.log(c())
`, "1\n2\n3")
}

func TestE2EParenthesizedFunctionTypeReturnAnnotation(t *testing.T) {
	assertOutput(t, `
function makeCounter(): (() => number) {
    let count: number = 0
    return (): number => { count++; return count; }
}
const c = makeCounter()
console.log(c())
console.log(c())
`, "1\n2")
}

func TestE2EClosureMutatesOuterScope(t *testing.T) {
	assertOutput(t, `
let sum: number = 0
const inc = (n: number): void => { sum += n; }
inc(1)
inc(2)
inc(3)
console.log(sum)
`, "6")
}

func TestE2EClosureIndependentInstances(t *testing.T) {
	assertOutput(t, `
function makeCounter(): () => number {
    let count: number = 0
    return (): number => { count++; return count; }
}
const c1 = makeCounter()
const c2 = makeCounter()
console.log(c1())
console.log(c1())
console.log(c2())
`, "1\n2\n1")
}

func TestE2ENestedClosureCapture(t *testing.T) {
	assertOutput(t, `
let total: number = 0
const outer = (): void => {
    const inner = (): void => { total += 10; }
    inner()
    inner()
}
outer()
outer()
console.log(total)
`, "40")
}

// --- Arrow function returning a callable closure ---

func TestE2EArrowFunctionReturnedClosureCallable(t *testing.T) {
	assertOutput(t, `
const middle = (): (() => void) => {
  let n = 0
  return () => { n = n + 1; console.log(n) }
}
const inner = middle()
inner()
inner()
`, "1\n2")
}

// --- (FuncType)[] array-of-function-type annotations ---

func TestE2EArrayOfFunctionTypeDeclaresAndTracksLength(t *testing.T) {
	assertOutput(t, `
let counters: (() => number)[] = []
console.log(counters.length)
`, "0")
}

func TestE2EArrayOfFunctionTypePushAndCallByIndex(t *testing.T) {
	assertOutput(t, `
function makeCounter(start: number): () => number {
  let n = start
  return () => { n = n + 1; return n }
}
let counters: (() => number)[] = []
counters.push(makeCounter(0))
counters.push(makeCounter(100))
console.log(counters[0]())
console.log(counters[0]())
console.log(counters[1]())
`, "1\n2\n101")
}

func TestE2EParenGroupedArrayTypeAnnotation(t *testing.T) {
	assertOutput(t, `
let nums: (number)[] = [1, 2, 3]
console.log(nums[0])
console.log(nums[2])
`, "1\n3")
}

func TestE2EFunctionTypedObjectFieldCallable(t *testing.T) {
	assertOutput(t, `
interface Handler { callback: () => number }
function makeCounter(start: number): () => number {
  let n = start
  return () => { n = n + 1; return n }
}
const h: Handler = { callback: makeCounter(10) }
console.log(h.callback())
console.log(h.callback())
`, "11\n12")
}

// Regression test: emitVarDecl's pre-inference type switch for an
// unannotated `const`/`let` initialized from a bare-identifier call used to
// only special-case a string-returning function's result was correctly
// allocated an i64 slot for what emitExpr(v.Init) actually produces (a ptr)
// — a hard clang-stage type mismatch, not just a wrong value. Found while
// wiring TDD-00010 V1's generic-function call support (whose most natural
// usage, `const y = identity("hi")`, hit the identical gap), but the root
// cause was entirely pre-existing and unrelated to generics.
func TestE2EUnannotatedConstFromStringReturningFunction(t *testing.T) {
	assertOutput(t, `
function greet(x: string): string {
  return x;
}
const y = greet("hi");
console.log(y);
`, "hi")
}

func TestE2EUnannotatedFunctionReturnsScalar(t *testing.T) {
	assertOutput(t, `
function addOne(n) { return n + 1 }
console.log(addOne(5))
`, "6")
}

func TestE2EUnannotatedRecursiveFunctionReturnsScalar(t *testing.T) {
	assertOutput(t, `
function factorial(n) {
  if (n <= 1) { return 1 }
  return n * factorial(n - 1)
}
console.log(factorial(5))
`, "120")
}

// --- default parameter values ---

func TestE2EDefaultParamNumber(t *testing.T) {
	assertOutput(t, `
function add(a: number, b: number = 10): number { return a + b }
console.log(add(5))
console.log(add(5, 3))
`, "15\n8")
}

func TestE2EDefaultParamString(t *testing.T) {
	assertOutput(t, `
function greet(name: string = 'World'): string { return 'Hello, ' + name }
console.log(greet())
console.log(greet('Alice'))
`, "Hello, World\nHello, Alice")
}

func TestE2EDefaultParamMultiple(t *testing.T) {
	assertOutput(t, `
function box(w: number = 1, h: number = 1, d: number = 1): number { return w * h * d }
console.log(box())
console.log(box(2))
console.log(box(2, 3))
console.log(box(2, 3, 4))
`, "1\n2\n6\n24")
}

func TestE2EDefaultParamArray(t *testing.T) {
	assertOutput(t, `
function sum(nums: number[] = [1, 2, 3]): number {
  let total = 0
  for (let i = 0; i < nums.length; i++) { total += nums[i] }
  return total
}
console.log(sum())
console.log(sum([10, 20]))
`, "6\n30")
}

// --- optional (`param?: T`) parameters ---

func TestE2EOptionalParamNumber(t *testing.T) {
	assertOutput(t, `
function f(x?: number): number { return x }
console.log(f())
console.log(f(5))
`, "0\n5")
}

func TestE2EOptionalParamString(t *testing.T) {
	assertOutput(t, `
function greet(name?: string): string { return name }
console.log(greet())
console.log(greet('Alice'))
`, "null\nAlice")
}

func TestE2EOptionalParamMultiple(t *testing.T) {
	assertOutput(t, `
function box(a: number, b?: number, c?: number): number { return a + b + c }
console.log(box(1))
console.log(box(1, 2))
console.log(box(1, 2, 3))
`, "1\n3\n6")
}

func TestE2EOptionalParamArray(t *testing.T) {
	assertOutput(t, `
function count(nums?: number[]): number { return nums.length }
console.log(count())
console.log(count([1, 2, 3]))
`, "0\n3")
}

func TestE2EOptionalParamClassMethod(t *testing.T) {
	assertOutput(t, `
class Greeter {
  greet(name?: string): string { return 'Hi, ' + name }
}
const g = new Greeter()
console.log(g.greet())
console.log(g.greet('Bob'))
`, "Hi, null\nHi, Bob")
}

func TestE2EOptionalParamStaticMethod(t *testing.T) {
	assertOutput(t, `
class Util {
  static greet(name?: string): string { return 'Hi, ' + name }
}
console.log(Util.greet())
console.log(Util.greet('Bob'))
`, "Hi, null\nHi, Bob")
}

// --- void return type ---

func TestE2EVoidReturn(t *testing.T) {
	assertOutput(t, `
function greet(name: string): void {
    console.log(name)
}
function clamp(x: number): void {
    if (x < 0) { return }
    console.log(x)
}
const printIt = (n: number): void => {
    console.log(n)
}
greet("hello")
clamp(-1)
clamp(5)
printIt(42)
`, "hello\n5\n42")
}

// --- Unannotated parameter typing (numeric-only inference) ---

func TestE2EUnannotatedParamNonNumericArgRejected(t *testing.T) {
	_, err := parseAndCompile(`
function log(msg) { console.log(msg) }
log("hello")
`)
	if err == nil {
		t.Fatal("expected a compile error for a non-numeric argument to an unannotated parameter, got none")
	}
}
func TestE2EUnannotatedArrowParamNonNumericArgRejected(t *testing.T) {
	_, err := parseAndCompile(`
const log = (msg) => { console.log(msg) }
log("hello")
`)
	if err == nil {
		t.Fatal("expected a compile error for a non-numeric argument to an unannotated arrow function parameter, got none")
	}
}
func TestE2EUnannotatedParamNumericArgStillWorks(t *testing.T) {
	assertOutput(t, `
function addOne(n) { return n + 1 }
console.log(addOne(5))
`, "6")
}
func TestE2EUnannotatedArrowParamNumericArgStillWorks(t *testing.T) {
	assertOutput(t, `
const addOne = (n) => n + 1
console.log(addOne(5))
`, "6")
}
func TestE2EAnnotatedParamNonNumericArgStillWorks(t *testing.T) {
	assertOutput(t, `
function log(msg: string) { console.log(msg) }
log("hello")
`, "hello")
}

// An unannotated arrow-function param assigned/declared into a declared
// function-typed slot infers its type from the slot's own declared param
// types, the same "propagate the known expected shape" principle object/
// array literals already get (TDD-00007/TDD-00028) — found missing while
// wiring EventSource's .onmessage handler (TDD-00038 Stage 1), confirmed
// as a real, EventSource-unrelated gap via this plain repro first.
func TestE2EUnannotatedArrowParamHintedFromVarDeclFuncType(t *testing.T) {
	assertOutput(t, `
interface Box { value: number }
let cb: (b: Box) => void = (b) => { console.log(b.value) }
cb({ value: 42 })
`, "42")
}

func TestE2EUnannotatedArrowParamHintedFromFieldAssignFuncType(t *testing.T) {
	assertOutput(t, `
interface Box { value: number }
interface Holder { cb: (b: Box) => void }
const h: Holder = { cb: (b) => {} }
h.cb = (b) => { console.log(b.value) }
h.cb({ value: 7 })
`, "7")
}

// --- Destructured function parameters ---
//
// `parser/parser_stmts.go`'s `parseParamList` previously unconditionally
// `expect`ed IDENT for every parameter — a destructuring pattern in
// parameter position was a parse error. V1 scope: object and array
// patterns (both, including array holes and object-field renaming), no
// nesting, an explicit type annotation always required (there's no
// sensible unannotated default for a pattern, unlike a plain scalar
// param), and no combination with `...`/a whole-parameter default value.
// Covers named function declarations, arrow functions (which parse their
// own, separate parameter list — parser/parser_literals.go's
// parseArrowFunction, not parseParamList), and class methods/constructors
// (emit_classes.go's own, third separate param-binding loop) — three
// genuinely independent implementations that each needed the identical
// fix, not one shared code path.

func TestE2EDestructuredObjectParamFunctionDecl(t *testing.T) {
	assertOutput(t, `
interface Point { x: number; y: number }
function addPoints(p1: Point, p2: Point): number {
  return p1.x + p1.y + p2.x + p2.y
}
function sum({ x, y }: Point): number {
  return x + y
}
console.log(sum({ x: 3, y: 4 }))
console.log(addPoints({ x: 1, y: 1 }, { x: 2, y: 2 }))
`, "7\n6")
}

func TestE2EDestructuredObjectParamRenaming(t *testing.T) {
	assertOutput(t, `
interface Point { x: number; y: number }
function show({ x: px, y: py }: Point): string {
  return px + "," + py
}
console.log(show({ x: 1, y: 2 }))
`, "1,2")
}

func TestE2EDestructuredArrayParamFunctionDecl(t *testing.T) {
	assertOutput(t, `
function sumFirstTwo([a, b]: number[]): number {
  return a + b
}
const arr: number[] = [5, 6, 7]
console.log(sumFirstTwo(arr))
`, "11")
}

func TestE2EDestructuredArrayParamWithHole(t *testing.T) {
	assertOutput(t, `
function skipFirst([, b, c]: number[]): number {
  return b + c
}
console.log(skipFirst([100, 200, 300]))
`, "500")
}

func TestE2EDestructuredObjectParamArrowFunction(t *testing.T) {
	assertOutput(t, `
interface Point { x: number; y: number }
const area = ({ x, y }: Point): number => x * y
console.log(area({ x: 5, y: 6 }))
`, "30")
}

// TestE2EDestructuredArrowParamShadowsOuterSameNamedVariable is a
// regression test for a real bug found while building this feature:
// gatherCaptures (emit_func.go, the closure free-variable scanner used to
// decide what an arrow function needs to capture from its enclosing scope)
// only knew about a parameter's synthetic internal name (e.g. "__param0"
// for a destructured param), never the pattern's own field/element names.
// A destructured field sharing a name with an outer-scope variable (`x`/`y`
// here, from a top-level array-destructuring statement) was wrongly
// free-variable-scanned as a capture of the *outer* binding — and since
// capture setup runs after parameter unpacking within the same function,
// it silently overwrote the correct local (parameter) binding with the
// captured (outer) one. `x * y` computed 10 * 20 (the outer values) instead
// of 5 * 6 (the actual arguments) before the fix.
func TestE2EDestructuredArrowParamShadowsOuterSameNamedVariable(t *testing.T) {
	assertOutput(t, `
const coords: number[] = [10, 20, 30]
const [x, y, z] = coords
console.log(x)
console.log(y)
interface Point { x: number; y: number }
const area = ({ x, y }: Point): number => x * y
console.log(area({ x: 5, y: 6 }))
console.log(x)
console.log(y)
`, "10\n20\n30\n10\n20")
}

// TestE2EParenthesizedObjectLiteralStillParsesAsExpression confirms the
// arrow-function-vs-parenthesized-expression disambiguation lookahead
// (parser/parser_literals.go's destructuredArrowParamLookahead) doesn't
// misfire on a parenthesized object/array literal — which starts
// identically to a destructured arrow parameter (`({` / `([`) and is only
// told apart by whether an explicit `: Type` annotation follows the
// closing brace/bracket.
func TestE2EParenthesizedObjectLiteralStillParsesAsExpression(t *testing.T) {
	assertOutput(t, `
interface Point { x: number; y: number }
const p: Point = ({ x: 1, y: 2 })
console.log(p.x)
console.log(p.y)
const arr: number[] = ([1, 2, 3])
console.log(arr[1])
`, "1\n2\n2")
}

func TestE2EDestructuredObjectParamClassMethodAndConstructor(t *testing.T) {
	assertOutput(t, `
interface Point { x: number; y: number }
class Vec {
  sum: number
  constructor({ x, y }: Point) {
    this.sum = x + y
  }
  static add({ x, y }: Point, other: Point): number {
    return x + y + other.x + other.y
  }
}
const v = new Vec({ x: 3, y: 4 })
console.log(v.sum)
console.log(Vec.add({ x: 1, y: 1 }, { x: 2, y: 2 }))
`, "7\n6")
}

// --- Array-typed arrow-function/closure parameters (TDD-00059/ADR-00151) ---
//
// Previously rejected entirely (a closure's own call ABI never decomposed
// an array parameter into its (ptr, i64) pair the way a named function's
// does — ADR-00105's Investigation) — fixed alongside tagged template
// literals, since an arrow function's own `strings: string[]` tag
// parameter is unavoidably array-typed. emitClosureFunc (the callee/
// definition side), emitClosureCallByPtr (a direct `closureVar(args)`
// call), and emitCBCall (HOF/callback dispatch — closes the array-methods
// "no nested-array element as the callback's own parameter" caveat too)
// all needed the matching decomposition.

func TestE2EDestructuredArrayParamOnArrowFunction(t *testing.T) {
	assertOutput(t, `
const f = ([a, b]: number[]): number => a + b
console.log(f([1, 2]))
`, "3")
}

func TestE2EPlainArrayParamOnArrowFunction(t *testing.T) {
	assertOutput(t, `
const f = (arr: number[]): number => arr.length
console.log(f([1, 2, 3]))
`, "3")
}

func TestE2ERestParamOnArrowFunction(t *testing.T) {
	assertOutput(t, `
const f = (a: number, ...rest: number[]): number => {
    let s = a;
    for (const v of rest) { s += v; }
    return s;
}
console.log(f(1, 2, 3, 4))
console.log(f(1))
`, "10\n1")
}

func TestE2EArrayParamOnArrowFunctionAsHOFCallback(t *testing.T) {
	// The "no nested-array element as the callback's own parameter"
	// fidelity gap ARRAY-METHODS.md documented — a HOF callback taking an
	// array-typed parameter (a nested array's own row) is exactly the
	// array-typed-closure-parameter gap this fix closes.
	assertOutput(t, `
const matrix: number[][] = [[1, 2], [3, 4], [5, 6]]
const sums = matrix.map((row: number[]): number => {
    let s = 0;
    for (const v of row) { s += v; }
    return s;
})
console.log(sums[0])
console.log(sums[1])
console.log(sums[2])
`, "3\n7\n11")
}

func TestE2ENamedFunctionArrayParamAsHOFCallback(t *testing.T) {
	assertOutput(t, `
function rowSum(row: number[]): number {
    let s = 0;
    for (const v of row) { s += v; }
    return s;
}
const matrix: number[][] = [[1, 2], [3, 4]]
const sums = matrix.map(rowSum)
console.log(sums[0])
console.log(sums[1])
`, "3\n7")
}

func TestE2EDestructuredParamRequiresTypeAnnotation(t *testing.T) {
	_, err := parseAndCompile(`function f({x, y}) { return x + y }`)
	if err == nil {
		t.Fatal("expected a compile error for a destructured parameter with no type annotation, got none")
	}
}

func TestE2EDestructuredParamWithDefaultRejected(t *testing.T) {
	_, err := parseAndCompile(`function f({x, y}: {x:number,y:number} = {x:1,y:2}): number { return x + y }`)
	if err == nil {
		t.Fatal("expected a compile error for a default value on a destructured parameter, got none")
	}
}

func TestE2ERestDestructuredParamRejected(t *testing.T) {
	_, err := parseAndCompile(`function f(...{x, y}: number[]) {}`)
	if err == nil {
		t.Fatal("expected a compile error for a rest parameter that's also a destructuring pattern, got none")
	}
}

// --- Nested function declarations (TDD-00057) ---

func TestE2ENestedFunctionDeclaration(t *testing.T) {
	assertOutput(t, `
function outer(x: number): number {
    function inner(y: number): number {
        return y * 2;
    }
    return inner(x) + 1;
}
console.log(outer(5));
`, "11")
}

func TestE2ENestedFunctionForwardReference(t *testing.T) {
	assertOutput(t, `
function outer(): number {
    const r = fib(6);
    return r;

    function fib(n: number): number {
        if (n <= 1) { return n; }
        return fib(n - 1) + fib(n - 2);
    }
}
console.log(outer());
`, "8")
}

func TestE2ENestedFunctionInArrowBlockBody(t *testing.T) {
	assertOutput(t, `
const f = (x: number): number => {
    function double(n: number): number { return n * 2; }
    function triple(n: number): number { return double(n) + n; }
    return triple(x);
};
console.log(f(4));
`, "12")
}

func TestE2ENestedFunctionThreeLevelsVisibility(t *testing.T) {
	assertOutput(t, `
function grand(): number {
    function helper(): number { return 100; }
    function outer(): number {
        function inner(): number {
            return helper();
        }
        return inner();
    }
    return outer();
}
console.log(grand());
`, "100")
}

func TestE2ENestedFunctionSameNameDifferentEnclosersDoNotCollide(t *testing.T) {
	assertOutput(t, `
function a(): number {
    function helper(): number { return 1; }
    return helper();
}
function b(): number {
    function helper(): number { return 2; }
    return helper();
}
console.log(a());
console.log(b());
`, "1\n2")
}

func TestE2ENestedFunctionCapturingOuterLocalRejected(t *testing.T) {
	// V1 scope (TDD-00057): a nested function declaration gets its own
	// clean scope, same as a top-level function — it does not close over
	// the enclosing function's locals the way an arrow function would.
	_, err := parseAndCompile(`
function outer(): number {
    const x: number = 10;
    function inner(): number {
        return x;
    }
    return inner();
}
console.log(outer());
`)
	if err == nil {
		t.Fatal("expected a compile error for a nested function referencing an enclosing local, got none")
	}
}

func TestE2ENestedFunctionInsideIfBlockRejected(t *testing.T) {
	// V1 scope: only supported directly in the enclosing body's own
	// immediate statement list, not one block deeper.
	_, err := parseAndCompile(`
function outer(cond: boolean): number {
    if (cond) {
        function inner(): number { return 1; }
        return inner();
    }
    return 0;
}
console.log(outer(true));
`)
	if err == nil {
		t.Fatal("expected a compile error for a nested function declared inside an if block, got none")
	}
	if !strings.Contains(err.Error(), "only supported directly") {
		t.Fatalf("expected the error to explain the scoping restriction, got: %v", err)
	}
}

func TestE2ENestedFunctionNotVisibleOutsideEncloser(t *testing.T) {
	_, err := parseAndCompile(`
function outer(): number {
    function inner(): number { return 1; }
    return inner();
}
console.log(inner());
`)
	if err == nil {
		t.Fatal("expected a compile error for calling a nested function from outside its enclosing function, got none")
	}
}

func TestE2ENestedFunctionDuplicateNameRejected(t *testing.T) {
	_, err := parseAndCompile(`
function outer(): number {
    function helper(): number { return 1; }
    function helper(): number { return 2; }
    return helper();
}
console.log(outer());
`)
	if err == nil {
		t.Fatal("expected a compile error for two same-named nested function declarations in the same scope, got none")
	}
}

// --- Fixed-point unannotated-return-type inference (TDD-00058) ---

func TestE2EForwardReferenceUnannotatedObjectReturn(t *testing.T) {
	// ADR-00041's originally-accepted boundary: makeA (unannotated) calls
	// makeB (also unannotated, declared later, returns an object) — makeA's
	// own inferred return type used to be computed before makeB was
	// registered, defaulting to a scalar and rejecting the field access.
	assertOutput(t, `
function makeA() { return makeB() }
function makeB() { return { x: 1 } }
console.log(makeA().x)
`, "1")
}

func TestE2EForwardReferenceChainDepthThree(t *testing.T) {
	assertOutput(t, `
function makeA() { return makeB() }
function makeB() { return makeC() }
function makeC() { return { x: 42 } }
console.log(makeA().x)
`, "42")
}

func TestE2ENestedFunctionForwardReferenceUnannotatedObjectReturn(t *testing.T) {
	// Same gap, scoped to a nested function (TDD-00057) calling a same-body
	// unannotated sibling declared later.
	assertOutput(t, `
function outer() {
    function makeA() { return makeB() }
    function makeB() { return { x: 7 } }
    return makeA().x;
}
console.log(outer())
`, "7")
}

func TestE2ENestedGenericFunctionRejected(t *testing.T) {
	_, err := parseAndCompile(`
function outer(): number {
    function identity<T>(x: T): T { return x; }
    return identity(1);
}
console.log(outer());
`)
	if err == nil {
		t.Fatal("expected a compile error for a generic nested function declaration, got none")
	}
}

// --- Function expressions (TDD-00060) ---

func TestE2EFunctionExpressionBasic(t *testing.T) {
	assertOutput(t, `
const add = function(x: number): number { return x + 1; };
console.log(add(5));
`, "6")
}

func TestE2EFunctionExpressionClosureCapture(t *testing.T) {
	assertOutput(t, `
const n = 10;
const addN = function(x: number): number { return n + x; };
console.log(addN(5));
`, "15")
}

func TestE2EFunctionExpressionAsCallback(t *testing.T) {
	assertOutput(t, `
const arr: number[] = [1, 2, 3];
const doubled = arr.map(function(x: number): number { return x * 2; });
console.log(doubled[0], doubled[1], doubled[2]);
`, "2 4 6")
}

func TestE2EFunctionExpressionNoAnnotation(t *testing.T) {
	assertOutput(t, `
const triple = function(x) { return x * 3; };
console.log(triple(7));
`, "21")
}

// TDD-00060/ADR-00178: a named function expression's name is bound only inside
// its own body, for self-reference/recursion.
func TestE2ENamedFunctionExpressionRecursion(t *testing.T) {
	assertOutput(t, `
const fact = function factorial(n: number): number {
	if (n <= 1) { return 1; }
	return n * factorial(n - 1);
};
console.log(fact(5));
`, "120")
}

// The name is visible only inside the body, not in the enclosing scope.
func TestE2ENamedFunctionExpressionNameNotLeaked(t *testing.T) {
	_, err := parseAndCompile(`
const f = function foo(): number { return 1; };
console.log(foo());
`)
	if err == nil {
		t.Fatal("expected the function-expression name 'foo' to be undefined outside its body, got no error")
	}
}

// Self-reference works alongside a captured variable from the enclosing scope.
func TestE2ENamedFunctionExpressionRecursionWithCapture(t *testing.T) {
	assertOutput(t, `
const base = 10;
const sum = function rec(n: number): number {
	if (n === 0) { return base; }
	return n + rec(n - 1);
};
console.log(sum(3));
`, "16")
}

// A name that shadows a top-level function of the same name is rejected
// cleanly (the resolver has already mangled the self-reference to the outer
// function; see ADR-00178) rather than silently calling the outer function.
func TestE2ENamedFunctionExpressionShadowingTopLevelRejected(t *testing.T) {
	_, err := parseAndCompile(`
function rec(n: number): number { return n; }
const f = function rec(n: number): number { if (n <= 0) { return 0; } return n + rec(n - 1); };
console.log(f(3));
`)
	if err == nil {
		t.Fatal("expected a clean error for a named FE shadowing a top-level function, got none")
	}
	if !strings.Contains(err.Error(), "shadows a top-level function") {
		t.Fatalf("expected 'shadows a top-level function', got: %v", err)
	}
}

func TestE2EDuplicateParamNameRejected(t *testing.T) {
	_, err := parseAndCompile(`
const f = function(a: number, a: number): number { return a; };
`)
	if err == nil {
		t.Fatal("expected a compile error for a duplicate parameter name, got none")
	}
	if !strings.Contains(err.Error(), "duplicate parameter name") {
		t.Fatalf("expected 'duplicate parameter name', got: %v", err)
	}
}

func TestE2EDuplicateParamNameRejectedOnDeclaration(t *testing.T) {
	_, err := parseAndCompile(`
function f(a: number, a: number): number { return a; }
`)
	if err == nil {
		t.Fatal("expected a compile error for a duplicate parameter name, got none")
	}
	if !strings.Contains(err.Error(), "duplicate parameter name") {
		t.Fatalf("expected 'duplicate parameter name', got: %v", err)
	}
}

func TestE2EUseStrictNonSimpleParamListRejected(t *testing.T) {
	_, err := parseAndCompile(`
const f = function(a: number = 0): number { "use strict"; return a; };
`)
	if err == nil {
		t.Fatal("expected a compile error for a non-simple parameter list under 'use strict', got none")
	}
	if !strings.Contains(err.Error(), "non-simple parameter list") {
		t.Fatalf("expected 'non-simple parameter list', got: %v", err)
	}
}

func TestE2EUseStrictEvalParamNameRejected(t *testing.T) {
	_, err := parseAndCompile(`
const f = function(eval: number): number { "use strict"; return eval; };
`)
	if err == nil {
		t.Fatal("expected a compile error for 'eval' as a strict-mode parameter name, got none")
	}
	if !strings.Contains(err.Error(), "cannot be a parameter name in strict mode") {
		t.Fatalf("expected 'cannot be a parameter name in strict mode', got: %v", err)
	}
}

func TestE2EUseStrictSimpleParamsAllowed(t *testing.T) {
	assertOutput(t, `
const f = function(a: number, b: number): number { "use strict"; return a + b; };
console.log(f(2, 3));
`, "5")
}

func TestE2EUseStrictEvalBindingNameRejected(t *testing.T) {
	_, err := parseAndCompile(`
const f = function(): number { "use strict"; var eval = 1; return eval; };
`)
	if err == nil {
		t.Fatal("expected a compile error for 'eval' as a strict-mode binding name, got none")
	}
	if !strings.Contains(err.Error(), "cannot be used as a binding name in strict mode") {
		t.Fatalf("expected 'cannot be used as a binding name in strict mode', got: %v", err)
	}
}

func TestE2EUseStrictArgumentsBindingNameInNestedBlockRejected(t *testing.T) {
	_, err := parseAndCompile(`
const f = function(): number { "use strict"; if (true) { let arguments = 2; return arguments; } return 0; };
`)
	if err == nil {
		t.Fatal("expected a compile error for 'arguments' as a strict-mode binding name in a nested block, got none")
	}
	if !strings.Contains(err.Error(), "cannot be used as a binding name in strict mode") {
		t.Fatalf("expected 'cannot be used as a binding name in strict mode', got: %v", err)
	}
}

func TestE2EClassMethodEvalBindingNameRejected(t *testing.T) {
	_, err := parseAndCompile(`
class C { m(): number { const eval = 1; return eval; } }
console.log(new C().m());
`)
	if err == nil {
		t.Fatal("expected a compile error for 'eval' as a binding name in an always-strict class method, got none")
	}
	if !strings.Contains(err.Error(), "cannot be used as a binding name in strict mode") {
		t.Fatalf("expected 'cannot be used as a binding name in strict mode', got: %v", err)
	}
}

func TestE2ESloppyEvalBindingNameAllowed(t *testing.T) {
	// Outside strict mode, `eval`/`arguments` are ordinary identifiers — this
	// compiler treats a plain function body (no "use strict" directive) as
	// sloppy, so binding them there stays legal, matching JS.
	assertOutput(t, `
const f = function(): number { var eval = 7; return eval; };
console.log(f());
`, "7")
}

func TestE2EConstMissingInitializerRejected(t *testing.T) {
	_, err := parseAndCompile(`
const x;
console.log(1);
`)
	if err == nil {
		t.Fatal("expected a compile error for a 'const' with no initializer, got none")
	}
	if !strings.Contains(err.Error(), "must be initialized") {
		t.Fatalf("expected 'must be initialized', got: %v", err)
	}
}

func TestE2EConstForOfLoopVariableStillAllowed(t *testing.T) {
	// The for-of loop-variable `const` form legitimately has no initializer
	// and must keep compiling despite the missing-initializer rejection above.
	assertOutput(t, `
for (const y of [1, 2, 3]) { console.log(y); }
`, "1\n2\n3")
}

func TestE2EFunctionExpressionVoid(t *testing.T) {
	assertOutput(t, `
let count = 0;
const bump = function() { count++; };
bump();
bump();
console.log(count);
`, "2")
}

func TestE2EUnannotatedFunctionExpressionParamHintedFromVarDeclFuncType(t *testing.T) {
	assertOutput(t, `
interface Box { value: number }
let cb: (b: Box) => void = function(b) { console.log(b.value); };
cb({ value: 42 })
`, "42")
}

func TestE2EFunctionExpressionIIFE(t *testing.T) {
	assertOutput(t, `
console.log((function(x: number): number { return x + 1; })(5));
`, "6")
}

func TestE2EDuplicateDestructuredParamNameRejected(t *testing.T) {
	_, err := parseAndCompile(`
function f({x}: {x: number}, {x}: {x: number}): number { return x; }
`)
	if err == nil {
		t.Fatal("expected a compile error for two destructured params binding the same name, got none")
	}
	if !strings.Contains(err.Error(), "duplicate parameter name") {
		t.Fatalf("expected 'duplicate parameter name', got: %v", err)
	}
}

func TestE2ENestedFunctionExpressionDestructuredParamShadowsOuterCapture(t *testing.T) {
	// A destructured parameter's real bound name is its pattern's field
	// name, not the parameter's own synthetic internal name — a nested
	// function expression's free-variable scan (run while gathering the
	// *enclosing* closure's own captures) must know that, or it wrongly
	// treats the inner `items` as an unresolved reference to the outer
	// `items: number[]` array, hitting "capturing array variable 'items'
	// in a closure is not yet supported" for code that never actually
	// references the outer array at all.
	assertOutput(t, `
const items: number[] = [1, 2, 3];
const outer = (): number => {
    const inner = function({items}: {items: number}): number {
        return items;
    };
    return inner({ items: 99 });
};
console.log(outer());
`, "99")
}

// --- Calling the result of an arbitrary expression ---

func TestE2ECallResultOfCallExpression(t *testing.T) {
	assertOutput(t, `
function make(): () => number {
    return () => 42;
}
console.log(make()());
`, "42")
}

func TestE2ECallResultOfConditionalExpression(t *testing.T) {
	assertOutput(t, `
const f = (x: number): number => x + 1;
const g = (x: number): number => x + 2;
const cond = true;
console.log((cond ? f : g)(10));
`, "11")
}

func TestE2ECallResultOfObjectFieldReturningAClosure(t *testing.T) {
	assertOutput(t, `
interface Box { getHandler: () => (() => number) }
const b: Box = { getHandler: () => (() => 99) };
console.log(b.getHandler()());
`, "99")
}

// --- Generator functions (TDD-00061/ADR-00172): V1 scope cuts still rejected ---
// (Real, working generator behavior is covered in generators_test.go.)

func TestE2EGeneratorFunctionMissingReturnTypeRejected(t *testing.T) {
	// The element type is now inferred from yields (TDD-00096/ADR-00293) —
	// only a genuinely non-joinable yield mix still demands the annotation.
	_, err := parseAndCompile(`
function* gen() {
    yield 1;
    yield "two";
}
const g = gen();
console.log(g.next().value);
`)
	if err == nil {
		t.Fatal("expected a compile error for a generator whose yields produce non-joinable element types, got none")
	}
	if !strings.Contains(err.Error(), "requires an explicit return type annotation") {
		t.Fatalf("expected 'requires an explicit return type annotation', got: %v", err)
	}
}

// A non-capturing nested generator declaration works (TDD-00094); a capturing one
// is a clean rejection until the __env capture work (sub-step 2) lands.
func TestE2ENestedGeneratorNonCapturing(t *testing.T) {
	assertOutput(t, `
function outer(): void {
    function* gen(n: number): number { yield n; yield n + 1 }
    for (const v of gen(5)) { console.log(v) }
}
outer()
`, "5\n6")
}

// A capturing nested generator closes over an enclosing variable by reference
// (TDD-00094): a mutation after the generator is created is seen by a later
// .next(), matching JS.
func TestE2ENestedGeneratorCapturing(t *testing.T) {
	assertOutput(t, `
function outer(): void {
    const base = 9
    function* gen(): number { yield base; yield base + 1 }
    for (const v of gen()) { console.log(v) }
}
outer()
`, "9\n10")
}

func TestE2ENestedGeneratorCaptureByReference(t *testing.T) {
	assertOutput(t, `
function outer(): void {
    let base = 10
    function* gen(): number { yield base; yield base }
    const it = gen()
    console.log(it.next().value)
    base = 99
    console.log(it.next().value)
}
outer()
`, "10\n99")
}

func TestE2EYieldOutsideGeneratorRejected(t *testing.T) {
	_, err := parseAndCompile(`
function notAGenerator(): void {
    yield 1;
}
`)
	if err == nil {
		t.Fatal("expected a compile error for yield outside a generator, got none")
	}
	if !strings.Contains(err.Error(), "'yield' is only valid inside a generator function body") {
		t.Fatalf("expected \"'yield' is only valid inside a generator function body\", got: %v", err)
	}
}

// yield* delegates to another generator (TDD-00086); yield* over a general
// iterable such as an array still needs Symbol.iterator and is a clean rejection.
func TestE2EYieldStarOverArrayRejected(t *testing.T) {
	_, err := parseAndCompile(`
function* gen(): number {
    yield* [1, 2, 3];
}
console.log(gen());
`)
	if err == nil {
		t.Fatal("expected a compile error for yield* over an array, got none")
	}
	if !strings.Contains(err.Error(), "yield* requires a generator operand") {
		t.Fatalf("expected 'yield* requires a generator operand', got: %v", err)
	}
}

// A `T | null` return value (TDD-00064 Stage 3) crosses the function boundary
// as a presence-flagged aggregate, so the caller distinguishes a real value
// (including 0) from null via `??` / `=== null`, and null prints as `null`.
func TestE2ENullableScalarReturnValue(t *testing.T) {
	assertOutput(t, `
function maybe(n: number): number | null {
  if (n < 0) { return null }
  return n * 2
}
console.log(maybe(5))
console.log(maybe(-1))
console.log(maybe(0))
console.log(maybe(5) ?? 99)
console.log(maybe(-1) ?? 99)
console.log(maybe(0) ?? 99)
console.log(maybe(-1) === null)
console.log(maybe(0) === null)
let z: number = maybe(4) ?? 0
console.log(z + 1)
`, "10\nnull\n0\n10\n99\n0\ntrue\nfalse\n9")
}

// A `T | null` parameter (TDD-00064 Stage 3) is passed as a presence-flagged
// aggregate, so a null argument and a present 0 are distinguishable inside the
// callee — across a top-level function, a method, and a closure.
func TestE2ENullableScalarParameter(t *testing.T) {
	assertOutput(t, `
function classify(v: number | null): string {
  if (v === null) { return "none" }
  return "some:" + v
}
console.log(classify(0))
console.log(classify(5))
console.log(classify(null))

class Box {
  base: number
  constructor(base: number) { this.base = base }
  add(v: number | null): number { return (v ?? 0) + this.base }
}
const b = new Box(10)
console.log(b.add(0))
console.log(b.add(null))
console.log(b.add(5))

const f = (v: number | null): number => v ?? -1
console.log(f(0))
console.log(f(null))
`, "some:0\nsome:5\nnone\n10\n10\n15\n0\n-1")
}

// A named function used as a value (`const g = f`, passing `f` as an argument,
// returning `f`) works via an env-dropping closure trampoline (ADR-00200).
func TestE2ENamedFunctionAsValue(t *testing.T) {
	assertOutput(t, `
function inc(v: number): number { return v + 1 }
const g = inc
console.log(g(5))
console.log(inc(5))

function apply(fn: (x: number) => number, v: number): number { return fn(v) }
console.log(apply(inc, 10))

function twice(fn: (x: number) => number, v: number): number { return fn(fn(v)) }
console.log(twice(inc, 10))

function pick(): (n: number) => number { return inc }
const h = pick()
console.log(h(41))
`, "6\n6\n11\n12\n42")
}

// A named function value round-trips array/rest/void parameter and return
// shapes through the trampoline unchanged.
func TestE2ENamedFunctionAsValueShapes(t *testing.T) {
	assertOutput(t, `
function firstTwo(xs: number[]): number[] { return [xs[0], xs[1]] }
const ft = firstTwo
const r = ft([9, 8, 7])
console.log(r[0])
console.log(r[1])

function sum(...ns: number[]): number {
  let t = 0
  for (const n of ns) { t = t + n }
  return t
}
const s = sum
console.log(s(1, 2, 3, 4))

function log(msg: string): void { console.log("LOG: " + msg) }
const lg = log
lg("hi")
`, "9\n8\n10\nLOG: hi")
}

// --- let/const/var scope semantics (TDD-00070 / ADR-00210) ---

func TestE2EVarRedeclarationTopLevelAllowed(t *testing.T) {
	// A repeated top-level `var` is legal JS (var re-declaration) — the second
	// declaration is observably just an assignment. Previously the kind-agnostic
	// duplicate check wrongly rejected this. (Stage A) — routed through the
	// resolver (assertOutputImports), where the kind-aware check actually lives.
	assertOutputImports(t, `
var x = 1;
var x = 2;
console.log(x);
`, "2")
}

func TestE2EVarThenFunctionSameNameTopLevelAllowed(t *testing.T) {
	// A `var` and a same-named `function` coexist as one binding in JS.
	assertOutputImports(t, `
var g = 1;
function g(): void {}
console.log(g);
`, "1")
}

func TestE2ELetRedeclarationTopLevelRejected(t *testing.T) {
	_, err := parseAndCompileImports(t, `
let x = 1;
let x = 2;
console.log(x);
`)
	if err == nil {
		t.Fatal("expected a compile error for a duplicate top-level 'let', got none")
	}
	if !strings.Contains(err.Error(), "declared more than once") {
		t.Fatalf("expected 'declared more than once', got: %v", err)
	}
}

func TestE2ELetVarCrossKindTopLevelRejected(t *testing.T) {
	_, err := parseAndCompileImports(t, `
let x = 1;
var x = 2;
console.log(x);
`)
	if err == nil {
		t.Fatal("expected a compile error for a let/var cross-kind collision, got none")
	}
	if !strings.Contains(err.Error(), "declared more than once") {
		t.Fatalf("expected 'declared more than once', got: %v", err)
	}
}

func TestE2EVarFunctionScopedLeaksBlock(t *testing.T) {
	// A `var` declared inside a block stays visible after the block, unlike a
	// block-scoped `let`/`const`. (Stage C)
	assertOutput(t, `
function f(): void {
  { var x = 5; }
  console.log(x);
}
f();
`, "5")
}

func TestE2EForVarLeaksToFunctionScope(t *testing.T) {
	assertOutput(t, `
function g(): void {
  for (var i = 0; i < 3; i = i + 1) {}
  console.log(i);
}
g();
`, "3")
}

func TestE2ELetDoesNotLeakBlock(t *testing.T) {
	// The counterpart to the var-leak test: a block-scoped `let` is not visible
	// after its block.
	_, err := parseAndCompile(`
function h(): void {
  { let y = 5; }
  console.log(y);
}
h();
`)
	if err == nil {
		t.Fatal("expected a compile error reading a block-scoped 'let' after its block, got none")
	}
	if !strings.Contains(err.Error(), "undefined variable") {
		t.Fatalf("expected 'undefined variable', got: %v", err)
	}
}

func TestE2EConditionalAnyVarReadsUndefined(t *testing.T) {
	// An `any`-typed `var` whose initializer runs on a not-taken path reads back
	// as `undefined` (JS-faithful hoist-to-undefined), not uninitialized memory.
	assertOutput(t, `
function f(cond: boolean): void {
  if (cond) { var r: any = 42; }
  console.log(r);
}
f(false);
`, "undefined")
}

func TestE2EConditionalTypedVarReadsZeroDeterministic(t *testing.T) {
	// A typed `var` on a not-taken path reads a deterministic zero default
	// rather than garbage (V1: full TS definite-assignment errors are deferred).
	assertOutput(t, `
function f(cond: boolean): void {
  if (cond) { var r = 42; }
  console.log(r);
}
f(false);
`, "0")
}

func TestE2EBlockScopedLetRedeclarationRejected(t *testing.T) {
	// A duplicate `let` in the same nested block is a redeclaration early-error
	// — previously this compiled and silently used the second value. (Stage B)
	_, err := parseAndCompileImports(t, `
function f(): void {
  let y = 1;
  let y = 2;
  console.log(y);
}
f();
`)
	if err == nil {
		t.Fatal("expected a compile error for a duplicate block-scoped 'let', got none")
	}
	if !strings.Contains(err.Error(), "already been declared") {
		t.Fatalf("expected 'already been declared', got: %v", err)
	}
}

func TestE2EBlockScopedLetConstCrossKindRejected(t *testing.T) {
	_, err := parseAndCompileImports(t, `
function f(): void { let a = 1; const a = 2; console.log(a); }
f();
`)
	if err == nil {
		t.Fatal("expected a compile error for a let/const collision in a block, got none")
	}
	if !strings.Contains(err.Error(), "already been declared") {
		t.Fatalf("expected 'already been declared', got: %v", err)
	}
}

func TestE2ESiblingBlockLetReuseAllowed(t *testing.T) {
	// The same name declared in two independent sibling blocks is fine.
	assertOutput(t, `
{ let z = 1; console.log(z); }
{ let z = 2; console.log(z); }
`, "1\n2")
}

func TestE2EForHeadAndBodyLetSameNameAllowed(t *testing.T) {
	// The loop-head binding and a body binding of the same name are separate
	// scopes — not a redeclaration.
	assertOutput(t, `
for (let i = 0; i < 2; i = i + 1) { let i = 99; console.log(i); }
`, "99\n99")
}

func TestE2EVarRedeclarationInBlockAllowed(t *testing.T) {
	assertOutput(t, `
function f(): void { var a = 1; var a = 2; console.log(a); }
f();
`, "2")
}

// --- cross-block var/lexical intersection (TDD-00070 caveat #3, ADR-00210 follow-up) ---

func TestE2ELetThenNestedVarSameNameRejected(t *testing.T) {
	// `let x; { var x }` — the block's var hoists to function scope and collides
	// with the enclosing block-scoped let. SyntaxError in JS.
	_, err := parseAndCompileImports(t, `
function f(): void {
  let x = 1;
  { var x = 2; }
  console.log(x);
}
f();
`)
	if err == nil {
		t.Fatal("expected a compile error for a nested var colliding with an outer let, got none")
	}
	if !strings.Contains(err.Error(), "already been declared") {
		t.Fatalf("expected 'already been declared', got: %v", err)
	}
}

func TestE2ETopLevelLetThenNestedVarRejected(t *testing.T) {
	_, err := parseAndCompileImports(t, `
let x = 1;
{ var x = 2; }
console.log(x);
`)
	if err == nil {
		t.Fatal("expected a compile error for a nested var colliding with a top-level let, got none")
	}
	if !strings.Contains(err.Error(), "already been declared") {
		t.Fatalf("expected 'already been declared', got: %v", err)
	}
}

func TestE2ELetThenForVarSameNameRejected(t *testing.T) {
	// `let i; for (var i ...)` — the for's var is function-scoped and collides.
	_, err := parseAndCompileImports(t, `
let i = 99;
for (var i = 0; i < 2; i = i + 1) {}
console.log(i);
`)
	if err == nil {
		t.Fatal("expected a compile error for a for-var colliding with an outer let, got none")
	}
	if !strings.Contains(err.Error(), "already been declared") {
		t.Fatalf("expected 'already been declared', got: %v", err)
	}
}

func TestE2EVarThenNestedLetShadowAllowed(t *testing.T) {
	// The legal inverse: an outer `var` with an inner block `let` of the same
	// name is a normal shadow, not a redeclaration.
	assertOutput(t, `
function f(): void {
  var x = 1;
  { let x = 2; console.log(x); }
  console.log(x);
}
f();
`, "2\n1")
}

func TestE2ENestedLetThenVarNonOverlappingAllowed(t *testing.T) {
	// A block-scoped `let` that has already gone out of scope before a later
	// `var` of the same name is declared is legal (non-overlapping scopes).
	assertOutput(t, `
function f(): void {
  { let x = 2; console.log(x); }
  var x = 1;
  console.log(x);
}
f();
`, "2\n1")
}

func TestE2EOuterLetThenForLetShadowAllowed(t *testing.T) {
	// A block-scoped for-`let` loop variable may reuse an outer `let` name.
	assertOutput(t, `
let i = 99;
for (let i = 0; i < 2; i = i + 1) { console.log(i); }
console.log(i);
`, "0\n1\n99")
}

// --- temporal dead zone (TDD-00071 Stage 1, caveat #2) ---

func TestE2ETDZUseBeforeDeclaration(t *testing.T) {
	_, err := parseAndCompileImports(t, `
function g(): void {
  console.log(y);
  let y = 3;
}
g();
`)
	if err == nil {
		t.Fatal("expected a TDZ compile error for a let read before its declaration, got none")
	}
	if !strings.Contains(err.Error(), "before initialization") {
		t.Fatalf("expected 'before initialization', got: %v", err)
	}
}

func TestE2ETDZShadowingReadsInnerBinding(t *testing.T) {
	// The shadowing correctness bug: inside the block, `x` binds to the block's
	// own hoisted (TDZ) `let x`, so the read before it is an error — previously
	// this silently read the outer x and printed 1.
	_, err := parseAndCompileImports(t, `
let x = 1;
{ console.log(x); let x = 2; }
`)
	if err == nil {
		t.Fatal("expected a TDZ compile error for a shadowing let read before its declaration, got none")
	}
	if !strings.Contains(err.Error(), "before initialization") {
		t.Fatalf("expected 'before initialization', got: %v", err)
	}
}

func TestE2ETDZSelfInitializerRejected(t *testing.T) {
	_, err := parseAndCompileImports(t, `
function h(): void { let z = z + 1; console.log(z); }
h();
`)
	if err == nil {
		t.Fatal("expected a TDZ compile error for a self-referential let initializer, got none")
	}
	if !strings.Contains(err.Error(), "before initialization") {
		t.Fatalf("expected 'before initialization', got: %v", err)
	}
}

func TestE2ELexicalReadAfterDeclarationAllowed(t *testing.T) {
	assertOutputImports(t, `
const a = 7;
console.log(a);
`, "7")
}

func TestE2EEnumReadAfterDeclarationAllowed(t *testing.T) {
	// Regression guard: an enum name is a lexical binding; without flipping it
	// out of TDZ at its declaration, a later E.A read would be a false positive.
	assertOutputImports(t, `
enum E { A = 1, B = 2 }
const v = E.A;
console.log(v);
`, "1")
}

func TestE2EClosureReadingLaterOuterBindingNotFalsePositive(t *testing.T) {
	// A closure that reads an outer binding declared later must NOT be a TDZ
	// error (real TDZ is a runtime check; the closure runs after the binding
	// initializes). We only assert the analysis doesn't reject it — no distinct
	// TDZ error is produced.
	_, err := parseAndCompileImports(t, `
function outer(): void {
  const log = (): void => { console.log(msg); };
  const msg = "hi";
  log();
}
outer();
`)
	if err != nil && strings.Contains(err.Error(), "before initialization") {
		t.Fatalf("closure reading a later outer binding was wrongly flagged as TDZ: %v", err)
	}
}

// --- definite assignment (TDD-00071 Stage 2, caveat #1) ---

func TestE2EDefiniteAssignConditionalVarRejected(t *testing.T) {
	// The flagship: a typed var assigned only on one branch, read after.
	_, err := parseAndCompileImports(t, `
function f(c: boolean): void {
  if (c) { var r = 42; }
  console.log(r);
}
f(false);
`)
	if err == nil {
		t.Fatal("expected a definite-assignment error, got none")
	}
	if !strings.Contains(err.Error(), "used before being assigned") {
		t.Fatalf("expected 'used before being assigned', got: %v", err)
	}
}

func TestE2EDefiniteAssignTypedLetNoInitRejected(t *testing.T) {
	_, err := parseAndCompileImports(t, `
function g(): void { let x: number; console.log(x); }
g();
`)
	if err == nil {
		t.Fatal("expected a definite-assignment error, got none")
	}
	if !strings.Contains(err.Error(), "used before being assigned") {
		t.Fatalf("expected 'used before being assigned', got: %v", err)
	}
}

func TestE2EDefiniteAssignBothBranchesAllowed(t *testing.T) {
	assertOutputImports(t, `
function h(c: boolean): void {
  var r: number;
  if (c) { r = 5; } else { r = 9; }
  console.log(r);
}
h(true);
`, "5")
}

func TestE2EDefiniteAssignElseDivergesAllowed(t *testing.T) {
	assertOutputImports(t, `
function k(c: boolean): number {
  var r: number;
  if (c) { r = 1; } else { return -1; }
  return r;
}
console.log(k(true));
`, "1")
}

func TestE2EDefiniteAssignAnyTypedExempt(t *testing.T) {
	assertOutputImports(t, `
function m(c: boolean): void {
  if (c) { var r: any = 1; }
  console.log(r);
}
m(false);
`, "undefined")
}

func TestE2EDefiniteAssignLoopCarriedAllowed(t *testing.T) {
	// A loop-carried assignment must not be a false positive.
	assertOutputImports(t, `
function f(): void {
  let x: number;
  for (let i = 0; i < 3; i = i + 1) {
    if (i === 0) { x = 1; } else { x = x + 1; }
  }
  console.log(x);
}
f();
`, "3")
}

func TestE2EDefiniteAssignSwitchDefaultAllowed(t *testing.T) {
	// A switch assigning in every case (with default) must not be flagged.
	assertOutputImports(t, `
function g(n: number): void {
  let x: number;
  switch (n) {
    case 1: x = 10; break;
    default: x = 20;
  }
  console.log(x);
}
g(5);
`, "20")
}

func TestE2EDefiniteAssignForVarInitReadAfterAllowed(t *testing.T) {
	// The for-init runs unconditionally, so a var it assigns is readable after.
	assertOutputImports(t, `
function c(): void {
  for (var i = 0; i < 3; i = i + 1) {}
  console.log(i);
}
c();
`, "3")
}

// --- definite assignment, tightened do/while + switch (ADR-00214) ---

func TestE2EDefiniteAssignDoWhileUnconditionalAllowed(t *testing.T) {
	// A do/while body runs at least once, so an unconditional assignment in it
	// IS definite afterward.
	assertOutputImports(t, `
function f(c: boolean): void { let x: number; do { x = 5; } while (c); console.log(x); }
f(false);
`, "5")
}

func TestE2EDefiniteAssignDoWhileConditionalRejected(t *testing.T) {
	// The precision win: a conditionally-assigned binding in a do/while body is
	// still caught (over-seeding would have missed it).
	_, err := parseAndCompileImports(t, `
function f(c: boolean): void { let x: number; do { if (c) { x = 5; } } while (c); console.log(x); }
f(false);
`)
	if err == nil || !strings.Contains(err.Error(), "used before being assigned") {
		t.Fatalf("expected 'used before being assigned', got: %v", err)
	}
}

func TestE2EDefiniteAssignDoWhileIfElseAllowed(t *testing.T) {
	assertOutputImports(t, `
function f(c: boolean): void { let x: number; do { if (c) { x = 5; } else { x = 9; } } while (c); console.log(x); }
f(false);
`, "9")
}

func TestE2EDefiniteAssignSwitchDefaultAllCasesAllowed(t *testing.T) {
	assertOutputImports(t, `
function g(n: number): void { let x: number; switch (n) { case 1: x = 10; break; default: x = 20; } console.log(x); }
g(1);
`, "10")
}

func TestE2EDefiniteAssignSwitchDefaultMissingAssignRejected(t *testing.T) {
	// A default that doesn't assign the binding is now caught.
	_, err := parseAndCompileImports(t, `
function g(n: number): void { let x: number; switch (n) { case 1: x = 10; break; default: console.log("d"); } console.log(x); }
g(1);
`)
	if err == nil || !strings.Contains(err.Error(), "used before being assigned") {
		t.Fatalf("expected 'used before being assigned', got: %v", err)
	}
}

func TestE2EDefiniteAssignSwitchNoDefaultRejected(t *testing.T) {
	// No default => an unmatched discriminant leaves the binding unassigned.
	_, err := parseAndCompileImports(t, `
function g(n: number): void { let x: number; switch (n) { case 1: x = 10; break; case 2: x = 20; break; } console.log(x); }
g(1);
`)
	if err == nil || !strings.Contains(err.Error(), "used before being assigned") {
		t.Fatalf("expected 'used before being assigned', got: %v", err)
	}
}

func TestE2EDefiniteAssignSwitchFallthroughAllowed(t *testing.T) {
	// case 1 falls through to case 2, which assigns — must not be a false positive.
	assertOutputImports(t, `
function g(n: number): void { let x: number; switch (n) { case 1: case 2: x = 10; break; default: x = 20; } console.log(x); }
g(2);
`, "10")
}

// --- documented escapes (sound gaps): these COMPILE by design; the analysis
// deliberately does not flag them (TDD-00071's no-false-positives trade). Kept
// as tests so a future tightening's effect is visible. ---

func TestE2EDefiniteAssignWhileOnlyEscapes(t *testing.T) {
	// A binding assigned only in a for/while body that might not run is NOT
	// caught (the body is over-seeded). Sound but incomplete — the read would be
	// unsafe if the loop runs zero times. Compiles today.
	_, err := parseAndCompileImports(t, `
function f(n: number): void { let x: number; while (n > 0) { x = n; n = n - 1; } console.log(x); }
f(5);
`)
	if err != nil && strings.Contains(err.Error(), "used before being assigned") {
		t.Fatalf("while-only assignment is a documented escape (should compile today), got: %v", err)
	}
}

func TestE2EDefiniteAssignTryEscapes(t *testing.T) {
	// A try body that may throw before its assignment is not caught (try is
	// over-seeded). Sound but incomplete. Compiles today.
	_, err := parseAndCompileImports(t, `
function f(): void { let x: number; try { x = 7; } catch (e) { } console.log(x); }
f();
`)
	if err != nil && strings.Contains(err.Error(), "used before being assigned") {
		t.Fatalf("try-body assignment is a documented escape (should compile today), got: %v", err)
	}
}

func TestE2EWhileEscapeReadsDeterministicDefault(t *testing.T) {
	// A definite-assignment escape (a let assigned only in a maybe-skipped loop)
	// now reads its deterministic zero default rather than uninitialized memory
	// — ADR-00215. Here the loop runs zero times.
	assertOutputImports(t, `
function f(n: number): void { let x: number; while (n > 0) { x = n; n = n - 1; } console.log(x); }
f(0);
`, "0")
}

func TestE2EAnyTypedLetNoInitReadsUndefined(t *testing.T) {
	assertOutputImports(t, `
function h(): void { let x: any; console.log(x); }
h();
`, "undefined")
}

// --- TDD-00093: a top-level `const`/`let`/`var` of a simple type is promoted to
// a module global, so a named `function` declaration (its own fresh scope, not a
// capturing closure) can read and write it. Previously a compile error
// ("undefined variable"). ---

func TestE2EModuleGlobalConstInFunction(t *testing.T) {
	assertOutput(t, `
const base = 100
function add(n: number): number { return base + n }
console.log(base)
console.log(add(5))
`, "100\n105")
}

func TestE2EModuleGlobalStringInFunction(t *testing.T) {
	assertOutput(t, `
const appName = "svc"
function banner(): string { return "[" + appName + "]" }
console.log(banner())
`, "[svc]")
}

func TestE2EModuleGlobalLetMutatedByFunction(t *testing.T) {
	assertOutput(t, `
let counter = 0
function inc(): void { counter = counter + 1 }
inc()
inc()
inc()
console.log(counter)
`, "3")
}

// A function mutation through the global is visible to an arrow that references
// the same top-level let — the arrow reads the one global, not a boxed copy.
func TestE2EModuleGlobalSharedBetweenFnAndArrow(t *testing.T) {
	assertOutput(t, `
let n = 1
function bump(): void { n = 42 }
const show = (): void => { console.log(n) }
show()
bump()
show()
`, "1\n42")
}

// A local of the same name shadows the module global inside the function.
func TestE2EModuleGlobalLocalShadow(t *testing.T) {
	assertOutput(t, `
const v = 1
function g(): void { const v = 99; console.log(v) }
g()
console.log(v)
`, "99\n1")
}

// Multi-declarator top-level list, all promoted and readable in a function.
func TestE2EModuleGlobalMultiDeclarator(t *testing.T) {
	assertOutput(t, `
let a = 3, b = 4
function sum(): number { return a + b }
console.log(sum())
`, "7")
}

// A boolean module global gates a branch inside a function.
func TestE2EModuleGlobalBoolInFunction(t *testing.T) {
	assertOutput(t, `
const verbose = true
function log(msg: string): void { if (verbose) { console.log(msg) } }
log("hi")
`, "hi")
}

// A top-level array is promoted to two module globals (data + length), so a
// named function can iterate/read it (TDD-00093).
func TestE2EModuleGlobalArrayInFunction(t *testing.T) {
	assertOutput(t, `
const nums: number[] = [10, 20, 30]
function total(): number {
  let s = 0
  for (const n of nums) { s = s + n }
  return s
}
console.log(total())
`, "60")
}

func TestE2EModuleGlobalStringArrayInFunction(t *testing.T) {
	assertOutput(t, `
const names = ["a", "b", "c"]
function joined(): string {
  let out = ""
  for (const s of names) { out = out + s }
  return out
}
console.log(joined())
`, "abc")
}

// The original trigger: a top-level Promise<number>[] consumed by for-await
// inside a function.
func TestE2EModuleGlobalPromiseArrayForAwait(t *testing.T) {
	assertOutput(t, `
async function f(n: number): Promise<number> { return n * 2 }
const jobs: Promise<number>[] = [f(1), f(2), f(3)]
async function run(): Promise<void> {
  for await (const x of jobs) { console.log(x) }
}
run()
`, "2\n4\n6")
}

// A top-level object is a single ptr module global, readable (and its fields
// mutable) from a named function (TDD-00093).
func TestE2EModuleGlobalObjectInFunction(t *testing.T) {
	assertOutput(t, `
const cfg = { host: "localhost", port: 8080 }
function url(): string { return cfg.host + ":" + cfg.port }
console.log(url())
`, "localhost:8080")
}

func TestE2EModuleGlobalObjectFieldMutation(t *testing.T) {
	assertOutput(t, `
const state = { count: 0 }
function inc(): void { state.count = state.count + 1 }
inc()
inc()
console.log(state.count)
`, "2")
}

func TestE2EModuleGlobalAnnotatedObjectInFunction(t *testing.T) {
	assertOutput(t, `
interface Cfg { name: string; max: number }
const c: Cfg = { name: "svc", max: 5 }
function describe(): string { return c.name + " max=" + c.max }
console.log(describe())
`, "svc max=5")
}

// A top-level Map/Set is a single ptr module global, usable from a named
// function (its mutations shared) (TDD-00093).
func TestE2EModuleGlobalMapInFunction(t *testing.T) {
	assertOutput(t, `
const cache = new Map<string, number>()
cache.set("a", 1)
function get(k: string): number { return cache.get(k) }
cache.set("b", 2)
console.log(get("a"))
console.log(get("b"))
`, "1\n2")
}

func TestE2EModuleGlobalSetInFunction(t *testing.T) {
	assertOutput(t, `
const seen = new Set<number>()
function mark(n: number): void { seen.add(n) }
mark(5)
mark(7)
console.log(seen.has(5))
console.log(seen.has(9))
`, "true\nfalse")
}

// TDD-00093 (complete design): an un-annotated initializer is promoted to a
// module global when its type is determinable in the pre-pass — a named
// function's composite return, or an earlier module global — so it too is
// readable from a named function.
func TestE2EModuleGlobalUnannotatedCallObject(t *testing.T) {
	assertOutput(t, `
interface Cfg { host: string; port: number }
function loadConfig(): Cfg { return { host: "h", port: 9 } }
const cfg = loadConfig()
function url(): string { return cfg.host + ":" + cfg.port }
console.log(url())
`, "h:9")
}

func TestE2EModuleGlobalUnannotatedCallArray(t *testing.T) {
	assertOutput(t, `
function seed(): number[] { return [1, 2, 3] }
const xs = seed()
function total(): number { let s = 0; for (const x of xs) { s = s + x } return s }
console.log(total())
`, "6")
}

func TestE2EModuleGlobalIdentifierOfEarlierGlobal(t *testing.T) {
	assertOutput(t, `
const base = 10
const derived = base
function get(): number { return derived }
console.log(get())
`, "10")
}

// A scope-dependent initializer (referencing a runtime-local destructuring
// target) is correctly NOT promoted — it stays a main() local and still compiles.
func TestE2EModuleGlobalScopeDependentStaysLocal(t *testing.T) {
	assertOutput(t, `
interface Rec { a: number; b: number; c: string }
let r: Rec = { a: 1, b: 2, c: "three" }
let { a, ...rest } = r
let copy = { ...rest }
console.log(copy.c)
`, "three")
}

// A nested function (and nested generator) can call a top-level function — the
// resolver now descends into nested function bodies and mangles their top-level
// references (previously `undefined function`) (TDD-00094).
func TestE2ENestedFunctionCallsTopLevel(t *testing.T) {
	assertOutput(t, `
function dbl(n: number): number { return n * 2 }
function outer(): number {
  function inner(): number { return dbl(21) }
  return inner()
}
console.log(outer())
`, "42")
}

func TestE2ENestedGeneratorCallsTopLevel(t *testing.T) {
	assertOutput(t, `
function triple(n: number): number { return n * 3 }
function outer(): void {
  function* g(n: number): number { yield triple(n) }
  for (const v of g(4)) { console.log(v) }
}
outer()
`, "12")
}

func TestE2ENestedAsyncGeneratorCaptureAndTopLevel(t *testing.T) {
	assertOutput(t, `
async function delay(v: number): Promise<number> { return v }
async function outer(): Promise<void> {
  const factor = 10
  async function* g(n: number): number {
    let i = 0
    while (i < n) { yield await delay(i * factor); i = i + 1 }
  }
  for await (const v of g(3)) { console.log(v) }
}
outer()
`, "0\n10\n20")
}
