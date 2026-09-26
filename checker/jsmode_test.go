package checker_test

import (
	"testing"

	"KlainMainLang/ast"
	"KlainMainLang/binder"
	"KlainMainLang/checker"
	"KlainMainLang/options"
	"KlainMainLang/parser"
)

// typeOfLastJS is typeOfLast under -compat=js.
func typeOfLastJS(t *testing.T, src string) string {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("%s: %v", src, err)
	}
	c := checker.NewWith(binder.BindWith(prog, binder.Options{AnnexB: true}), options.Options{Compat: "js"})
	var last ast.Expression
	ast.Inspect(prog, func(n ast.Node) bool {
		if es, ok := n.(*ast.ExpressionStatement); ok {
			last = es.Expr
		}
		return true
	})
	ty := c.TypeOf(last)
	if c.Unanswered(ty) {
		return "?"
	}
	return ty.String()
}

func TestCompatJS(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{"function add(a, b) { a; return a + b }\nadd(1, 2)", "number"},
		{"function greet(n) { n; return 'hi ' + n }\ngreet('x')", "string"},
		{"function f(a) { a }\nf(1)\nf('x')", "?"},
		{"function g(a) { a }", "any"},
		{"declare const xs: number[]\nxs.map((v) => { v; return 0 })", "?"},
		{"const h = (a) => a\nh(true)", "boolean"},
		{"r(3)\nfunction r(n) { if (n > 0) r(n - 1); n }", "number"}, // the recursive site is skipped
		{"class P { constructor(x, y) { this.x = x; this.y = y } }\nnew P(1, 'a').y", "string"},
		{"class P { constructor(x) { this.x = x; if (x) { this.z = [1] } } }\nnew P(1).z", "number[]"},
		{"class Q { constructor() { this.v = 1; this.v = 'a' } }\nnew Q().v", "?"},
	} {
		if got := typeOfLastJS(t, tc.src); got != tc.want {
			t.Errorf("%q: got %s, want %s", tc.src, got, tc.want)
		}
	}
	// Strict mode keeps an unannotated parameter unanswered.
	if got := typeOfLast(t, "function g(a) { a }\ng(1)\na0()\nfunction a0() { }\ng"); got != "?" {
		t.Errorf("strict: got %s", got)
	}
}
