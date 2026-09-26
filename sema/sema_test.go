package sema_test

import (
	"reflect"
	"strings"
	"testing"

	"KlainMainLang/ast"
	"KlainMainLang/parser"
	"KlainMainLang/sema"
)

// newAt returns the expression of the n-th `new` in src after Prepare.
func prepared(t *testing.T, src string) *ast.Program {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := sema.Prepare(prog); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	return prog
}

func firstNew(prog *ast.Program) ast.Node {
	var found ast.Node
	ast.Inspect(prog, func(n ast.Node) bool {
		if found != nil {
			return false
		}
		if strings.HasPrefix(reflect.TypeOf(n).Elem().Name(), "New") {
			found = n
			return false
		}
		return true
	})
	return found
}

func TestBuiltinConstructorsResolved(t *testing.T) {
	cases := []struct {
		src  string
		want any
	}{
		{"const a = new Array<number>(3)", &ast.NewArrayExpression{}},
		{"const m = new Map<string, Array<number>>()", &ast.NewMapExpression{}},
		{"const s = new Set<number>()", &ast.NewSetExpression{}},
		{`const e = new Error("oops")`, &ast.NewErrorExpression{}},
		{"const d = new Date", &ast.NewDateExpression{}},
		{"const u = new Uint8Array(4)", &ast.NewTypedArrayExpression{}},
		// Readable is a `stream` export, never a global: no import, no builtin.
		{"const r = new stream.Readable()", &ast.NewExpression{}},
		{"const a = new Array(1, 2)", &ast.ArrayLiteral{}},
	}
	for _, c := range cases {
		got := firstNew(prepared(t, c.src))
		if got == nil {
			// `new Array(1, 2)` becomes an array literal
			var lit ast.Node
			ast.Inspect(prepared(t, c.src), func(n ast.Node) bool {
				if _, ok := n.(*ast.ArrayLiteral); ok {
					lit = n
				}
				return true
			})
			got = lit
		}
		if reflect.TypeOf(got) != reflect.TypeOf(c.want) {
			t.Errorf("%s: got %T, want %T", c.src, got, c.want)
		}
	}
}

// A user binding named like a builtin shadows it wherever it is in scope.
func TestUserBindingsShadowBuiltins(t *testing.T) {
	for _, src := range []string{
		"class Event { n: number; constructor(n: number) { this.n = n } }\nconst e = new Event(5)",
		"function f(Map: any) { return new Map() }",
		"function g() { class Date {}; return new Date() }",
		"const h = (Set: any) => new Set()",
		"import { Blob } from './my-blob'\nconst b = new Blob()",
	} {
		if n, ok := firstNew(prepared(t, src)).(*ast.NewExpression); !ok {
			t.Errorf("%q: user binding not respected, got %T", src, n)
		}
	}
	// … and only there: outside the shadowing function the builtin is back.
	prog := prepared(t, "function f(Map: any) { return 1 }\nconst m = new Map<string, number>()")
	if _, ok := firstNew(prog).(*ast.NewMapExpression); !ok {
		t.Errorf("builtin Map outside the shadowing scope not resolved: %T", firstNew(prog))
	}
}

func TestBuiltinConstructorArity(t *testing.T) {
	for src, want := range map[string]string{
		"new WeakMap(1)":            "does not accept arguments",
		"new EventSource()":         "takes 1 argument",
		"new Worker(path)":          "string-literal path",
		`new Error("x", { y: 1 })`:  "{ cause: <expr> }",
		"new Date(1,2,3,4,5,6,7,8)": "at most 7 arguments",
	} {
		prog, err := parser.Parse(src)
		if err != nil {
			t.Fatalf("parse %s: %v", src, err)
		}
		if err := sema.Prepare(prog); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v, want error containing %q", src, err, want)
		}
	}
}
