package checker_test

import (
	"strings"
	"testing"

	"KlainMainLang/ast"
	"KlainMainLang/binder"
	"KlainMainLang/checker"
	"KlainMainLang/lib"
	"KlainMainLang/parser"
)

// TestInferFromIndexSignature: a type argument inferred from an index
// signature (`ArrayLike<T>` from a typed array, a subclass of one
// included), as tsc's inferFromIndexTypes.
func TestInferFromIndexSignature(t *testing.T) {
	src := "const u = new Uint8Array(2);\nfunction g<T>(x: ArrayLike<T>): T { return x[0]; }\n" +
		"g(u);\nArray.from(u);\nArray.from(u, b => b + 1);\n" +
		"class Bytes extends Uint8Array { hex(): string[] { return Array.from(this, b => b.toString(16)); } }\n"
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	lp, err := lib.Programs()
	if err != nil {
		t.Fatal(err)
	}
	c := checker.New(binder.BindWith(prog, binder.Options{Lib: lp}))
	for _, d := range c.Check() {
		t.Errorf("diagnostic: %v", d)
	}
	var got []string
	ast.Inspect(prog, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpression); ok {
			if id, ok := call.Callee.(*ast.Identifier); ok && id.Name == "g" {
				got = append(got, c.TypeOf(call).String())
			}
			if m, ok := call.Callee.(*ast.MemberExpression); ok && m.Property == "from" {
				got = append(got, c.TypeOf(call).String())
			}
		}
		return true
	})
	want := []string{"number", "number[]", "number[]", "string[]"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("call %d: got %s, want %s", i, got[i], want[i])
		}
	}
}

// TestInferUncandidatedTypeParams: a type parameter nothing infers is
// unknown (tsc's getInferredType), fixed before a callback that takes it
// as a parameter is typed; an overload set of `declare function`s infers
// through each signature's own type parameters.
func TestInferUncandidatedTypeParams(t *testing.T) {
	src := "declare function ff<A>(a: ArrayLike<A>): A[];\ndeclare function ff<B>(a: Iterable<B> | ArrayLike<B>): B[];\n" +
		"declare function h<T, U>(x: ArrayLike<T>, f: (v: T, k: number) => U): U[];\n" +
		"ff({ length: 2 });\nh({ length: 2 }, (_, i) => i * 2);\n" +
		"Array.from({ length: 5 }, (_, i) => i * i);\nArray.from({ length: 3 }, (_, i) => 'r' + i);\nArray.from({ length: 2 });\n"
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	lp, err := lib.Programs()
	if err != nil {
		t.Fatal(err)
	}
	c := checker.New(binder.BindWith(prog, binder.Options{Lib: lp}))
	for _, d := range c.Check() {
		t.Errorf("diagnostic: %v", d)
	}
	var got []string
	for _, st := range prog.Body {
		if es, ok := st.(*ast.ExpressionStatement); ok {
			got = append(got, c.TypeOf(es.Expr).String())
		}
	}
	want := []string{"unknown[]", "number[]", "number[]", "string[]", "unknown[]"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestInferFromIterables: Iterable<T> inferred from what iterating the
// argument yields (a Map's entries, a Set's elements, an object's own
// `[Symbol.iterator]`), and an overload whose inference left a library
// argument's type parameter without a candidate yields to one that infers
// it.
func TestInferFromIterables(t *testing.T) {
	src := "const m = new Map<string, number>();\nconst s = new Set<number>();\n" +
		"const o = { *[Symbol.iterator]() { yield 4; yield 9 } };\n" +
		"Array.from(m);\nArray.from(s);\nArray.from(m.values());\nArray.from(o, (x) => x * 2);\n"
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	lp, err := lib.Programs()
	if err != nil {
		t.Fatal(err)
	}
	c := checker.New(binder.BindWith(prog, binder.Options{Lib: lp}))
	for _, d := range c.Check() {
		t.Errorf("diagnostic: %v", d)
	}
	var got []string
	for _, st := range prog.Body {
		if es, ok := st.(*ast.ExpressionStatement); ok {
			got = append(got, c.TypeOf(es.Expr).String())
		}
	}
	want := "[string, number][] number[] number[] number[]"
	if strings.Join(got, " ") != want {
		t.Errorf("got %v, want %s", got, want)
	}
}
