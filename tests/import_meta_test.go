package tests

// --- import.meta.url (TDD-00055 Stage 1) ---
//
// Resolved entirely at resolve time, per file, into a plain string literal
// — codegen never sees the node. These tests need the actual temp
// directory the resolver ran against (to build the expected "file://..."
// string), which the shared assertMultiFileOutput/resolveMultiFile helpers
// don't expose, so they drive resolver.ResolveProgram + codegen + clang
// directly, mirroring buildBinaryMultiFile's own implementation.

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"KlainMainLang/codegen/llvm"
	"KlainMainLang/resolver"
)

func buildAndRunFromDir(t *testing.T, dir, entryName string) string {
	t.Helper()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not found in PATH")
	}
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
	if out, err := exec.Command("clang", clangArgs...).CombinedOutput(); err != nil {
		t.Fatalf("clang: %v\n%s", err, out)
	}
	result, err := exec.Command(binFile).Output()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	return string(result)
}

func TestE2EImportMetaUrlSingleFile(t *testing.T) {
	dir := writeMultiFile(t, map[string]string{
		"main.ts": `console.log(import.meta.url)`,
	})
	got := buildAndRunFromDir(t, dir, "main.ts")
	want := "file://" + filepath.Join(dir, "main.ts") + "\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestE2EImportMetaUrlIsPerFile(t *testing.T) {
	// Each file's import.meta.url resolves to *its own* absolute path, not
	// the entry file's — proven by having a non-entry file print its own
	// value through an exported function.
	dir := writeMultiFile(t, map[string]string{
		"lib.ts": `
export function libUrl(): string { return import.meta.url }
`,
		"main.ts": `
import { libUrl } from './lib'
console.log(import.meta.url)
console.log(libUrl())
`,
	})
	got := buildAndRunFromDir(t, dir, "main.ts")
	want := "file://" + filepath.Join(dir, "main.ts") + "\n" +
		"file://" + filepath.Join(dir, "lib.ts") + "\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestE2EImportMetaBareRejected(t *testing.T) {
	_, err := resolveMultiFile(t, map[string]string{
		"main.ts": `console.log(import.meta)`,
	}, "main.ts")
	if err == nil {
		t.Fatal("expected a compile error for bare 'import.meta' (no '.url'), got none")
	}
}

func TestE2EImportMetaOtherMemberRejected(t *testing.T) {
	_, err := resolveMultiFile(t, map[string]string{
		"main.ts": `console.log(import.meta.resolve)`,
	}, "main.ts")
	if err == nil {
		t.Fatal("expected a compile error for 'import.meta.resolve' (only '.url' is supported), got none")
	}
}

func TestE2EDynamicImportCallNotYetSupportedRejected(t *testing.T) {
	_, err := resolveMultiFile(t, map[string]string{
		"main.ts": `
async function main(): Promise<void> {
    const mod = await import('./lib')
    console.log(mod)
}
`,
	}, "main.ts")
	if err == nil {
		t.Fatal("expected a compile error for dynamic import(), got none")
	}
}
