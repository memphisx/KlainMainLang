package tests

import (
	"KlainMainLang/codegen/llvm"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// buildCLI builds the klainmain CLI binary once per test that needs it — the
// island backend (TDD-00056) runs in main.go's clang orchestration, not the
// in-process emitter the other E2E helpers use, so these tests shell out.
func buildCLI(t *testing.T) string {
	t.Helper()
	root := findRepoRoot(t)
	bin := filepath.Join(tempDir(t), "klainmain"+llvm.HostExeSuffix())
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build klainmain: %v\n%s", err, out)
	}
	return bin
}

// findRepoRoot walks up from the working directory to the module root (the
// directory holding go.mod). Robust to how the suite is launched: `go test
// ./tests/` runs from the package dir, while `make test-par` executes the
// precompiled test binary from the repo root — a fixed `..` is wrong for one of
// them, but the go.mod probe finds the root in both.
func findRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("go.mod not found walking up from working directory")
		}
		dir = parent
	}
}

// TestE2EDynamicImportLazy exercises the full lazy dynamic import (TDD-00056):
// the target is compiled to a shared-library island loaded on first import, its
// top-level runs lazily (not at startup), and its typed exports are read back
// through the result object.
func TestE2EDynamicImportLazy(t *testing.T) {
	cli := buildCLI(t)
	dir := tempDir(t)
	writeFile(t, filepath.Join(dir, "mod.ts"),
		"export const answer: number = 42;\nexport const greeting: string = \"kalimera\";\nconsole.log(\"island top-level\");\n")
	writeFile(t, filepath.Join(dir, "entry.ts"),
		"async function main(): Promise<void> {\n"+
			"  console.log(\"before\");\n"+
			"  const m = await import('./mod');\n"+
			"  console.log(m.greeting, m.answer);\n"+
			"}\nmain();\n")

	entry := filepath.Join(dir, "entry.ts")
	compile := exec.Command(cli, "-dynamic-import=lazy", entry)
	if out, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	bin := filepath.Join(dir, "entry")
	run := exec.Command(bin)
	out, err := run.CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	got := string(out)
	// Laziness: the island's top-level must run AFTER "before", not at startup.
	want := "before\nisland top-level\nkalimera 42\n"
	if got != want {
		t.Errorf("lazy dynamic import output:\ngot  %q\nwant %q", got, want)
	}
}

// TestE2EDynamicImportLazyNonASCIIPath: the island is located beside the
// executable, so the loader must survive an install directory whose name is not
// representable in a narrow code page (on Windows that means the wide
// GetModuleFileNameW/LoadLibraryW boundary, not the ANSI one).
func TestE2EDynamicImportLazyNonASCIIPath(t *testing.T) {
	cli := buildCLI(t)
	dir := filepath.Join(tempDir(t), "καλημέρα-日本")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeFile(t, filepath.Join(dir, "mod.ts"), "export const answer: number = 7;\n")
	writeFile(t, filepath.Join(dir, "entry.ts"),
		"async function main(): Promise<void> {\n"+
			"  const m = await import('./mod');\n"+
			"  console.log(m.answer);\n"+
			"}\nmain();\n")
	compile := exec.Command(cli, "-dynamic-import=lazy", filepath.Join(dir, "entry.ts"))
	if out, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	out, err := exec.Command(filepath.Join(dir, "entry")).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	if string(out) != "7\n" {
		t.Errorf("got %q, want %q", out, "7\n")
	}
}

// TestE2EDynamicImportNonLiteralRejected confirms a runtime-computed specifier
// is a clean compile error, not a silent gap.
func TestE2EDynamicImportNonLiteralRejected(t *testing.T) {
	cli := buildCLI(t)
	dir := tempDir(t)
	writeFile(t, filepath.Join(dir, "entry.ts"),
		"const p = \"./mod\";\nasync function main(): Promise<void> { await import(p); }\nmain();\n")
	compile := exec.Command(cli, "-dynamic-import=lazy", filepath.Join(dir, "entry.ts"))
	out, err := compile.CombinedOutput()
	if err == nil {
		t.Fatalf("expected compile failure for non-literal specifier, got success")
	}
	if !strings.Contains(string(out), "string-literal specifier") {
		t.Errorf("expected string-literal-specifier error, got: %s", out)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// runLazyIsland compiles entry.ts (with the given island files) under
// -dynamic-import=lazy and runs it, returning combined output and exit code.
func runLazyIsland(t *testing.T, files map[string]string) (string, int) {
	t.Helper()
	cli := buildCLI(t)
	dir := tempDir(t)
	for name, src := range files {
		writeFile(t, filepath.Join(dir, name), src)
	}
	compile := exec.Command(cli, "-dynamic-import=lazy", filepath.Join(dir, "entry.ts"))
	if out, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	run := exec.Command(filepath.Join(dir, "entry"))
	out, _ := run.CombinedOutput()
	return string(out), run.ProcessState.ExitCode()
}

// --- TDD-00225: an island with a top-level await is driven by the importer's
// loop (ADR-01056) — the importer's own microtasks and timers interleave with
// the island's awaits, as in Node, instead of waiting behind a nested loop. ---

func TestE2EDynamicImportLazyIslandTopLevelAwaitInterleaves(t *testing.T) {
	out, code := runLazyIsland(t, map[string]string{
		"mod.ts": "const later = (ms: number) => new Promise<number>((res) => setTimeout(() => res(42), ms));\n" +
			"console.log(\"island: start\");\n" +
			"export const v: number = await later(30);\n" +
			"console.log(\"island: after await\", v);\n" +
			"export const after: string = \"done\";\n",
		"entry.ts": "console.log(\"main: start\");\n" +
			"setTimeout(() => console.log(\"main: timer 10\"), 10);\n" +
			"Promise.resolve().then(() => console.log(\"main: microtask\"));\n" +
			"const m = await import('./mod');\n" +
			"console.log(\"main: got\", m.v, m.after);\n" +
			"setTimeout(() => console.log(\"main: timer after\"), 5);\n",
	})
	want := "main: start\nisland: start\nmain: microtask\nmain: timer 10\nisland: after await 42\nmain: got 42 done\nmain: timer after\n"
	if out != want || code != 0 {
		t.Errorf("exit %d, output:\ngot  %q\nwant %q", code, out, want)
	}
}

// import() from inside an async function: the importer's synchronous tail and
// its timer run while the island waits.
func TestE2EDynamicImportLazyIslandAwaitFromAsyncFunction(t *testing.T) {
	out, code := runLazyIsland(t, map[string]string{
		"mod.ts": "const later = (ms: number) => new Promise<number>((res) => setTimeout(() => res(42), ms));\n" +
			"console.log(\"island: start\");\n" +
			"export const v: number = await later(30);\n" +
			"console.log(\"island: after await\", v);\n",
		"entry.ts": "async function load(): Promise<number> {\n" +
			"  console.log(\"load: before\");\n" +
			"  const m = await import('./mod');\n" +
			"  console.log(\"load: after\", m.v);\n" +
			"  return m.v;\n}\n" +
			"setTimeout(() => console.log(\"main: timer 15\"), 15);\n" +
			"load().then((v) => console.log(\"main: loaded\", v));\n" +
			"console.log(\"main: sync end\");\n",
	})
	want := "load: before\nisland: start\nmain: sync end\nmain: timer 15\nisland: after await 42\nload: after 42\nmain: loaded 42\n"
	if out != want || code != 0 {
		t.Errorf("exit %d, output:\ngot  %q\nwant %q", code, out, want)
	}
}

// A throw after the island's top-level await rejects the import() promise
// with the island's Error object.
func TestE2EDynamicImportLazyIslandThrowRejectsImport(t *testing.T) {
	out, code := runLazyIsland(t, map[string]string{
		"mod.ts": "export const x: number = 1;\n" +
			"await new Promise<void>((r) => setTimeout(r, 5));\n" +
			"throw new RangeError(\"island exploded\");\n",
		"entry.ts": "try {\n  const m = await import('./mod');\n  console.log(\"unexpected\", m.x);\n} catch (e) {\n" +
			"  console.log(\"caught:\", e.name, e.message, e instanceof RangeError);\n}\nconsole.log(\"after\");\n",
	})
	want := "caught: RangeError island exploded true\nafter\n"
	if out != want || code != 0 {
		t.Errorf("exit %d, output:\ngot  %q\nwant %q", code, out, want)
	}
}

// An island whose top-level await can never settle: the importer's await of
// import() is an unsettled top-level await — Node's warning, exit code 13.
func TestE2EDynamicImportLazyIslandUnsettledExits13(t *testing.T) {
	out, code := runLazyIsland(t, map[string]string{
		"mod.ts":   "export const y: number = 2;\nconsole.log(\"island: start\");\nawait new Promise<void>(() => {});\n",
		"entry.ts": "console.log(\"before\");\nconst m = await import('./mod');\nconsole.log(\"never\", m.y);\n",
	})
	if code != 13 || !strings.Contains(out, "unsettled top-level await") || strings.Contains(out, "never") {
		t.Errorf("exit %d, output:\n%s", code, out)
	}
}
