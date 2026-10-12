package checker_test

import (
	"testing"

	"KlainMainLang/ast"
	"KlainMainLang/binder"
	"KlainMainLang/checker"
	"KlainMainLang/lib"
	"KlainMainLang/options"
	"KlainMainLang/parser"
)

// A union of several generic members (`TResult1 | PromiseLike<TResult1>`)
// infers each source member against the member it matches, as tsc's
// inferToMultipleTypes: a callback returning a Promise<number> gives
// `then` a Promise<number>.
func TestInferThroughPromiseLikeUnion(t *testing.T) {
	libProgs, err := lib.Programs()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ src, want string }{
		{"declare const pn: Promise<number>;\nconst r = Promise.resolve(1).then(() => pn);\nr", "Promise<number>"},
		{"declare function g(n: number): Promise<number>;\nconst p = Promise.resolve(20).then((n: number) => g(n));\np", "Promise<number>"},
		{"const q = Promise.resolve(1).then(() => 22);\nq", "Promise<number>"},
	} {
		prog, err := parser.Parse(c.src)
		if err != nil {
			t.Fatal(err)
		}
		ck := checker.NewWith(binder.BindWith(prog, binder.Options{Lib: libProgs}), options.Options{})
		es := prog.Body[len(prog.Body)-1].(*ast.ExpressionStatement)
		if got := ck.TypeOf(es.Expr).String(); got != c.want {
			t.Errorf("%s: got %s, want %s", c.src, got, c.want)
		}
	}
}
