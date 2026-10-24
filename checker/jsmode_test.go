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

// typeOfLastJS is typeOfLast under -compat=js.
func typeOfLastJS(t *testing.T, src string) string {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("%s: %v", src, err)
	}
	c := checker.NewWith(binder.BindWith(prog, binder.Options{AnnexB: true}), options.Options{Compat: "js"})
	var last ast.Expression
	ast.Inspect(prog, func(n ast.Node) bool {
		if es, ok := n.(*ast.ExpressionStatement); ok {
			last = es.Expr
		}
		return true
	})
	ty := c.TypeOf(last)
	if c.Unanswered(ty) {
		return "?"
	}
	return ty.String()
}

func TestCompatJS(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{"function add(a, b) { a; return a + b }\nadd(1, 2)", "number"},
		{"function greet(n) { n; return 'hi ' + n }\ngreet('x')", "string"},
		{"function f(a) { a }\nf(1)\nf('x')\nf", "(a: any) => void"}, // disagreeing sites: checkJs's any
		{"function g(a) { a }", "any"},
		{"declare const xs: number[]\nxs.map((v) => { v; return 0 })", "any"},
		{"const h = (a) => a\nh(true)", "boolean"},
		{"r(3)\nfunction r(n) { if (n > 0) r(n - 1); n }", "number"}, // the recursive site is skipped
		{"class P { constructor(x, y) { this.x = x; this.y = y } }\nnew P(1, 'a').y", "string"},
		{"class P { constructor(x) { this.x = x; if (x) { this.z = [1] } } }\nnew P(1).z", "number[]"},
		{"class Q { constructor() { this.v = 1; this.v = 'a' } }\nnew Q().v", "?"},
		{"class M { n(x) { return 1 }\n get b() { this.n(2); return 0 } }", "number"},
		{"class M { #m(x) { return x }\n get b() { this.#m; return 0 } }", "(x: any) => any"},
		{"function k(a) { a; return a + 1 }\nk", "(a: any) => any"},
		{"n(null)\nfunction n(a) { a }", "any"},                            // null and undefined widen to any
		{"u(undefined)\nu(2)\nfunction u(a) { a }", "any"},                 // it holds undefined at run time
		{"function mk() { return (a) => a }\nmk", "() => (a: any) => any"}, // a returned function's callers are out of sight
		{"const mk = () => (a) => a\nmk", "() => (a: any) => any"},
		{"var o = { f: function (a) { } }\no.f", "(a: any) => void"}, // an object literal's method: its receiver decides the call
		{"var o = { f(a, b) { return 1 } }\no", "{ f: (a: any, b: any) => number; }"},
		{"class K { f = (a, b) => 1 }\nnew K().f", "(a: any, b: any) => number"}, // a field's readers decide the call
	} {
		if got := typeOfLastJS(t, tc.src); got != tc.want {
			t.Errorf("%q: got %s, want %s", tc.src, got, tc.want)
		}
	}
	// Strict mode keeps an unannotated parameter unanswered.
	if got := typeOfLast(t, "function g(a) { a }\ng(1)\na0()\nfunction a0() { }\ng"); got != "?" {
		t.Errorf("strict: got %s", got)
	}
}

// A parameter asked for before the function itself is read (codegen asks
// JSCallSiteParam first) must not leave a half-built signature behind: a
// site's argument reads the function back through its contextual type.
func TestJSCallSiteParamBeforeFunctionRead(t *testing.T) {
	src := "function props(x) { var a = []; for (let p in x) a.push(p); return a }\nprops([]);\nprops([1]);\nprops"
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	c := checker.NewWith(binder.BindWith(prog, binder.Options{AnnexB: true}), options.Options{Compat: "js"})
	var fd *ast.FunctionDeclaration
	var last ast.Expression
	ast.Inspect(prog, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FunctionDeclaration:
			fd = n
		case *ast.ExpressionStatement:
			last = n.Expr
		}
		return true
	})
	if p := c.JSCallSiteParam(fd, 0); p == nil || p.String() != "number[]" {
		t.Fatalf("parameter: got %v, want number[]", p)
	}
	if got := c.TypeOf(last); c.Unanswered(got) || got.String() != "(x: number[]) => string[]" {
		t.Fatalf("function: got %s", got)
	}
}

// A function literal a member is read off (`.call`, `.apply`) takes any.
func TestJSMemberReceiverParams(t *testing.T) {
	for _, s := range []string{
		"(function(obj) { obj; }.apply(null, [{a: 1}]))",
		"(function(obj) { obj; }.call(null, {a: 1}))",
	} {
		prog, _ := parser.Parse(s)
		c := checker.NewWith(binder.BindWith(prog, binder.Options{AnnexB: true}), options.Options{Compat: "js"})
		ast.Inspect(prog, func(n ast.Node) bool {
			if fe, ok := n.(*ast.FunctionExpression); ok {
				if ty := c.TypeOf(fe); c.Unanswered(ty) || ty.String() != "(obj: any) => void" {
					t.Errorf("%q: got %s", s, ty)
				}
			}
			return true
		})
	}
}

// `new c.B()` constructs through the value c.B (a class-valued field), not
// a declaration named B.
func TestQualifiedNewThroughVariable(t *testing.T) {
	s := "class B {}\nvar C = class { B = class { m() { return 1; } }; };\nlet c = new C();\nlet x = new c.B();\n"
	prog, _ := parser.Parse(s)
	c := checker.NewWith(binder.BindWith(prog, binder.Options{AnnexB: true}), options.Options{Compat: "js"})
	for _, st := range prog.Body {
		if v, ok := st.(*ast.VarDeclaration); ok && v.Name == "x" {
			if ty := c.TypeOf(v.Init); ty.String() != "(Anonymous class)" {
				t.Errorf("new c.B(): got %s", ty)
			}
		}
	}
}

// `yield*` over a value of type never iterates tsc's error type, any: the
// generator is still typed (tsc 5.6 declaration output).
func TestYieldStarNeverIsAny(t *testing.T) {
	s := "var abrupt = function() { throw 1; };\nvar gen = async function *g() { yield* abrupt(); };\nvar gs = function *() { yield* abrupt(); };\nvar p = gen().next();\n"
	prog, _ := parser.Parse(s)
	c := checker.NewWith(binder.BindWith(prog, binder.Options{AnnexB: true, Lib: libProgsT(t)}), options.Options{Compat: "js"})
	want := map[string]string{"gen": "() => AsyncGenerator<any, void, unknown>", "gs": "() => Generator<any, void, unknown>", "p": "Promise<IteratorYieldResult<any> | IteratorReturnResult<void>>"}
	for _, st := range prog.Body {
		if v, ok := st.(*ast.VarDeclaration); ok && want[v.Name] != "" {
			if got := c.TypeOf(v.Init).String(); got != want[v.Name] {
				t.Errorf("%s: got %s, want %s", v.Name, got, want[v.Name])
			}
		}
	}
}

func libProgsT(t *testing.T) []*ast.Program {
	p, err := lib.Programs()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// An empty literal under a nested pattern in a parameter's default is
// typed by binding-pattern context (tsc 5.6 declaration output).
func TestNestedPatternEmptyDefault(t *testing.T) {
	for src, want := range map[string]string{
		"function ([[,] = g()] = [[]]) {}": "(__param0?: [never[]] | undefined) => void",
		"function ([[,]] = [[]]) {}":       "(__param0?: [[]] | undefined) => void",
		"function ([[x] = [1]] = [[]]) {}": "(__param0?: [[]] | undefined) => void",
		"function ([[,] = 1] = [[]]) {}":   "(__param0?: [never[]] | undefined) => void",
	} {
		pr, _ := parser.Parse("function* g() { }\nconst f = " + src + ";\n")
		cc := checker.NewWith(binder.BindWith(pr, binder.Options{Lib: libProgsT(t)}), options.Options{})
		for _, st := range pr.Body {
			if v, ok := st.(*ast.VarDeclaration); ok {
				if got := cc.TypeOf(v.Init).String(); got != want {
					t.Errorf("%s: got %s, want %s", src, got, want)
				}
			}
		}
	}
}

// A computed key of a non-literal type gives the literal tsc's index
// signatures; a literal-typed one names its property (tsc 5.6 --strict).
func TestComputedKeyIndexSignature(t *testing.T) {
	s := "let v = \"v\";\nconst k = \"lit\";\nlet n = 3;\nconst o = { [v]: 1, f() {} };\nconst p = { [k]: true, a: 1 };\nconst q = { [n]: \"s\", 2: 5, b: 1 };\nconst r = { [v]: 1, [n]: \"x\" };\n"
	want := map[string]string{
		"o": "{ [x: string]: number | () => void; f: () => void; }",
		"p": "{ lit: boolean; a: number; }",
		"q": "{ [x: number]: string | number; 2: number; b: number; }",
		"r": "{ [x: string]: string | number; [x: number]: string; }",
	}
	prog, _ := parser.Parse(s)
	c := checker.NewWith(binder.BindWith(prog, binder.Options{Lib: libProgsT(t)}), options.Options{})
	for _, st := range prog.Body {
		if v, ok := st.(*ast.VarDeclaration); ok && want[v.Name] != "" {
			if got := c.TypeOf(v.Init).String(); got != want[v.Name] {
				t.Errorf("%s: got %s, want %s", v.Name, got, want[v.Name])
			}
		}
	}
}

// A JavaScript function literal written as an array element takes any for
// its parameters, as checkJs gives (tsc 5.6 declaration output).
func TestJSArrayElementFunction(t *testing.T) {
	s := "var a = [function *(a) { yield a+1; return; }];\nvar e = [function (x) { return x; }];\nvar g = [(x) => 1];\n"
	want := map[string]string{"a": "((a: any) => Generator<any, void, unknown>)[]", "e": "((x: any) => any)[]", "g": "((x: any) => number)[]"}
	prog, _ := parser.Parse(s)
	c := checker.NewWith(binder.BindWith(prog, binder.Options{AnnexB: true, Lib: libProgsT(t)}), options.Options{Compat: "js"})
	for _, st := range prog.Body {
		if v, ok := st.(*ast.VarDeclaration); ok {
			if got := c.TypeOf(v.Init).String(); got != want[v.Name] {
				t.Errorf("%s: got %s, want %s", v.Name, got, want[v.Name])
			}
		}
	}
}

// A conditional's object literal branch: dropped when the other branch's
// type covers it, else normalized with the other's missing properties
// (tsc 5.6 declaration output).
func TestConditionalObjectLiteralBranches(t *testing.T) {
	s := "function f(options?: { id?: number; process?: any; state?: string }) {\n  const o = options !== undefined && options !== null && typeof options === \"object\" ? options : {};\n  return o;\n}\nfunction g(c: boolean) { return c ? { a: 1 } : {}; }\nfunction h(c: boolean) { return c ? { a: 1 } : { b: \"x\" }; }\n"
	want := map[int]string{
		2: "{ id?: number | undefined; process?: any; state?: string | undefined; }",
		5: "{ a: number; } | { a?: undefined; }",
		6: "{ a: number; b?: undefined; } | { b: string; a?: undefined; }",
	}
	prog, _ := parser.Parse(s)
	c := checker.NewWith(binder.BindWith(prog, binder.Options{Lib: libProgsT(t)}), options.Options{})
	ast.Inspect(prog, func(n ast.Node) bool {
		if x, ok := n.(*ast.ConditionalExpression); ok {
			if got := c.TypeOf(x).String(); got != want[x.GetPos().Line] {
				t.Errorf("line %d: got %s, want %s", x.GetPos().Line, got, want[x.GetPos().Line])
			}
		}
		return true
	})
}

// An object spread of a plain object type copies its properties (tsc's
// getSpreadType): later ones override, an optional one unions with an
// earlier property (tsc 5.6 declaration output).
func TestObjectSpreadType(t *testing.T) {
	s := "let o = { c: 3, d: \"x\" };\nlet p: { a?: number } = {};\nconst r = { a: 1, ...o, d: true };\nconst s = { a: \"s\", ...p };\n"
	want := map[string]string{"r": "{ a: number; c: number; d: boolean; }", "s": "{ a: string | number; }"}
	prog, _ := parser.Parse(s)
	c := checker.NewWith(binder.BindWith(prog, binder.Options{Lib: libProgsT(t)}), options.Options{})
	for _, st := range prog.Body {
		if v, ok := st.(*ast.VarDeclaration); ok && want[v.Name] != "" {
			if got := c.TypeOf(v.Init).String(); got != want[v.Name] {
				t.Errorf("%s: got %s, want %s", v.Name, got, want[v.Name])
			}
		}
	}
}

// A static initializer's `this` is the class value, also while that
// value's own type is being built, and a class value reads Function's
// members (tsc 5.6 declaration output).
func TestStaticInitializerThis(t *testing.T) {
	s := "class D { static g = 1; static f = this.g; static h = (this.name); static k = D.length; }\nconst a = D.f;\nconst b = D.h;\nconst n = D.k;\n"
	want := map[string]string{"a": "number", "b": "string", "n": "number"}
	prog, _ := parser.Parse(s)
	c := checker.NewWith(binder.BindWith(prog, binder.Options{Lib: libProgsT(t)}), options.Options{})
	for _, st := range prog.Body {
		if v, ok := st.(*ast.VarDeclaration); ok && want[v.Name] != "" {
			if got := c.TypeOf(v.Init).String(); got != want[v.Name] {
				t.Errorf("%s: got %s, want %s", v.Name, got, want[v.Name])
			}
		}
	}
}

// `this` in an object literal's method is the literal's type, while the
// literal is typed too (tsc 5.6 declaration output).
func TestObjectLiteralMethodThis(t *testing.T) {
	s := "const a = { b() { return this._b; }, _b: { c: 42 }, n: 1, m() { return this.n + 1; } };\nconst r = a.b();\nconst s = a.m();\n"
	want := map[string]string{"r": "{ c: number; }", "s": "number"}
	prog, _ := parser.Parse(s)
	c := checker.NewWith(binder.BindWith(prog, binder.Options{Lib: libProgsT(t)}), options.Options{})
	for _, st := range prog.Body {
		if v, ok := st.(*ast.VarDeclaration); ok && want[v.Name] != "" {
			if got := c.TypeOf(v.Init).String(); got != want[v.Name] {
				t.Errorf("%s: got %s, want %s", v.Name, got, want[v.Name])
			}
		}
	}
}

// `opts ?? {}` and `opts || {}` reduce by subtype to the options type
// (tsc 5.6 declaration output).
func TestNullishLiteralReduction(t *testing.T) {
	s := "interface O { a?: number; b?: string }\ndeclare const opts: O | undefined;\nconst x = opts ?? {};\nconst y = opts || {};\nconst z = opts ?? { c: 1 };\ndeclare const xs: string[] | undefined;\nconst w = xs ?? [];\n"
	want := map[string]string{"x": "O", "y": "O", "z": "O | { c: number; }", "w": "string[]"}
	prog, _ := parser.Parse(s)
	c := checker.NewWith(binder.BindWith(prog, binder.Options{Lib: libProgsT(t)}), options.Options{})
	for _, st := range prog.Body {
		if v, ok := st.(*ast.VarDeclaration); ok && want[v.Name] != "" {
			if got := c.TypeOf(v.Init).String(); got != want[v.Name] {
				t.Errorf("%s: got %s, want %s", v.Name, got, want[v.Name])
			}
		}
	}
}

// `ThisType<any>` in an intersection types an object literal method's
// `this` (Object.defineProperty's descriptor).
func TestThisTypeMarker(t *testing.T) {
	codes := checkCodesLib(t, "const o = { a: 1 };\nObject.defineProperty(o, \"g\", { get() { return this.a * 10; } });\nconst d: PropertyDescriptor = { get() { return this.a; } };\n")
	if codes != "3:TS2339" {
		t.Fatalf("codes %q, want 3:TS2339 (this.a on the descriptor without the marker)", codes)
	}
}

// `this.b` in a literal's method naming none of its properties is tsc's
// error type: the literal still types, and the read is TS2339.
func TestObjectLiteralMethodThisMissing(t *testing.T) {
	if codes := checkCodesLib(t, "const p = { a: 1, m() { return this.b; } };\n"); codes != "1:TS2339" {
		t.Fatalf("codes %q, want 1:TS2339", codes)
	}
}

// A literal getter that only throws returns void, a method never (tsc's
// mayReturnNever).
func TestLiteralAccessorThrowsVoid(t *testing.T) {
	s := "const o = { get a() { throw 1; }, m() { throw 1; } };\nconst x = o.a;\nconst y = o.m;\n"
	want := map[string]string{"x": "void", "y": "() => never"}
	prog, _ := parser.Parse(s)
	c := checker.NewWith(binder.BindWith(prog, binder.Options{Lib: libProgsT(t)}), options.Options{})
	for _, st := range prog.Body {
		if v, ok := st.(*ast.VarDeclaration); ok && want[v.Name] != "" {
			if got := c.TypeOf(v.Init).String(); got != want[v.Name] {
				t.Errorf("%s: got %s, want %s", v.Name, got, want[v.Name])
			}
		}
	}
}

// An annotated optional tuple element (`[T?]`, `[a?: T]`).
func TestOptionalTupleAnnotation(t *testing.T) {
	codes := checkCodesLib(t, "const a: [number, string?] = [1];\nconst b: [number, string?] = [1, \"x\"];\nconst c: [x: number, y?: string] = [];\nconst n: 1 | 2 = a.length;\n")
	if codes != "3:TS2322" {
		t.Fatalf("codes %q, want 3:TS2322", codes)
	}
}

// A field initializer calling a method declared after it (`#b =
// this.#m()`) reads the method's type, resolved on demand while the
// instance type is built, in either lane.
func TestClassFieldInitializerMethod(t *testing.T) {
	s := "var C = class {\n  a = this.#m();\n  #m() { return 42; }\n  #b = this.#m();\n};\n"
	for _, js := range []bool{false, true} {
		prog, _ := parser.Parse(s)
		o := options.Options{}
		if js {
			o.Compat = "js"
		}
		c := checker.NewWith(binder.BindWith(prog, binder.Options{Lib: libProgsT(t)}), o)
		ast.Inspect(prog, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpression); ok {
				if got := c.TypeOf(call); c.Unanswered(got) || got.String() != "number" {
					t.Errorf("js=%v line %d: got %s", js, call.GetPos().Line, got)
				}
			}
			return true
		})
	}
}

// In JavaScript a member a class never declares or assigns is tsc's
// error type, any; one a method assigns is not.
func TestJSUndeclaredMember(t *testing.T) {
	s := "class C { m() { this.y = 1; } }\nconst c = new C();\nconst a = c.__lookupGetter__;\nconst b = c.y;\n"
	prog, _ := parser.Parse(s)
	c := checker.NewWith(binder.BindWith(prog, binder.Options{Lib: libProgsT(t)}), options.Options{Compat: "js"})
	for _, st := range prog.Body {
		if v, ok := st.(*ast.VarDeclaration); ok && (v.Name == "a" || v.Name == "b") {
			got := c.TypeOf(v.Init)
			if v.Name == "a" && got.String() != "any" {
				t.Errorf("a: got %s, want any", got)
			}
			if v.Name == "b" && !c.Unanswered(got) && got.String() == "any" {
				t.Errorf("b: got any for an assigned member")
			}
		}
	}
}

// TestJSCallSiteOrder: a callee's type does not depend on which call is
// asked first, though inferring its parameters from the call sites types
// an argument whose context reads the callee back.
func TestJSCallSiteOrder(t *testing.T) {
	src := "function props(x) {\n  var array = [];\n  for (let p in x) array.push(p);\n  return array;\n}\nvar s;\ns = props({});\ns = props({x:1});\n"
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	var callees []ast.Expression
	ast.Inspect(prog, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpression); ok {
			if id, ok := call.Callee.(*ast.Identifier); ok && id.Name == "props" {
				callees = append(callees, id)
			}
		}
		return true
	})
	for _, rev := range []bool{false, true} {
		c := checker.NewWith(binder.BindWith(prog, binder.Options{AnnexB: true}), options.Options{Compat: "js"})
		for k := range callees {
			i := k
			if rev {
				i = len(callees) - 1 - k
			}
			if got := c.TypeOf(callees[i]).String(); got != "(x: any) => string[]" {
				t.Errorf("reversed=%v callee %d: %s", rev, i, got)
			}
		}
	}
}
