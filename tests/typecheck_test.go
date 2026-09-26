package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"KlainMainLang/resolver"
)

// The strict lane rejects a program TypeScript rejects for a type error,
// with tsc's code and message; -compat=js compiles it (TDD-00230 P2.7).

func TestE2ETypeErrorRejectedStrict(t *testing.T) {
	_, err := resolveAndCompile(t, "let n: number = \"x\"\nconsole.log(n)\n")
	if err == nil || !strings.Contains(err.Error(), "type 'string' is not assignable to type 'number'") {
		t.Fatalf("expected TS2322, got %v", err)
	}
}

func TestE2ETypeErrorAcceptedCompatJS(t *testing.T) {
	assertOutputCompatJS(t, "const o = { valueOf() { return 4 } }\nconsole.log((o as any) * 2)\n", "8")
}

// An interface declared in an imported file is found under the resolver's
// renaming, and its error names the importing file's line and the source
// name, not the mangled one.
func TestE2ETypeErrorAcrossFiles(t *testing.T) {
	dir := tempDir(t)
	writeFile(t, filepath.Join(dir, "shapes.ts"), "export interface Point { x: number; y: number }\n")
	writeFile(t, filepath.Join(dir, "main.ts"), "import { Point } from './shapes'\nconst p: Point = { x: 1 }\nconsole.log(p.x)\n")
	_, err := resolver.ResolveProgram(filepath.Join(dir, "main.ts"))
	if err == nil {
		t.Fatal("expected TS2741 for the missing property")
	}
	msg := err.Error()
	if !strings.Contains(msg, "main.ts") || !strings.Contains(msg, "2:1: property 'y' is missing in type '{ x: number; }' but required in type 'Point'") {
		t.Errorf("unexpected error: %s", msg)
	}
}

// The CLI labels a type error and prints the -compat=js hint.
func TestE2ETypeErrorCLI(t *testing.T) {
	cli := buildCLI(t)
	dir := tempDir(t)
	src := filepath.Join(dir, "main.ts")
	if err := os.WriteFile(src, []byte("interface P { x: number }\ndeclare const p: P\nconsole.log(p.z)\n"), 0644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(cli, src).CombinedOutput()
	if err == nil {
		t.Fatalf("expected a type error, got success:\n%s", out)
	}
	for _, want := range []string{"type error:", "3:15: property 'z' does not exist on type 'P'", "hint: TypeScript rejects this program; -compat=js compiles it as JavaScript"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}
