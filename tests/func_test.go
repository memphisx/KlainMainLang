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
