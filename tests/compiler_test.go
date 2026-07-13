package tests

import (
	"os"
	"os/exec"
	"path/filepath"
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
