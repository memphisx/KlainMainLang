package binder_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"KlainMainLang/ast"
	"KlainMainLang/binder"
	"KlainMainLang/parser"
)

// refs binds src and renders each identifier reference as
// `name@line:col -> declLine:declCol` (the position of the node declaring the
// symbol first: the function, for a parameter),
// `name@line:col -> global`, or `name -> arguments` for the implicit one.
func refs(t *testing.T, src string) []string {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	b := binder.Bind(prog)
	var out []string
	ast.Inspect(prog, func(n ast.Node) bool {
		id, ok := n.(*ast.Identifier)
		if !ok {
			return true
		}
		sym, seen := b.Resolve(id)
		p := id.GetPos()
		switch {
		case !seen:
			out = append(out, fmt.Sprintf("%s@%d:%d -> UNSEEN", id.Name, p.Line, p.Col))
		case sym == nil:
			out = append(out, fmt.Sprintf("%s@%d:%d -> global", id.Name, p.Line, p.Col))
		case sym.Implicit:
			out = append(out, fmt.Sprintf("%s@%d:%d -> implicit", id.Name, p.Line, p.Col))
		default:
			d := sym.Declarations[0].Pos
			out = append(out, fmt.Sprintf("%s@%d:%d -> %d:%d", id.Name, p.Line, p.Col, d.Line, d.Col))
		}
		return true
	})
	return out
}

func check(t *testing.T, src string, want ...string) {
	t.Helper()
	got := refs(t, src)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("%s\ngot:\n  %s\nwant:\n  %s", src, strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

func TestScopes(t *testing.T) {
	// var hoists to the function; a later top-level function is visible.
	check(t, "function f() {\n  x = 1\n  var x\n  return g()\n}\nfunction g() { return 2 }",
		"x@2:3 -> 3:3", "g@4:10 -> 6:11")
	// let is block-scoped: the inner block shadows, the outer resumes.
	check(t, "let a = 1\n{\n  let a = 2\n  log(a)\n}\nlog(a)",
		"log@4:3 -> global", "a@4:7 -> 3:3", "log@6:1 -> global", "a@6:5 -> 1:1")
	// Parameters, and a default reading an earlier parameter.
	check(t, "function f(a: number, b: number = a) { return a + b }",
		"a@1:35 -> 1:11", "a@1:47 -> 1:11", "b@1:51 -> 1:11")
	// A catch variable is visible only in its clause.
	check(t, "try { t() } catch (e) { log(e) }\nlog(e)",
		"t@1:7 -> global", "log@1:25 -> global", "e@1:29 -> 1:1", "log@2:1 -> global", "e@2:5 -> global")
	// A named function expression sees its own name; outside, it is not
	// declared.
	check(t, "const h = function fact(n: number): number { return n ? n * fact(n - 1) : 1 }\nfact(1)",
		"n@1:53 -> 1:11", "n@1:57 -> 1:11", "fact@1:61 -> 1:11", "n@1:66 -> 1:11", "fact@2:1 -> global")
	// `arguments` is implicit in a function, not in an arrow.
	check(t, "function f() { return arguments }\nconst g = () => arguments",
		"arguments@1:23 -> implicit", "arguments@2:17 -> global")
	// for-of and for-let heads are scoped to the loop.
	check(t, "for (const v of xs) { log(v) }\nfor (let i = 0; i < 3; i++) {}\nlog(i)",
		"xs@1:17 -> global", "log@1:23 -> global", "v@1:27 -> 1:1", "i@2:17 -> 2:6", "i@2:24 -> 2:6", "log@3:1 -> global", "i@3:5 -> global")
	// A namespace member's body resolves a bare sibling name to the member;
	// outside, only the namespace itself is declared.
	check(t, "namespace Geo {\n  const SCALE = 2\n  var unit = 1\n  function area(w: number) { return w * SCALE * unit }\n  export function describe() { return area(3) }\n}\nGeo.describe()\nSCALE",
		"w@4:37 -> 4:16", "SCALE@4:41 -> 2:3", "unit@4:49 -> 3:3", "area@5:39 -> 4:16", "Geo@7:1 -> 0:0", "SCALE@8:1 -> global")
	// Imports, classes and enum members.
	check(t, "import { readFileSync as rf } from 'fs'\nclass C { m() { return rf('x') } }\nenum E { A = 1, B = A + 1 }\nnew C()",
		"rf@2:24 -> 1:1", "A@3:21 -> 3:1")
}

func TestNewTargetAndConflicts(t *testing.T) {
	prog, err := parser.Parse("class P {}\nconst p = new P()\nconst q = new Map()\nlet z = 1\nlet z = 2\nvar v = 1\nvar v = 2")
	if err != nil {
		t.Fatal(err)
	}
	b := binder.Bind(prog)
	var news []*ast.NewExpression
	ast.Inspect(prog, func(n ast.Node) bool {
		if ne, ok := n.(*ast.NewExpression); ok {
			news = append(news, ne)
		}
		return true
	})
	if len(news) != 2 || b.NewTarget(news[0]) == nil || b.NewTarget(news[0]).Name != "P" || b.NewTarget(news[1]) != nil {
		t.Errorf("new targets wrong")
	}
	if len(b.Conflicts) != 1 || b.Conflicts[0].Symbol.Name != "z" {
		t.Errorf("conflicts %+v", b.Conflicts)
	}
}

// Every identifier the generated traversal reaches, in every parseable file
// of the corpus, was seen by the binder: none is left out of both resolution
// and the global set.
func TestEveryIdentifierIsBound(t *testing.T) {
	var files []string
	for _, root := range []string{"../examples", "../tests", "../apps", "../tools"} {
		filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() && (strings.HasSuffix(p, ".ts") || strings.HasSuffix(p, ".js")) && !strings.Contains(p, "node_modules") {
				files = append(files, p)
			}
			return nil
		})
	}
	checked := 0
	for _, f := range files {
		src, _ := os.ReadFile(f)
		prog, err := parser.Parse(string(src))
		if err != nil {
			continue
		}
		b := binder.Bind(prog)
		ast.Inspect(prog, func(n ast.Node) bool {
			if id, ok := n.(*ast.Identifier); ok {
				if _, seen := b.Resolve(id); !seen {
					t.Errorf("%s: identifier %s at %d:%d not seen by the binder", f, id.Name, id.GetPos().Line, id.GetPos().Col)
				}
			}
			return true
		})
		checked++
	}
	if checked < 300 {
		t.Fatalf("only %d files bound", checked)
	}
}

// Redeclarations follow strict code's early errors.
func TestRedeclarations(t *testing.T) {
	for _, c := range []struct {
		src  string
		want string // clashing names, in order
	}{
		{"let x = 1\nlet x = 2", "x"},
		{"var x = 1\nvar x = 2", ""},
		{"function f() {}\nvar f = 1", ""},
		{"function h() {}\nfunction h() {}", ""},
		{"let x = 1\n{ var x = 2 }", "x"},
		{"{ var x = 2 }\nlet x = 1", "x"},
		{"function f(a: number) { let a = 1 }", "a"},
		{"function f(a: number) { var a = 1 }", ""},
		{"const f = (a: number) => { let a = 1 }", "a"},
		{"try {} catch (e) { let e = 1 }", "e"},
		{"try {} catch (e) { var e = 1 }", ""},
		{"{ function g() {}\nfunction g() {} }", "g"},
		{"class C {}\ninterface C { x: number }", ""},
		{"class C {}\nlet C = 1", "C"},
		{"enum E { A }\nenum E { B = 2 }", "E (merge)"},
		{"interface I { a: number }\ninterface I { b: number }", ""},
		{"type T = number\ninterface T { a: number }", "T"},
		{"interface V { a: number }\nconst V = 1", ""},
		{"enum E { A }\nconst E = 1", "E"},
		{"import { a } from 'fs'\nlet a = 1", "a"},
		{"namespace N { export const v = 1 }\nfunction N() {}", ""},
		{"namespace N { export const v = 1 }\nlet N = 1", "N"},
		{"for (let i = 0; i < 1; i++) { let i = 2 }", ""},
		{"class K { m(p: number) { let p = 1 } }", "p"},
	} {
		prog, err := parser.Parse(c.src)
		if err != nil {
			t.Fatalf("%s: %v", c.src, err)
		}
		var got []string
		for _, d := range binder.Bind(prog).Diagnostics(nil) {
			name := strings.TrimSuffix(strings.TrimPrefix(d.Text, "identifier '"), "' has already been declared")
			if strings.HasPrefix(d.Text, "merging") {
				name = strings.TrimSuffix(strings.TrimPrefix(d.Text, "merging the declarations of '"), "' is not yet supported") + " (merge)"
			}
			got = append(got, name)
		}
		if strings.Join(got, ",") != c.want {
			t.Errorf("%q: clashes %v, want %q", c.src, got, c.want)
		}
	}
}

// Under Annex B (sloppy scripts), plain function declarations in a block
// merge; async or generator ones, and a clash with let, still error.
func TestAnnexBBlockFunctions(t *testing.T) {
	for src, want := range map[string]int{
		"{ function a() {}\nfunction a() {} }":                             0,
		"{ async function a() {}\nfunction a() {} }":                       1,
		"{ function a() {}\nlet a = 1 }":                                   1,
		"switch (1) { case 1: function a() {}\ndefault: function a() {} }": 0,
	} {
		prog, err := parser.Parse(src)
		if err != nil {
			t.Fatal(err)
		}
		if got := len(binder.BindWith(prog, binder.Options{AnnexB: true}).Diagnostics(nil)); got != want {
			t.Errorf("%q: %d clashes, want %d", src, got, want)
		}
		if strict := len(binder.Bind(prog).Diagnostics(nil)); strict == 0 {
			t.Errorf("%q: strict code accepted it", src)
		}
	}
}
