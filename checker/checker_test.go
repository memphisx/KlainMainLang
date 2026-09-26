package checker_test

import (
	"testing"

	"KlainMainLang/ast"
	"KlainMainLang/binder"
	"KlainMainLang/checker"
	"KlainMainLang/parser"
)

// typeOfLast checks src and returns the type of the expression in its last
// statement, printed as TypeScript prints it ("?" when not modelled).
func typeOfLast(t *testing.T, src string) string {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("%s: %v", src, err)
	}
	c := checker.New(binder.Bind(prog))
	es, ok := prog.Body[len(prog.Body)-1].(*ast.ExpressionStatement)
	if !ok {
		t.Fatalf("%s: last statement is not an expression", src)
	}
	ty := c.TypeOf(es.Expr)
	if c.Unanswered(ty) {
		return "?"
	}
	return ty.String()
}

func TestTypeOf(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{"1.50", "1.5"},
		{"0x10", "16"},
		{"'a'", `"a"`},
		{"10n", "10n"},
		{"const a = 1\na", "1"},
		{"let b = 1\nb", "number"},
		{"let s = 'x'\ns + 1", "string"},
		{"1 + 2", "number"},
		{"declare const d: { x: number }\nd - 1", "?"},
		{"let q: any = 1\nq - 1", "number"},
		{"let q: any = 1\nq.a.b", "any"},
		{"let q: any = 1\nq?.a", "any"},
		{"let q: any = 1\nq[0]", "any"},
		{"let q: any = 1\nq(1)", "any"},
		{"let q: any = 1\nq.m(1)", "any"},
		{"1n * 2n", "bigint"},
		{"1 < 2", "boolean"},
		{"!0", "boolean"},
		{"typeof 1", `"bigint" | "boolean" | "function" | "number" | "object" | "string" | "symbol" | "undefined"`},
		{"let c = true\nc ? 1 : 'x'", `1 | "x"`},
		// n is narrowed to number by its initializer: never nullish, so `??`
		// is its left side (tsc's checkNullishCoalescing).
		{"let n: number | undefined = 1\nn ?? 'd'", `number`},
		{"declare let m: number | undefined\nm ?? 'd'", `number | "d"`},
		{"let o = { a: 1, b: 'x' }\no.b", "string"},
		{"let xs = [1, 2]\nxs", "number[]"},
		{"let xs = [1, 2]\nxs[0]", "number"},
		{"let xs = [1, 2]\nxs.length", "number"},
		{"function f(a: number, b?: string): string { return '' }\nf(1)", "string"},
		{"function g(a: number): boolean { return true }\ng", "(p0: number) => boolean"},
		{"function k(p: number) { return p }\nk", "(p0: number) => number"},
		{"function r(c: boolean) { if (c) { return 1 } return 'x' }\nr(true)", "1 | \"x\""},
		{"function v() { }\nv()", "void"},
		{"function mb(c: boolean) { if (c) return; return 2 }\nmb(true)", "2 | undefined"},
		{"const sq = (n: number) => n * n\nsq(3)", "number"},
		{"function me(c: boolean) { if (c) return 2 }\nme(true)", "2 | undefined"},
		{"const [d0, d1] = [1, 2]\nd1", "number"},
		{"interface Named { name: string }\ninterface Aged { age: number }\ninterface P extends Named, Aged { id: bigint }\ndeclare const p: P\np.name", "string"},
		{"interface Named { name: string }\ninterface P extends Named { id: bigint }\ndeclare const p: P\np.id", "bigint"},
		{"const t: [string, number] = ['a', 1]\nconst [t0, t1] = t\nt0", "string"},
		{"const t: [string, number, boolean] = ['a', 1, true]\nconst [, ...tr] = t\ntr", "[number, boolean]"},
		{"const o: { a?: number, b: string } = { b: 'x' }\nconst { a = 'z', b: bb } = o\na", "number | \"z\""},
		{"const o: { a?: number, b: string } = { b: 'x' }\nlet { a = 'z' } = o\na", "string | number"},
		{"const n: { p: { q: [number] } } = { p: { q: [1] } }\nconst { p: { q: [qq] } } = n\nqq", "number"},
		{"function mt(c: boolean) { if (c) return 2; throw 1 }\nmt(true)", "number"},
		{"function ml(c: boolean) { while (true) { if (c) return 'a' } }\nml(true)", "string"},
		{"function rec(n: number) { return n ? rec(n - 1) : 0 }\nrec", "?"},
		{"class P { x: number = 0; y = 'a'; m(): number { return 1 } }\nnew P().y", "string"},
		{"class P { x: number = 0; m(): number { return 1 } }\nnew P().m()", "number"},
		{"interface Pt { x: number; y?: string }\nlet p: Pt = { x: 1 }\np.y", "string | undefined"},
		{"type Pair = [number, string]\nlet q: Pair = [1, 'a']\nq", "[number, string]"},
		{"/** @type {int32} */\nlet w = 5\nw", "int32"},
		{"let f32: float32 = 1\nf32", "float32"},
		{"console.log(1)", "?"},
		{"let z = unknownGlobal\nz", "?"},
		{"for (const v of [1, 2]) { v }\n0", "0"},
	} {
		if got := typeOfLast(t, c.src); got != c.want {
			t.Errorf("%q: %s, want %s", c.src, got, c.want)
		}
	}
}

// Every expression of every parseable corpus file is typed without a
// panic, and each is checked once (the memo answers the second asking).
func TestTypeOfWholeCorpus(t *testing.T) {
	n := 0
	for _, f := range corpusFiles(t) {
		prog, err := parser.Parse(f)
		if err != nil {
			continue
		}
		c := checker.New(binder.Bind(prog))
		ast.Inspect(prog, func(node ast.Node) bool {
			if e, ok := node.(ast.Expression); ok {
				a := c.TypeOf(e)
				if c.TypeOf(e) != a {
					t.Fatalf("TypeOf is not memoised")
				}
			}
			return true
		})
		n++
	}
	if n < 300 {
		t.Fatalf("only %d files", n)
	}
}

func corpusFiles(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, pattern := range []string{"../examples/*/*.ts", "../examples/*/*/*.ts", "../apps/*/*.ts"} {
		matches, _ := globFiles(pattern)
		out = append(out, matches...)
	}
	return out
}

// TestMissing: an omitted optional property is the missing type, printed and
// narrowed as undefined but distinct from the undefined a value or an
// annotation names (TDD-00230 P2.5).
func TestMissing(t *testing.T) {
	const opt = "interface P { s?: string | null; u?: number | undefined }\ndeclare const p: P\nclass K { f?: boolean }\ndeclare const t: { a?: string }\n"
	for _, tc := range []struct {
		src, want string
		missing   bool // the type has a missing member
		undef     bool // the type has an explicit undefined member
	}{
		{opt + "p.s", "string | null | undefined", true, false},
		{opt + "p.u", "number | undefined", true, true},
		{opt + "new K().f", "boolean | undefined", true, false},
		{opt + "t.a", "string | undefined", true, false},
		{opt + "const { s = 1 } = p\ns", "string | 1 | null", false, false},
		{opt + "const { u = 1 } = p\nu", "number", false, false},
		{opt + "p.s ?? 'd'", "string", false, false},
		{"function f(x?: number) { return x }\nf()", "number | undefined", false, true},
		// Assignment reduction keeps the declared type's own members.
		{opt + "let v: number | undefined = p.u\nv", "number | undefined", false, true},
	} {
		prog, err := parser.Parse(tc.src)
		if err != nil {
			t.Fatalf("%s: %v", tc.src, err)
		}
		c := checker.New(binder.Bind(prog))
		ty := c.TypeOf(prog.Body[len(prog.Body)-1].(*ast.ExpressionStatement).Expr)
		if c.Unanswered(ty) {
			t.Errorf("%q: unanswered", tc.src)
			continue
		}
		missing, undef := false, false
		for _, m := range append([]*checker.Type{ty}, ty.Types...) {
			if m.IsMissing() {
				missing = true
			} else if m.Flags&checker.Undefined != 0 {
				undef = true
			}
		}
		if ty.String() != tc.want || missing != tc.missing || undef != tc.undef {
			t.Errorf("%q: got %s (missing %v, undefined %v), want %s (missing %v, undefined %v)",
				tc.src, ty, missing, undef, tc.want, tc.missing, tc.undef)
		}
	}
}
