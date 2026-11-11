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

func TestE2EFinRegRegisterReturnsUndefined(t *testing.T) {
	// register() evaluates to `undefined` (spec). Using the result must not emit
	// an empty operand (was: invalid IR — a Ref-less void reaching a coercion).
	assertOutputImports(t, `
interface Res { id: number }
const reg = new FinalizationRegistry((held: string) => {})
let a: Res = { id: 1 }
const r = reg.register(a, "res-a")
console.log(r === undefined)
console.log(typeof r)
`, "true\nundefined")
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

func TestE2EFinRegHeldEqualsTargetThrows(t *testing.T) {
	assertOutput(t, `
const reg = new FinalizationRegistry((held: object) => {})
const a = { v: 1 }
try { reg.register(a, a) } catch (e) { console.log((e as Error).name, (e as Error).message) }
`, "TypeError FinalizationRegistry.prototype.register: target and holdings must not be same")
}

func TestE2EFinRegPrimitiveTargetRejected(t *testing.T) {
	mustCompileError(t, `
const reg = new FinalizationRegistry((held: string) => {})
reg.register("prim", "h")
`, "not assignable to parameter of type 'object | symbol'")
}

// buildBinaryFinalizersReport mirrors buildBinary with -finalizers=report set
// (main.go's em.SetFinalizersMode), for the leak-diagnostics tests.
func buildBinaryFinalizersReport(t *testing.T, src string) string {
	t.Helper()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not found in PATH")
	}
	dir := tempDir(t)
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
		clangArgs = append(clangArgs, llvm.LinkLibFlags(lib)...)
	}
	// The float formatter C file, linked exactly as buildBinary and the CLI do —
	// a timer + microtask program ends in the full event loop, which formats
	// numbers.
	clangArgs = appendRuntime(t, em, dir, clangArgs)
	out, err := llvm.ClangCommand(clangArgs...).CombinedOutput()
	if err != nil {
		t.Fatalf("clang: %v\n%s", err, llvm.AnnotateClangOutput(out))
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
	// Same orphan shape as TestE2EGCModeWeakCollected: each referent is
	// created in (and escapes) a helper, and the WeakRefs prove collection
	// happened. Boehm scans conservatively, so a stale copy of one pointer
	// (a spilled callee-saved register, a reused stack slot) can pin that
	// orphan; those copies are few, so most of 64 registered orphans are
	// collected and their cleanups run on a later task.
	const src = `
interface Box { v: number }
let cleaned = 0
const reg = new FinalizationRegistry((held: string) => { if (held === "orphan") cleaned++ })
function orphan(i: number): WeakRef<Box> {
  const tmp: Box = { v: i }
  reg.register(tmp, "orphan")
  return new WeakRef(tmp)
}
const refs: WeakRef<Box>[] = []
for (let i = 0; i < 64; i++) { refs.push(orphan(i)) }
// A WeakRef keeps its target to the end of the job that made it (ADR-01366).
setTimeout(() => {
  gc()
  gc()
  const gone = refs.filter((r) => r.deref() === undefined).length
  console.log("most collected:", gone >= 32)
  setTimeout(() => {
    console.log("cleanups match:", cleaned === gone)
  }, 0)
}, 0)
`
	dir := tempDir(t)
	srcFile := filepath.Join(dir, "prog.ts")
	if err := os.WriteFile(srcFile, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}
	binFile := buildGCProgram(t, dir, srcFile)
	out, err := exec.Command(binFile).Output()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	got := strings.TrimRight(string(out), "\n")
	want := "most collected: true\ncleanups match: true"
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
	dir := tempDir(t)
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
		clangArgs = append(clangArgs, llvm.LinkLibFlags(lib)...)
	}
	// The float formatter C file, linked exactly as buildBinary and the CLI do —
	// a timer + microtask program ends in the full event loop, which formats
	// numbers.
	clangArgs = appendRuntime(t, em, dir, clangArgs)
	out, err := llvm.ClangCommand(clangArgs...).CombinedOutput()
	if err != nil {
		t.Fatalf("clang: %v\n%s", err, llvm.AnnotateClangOutput(out))
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

// A symbol that is not registered can be a target or a token; a registered
// one (`Symbol.for`) is Node's TypeError.
func TestE2EFinRegSymbolTokens(t *testing.T) {
	assertSameAsNode(t, `
const r = new FinalizationRegistry<number>(() => {});
const s = Symbol("t");
r.register({}, 1, s);
console.log(r.unregister(s));
try { r.unregister(Symbol.for("y")) } catch (e: any) { console.log(e.name, e.message) }
try { r.register(Symbol.for("x"), 1) } catch (e: any) { console.log(e.name, e.message) }
`)
}

// An object held boxed (`{}`) is a valid target and token; a primitive is
// Node's TypeError at run time in a JavaScript file.
func TestE2EFinalizationRegistryBoxedObjectArgs(t *testing.T) {
	assertSameAsNode(t, `
const fr = new FinalizationRegistry<string>((h) => console.log(h));
const tok = {}; const r = fr.register({}, "x", tok); console.log(r, fr.unregister(tok), fr.unregister(tok));
`)
	assertSameAsNodeCompatJS(t, `
const fr = new FinalizationRegistry(() => {});
for (const [k, f] of [["t", () => fr.register(5, "x")], ["u", () => fr.register({}, "x", 5)], ["uu", () => fr.unregister(5)], ["same", () => { const o = {}; fr.register(o, "x", o); }]])
  try { f(); console.log(k, "ok"); } catch (e) { console.log(k, e.constructor.name, e.message); }
`)
}
