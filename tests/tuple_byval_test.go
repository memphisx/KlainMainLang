package tests

// Value-returned small tuples (TDD-00134 Stage 3, -optimize-memory): an
// eligible top-level function returns its ≤2-scalar-field tuple as a struct
// aggregate in registers; `return [a, b]` and `const [v, err] = f()` are
// allocation-free. IR assertions prove the ABI decision (aggregate vs ptr
// signature, insertvalue/extractvalue vs calloc); behavioral runs prove
// output equivalence against the default pointer ABI.

import (
	"os/exec"
	"strings"
	"testing"

	"KlainMainLang/codegen/llvm"
	"KlainMainLang/parser"
)

const tupleByValSrc = `
function divmod(a: number, b: number): [number, number] {
  return [Math.floor(a / b), a % b]
}
function named(): [number, string] {
  const t: [number, string] = [7, "seven"]
  return t
}
function chain(n: number): [number, number] {
  if (n <= 0) { return [0, 0] }
  const [a, b] = chain(n - 1)
  return [a + n, b + n * 2]
}
function forward(): [number, number] {
  return chain(3)
}
function run(): void {
  const [q, r] = divmod(17, 5)
  console.log(q, r)
  const [n, s] = named()
  console.log(n, s)
  const t = divmod(9, 2)
  console.log(t[0], t[1])
  const [, only] = named()
  console.log(only)
  const [x, y] = forward()
  console.log(x, y)
}
run()
`

const tupleByValWant = "3 2\n7 seven\n4 1\nseven\n6 12"

func TestE2ETupleByValBehavesIdentically(t *testing.T) {
	binFile := buildBinaryOptimizeMemory(t, tupleByValSrc, "")
	out, err := exec.Command(binFile).Output()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	compareLines(t, strings.TrimRight(string(out), "\n"), tupleByValWant)
	// And identical without the flag (pointer ABI).
	assertOutput(t, tupleByValSrc, tupleByValWant)
}

func TestTupleByValIRShape(t *testing.T) {
	ir := emitIROptimizeMemory(t, tupleByValSrc)
	// Eligible functions return the aggregate...
	for _, want := range []string{
		"define { double, double } @divmod",
		"define { double, ptr } @named",
		"define { double, double } @chain",
	} {
		if !strings.Contains(ir, want) {
			t.Errorf("missing %q in IR", want)
		}
	}
	// ...the literal return in divmod is insertvalue, not an allocation, and
	// destructuring uses extractvalue.
	if !strings.Contains(ir, "insertvalue { double, double }") {
		t.Errorf("expected insertvalue aggregate build for the literal return")
	}
	if !strings.Contains(ir, "extractvalue { double, double }") {
		t.Errorf("expected extractvalue destructuring at the call site")
	}
}

func TestTupleByValExclusions(t *testing.T) {
	ir := emitIROptimizeMemory(t, `
function three(): [number, number, number] {
  return [1, 2, 3]
}
function pair(n: number): [number, number] {
  return [n, n * 2]
}
function run(): void {
  // pair referenced as a value: pointer ABI must be kept program-wide.
  const alias: (n: number) => [number, number] = pair
  const [a, b, c] = three()
  const [p, q] = alias(2)
  const [x, y] = pair(1)
  console.log(a + b + c + p + q + x + y)
}
run()
`)
	if !strings.Contains(ir, "define ptr @three") {
		t.Errorf("a 3-field tuple must keep the pointer ABI")
	}
	if !strings.Contains(ir, "define ptr @pair") {
		t.Errorf("a function used as a value must keep the pointer ABI")
	}
}

func TestTupleByValDefaultModeUnchanged(t *testing.T) {
	// Without -optimize-memory the flag never plans: pointer ABI everywhere.
	prog, err := parser.Parse(tupleByValSrc)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	ir, err := llvm.NewEmitter().EmitProgram(prog)
	if err != nil {
		t.Fatalf("codegen: %v", err)
	}
	if strings.Contains(ir, "define { double, double }") {
		t.Errorf("by-value tuple ABI leaked into default (no-flag) mode")
	}
}

func TestE2EAsyncTupleReturn(t *testing.T) {
	// The pre-existing bug fixed alongside Stage 3: an async function
	// returning Promise<[T, U]> emitted its tuple-literal return as an ARRAY
	// literal — invalid IR in every mode. The promise's value type is now the
	// emission hint.
	const src = `
async function apair(): Promise<[number, string]> {
  return [4, "four"]
}
async function run(): Promise<void> {
  const [n, s] = await apair()
  console.log(n, s)
}
run()
`
	assertOutput(t, src, "4 four")
}
