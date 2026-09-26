package checker_test

import (
	"strings"
	"testing"
	"time"

	"KlainMainLang/ast"
	"KlainMainLang/binder"
	"KlainMainLang/checker"
	"KlainMainLang/parser"
)

// typeOfProbe returns the type of the identifier statement `probe`'s operand:
// the last expression statement in src, at any depth.
func typeOfProbe(t *testing.T, src string) string {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("%s: %v", src, err)
	}
	c := checker.New(binder.Bind(prog))
	var last ast.Expression
	ast.Inspect(prog, func(n ast.Node) bool {
		if es, ok := n.(*ast.ExpressionStatement); ok {
			last = es.Expr
		}
		return true
	})
	if last == nil {
		t.Fatalf("%s: no expression statement", src)
	}
	ty := c.TypeOf(last)
	if c.Unanswered(ty) {
		return "?"
	}
	return ty.String()
}

const shapes = "type Sq = { kind: 'sq', side: number; }\ntype Ci = { kind: 'ci', r: number; }\ndeclare const s: Sq | Ci\n"
const classes = "class A { n = 1 }\nclass B { n = 2; b = true }\nfunction isB(v: A | B | undefined): v is B { return v instanceof B }\nlet a: A | B | undefined = new A()\nfunction g(): A | B | undefined { return a }\na = g()\n"

const zoo = "class Animal { legs = 4 }\nclass Dog extends Animal { bark(): string { return 'w' } }\nclass Cat extends Animal { }\nfunction pick(): Dog | Cat { return new Cat() }\nlet a: Animal = new Animal()\na = pick()\n"

const opt = "interface P { s?: string | null; n?: number }\ndeclare const p: P\n"

const box = "declare function mk(): { v: string | number | undefined, in: { w: number | undefined } }\nlet b = mk()\n"

func TestNarrowing(t *testing.T) {
	const decl = "declare const c: boolean\nlet x: string | number | undefined = f()\nfunction f(): string | number | undefined { return 1 }\n"
	for _, tc := range []struct{ src, want string }{
		{decl + "x", "string | number | undefined"},
		{decl + "if (typeof x === 'string') { x }", "string"},
		{decl + "if (typeof x !== 'string') { x }", "number | undefined"},
		{decl + "if (typeof x === 'string') { } else { x }", "number | undefined"},
		{decl + "if (x !== undefined) { x }", "string | number"},
		{decl + "if (x != null) { x }", "string | number"},
		{decl + "if (x === undefined) { x }", "undefined"},
		{decl + "if (x) { x }", "string | number"},
		{decl + "if (!x) { x }", "string | number | undefined"},
		{decl + "if (x === 1) { x }", "1"}, // replacePrimitivesWithLiterals
		{decl + "if (typeof x === 'number' || typeof x === 'string') { x }", "string | number"},
		{decl + "if (x && typeof x === 'number') { x }", "number"},
		{decl + "typeof x === 'number' ? x : 0", "number"},
		{decl + "x = 'a'\nx", "string"},
		{decl + "x = 1\nif (c) { x = 'a' }\nx", "string | number"},
		{decl + "if (typeof x === 'string') { x = 1 }\nx", "number | undefined"},
		{decl + "if (typeof x !== 'number') { throw 1 }\nx", "number"},
		{decl + "while (typeof x === 'string') { x }", "string"},
		{decl + "x = 1\nwhile (c) { x }", "number"},
		{decl + "x = 1\nwhile (c) { x; x = 'a' }\nx", "string | number"},
		{decl + "x = 1\nfor (let i = 0; i < 3; i++) { x = x + 1 }\nx", "number"},
		// A closure sees the narrowed type when nothing assigns the variable
		// after the read (TypeScript 5.4); otherwise the declared type, as
		// the variable may change before it runs.
		{decl + "if (typeof x === 'string') { const g = () => x; g() }", "string"},
		{decl + "if (typeof x === 'string') { const g = () => x; g() }\nlet q = (x = 1)", "string | number | undefined"},
		{decl + "if (typeof x === 'string') { const g = () => { x = 1 }; const h = () => x; h() }", "string | number | undefined"},
		{"declare function f(): string | undefined\nconst k = f()\nif (k) { const g = function () { return () => k }; g()() }", "string"},
		{"declare function f(): string | undefined\nconst k = f()\nif (k) { function g() { return k } g() }", "string | undefined"},
		{"let u: unknown = 1\nif (typeof u === 'number') { u }", "number"},
		{"let v: string | undefined\nv", "string | undefined"},
		{"let w: string | number = 1\nw", "number"},
		{shapes + "if (s.kind === 'sq') { s }", "{ kind: \"sq\"; side: number; }"},
		{shapes + "if (s.kind !== 'sq') { s }", "{ kind: \"ci\"; r: number; }"},
		{shapes + "if (s.kind === 'sq') { s.side }", "number"},
		{shapes + "if ('r' in s) { s }", "{ kind: \"ci\"; r: number; }"},
		{shapes + "if (!('r' in s)) { s }", "{ kind: \"sq\"; side: number; }"},
		{shapes + "s.kind", "\"sq\" | \"ci\""},
		{classes + "if (a instanceof A) { a }", "A"},
		{classes + "if (a instanceof A) { } else { a }", "B | undefined"},
		{classes + "if (isB(a)) { a }", "B"},
		{classes + "if (!isB(a)) { a }", "A | undefined"},
		{classes + "if (a?.n === 1) { a }", "A | B"},
		{shapes + "switch (s.kind) { case 'sq': s; break }", "{ kind: \"sq\"; side: number; }"},
		{shapes + "switch (s.kind) { case 'sq': break; default: s }", "{ kind: \"ci\"; r: number; }"},
		{shapes + "switch (s.kind) { case 'sq': case 'ci': s }", "{ kind: \"sq\"; side: number; } | { kind: \"ci\"; r: number; }"},
		{decl + "switch (typeof x) { case 'number': x }", "number"},
		{decl + "switch (typeof x) { case 'number': break; case 'string': break; default: x }", "undefined"},
		{decl + "switch (x) { case undefined: x }", "undefined"},
		{zoo + "if (a instanceof Dog) { a }", "Dog"},
		{zoo + "if (a instanceof Dog) { a.bark() }", "string"},
		{zoo + "if (a instanceof Dog) { a.legs }", "number"},
		{zoo + "if (a instanceof Dog) { } else { a }", "Animal"},
		{zoo + "let d: Dog | Cat = new Dog()\nd = pick()\nif (d instanceof Animal) { d }", "Dog | Cat"},
		{decl + "function isStr(v: unknown): asserts v is string { }\nisStr(x)\nx", "string"},
		{decl + "function ok(v: unknown): asserts v { }\nok(x)\nx", "string | number"},
		{decl + "function ok(v: unknown): asserts v { }\nok(typeof x === 'number')\nx", "number"},
		{box + "if (typeof b.v === 'string') { b.v }", "string"},
		{box + "if (b.v !== undefined) { b.v }", "string | number"},
		{box + "if (typeof b.v === 'string') { b.v = 1; b.v }", "number"},
		{box + "if (typeof b.v === 'string') { b = { v: 2, in: { w: 1 } }; b.v }", "string | number | undefined"},
		{box + "if (b.in.w) { b.in.w }", "number"},
		{box + "if (b.in.w) { b.in = { w: undefined }; b.in.w }", "number | undefined"},
		{box + "if (typeof b.v === 'string') { const g = () => b.v; g() }", "string | number | undefined"},
		{box + "if (typeof b['v'] === 'string') { b.v }", "string"},
		{box + "if (typeof b.v === 'string') { b['v'] = 1; b.v }", "number"},
		{"declare function tp(): [string | undefined, number]\nlet p = tp()\nif (p[0]) { p[0] }", "string"},
		{"try { } catch (e) { e }", "unknown"},
		{"try { } catch (e) { if (typeof e === 'string') { e } }", "string"},
		{"let e\ne", "undefined"},
		{"let e\ne = 1\ne", "number"},
		{"declare const c: boolean\nlet e\nif (c) { e = 'a' }\ne", "string | undefined"},
		{"declare const c: boolean\nlet e\nif (c) { e = 'a' } else { e = 2 }\ne", "string | number"},
		{"declare const c: boolean\nlet e\ne = 1\nwhile (c) { e = 's' }\ne", "string | number"},
		// A discriminant needs a literal type in some member (tsc's
		// isDiscriminantProperty); undefined and an absent optional property
		// are the same unit.
		{"type A = { k: 'a', x?: number }\ntype B = { k: 'b', x: string; }\ndeclare const v: A | B\nif (v.x === undefined) { v }", "{ k: \"a\"; x?: number | undefined; } | { k: \"b\"; x: string; }"},
		{"type A = { k: 'a', x?: 'p' }\ntype B = { k: 'b', x: 'q' }\ndeclare const v: A | B\nif (v.x === undefined) { v }", "{ k: \"a\"; x?: \"p\" | undefined; }"},
		{"type A = { k: 'a', x?: 'p' }\ntype B = { k: 'b', x: 'q' }\ndeclare const v: A | B\nif (v.x !== 'q') { v }", "{ k: \"a\"; x?: \"p\" | undefined; }"},
		{"type C = { t: boolean | undefined, c: 1; }\ntype D = { t: string, d: 1 }\ndeclare const w: C | D\nif (w.t === undefined) { w }", "{ t: boolean | undefined; c: 1; }"},
		{opt + "if (p.s === undefined) { p.s }", "undefined"},
		{opt + "if (p.s === undefined) { } else { p.s }", "string | null"},
	} {
		if got := typeOfProbe(t, tc.src); got != tc.want {
			t.Errorf("%q: got %s, want %s", tc.src, got, tc.want)
		}
	}
}

// TestFlowCost: shapes that make an uncached flow walk exponential
// (TypeScript's own controlFlowCaching and controlFlowSelfReferentialLoop
// repros) check in bounded time.
func TestFlowCost(t *testing.T) {
	chain := []string{"a=FF(a,b,c,d);", "d=FF(d,a,b,c);", "c=FF(c,d,a,b);", "b=FF(b,c,d,a);"}
	var body []string
	for i := 0; i < 64; i++ {
		body = append(body, chain[i%4])
	}
	srcs := []string{
		"function md5(): void {\nfunction FF(a,b,c,d) { return 0 }\nvar a,b,c,d;\na=0;b=0;c=0;d=0;\nfor (let k=0;k<1;k++) {\n" + strings.Join(body, "\n") + "\n}\n}",
		"enum E { A, B }\nconst e: E = E.A\nconst one = E.A\nwhile (true) {\n" + strings.Repeat("if (e === one) {}\n", 200) + "}",
		// A call through a narrowable object inside a loop: checking it as a
		// possible assertion must not narrow its own callee (a walk that
		// meets the same call again).
		"interface C { list: string[]; n: number }\ndeclare const argv: string[]\nfunction f(): C {\nconst cfg: C = { list: [], n: 0 }\nlet i = 0\nwhile (i < argv.length) {\n" +
			strings.Repeat("if (argv[i] === 'x') { cfg.list.push(argv[i]); i += 1 } else ", 30) + "{ i += 1 }\n}\nreturn cfg\n}",
	}
	for _, src := range srcs {
		prog, err := parser.Parse(src)
		if err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		checker.New(binder.Bind(prog)).Check()
		if d := time.Since(start); d > 5*time.Second {
			t.Errorf("checking took %v", d)
		}
	}
}
