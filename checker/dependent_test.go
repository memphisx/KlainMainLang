package checker_test

import (
	"testing"

	"KlainMainLang/ast"
	"KlainMainLang/binder"
	"KlainMainLang/checker"
	"KlainMainLang/lib"
	"KlainMainLang/parser"
)

// typeOfLastRead is the type of the last read of name in src.
func typeOfLastRead(t *testing.T, src, name string) string {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("%s: %v", src, err)
	}
	lp, err := lib.Programs()
	if err != nil {
		t.Fatal(err)
	}
	c := checker.New(binder.BindWith(prog, binder.Options{Lib: lp}))
	var last *ast.Identifier
	ast.Inspect(prog, func(n ast.Node) bool {
		if id, ok := n.(*ast.Identifier); ok && id.Name == name {
			last = id
		}
		return true
	})
	ty := c.TypeOf(last)
	if c.Unanswered(ty) {
		return "?"
	}
	return ty.String()
}

// A destructured union's leaves are its members' properties, and the leaves
// of a const or a never-reassigned parameter narrow together by a test of
// the discriminant leaf (TypeScript 4.6's dependent destructuring).
func TestDependentDestructuring(t *testing.T) {
	u := "declare const u: { done: false, value: number } | { done: true, value: string }\n"
	k := "type S = { kind: 'a', v: number } | { kind: 'b', v: string } | { kind: 'c', w: boolean, v: boolean }\n"
	for _, tc := range []struct{ src, name, want string }{
		{u + "const { done, value } = u\nvalue", "value", "string | number"},
		{u + "const { done, value } = u\nif (!done) { value }", "value", "number"},
		{u + "const { done, value } = u\nif (done === true) { value }", "value", "string"},
		{u + "const { done, value } = u\nif (done) {} else { value }", "value", "number"},
		{u + "const { done, value } = u\nif (done) { throw 0 }\nvalue", "value", "number"},
		{u + "const { done, value } = u\nif (typeof value === 'string') { value }", "value", "string"},
		{u + "const { done, value } = u\nconst h = () => { if (!done) { value } }", "value", "number"},
		{u + "let { done, value } = u\nif (!done) { value }", "value", "string | number"}, // a let leaf can change
		{k + "function f({ kind, v }: S) { if (kind === 'a') { v } }", "v", "number"},
		{k + "function f({ kind, v }: S) { switch (kind) { case 'b': return v } }", "v", "string"},
		{k + "function f({ kind, v }: S) { kind = 'a'; if (kind === 'a') { v } }", "v", "string | number | boolean"},
		{k + "declare function g(cb: (s: S) => void): void\ng(({ kind, v }) => { if (kind !== 'a') { v } })", "v", "string | boolean"},
		{"async function* gen() { yield 1; }\ngen().next().then(({ done, value }) => { if (!done) { value } })", "value", "number"},
	} {
		if got := typeOfLastRead(t, tc.src, tc.name); got != tc.want {
			t.Errorf("%q: got %s, want %s", tc.src, got, tc.want)
		}
	}
}

// `x instanceof Map` keeps the union's own Map instantiation (tsc's
// getNarrowedType through the type arguments), not the constructor's
// Map<any, any>.
func TestInstanceofKeepsInstantiation(t *testing.T) {
	src := "function hm(x: number | Promise<number> | Map<string, number>): string { if (x instanceof Map) { x; } return \"n\"; }"
	if got := typeOfLastRead(t, src, "x"); got != "Map<string, number>" {
		t.Fatalf("got %s", got)
	}
}

// A spread of a library iterable reads its iterator's element type:
// lib.dom.iterable's Headers/URLSearchParams iterators, and an interface
// extending Array (RegExpExecArray).
func TestSpreadLibraryIterables(t *testing.T) {
	for src, want := range map[string]string{
		"const p = new URLSearchParams(); const x = [...p.keys()]; x": "string[]",
		"const h = new Headers(); const x = [...h]; x":                "[string, string][]",
		"const m = /a/.exec('a')!; const x = [...m]; x":               "string[]",
	} {
		if got := typeOfLastRead(t, src, "x"); got != want {
			t.Errorf("%s: got %s, want %s", src, got, want)
		}
	}
}

// An object pattern's rest is the source's other properties, and a tuple
// read at a number index is the union of its elements (undefined too, for
// an optional element).
func TestObjectRestAndTupleIndex(t *testing.T) {
	for src, want := range map[string]string{
		"interface Rec { a: number; b: number; c: string }\nlet r: Rec = { a: 1, b: 2, c: 'three' }\nlet { a, ...x } = r\nx": "{ b: number; c: string; }",
		"declare const t: [number, string, boolean]; declare const i: number; const x = t[i]; x":                             "string | number | boolean",
		"declare const t: [number, string?]; declare const i: number; const x = t[i]; x":                                     "string | number | undefined",
		"var f = ({ aNumber = 12, ...x }) => { x; return aNumber }":                                                          "{ [x: string]: any; }",
	} {
		if got := typeOfLastRead(t, src, "x"); got != want {
			t.Errorf("%s: got %s, want %s", src, got, want)
		}
	}
}

// `this` in an object literal's getter is the literal: while the literal
// is typed, its snapshot (accessors left out, as methods are).
func TestLiteralGetterThis(t *testing.T) {
	src := "const o = { index: 0, get val() { const x = this.index; x; return 1 } }"
	if got := typeOfLastRead(t, src, "x"); got != "number" {
		t.Errorf("got %s, want number", got)
	}
}

// An assignment narrows a declared union of objects by the full relation,
// type arguments included (tsc's getAssignmentReducedType).
func TestAssignmentReducesGenericUnion(t *testing.T) {
	for src, want := range map[string]string{
		"function f1(s: Set<string> | Set<number>) { s = new Set<number>(); s; }": "Set<number>",
		"function f1(s: string[] | number[]) { s = [1]; s; }":                     "number[]",
	} {
		if got := typeOfLastRead(t, src, "s"); got != want {
			t.Errorf("%s: got %s, want %s", src, got, want)
		}
	}
}

// A catch clause is entered from any assignment inside its try block too.
func TestCatchSeesTryAssignments(t *testing.T) {
	src := "type State = { tag: \"one\" } | { tag: \"two\" } | { tag: \"three\" };\nfunction f() { let state: State = { tag: 'one' }; try { state = { tag: 'two' }; try { state = { tag: 'three' } } finally {} } catch (e) { state; } }"
	if got, want := typeOfLastRead(t, src, "state"), `{ tag: "one"; } | { tag: "two"; } | { tag: "three"; }`; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}
