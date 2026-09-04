package tests

// memory_auto_test.go — TDD-00173: `-mm=auto`, `/** @free */`, `/** @owned */`.
//
// The annotations are honored in every memory mode (they are compiler-checked,
// compiler-placed Memory.free), so the behavioral tests run in default manual
// mode; auto-mode-specific tests cover the Memory.free ban and the implicit
// zero-annotation layer, with an RSS-bounded churn run as the proof that
// frees actually happen (mirroring TestE2EGCModeBoundsMemory).

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"KlainMainLang/codegen/llvm"
	"KlainMainLang/parser"
	"KlainMainLang/resolver"
)

// emitAutoImports compiles src (through the resolver, so imports work) under
// -mm=auto and returns the IR or the codegen error.
func emitAutoImports(t *testing.T, src string) (string, error) {
	t.Helper()
	dir := t.TempDir()
	srcFile := filepath.Join(dir, "main.ts")
	if err := os.WriteFile(srcFile, []byte(src), 0644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	prog, err := resolver.ResolveProgram(srcFile)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	em := llvm.NewEmitter()
	em.SetMemMode("auto")
	return em.EmitProgram(prog)
}

// buildBinaryAuto is buildBinary with -mm=auto, sharing the same backend-C
// append chain so the two clang invocations can't drift.
func buildBinaryAuto(t *testing.T, src string) string {
	t.Helper()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not found in PATH")
	}
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	em := llvm.NewEmitter()
	em.SetMemMode("auto")
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

// --- @free: block-exit free, honored in the default (manual) mode ---

func TestE2EMemoryAutoFreeBlockExit(t *testing.T) {
	assertOutput(t, `
function work(): number {
  /** @free */ let arr: number[] = [10, 20, 30]
  return arr.length
}
console.log(work())
console.log(work())
`, "3\n3")
}

func TestE2EMemoryAutoFreeLoopChurn(t *testing.T) {
	// Declared inside the loop body → freed every iteration, including the
	// continue path.
	assertOutput(t, `
let total: number = 0
for (let i: number = 0; i < 100; i++) {
  /** @free */ let chunk: number[] = [i, i + 1, i + 2]
  if (i % 2 === 0) { continue }
  total = total + chunk[0]
}
console.log(total)
`, "2500")
}

func TestE2EMemoryAutoFreeBreakPath(t *testing.T) {
	assertOutput(t, `
let found: number = -1
for (let i: number = 0; i < 10; i++) {
  /** @free */ let s: string = "item-" + i
  if (s.length > 5 && i > 3) { found = i; break }
}
console.log(found)
`, "4")
}

func TestE2EMemoryAutoFreeFinallyOrder(t *testing.T) {
	// The finally body still reads the value; the free runs strictly after.
	assertOutput(t, `
function f(): number {
  /** @free */ let data: number[] = [7, 8, 9]
  try {
    return data.length
  } finally {
    console.log("finally sees", data[0])
  }
}
console.log(f())
`, "finally sees 7\n3")
}

// --- @free rejections ---

func TestE2EMemoryAutoFreeRejectsReturn(t *testing.T) {
	prog, err := parser.Parse(`
function bad(): number[] {
  /** @free */ let arr: number[] = [1]
  return arr
}
`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, err = llvm.NewEmitter().EmitProgram(prog)
	if err == nil || !strings.Contains(err.Error(), "returned") {
		t.Fatalf("want returned-escape error, got %v", err)
	}
}

func TestE2EMemoryAutoFreeRejectsClosureCapture(t *testing.T) {
	prog, err := parser.Parse(`
/** @free */ let arr: number[] = [1, 2]
const f = () => arr.length
console.log(f())
`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, err = llvm.NewEmitter().EmitProgram(prog)
	if err == nil || !strings.Contains(err.Error(), "captured") {
		t.Fatalf("want capture error, got %v", err)
	}
}

func TestE2EMemoryAutoFreeRejectsManualFreeCombo(t *testing.T) {
	_, err := parseAndCompileImports(t, `
import Memory from 'memory'
/** @free */ let arr: number[] = [1]
Memory.free(arr)
`)
	if err == nil || !strings.Contains(err.Error(), "Memory.free") {
		t.Fatalf("want Memory.free-combo error, got %v", err)
	}
}

func TestE2EMemoryAutoFreeRejectsMultiDeclarator(t *testing.T) {
	_, err := parser.Parse(`/** @free */ let a: number[] = [1], b: number[] = [2]`)
	if err == nil || !strings.Contains(err.Error(), "single-variable") {
		t.Fatalf("want multi-declarator parse error, got %v", err)
	}
}

func TestE2EMemoryAutoFreeRejectsStringLiteralInit(t *testing.T) {
	prog, err := parser.Parse(`
/** @free */ let s: string = "hi"
console.log(s)
`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, err = llvm.NewEmitter().EmitProgram(prog)
	if err == nil || !strings.Contains(err.Error(), "initializer") {
		t.Fatalf("want non-owning-initializer error, got %v", err)
	}
}

// --- @owned: last-use free (Stage 3) ---

func TestE2EMemoryAutoOwnedLocalLastUse(t *testing.T) {
	assertOutput(t, `
/** @owned */ let big: string = "x" + "y"
console.log(big.length)
console.log("after")
`, "2\nafter")
}

func TestE2EMemoryAutoOwnedParamPipeline(t *testing.T) {
	assertOutput(t, `
/** @owned input */
function transform(input: number[]): number {
  const doubled: number = input[0] * 2
  console.log("working")
  return doubled
}
console.log(transform([21]))
`, "working\n42")
}

func TestE2EMemoryAutoOwnedParamRejectsLiveArg(t *testing.T) {
	prog, err := parser.Parse(`
/** @owned input */
function consume(input: number[]): number { return input.length }
let data: number[] = [1, 2]
console.log(consume(data))
console.log(data.length)
`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, err = llvm.NewEmitter().EmitProgram(prog)
	if err == nil || !strings.Contains(err.Error(), "used again after this call") {
		t.Fatalf("want live-arg error, got %v", err)
	}
}

func TestE2EMemoryAutoOwnedFnAsValueRejected(t *testing.T) {
	prog, err := parser.Parse(`
/** @owned input */
function consume(input: number[]): number { return input.length }
const f = consume
`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, err = llvm.NewEmitter().EmitProgram(prog)
	if err == nil || !strings.Contains(err.Error(), "function reference") {
		t.Fatalf("want fn-as-value error, got %v", err)
	}
}

func TestE2EMemoryAutoOwnedRejectsUnknownParamName(t *testing.T) {
	_, err := parser.Parse(`
/** @owned nosuch */
function f(input: number[]): number { return input.length }
`)
	if err == nil || !strings.Contains(err.Error(), "unknown parameter") {
		t.Fatalf("want unknown-param parse error, got %v", err)
	}
}

// --- -mm=auto: Memory.free ban + implicit layer ---

func TestE2EMemoryAutoModeBansMemoryFree(t *testing.T) {
	_, err := emitAutoImports(t, `
import Memory from 'memory'
let a: number[] = [1]
Memory.free(a)
`)
	if err == nil || !strings.Contains(err.Error(), "not allowed under -mm=auto") {
		t.Fatalf("want Memory.free ban, got %v", err)
	}
}

func TestE2EMemoryAutoModeImplicitParity(t *testing.T) {
	// Same source, zero annotations: auto mode must produce identical output
	// to manual mode — inserted frees are never observable.
	src := `
let total: number = 0
for (let i: number = 0; i < 50; i++) {
  let chunk: number[] = [i, i * 2]
  let s: string = "n=" + chunk[1]
  total = total + s.length
}
console.log(total)
let keep: number[] = [1, 2, 3]
console.log(keep.length)
`
	runOne := func(bin string) string {
		out, err := exec.Command(bin).Output()
		if err != nil {
			t.Fatalf("run %s: %v", bin, err)
		}
		return strings.TrimRight(string(out), "\n")
	}
	want := runOne(buildBinary(t, src))
	got := runOne(buildBinaryAuto(t, src))
	if got != want {
		t.Errorf("auto-mode output %q differs from manual %q", got, want)
	}
}

// autoChurnProgram allocates ~150MB across iterations with zero annotations;
// the implicit layer must keep RSS bounded. Elements are read back so -O2
// cannot elide the allocations.
const autoChurnProgram = `
let total: number = 0
for (let i: number = 0; i < 300000; i++) {
  let chunk: number[] = [i, i + 1, i + 2, i + 3, i + 4, i + 5, i + 6, i + 7,
    i, i, i, i, i, i, i, i, i, i, i, i, i, i, i, i, i, i, i, i, i, i, i, i,
    i, i, i, i, i, i, i, i, i, i, i, i, i, i, i, i, i, i, i, i, i, i, i, i,
    i, i, i, i, i, i, i, i]
  total = total + chunk[i % 64]
}
console.log(total)
`

func TestE2EMemoryAutoModeBoundsMemory(t *testing.T) {
	binFile := buildBinaryAuto(t, autoChurnProgram)

	if _, err := exec.LookPath("/usr/bin/time"); err != nil {
		t.Skip("/usr/bin/time not found")
	}
	var args []string
	switch runtime.GOOS {
	case "darwin":
		args = []string{"-l", binFile}
	case "linux":
		args = []string{"-v", binFile}
	default:
		t.Skipf("no known /usr/bin/time RSS format for %s", runtime.GOOS)
	}
	out, err := exec.Command("/usr/bin/time", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("run under /usr/bin/time: %v\n%s", err, out)
	}
	peakBytes, ok := parsePeakRSS(string(out))
	if !ok {
		t.Skipf("could not parse peak RSS from /usr/bin/time output:\n%s", out)
	}
	// ~160MB churned; without the implicit frees peak RSS exceeds that.
	const boundBytes = 30_000_000
	if peakBytes > boundBytes {
		t.Errorf("peak RSS %d bytes exceeds %d bound — implicit frees may not be happening", peakBytes, boundBytes)
	}
}
