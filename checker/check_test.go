package checker_test

import (
	"strings"
	"testing"

	"KlainMainLang/binder"
	"KlainMainLang/checker"
	"KlainMainLang/lib"
	"KlainMainLang/parser"
)

// checkCodes checks src and returns its diagnostics as "line:TScode".
func checkCodes(t *testing.T, src string) string {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("%s: %v", src, err)
	}
	var out []string
	for _, d := range checker.New(binder.Bind(prog)).Check() {
		out = append(out, strings.TrimSpace(strings.Split(d.Error(), ":")[0])+":TS"+itoa(d.Code()))
	}
	return strings.Join(out, " ")
}

func itoa(n int) string {
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}

// TestCheck: the checker's type errors, each expectation (line and code)
// taken from tsc 5.6 --strict. Only a definite error is reported.
func TestCheck(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{"let a: number = 'x'", "1:TS2322"},
		{"let b: number\nb = 'y'", "2:TS2322"},
		{"function f(): number { return 'z' }", "1:TS2322"},
		{"function g(n: number, s?: string) { return n }\ng('w')", "2:TS2345"},
		{"function g(n: number, s?: string) { return n }\ng()", "2:TS2554"},
		{"function g(n: number, s?: string) { return n }\ng(1, 'a', 3)", "2:TS2554"},
		{"function r(a: number, ...b: number[]) { return a }\nr()", "2:TS2555"},
		{"const o: { x: number } = { x: 'q' }", "1:TS2322"},
		{"const v = 'a' - 1", "1:TS2362"},
		{"const v = 1 * 'b'", "1:TS2363"},
		{"const v = true + 1", "1:TS2365"},
		{"declare const b: boolean\nconst v = b & b", ""}, // TS2447, not reported
		{"let nn: number = null", "1:TS2322"},
		{"const q: string[] = [1, 2]", "1:TS2322 1:TS2322"}, // elaborated per element
		// An array literal in a tuple context; a generic base; an
		// intersection; an optional parameter's object; tsc 5.6's codes.
		{"interface P { pair?: [number, number] }\nconst f: P = { pair: [1, 2] }", ""},
		{"interface P { pair?: [number, number] }\nconst f: P = { pair: [1, 2, 3] }", "2:TS2322"},
		{"let x: number | null = null\ndeclare function g(): number | null\nif ((x = g()) !== null) { const n: number = x }", ""},
		{"interface SB<T> { size: T }\ninterface St extends SB<number> {}\ndeclare const st: St\nconst s: string = st.size", "4:TS2322"},
		{"interface A { a: string }\ninterface B { b: string }\ndeclare const a: A\nconst ab: A & B = a", "4:TS2322"},
		{"declare function o(x?: { a?: number }): void\no({ a: 'x' })", "2:TS2322"},
		{"declare function r(o?: { encoding?: null; flag?: string } | null): number\ndeclare function r(o: { encoding: 'utf8' | 'hex'; flag?: string } | 'utf8' | 'hex'): string\nconst t2: string = r({ encoding: 'utf8' })", ""},
		{"let b: number | null = 3\nconst bump = () => { b = b === null ? 1 : b + 1 }", ""},
		// A type name nothing declares (TS2304); a near miss is tsc's TS2552,
		// not reported; every kind of type parameter resolves.
		{"const x: Frobnicator = 1", "1:TS2304"},
		{"interface Point { x: number }\nconst p: Piont = { x: 1 }", ""},
		{"const g = <U>(y: U): U => y\ntype M<K extends string> = { [P in K]: number }\ntype E<T> = T extends Array<infer I> ? I : never\nclass C<Z> { m<W>(w: W): Z | W { return w } }", ""},
		// The forms of TS2304 tsc gives when it knows more (onFailedToResolveSymbol).
		{"class C { static foo = 1; m() { return foo } }", "1:TS2662"},
		{"class C { bar = 1; m() { return bar } }", "1:TS2663"},
		{"interface I { x: number }\nconst v = I", "2:TS2693"},
		{"namespace N { export interface J {} }\nconst w = N", "2:TS2708"},
		{"const blob = 1\nconst z = bob", "2:TS2552"},
		{"describe('x', () => {})", "1:TS2593"},
		{"const o = { missing }", "1:TS18004"},
		{"const n = number", "1:TS2693"},
		{"declare function f(x: string): string\nf<number>('a')", "2:TS2558"},
		{"class B { constructor(a: number) {} }\nclass D extends B { m() { super<number>(0) } }", "2:TS2754"},
		// Accessibility (codes from tsc 7.0.2): private, protected, a
		// protected member through another class's instance, a parent's
		// field through super, a private or protected constructor.
		{"class A { private p = 1; m() { return () => this.p } }\nnew A().p", "2:TS2341"},
		{"class A { protected q = 1 }\nclass B extends A { m(a: A, b: B) { return b.q + a.q } }\nclass C { k(a: A) { return a.q } }", "2:TS2446 3:TS2445"},
		{"class A { q = 1 }\nclass B extends A { m() { return super.q } }", "2:TS2855"},
		{"class S { private constructor() {} static make() { return new S() } }\nclass P { protected constructor() {} }\nclass R extends P {}\nnew S(); new R()", "4:TS2673 4:TS2674"},
		{"class A { private p = 1 }\nconst v = new A()['p']", ""},
		{"class C { get n() { return 1 } private set n(v: number) {} }\nnew C().n\nnew C().n = 2", "3:TS2341"},
		// `if (o?.f)` narrows o to defined.
		{"declare const o: { f?: () => void } | undefined\nif (o?.f) o.f()", ""},
		{"declare const u: string | { a: 1 }\nif (typeof u === 'Object') { const o: { a: 1 } = u } else { const s: string = u }", "2:TS2367"},
		// What is not an error.
		{"declare const t: [number, string]\nconst l: 2 = t.length", ""},
		{"const s: 'ab' = `ab`", ""},
		{"declare function h<T>(x: T): T\nlet k = h<'a'>('a')\nconst m: 'a' = k", ""},
		{"let e = ''\ne = e + 'x'", ""},
		{"type D = { done: true, v: 1 } | { done: false, v: 2 }\ndeclare const d: D\nif (d.done) { const one: 1 = d.v }", ""},
		{"let x: 0 | 1 = 0\nlet y: 0 | 1 | 9\n[y] = [x]\nconst yy: 0 = y", ""},
		{"var x: (...y: string[]) => void = function (...y) { const z: string[] = y }", ""},
		{"namespace M { export declare var n: number }\nconst mn: number = M.n", ""},
		{"let x: 1 = +1\nlet y: -1 = -1", ""},
		{"let i: 1e999 = 1e9999", ""}, // both are Infinity
		{"let e = undefined\ne = 1\ne = 'a'", ""},
		{"const f20: () => undefined = () => { }", ""},
		{"declare function t(f: () => string): void\nt(() => { throw new Error() })", ""},
		{"const f6: () => 'foo' | 'bar' = () => 'bar'", ""},
		{"declare let u: unknown\nlet v7: {} | null | undefined = u", ""},
		{"function k(x: true | false) { switch (x) { case true: return 1; case false: return 2 } const n: never = x }", ""},
		{"function k(x: string | number) { switch (typeof x) { case 'string': return 1; case 'number': return 2; case 'number': const n: never = x } }", ""},
		{"function k(x: 1 | 2) { switch (true) { case x === 1: return; case x === 2: return } const n: never = x }", ""},
		{"type F = { bar: number | null }\ndeclare const a: F\nif (a.bar) { const { bar } = a; const w: number = bar }", ""},
		{"const o = ['hello' as const][0]\nconst h: 'hello' = o", ""},
		{"type D = 'R' | 'L'\ninterface G { set: (d: D) => void }\ndeclare function take(d: D): void\nconst g: G = { set: (d = 'R') => { take(d) } }", ""},
		{"declare function assert(v: any): asserts v\nfunction f(p: number | undefined): number { return (assert(p !== undefined), p) }", ""},
		{"interface P { s?: string | null }\ndeclare const p: P\nconst { s = 'd' } = p\nconst t: string | null = s", ""},
		{"class C { constructor(public p?: number) {} }\nconst c: number | undefined = new C().p", ""},
		// Properties (tsc 5.6: TS2339, TS2741, TS2353, TS2739, TS2740).
		{"interface P { x: number; y: string }\ndeclare const p: P\np.z", "3:TS2339"},
		{"interface P { x: number; y: string }\nconst a: P = { x: 1 }", "2:TS2741"},
		{"interface P { x: number; y: string }\nconst b: P = { x: 1, y: 's', w: 2 }", "2:TS2353"},
		{"const c: { a: number; b: number; c: number } = {}", "1:TS2739"},
		{"const d: { a: number; b: number; c: number; d: number; e: number } = {} as { q: number }", "1:TS2739"},
		{"const d: { a: number; b: number; c: number; d: number; e: number; f: number } = {} as { q: number }", "1:TS2740"},
		{"interface P { x: number }\nconst e: P = <P>{}", ""},
		{"class S { get v(): string { return '' } set v(x: string | undefined) {} }\nnew S().v = undefined", ""},
		{"class A1 { x = 1 }\nclass B1 { foo(this: A1) { return this.x } }", ""},
		{"interface Element { tag: string }\ndeclare const el: Element\nel.textContent", ""},
		{"class K { toString() { return '' } }\nnew K().valueOf()", ""},
		// Overloads declared without bodies are not checked by the first one.
		{"declare function o(a: number): void\ndeclare function o(a: number, b: number): void\no(1, 2)", ""},
	} {
		if got := checkCodes(t, tc.src); got != tc.want {
			t.Errorf("%q: got %q, want %q", tc.src, got, tc.want)
		}
	}
}

// checkCodesLib is checkCodes with the builtin declarations bound around
// the program, as a compile binds them.
func checkCodesLib(t *testing.T, src string) string {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("%s: %v", src, err)
	}
	libProgs, err := lib.Programs()
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, d := range checker.New(binder.BindWith(prog, binder.Options{Lib: libProgs})).Check() {
		out = append(out, strings.TrimSpace(strings.Split(d.Error(), ":")[0])+":TS"+itoa(d.Code()))
	}
	return strings.Join(out, " ")
}

// TestInferredPredicates: TypeScript 5.5's inferred type predicates, the
// type-guard overload of Array.filter, and a primitive narrowed to never;
// each expectation from tsc 5.6 --strict.
func TestInferredPredicates(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{"function isS(x: unknown) { return typeof x === \"string\" }\ndeclare let u: unknown\nif (isS(u)) { const s: string = u }", ""},
		{"function isN(x: string | number) { return typeof x === \"number\" }\ndeclare let v: string | number\nif (isN(v)) {} else { const s: string = v }", ""},
		// The false branch keeps strings: no predicate.
		{"function isShort(x: unknown) { return typeof x === \"string\" && x.length < 10 }\ndeclare let u: unknown\nif (isShort(u)) { const s: string = u }", "3:TS2322"},
		{"declare const a: (number | null)[]\nconst b: number[] = a.filter(x => x !== null)", ""},
		{"declare const a: (number | null)[]\nconst b: number[] = a.filter(x => !!x)", "2:TS2322"},
		{"declare const q: string[]\nconst r: string[] = q.filter(j => j !== \"x\")", ""},
		{"declare let s: string\nif (typeof s !== \"string\") { const n: never = s }", ""},
		{"const h: (x: number | null) => x is number = (x: number | null) => true", "1:TS2322"},
	} {
		if got := checkCodesLib(t, tc.src); got != tc.want {
			t.Errorf("%s:\n got  %q\n want %q", tc.src, got, tc.want)
		}
	}
}

// TestAwaited: `await` is the promise's value type (tsc's getAwaitedType),
// and an awaited literal keeps its literal in context; each expectation from
// tsc --strict.
func TestAwaited(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{"async function g(p: Promise<number>) { const s: string = await p }", "1:TS2322"},
		{"async function g(p: Promise<number | string>) { const n: number = await p }", "1:TS2322"},
		{"async function g(p: Promise<Promise<number>>) { const s: string = await p }", "1:TS2322"},
		{"async function g(p: Promise<number>) { const n: number = await p; const m: number = await 3 }", ""},
		{"interface Obj { key: \"value\" }\nasync function g() { const o: Obj = await { key: \"value\" } }", ""},
		{"async function g() { const v = await Promise.resolve(41); return v + 1 }", ""},
	} {
		if got := checkCodesLib(t, tc.src); got != tc.want {
			t.Errorf("%s:\n got  %q\n want %q", tc.src, got, tc.want)
		}
	}
}
