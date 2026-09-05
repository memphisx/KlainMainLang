package tests

// FinalizationRegistry E2E tests (TDD-00163): manual-mode deterministic
// firing at Memory.free + the atexit survivor flush, unregister, tokens,
// compat=js dynamic targets, -finalizers=report leak lines, gc-mode real
// collection-triggered firing, and the compile-time rejections.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"KlainMainLang/codegen/llvm"
	"KlainMainLang/resolver"
)

func TestE2EFinRegManualFreeFires(t *testing.T) {
	assertOutputImports(t, `
import Memory from 'memory'
interface Res { id: number }
const reg = new FinalizationRegistry((held: string) => {
  console.log("cleanup:", held)
})
let a: Res = { id: 1 }
let b: Res = { id: 2 }
reg.register(a, "resource-a")
reg.register(b, "resource-b")
Memory.free(a)
console.log("end of script")
`, "end of script\ncleanup: resource-a\ncleanup: resource-b")
}

func TestE2EFinRegUnregisterToken(t *testing.T) {
	assertOutputImports(t, `
import Memory from 'memory'
interface Res { id: number }
const reg = new FinalizationRegistry((held: string) => { console.log("cleanup:", held) })
let a: Res = { id: 1 }
let b: Res = { id: 2 }
const tok: Res = { id: 99 }
reg.register(a, "res-a", tok)
reg.register(b, "res-b")
console.log("unregistered:", reg.unregister(tok))
console.log("again:", reg.unregister(tok))
Memory.free(a)
Memory.free(b)
console.log("end")
`, "unregistered: true\nagain: false\nend\ncleanup: res-b")
}

func TestE2EFinRegNumberHeld(t *testing.T) {
	assertOutputImports(t, `
import Memory from 'memory'
interface Res { id: number }
const reg = new FinalizationRegistry((fd: number) => { console.log("close fd", fd) })
let a: Res = { id: 1 }
reg.register(a, 42)
Memory.free(a)
console.log("end")
`, "end\nclose fd 42")
}

func TestE2EFinRegRegisterInsideFunction(t *testing.T) {
	// The registry handle promotes to a module global; register/free from a
	// named function body sees it.
	assertOutputImports(t, `
import Memory from 'memory'
interface Res { id: number }
const reg = new FinalizationRegistry((held: string) => { console.log("drop:", held) })
function makeAndFree(): void {
  let r: Res = { id: 3 }
  reg.register(r, "inner")
  Memory.free(r)
}
makeAndFree()
console.log("after")
`, "after\ndrop: inner")
}

func TestE2EFinRegCompatJSDynamicTarget(t *testing.T) {
	// Under -compat=js an object literal is a dynamic (any) value; register
	// unboxes the referent like the WeakMap key path does (ADR-00661). No
	// free ever happens, so the callback runs in the exit flush.
	assertOutputCompatJS(t, `
var obj = { name: "dyn" }
var reg = new FinalizationRegistry((held: number) => { console.log("flushed id", held) })
reg.register(obj, 42)
console.log("done")
`, "done\nflushed id 42")
}

func TestE2EFinRegHeldEqualsTargetRejected(t *testing.T) {
	mustCompileError(t, `
const reg = new FinalizationRegistry((held: string) => {})
const a = { v: 1 }
reg.register(a, a)
`, "must not be the target itself")
}

func TestE2EFinRegPrimitiveTargetRejected(t *testing.T) {
	mustCompileError(t, `
const reg = new FinalizationRegistry((held: string) => {})
reg.register("prim", "h")
`, "must be an object")
}

// buildBinaryFinalizersReport mirrors buildBinary with -finalizers=report set
// (main.go's em.SetFinalizersMode), for the leak-diagnostics tests.
func buildBinaryFinalizersReport(t *testing.T, src string) string {
	t.Helper()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not found in PATH")
	}
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
	em.SetFinalizersMode("report")
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

func TestE2EFinRegLeakReport(t *testing.T) {
	binFile := buildBinaryFinalizersReport(t, `
import Memory from 'memory'
interface Res { id: number }
const reg = new FinalizationRegistry((held: string) => { console.log("cleanup:", held) })
let a: Res = { id: 1 }
let b: Res = { id: 2 }
reg.register(a, "freed-one")
reg.register(b, "leaked-one")
Memory.free(a)
console.log("end")
`)
	result, err := exec.Command(binFile).Output()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	got := strings.TrimRight(string(result), "\n")
	want := "end\ncleanup: freed-one\n" +
		"[finalizers] leak: 1 registration(s) never freed\n" +
		"  held=leaked-one   registered at 8:13\n" +
		"cleanup: leaked-one"
	compareLines(t, got, want)
}

func TestE2EFinRegGCModeCollectedFires(t *testing.T) {
	// Same orphan shape as TestE2EGCModeWeakCollected: the referent is
	// created in (and escapes) a helper so conservative stack scanning does
	// not pin it; the WeakRef proves collection actually happened. The
	// cleanup callback fires on the queue drained after the top-level script.
	const src = `
interface Box { v: number }
const reg = new FinalizationRegistry((held: string) => { console.log("collected:", held) })
function orphan(): WeakRef<Box> {
  const tmp: Box = { v: 1 }
  reg.register(tmp, "orphan-box")
  return new WeakRef(tmp)
}
const wr = orphan()
gc()
gc()
console.log("deref null:", wr.deref() === null)
console.log("end")
`
	binFile := buildBinaryGC(t, src)
	out, err := exec.Command(binFile).Output()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	got := strings.TrimRight(string(out), "\n")
	want := "deref null: true\nend\ncollected: orphan-box"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// --- TDD-00163 Stage 5: scope-exit finalization via the free planner ---

// buildBinaryFinRegAuto compiles under -mm=auto (imports resolved).
func buildBinaryFinRegAuto(t *testing.T, src string) string {
	t.Helper()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not found in PATH")
	}
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

func TestE2EFinRegAutoModeScopeExitFires(t *testing.T) {
	// The register-TARGET exemption (TDD-00163 Stage 5): passing the value
	// to reg.register no longer disqualifies it from auto-freeing, so the
	// cleanup fires at scope exit — drained BEFORE the 0ms timer tick, not
	// at the exit flush.
	binFile := buildBinaryFinRegAuto(t, `
interface Res { id: number }
const reg = new FinalizationRegistry((held: string) => { console.log("freed:", held) })
function work(): void {
  const tmp: Res = { id: 1 }
  reg.register(tmp, "scoped-resource")
}
work()
setTimeout(() => { console.log("tick") }, 0)
console.log("end")
`)
	out, err := exec.Command(binFile).Output()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	compareLines(t, strings.TrimRight(string(out), "\n"), "end\nfreed: scoped-resource\ntick")
}

func TestE2EFinRegExplicitFreeFires(t *testing.T) {
	// The same exemption serves an explicit @free under default manual mode.
	assertOutputImports(t, `
import Memory from 'memory'
interface Res { id: number }
const reg = new FinalizationRegistry((held: string) => { console.log("freed:", held) })
function work(): void {
  /** @free */
  const tmp: Res = { id: 1 }
  reg.register(tmp, "explicit-free")
}
work()
setTimeout(() => { console.log("tick") }, 0)
console.log("end")
`, "end\nfreed: explicit-free\ntick")
}

func TestE2EFinRegHeldPositionStillEscapes(t *testing.T) {
	// Only the TARGET position is exempt: a held value is read by the
	// callback after the free, so @free on it stays a compile error.
	mustCompileError(t, `
const reg = new FinalizationRegistry((held: string) => { console.log(held) })
function f(): void {
  const t = { id: 1 }
  /** @free */
  const label = "x" + "y"
  reg.register(t, label)
}
f()
`, "may escape its block")
}

func TestE2EFinRegShadowedRegisterNotExempt(t *testing.T) {
	// A same-named non-registry `register` receiver disqualifies the name
	// program-wide — the exemption never applies to it.
	mustCompileError(t, `
const reg = { register: (a: { id: number }, b: string): void => {} }
function f(): void {
  /** @free */
  const t = { id: 1 }
  reg.register(t, "h")
}
f()
`, "may escape its block")
}
