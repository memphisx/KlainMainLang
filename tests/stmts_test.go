package tests

import (
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
