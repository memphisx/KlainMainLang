package checker_test

import (
	"KlainMainLang/lib"
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

// typeOfLastLib is typeOfLast with the builtin declarations bound around
// the program, as a compile binds them.
func typeOfLastLib(t *testing.T, src string) string {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("%s: %v", src, err)
	}
	libProgs, err := lib.Programs()
	if err != nil {
		t.Fatal(err)
	}
	c := checker.New(binder.BindWith(prog, binder.Options{Lib: libProgs}))
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
		{"declare const a: any\n({ ...a, y: 1 })", "any"}, // spreading an any (getSpreadType)
		{"class S { h = false }\nclass T extends S { t = 1 }\ndeclare const s: S\ndeclare const h: boolean\nif (h || s instanceof T) {}\ns", "S"}, // a join reduces the subtype instanceof injected
		// An element access by a well-known symbol: the member declared
		// with it.
		{"const o = { [Symbol.iterator]() { return \"s\"; } }\no[Symbol.iterator]()", "string"},
		// A call through a union of functions with identical parameters:
		// the union of their results.
		{"declare const u: ((x: number) => string) | ((x: number) => boolean)\nu(1)", "string | boolean"},
		// `typeof` of an object's property: the property's type.
		{"const o = { a: 1, b: \"x\" }\nlet y: typeof o.b = \"q\"\ny", "string"},
		// A generic construction nested in another's arguments: typing the
		// inner one's context infers the outer's arguments, the inner among
		// them, which does not start over.
		{"declare var Box: { new <T>(v: T): { v: T } }\nconst b = new Box(new Box(1))\nb", "{ v: { v: number; }; }"},
		// A function read inside its own return: itself, with an any result.
		{"function f(a: number) { return f; }\nf", "(a: number) => (a: number) => any"},
		{"0x10", "16"},
		// An empty element is never[], which the element type's subtype
		// reduction drops beside another array.
		{"const ragged = [[1, 2, 3], [4], [], [5, 6]]\nragged", "number[][]"},
		{"const p = [[], []]\np", "never[][]"},
		// So does a conditional's empty branch beside an array.
		{"declare const c: boolean\ndeclare const s: string[]\nconst h = c ? [] : s\nh", "string[]"},
		{"declare const c: boolean\nconst h = c ? [] : []\nh", "never[]"},
		{"'a'", `"a"`},
		{"10n", "10n"},
		{"const a = 1\na", "1"},
		{"let b = 1\nb", "number"},
		{"let s = 'x'\ns + 1", "string"},
		{"1 + 2", "number"},
		{"declare const d: { x: number }\nd - 1", "number"}, // TS2362, still a number
		{"let q: any = 1\nq - 1", "number"},
		{"let q: any = 1\nq.a.b", "any"},
		{"let q: any = 1\nq?.a", "any"},
		// An optional chain reads its receiver present and adds undefined
		// where it can short-circuit (tsc's checkPropertyAccessChain).
		{"declare const a: { b: { c: number }, s?: string, f?: (x: number) => string, arr: number[] } | undefined\na?.b.c", "number | undefined"},
		{"declare const a: { b: { c: number }, s?: string, f?: (x: number) => string, arr: number[] } | undefined\na?.s", "string | undefined"},
		{"declare const a: { b: { c: number }, s?: string, f?: (x: number) => string, arr: number[] } | undefined\na?.f?.(1)", "string | undefined"},
		{"declare const a: { b: { c: number }, s?: string, f?: (x: number) => string, arr: number[] } | undefined\na?.arr[0]", "number | undefined"},
		{"declare const n: { b: number }\nn?.b", "number"},
		{"declare const g: ((x: number) => boolean) | null\ng?.(2)", "boolean | undefined"},
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
		{"function g(a: number): boolean { return true }\ng", "(a: number) => boolean"},
		{"function k(p: number) { return p }\nk", "(p: number) => number"},
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
		{"function rec(n: number) { return n ? rec(n - 1) : 0 }\nrec", "(n: number) => any"},
		{"class P { x: number = 0; y = 'a'; m(): number { return 1 } }\nnew P().y", "string"},
		{"class P { x: number = 0; m(): number { return 1 } }\nnew P().m()", "number"},
		{"interface Pt { x: number; y?: string }\nlet p: Pt = { x: 1 }\np.y", "string | undefined"},
		{"type Pair = [number, string]\nlet q: Pair = [1, 'a']\nq", "[number, string]"},
		{"/** @type {int32} */\nlet w = 5\nw", "int32"},
		{"let f32: float32 = 1\nf32", "float32"},
		{"console.log(1)", "?"},
		{"let z = unknownGlobal\nz", "?"},
		{"for (const v of [1, 2]) { v }\n0", "0"},
		// A var sharing a function declaration's name is typed by the function.
		{"function f1() { var x; return x; function x() { return 7 } }\nf1()", "() => number"},
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

// A for-of loop variable takes the iterable's element type, as
// iteratedType gives it: an any's element is any, a Map's an entry.
func TestForOfBindingElement(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{"declare const xs: any\nfor (const x of xs) { x }", "any"},
		{"declare const t: [number, string]\nfor (const x of t) { x }", "string | number"},
	} {
		prog, err := parser.Parse(tc.src)
		if err != nil {
			t.Fatalf("%s: %v", tc.src, err)
		}
		c := checker.New(binder.Bind(prog))
		var last ast.Expression
		ast.Inspect(prog, func(n ast.Node) bool {
			if es, ok := n.(*ast.ExpressionStatement); ok {
				last = es.Expr
			}
			return true
		})
		got := "?"
		if ty := c.TypeOf(last); !c.Unanswered(ty) {
			got = ty.String()
		}
		if got != tc.want {
			t.Errorf("%q: got %s, want %s", tc.src, got, tc.want)
		}
	}
}

// A call through an unannotated variable is no assertion (tsc's
// getEffectsSignature, TS2775): a loop holding `b.cb(s…)` leaves s its
// declared type instead of walking back through b's initializer into the
// loop itself.
func TestCallThroughUnannotatedVariableIsNoAssertion(t *testing.T) {
	src := "interface B { cb: (e: any) => void }\nclass S { buffered: B[] = []; errored: any = null }\n" +
		"function eb(s: S): void {\n  while (true) { const b = s.buffered[0]; b.cb(s.errored ?? 1); }\n}"
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	c := checker.New(binder.Bind(prog))
	ast.Inspect(prog, func(n ast.Node) bool {
		if id, ok := n.(*ast.Identifier); ok && id.Name == "s" {
			if ty := c.TypeOf(id); c.Unanswered(ty) || ty.String() != "S" {
				t.Errorf("s at %d:%d: got %s, want S", id.GetPos().Line, id.GetPos().Col, ty)
			}
		}
		return true
	})
}

// A tuple of one rest element is its array, so a signature over one (the
// events module's `...args: Args<K, T>` overloads) is typed.
func TestSingleRestTupleIsArray(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{"type A = [...args: any[]];\ndeclare const x: A;\nx", "any[]"},
		{"type A = [...number[]];\ndeclare const x: A;\nx", "number[]"},
		{"class E<T> {\n  r<K>(n: string, ...a: T extends 1 ? [...a: any[]] : never): boolean;\n  r(n: string, ...a: any[]): boolean { return true }\n}\nconst e = new E<1>();\ne.r", "{ <K>(n: string, ...a: any[]): boolean }"},
	} {
		if got := typeOfLast(t, tc.src); got != tc.want {
			t.Errorf("%q: got %s, want %s", tc.src, got, tc.want)
		}
	}
}

// A class whose type parameters code generation erased (cleared, the list
// kept in Declared) is still generic to the checker: its overloads over T
// stay typed.
func TestCodegenErasedClassKeepsTypeParams(t *testing.T) {
	src := "class E<T = [never]> {\n  r<K>(n: T extends [never] ? string : K): this;\n  r(n: string): this { return this }\n}\nconst e = new E();\ne.r"
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	cd := prog.Body[0].(*ast.ClassDeclaration)
	declared := cd.DeclaredTypeParams()
	cd.Declared = &declared
	cd.TypeParams, cd.TypeParamConstraints, cd.TypeParamDefaults = nil, nil, nil
	c := checker.New(binder.Bind(prog))
	got := "?"
	if ty := c.TypeOf(prog.Body[len(prog.Body)-1].(*ast.ExpressionStatement).Expr); !c.Unanswered(ty) {
		got = ty.String()
	}
	if want := "{ <K>(n: string): E<[never]> }"; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

// A conditional type over a generic signature (`ReturnType<typeof
// setTimeout>`) is decided: the signature's own type parameters are not
// free.
func TestConditionalOverGenericSignature(t *testing.T) {
	h := "type RT<T extends (...args: any) => any> = T extends (...args: any) => infer R ? R : any;\ninterface Tm { x: number }\n"
	for _, tc := range []struct{ src, want string }{
		{h + "declare function st<A extends any[]>(cb: (...a: A) => void, d?: number, ...a: A): Tm;\ndeclare function st(cb: (_: void) => void, d?: number): Tm;\ndeclare const x: RT<typeof st>;\nx", "Tm"},
		{h + "declare function st<A extends any[]>(cb: (...a: A) => void, ...a: A): Tm;\ndeclare const x: RT<typeof st>;\nx", "Tm"},
		{h + "declare function id<T>(x: T): T;\ndeclare const x: RT<typeof id>;\nx", "unknown"},
	} {
		if got := typeOfLast(t, tc.src); got != tc.want {
			t.Errorf("%q: got %s, want %s", tc.src, got, tc.want)
		}
	}
}

// A member read on a receiver that may be null or undefined is typed from
// the non-nullable receiver, as tsc does after reporting the access.
func TestMemberOfPossiblyAbsentReceiver(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{"declare const o: {a: number} | undefined;\no.a", "number"},
		{"declare const o: {a: number} | null;\no.a", "number"},
		{"declare const o: {a: number} | undefined;\no?.a", "number | undefined"},
		{"declare const o: undefined;\no.a", "?"},
	} {
		if got := typeOfLast(t, tc.src); got != tc.want {
			t.Errorf("%q: got %s, want %s", tc.src, got, tc.want)
		}
	}
}

// A parameter default may itself be a compound assignment (`a = c += 1`):
// it is typed like any other expression.
func TestCompoundAssignmentInParameterDefault(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{"let c = 0;\nconst r = (a = c += 1) => a;\nr", "(a?: number | undefined) => number"},
		{"let c = 0;\nconst r = (a = c = 1) => a;\nr", "(a?: number | undefined) => number"},
	} {
		if got := typeOfLast(t, tc.src); got != tc.want {
			t.Errorf("%q: got %s, want %s", tc.src, got, tc.want)
		}
	}
}

// An unannotated destructured parameter with a default is typed from it,
// an array pattern over an array literal as a tuple (tsc 5.6 --strict).
func TestPatternParameterFromDefault(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{"function f([x, y] = [1, \"a\"]) { }\nf", "(__param0?: [number, string] | undefined) => void"},
		{"function f({ a } = { a: 1 }) { }\nf", "(__param0?: { a: number; } | undefined) => void"},
		{"var C = class { static async *p([x] = [1]) { } };\nC.p", "(__param0?: [number] | undefined) => AsyncGenerator<never, void, unknown>"},
		{"function f([n, s] = [1, \"a\"]) { return s; }\nf", "(__param0?: [number, string] | undefined) => string"},
		{"const f = ([[x, y] = [4, 5]] = []) => x;\nf", "(__param0?: [([number, number] | undefined)?] | undefined) => number"},
		{"const g = ([a = 1, b] = []) => a;\ng", "(__param0?: [(number | undefined)?, any?] | undefined) => number"},
		{"function h([[x, y, z] = [4, 5, 6]]) { return x; }\nh", "(__param0: [([number, number, number] | undefined)?]) => number"},
		{"const f = ({ a = 1, b = \"s\" } = {}) => b;\nf", "(__param0?: { a?: number; b?: string; } | undefined) => string"},
	} {
		if got := typeOfLastLib(t, tc.src); got != tc.want {
			t.Errorf("%q: got %s, want %s", tc.src, got, tc.want)
		}
	}
}

// A parameter default reading the parameter itself is TS2372 in tsc, which
// types the parameter any.
func TestSelfReferencingParameterDefault(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{"let x = 0;\nconst f = (x = x) => { };\nf", "(x?: any) => void"},
		{"const g = (y = 1, z = () => y) => y;\ng", "(y?: number | undefined, z?: () => number | undefined) => number"},
	} {
		if got := typeOfLast(t, tc.src); got != tc.want {
			t.Errorf("%q: got %s, want %s", tc.src, got, tc.want)
		}
	}
}

// A spread's or a later property naming an earlier one replaces it where
// it was first added: the type's order is the object's own-key order.
func TestSpreadPropertyOrder(t *testing.T) {
	h := "declare const o: { b: number; c: number };\ndeclare const p: { c: string; d: number };\n"
	for _, tc := range []struct{ src, want string }{
		{"({ a: 1, ...o })", "{ a: number; b: number; c: number; }"},
		{"({ ...o, a: 1 })", "{ b: number; c: number; a: number; }"},
		{"({ ...o, b: 'x' })", "{ b: string; c: number; }"},
		{"({ ...o, ...p })", "{ b: number; c: string; d: number; }"},
		{"({ a: 1, ...o, b: 'x', e: 2 })", "{ a: number; b: string; c: number; e: number; }"},
		{"({ b: 'x', ...p, b2: 1, ...o })", "{ b: number; c: number; d: number; b2: number; }"},
	} {
		if got := typeOfLastLib(t, h+tc.src); got != tc.want {
			t.Errorf("%s: got %s, want %s", tc.src, got, tc.want)
		}
	}
}

// An import-equals alias of a namespace (`import X = A.B`) stands for it,
// at top level and inside a namespace.
func TestImportEqualsAlias(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{"namespace App { export namespace Config { export const port = 8080 } }\nimport Cfg = App.Config\nCfg.port", "8080"},
		{"namespace A { export namespace B { export const v = 'x' } import BB = B; export const w = BB.v }\nA.w", "\"x\""},
	} {
		if got := typeOfLastLib(t, tc.src); got != tc.want {
			t.Errorf("%q: got %s, want %s", tc.src, got, tc.want)
		}
	}
}

// An enum member's initializer is TypeScript's constant evaluation of it:
// operators and earlier members, not only a literal.
func TestEnumConstantMembers(t *testing.T) {
	h := "enum K { A = 1, B = 2.5, C = 2 ** 53 + 1, D = -3, E = A << 4, F = K.B * 2 }\nenum S { X = 'x', Z = X + '!' }\n"
	for _, tc := range []struct{ src, want string }{
		{"K.C", "K.C"},
		{"K.F", "K.F"},
		{"S.Z", "S.Z"},
		{"K.D * 2", "number"},
	} {
		if got := typeOfLastLib(t, h+tc.src); got != tc.want {
			t.Errorf("%s: got %s, want %s", tc.src, got, tc.want)
		}
	}
}

// An object pattern over an array or a primitive reads its apparent type's
// members.
func TestObjectPatternApparentMembers(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{"declare const a: string[]\nconst { length } = a\nlength", "number"},
		{"const { toUpperCase } = 'abc'\ntoUpperCase", "() => string"},
	} {
		if got := typeOfLastLib(t, tc.src); got != tc.want {
			t.Errorf("%q: got %s, want %s", tc.src, got, tc.want)
		}
	}
}

// A generator member's yields read a binding initialized from `this.nx()`:
// the annotated member resolves on its own, not through the instance type
// that member's own inference is building.
func TestGeneratorMemberReadsThisMember(t *testing.T) {
	src := "class A { *[Symbol.iterator]() { let v = this.nx(); while (v !== null) { yield v; v = this.nx(); } } nx(): string | null { return null } }\nfunction first(a: A) { for (const s of a) return s; return null }\nfirst(new A())"
	if got := typeOfLastLib(t, src); got != "string | null" {
		t.Errorf("got %s, want string | null", got)
	}
}

// A `for await` loop variable is the awaited element of what it iterates,
// an async generator's yield included.
func TestForAwaitLoopVariable(t *testing.T) {
	h := "async function * readFile() {\n  yield \"a\";\n}\n"
	for _, tc := range []struct{ src, want string }{
		{h + "async function * gen() { for await (let line of readFile()) { yield line; } }\ngen", "() => AsyncGenerator<string, void, unknown>"},
		{"declare const ps: Promise<number>[];\nasync function f() { for await (const v of ps) { return v; } return 0; }\nf", "() => Promise<number>"},
		{"declare const s: Set<Promise<string>>;\nasync function f() { for await (const v of s) { return v; } return \"\"; }\nf", "() => Promise<string>"},
	} {
		if got := typeOfLastLib(t, tc.src); got != tc.want {
			t.Errorf("%q: got %s, want %s", tc.src, got, tc.want)
		}
	}
}

// An unannotated destructured parameter takes its contextual type, else
// the type its pattern implies (tsc 5.6).
func TestPatternParameterContextual(t *testing.T) {
	h := "async function * g() { yield \"a\"; }\nvar iter = g();\n"
	for _, tc := range []struct{ src, want string }{
		{h + "iter.next().then(({done, value}) => { })", "Promise<void>"},
		{"(({done, value}) => { })", "(__param0: { done: any; value: any; }) => void"},
		{"(([a, [b]], { c = 1 }) => { })", "(__param0: [any, [any]], __param1: { c?: number; }) => void"},
		{"declare function on(f: (o: { x: number }) => number): void;\non(({ x }) => x)", "void"},
	} {
		if got := typeOfLastLib(t, tc.src); got != tc.want {
			t.Errorf("%q: got %s, want %s", tc.src, got, tc.want)
		}
	}
}

// An element read through a string or number key on an object without an
// index signature is an implicit any.
func TestImplicitAnyElementAccess(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{"class C { m() { return 2; } }\nconst c = new C();\ndeclare const k: string;\nc[k]", "any"},
		{"const o = { a: 1 };\ndeclare const n: number;\no[n]", "any"},
		{"const o = { a: 1 };\no[\"a\"]", "number"},
	} {
		if got := typeOfLast(t, tc.src); got != tc.want {
			t.Errorf("%q: got %s, want %s", tc.src, got, tc.want)
		}
	}
}

// `yield*` over a hand-built iterable: what its iterator's next() yields,
// awaited for an async one.
func TestYieldStarOverProtocolObject(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{"const obj = { [Symbol.asyncIterator]() { return { next() { return Promise.resolve({ value: 1, done: false }); } }; } };\nasync function * g() { yield* obj; }\ng", "() => AsyncGenerator<number, void, unknown>"},
		{"const obj = { [Symbol.iterator]() { return { next() { return { value: \"s\", done: false }; } }; } };\nfunction * g() { yield* obj; }\ng", "() => Generator<string, void, unknown>"},
	} {
		if got := typeOfLastLib(t, tc.src); got != tc.want {
			t.Errorf("%q: got %s, want %s", tc.src, got, tc.want)
		}
	}
}

// A string's number index reads a string.
func TestStringNumberIndex(t *testing.T) {
	for _, s := range []string{"\"lol\"[1]", "declare const s: string;\ns[1]", "var C = class { x = \"lol\"[1] };\nnew C().x"} {
		if got := typeOfLastLib(t, s); got != "string" {
			t.Errorf("%q: got %s, want string", s, got)
		}
	}
}
