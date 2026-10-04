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
	if !strings.Contains(msg, "main.ts") || !strings.Contains(msg, "2:7: property 'y' is missing in type '{ x: number; }' but required in type 'Point'") {
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

// A generic class named without type arguments takes its parameters'
// defaults (`class G<T = any>`: `G` is `G<any>`), not an unchecked type.
func TestE2ETypeErrorClassDefaultTypeArgs(t *testing.T) {
	_, err := resolveAndCompile(t, "class G<T = any> { x = 1 }\nfunction f(): G { return new G() }\nconst s: number = f\n")
	if err == nil || !strings.Contains(err.Error(), "type '() => G<any>' is not assignable to type 'number'") {
		t.Fatalf("expected TS2322, got %v", err)
	}
}

// Two implementations of one function are TS2393; overload signatures
// followed by one implementation are not.
func TestE2ETypeErrorDuplicateFunctionImplementation(t *testing.T) {
	_, err := resolveAndCompile(t, "function f() { return 1 }\nfunction f() { return 2 }\nconsole.log(f())\n")
	if err == nil || !strings.Contains(err.Error(), "duplicate function implementation of 'f'") {
		t.Fatalf("expected TS2393, got %v", err)
	}
	assertOutput(t, "function g(a: number): number;\nfunction g(a: string): string;\nfunction g(a: any): any { return a }\nconsole.log(g(1))\n", "1")
}

// `let m: RegExpExecArray | null; m = re.exec(s)` is exec's result type.
func TestE2ERegExpExecArrayAnnotation(t *testing.T) {
	assertOutput(t, "let m: RegExpExecArray | null;\nif ((m = /a(b)/.exec('xab')) !== null) console.log(m[1])\nconst k: RegExpMatchArray | null = 'cd'.match(/(d)/)\nconsole.log(k![1])\n", "b\nd")
}

// A namespace merged into a function declares members the function has.
func TestE2EFunctionNamespaceMergeMembers(t *testing.T) {
	assertOutput(t, "function d(km: number): string { return km + 'km' }\nnamespace d { export const unit = 'km'; export function miles(k: number): number { return k * 0.62 } }\nconsole.log(d(5), d.unit, d.miles(10))\n", "5km km 6.2")
}
