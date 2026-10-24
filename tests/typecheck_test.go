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

// A return annotation the body cannot meet is tsc's TS2322 (a generator's
// must be an iterator type) or TS2355 (a body that never returns a value);
// -compat=js runs the same program, the annotation being only a type.
func TestE2EReturnAnnotationRejectedStrict(t *testing.T) {
	for src, want := range map[string]string{
		"function* g(): number { yield 1 }\nfor (const v of g()) console.log(v)\n": "type 'Generator<any, any, unknown>' is not assignable to type 'number'",
		"async function w(): Promise<number> {}\nw().then(v => console.log(v))\n":  "must return a value",
	} {
		if _, err := resolveAndCompile(t, src); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: expected %q, got %v", src, want, err)
		}
	}
}

func TestE2EReturnAnnotationAcceptedCompatJS(t *testing.T) {
	assertOutputCompatJS(t, "function* g(): number { yield 1; yield 2 }\nfor (const v of g()) console.log(v)\n", "1\n2")
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

// An element access by a constant key reads the member it names, and one
// naming no member is TS7053 (TDD-00246).
func TestE2ETypeErrorElementAccessKey(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{"const kRefed = Symbol('refed')\nclass T { [kRefed]: boolean = true\n  m() { const s: string = this[kRefed]; return s } }\n",
			"type 'boolean' is not assignable to type 'string'"},
		{"const kOther = Symbol('other')\nclass T { a = 1 }\nconsole.log(new T()[kOther])\n",
			"element implicitly has an 'any' type because expression of type 'unique symbol' can't be used to index type 'T'"},
		{"class T { a = 1 }\nconst k = 'nope'\nconsole.log(new T()[k])\n",
			"element implicitly has an 'any' type because expression of type '\"nope\"' can't be used to index type 'T'"},
	} {
		_, err := resolveAndCompile(t, c.src)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: expected %q, got %v", c.src, c.want, err)
		}
	}
	if _, err := resolveAndCompile(t, "interface Bag { [s: string]: number }\nconst bag: Bag = { x: 1 }\nconst o = { a: 1, 0x10: 2 }\nclass T { a = 1 }\nconsole.log(bag['zz'], o['a'], o[16], new T()['a'], new T()['toString']())\n"); err != nil {
		t.Errorf("valid accesses rejected: %v", err)
	}
}

// A namespace's type named qualified (`N.A`, `N.I<T>`) is checked: its
// declaration is file-private in the merged program, and an omitted type
// argument takes its default (BACKLOG 70).
func TestQualifiedNamespaceTypesAreChecked(t *testing.T) {
	assertSameAsNode(t, `
namespace N {
  export type A<T = string> = { v: T };
  export interface I<T = number> { w: T }
}
const a: N.A = { v: "x" };
const i: N.I = { w: 3 };
console.log(a.v.toUpperCase(), i.w + 1);
`)
	for _, src := range []string{
		"namespace N { export type A<T = string> = { v: T } }\nconst a: N.A = { v: 1 }\n",
		"namespace N { export type A<T = string> = { v: T } }\nconst a: N.A<string> = { v: 1 }\n",
		"namespace N { export interface I<T = number> { w: T } }\nconst i: N.I = { w: 'x' }\n",
	} {
		_, err := resolveAndCompile(t, src)
		if err == nil || !strings.Contains(err.Error(), "is not assignable to type") {
			t.Errorf("%q: expected a type error, got %v", src, err)
		}
	}
}

// Strict code's early errors reject the strict lane (TypeScript's
// alwaysStrict); -compat=js runs the same names as sloppy JavaScript.
func TestE2EStrictCodeEarlyErrors(t *testing.T) {
	_, err := resolveAndCompile(t, "function g() { return (arguments: any) => arguments }\nconsole.log(g()(1))\n")
	if err == nil || !strings.Contains(err.Error(), "invalid use of 'arguments' in strict mode") {
		t.Fatalf("expected TS1100, got %v", err)
	}
	_, err = resolveAndCompile(t, "var q = 1\nconsole.log(delete q)\n")
	if err == nil || !strings.Contains(err.Error(), "'delete' cannot be called on an identifier") {
		t.Fatalf("expected TS1102, got %v", err)
	}
	assertOutputCompatJS(t, `var q = 1;
function f(p) { var l = 2; return [delete p, delete l, p, l]; }
console.log(delete q, q, f(3), delete nope);
function g() { return (arguments) => arguments; }
console.log(g()(4));
`, "false 1 [ false, false, 3, 2 ] true\n4")
}

// An auto-typed (`let f;`) or `any` name gives a function assigned to it no
// context: its unannotated parameters are TS7006, and its parameters' types
// do not wait on the name's own storage type (ADR-01834).
func TestE2ETypeErrorAssignedFunctionNoContext(t *testing.T) {
	for _, src := range []string{
		"export {}\nlet f;\nf = (a) => a;\nconsole.log(f(1));\n",
		"export {}\nlet h: any;\nh = (a) => a;\nconsole.log(h(3));\n",
	} {
		_, err := resolveAndCompile(t, src)
		if err == nil || !strings.Contains(err.Error(), "parameter 'a' implicitly has an 'any' type") {
			t.Errorf("%s: expected TS7006, got %v", src, err)
		}
	}
	if _, err := resolveAndCompile(t, "export {}\nlet f: (n: number) => number;\nf = (a) => a + 1;\nlet g;\ng = async () => 2;\ng().then((v: number) => console.log(f(v)));\n"); err != nil {
		t.Errorf("contextually typed assignments rejected: %v", err)
	}
	assertSameAsNodeCompatJS(t, `var callCount = 0;
var ref;
ref = async (a) => { callCount = callCount + 1; return a; };
ref(false).then((v) => { console.log(v, callCount); });
var h;
h = function (x, y) { return [x, y]; };
console.log(h(1, "s"));
`)
}
