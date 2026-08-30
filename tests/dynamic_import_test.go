package tests

import (
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
	bin := filepath.Join(t.TempDir(), "klainmain")
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
	dir := t.TempDir()
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

// TestE2EDynamicImportNonLiteralRejected confirms a runtime-computed specifier
// is a clean compile error, not a silent gap.
func TestE2EDynamicImportNonLiteralRejected(t *testing.T) {
	cli := buildCLI(t)
	dir := t.TempDir()
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
