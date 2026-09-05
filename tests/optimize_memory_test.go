package tests

// -optimize-memory E2E tests (TDD-00134 Stage 1): escape analysis → stack
// allocation of non-escaping object literals. IR-level assertions prove the
// allocation decision (alloca vs calloc); behavioral runs prove equivalence,
// including the per-iteration re-zeroing that preserves calloc's
// absent-optional-field guarantee.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"KlainMainLang/codegen/llvm"
	"KlainMainLang/parser"
)

// emitIROptimizeMemory compiles src to IR with -optimize-memory set.
func emitIROptimizeMemory(t *testing.T, src string) string {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	em := llvm.NewEmitter()
	em.SetOptimizeMemory(true)
	ir, err := em.EmitProgram(prog)
	if err != nil {
		t.Fatalf("codegen: %v", err)
	}
	return ir
}

func TestOptimizeMemoryStackAllocatesNonEscaping(t *testing.T) {
	ir := emitIROptimizeMemory(t, `
interface P { x: number; y: number }
function f(i: number): number {
  const p: P = { x: i, y: i + 1 }
  return p.x + p.y
}
console.log(f(1))
`)
	if strings.Contains(ir, "@calloc(i64 1, i64 16)") {
		t.Errorf("non-escaping literal still calloc'd:\n%s", ir)
	}
	if !strings.Contains(ir, "alloca { double, double }") {
		t.Errorf("expected a stack alloca for the literal")
	}
}

func TestOptimizeMemoryEscapingStaysHeap(t *testing.T) {
	// Returned and stored literals escape — they must keep the heap path.
	ir := emitIROptimizeMemory(t, `
interface P { x: number; y: number }
function make(i: number): P {
  const p: P = { x: i, y: i }
  return p
}
const keep: P[] = []
function stash(i: number): void {
  const q: P = { x: i, y: i }
  keep.push(q)
}
console.log(make(1).x)
stash(2)
console.log(keep.length)
`)
	if c := strings.Count(ir, "call ptr @calloc(i64 1, i64 16)"); c != 2 {
		t.Errorf("expected both escaping literals on the heap (2 callocs), got %d", c)
	}
}

// buildBinaryOptimizeMemory mirrors buildBinary with -optimize-memory (and
// optionally -mm) set.
func buildBinaryOptimizeMemory(t *testing.T, src, mm string) string {
	t.Helper()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not found in PATH")
	}
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	em := llvm.NewEmitter()
	em.SetOptimizeMemory(true)
	if mm != "" {
		em.SetMemMode(mm)
	}
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
	clangArgs = appendDtoa(t, em, dir, clangArgs)
	out, err := exec.Command("clang", clangArgs...).CombinedOutput()
	if err != nil {
		t.Fatalf("clang: %v\n%s", err, out)
	}
	return binFile
}

func TestE2EOptimizeMemoryBehavesIdentically(t *testing.T) {
	const src = `
interface P { x: number; y: number }
interface Q { a: number; b?: number }
function dist2(ax: number, ay: number): number {
  const p: P = { x: ax, y: ay }
  return p.x * p.x + p.y * p.y
}
function opt(i: number): void {
  if (i === 1) {
    const q: Q = { a: i, b: 99 }
    console.log(q.a, q.b)
  } else {
    const q: Q = { a: i }
    console.log(q.a, q.b)
  }
}
let total = 0
for (let i = 0; i < 5; i++) {
  const r: P = { x: i, y: i + 1 }
  total += r.x + r.y + dist2(i, i)
}
console.log(total)
for (let i = 0; i < 3; i++) { opt(i) }
`
	const want = "85\n0 0\n1 99\n2 0"
	binFile := buildBinaryOptimizeMemory(t, src, "")
	out, err := exec.Command(binFile).Output()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	compareLines(t, strings.TrimRight(string(out), "\n"), want)
	// And identical without the flag.
	assertOutput(t, src, want)
}

func TestE2EOptimizeMemoryAutoModeCombo(t *testing.T) {
	// -mm=auto + -optimize-memory: a registered FinalizationRegistry target
	// stays heap (the stack planner has no register exemption) and its
	// auto-free at scope exit fires the cleanup; the unregistered local is
	// free to stack-allocate. The callback drains before the timer tick.
	const src = `
interface Res { id: number }
const reg = new FinalizationRegistry((held: string) => { console.log("freed:", held) })
function work(): void {
  const tracked: Res = { id: 1 }
  const local: Res = { id: 2 }
  reg.register(tracked, "tracked")
  console.log("sum", tracked.id + local.id)
}
work()
setTimeout(() => { console.log("tick") }, 0)
console.log("end")
`
	binFile := buildBinaryOptimizeMemory(t, src, "auto")
	out, err := exec.Command(binFile).Output()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	compareLines(t, strings.TrimRight(string(out), "\n"), "sum 3\nend\nfreed: tracked\ntick")
}

func TestOptimizeMemoryClosureStackAllocated(t *testing.T) {
	// A non-escaping closure's {fn,env} header and env struct become
	// entry-block allocas; the shared capture cells stay heap (malloc(8)).
	ir := emitIROptimizeMemory(t, `
function calc(base: number): number {
  let acc = 0
  const add = (x: number): void => { acc += x + base }
  add(1)
  add(2)
  const twice = function (x: number): number { return x * 2 }
  return acc + twice(base)
}
console.log(calc(10))
`)
	if strings.Contains(ir, "malloc(i64 16)") {
		t.Errorf("non-escaping closure header/env still malloc'd")
	}
	if c := strings.Count(ir, "alloca {ptr, ptr}"); c != 2 {
		t.Errorf("expected 2 header allocas, got %d", c)
	}
	if c := strings.Count(ir, "call ptr @malloc(i64 8)"); c != 2 {
		t.Errorf("expected the 2 capture cells to stay heap, got %d malloc(8)", c)
	}
}

func TestOptimizeMemoryEscapingClosureStaysHeap(t *testing.T) {
	ir := emitIROptimizeMemory(t, `
function makeAdder(n: number): (x: number) => number {
  const f = (x: number): number => x + n
  return f
}
const cbs: (() => void)[] = []
function stash(): void {
  const h = (): void => { console.log("hi") }
  cbs.push(h)
}
console.log(makeAdder(5)(2))
stash()
`)
	if strings.Contains(ir, "alloca {ptr, ptr}") {
		t.Errorf("escaping closure was stack-allocated")
	}
}

func TestE2EOptimizeMemoryClosureBehavesIdentically(t *testing.T) {
	const src = `
function calc(base: number): number {
  let acc = 0
  const add = (x: number): void => { acc += x + base }
  add(1)
  add(2)
  const twice = function (x: number): number { return x * 2 }
  return acc + twice(base)
}
console.log(calc(10))
console.log(calc(0))
for (let i = 0; i < 3; i++) {
  const f = (n: number): number => n + i
  console.log(f(10))
}
`
	const want = "43\n3\n10\n11\n12"
	binFile := buildBinaryOptimizeMemory(t, src, "")
	out, err := exec.Command(binFile).Output()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	compareLines(t, strings.TrimRight(string(out), "\n"), want)
	assertOutput(t, src, want)
}

func TestOptimizeMemoryTupleAndClassStackAllocated(t *testing.T) {
	// A tuple-annotated literal and an instance of a this-clean class both
	// lose their callocs; method calls, vtable dispatch, and instanceof all
	// keep working on the stack instance (behavioral test below).
	ir := emitIROptimizeMemory(t, `
class Point {
  x: number
  y: number
  constructor(x: number, y: number) { this.x = x; this.y = y }
  dist2(): number { return this.x * this.x + this.y * this.y }
  scale(f: number): void { this.x *= f; this.y *= f }
}
function usePoint(i: number): number {
  const t: [number, number] = [i, i + 1]
  const p = new Point(t[0], t[1])
  p.scale(2)
  return p.dist2()
}
console.log(usePoint(1))
`)
	if strings.Contains(ir, "call ptr @calloc") {
		t.Errorf("tuple/class instance still calloc'd")
	}
}

func TestOptimizeMemoryThisLeakingClassStaysHeap(t *testing.T) {
	// Every way `this` can leave the instance keeps the class heap-bound:
	// ctor self-registration, a method returning this (chaining), and a
	// nested arrow capturing this (even for a bare field read).
	ir := emitIROptimizeMemory(t, `
class SelfReg {
  id: number
  constructor(id: number) { this.id = id; registry.push(this) }
}
const registry: SelfReg[] = []
class Chainy {
  v = 0
  add(n: number): Chainy { this.v += n; return this }
}
class Lambda {
  n = 1
  make(): () => number { return () => this.n }
}
function f(): number {
  const a = new SelfReg(1)
  const b = new Chainy()
  b.add(2)
  const c = new Lambda()
  return a.id + b.v + c.n
}
console.log(f())
`)
	if c := strings.Count(ir, "call ptr @calloc"); c != 3 {
		t.Errorf("expected all 3 this-leaking instances on the heap, got %d callocs", c)
	}
}

func TestE2EOptimizeMemoryClassBehavesIdentically(t *testing.T) {
	const src = `
class Animal {
  name: string
  constructor(name: string) { this.name = name }
  speak(): string { return this.name + " makes a sound" }
}
class Dog extends Animal {
  constructor(name: string) { super(name) }
  speak(): string { return this.name + " barks" }
}
const pen: Animal[] = []
class Escapee extends Animal {
  constructor(name: string) { super(name); pen.push(this) }
}
function run(): void {
  const d = new Dog("rex")
  console.log(d.speak())
  console.log(d instanceof Animal)
  const e2 = new Escapee("bob")
  console.log(e2.name)
  const t: [number, string] = [7, "seven"]
  console.log(t[0], t[1])
}
run()
console.log(pen.length)
`
	const want = "rex barks\ntrue\nbob\n7 seven\n1"
	binFile := buildBinaryOptimizeMemory(t, src, "")
	out, err := exec.Command(binFile).Output()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	compareLines(t, strings.TrimRight(string(out), "\n"), want)
	assertOutput(t, src, want)
}
