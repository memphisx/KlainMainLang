package binder_test

import (
	"strconv"
	"strings"
	"testing"

	"KlainMainLang/binder"
	"KlainMainLang/parser"
)

// flowErrors binds src and renders its flow diagnostics as
// `line:col name code`.
func flowErrors(t *testing.T, src string) string {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("%s: %v", src, err)
	}
	var out []string
	for _, d := range binder.Bind(prog).FlowDiagnostics(nil, true) {
		name := d.Text[strings.IndexByte(d.Text, '\'')+1:]
		name = name[:strings.IndexByte(name, '\'')]
		out = append(out, d.Error()[:strings.Index(d.Error(), ": ")]+" "+name+" "+strconv.Itoa(d.Code()))
	}
	return strings.Join(out, ", ")
}

func TestTemporalDeadZone(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{"f(x)\nlet x = 1", "1:3 x 2448"},
		{"let x = 1\nf(x)", ""},
		// The block's own x shadows the outer one from the block's start.
		{"let x = 1\n{ f(x)\nlet x = 2 }", "2:5 x 2448"},
		// A closure may run later: exempt.
		{"const g = () => x\nlet x = 1", ""},
		{"function h() { return y }\nconst y = 2", ""},
		// Inside a loop, before the declaration after it.
		{"while (c) { f(z) }\nlet z = 1", "1:15 z 2448"},
		// A block-scoped binding is fresh on every iteration.
		{"for (const q of xs) { if (q) { f(w) }\nlet w = 1 }", "1:34 w 2448"},
		// A switch case that is only reached around the declaration.
		{"switch (v) { case 1: let s = 1; break\ncase 2: f(s) }", "2:11 s 2448"},
		// Reached after the declaration on some path: not certain, no error.
		{"switch (v) { case 1: let s = 1\ncase 2: f(s) }", ""},
		{"class C {}\nnew C()\nf(D)\nclass D {}", "3:3 D 2448"},
		{"f(E.A)\nenum E { A }", "1:3 E 2448"},
		// A pattern binds left to right: a later default reads an earlier name.
		{"for (const [v, m = String(v)] of xs) { f(m) }", ""},
		{"const [p, q = p] = xs", ""},
		{"const [r = s, s] = xs", "1:12 s 2448"},
		// A write in the dead zone throws too.
		{"let y = [y] = []", "1:10 y 2448"},
		// A parameter default does not see the body's declarations.
		{"function f(p = arguments) { let arguments = 1 }", ""},
		{"function g(p = q) { let q = 1 }\nconst q = 2", ""},
	} {
		if got := flowErrors(t, c.src); got != c.want {
			t.Errorf("%q: got %q, want %q", c.src, got, c.want)
		}
	}
}

func TestDefiniteAssignment(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{"let a: number\nf(a)", "2:3 a 2454"},
		// An unannotated let has an evolving type: not checked.
		{"let u\nf(u)", ""},
		{"let a: number\na = 1\nf(a)", ""},
		{"let a: number\nif (c) { a = 1 }\nf(a)", "3:3 a 2454"},
		{"let a: number\nif (c) { a = 1 } else { a = 2 }\nf(a)", ""},
		// A branch that leaves does not reach the read.
		{"function g(c: boolean) { let a: number\nif (c) { a = 1 } else { return }\nf(a) }", ""},
		{"let a: number\nwhile (c) { a = 1 }\nf(a)", "3:3 a 2454"},
		{"let a: number\ndo { a = 1 } while (c)\nf(a)", ""},
		{"let a: number\nfor (;;) { a = 1; break }\nf(a)", ""},
		{"let a: number\nwhile (true) { if (c) { a = 1; break } }\nf(a)", ""},
		// An initialized let is always assigned where its declaration has run.
		{"let a = 1\nf(a)", ""},
		// Compound assignment reads first.
		{"let a: number\na += 1", "2:1 a 2454"},
		// A destructuring assignment stores.
		{"let a: number\nlet b: number\n;[a, b] = [1, 2]\nf(a + b)", ""},
		{"let a: number\n;({ a } = o)\nf(a)", ""},
		// Exempt: any, nullable, var, a closure.
		{"let a: any\nf(a)", ""},
		{"let a: number | undefined\nf(a)", ""},
		{"var a: number\nf(a)", ""},
		{"let a: number\nconst g = () => a", ""},
		// A try block may not have run when the catch clause does.
		{"let a: number\ntry { a = 1 } catch (e) { f(a) }", "2:29 a 2454"},
		{"let a: number\ntry { a = 1 } catch (e) { a = 2 }\nf(a)", ""},
		{"let a: number\ntry { a = 1 } finally { }\nf(a)", ""},
		{"let a: number\nc && (a = 1)\nf(a)", "3:3 a 2454"},
		{"let a: number\nlabel: { a = 1; break label }\nf(a)", ""},
	} {
		if got := flowErrors(t, c.src); got != c.want {
			t.Errorf("%q: got %q, want %q", c.src, got, c.want)
		}
	}
}
