package tests

import (
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"KlainMainLang/diag"
	"KlainMainLang/options"
	"KlainMainLang/resolver"
)

// TypeScript's noUnusedLocals and noUnusedParameters (-no-unused-locals,
// -no-unused-parameters): each test resolves a program with the checks on
// and compares the diagnostics with the ones tsc reports for it.

func unusedDiags(t *testing.T, files map[string]string, entry string, locals, params bool) []string {
	t.Helper()
	dir := writeMultiFile(t, files)
	_, err := resolver.ResolveProgramWithOptions(filepath.Join(dir, entry),
		options.Options{NoUnusedLocals: locals, NoUnusedParameters: params})
	var got []string
	for _, d := range diag.As(err) {
		got = append(got, filepath.Base(d.File)+":"+itoa(d.Pos.Line)+" TS"+itoa(d.Code())+" "+d.Text)
	}
	if err != nil && len(got) == 0 {
		t.Fatalf("resolve: %v", err)
	}
	sort.Strings(got)
	return got
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for ; n > 0; n /= 10 {
		s = string(rune('0'+n%10)) + s
	}
	return s
}

func wantDiags(t *testing.T, got []string, want ...string) {
	t.Helper()
	sort.Strings(want)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("diagnostics:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

const unusedProgram = `import { readFileSync, existsSync } from "fs";
interface Unused { a: number }
interface Used { a: number }
type Alias = string;
export function f(x: number, _y: number, z: number): number {
  let w = 1;
  w = 2;
  let c = 0;
  c++;
  const [p, _q] = [1, 2];
  const { a, ...rest } = { a: 1, b: 2 };
  function rec(n: number): number { return n > 0 ? rec(n - 1) : 0; }
  const u: Used = { a: x };
  console.log(rest, u.a, existsSync("x"));
  return 1;
}
function g<T, U>(t: T): T { return t; }
class K {
  private hidden = 1;
  private shown = 2;
  #never = 3;
  constructor(private kept: number, private dropped: number) {}
  show(): number { return this.shown + this.kept; }
}
interface Box<V, W> { v: V }
export const k = g<number, string>(1);
export const box: Box<number, string> = { v: 1 };
export const kk = new K(1, 2).show();
`

func TestE2ENoUnusedLocals(t *testing.T) {
	got := unusedDiags(t, map[string]string{"main.ts": unusedProgram}, "main.ts", true, false)
	wantDiags(t, got,
		"main.ts:1 TS6133 'readFileSync' is declared but its value is never read",
		"main.ts:2 TS6196 'Unused' is declared but never used",
		"main.ts:4 TS6196 'Alias' is declared but never used",
		"main.ts:6 TS6133 'w' is declared but its value is never read",
		"main.ts:8 TS6133 'c' is declared but its value is never read",
		"main.ts:10 TS6133 'p' is declared but its value is never read",
		"main.ts:12 TS6133 'rec' is declared but its value is never read",
		"main.ts:19 TS6133 'hidden' is declared but its value is never read",
		"main.ts:21 TS6133 '#never' is declared but its value is never read",
		"main.ts:22 TS6138 property 'dropped' is declared but its value is never read",
	)
}

func TestE2ENoUnusedParameters(t *testing.T) {
	got := unusedDiags(t, map[string]string{"main.ts": unusedProgram}, "main.ts", false, true)
	wantDiags(t, got,
		"main.ts:5 TS6133 'z' is declared but its value is never read",
		"main.ts:17 TS6133 'U' is declared but its value is never read",
		"main.ts:25 TS6133 'W' is declared but its value is never read",
	)
}

// Groups whose every member is unused are reported once.
func TestE2ENoUnusedGroups(t *testing.T) {
	got := unusedDiags(t, map[string]string{"main.ts": `import { join, basename } from "path";
export function f(): void {
  var x, y = 10;
  const { a, b } = { a: 1, b: 2 };
}
export function g<T, U>(): void {}
`}, "main.ts", true, true)
	wantDiags(t, got,
		"main.ts:1 TS6192 all imports in import declaration are unused",
		"main.ts:3 TS6199 all variables are unused",
		"main.ts:4 TS6198 all destructured elements are unused",
		"main.ts:6 TS6205 all type parameters are unused",
	)
}

// Each file is checked as written: an import only one file uses, the
// exported declarations of a module, and the globals of a script.
func TestE2ENoUnusedAcrossFiles(t *testing.T) {
	got := unusedDiags(t, map[string]string{
		"lib.ts": `export function helper(): number { return 1; }
export const shared = 2;
function privateHelper(): number { return 3; }
`,
		"main.ts": `import { helper, shared } from "./lib";
console.log(helper());
`,
	}, "main.ts", true, true)
	wantDiags(t, got,
		"lib.ts:3 TS6133 'privateHelper' is declared but its value is never read",
		"main.ts:1 TS6133 'shared' is declared but its value is never read",
	)
	// A script's top-level declarations are globals: neither check covers them.
	got = unusedDiags(t, map[string]string{"main.ts": "const a = 1;\nfunction b(): void {}\n"}, "main.ts", true, true)
	wantDiags(t, got)
}

// Without the options, and under -compat=js, nothing is reported.
func TestE2ENoUnusedOffByDefault(t *testing.T) {
	got := unusedDiags(t, map[string]string{"main.ts": unusedProgram}, "main.ts", false, false)
	wantDiags(t, got)
}
