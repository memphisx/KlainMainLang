package checker_test

import "testing"

const fns = "function id<T>(x: T): T { return x }\n" +
	"function wrap<T>(x: T): T[] { return [x] }\n" +
	"function first<T>(xs: T[]): T | undefined { return xs[0] }\n" +
	"function pair<A, B>(a: A, b: B): [A, B] { return [a, b] }\n" +
	"function apply<T, U>(x: T, f: (t: T) => U): U { return f(x) }\n" +
	"function orElse<T>(x: T | undefined, d: T): T { return x ?? d }\n" +
	"function len<S extends string>(s: S): S { return s }\n" +
	"declare const u: number | undefined\n"

func TestGenerics(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{fns + "id", "<T>(p0: T) => T"},
		{fns + "id(1)", "1"},
		{"function ap<T>(v: T, f: (x: T) => T): T { return f(v) }\nap(2, (x) => x * 3)", "number"},
		{fns + "id('a')", `"a"`},
		{fns + "wrap(1)", "number[]"},
		{fns + "first([1, 2])", "number | undefined"},
		{fns + "pair(1, 'x')", "[number, string]"},
		{fns + "id<string>('a')", "string"},
		{fns + "apply(2, n => n * 2)", "number"},
		{fns + "apply('s', s => s.length)", "number"},
		{fns + "apply(2, (n) => { const r = n + 1; return r })", "number"},
		{fns + "orElse(u, 5)", "number"},
		{fns + "len('abc')", `"abc"`},
		{fns + "const k = (n: number) => n\nid(k)", "(p0: number) => number"},
		{fns + "declare const q: any\nid(q)", "any"},
		{"function g<T>(x: T): T { return x }\ng(undefinedName)", "?"},
	} {
		if got := typeOfLast(t, tc.src); got != tc.want {
			t.Errorf("%q: got %s, want %s", tc.src, got, tc.want)
		}
	}
	// A callback's parameter read inside it.
	for _, tc := range []struct{ src, want string }{
		{"const h: (n: number) => number = n => { n; return n }", "number"},
		{fns + "apply('s', s => { s; return 1 })", "string"},
		{fns + "apply([1], xs => { xs; return 1 })", "number[]"},
		{"function ap<T>(v: T, f: (x: T) => T): T { return f(v) }\nap(2, (x) => { x; return x * 3 })", "number"},
		{"function run(fn: (a?: number) => number): number { return fn(10) }\nrun((a) => { a; return 1 })", "number | undefined"},
	} {
		if got := typeOfProbe(t, tc.src); got != tc.want {
			t.Errorf("%q: got %s, want %s", tc.src, got, tc.want)
		}
	}
}

const decls = "interface Box<T> { value: T; tag: string }\n" +
	"type Pair<A, B> = [A, B]\n" +
	"type Maybe<T> = T | undefined\n" +
	"interface Node<T> { value: T; next: Node<T> | null }\n" +
	"class Stack<T> { items: T[] = []; constructor(first: T) { this.items.push(first) }\n" +
	"  peek(): T { return this.items[0] }\n  size() { return this.items.length } }\n" +
	"class Plain { constructor(n: number) { } }\n"

func TestGenericDeclarations(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{decls + "declare const b: Box<number>\nb.value", "number"},
		{decls + "declare const b: Box<number>\nb", "Box<number>"},
		{decls + "declare const p: Pair<string, boolean>\np", "[string, boolean]"},
		{decls + "declare const m: Maybe<string>\nm", "string | undefined"},
		{decls + "declare const n: Node<number>\nn.next", "Node<number> | null"},
		{decls + "new Stack(1)", "Stack<number>"},
		{decls + "new Stack<string>('a')", "Stack<string>"},
		{decls + "new Stack(1).peek()", "number"},
		{decls + "new Stack('s').items", "string[]"},
		{decls + "new Stack(1).size", "?"},
		{decls + "new Plain(1)", "Plain"},
		{decls + "declare const q: Box<Pair<number, string>>\nq.value", "[number, string]"},
	} {
		if got := typeOfLast(t, tc.src); got != tc.want {
			t.Errorf("%q: got %s, want %s", tc.src, got, tc.want)
		}
	}
}

const overloads = "function conv(x: string): number;\n" +
	"function conv(x: number): string;\n" +
	"function conv(x: string | number): string | number { return typeof x === 'string' ? x.length : String(x) }\n" +
	"function opt(a: number): number;\n" +
	"function opt(a: number, b: string): string;\n" +
	"function opt(a: number, b?: string): number | string { return b ?? a }\n" +
	"function gen<T>(x: T[]): T;\n" +
	"function gen(x: string): string;\n" +
	"function gen(x: any): any { return x }\n" +
	"class K { m(x: string): string; m(x: number): number; m(x: any): any { return x } }\n"

func TestOverloads(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{overloads + "conv('a')", "number"},
		{overloads + "conv(1)", "string"},
		{overloads + "opt(1)", "number"},
		{overloads + "opt(1, 'x')", "string"},
		{overloads + "gen([true])", "boolean"},
		{overloads + "gen('s')", "string"},
		{overloads + "new K().m(2)", "number"},
		{overloads + "new K().m('q')", "string"},
		{overloads + "conv", "{ (p0: string): number; (p0: number): string }"},
		{overloads + "declare const u: string | number\nconv(u)", "?"},
		{overloads + "conv(unknownName)", "?"},
	} {
		if got := typeOfLast(t, tc.src); got != tc.want {
			t.Errorf("%q: got %s, want %s", tc.src, got, tc.want)
		}
	}
}

func TestThis(t *testing.T) {
	const cls = "class P { x = 1; s: string = 'a'\n  m() { this.x; return this.s }\n  n() { const f = () => this.x; return f() }\n  static k() { return this }\n}\n"
	for _, tc := range []struct{ src, want string }{
		{cls + "new P().m()", "string"},
		{cls + "new P().n()", "number"},
		{"class Q { v: number = 1; m() { this; return 0 } }", "Q"},
		{"class Q { v: number = 1; static m() { this; return 0 } }", "?"},
		{"class B<T> { items: T[] = []; m(): number { this.items; return 0 } }", "T[]"},
		{"const o = { a: 1, m() { this; return 0 } }", "?"},
		{"function f(this: any) { this }", "any"},
	} {
		if got := typeOfProbe(t, tc.src); got != tc.want {
			t.Errorf("%q: got %s, want %s", tc.src, got, tc.want)
		}
	}
}

func TestEnums(t *testing.T) {
	const en = "enum Dir { N, E, S = 10, W }\nenum Col { Red = 'r', Green = 'g' }\n"
	for _, tc := range []struct{ src, want string }{
		{en + "Dir.E", "Dir.E"},
		{en + "Dir.W", "Dir.W"},
		{en + "const d = Dir.E\nd", "Dir.E"},
		{en + "let d = Dir.E\nd", "Dir.E"}, // declared Dir, narrowed by the assignment
		{en + "let d: Dir = Dir.N\nd", "Dir.N"},
		{en + "declare const d: Dir\nd", "Dir"},
		{en + "Col.Red", "Col.Red"},
		{en + "declare const c: Col\nc", "Col"},
		{"enum X { A = 1 + 1 }\nX.A", "?"},
	} {
		if got := typeOfLast(t, tc.src); got != tc.want {
			t.Errorf("%q: got %s, want %s", tc.src, got, tc.want)
		}
	}
	for _, tc := range []struct{ src, want string }{
		{en + "declare const d: Dir\nif (d === Dir.N) { d }", "Dir.N"},
		{en + "declare const d: Dir\nif (d !== Dir.N) { d }", "Dir.E | Dir.S | Dir.W"},
		{en + "declare const d: Dir\nswitch (d) { case Dir.N: break; case Dir.E: break; default: d }", "Dir.S | Dir.W"},
	} {
		if got := typeOfProbe(t, tc.src); got != tc.want {
			t.Errorf("%q: got %s, want %s", tc.src, got, tc.want)
		}
	}
}

// TestGenericBase: a class extending a generic base (`extends Box<T[]>`)
// inherits the instantiated members and, without a constructor of its own,
// the base's instantiated constructor. Expectations from tsc 5.6.
func TestGenericBase(t *testing.T) {
	const box = "class Box<T> { v: T; constructor(v: T) { this.v = v } get2(): T { return this.v } }\n"
	for _, tc := range []struct{ src, want string }{
		{box + "class NumBox extends Box<number> {}\nnew NumBox(1).v", "number"},
		{box + "class NumBox extends Box<number> {}\nnew NumBox(1)", "NumBox"},
		{box + "class NumBox extends Box<number> {}\nnew NumBox(1).get2()", "number"},
		{box + "class Pair<T> extends Box<T[]> {}\nnew Pair([1]).v", "number[]"},
		{box + "class Pair<T> extends Box<T[]> {}\nnew Pair([1])", "Pair<number>"},
		{box + "class Pair<T> extends Box<T[]> {}\nnew Pair(['a']).get2()", "string[]"},
		{box + "class S extends Box<string> { constructor() { super('x') } }\nnew S()", "S"},
		// A base's members see its own type parameters, not the subclass's
		// same-named ones.
		{"interface Item { n: number }\nclass Base<T> { x!: Item; t!: T }\nclass D<Item> extends Base<Item> {}\ndeclare const d: D<string>\nd.x", "Item"},
		{"interface Item { n: number }\nclass Base<T> { x!: Item; t!: T }\nclass D<Item> extends Base<Item> {}\ndeclare const d: D<string>\nd.t", "string"},
		// A base may name its own subclass.
		{"class B0 { child?: D0 }\nclass D0 extends B0 { k = 1 }\ndeclare const d0: D0\nd0.child", "D0 | undefined"},
		// A circular `extends` has no answer (tsc rejects it), and ends.
		{"class A extends B {}\nclass B extends A {}\ndeclare const a: A\na", "?"},
		{"class A extends A {}\nnew A()", "?"},
	} {
		if got := typeOfLast(t, tc.src); got != tc.want {
			t.Errorf("%q: got %s, want %s", tc.src, got, tc.want)
		}
	}
}

// TestExpandingGeneric: a generic naming an ever-larger instantiation of
// itself in its members (`interface I<T> { x: I<T[]> }`) ends: past a bound
// the deeper instantiation is unanswered.
func TestExpandingGeneric(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{"interface I<T> { x: I<T[]>; v: T }\ndeclare const i: I<number>\ni.v", "number"},
		{"class C<T> { x!: C<T[]>; v!: T }\ndeclare const c: C<number>\nc.v", "number"},
		{"type A<T> = { x: A<T[]>; v: T }\ndeclare const a: A<number>\na.v", "?"},
	} {
		if got := typeOfLast(t, tc.src); got != tc.want {
			t.Errorf("%q: got %s, want %s", tc.src, got, tc.want)
		}
	}
}
