package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"KlainMainLang/ast"
	"KlainMainLang/codegen/llvm"
	"KlainMainLang/parser"
	"KlainMainLang/resolver"
)

// parseAndCompile runs parsing and codegen only (no clang), returning the
// generated IR and any error — used by negative tests asserting a clean
// compile-time rejection rather than a successful run.
func parseAndCompile(src string) (string, error) {
	prog, err := parser.Parse(src)
	if err != nil {
		return "", err
	}
	em := llvm.NewEmitter()
	return em.EmitProgram(prog)
}

// buildBinary compiles the given TypeScript source to a native binary and
// returns its path. The test is skipped if clang is not available.
func buildBinary(t *testing.T, src string) string {
	t.Helper()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not found in PATH")
	}

	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	em := llvm.NewEmitter()
	ir, err := em.EmitProgram(prog)
	if err != nil {
		t.Fatalf("codegen: %v", err)
	}

	dir := t.TempDir()
	llFile := filepath.Join(dir, "prog.ll")
	binFile := filepath.Join(dir, "prog")

	if err := os.WriteFile(llFile, []byte(ir), 0644); err != nil {
		t.Fatalf("write IR: %v", err)
	}

	clangArgs := []string{"-O2", llFile, "-o", binFile}
	for _, lib := range em.LinkLibs() {
		clangArgs = append(clangArgs, "-l"+lib)
	}
	out, err := exec.Command("clang", clangArgs...).CombinedOutput()
	if err != nil {
		t.Fatalf("clang: %v\n%s", err, out)
	}
	return binFile
}

// writeMultiFile writes each file in files (keyed by relative path, e.g.
// "math.ts") into a fresh temp directory and returns the directory.
func writeMultiFile(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatalf("mkdir for %s: %v", name, err)
		}
		if err := os.WriteFile(p, []byte(content), 0644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

// resolveMultiFile writes files to a temp dir and runs the module resolver
// on entryName, returning the merged program (or a resolution error) — used
// by negative tests asserting a clean multi-file compile-time rejection.
func resolveMultiFile(t *testing.T, files map[string]string, entryName string) (*ast.Program, error) {
	t.Helper()
	dir := writeMultiFile(t, files)
	return resolver.ResolveProgram(filepath.Join(dir, entryName))
}

// buildBinaryMultiFile writes files to a temp dir, resolves imports
// starting from entryName, and compiles the merged program to a native
// binary. The test is skipped if clang is not available.
func buildBinaryMultiFile(t *testing.T, files map[string]string, entryName string) string {
	t.Helper()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not found in PATH")
	}
	dir := writeMultiFile(t, files)

	prog, err := resolver.ResolveProgram(filepath.Join(dir, entryName))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	em := llvm.NewEmitter()
	ir, err := em.EmitProgram(prog)
	if err != nil {
		t.Fatalf("codegen: %v", err)
	}

	llFile := filepath.Join(dir, "prog.ll")
	binFile := filepath.Join(dir, "prog")
	if err := os.WriteFile(llFile, []byte(ir), 0644); err != nil {
		t.Fatalf("write IR: %v", err)
	}
	clangArgs := []string{"-O2", llFile, "-o", binFile}
	for _, lib := range em.LinkLibs() {
		clangArgs = append(clangArgs, "-l"+lib)
	}
	out, err := exec.Command("clang", clangArgs...).CombinedOutput()
	if err != nil {
		t.Fatalf("clang: %v\n%s", err, out)
	}
	return binFile
}

// assertMultiFileOutput builds and runs a multi-file program and compares
// its stdout against want, line by line.
func assertMultiFileOutput(t *testing.T, files map[string]string, entryName, want string) {
	t.Helper()
	binFile := buildBinaryMultiFile(t, files, entryName)
	result, err := exec.Command(binFile).Output()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	compareLines(t, strings.TrimRight(string(result), "\n"), want)
}

// compileAndRun compiles the given TypeScript source to a native binary and
// returns its stdout. The test is skipped if clang is not available.
func compileAndRun(t *testing.T, src string) string {
	t.Helper()
	binFile := buildBinary(t, src)
	result, err := exec.Command(binFile).Output()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	return strings.TrimRight(string(result), "\n")
}

// compileAndRunWithStdin is like compileAndRun but feeds stdin to the binary.
func compileAndRunWithStdin(t *testing.T, src, stdin string) string {
	t.Helper()
	binFile := buildBinary(t, src)
	cmd := exec.Command(binFile)
	cmd.Stdin = strings.NewReader(stdin)
	result, err := cmd.Output()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	return strings.TrimRight(string(result), "\n")
}

// compileAndRunWithArgs is like compileAndRun but passes extra CLI args to the binary.
func compileAndRunWithArgs(t *testing.T, src string, args ...string) string {
	t.Helper()
	binFile := buildBinary(t, src)
	result, err := exec.Command(binFile, args...).Output()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	return strings.TrimRight(string(result), "\n")
}

// compileAndRunExpectExit compiles and runs the given source, returning stdout
// and the process exit code (instead of failing the test on a non-zero exit).
func compileAndRunExpectExit(t *testing.T, src string) (string, int) {
	t.Helper()
	binFile := buildBinary(t, src)
	cmd := exec.Command(binFile)
	var stdout strings.Builder
	cmd.Stdout = &stdout
	err := cmd.Run()
	exitCode := 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		exitCode = exitErr.ExitCode()
	} else if err != nil {
		t.Fatalf("run: %v", err)
	}
	return strings.TrimRight(stdout.String(), "\n"), exitCode
}

func assertOutput(t *testing.T, src, want string) {
	t.Helper()
	compareLines(t, compileAndRun(t, src), want)
}

// compareLines compares got against want line by line so individual
// mismatches are clear, rather than one big diff on the whole string.
func compareLines(t *testing.T, got, want string) {
	t.Helper()
	gotLines := strings.Split(got, "\n")
	wantLines := strings.Split(want, "\n")
	for i := 0; i < len(wantLines) || i < len(gotLines); i++ {
		var g, w string
		if i < len(gotLines) {
			g = gotLines[i]
		}
		if i < len(wantLines) {
			w = wantLines[i]
		}
		if g != w {
			t.Errorf("line %d: got %q, want %q", i+1, g, w)
		}
	}
}

// --- Single quotes and optional semicolons ---

func TestE2ESingleQuotesNoSemicolons(t *testing.T) {
	assertOutput(t, `
const greeting = 'hello'
const name = 'world'
console.log(greeting + ', ' + name + '!')
`, "hello, world!")
}

func TestE2EMixedQuoteStyles(t *testing.T) {
	// double quotes remain valid when needed (e.g. string contains single quote)
	assertOutput(t, `
const a = 'single'
const b = "double"
console.log(a)
console.log(b)
`, "single\ndouble")
}

// --- Arithmetic and variables ---

func TestE2EArithmetic(t *testing.T) {
	assertOutput(t, `
const a: number = 10
const b: number = 3
console.log(a + b)
console.log(a - b)
console.log(a * b)
console.log(a / b)
console.log(a % b)
`, "13\n7\n30\n3\n1")
}

func TestE2EBoolean(t *testing.T) {
	assertOutput(t, `
console.log(1 < 2)
console.log(2 > 3)
console.log(1 === 1)
console.log(1 !== 2)
`, "1\n0\n1\n1")
}

func TestE2ETernary(t *testing.T) {
	assertOutput(t, `
const x: number = 5
const abs: number = x < 0 ? -x : x
console.log(abs)
`, "5")
}

// --- Strings ---

func TestE2EStringConcat(t *testing.T) {
	assertOutput(t, `
const a: string = 'hello'
const b: string = 'world'
console.log(a + ', ' + b + '!')
`, "hello, world!")
}

func TestE2EStringPlusNumberConcat(t *testing.T) {
	// Regression test: "+" with exactly one string operand must stringify
	// the other side (matching real JS), not blindly reinterpret it as
	// already being a string pointer — found broken (crashed at the clang
	// verification step) while writing a Timers example that printed an
	// interval tick count.
	assertOutput(t, `
let count: number = 3
console.log("tick " + count)
console.log(count + " tick")
`, "tick 3\n3 tick")
}

func TestE2EStringPlusBooleanConcat(t *testing.T) {
	assertOutput(t, `
let flag: boolean = true
console.log("flag is " + flag)
console.log(flag + " is the flag")
`, "flag is true\ntrue is the flag")
}

func TestE2EStringMethods(t *testing.T) {
	assertOutput(t, `
const s: string = 'Hello, World!'
console.log(s.length)
console.log(s.toUpperCase())
console.log(s.toLowerCase())
console.log(s.includes('World'))
console.log(s.startsWith('Hello'))
console.log(s.indexOf('World'))
`, "13\nHELLO, WORLD!\nhello, world!\n1\n1\n7")
}

func TestE2EStringSlice(t *testing.T) {
	assertOutput(t, `
const s: string = 'hello'
console.log(s.slice(1, 3))
console.log(s.slice(-2))
console.log(s.substring(1, 3))
`, "el\nlo\nel")
}

func TestE2EStringReplaceAll(t *testing.T) {
	assertOutput(t, `
console.log("aaa".replaceAll("a", "bb"))
console.log("hello world hello".replaceAll("hello", "hi"))
console.log("no match here".replaceAll("xyz", "abc"))
console.log("aaa".replaceAll("a", "a"))
console.log("banana".replaceAll("ana", "ANA"))
`, "bbbbbb\nhi world hi\nno match here\naaa\nbANAna")
}

func TestE2ETemplateLiteral(t *testing.T) {
	assertOutput(t, `
const x: number = 42
const msg: string = ` + "`" + `value is ${x}` + "`" + `
console.log(msg)
`, "value is 42")
}

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

// --- Arrays ---

func TestE2EArrayHOF(t *testing.T) {
	assertOutput(t, `
const nums: number[] = [1, 2, 3, 4, 5]
const doubled = nums.map((n: number) => n * 2)
const evens = nums.filter((n: number) => n % 2 === 0)
const sum = nums.reduce((acc: number, n: number) => acc + n, 0)
console.log(doubled[0])
console.log(doubled[4])
console.log(evens.length)
console.log(sum)
`, "2\n10\n2\n15")
}

func TestE2EArrayForEach(t *testing.T) {
	assertOutput(t, `
const nums: number[] = [1, 2, 3]
let sum: number = 0
nums.forEach((n: number) => {
    sum += n
})
console.log(sum)
nums.forEach((n: number, i: number) => {
    console.log(i * 100 + n)
})
`, "6\n1\n102\n203")
}

func TestE2EArrayForEachConsoleLogCallback(t *testing.T) {
	assertOutput(t, `
const names: string[] = ["a", "b", "c"]
names.forEach((n) => console.log(n))
`, "a\nb\nc")
}

func TestE2EArrayForEachUnannotatedStringParam(t *testing.T) {
	assertOutput(t, `
const names: string[] = ["a", "bb", "ccc"]
let total: number = 0
names.forEach((n) => { total += n.length })
console.log(total)
`, "6")
}

func TestE2EArrayMapUnannotatedStringParam(t *testing.T) {
	assertOutput(t, `
const names: string[] = ["a", "bb", "ccc"]
const lengths = names.map((n) => n.length)
console.log(lengths[0])
console.log(lengths[1])
console.log(lengths[2])
`, "1\n2\n3")
}

func TestE2EArrayFilterUnannotatedStringParam(t *testing.T) {
	assertOutput(t, `
const names: string[] = ["apple", "bob", "cat"]
const shortOnes = names.filter((n) => n.length === 3)
console.log(shortOnes[0])
console.log(shortOnes[1])
`, "bob\ncat")
}

func TestE2EArrayFindUnannotatedStringParam(t *testing.T) {
	assertOutput(t, `
const names: string[] = ["apple", "bob", "cat"]
console.log(names.find((n) => n.length === 3))
`, "bob")
}

func TestE2EArraySomeEveryUnannotatedStringParam(t *testing.T) {
	assertOutput(t, `
const names: string[] = ["apple", "bob", "cat"]
console.log(names.some((n) => n.length === 3))
console.log(names.every((n) => n.length <= 5))
`, "1\n1")
}

func TestE2EArrayFindIndexUnannotatedStringParam(t *testing.T) {
	assertOutput(t, `
const names: string[] = ["apple", "bob", "cat"]
console.log(names.findIndex((n) => n.length === 3))
`, "1")
}

func TestE2EArrayReduceUnannotatedStringAccumulatorAndElement(t *testing.T) {
	assertOutput(t, `
const names: string[] = ["a", "bb", "ccc"]
const totalLen = names.reduce((acc, n) => acc + n.length, 0)
console.log(totalLen)
const joined = names.reduce((acc, n) => acc + n, "")
console.log(joined)
`, "6\nabbccc")
}

func TestE2EArraySort(t *testing.T) {
	assertOutput(t, `
const nums: number[] = [3, 1, 4, 1, 5, 9, 2, 6]
nums.sort()
console.log(nums[0])
console.log(nums[7])
const desc: number[] = [3, 1, 4, 1, 5]
desc.sort((a: number, b: number) => b - a)
console.log(desc[0])
console.log(desc[4])
`, "1\n9\n5\n1")
}

func TestE2EForOf(t *testing.T) {
	assertOutput(t, `
const words: string[] = ['apple', 'banana', 'cherry']
let out: string = ''
for (const w of words) {
    out = out + w[0]
}
console.log(out)
`, "abc")
}

// --- Bitwise operators ---

func TestE2EBitwiseOps(t *testing.T) {
	assertOutput(t, `
const a: number = 10
const b: number = 12
console.log(a & b)
console.log(a | b)
console.log(a ^ b)
console.log(~a)
console.log(a << 1)
console.log(a >> 1)
`, "8\n14\n6\n-11\n20\n5")
}

func TestE2EBitwiseAssign(t *testing.T) {
	assertOutput(t, `
let x: number = 15
x &= 6
console.log(x)
x |= 8
console.log(x)
x ^= 3
console.log(x)
`, "6\n14\n13")
}

// --- Hex / binary / octal literals ---

func TestE2EHexLiterals(t *testing.T) {
	assertOutput(t, `
const mask: number = 0xFF
console.log(mask)
const rgb: number = 0xFF0000
console.log(rgb)
const combined: number = 0xFF & 0b11110000
console.log(combined)
`, "255\n16711680\n240")
}

func TestE2EBinaryOctalLiterals(t *testing.T) {
	assertOutput(t, `
const a: number = 0b0001
const b: number = 0b0110
console.log(a | b)
console.log(a & b)
const perms: number = 0o755
console.log(perms)
`, "7\n0\n493")
}

// --- Null coalescing ?? ---

func TestE2ENullCoalescing(t *testing.T) {
	assertOutput(t, `
const s: string = 'hello'
const result: string = s ?? 'default'
console.log(result)
`, "hello")
}

func TestE2ENullCoalescingNumber(t *testing.T) {
	assertOutput(t, `
const n: number = 42
const r: number = n ?? 99
console.log(r)
`, "42")
}

func TestE2ENullCoalescingChained(t *testing.T) {
	assertOutput(t, `
const a: string = 'first'
const b: string = 'second'
const r: string = a ?? b ?? 'fallback'
console.log(r)
`, "first")
}

// --- Optional chaining ?. ---

func TestE2EOptionalChainingLength(t *testing.T) {
	assertOutput(t, `
const s: string = 'hello'
const n: number = s?.length ?? 0
console.log(n)
`, "5")
}

func TestE2EOptionalChainingCombined(t *testing.T) {
	assertOutput(t, `
const greeting: string = 'world'
const msg: string = greeting ?? 'stranger'
console.log(msg)
const len: number = greeting?.length ?? 0
console.log(len)
`, "world\n5")
}

// --- interface / type alias ---

func TestE2EInterface(t *testing.T) {
	assertOutput(t, `
interface Point { x: number; y: number }
function distance(p: Point): number {
  return Math.floor(Math.sqrt(p.x * p.x + p.y * p.y))
}
const p: Point = { x: 3, y: 4 }
console.log(distance(p))
`, "5")
}

func TestE2ETypeAlias(t *testing.T) {
	assertOutput(t, `
type Rect = { width: number; height: number }
function area(r: Rect): number { return r.width * r.height }
const r: Rect = { width: 6, height: 7 }
console.log(area(r))
`, "42")
}

func TestE2EInterfaceWithString(t *testing.T) {
	assertOutput(t, `
interface User { name: string; age: number }
function greet(u: User): string { return u.name }
const u: User = { name: 'Alice', age: 30 }
console.log(greet(u))
console.log(JSON.stringify(u))
`, "Alice\n{\"name\":\"Alice\",\"age\":30}")
}

func TestE2EInterfaceFloatField(t *testing.T) {
	assertOutput(t, `
interface Point {
  x: number;
  /** @type {float64} */
  score: number;
}
const p: Point = { x: 1, score: 9.5 }
console.log(p.score)
console.log(JSON.stringify(p))
`, "9.5\n{\"x\":1,\"score\":9.5}")
}

func TestE2EInterfaceFloatFieldJSONParse(t *testing.T) {
	assertOutput(t, `
interface Point {
  x: number;
  /** @type {float64} */
  score: number;
}
const p: Point = JSON.parse('{"x":1,"score":9.5}')
console.log(p.x)
console.log(p.score)
`, "1\n9.5")
}

func TestE2EInterfaceReturnType(t *testing.T) {
	assertOutput(t, `
interface Point { x: number; y: number }
function origin(): Point { return { x: 0, y: 0 } }
const p = origin()
console.log(p.x)
console.log(p.y)
`, "0\n0")
}

func TestE2EUnannotatedFunctionReturnsObjectLiteral(t *testing.T) {
	assertOutput(t, `
function makePoint(x, y) { return { x: x, y: y } }
const p = makePoint(3, 4)
console.log(p.x)
console.log(p.y)
`, "3\n4")
}

func TestE2EUnannotatedArrowFunctionReturnsObjectLiteral(t *testing.T) {
	assertOutput(t, `
const makePoint = (x, y) => { return { x: x, y: y } }
const p = makePoint(5, 6)
console.log(p.x)
console.log(p.y)
`, "5\n6")
}

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

func TestE2EObjectShorthandProps(t *testing.T) {
	assertOutput(t, `
const x: number = 1
const y: number = 2
const obj = { x, y }
console.log(obj.x)
console.log(obj.y)
`, "1\n2")
}

func TestE2EObjectShorthandPropsMixed(t *testing.T) {
	assertOutput(t, `
const name: string = 'Alice'
const age: number = 30
const person = { name, age, active: true }
console.log(person.name)
console.log(person.age)
console.log(person.active)
`, "Alice\n30\n1")
}

func TestE2EObjectShorthandPropsInFunction(t *testing.T) {
	assertOutput(t, `
interface Point { x: number; y: number }
function makePoint(x: number, y: number): Point {
    return { x, y }
}
const p = makePoint(3, 4)
console.log(p.x)
console.log(p.y)
`, "3\n4")
}

func TestE2EObjectSpread(t *testing.T) {
	assertOutput(t, `
interface Point { x: number; y: number }
const p: Point = { x: 1, y: 2 }
const copy = { ...p }
console.log(copy.x)
console.log(copy.y)
`, "1\n2")
}

func TestE2EObjectSpreadOverride(t *testing.T) {
	assertOutput(t, `
interface Point { x: number; y: number }
const p: Point = { x: 1, y: 2 }
const overridden = { ...p, y: 20 }
console.log(overridden.x)
console.log(overridden.y)
`, "1\n20")
}

func TestE2EObjectSpreadOverriddenBySpread(t *testing.T) {
	// A spread appearing AFTER an explicit property overrides it, matching JS.
	assertOutput(t, `
interface Point { x: number; y: number }
const p: Point = { x: 1, y: 2 }
const merged = { x: 100, ...p }
console.log(merged.x)
console.log(merged.y)
`, "1\n2")
}

func TestE2EObjectSpreadAddField(t *testing.T) {
	assertOutput(t, `
interface Point { x: number; y: number }
const p: Point = { x: 1, y: 2 }
const withZ = { ...p, z: 3 }
console.log(withZ.x)
console.log(withZ.y)
console.log(withZ.z)
`, "1\n2\n3")
}

func TestE2EObjectSpreadIsShallow(t *testing.T) {
	assertOutput(t, `
interface Inner { v: number }
interface Outer { name: string; inner: Inner }
const o: Outer = { name: 'a', inner: { v: 1 } }
const o2 = { ...o, name: 'b' }
o2.inner.v = 99
console.log(o.inner.v)
`, "99")
}

// --- Map<K,V> ---

func TestE2EMapStringKey(t *testing.T) {
	assertOutput(t, `
const m = new Map<string, number>()
m.set('alice', 95)
m.set('bob', 87)
console.log(m.size)
console.log(m.get('alice'))
console.log(m.has('bob'))
console.log(m.has('dave'))
`, "2\n95\n1\n0")
}

func TestE2EMapDelete(t *testing.T) {
	assertOutput(t, `
const m = new Map<string, number>()
m.set('x', 1)
m.set('y', 2)
m.set('z', 3)
console.log(m.size)
m.delete('y')
console.log(m.size)
console.log(m.has('y'))
console.log(m.get('x'))
`, "3\n2\n0\n1")
}

func TestE2EMapNumberKey(t *testing.T) {
	assertOutput(t, `
const m = new Map<number, number>()
m.set(1, 100)
m.set(2, 200)
m.set(3, 300)
console.log(m.get(2))
console.log(m.has(4))
console.log(m.size)
`, "200\n0\n3")
}

func TestE2EMapOverwrite(t *testing.T) {
	assertOutput(t, `
const m = new Map<string, number>()
m.set('k', 10)
console.log(m.get('k'))
m.set('k', 99)
console.log(m.get('k'))
console.log(m.size)
`, "10\n99\n1")
}

// --- Set<T> ---

func TestE2ESetString(t *testing.T) {
	assertOutput(t, `
const s = new Set<string>()
s.add('apple')
s.add('banana')
s.add('apple')
console.log(s.size)
console.log(s.has('apple'))
console.log(s.has('cherry'))
`, "2\n1\n0")
}

func TestE2ESetDelete(t *testing.T) {
	assertOutput(t, `
const s = new Set<string>()
s.add('a')
s.add('b')
s.add('c')
console.log(s.size)
s.delete('b')
console.log(s.size)
console.log(s.has('b'))
`, "3\n2\n0")
}

// --- JSON.stringify objects ---

func TestE2EJSONStringifyObject(t *testing.T) {
	assertOutput(t, `
const user = { name: 'Alice', age: 30 }
console.log(JSON.stringify(user))
`, `{"name":"Alice","age":30}`)
}

func TestE2EJSONStringifyObjectBool(t *testing.T) {
	assertOutput(t, `
const flag = { enabled: true, count: 5 }
console.log(JSON.stringify(flag))
`, `{"enabled":true,"count":5}`)
}

func TestE2EJSONStringifyObjectNumeric(t *testing.T) {
	assertOutput(t, `
const point = { x: 10, y: 20 }
console.log(JSON.stringify(point))
`, `{"x":10,"y":20}`)
}

func TestE2EJSONStringifyObjectFloat(t *testing.T) {
	assertOutput(t, `
const result = { score: 9.5 }
console.log(JSON.stringify(result))
`, `{"score":9.5}`)
}

func TestE2EJSONStringifyFloatDirect(t *testing.T) {
	assertOutput(t, `
console.log(JSON.stringify(9.5))
`, `9.5`)
}

func TestE2EJSONStringifyObjectDateField(t *testing.T) {
	assertOutput(t, `
const d = new Date(0)
console.log(JSON.stringify({ when: d }))
`, `{"when":"1970-01-01T00:00:00.000Z"}`)
}

func TestE2EJSONStringifyDateDirect(t *testing.T) {
	assertOutput(t, `
const d = new Date(0)
console.log(JSON.stringify(d))
`, `"1970-01-01T00:00:00.000Z"`)
}

func TestE2EJSONStringifyNestedObject(t *testing.T) {
	assertOutput(t, `
const person = { name: 'Alexandros', address: { city: 'Thessaloniki', zip: 10001 } }
console.log(JSON.stringify(person))
`, `{"name":"Alexandros","address":{"city":"Thessaloniki","zip":10001}}`)
}

func TestE2EJSONStringifyBoolArray(t *testing.T) {
	assertOutput(t, `
const flags: boolean[] = [true, false, true]
console.log(JSON.stringify(flags))
const empty: boolean[] = []
console.log(JSON.stringify(empty))
`, "[true,false,true]\n[]")
}

func TestE2EJSONStringifyObjectArray(t *testing.T) {
	assertOutput(t, `
interface Point { x: number; y: number }
const pts: Point[] = [{ x: 1, y: 2 }, { x: 3, y: 4 }]
console.log(JSON.stringify(pts))
`, `[{"x":1,"y":2},{"x":3,"y":4}]`)
}

func TestE2EJSONParseObject(t *testing.T) {
	assertOutput(t, `
interface Point { x: number; y: number }
const p: Point = JSON.parse('{"x":1,"y":2}')
console.log(p.x)
console.log(p.y)
`, "1\n2")
}

func TestE2EJSONParseObjectMixedFields(t *testing.T) {
	assertOutput(t, `
interface Person { name: string; age: number; active: boolean }
const person: Person = JSON.parse('{"name":"Alice","age":30,"active":true}')
console.log(person.name)
console.log(person.age)
console.log(person.active)
`, "Alice\n30\n1")
}

func TestE2EJSONParseObjectMissingField(t *testing.T) {
	assertOutput(t, `
interface Pair { a: number; b: number }
const p: Pair = JSON.parse('{"a":5}')
console.log(p.a)
console.log(p.b)
`, "5\n0")
}

// TestE2EJSONParseObjectMissingStringField is a regression test: a missing
// *string* field used to default to a null pointer (zeroRef's general ptr
// default), which crashed the moment it was printed or concatenated (every
// other string operation in this compiler assumes a `string` value is never
// null) — found while investigating an unrelated, real-world crash in the
// fetch example against a degraded (non-JSON, 503-page) response body.
// Fixed to default to an empty string instead.
func TestE2EJSONParseObjectMissingStringField(t *testing.T) {
	assertOutput(t, `
interface Ip { origin: string }
const p: Ip = JSON.parse('<html>503 Service Unavailable</html>')
console.log("[" + p.origin + "]")
console.log(p.origin.length)
`, "[]\n0")
}

func TestE2EJSONParseObjectEscapedString(t *testing.T) {
	assertOutput(t, `
interface Msg { text: string }
const m: Msg = JSON.parse('{"text":"line1\\nline2 \\"quoted\\""}')
console.log(m.text)
`, "line1\nline2 \"quoted\"")
}

func TestE2ESetNumber(t *testing.T) {
	assertOutput(t, `
const s = new Set<number>()
s.add(10)
s.add(20)
s.add(10)
console.log(s.size)
console.log(s.has(20))
console.log(s.has(30))
`, "2\n1\n0")
}

func TestE2EForOfSet(t *testing.T) {
	assertOutput(t, `
const s = new Set<number>()
s.add(10)
s.add(20)
s.add(30)
for (const v of s) {
    console.log(v)
}
`, "10\n20\n30")
}

func TestE2EForOfSetString(t *testing.T) {
	assertOutput(t, `
const s = new Set<string>()
s.add('a')
s.add('b')
for (const v of s) {
    console.log(v)
}
`, "a\nb")
}

func TestE2EForOfMapValues(t *testing.T) {
	assertOutput(t, `
const m = new Map<string, number>()
m.set('x', 1)
m.set('y', 2)
for (const v of m) {
    console.log(v)
}
`, "1\n2")
}

func TestE2EForOfMapValuesExplicit(t *testing.T) {
	assertOutput(t, `
const m = new Map<string, number>()
m.set('x', 1)
m.set('y', 2)
for (const v of m.values()) {
    console.log(v)
}
for (const k of m.keys()) {
    console.log(k)
}
`, "1\n2\nx\ny")
}

func TestE2EForOfEmptySet(t *testing.T) {
	assertOutput(t, `
const s = new Set<number>()
for (const v of s) {
    console.log('should not print')
}
console.log('done')
`, "done")
}

func TestE2EForOfMapLabeledBreak(t *testing.T) {
	assertOutput(t, `
const m = new Map<string, number>()
m.set('a', 1)
m.set('b', 2)
m.set('c', 3)
outer: for (const v of m) {
    if (v === 2) break outer;
    console.log(v)
}
`, "1")
}

// --- typeof ---

func TestE2ETypeofPrimitives(t *testing.T) {
	assertOutput(t, `
const n: number = 42
const s: string = 'hi'
const b: boolean = true
console.log(typeof n)
console.log(typeof s)
console.log(typeof b)
`, "number\nstring\nboolean")
}

func TestE2ETypeofGuard(t *testing.T) {
	assertOutput(t, `
const x: number = 7
if (typeof x === 'number') { console.log('yes') } else { console.log('no') }
`, "yes")
}

func TestE2ETypeofFunction(t *testing.T) {
	assertOutput(t, `
function add(a: number, b: number): number { return a + b }
console.log(typeof add)
`, "function")
}

// --- Object.keys / Object.values / Object.entries ---

func TestE2EObjectKeys(t *testing.T) {
	assertOutput(t, `
interface Point { x: number; y: number }
const p: Point = { x: 3, y: 4 }
const k = Object.keys(p)
console.log(k[0])
console.log(k[1])
`, "x\ny")
}

func TestE2EObjectValues(t *testing.T) {
	assertOutput(t, `
interface User { name: string; age: number; active: boolean }
const u: User = { name: 'Alexandros', age: 25, active: true }
const v = Object.values(u)
console.log(v[0])
console.log(v[1])
console.log(v[2])
`, "Alexandros\n25\ntrue")
}

func TestE2EObjectEntries(t *testing.T) {
	assertOutput(t, `
interface Config { host: string; port: number }
const c: Config = { host: 'localhost', port: 8080 }
const entries = Object.entries(c)
for (const e of entries) {
  console.log(e.key + '=' + e.value)
}
`, "host=localhost\nport=8080")
}

// --- console methods ---

func TestE2EConsoleInfoDebug(t *testing.T) {
	// info and debug are aliases for log — go to stdout
	assertOutput(t, `
console.info('hello')
console.debug('world')
`, "hello\nworld")
}

func TestE2EConsoleAssertPass(t *testing.T) {
	// passing assertion is silent
	assertOutput(t, `
console.assert(1 === 1, 'should not print')
console.log('ok')
`, "ok")
}

func TestE2EConsoleAssertFail(t *testing.T) {
	// failing assertion prints to stderr; stdout is unaffected
	assertOutput(t, `
console.assert(1 === 2, 'bad math')
console.log('still running')
`, "still running")
}

func TestE2EConsoleDir(t *testing.T) {
	assertOutput(t, `
console.dir("hello")
console.dir(42)
`, "hello\n42")
}

func TestE2EConsoleDirWrongArgCountRejected(t *testing.T) {
	_, err := parseAndCompile(`console.dir()`)
	if err == nil {
		t.Fatal("expected a compile error for console.dir() with no arguments, got none")
	}
}

func TestE2EConsoleTimeEnd(t *testing.T) {
	// The exact elapsed time is non-deterministic (and can even be exactly
	// 0ms if -O2 collapses the timed loop into a closed-form constant, a
	// known, harmless LLVM loop-idiom-recognition artifact — see
	// ADR-00024) — only the fixed "<label>: ...ms" shape is checked here.
	got := compileAndRun(t, `
console.time("mylabel")
console.timeEnd("mylabel")
console.timeEnd()
`)
	lines := strings.Split(got, "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines of output, got %d: %q", len(lines), got)
	}
	if !strings.HasPrefix(lines[0], "mylabel: ") || !strings.HasSuffix(lines[0], "ms") {
		t.Errorf("line 1: got %q, want prefix %q and suffix %q", lines[0], "mylabel: ", "ms")
	}
	if !strings.HasPrefix(lines[1], "default: ") || !strings.HasSuffix(lines[1], "ms") {
		t.Errorf("line 2: got %q, want prefix %q and suffix %q", lines[1], "default: ", "ms")
	}
}

func TestE2EConsoleTimeEndWrongArgCountRejected(t *testing.T) {
	_, err := parseAndCompile(`console.timeEnd("a", "b")`)
	if err == nil {
		t.Fatal("expected a compile error for console.timeEnd with 2 arguments, got none")
	}
}

func TestE2EConsoleCount(t *testing.T) {
	assertOutput(t, `
console.count()
console.count()
console.count("apples")
console.count()
console.count("apples")
console.countReset("apples")
console.count("apples")
`, "default: 1\ndefault: 2\napples: 1\ndefault: 3\napples: 2\napples: 1")
}

func TestE2EConsoleCountWrongArgCountRejected(t *testing.T) {
	_, err := parseAndCompile(`console.count("a", "b")`)
	if err == nil {
		t.Fatal("expected a compile error for console.count with 2 arguments, got none")
	}
}

func TestE2EConsoleGroupIndentsSubsequentOutput(t *testing.T) {
	assertOutput(t, `
console.log("top")
console.group("A")
console.log("inside A")
console.group("A.1")
console.log("inside A.1")
console.groupEnd()
console.log("back in A")
console.groupEnd()
console.log("back to top")
`, "top\nA\n  inside A\n  A.1\n    inside A.1\n  back in A\nback to top")
}

func TestE2EConsoleGroupMultiArgIndentsEveryLine(t *testing.T) {
	assertOutput(t, `
console.group("g")
console.log("a", "b", "c")
console.groupEnd()
`, "g\n  a\n  b\n  c")
}

func TestE2EConsoleGroupEndUnbalancedDoesNotUnderflow(t *testing.T) {
	assertOutput(t, `
console.groupEnd()
console.groupEnd()
console.log("still top level")
`, "still top level")
}

func TestE2EConsoleGroupEndWrongArgCountRejected(t *testing.T) {
	_, err := parseAndCompile(`console.groupEnd("a")`)
	if err == nil {
		t.Fatal("expected a compile error for console.groupEnd with an argument, got none")
	}
}

func TestE2EConsoleGroupWithLogAfterDeadCode(t *testing.T) {
	// Regression test for two bugs found together while implementing
	// console.group: (1) a parser bug (fixed in parseReturnStatement) where
	// a bare `return` followed by an expression on the *next* line parsed
	// as `return <thatExpression>` instead of two separate statements,
	// missing JS's ASI restriction against a line terminator there — which
	// meant "dead" code after a bare `return` wasn't actually being treated
	// as dead at all. That bug was masked for plain console.log calls (no
	// internal branching), but (2) console.group's own indent loop, once
	// added, depended on a value computed before its own labels — dropped
	// as dead code, then referenced by code that looked "live" again purely
	// because emitLabel unconditionally resets blockDone — surfacing an
	// LLVM verifier error ("use of undefined value") instead of silently
	// executing code that looked dead. Fixing bug (1) is what actually
	// fixes this case; see TestE2EReturnASI below for a narrower,
	// console.group-independent test of the parser fix itself.
	assertOutput(t, `
function f(): void {
    console.log("before")
    return
    console.log("after")
}
f()
`, "before")
}

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

// --- enum ---

func TestE2EEnumNumeric(t *testing.T) {
	assertOutput(t, `
enum Direction { Up, Down, Left, Right }
console.log(Direction.Up)
console.log(Direction.Down)
console.log(Direction.Right)
`, "0\n1\n3")
}

func TestE2EEnumExplicitValues(t *testing.T) {
	assertOutput(t, `
enum Status { Active = 1, Inactive = 2, Pending = 10 }
console.log(Status.Active)
console.log(Status.Inactive)
console.log(Status.Pending)
`, "1\n2\n10")
}

func TestE2EEnumAutoIncrementAfterExplicit(t *testing.T) {
	assertOutput(t, `
enum Level { Low = 1, Medium, High, Critical = 100, Fatal }
console.log(Level.Low)
console.log(Level.Medium)
console.log(Level.High)
console.log(Level.Critical)
console.log(Level.Fatal)
`, "1\n2\n3\n100\n101")
}

func TestE2EEnumString(t *testing.T) {
	assertOutput(t, `
enum Suit { Clubs = 'C', Diamonds = 'D', Hearts = 'H', Spades = 'S' }
console.log(Suit.Hearts)
console.log(Suit.Spades)
`, "H\nS")
}

func TestE2EConstEnum(t *testing.T) {
	assertOutput(t, `
const enum Color { Red = 0, Green = 1, Blue = 2 }
function paintIt(c: number): string {
  if (c === Color.Red) { return 'red' }
  if (c === Color.Green) { return 'green' }
  return 'blue'
}
console.log(paintIt(Color.Green))
console.log(paintIt(Color.Blue))
`, "green\nblue")
}

func TestE2EEnumInSwitch(t *testing.T) {
	assertOutput(t, `
enum Op { Add = 0, Sub = 1, Mul = 2 }
function calc(op: number, a: number, b: number): number {
  switch (op) {
    case Op.Add: return a + b
    case Op.Sub: return a - b
    case Op.Mul: return a * b
  }
  return 0
}
console.log(calc(Op.Add, 3, 4))
console.log(calc(Op.Sub, 10, 3))
console.log(calc(Op.Mul, 5, 6))
`, "7\n7\n30")
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

// --- try / catch / throw ---

func TestE2ETryCatchBasic(t *testing.T) {
	assertOutput(t, `
function divide(a: number, b: number): number {
  if (b === 0) { throw new Error('division by zero') }
  return a / b
}
try {
  console.log(divide(10, 2))
} catch (e) {
  console.log('err: ' + e.message)
}
try {
  console.log(divide(10, 0))
} catch (e) {
  console.log('err: ' + e.message)
}
`, "5\nerr: division by zero")
}

func TestE2ETryCatchNoThrow(t *testing.T) {
	assertOutput(t, `
try {
  const x: number = 42
  console.log(x)
} catch (e) {
  console.log('should not reach')
}
`, "42")
}

func TestE2ETryCatchNested(t *testing.T) {
	assertOutput(t, `
function inner(): void {
  throw new Error('from inner')
}
function outer(): void {
  try {
    inner()
  } catch (e) {
    console.log('outer caught: ' + e.message)
    throw new Error('rethrown')
  }
}
try {
  outer()
} catch (e) {
  console.log('top caught: ' + e.message)
}
`, "outer caught: from inner\ntop caught: rethrown")
}

func TestE2ETryFinally(t *testing.T) {
	assertOutput(t, `
let ran: number = 0
try {
  ran = 1
} catch (e) {
  ran = 2
} finally {
  console.log(ran)
}
`, "1")
}

func TestE2EThrowInCatch(t *testing.T) {
	assertOutput(t, `
try {
  throw new Error('original')
} catch (e) {
  console.log('caught: ' + e.message)
}
console.log('done')
`, "caught: original\ndone")
}

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

// --- .length on non-variable array expressions ---

func TestE2ELengthOnObjectKeys(t *testing.T) {
	assertOutput(t, `
const obj = { a: 1, b: 2, c: 3 }
console.log(Object.keys(obj).length)
`, "3")
}

func TestE2ELengthOnArraySlice(t *testing.T) {
	assertOutput(t, `
const nums: number[] = [1, 2, 3, 4, 5]
console.log(nums.slice(2).length)
`, "3")
}

// --- Indexing into non-variable array expressions ---

func TestE2EIndexOnObjectKeys(t *testing.T) {
	assertOutput(t, `
const obj = { x: 1, y: 2, z: 3 }
console.log(Object.keys(obj)[0])
console.log(Object.keys(obj)[2])
`, "x\nz")
}

func TestE2EIndexOnArraySlice(t *testing.T) {
	assertOutput(t, `
const arr: number[] = [10, 20, 30, 40]
console.log(arr.slice(1)[0])
console.log(arr.slice(1)[2])
`, "20\n40")
}

// --- arr.indexOf / arr.includes ---

func TestE2EArrayIndexOf(t *testing.T) {
	assertOutput(t, `
const nums: number[] = [10, 20, 30, 20, 40]
console.log(nums.indexOf(20))
console.log(nums.indexOf(99))
const words: string[] = ['foo', 'bar', 'baz']
console.log(words.indexOf('bar'))
console.log(words.indexOf('nope'))
`, "1\n-1\n1\n-1")
}

func TestE2EArrayIncludes(t *testing.T) {
	assertOutput(t, `
const nums: number[] = [10, 20, 30]
console.log(nums.includes(20))
console.log(nums.includes(99))
`, "1\n0")
}

// --- arr.findIndex ---

func TestE2EArrayFindIndex(t *testing.T) {
	assertOutput(t, `
const nums: number[] = [10, 20, 30, 40]
console.log(nums.findIndex((n: number) => n > 25))
console.log(nums.findIndex((n: number) => n > 999))
`, "2\n-1")
}

// --- arr.concat ---

func TestE2EArrayConcat(t *testing.T) {
	assertOutput(t, `
const a: number[] = [1, 2, 3]
const b: number[] = [4, 5, 6]
const c = a.concat(b)
console.log(c.length)
console.log(c[0])
console.log(c[5])
`, "6\n1\n6")
}

// --- arr.reverse ---

func TestE2EArrayReverse(t *testing.T) {
	assertOutput(t, `
const r: number[] = [1, 2, 3, 4, 5]
r.reverse()
console.log(r[0])
console.log(r[4])
`, "5\n1")
}

// --- arr.fill ---

func TestE2EArrayFill(t *testing.T) {
	assertOutput(t, `
const f: number[] = [0, 0, 0, 0, 0]
f.fill(7)
console.log(f[0])
console.log(f[4])
const g: number[] = [0, 0, 0, 0, 0]
g.fill(9, 1, 3)
console.log(g[0])
console.log(g[1])
console.log(g[3])
`, "7\n7\n0\n9\n0")
}

// --- arr.at ---

func TestE2EArrayAt(t *testing.T) {
	assertOutput(t, `
const arr: number[] = [10, 20, 30]
console.log(arr.at(0))
console.log(arr.at(-1))
console.log(arr.at(1))
`, "10\n30\n20")
}

// --- Array.isArray ---

func TestE2EArrayIsArray(t *testing.T) {
	assertOutput(t, `
const nums: number[] = [1, 2, 3]
console.log(Array.isArray(nums))
console.log(Array.isArray('hello'))
`, "1\n0")
}

// --- str.repeat ---

func TestE2EStringRepeat(t *testing.T) {
	assertOutput(t, `
console.log('ab'.repeat(3))
console.log('x'.repeat(0))
`, "ababab\n")
}

// --- str.at ---

func TestE2EStringAt(t *testing.T) {
	assertOutput(t, `
const s: string = 'hello'
console.log(s.at(0))
console.log(s.at(-1))
console.log(s.at(1))
`, "h\no\ne")
}

func TestE2EStringCharAt(t *testing.T) {
	assertOutput(t, `
const s: string = 'hello'
console.log(s.charAt(0))
console.log(s.charAt(4))
console.log("[" + s.charAt(10) + "]")
console.log("[" + s.charAt(-1) + "]")
`, "h\no\n[]\n[]")
}

func TestE2EStringCharAtWrongArgCountRejected(t *testing.T) {
	_, err := parseAndCompile(`"a".charAt()`)
	if err == nil {
		t.Fatal("expected a compile error for .charAt() with no arguments, got none")
	}
}

func TestE2EStringCodePointAt(t *testing.T) {
	assertOutput(t, `
const s: string = 'hello'
console.log(s.codePointAt(0))
console.log(s.codePointAt(0) === s.charCodeAt(0))
`, "104\n1")
}

func TestE2EStringSearch(t *testing.T) {
	assertOutput(t, `
const s: string = 'hello world'
console.log(s.search('world'))
console.log(s.search('xyz'))
console.log(s.search('world') === s.indexOf('world'))
`, "6\n-1\n1")
}

func TestE2EStringLocaleCompare(t *testing.T) {
	assertOutput(t, `
console.log('apple'.localeCompare('banana'))
console.log('banana'.localeCompare('apple'))
console.log('apple'.localeCompare('apple'))
`, "-1\n1\n0")
}

func TestE2EStringLocaleCompareWrongArgCountRejected(t *testing.T) {
	_, err := parseAndCompile(`"a".localeCompare()`)
	if err == nil {
		t.Fatal("expected a compile error for .localeCompare() with no arguments, got none")
	}
}

// --- str.padStart / str.padEnd ---

func TestE2EStringPadStart(t *testing.T) {
	assertOutput(t, `
console.log('5'.padStart(3, '0'))
console.log('hello'.padStart(3))
console.log('hi'.padStart(5, 'ab'))
`, "005\nhello\nabahi")
}

func TestE2EStringPadEnd(t *testing.T) {
	assertOutput(t, `
console.log('5'.padEnd(4, '0'))
console.log('hi'.padEnd(6, '!-'))
`, "5000\nhi!-!-")
}

func TestE2EStringTrimStartEnd(t *testing.T) {
	assertOutput(t, `
console.log("[" + "  hello  ".trimStart() + "]")
console.log("[" + "  hello  ".trimEnd() + "]")
console.log("[" + "hello".trimStart() + "]")
console.log("[" + "   ".trimStart() + "]")
console.log("[" + "   ".trimEnd() + "]")
console.log("[" + "".trimStart() + "]")
console.log("[" + "".trimEnd() + "]")
`, "[hello  ]\n[  hello]\n[hello]\n[]\n[]\n[]\n[]")
}

func TestE2EStringPadEmptyFill(t *testing.T) {
	assertOutput(t, `
console.log('ab'.padStart(5, ''))
console.log('ab'.padEnd(5, ''))
`, "ab\nab")
}

func TestE2EStringSplitEmptySeparator(t *testing.T) {
	assertOutput(t, `
const chars: string[] = "abc".split("")
console.log(chars.length)
console.log(chars[0])
console.log(chars[2])
const empty: string[] = "".split("")
console.log(empty.length)
`, "3\na\nc\n0")
}

// --- n.toFixed ---

func TestE2ENumberToFixed(t *testing.T) {
	assertOutput(t, `
console.log((3.14159).toFixed(2))
console.log((42).toFixed(0))
console.log((1.5).toFixed(3))
`, "3.14\n42\n1.500")
}

// --- null / undefined ---

func TestE2ENullLiteral(t *testing.T) {
	assertOutput(t, `
let x: string | null = null
console.log(x === null)
console.log(x !== null)
x = "hello"
console.log(x === null)
console.log(x !== null)
`, "1\n0\n0\n1")
}

func TestE2ENullInTemplate(t *testing.T) {
	assertOutput(t, `
const n: string | null = null
console.log(`+"`"+`value is ${n}`+"`"+`)
`, "value is null")
}

func TestE2EUndefinedInTemplate(t *testing.T) {
	assertOutput(t, `
const u = undefined
console.log(`+"`"+`u is ${u}`+"`"+`)
`, "u is undefined")
}

func TestE2ENullNullishCoalesce(t *testing.T) {
	assertOutput(t, `
const a = null ?? "fallback"
const b: string | null = "real"
const c = b ?? "fallback"
console.log(a)
console.log(c)
`, "fallback\nreal")
}

func TestE2ENullEquality(t *testing.T) {
	assertOutput(t, `
console.log(null === null)
console.log(null === undefined)
console.log(null !== null)
`, "1\n1\n0")
}

func TestE2ENullOptionalChain(t *testing.T) {
	assertOutput(t, `
const s: string | null = null
console.log(s?.length)
const t2: string | null = "hello"
console.log(t2?.length)
`, "0\n5")
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

func TestE2EAsyncAwaitNumber(t *testing.T) {
	assertOutput(t, `
async function add(a: number, b: number): Promise<number> {
    return a + b
}
const result = await add(3, 4)
console.log(result)
`, "7")
}

func TestE2EAsyncAwaitString(t *testing.T) {
	assertOutput(t, `
async function greet(name: string): Promise<string> {
    return "Hello, " + name + "!"
}
const msg = await greet("world")
console.log(msg)
`, "Hello, world!")
}

func TestE2EAsyncAwaitVoid(t *testing.T) {
	assertOutput(t, `
async function doNothing(): Promise<void> {
    console.log("doing nothing")
}
await doNothing()
`, "doing nothing")
}

func TestE2EAsyncChained(t *testing.T) {
	assertOutput(t, `
async function double(n: number): Promise<number> {
    return n * 2
}
async function addOne(n: number): Promise<number> {
    return n + 1
}
const a = await double(5)
const b = await addOne(a)
console.log(b)
`, "11")
}

// --- process.argv / process.exit / process.env ---

func TestE2EProcessArgv(t *testing.T) {
	t.Helper()
	got := compileAndRunWithArgs(t, `
const args: string[] = process.argv
console.log(args.length)
console.log(args[1])
console.log(args[2])
`, "hello", "world")
	want := "3\nhello\nworld"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestE2EProcessExit(t *testing.T) {
	stdout, code := compileAndRunExpectExit(t, `
console.log("before")
process.exit(42)
console.log("after")
`)
	if stdout != "before" {
		t.Errorf("stdout: got %q, want %q", stdout, "before")
	}
	if code != 42 {
		t.Errorf("exit code: got %d, want 42", code)
	}
}

func TestE2EProcessEnv(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not found in PATH")
	}
	binFile := buildBinary(t, `
const fromDot: string = process.env.KML_TEST_VAR
console.log(fromDot)
const key: string = "KML_TEST_VAR"
const fromBracket: string = process.env[key]
console.log(fromBracket)
const missing = process.env.KML_TEST_VAR_MISSING ?? "default"
console.log(missing)
`)
	cmd := exec.Command(binFile)
	cmd.Env = append(os.Environ(), "KML_TEST_VAR=hello-env")
	result, err := cmd.Output()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	got := strings.TrimRight(string(result), "\n")
	want := "hello-env\nhello-env\ndefault"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// --- any / unknown (Staged V1: declare/assign/reassign, print, typeof, ===/!==) ---

func TestE2EAnyReassignAcrossTypes(t *testing.T) {
	assertOutput(t, `
let x: any = 5
console.log(x)
x = "hello"
console.log(x)
x = true
console.log(x)
x = null
console.log(x)
`, "5\nhello\ntrue\nnull")
}

func TestE2EAnyTemplateLiteral(t *testing.T) {
	assertOutput(t, `
let x: any = 42
console.log(` + "`value: ${x}`" + `)
x = "world"
console.log(` + "`value: ${x}`" + `)
`, "value: 42\nvalue: world")
}

func TestE2EAnyTypeofRuntime(t *testing.T) {
	assertOutput(t, `
let x: any = 5
console.log(typeof x)
x = "hi"
console.log(typeof x)
x = true
console.log(typeof x)
x = null
console.log(typeof x)
let y: any
console.log(typeof y)
`, "number\nstring\nboolean\nobject\nundefined")
}

func TestE2EAnyEquality(t *testing.T) {
	assertOutput(t, `
let a: any = 5
let b: any = 5
console.log(a === b)
let c: any = "5"
console.log(a === c)
console.log(a !== c)
let d: any = 5.0
console.log(a === d)
console.log(a === 5)
`, "1\n0\n1\n1\n1")
}

func TestE2EUnknownFloat(t *testing.T) {
	assertOutput(t, `
let y: unknown = 3.14
console.log(y)
console.log(typeof y)
`, "3.14\nnumber")
}

func TestE2EAnyArithmeticRejected(t *testing.T) {
	_, err := parseAndCompile(`
let x: any = 5
console.log(x + 1)
`)
	if err == nil {
		t.Fatal("expected a compile error for arithmetic on an any-typed value, got none")
	}
}

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

func TestE2EAnyAsFunctionParamRejected(t *testing.T) {
	_, err := parseAndCompile(`
function f(x: any): void { console.log(x) }
f(5)
`)
	if err == nil {
		t.Fatal("expected a compile error for any as a function parameter type, got none")
	}
}

func TestE2EAnyArrayElementRejected(t *testing.T) {
	_, err := parseAndCompile(`
let arr: any[] = [1, 2, 3]
console.log(arr.length)
`)
	if err == nil {
		t.Fatal("expected a compile error for any as an array element type, got none")
	}
}

// --- Math trig/hyperbolic/misc additions ---

func TestE2EMathTrigInverse(t *testing.T) {
	assertOutput(t, `
console.log(Math.acos(1.0))
console.log(Math.round(Math.asin(1.0) * 2.0))
console.log(Math.round(Math.atan(1.0) * 4.0))
console.log(Math.round(Math.atan2(1.0, 1.0) * 4.0))
`, "0\n3\n3\n3")
}

func TestE2EMathHyperbolic(t *testing.T) {
	assertOutput(t, `
console.log(Math.sinh(0.0))
console.log(Math.cosh(0.0))
console.log(Math.tanh(0.0))
`, "0\n1\n0")
}

func TestE2EMathCbrtExpm1Log1p(t *testing.T) {
	assertOutput(t, `
console.log(Math.cbrt(27.0))
console.log(Math.expm1(0.0))
console.log(Math.log1p(0.0))
`, "3\n0\n0")
}

// --- Date (UTC only, not local time — see docs/adr/ADR-00014.md) ---

func TestE2EDateEpoch(t *testing.T) {
	assertOutput(t, `
const d: Date = new Date(0)
console.log(d.getFullYear())
console.log(d.getMonth())
console.log(d.getDate())
console.log(d.getDay())
console.log(d.getHours())
console.log(d.getMinutes())
console.log(d.getSeconds())
console.log(d.getMilliseconds())
console.log(d.getTime())
console.log(d.valueOf())
console.log(d.toISOString())
`, "1970\n0\n1\n4\n0\n0\n0\n0\n0\n0\n1970-01-01T00:00:00.000Z")
}

func TestE2EDateFromTimestamp(t *testing.T) {
	assertOutput(t, `
const d: Date = new Date(1700000000000)
console.log(d.toISOString())
console.log(d.getFullYear())
console.log(d.getMonth())
console.log(d.getDate())
`, "2023-11-14T22:13:20.000Z\n2023\n10\n14")
}

func TestE2EDateFromStringLiteral(t *testing.T) {
	assertOutput(t, `
const d: Date = new Date("2023-11-14T00:00:00.000Z")
console.log(d.getTime())
console.log(d.toISOString())
`, "1699920000000\n2023-11-14T00:00:00.000Z")
}

func TestE2EDateFromInvalidStringLiteral(t *testing.T) {
	assertOutput(t, `
const d: Date = new Date("not a date")
console.log(d.getTime())
`, "-1")
}

func TestE2EDateMultiArgConstructor(t *testing.T) {
	assertOutput(t, `
const d: Date = new Date(2023, 10, 14)
console.log(d.toISOString())
console.log(d.getFullYear())
console.log(d.getMonth())
console.log(d.getDate())
`, "2023-11-14T00:00:00.000Z\n2023\n10\n14")
}

func TestE2EDateMultiArgConstructorFullFields(t *testing.T) {
	assertOutput(t, `
const d: Date = new Date(2023, 10, 14, 22, 13, 20, 500)
console.log(d.toISOString())
`, "2023-11-14T22:13:20.500Z")
}

func TestE2EDateMultiArgConstructorDefaultsDay(t *testing.T) {
	assertOutput(t, `
const d: Date = new Date(2023, 0)
console.log(d.toISOString())
`, "2023-01-01T00:00:00.000Z")
}

func TestE2EDateNow(t *testing.T) {
	assertOutput(t, `
const now: number = Date.now()
console.log(now > 1700000000000)
const d: Date = new Date()
console.log(d.getTime() > 1700000000000)
`, "1\n1")
}

func TestE2EDateUntypedInference(t *testing.T) {
	// Date must be recognized when inferred from an untyped declaration, not
	// just via an explicit `: Date` annotation.
	assertOutput(t, `
const d = new Date(0)
console.log(d.getFullYear())

function epoch(): Date {
    return new Date(0)
}
const e = epoch()
console.log(e.getFullYear())
`, "1970\n1970")
}

func TestE2EDateAsFunctionParam(t *testing.T) {
	assertOutput(t, `
function year(d: Date): number {
    return d.getFullYear()
}
const d: Date = new Date(0)
console.log(year(d))
`, "1970")
}

func TestE2EUntypedNewError(t *testing.T) {
	// Regression guard: found alongside the Date work — an untyped `const`
	// initialized from `new Error(...)` previously fell back to a plain i64
	// default (the same missing-case bug that affected Date), so `.message`
	// access failed with "field access on non-object".
	assertOutput(t, `
const e = new Error('oops')
console.log(e.message)
`, "oops")
}

func TestE2EDateAsObjectField(t *testing.T) {
	assertOutput(t, `
interface Event { name: string; when: Date }
const ev: Event = { name: 'launch', when: new Date(0) }
console.log(ev.name)
console.log(ev.when.getFullYear())
`, "launch\n1970")
}

func TestE2EDateParseFullISO(t *testing.T) {
	assertOutput(t, `
const ms: number = Date.parse("1970-01-01T00:00:00.000Z")
console.log(ms)
const ms2: number = Date.parse("2023-11-14T22:13:20.000Z")
console.log(ms2)
`, "0\n1700000000000")
}

func TestE2EDateParseWithoutMillis(t *testing.T) {
	assertOutput(t, `
console.log(Date.parse("2023-11-14T22:13:20Z"))
`, "1700000000000")
}

func TestE2EDateParseDateOnly(t *testing.T) {
	assertOutput(t, `
console.log(Date.parse("2023-11-14"))
`, "1699920000000")
}

func TestE2EDateParseInvalid(t *testing.T) {
	assertOutput(t, `
console.log(Date.parse("not a date"))
`, "-1")
}

func TestE2EDateParseRoundTrip(t *testing.T) {
	assertOutput(t, `
const d: Date = new Date(Date.parse("2023-11-14T22:13:20.000Z"))
console.log(d.toISOString())
console.log(d.getFullYear())
`, "2023-11-14T22:13:20.000Z\n2023")
}

func TestE2EDateParseOffsetWithMillis(t *testing.T) {
	assertOutput(t, `
console.log(Date.parse("2023-11-14T22:13:20.000+02:00"))
console.log(Date.parse("2023-11-14T22:13:20.000-05:00"))
`, "1699992800000\n1700018000000")
}

func TestE2EDateParseOffsetWithoutMillis(t *testing.T) {
	assertOutput(t, `
console.log(Date.parse("2023-11-14T22:13:20+02:00"))
console.log(Date.parse("2023-11-14T22:13:20-05:00"))
`, "1699992800000\n1700018000000")
}

func TestE2EDateParseOffsetZeroMatchesZ(t *testing.T) {
	assertOutput(t, `
console.log(Date.parse("2023-11-14T22:13:20.000+00:00"))
console.log(Date.parse("2023-11-14T22:13:20.000Z"))
`, "1700000000000\n1700000000000")
}

func TestE2EDateParseOffsetHalfHour(t *testing.T) {
	assertOutput(t, `
console.log(Date.parse("2023-11-14T22:13:20.000+05:30"))
`, "1699980200000")
}

func TestE2EDateParseOffsetNegativeZeroHour(t *testing.T) {
	// "-00:30": zero-hour part with a negative sign — a real edge case where
	// naively parsing the hour field as a signed integer loses the sign
	// (-0 == 0), silently misinterpreting this as a positive/zero offset.
	assertOutput(t, `
console.log(Date.parse("2023-11-14T22:13:20.000-00:30"))
`, "1700001800000")
}

func TestE2EDateParseOffsetRoundTrip(t *testing.T) {
	assertOutput(t, `
const d: Date = new Date(Date.parse("2023-11-14T22:13:20.000+02:00"))
console.log(d.toISOString())
`, "2023-11-14T20:13:20.000Z")
}

func TestE2EDateParseUntypedInference(t *testing.T) {
	assertOutput(t, `
const a = Date.parse("1970-01-01T00:00:00.000Z")
console.log(a)

function parseIt(s: string): number {
    return Date.parse(s)
}
console.log(parseIt("2023-11-14T22:13:20.000Z"))
`, "0\n1700000000000")
}

func TestE2EDateSetFullYear(t *testing.T) {
	assertOutput(t, `
const d: Date = new Date(0)
const returned: number = d.setFullYear(2020)
console.log(returned)
console.log(d.getFullYear())
console.log(d.toISOString())
`, "1577836800000\n2020\n2020-01-01T00:00:00.000Z")
}

func TestE2EDateSetAllFields(t *testing.T) {
	assertOutput(t, `
const d: Date = new Date(0)
d.setFullYear(2020)
d.setMonth(5)
d.setDate(15)
d.setHours(12)
d.setMinutes(30)
d.setSeconds(45)
d.setMilliseconds(500)
console.log(d.toISOString())
`, "2020-06-15T12:30:45.500Z")
}

func TestE2EDateSetTime(t *testing.T) {
	assertOutput(t, `
const d: Date = new Date(0)
d.setFullYear(2020)
const t: number = d.setTime(0)
console.log(t)
console.log(d.toISOString())
`, "0\n1970-01-01T00:00:00.000Z")
}

func TestE2EDateSetterOverflowRollsOverLikeRealJS(t *testing.T) {
	assertOutput(t, `
const d1: Date = new Date(0)
d1.setMonth(12)
console.log(d1.toISOString())

const d2: Date = new Date(0)
d2.setDate(32)
console.log(d2.toISOString())
`, "1971-01-01T00:00:00.000Z\n1970-02-01T00:00:00.000Z")
}

func TestE2EDateSetterOnObjectFieldRejected(t *testing.T) {
	_, err := parseAndCompile(`
interface Box { when: Date }
const b: Box = { when: new Date(0) }
b.when.setFullYear(2020)
`)
	if err == nil {
		t.Fatal("expected a compile error for a Date setter on a non-identifier receiver, got none")
	}
}

func TestE2EDateSetterMultiArgRejected(t *testing.T) {
	_, err := parseAndCompile(`
const d: Date = new Date(0)
d.setFullYear(2020, 5)
`)
	if err == nil {
		t.Fatal("expected a compile error for a multi-argument Date setter overload, got none")
	}
}

func TestE2EDateMinusDateGivesMillisDifference(t *testing.T) {
	assertOutput(t, `
const d1: Date = new Date(1000)
const d2: Date = new Date(3000)
const diff: number = d1 - d2
console.log(diff)
console.log(d2 - d1)
`, "-2000\n2000")
}

func TestE2EDatePlusNumberGivesNewDate(t *testing.T) {
	assertOutput(t, `
const d1: Date = new Date(1000)
console.log((d1 + 500).getTime())
console.log((500 + d1).getTime())
console.log((d1 + 86400000).toISOString())
`, "1500\n1500\n1970-01-02T00:00:01.000Z")
}

func TestE2EDateMinusNumberGivesNewDate(t *testing.T) {
	assertOutput(t, `
const d1: Date = new Date(10000)
console.log((d1 - 500).getTime())
console.log((d1 - 10000).toISOString())
`, "9500\n1970-01-01T00:00:00.000Z")
}

func TestE2EDateArithmeticUntypedInference(t *testing.T) {
	assertOutput(t, `
const d1: Date = new Date(0)
const later = d1 + 86400000
console.log(later.getFullYear())
console.log(later.toISOString())

const diff = later - d1
console.log(diff)
`, "1970\n1970-01-02T00:00:00.000Z\n86400000")
}

func TestE2EDateCompoundAssignAddsDuration(t *testing.T) {
	assertOutput(t, `
let d: Date = new Date(0)
d += 86400000
console.log(d.toISOString())
d -= 3600000
console.log(d.toISOString())
`, "1970-01-02T00:00:00.000Z\n1970-01-01T23:00:00.000Z")
}

func TestE2EDatePlusDateRejected(t *testing.T) {
	_, err := parseAndCompile(`
const d1: Date = new Date(1000)
const d2: Date = new Date(3000)
console.log(d1 + d2)
`)
	if err == nil {
		t.Fatal("expected a compile error for adding two Dates together, got none")
	}
}

func TestE2ENumberMinusDateRejected(t *testing.T) {
	_, err := parseAndCompile(`
const d1: Date = new Date(1000)
console.log(500 - d1)
`)
	if err == nil {
		t.Fatal("expected a compile error for subtracting a Date from a number, got none")
	}
}

func TestE2EDateCompoundAssignDateRejected(t *testing.T) {
	_, err := parseAndCompile(`
let d1: Date = new Date(1000)
const d2: Date = new Date(3000)
d1 += d2
`)
	if err == nil {
		t.Fatal("expected a compile error for compound-assigning a Date into a Date, got none")
	}
}

func TestE2EDateToDateString(t *testing.T) {
	assertOutput(t, `
const d1: Date = new Date(0)
console.log(d1.toDateString())
const d2: Date = new Date(1700000000000)
console.log(d2.toDateString())
`, "Thu Jan 01 1970\nTue Nov 14 2023")
}

func TestE2EDateToLocaleDateString(t *testing.T) {
	assertOutput(t, `
const d1: Date = new Date(0)
console.log(d1.toLocaleDateString())
const d2: Date = new Date(1700000000000)
console.log(d2.toLocaleDateString())
`, "1/1/1970\n11/14/2023")
}

func TestE2EDateFormattingUntypedInferenceAndFunctionChain(t *testing.T) {
	assertOutput(t, `
const d: Date = new Date(0)
const s = d.toDateString()
console.log(s)

function fmt(x: Date): string {
    return x.toLocaleDateString()
}
console.log(fmt(d))
`, "Thu Jan 01 1970\n1/1/1970")
}

// --- fetch / Response ---
//
// These spin up a local httptest.Server rather than hitting a real external
// URL, so the suite stays deterministic and offline-capable — but they still
// exercise the real libcurl HTTP client path end to end (a local server is a
// real TCP connection with real HTTP framing, not a mocked-out call site).

func newFetchTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/flat", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"title":"hello","count":42,"active":true}`)
	})
	mux.HandleFunc("/notfound", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, "not found")
	})
	mux.HandleFunc("/servererror", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	mux.HandleFunc("/redirect", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/flat", http.StatusFound)
	})
	mux.HandleFunc("/large", func(w http.ResponseWriter, r *http.Request) {
		body := strings.Repeat("x", 40000)
		fmt.Fprint(w, body)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestE2EFetchStatusAndText(t *testing.T) {
	srv := newFetchTestServer(t)
	src := fmt.Sprintf(`
async function main2(): Promise<void> {
    const r: Response = await fetch("%s/flat")
    console.log(r.status)
    console.log(r.ok)
    const body: string = r.text()
    console.log(body)
}
main2()
`, srv.URL)
	assertOutput(t, src, "200\n1\n"+`{"title":"hello","count":42,"active":true}`)
}

func TestE2EFetchNotFoundHasOkFalse(t *testing.T) {
	srv := newFetchTestServer(t)
	src := fmt.Sprintf(`
async function main2(): Promise<void> {
    const r: Response = await fetch("%s/notfound")
    console.log(r.status)
    console.log(r.ok)
}
main2()
`, srv.URL)
	assertOutput(t, src, "404\n0")
}

func TestE2EFetchServerErrorHasOkFalse(t *testing.T) {
	srv := newFetchTestServer(t)
	src := fmt.Sprintf(`
async function main2(): Promise<void> {
    const r: Response = await fetch("%s/servererror")
    console.log(r.status)
    console.log(r.ok)
}
main2()
`, srv.URL)
	assertOutput(t, src, "500\n0")
}

func TestE2EFetchFollowsRedirects(t *testing.T) {
	srv := newFetchTestServer(t)
	src := fmt.Sprintf(`
async function main2(): Promise<void> {
    const r: Response = await fetch("%s/redirect")
    console.log(r.status)
}
main2()
`, srv.URL)
	assertOutput(t, src, "200")
}

func TestE2EFetchJSONIntoTypedTarget(t *testing.T) {
	srv := newFetchTestServer(t)
	src := fmt.Sprintf(`
interface FlatData { title: string; count: number; active: boolean }

async function main2(): Promise<void> {
    const data: FlatData = (await fetch("%s/flat")).json()
    console.log(data.title)
    console.log(data.count)
    console.log(data.active)
}
main2()
`, srv.URL)
	assertOutput(t, src, "hello\n42\n1")
}

func TestE2EFetchUntypedInference(t *testing.T) {
	srv := newFetchTestServer(t)
	src := fmt.Sprintf(`
async function main2(): Promise<void> {
    const p = fetch("%s/flat")
    const r = await p
    console.log(r.status)
}
main2()
`, srv.URL)
	assertOutput(t, src, "200")
}

func TestE2EFetchLargeBody(t *testing.T) {
	srv := newFetchTestServer(t)
	src := fmt.Sprintf(`
async function main2(): Promise<void> {
    const r: Response = await fetch("%s/large")
    const body: string = r.text()
    console.log(body.length)
}
main2()
`, srv.URL)
	assertOutput(t, src, "40000")
}

func TestE2EFetchNetworkFailureThrows(t *testing.T) {
	src := `
async function main2(): Promise<void> {
    try {
        const r: Response = await fetch("http://127.0.0.1:1/unreachable")
        console.log(r.status)
    } catch (e) {
        console.log("caught")
    }
}
main2()
`
	assertOutput(t, src, "caught")
}

func TestE2EFetchWrongArgCountRejected(t *testing.T) {
	_, err := parseAndCompile(`fetch("a", "b")`)
	if err == nil {
		t.Fatal("expected a compile error for fetch() with the wrong argument count, got none")
	}
}

func TestE2EFetchFieldAccessOnNonResponseRejected(t *testing.T) {
	_, err := parseAndCompile(`
const x: number = 5
console.log(x.status)
`)
	if err == nil {
		t.Fatal("expected a compile error for accessing .status on a non-Response value, got none")
	}
}

// --- imports / exports (multi-file) ---

func TestE2EImportFunctionsAndInterface(t *testing.T) {
	assertMultiFileOutput(t, map[string]string{
		"math.ts": `
export function add(a: number, b: number): number {
    return a + b
}
export function mul(a: number, b: number): number {
    return a * b
}
export interface Point { x: number; y: number }
`,
		"main.ts": `
import { add, mul } from './math'
import { Point } from './math'

console.log(add(2, 3))
console.log(mul(4, 5))

const p: Point = { x: 1, y: 2 }
console.log(p.x + p.y)
`,
	}, "main.ts", "5\n20\n3")
}

func TestE2EImportEnumAndTypeAliasThroughChain(t *testing.T) {
	// a imports from b (and also directly from c); b imports from c —
	// a 3-file, diamond-shaped import graph.
	assertMultiFileOutput(t, map[string]string{
		"c.ts": `
export enum Color { Red, Green, Blue }
export type Pair = { a: number; b: number }
`,
		"b.ts": `
import { Color, Pair } from './c'
export function describe(c: Color): string {
    if (c === Color.Red) return "red"
    return "other"
}
export function makePair(a: number, b: number): Pair {
    return { a, b }
}
`,
		"a.ts": `
import { describe, makePair } from './b'
import { Color } from './c'
console.log(describe(Color.Red))
console.log(describe(Color.Blue))
const p = makePair(10, 20)
console.log(p.a + p.b)
`,
	}, "a.ts", "red\nother\n30")
}

func TestE2EImportCircular(t *testing.T) {
	assertMultiFileOutput(t, map[string]string{
		"circA.ts": `
import { helperB } from './circB'
export function helperA(): number { return 1 }
export function useB(): number { return helperB() }
`,
		"circB.ts": `
import { helperA } from './circA'
export function helperB(): number { return 2 }
export function useA(): number { return helperA() }
`,
		"main.ts": `
import { useB } from './circA'
import { useA } from './circB'
console.log(useB())
console.log(useA())
`,
	}, "main.ts", "2\n1")
}

func TestE2EImportNonExportedNameRejected(t *testing.T) {
	_, err := resolveMultiFile(t, map[string]string{
		"math.ts": `
function internalHelper(): number { return 42 }
export function add(a: number, b: number): number { return a + b }
`,
		"main.ts": `
import { internalHelper } from './math'
console.log(internalHelper())
`,
	}, "main.ts")
	if err == nil {
		t.Fatal("expected a compile error for importing a non-exported name, got none")
	}
}

func TestE2EImportUnknownNameRejected(t *testing.T) {
	_, err := resolveMultiFile(t, map[string]string{
		"math.ts": `export function add(a: number, b: number): number { return a + b }`,
		"main.ts": `
import { doesNotExist } from './math'
console.log(doesNotExist())
`,
	}, "main.ts")
	if err == nil {
		t.Fatal("expected a compile error for importing a name that doesn't exist, got none")
	}
}

func TestE2EImportExecutableStatementInNonEntryFileRejected(t *testing.T) {
	_, err := resolveMultiFile(t, map[string]string{
		"sideeffect.ts": `
export function foo(): number { return 1 }
console.log("side effect")
`,
		"main.ts": `
import { foo } from './sideeffect'
console.log(foo())
`,
	}, "main.ts")
	if err == nil {
		t.Fatal("expected a compile error for an executable top-level statement in a non-entry file, got none")
	}
}

func TestE2EImportDuplicateNameAcrossFilesRejected(t *testing.T) {
	_, err := resolveMultiFile(t, map[string]string{
		"math.ts": `export function add(a: number, b: number): number { return a + b }`,
		"dup.ts":  `export function add(a: number, b: number): number { return a - b }`,
		"main.ts": `
import { add } from './math'
import { add } from './dup'
console.log(add(1, 2))
`,
	}, "main.ts")
	if err == nil {
		t.Fatal("expected a compile error for the same name declared in two different imported files, got none")
	}
}

func TestE2EImportNonexistentModuleRejected(t *testing.T) {
	_, err := resolveMultiFile(t, map[string]string{
		"main.ts": `
import { x } from './doesnotexist'
console.log(x)
`,
	}, "main.ts")
	if err == nil {
		t.Fatal("expected a compile error for importing a nonexistent module, got none")
	}
}

func TestE2EImportBarePackageNameRejected(t *testing.T) {
	_, err := resolveMultiFile(t, map[string]string{
		"main.ts": `
import { x } from 'somepackage'
console.log(x)
`,
	}, "main.ts")
	if err == nil {
		t.Fatal("expected a compile error for a non-relative (bare) import path, got none")
	}
}

func TestE2EImportAliasingRejected(t *testing.T) {
	_, err := resolveMultiFile(t, map[string]string{
		"math.ts": `export function add(a: number, b: number): number { return a + b }`,
		"main.ts": `
import { add as sum } from './math'
console.log(sum(1, 2))
`,
	}, "main.ts")
	if err == nil {
		t.Fatal("expected a compile error for import aliasing ('as'), which is not yet supported, got none")
	}
}

// --- fs (readFileSync/writeFileSync/appendFileSync/existsSync/unlinkSync) ---

func TestE2EFsWriteReadAppendUnlink(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	src := fmt.Sprintf(`
const path: string = %q
console.log(fs.existsSync(path))
fs.writeFileSync(path, "hello")
console.log(fs.existsSync(path))
const content: string = fs.readFileSync(path)
console.log(content)
fs.appendFileSync(path, " world")
console.log(fs.readFileSync(path))
fs.unlinkSync(path)
console.log(fs.existsSync(path))
`, path)
	assertOutput(t, src, "0\n1\nhello\nhello world\n0")
}

func TestE2EFsWriteFileSyncOverwritesExistingContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	src := fmt.Sprintf(`
fs.writeFileSync(%q, "first")
fs.writeFileSync(%q, "second")
console.log(fs.readFileSync(%q))
`, path, path, path)
	assertOutput(t, src, "second")
}

func TestE2EFsReadFileSyncUntypedInference(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	src := fmt.Sprintf(`
fs.writeFileSync(%q, "abc")
const content = fs.readFileSync(%q)
console.log(content.length)
`, path, path)
	assertOutput(t, src, "3")
}

func TestE2EFsReadFileSyncNonexistentThrows(t *testing.T) {
	src := `
try {
    const content: string = fs.readFileSync("/definitely/does/not/exist/kml-test-file.txt")
    console.log(content)
} catch (e) {
    console.log("caught")
}
`
	assertOutput(t, src, "caught")
}

func TestE2EFsReadFileSyncNonexistentUncaughtExitsNonZero(t *testing.T) {
	_, exitCode := compileAndRunExpectExit(t, `
const content: string = fs.readFileSync("/definitely/does/not/exist/kml-test-file.txt")
console.log(content)
`)
	if exitCode == 0 {
		t.Fatal("expected a non-zero exit code for an uncaught fs.readFileSync failure, got 0")
	}
}

func TestE2EFsUnlinkSyncNonexistentThrows(t *testing.T) {
	src := `
try {
    fs.unlinkSync("/definitely/does/not/exist/kml-test-file.txt")
} catch (e) {
    console.log("caught")
}
`
	assertOutput(t, src, "caught")
}

func TestE2EFsWriteFileSyncWrongArgCountRejected(t *testing.T) {
	_, err := parseAndCompile(`fs.writeFileSync("a")`)
	if err == nil {
		t.Fatal("expected a compile error for fs.writeFileSync with the wrong argument count, got none")
	}
}

func TestE2EFsReadFileSyncWrongArgCountRejected(t *testing.T) {
	_, err := parseAndCompile(`fs.readFileSync("a", "b")`)
	if err == nil {
		t.Fatal("expected a compile error for fs.readFileSync with the wrong argument count, got none")
	}
}

// --- fs.mkdirSync / renameSync / copyFileSync / readdirSync ---

func TestE2EFsMkdirSyncCreatesDirectory(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "newdir")
	src := fmt.Sprintf(`
console.log(fs.existsSync(%q))
fs.mkdirSync(%q)
console.log(fs.existsSync(%q))
`, sub, sub, sub)
	assertOutput(t, src, "0\n1")
}

func TestE2EFsMkdirSyncAlreadyExistsThrows(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "newdir")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatalf("os.Mkdir(%q): %v", sub, err)
	}
	src := fmt.Sprintf(`
try {
    fs.mkdirSync(%q)
    console.log("should not print")
} catch (e) {
    console.log(e.message.startsWith("cannot create directory '%s': "))
}
`, sub, sub)
	assertOutput(t, src, "1")
}

func TestE2EFsMkdirSyncWrongArgCountRejected(t *testing.T) {
	_, err := parseAndCompile(`fs.mkdirSync()`)
	if err == nil {
		t.Fatal("expected a compile error for fs.mkdirSync() with no arguments, got none")
	}
}

func TestE2EFsRmdirSyncRemovesEmptyDirectory(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "toremove")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatalf("os.Mkdir(%q): %v", sub, err)
	}
	src := fmt.Sprintf(`
console.log(fs.existsSync(%q))
fs.rmdirSync(%q)
console.log(fs.existsSync(%q))
`, sub, sub, sub)
	assertOutput(t, src, "1\n0")
}

func TestE2EFsRmdirSyncNonEmptyThrows(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "nonempty")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatalf("os.Mkdir(%q): %v", sub, err)
	}
	if err := os.WriteFile(filepath.Join(sub, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("os.WriteFile: %v", err)
	}
	src := fmt.Sprintf(`
try {
    fs.rmdirSync(%q)
    console.log("should not print")
} catch (e) {
    console.log("caught")
}
`, sub)
	assertOutput(t, src, "caught")
}

func TestE2EFsRmdirSyncWrongArgCountRejected(t *testing.T) {
	_, err := parseAndCompile(`fs.rmdirSync()`)
	if err == nil {
		t.Fatal("expected a compile error for fs.rmdirSync() with no arguments, got none")
	}
}

func TestE2EFsRenameSyncMovesFile(t *testing.T) {
	dir := t.TempDir()
	oldPath := filepath.Join(dir, "old.txt")
	newPath := filepath.Join(dir, "new.txt")
	src := fmt.Sprintf(`
fs.writeFileSync(%q, "content")
fs.renameSync(%q, %q)
console.log(fs.existsSync(%q))
console.log(fs.existsSync(%q))
console.log(fs.readFileSync(%q))
`, oldPath, oldPath, newPath, oldPath, newPath, newPath)
	assertOutput(t, src, "0\n1\ncontent")
}

func TestE2EFsRenameSyncNonexistentThrows(t *testing.T) {
	dir := t.TempDir()
	oldPath := filepath.Join(dir, "does-not-exist.txt")
	newPath := filepath.Join(dir, "new.txt")
	src := fmt.Sprintf(`
try {
    fs.renameSync(%q, %q)
    console.log("should not print")
} catch (e) {
    console.log("caught")
}
`, oldPath, newPath)
	assertOutput(t, src, "caught")
}

func TestE2EFsRenameSyncWrongArgCountRejected(t *testing.T) {
	_, err := parseAndCompile(`fs.renameSync("a")`)
	if err == nil {
		t.Fatal("expected a compile error for fs.renameSync with the wrong argument count, got none")
	}
}

func TestE2EFsCopyFileSyncCopiesContent(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	dest := filepath.Join(dir, "dest.txt")
	code := fmt.Sprintf(`
fs.writeFileSync(%q, "copy me")
fs.copyFileSync(%q, %q)
console.log(fs.existsSync(%q))
console.log(fs.readFileSync(%q))
console.log(fs.readFileSync(%q))
`, src, src, dest, src, src, dest)
	assertOutput(t, code, "1\ncopy me\ncopy me")
}

func TestE2EFsCopyFileSyncWrongArgCountRejected(t *testing.T) {
	_, err := parseAndCompile(`fs.copyFileSync("a")`)
	if err == nil {
		t.Fatal("expected a compile error for fs.copyFileSync with the wrong argument count, got none")
	}
}

func TestE2EFsReaddirSyncListsEntriesExcludingDotAndDotDot(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("os.WriteFile: %v", err)
		}
	}
	src := fmt.Sprintf(`
const entries: string[] = fs.readdirSync(%q)
console.log(entries.length)
entries.sort()
for (const e of entries) {
    console.log(e)
}
`, dir)
	assertOutput(t, src, "3\na.txt\nb.txt\nc.txt")
}

func TestE2EFsReaddirSyncEmptyDirectory(t *testing.T) {
	dir := t.TempDir()
	src := fmt.Sprintf(`
const entries: string[] = fs.readdirSync(%q)
console.log(entries.length)
`, dir)
	assertOutput(t, src, "0")
}

func TestE2EFsReaddirSyncNonexistentThrows(t *testing.T) {
	assertOutput(t, `
try {
    fs.readdirSync("/definitely/does/not/exist/kml-test-dir")
    console.log("should not print")
} catch (e) {
    console.log("caught")
}
`, "caught")
}

func TestE2EFsReaddirSyncWrongArgCountRejected(t *testing.T) {
	_, err := parseAndCompile(`fs.readdirSync()`)
	if err == nil {
		t.Fatal("expected a compile error for fs.readdirSync() with no arguments, got none")
	}
}

// --- Near-zero-effort roadmap batch: NaN/Infinity, performance.now,
// atob/btoa, encodeURI(Component)/decodeURI(Component),
// crypto.getRandomValues/randomUUID, process.readLineSync ---

func TestE2ENaNInfinityBareGlobals(t *testing.T) {
	assertOutput(t, `
console.log(isNaN(NaN))
console.log(isFinite(Infinity))
console.log(-Infinity < 0)
console.log(Infinity > 1000000)
const x = NaN
console.log(isNaN(x))
`, "1\n0\n1\n1\n1")
}

func TestE2EPerformanceNow(t *testing.T) {
	assertOutput(t, `
const t1: number = performance.now()
let arr: number[] = []
for (let i = 0; i < 200000; i++) { arr.push(i) }
const t2: number = performance.now()
console.log(arr.length)
console.log(t2 >= t1)
`, "200000\n1")
}

func TestE2EBtoaAtob(t *testing.T) {
	assertOutput(t, `
console.log(btoa("hello"))
console.log(btoa("hi"))
console.log(btoa("hey!"))
console.log(btoa(""))
console.log(atob("aGVsbG8="))
console.log(atob("aGk="))
console.log(atob("aGV5IQ=="))
console.log(atob(btoa("round trip 123!@#")))
`, "aGVsbG8=\naGk=\naGV5IQ==\n\nhello\nhi\nhey!\nround trip 123!@#")
}

func TestE2EBtoaAtobWrongArgCountRejected(t *testing.T) {
	_, err := parseAndCompile(`btoa("a", "b")`)
	if err == nil {
		t.Fatal("expected a compile error for btoa with the wrong argument count, got none")
	}
}

func TestE2EEncodeDecodeURIComponent(t *testing.T) {
	assertOutput(t, `
console.log(encodeURIComponent("hello world"))
console.log(encodeURIComponent("a=b&c=d"))
console.log(encodeURIComponent("path/to/thing?x=1"))
console.log(decodeURIComponent("hello%20world"))
console.log(decodeURIComponent("a%3Db%26c%3Dd"))
console.log(decodeURIComponent(encodeURIComponent("weird chars: <>{}[]")))
`, "hello%20world\na%3Db%26c%3Dd\npath%2Fto%2Fthing%3Fx%3D1\nhello world\na=b&c=d\nweird chars: <>{}[]")
}

func TestE2EEncodeDecodeURIPreservesReservedChars(t *testing.T) {
	assertOutput(t, `
console.log(encodeURI("http://example.com/path?a=1&b=2 space"))
console.log(decodeURI("http://example.com/path%3Fa=1&b=2%20space"))
console.log(decodeURI("path%2Ftest"))
`, "http://example.com/path?a=1&b=2%20space\nhttp://example.com/path%3Fa=1&b=2 space\npath%2Ftest")
}

func TestE2ECryptoGetRandomValues(t *testing.T) {
	assertOutput(t, `
let buf: number[] = new Array<number>(16)
crypto.getRandomValues(buf)
console.log(buf.length)
let allInRange = true
for (const b of buf) {
    if (b < 0 || b > 255) { allInRange = false }
}
console.log(allInRange)
`, "16\n1")
}

func TestE2ECryptoRandomUUID(t *testing.T) {
	assertOutput(t, `
const id1: string = crypto.randomUUID()
const id2: string = crypto.randomUUID()
console.log(id1.length)
console.log(id1 !== id2)
console.log(id1[8])
console.log(id1[13])
console.log(id1[18])
console.log(id1[23])
console.log(id1[14])
`, "36\n1\n-\n-\n-\n-\n4")
}

func TestE2EProcessReadLineSync(t *testing.T) {
	src := `
const line1 = process.readLineSync()
console.log("got: " + line1)
const line2 = process.readLineSync()
console.log("got: " + line2)
const line3 = process.readLineSync()
console.log(line3 === null)
`
	got := compileAndRunWithStdin(t, src, "hello\nworld\n")
	compareLines(t, got, "got: hello\ngot: world\n1")
}

func TestE2EProcessReadLineSyncNoTrailingNewline(t *testing.T) {
	src := `
const line1 = process.readLineSync()
console.log("got: " + line1)
const line2 = process.readLineSync()
console.log(line2 === null)
`
	got := compileAndRunWithStdin(t, src, "last line no newline")
	compareLines(t, got, "got: last line no newline\n1")
}

// --- process.execFileSync ---
//
// Spawns real child processes via fork+execvp — /bin/echo, /bin/sh, and
// PATH-resolved bare names are used since they're present on every POSIX
// system this compiler targets (macOS, Linux), unlike httpbin.org-style
// external-network tests which stay in examples/, not here.

func TestE2EExecFileSyncCapturesStdout(t *testing.T) {
	assertOutput(t, `
const args: string[] = ["hello", "world"]
const out: string = process.execFileSync("/bin/echo", args)
console.log(out)
`, "hello world\n")
}

func TestE2EExecFileSyncNoArgs(t *testing.T) {
	assertOutput(t, `
const out: string = process.execFileSync("/bin/echo")
console.log(out.length)
`, "1")
}

func TestE2EExecFileSyncResolvesViaPath(t *testing.T) {
	assertOutput(t, `
const args: string[] = ["via", "path"]
const out: string = process.execFileSync("echo", args)
console.log(out)
`, "via path\n")
}

func TestE2EExecFileSyncDoesNotInvokeAShell(t *testing.T) {
	// Real execFileSync semantics: argv is passed straight to execvp, no
	// shell involved — shell metacharacters must come back out verbatim,
	// not get expanded/interpreted.
	assertOutput(t, `
const args: string[] = ["$(echo pwned); ls"]
const out: string = process.execFileSync("/bin/echo", args)
console.log(out)
`, "$(echo pwned); ls\n")
}

func TestE2EExecFileSyncNonZeroExitThrows(t *testing.T) {
	assertOutput(t, `
try {
    process.execFileSync("/usr/bin/false")
    console.log("should not print")
} catch (e) {
    console.log(e.message)
}
`, "Command failed with exit code 1: /usr/bin/false")
}

func TestE2EExecFileSyncSignalDeathThrows(t *testing.T) {
	assertOutput(t, `
const args: string[] = ["-c", "kill -9 $$"]
try {
    process.execFileSync("/bin/sh", args)
    console.log("should not print")
} catch (e) {
    console.log(e.message)
}
`, "Command was terminated by signal 9: /bin/sh")
}

func TestE2EExecFileSyncMissingBinaryThrows(t *testing.T) {
	assertOutput(t, `
try {
    process.execFileSync("/no/such/binary/at/all")
    console.log("should not print")
} catch (e) {
    console.log(e.message)
}
`, "Command failed with exit code 127: /no/such/binary/at/all")
}

func TestE2EExecFileSyncLargeOutputGrowsBuffer(t *testing.T) {
	// Forces output past a single pipe read (and the growable buffer's
	// initial capacity), exercising the realloc-doubling path.
	assertOutput(t, `
const args: string[] = ["-c", "for i in $(seq 1 5000); do printf '0123456789'; done"]
const out: string = process.execFileSync("/bin/sh", args)
console.log(out.length)
`, "50000")
}

func TestE2EExecFileSyncWrongArgCountRejected(t *testing.T) {
	_, err := parseAndCompile(`process.execFileSync()`)
	if err == nil {
		t.Fatal("expected a compile error for process.execFileSync() with no arguments, got none")
	}
}

func TestE2EExecFileSyncNonStringArrayArgsRejected(t *testing.T) {
	_, err := parseAndCompile(`
const args: number[] = [1, 2, 3]
process.execFileSync("/bin/echo", args)
`)
	if err == nil {
		t.Fatal("expected a compile error for process.execFileSync with a non-string[] args argument, got none")
	}
}

// --- process.cwd/chdir/pid/platform/kill ---

func TestE2EProcessCwdAndChdir(t *testing.T) {
	dir := t.TempDir()
	// Resolve symlinks the same way the OS's own getcwd() would (macOS's
	// /tmp is itself a symlink to /private/tmp) so the comparison is exact,
	// not just "close enough".
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", dir, err)
	}
	src := fmt.Sprintf(`
process.chdir(%q)
console.log(process.cwd())
`, resolved)
	assertOutput(t, src, resolved)
}

func TestE2EProcessChdirNonexistentThrows(t *testing.T) {
	assertOutput(t, `
try {
    process.chdir("/definitely/does/not/exist/kml-test-dir")
    console.log("should not print")
} catch (e) {
    console.log(e.message.startsWith("cannot change directory to '/definitely/does/not/exist/kml-test-dir': "))
}
`, "1")
}

func TestE2EProcessChdirWrongArgCountRejected(t *testing.T) {
	_, err := parseAndCompile(`process.chdir()`)
	if err == nil {
		t.Fatal("expected a compile error for process.chdir() with no arguments, got none")
	}
}

func TestE2EProcessPidIsPositive(t *testing.T) {
	assertOutput(t, `console.log(process.pid > 0)`, "1")
}

func TestE2EProcessPlatform(t *testing.T) {
	want := runtime.GOOS
	if want == "windows" {
		want = "win32"
	}
	assertOutput(t, `console.log(process.platform)`, want)
}

func TestE2EProcessKillSignalZeroOnSelfSucceeds(t *testing.T) {
	// Signal 0 is the POSIX "existence check" convention: no signal is
	// actually delivered, kill() just reports whether it could have been.
	assertOutput(t, `
process.kill(process.pid, 0)
console.log("no throw")
`, "no throw")
}

func TestE2EProcessKillDefaultsToSigterm(t *testing.T) {
	_, err := parseAndCompile(`process.kill(1)`)
	if err != nil {
		t.Fatalf("expected process.kill with a single argument (implicit SIGTERM) to compile, got: %v", err)
	}
}

func TestE2EProcessKillNonexistentPidThrows(t *testing.T) {
	assertOutput(t, `
try {
    process.kill(999999999, 0)
    console.log("should not print")
} catch (e) {
    console.log(e.message.startsWith("kill(pid=999999999, signal=0): "))
}
`, "1")
}

func TestE2EProcessKillWrongArgCountRejected(t *testing.T) {
	_, err := parseAndCompile(`process.kill()`)
	if err == nil {
		t.Fatal("expected a compile error for process.kill() with no arguments, got none")
	}
}

// --- Memory.free(x) ---
//
// Stage 1 of the staged manual-memory-management plan in STATUS.md: a raw,
// unsafe, shallow free. String literals are interned as compile-time global
// constants, not malloc'd, so every string test here uses a concatenation
// result (guaranteed heap-allocated) rather than a bare literal.

func TestE2EMemoryFreeString(t *testing.T) {
	assertOutput(t, `
let s: string = "hello " + "world"
console.log(s.length)
Memory.free(s)
console.log(s === null)
`, "11\n1")
}

func TestE2EMemoryFreeArray(t *testing.T) {
	assertOutput(t, `
let arr: number[] = [1, 2, 3, 4, 5]
console.log(arr.length)
Memory.free(arr)
console.log(arr.length)
`, "5\n0")
}

func TestE2EMemoryFreeObject(t *testing.T) {
	assertOutput(t, `
interface Point { x: number; y: number }
let p: Point = { x: 1, y: 2 }
console.log(p.x)
Memory.free(p)
console.log("done")
`, "1\ndone")
}

func TestE2EMemoryFreeClosureNoCaptures(t *testing.T) {
	assertOutput(t, `
let f: () => number = () => 42
console.log(f())
Memory.free(f)
console.log("done")
`, "42\ndone")
}

func TestE2EMemoryFreeClosureLeavesSharedCaptureIntact(t *testing.T) {
	// The closure's own header+env are freed, but the captured variable's
	// heap-promoted cell (shared with the enclosing scope, ADR-00001) must
	// survive — freeing it would be a real use-after-free for the
	// enclosing scope's own continued use of the variable, not just the
	// user's own responsibility.
	assertOutput(t, `
let counter: number = 0
const inc = (): number => { counter = counter + 1; return counter }
console.log(inc())
console.log(inc())
Memory.free(inc)
console.log(counter)
`, "1\n2\n2")
}

func TestE2EMemoryFreeMap(t *testing.T) {
	assertOutput(t, `
let m: Map<string, number> = new Map<string, number>()
m.set("a", 1)
m.set("b", 2)
console.log(m.size)
Memory.free(m)
console.log("done")
`, "2\ndone")
}

func TestE2EMemoryFreeSet(t *testing.T) {
	assertOutput(t, `
let st: Set<string> = new Set<string>()
st.add("x")
console.log(st.size)
Memory.free(st)
console.log("done")
`, "1\ndone")
}

func TestE2EMemoryFreeGeneralExpression(t *testing.T) {
	// Not just named variables — a directly-evaluated expression works too.
	assertOutput(t, `
interface Point { x: number; y: number }
function makePoint(): Point {
    return { x: 1, y: 2 }
}
Memory.free(makePoint())
console.log("done")
`, "done")
}

func TestE2EMemoryFreeDoubleFreeViaSameVariableIsSafe(t *testing.T) {
	// free(NULL) is a well-defined no-op in C — since the first free nulls
	// out the variable's own storage, a second Memory.free() call on that
	// same variable is harmless. (Freeing the same underlying allocation
	// through two different aliases is still unsafe, by design — this only
	// covers the single-named-variable case.)
	assertOutput(t, `
let arr: number[] = [1, 2, 3]
Memory.free(arr)
Memory.free(arr)
console.log("no crash")
`, "no crash")
}

func TestE2EMemoryFreeUnsupportedTypeRejected(t *testing.T) {
	_, err := parseAndCompile(`
let n: number = 5
Memory.free(n)
`)
	if err == nil {
		t.Fatal("expected a compile error for Memory.free on a number, got none")
	}
}

func TestE2EMemoryFreeWrongArgCountRejected(t *testing.T) {
	_, err := parseAndCompile(`Memory.free()`)
	if err == nil {
		t.Fatal("expected a compile error for Memory.free() with no arguments, got none")
	}
}

// --- setTimeout/clearTimeout/setInterval/clearInterval ---
//
// Real wall-clock delays, kept small (a handful of ms) so the suite stays
// fast. Assertions are on order/behavior, never on exact timing (matching
// the same convention console.timeEnd's own tests already use).

func TestE2ESetTimeoutFires(t *testing.T) {
	assertOutput(t, `
console.log("sync")
setTimeout(() => {
    console.log("fired")
}, 5)
`, "sync\nfired")
}

func TestE2ESetTimeoutOrdersByDelayNotRegistration(t *testing.T) {
	assertOutput(t, `
setTimeout(() => { console.log("C") }, 30)
setTimeout(() => { console.log("A") }, 5)
setTimeout(() => { console.log("B") }, 15)
console.log("sync")
`, "sync\nA\nB\nC")
}

func TestE2EClearTimeoutCancelsBeforeFiring(t *testing.T) {
	assertOutput(t, `
const id = setTimeout(() => {
    console.log("should not print")
}, 20)
clearTimeout(id)
console.log("cancelled")
`, "cancelled")
}

func TestE2ESetIntervalRepeatsAndSelfCancels(t *testing.T) {
	// Regression test for a real bug found while writing this test: the
	// idiomatic self-cancelling-interval pattern (the interval's own
	// callback reads the `id` its own declaration is in the middle of
	// producing) silently never cancelled anything, because emitVarDecl
	// stored the real setInterval() return value into the variable's
	// pre-promotion alloca — but the callback's capture had already been
	// boxed to a *different*, freshly-malloc'd cell (ADR-00001) by the time
	// that store happened, so the closure only ever saw the cell's stale,
	// pre-initialization value. Fixed by re-resolving the variable's
	// current storage location (via a fresh lookup) right before the final
	// store, instead of trusting the pointer captured before the
	// initializer ran.
	assertOutput(t, `
let count: number = 0
const id = setInterval(() => {
    count = count + 1
    console.log("tick " + count)
    if (count >= 3) {
        clearInterval(id)
    }
}, 5)
`, "tick 1\ntick 2\ntick 3")
}

func TestE2EProcessExitSkipsPendingTimers(t *testing.T) {
	assertOutput(t, `
setTimeout(() => {
    console.log("should not print")
}, 10)
console.log("before exit")
process.exit(0)
`, "before exit")
}

func TestE2ESetTimeoutUncaughtThrowPropagates(t *testing.T) {
	_, code := compileAndRunExpectExit(t, `
setTimeout(() => {
    throw new Error("boom from timer")
}, 5)
console.log("sync done")
`)
	if code == 0 {
		t.Fatal("expected a non-zero exit code for an uncaught throw from a timer callback, got 0")
	}
}

func TestE2ESetTimeoutWrongArgCountRejected(t *testing.T) {
	_, err := parseAndCompile(`setTimeout()`)
	if err == nil {
		t.Fatal("expected a compile error for setTimeout() with no arguments, got none")
	}
}

func TestE2ESetTimeoutNonFunctionCallbackRejected(t *testing.T) {
	_, err := parseAndCompile(`setTimeout(5, 10)`)
	if err == nil {
		t.Fatal("expected a compile error for setTimeout with a non-function first argument, got none")
	}
}

func TestE2EClearIntervalWrongArgCountRejected(t *testing.T) {
	_, err := parseAndCompile(`clearInterval()`)
	if err == nil {
		t.Fatal("expected a compile error for clearInterval() with no arguments, got none")
	}
}
