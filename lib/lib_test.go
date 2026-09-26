package lib_test

import (
	"testing"

	"KlainMainLang/ast"
	"KlainMainLang/binder"
	"KlainMainLang/checker"
	"KlainMainLang/lib"
	"KlainMainLang/parser"
)

// TestDeclarationsType: the declarations bind around a program, and the
// checker reads builtin members through them (apparent types included).
func TestDeclarationsType(t *testing.T) {
	progs, err := lib.Programs()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ src, want string }{
		{`"abc".indexOf("b")`, "number"},
		{`"abc".toUpperCase()`, "string"},
		{`[1, 2].indexOf(2)`, "number"},
		{`[1, 2].pop()`, "number | undefined"},
		{`Math.max(1, 2)`, "number"},
		{`Number.isInteger(1)`, "boolean"},
		{`JSON.stringify(1)`, "string"},
		{`parseInt("7")`, "number"},
		{`(1.5).toFixed(1)`, "string"},
	} {
		prog, err := parser.Parse(tc.src)
		if err != nil {
			t.Fatalf("%s: %v", tc.src, err)
		}
		c := checker.New(binder.BindWith(prog, binder.Options{Lib: progs}))
		es, ok := prog.Body[len(prog.Body)-1].(*ast.ExpressionStatement)
		if !ok {
			t.Fatalf("%s: not an expression statement", tc.src)
		}
		ty := c.TypeOf(es.Expr)
		got := "?"
		if !c.Unanswered(ty) {
			got = ty.String()
		}
		if got != tc.want {
			t.Errorf("%s: got %s, want %s", tc.src, got, tc.want)
		}
	}
}
