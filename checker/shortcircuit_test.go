package checker_test

import (
	"strings"
	"testing"

	"KlainMainLang/ast"
	"KlainMainLang/binder"
	"KlainMainLang/checker"
	"KlainMainLang/lib"
	"KlainMainLang/options"
	"KlainMainLang/parser"
)

// typeAndCodes is the type of src's last expression statement and, in the
// strict lane, the diagnostics' line:code list, under compat ("" strict,
// "js").
func typeAndCodes(t *testing.T, src, compat string) (string, string) {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("%s: %v", src, err)
	}
	lp, err := lib.Programs()
	if err != nil {
		t.Fatal(err)
	}
	c := checker.NewWith(binder.BindWith(prog, binder.Options{AnnexB: true, Lib: lp}), options.Options{Compat: compat})
	es := prog.Body[len(prog.Body)-1].(*ast.ExpressionStatement)
	ty := "?"
	if t := c.TypeOf(es.Expr); !c.Unanswered(t) {
		ty = t.String()
	}
	var out []string
	if compat == "js" {
		return ty, "" // the js lane is not type-checked (resolver.TypeCheck)
	}
	for _, d := range c.Check() {
		out = append(out, strings.TrimSpace(strings.Split(d.Error(), ":")[0])+":TS"+itoa(d.Code()))
	}
	return ty, strings.Join(out, " ")
}

// An optional chain on a value only ever null or undefined always
// short-circuits: a member or call of never is tsc's error type (TS2339,
// TS2349), an element undefined. A logical assignment reads its target as
// declared; any compound assignment to an evolving implicit any is any.
// Each expectation from tsc 5.9 --strict (the js lane reports no error).
func TestShortCircuitAndLogicalAssignment(t *testing.T) {
	for _, tc := range []struct{ src, typ, codes string }{
		{"const a = undefined\na?.b", "any", "2:TS2339"},
		{"const a = undefined\na?.b.c", "any", "2:TS2339"},
		{"undefined?.b", "any", "1:TS2339"},
		{"const a = undefined\nlet x = 1\na?.[++x]", "undefined", ""},
		{"const a = undefined\na?.()", "any", "2:TS2349"},
		{"let x = null\nlet o = { [x ??= 1]: 2 }\nx ??= 1", "any", ""},
		{"let s\ns ??= 'a'", "any", ""},
		{"let w: number | null = null\nw ??= 1", "number", ""},
		{"let n: 1 | 2 | null = null\nn ??= 1", "1 | 2", ""},
		{"let v: string | number = 5\nv += 1", "number", ""},
		// An object, array or function key is no property key (TS2538): the
		// access is any, whatever the receiver.
		{"var base = {}\nvar prop = { toString() { return 1 } }\nbase[prop]", "any", "3:TS2538"},
		{"var prop = { toString() { return 1 } }\ndeclare const v: any\nv[prop]", "any", "3:TS2538"},
		{"const m: Record<string, number> = {}\nm[[1]]", "any", "2:TS2538"},
		// A symbol key: TS7053 where there is no index signature, TS2538
		// where there are only string or number ones.
		{"let k = Symbol('k')\nlet o = {}\no[k] = 1", "1", "3:TS7053"},
		{"let k = Symbol('k')\nconst q: { a: number } = { a: 1 }\nq[k]", "any", "3:TS7053"},
		{"let k = Symbol('k')\nconst r: Record<string, number> = {}\nr[k]", "any", "3:TS2538"},
		{"const u = Symbol('u')\nconst r: Record<string, number> = {}\nr[u]", "any", "3:TS2538"},
	} {
		for _, compat := range []string{"", "js"} {
			want := tc.codes
			if compat == "js" {
				want = ""
			}
			ty, codes := typeAndCodes(t, tc.src, compat)
			if ty != tc.typ || codes != want {
				t.Errorf("[%s] %q: got %s %q, want %s %q", compat, tc.src, ty, codes, tc.typ, want)
			}
		}
	}
}

// An immediately invoked function's parameter takes its argument's type
// whichever node is typed first: a read of the parameter in the body, or
// the argument's literal (whose freshness asks for its context), must not
// type the function while its parameter is being typed.
func TestIIFEParamOrderIndependent(t *testing.T) {
	src := "let s = Symbol('k');\nlet p = {};\n(function(obj) { console.log(obj.c, obj[s]) }({...p, c: 4}));\n"
	for _, first := range []string{"function", "param", "index", "argument"} {
		prog, err := parser.Parse(src)
		if err != nil {
			t.Fatal(err)
		}
		lp, err := lib.Programs()
		if err != nil {
			t.Fatal(err)
		}
		c := checker.NewWith(binder.BindWith(prog, binder.Options{AnnexB: true, Lib: lp}), options.Options{})
		call := prog.Body[2].(*ast.ExpressionStatement).Expr.(*ast.CallExpression)
		fe := call.Callee.(*ast.FunctionExpression)
		ix := fe.Body.Body[0].(*ast.ExpressionStatement).Expr.(*ast.CallExpression).Args[1].(*ast.IndexExpression)
		switch first {
		case "param":
			c.TypeOf(ix.Object)
		case "index":
			c.TypeOf(ix)
		case "argument":
			c.TypeOf(call.Args[0])
		}
		if got := c.TypeOf(fe); c.Unanswered(got) || got.String() != "(obj: { c: number; }) => void" {
			t.Errorf("%s typed first: function is %s", first, got)
		}
	}
}
