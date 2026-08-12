package tests

import (
	"strings"
	"testing"
)

// --- Control flow ---

func TestE2EForLoop(t *testing.T) {
	assertOutput(t, `
let sum: number = 0
for (let i = 1; i <= 5; i++) {
    sum += i
}
console.log(sum)
`, "15")
}

// --- Multi-declarator let/const/var (`let i = 0, j = 10;`) ---

func TestE2EMultiDeclaratorLet(t *testing.T) {
	assertOutput(t, `
let i = 1, j = 2, k = 3;
console.log(i, j, k);
`, "1\n2\n3")
}

func TestE2EMultiDeclaratorConst(t *testing.T) {
	assertOutput(t, `
const x: number = 1, y: number = 2;
console.log(x + y);
`, "3")
}

func TestE2EMultiDeclaratorInFunctionBody(t *testing.T) {
	assertOutput(t, `
function sum(): number {
  let a = 1, b = 2, c = 3;
  return a + b + c;
}
console.log(sum());
`, "6")
}

func TestE2EMultiDeclaratorClosureCapture(t *testing.T) {
	// Every declarator in a multi-declarator statement must be a real,
	// individually-bound local — not accidentally treated as free/captured
	// by a nested closure referencing it.
	assertOutput(t, `
function make(): () => number {
  let a = 1, b = 2;
  const f = () => a + b;
  return f;
}
const fn = make();
console.log(fn());
`, "3")
}

func TestE2EForLoopMultiDeclaratorInit(t *testing.T) {
	assertOutput(t, `
for (let i = 0, j = 10; i < j; i++) {
  console.log(i, j);
  break;
}
`, "0\n10")
}

func TestE2EForLoopCommaUpdate(t *testing.T) {
	// `i++, j--` — a common two-pointer idiom, not the general comma
	// operator (which stays out of scope everywhere else in this
	// compiler).
	assertOutput(t, `
let out = "";
for (let i = 0, j = 10; i < j; i++, j--) {
  out += i.toString() + "," + j.toString() + " ";
}
console.log(out.trim());
`, "0,10 1,9 2,8 3,7 4,6")
}

func TestE2EForLoopMultiDeclaratorClosureCapture(t *testing.T) {
	// Same free-variable-capture correctness concern as
	// TestE2EMultiDeclaratorClosureCapture, but for a for-loop's own Init
	// clause specifically (a separate code path in the compiler's
	// closure-capture scan).
	assertOutput(t, `
function make(): () => number {
  let result = 0;
  for (let i = 0, j = 10; i < 3; i++, j--) {
    result = result + i + j;
  }
  return () => result;
}
const fn = make();
console.log(fn());
`, "30")
}

func TestE2EExportMultiDeclaratorRejected(t *testing.T) {
	// `export let a = 1, b = 2;` stays a clean rejection — exporting a
	// multi-declarator statement isn't supported (a narrower, deliberate
	// scope cut, not attempted here).
	_, err := parseAndCompile(`
export let a = 1, b = 2;
`)
	if err == nil {
		t.Fatal("expected a compile error for 'export' on a multi-declarator let, got none")
	}
}

func TestE2EForLoopPlainCommaInitRejected(t *testing.T) {
	// `for (i = 0, j = 10; ...)` — comma-separated plain assignment
	// expressions (not a declaration) in the init clause — stays a clean
	// rejection. Distinct from the update clause (`i++, j--`), which is
	// supported: this is the general comma operator in a non-for-loop-
	// update position, still out of scope everywhere in this compiler.
	_, err := parseAndCompile(`
let i: number = 0;
let j: number = 0;
for (i = 0, j = 10; i < j; i++) {
  console.log(i);
}
`)
	if err == nil {
		t.Fatal("expected a compile error for comma-separated plain-assignment for-init, got none")
	}
}

func TestE2EWhileLoop(t *testing.T) {
	assertOutput(t, `
let n: number = 5
let fact: number = 1
while (n > 1) {
    fact *= n
    n--
}
console.log(fact)
`, "120")
}

func TestE2EIfElse(t *testing.T) {
	assertOutput(t, `
function sign(x: number): number {
    if (x > 0) { return 1; }
    else if (x < 0) { return -1; }
    else { return 0; }
}
console.log(sign(10))
console.log(sign(-5))
console.log(sign(0))
`, "1\n-1\n0")
}

// --- Automatic semicolon insertion on return ---

func TestE2EReturnASI(t *testing.T) {
	// A bare `return` followed by an expression on the next line must be
	// two statements (`return;` then the expression as its own statement),
	// not `return <thatExpression>` — matching JS's ASI restriction that
	// disallows a line terminator between `return` and its value.
	assertOutput(t, `
function f(): number {
    return
    42
}
console.log(f())
`, "0")
}

// --- do...while, for...in, labeled break/continue, braceless bodies ---

func TestE2EDoWhileBasic(t *testing.T) {
	assertOutput(t, `
let i = 0
do {
  console.log(i)
  i = i + 1
} while (i < 3)
`, "0\n1\n2")
}
func TestE2EDoWhileRunsOnce(t *testing.T) {
	// Body must execute even when condition is false from the start.
	assertOutput(t, `
let i = 0
do {
  console.log('run')
  i = i + 1
} while (i < 0)
`, "run")
}
func TestE2EDoWhileBreak(t *testing.T) {
	assertOutput(t, `
let i = 0
do {
  if (i === 2) break
  console.log(i)
  i = i + 1
} while (i < 5)
`, "0\n1")
}
func TestE2EForInBasic(t *testing.T) {
	assertOutput(t, `
const obj = { a: 1, b: 2, c: 3 }
for (const key in obj) {
  console.log(key)
}
`, "a\nb\nc")
}
func TestE2EForInCollect(t *testing.T) {
	assertOutput(t, `
const person = { name: 'Alice', age: 30 }
let result = ''
for (const k in person) {
  result = result + k + ' '
}
console.log(result)
`, "name age ")
}
func TestE2EForInBreak(t *testing.T) {
	assertOutput(t, `
const obj = { x: 1, y: 2, z: 3 }
for (const key in obj) {
  if (key === 'y') break
  console.log(key)
}
`, "x")
}

// Regression test: for...in used to only recognize a plain named variable
// (`for (const k in obj)`) — a field access like `c.point` fell through to
// "for...in requires a named object variable" even though iterating it only
// ever needs the field's static type (its field-name list), never its
// runtime value. See ADR-00060.
func TestE2EForInFieldAccess(t *testing.T) {
	assertOutput(t, `
interface Container {
    point: { x: number; y: number }
}
const c: Container = { point: { x: 1, y: 2 } }
for (const k in c.point) {
    console.log(k)
}
`, "x\ny")
}
func TestE2ELabeledBreak(t *testing.T) {
	assertOutput(t, `
outer: for (let i = 0; i < 3; i++) {
    for (let j = 0; j < 3; j++) {
        if (j === 1) break outer;
        console.log(i);
        console.log(j);
    }
}
`, "0\n0")
}
func TestE2ELabeledContinue(t *testing.T) {
	assertOutput(t, `
outer: for (let i = 0; i < 3; i++) {
    for (let j = 0; j < 3; j++) {
        if (j === 1) continue outer;
        console.log(i);
        console.log(j);
    }
}
`, "0\n0\n1\n0\n2\n0")
}
func TestE2ELabeledContinueWhile(t *testing.T) {
	assertOutput(t, `
let i: number = 0
outer: while (i < 3) {
    let j: number = 0
    while (j < 3) {
        if (j === 1) { i++; continue outer; }
        console.log(i);
        console.log(j);
        j++;
    }
}
`, "0\n0\n1\n0\n2\n0")
}
func TestE2ELabeledBreakAcrossFunctionBoundaryRejected(t *testing.T) {
	// A labeled break/continue can only target a loop in the *same*
	// function — reaching into an enclosing function's loop from inside a
	// nested closure would need a `br label` crossing into a different
	// LLVM function's own label space, which is invalid IR. Found via
	// ADR-00168's IIFE work: before the break/continue/named-label stacks
	// were reset per function entry, this silently compiled to invalid IR
	// that only `clang` rejected, not this compiler itself.
	_, err := parseAndCompile(`
var x = 0;
LABEL1: do {
    x++;
    (function(){ break LABEL1; })();
} while (0);
`)
	if err == nil {
		t.Fatal("expected a compile error for a labeled break crossing a function boundary, got none")
	}
	if !strings.Contains(err.Error(), "undefined label") {
		t.Fatalf("expected 'undefined label', got: %v", err)
	}
}

func TestE2ELabeledContinueAcrossFunctionBoundaryRejected(t *testing.T) {
	_, err := parseAndCompile(`
var x = 0;
LABEL1: do {
    x++;
    (function(){ continue LABEL1; })();
} while (0);
`)
	if err == nil {
		t.Fatal("expected a compile error for a labeled continue crossing a function boundary, got none")
	}
	if !strings.Contains(err.Error(), "undefined label") {
		t.Fatalf("expected 'undefined label', got: %v", err)
	}
}

func TestE2EUnlabeledBreakAcrossFunctionBoundaryRejected(t *testing.T) {
	_, err := parseAndCompile(`
for (let i = 0; i < 3; i++) {
    (function(){ break; })();
}
`)
	if err == nil {
		t.Fatal("expected a compile error for a bare break inside a closure nested in a loop, got none")
	}
	if !strings.Contains(err.Error(), "break statement outside of loop") {
		t.Fatalf("expected 'break statement outside of loop', got: %v", err)
	}
}

func TestE2EBreakInsideNestedClosureOwnLoopStillWorks(t *testing.T) {
	// The fix must not reject a break/continue targeting a loop declared
	// *within* the closure's own body — only one reaching into an
	// enclosing function.
	assertOutput(t, `
const f = function(): number {
    let sum = 0;
    for (let i = 0; i < 5; i++) {
        if (i === 3) { break; }
        sum += i;
    }
    return sum;
};
console.log(f());
`, "3")
}

func TestE2EBreakStillWorksWithoutSemicolon(t *testing.T) {
	// Regression guard: a bare `break`/`continue` with no label, on its own
	// line with no semicolon, must not swallow the next line's leading
	// identifier as a label (break/continue labels require the "no line
	// terminator" rule, same as real JS).
	assertOutput(t, `
for (let i = 0; i < 3; i++) {
    if (i === 1) break
    console.log(i)
}
`, "0")
}
func TestE2EBracelessIf(t *testing.T) {
	assertOutput(t, `
const x = 5
if (x > 3) console.log('big')
else console.log('small')
`, "big")
}
func TestE2EBracelessWhile(t *testing.T) {
	assertOutput(t, `
let i = 0
while (i < 3) console.log(i++)
`, "0\n1\n2")
}
func TestE2EBracelessFor(t *testing.T) {
	assertOutput(t, `
for (let i = 0; i < 3; i++) console.log(i)
`, "0\n1\n2")
}

// --- Expression iterables in for...of ---

func TestE2EForOfObjectKeys(t *testing.T) {
	// Object.keys() result used directly in for...of without intermediate variable
	assertOutput(t, `
const obj = { a: 1, b: 2, c: 3 }
for (const k of Object.keys(obj)) console.log(k)
`, "a\nb\nc")
}

func TestE2EForOfObjectValues(t *testing.T) {
	assertOutput(t, `
const p = { x: 10, y: 20 }
for (const v of Object.values(p)) console.log(v)
`, "10\n20")
}

func TestE2EForOfObjectEntries(t *testing.T) {
	assertOutput(t, `
const p = { name: 'Alice', age: 30 }
for (const e of Object.entries(p)) {
  console.log(e.key + '=' + e.value)
}
`, "name=Alice\nage=30")
}

func TestE2EForOfArraySlice(t *testing.T) {
	// .slice() result iterated directly
	assertOutput(t, `
const nums: number[] = [10, 20, 30, 40, 50]
for (const n of nums.slice(1, 4)) console.log(n)
`, "20\n30\n40")
}
