package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ADR-00926: `--run` compiles into a temp directory, executes the binary
// (forwarding program arguments after the filename), propagates its exit code,
// and cleans up. Runs through main.go's CLI orchestration, so it shells out.

func TestRunFlagExecutesAndForwardsExit(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not found in PATH")
	}
	cli := buildCLI(t)
	dir := tempDir(t)
	src := filepath.Join(dir, "prog.ts")
	if err := os.WriteFile(src, []byte(`console.log("ran ok"); process.exit(4);`), 0644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(cli, "--run", src)
	out, err := cmd.CombinedOutput()
	if !strings.Contains(string(out), "ran ok") {
		t.Fatalf("expected program stdout 'ran ok', got:\n%s", out)
	}
	ee, ok := err.(*exec.ExitError)
	if !ok || ee.ExitCode() != 4 {
		t.Fatalf("expected propagated exit code 4, got err=%v", err)
	}
	// The temp build directory must not leak into the source tree.
	if entries, _ := filepath.Glob(filepath.Join(dir, "prog")); len(entries) != 0 {
		t.Fatalf("--run left a binary behind: %v", entries)
	}
}

func TestRunFlagRejectsWithTarget(t *testing.T) {
	cli := buildCLI(t)
	dir := tempDir(t)
	src := filepath.Join(dir, "p.ts")
	if err := os.WriteFile(src, []byte(`console.log(1)`), 0644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(cli, "--run", "--target", "aarch64-linux-gnu", src)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected --run --target to be rejected, got success:\n%s", out)
	}
	if !strings.Contains(string(out), "--run cannot be combined with --target") {
		t.Fatalf("expected the --run/--target rejection message, got:\n%s", out)
	}
}
