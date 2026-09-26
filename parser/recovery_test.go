package parser_test

import (
	"strings"
	"testing"

	"KlainMainLang/diag"
	"KlainMainLang/parser"
)

// diagnostics parses src and returns its diagnostics as `line:col code`
// strings.
func diagnostics(t *testing.T, src string) []string {
	t.Helper()
	_, err := parser.Parse(src)
	var out []string
	for _, d := range diag.As(err) {
		out = append(out, strings.SplitN(d.Error(), ": ", 2)[0]+" "+itoa(d.Code()))
	}
	if err != nil && out == nil {
		t.Fatalf("error without diagnostics: %v", err)
	}
	return out
}

func itoa(n int) string {
	var b [12]byte
	i := len(b)
	for n > 0 || i == len(b) {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func TestRecoveryReportsEveryStatementError(t *testing.T) {
	for _, c := range []struct {
		name, src string
		want      []string
	}{
		{"statements", "let a = ;\nlet b = 1 2\nlet c = 3", []string{"1:9 1109", "2:11 1005"}},
		{"nested blocks", "function f() {\n  let b = 1 2\n  if (b) { c d }\n}\n}\nlet ok = 1", []string{"2:13 1005", "3:14 1005", "5:1 1109"}},
		{"switch clause", "switch (x) {\ncase 1:\n  let a = ;\ncase 2:\n  let b = ;\n}", []string{"3:11 1109", "5:11 1109"}},
		{"class members", "class A {\n  x: number = ;\n  m() { return 1 }\n  y = 1 2\n}\nlet z = ;", []string{"2:15 1109", "4:9 1005", "6:9 1109"}},
		{"method body closes the class", "class A {\n  m() { let v = ; }\n  n() { return ) }\n}", []string{"2:17 1109", "3:16 1109"}},
		{"clean", "let a = 1\nclass B { m() {} }", nil},
	} {
		got := diagnostics(t, c.src)
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

// The first diagnostic is the error the parser reported before recovery, and
// a token the parse stops at twice is reported once.
func TestRecoveryFirstErrorAndOnePerPosition(t *testing.T) {
	_, err := parser.Parse("let a = ;\n")
	if err == nil || err.Error() != "1:9: unexpected token ; in expression" {
		t.Fatalf("got %v", err)
	}
	if got := diagnostics(t, "function f() {"); len(got) != 1 {
		t.Errorf("unclosed function: %v", got)
	}
}

// Input the scanner cannot read is reported in place of the parse errors it
// causes.
func TestRecoveryScanError(t *testing.T) {
	_, err := parser.Parse("let a = 1\nlet s = \"unterminated\nlet b = 2")
	ds := diag.As(err)
	if len(ds) != 1 || ds[0].Code() != 1002 || ds[0].Error() != "2:9: unterminated string literal" {
		t.Fatalf("got %v", err)
	}
	// An independent error before the unreadable input is kept, in order.
	_, err = parser.Parse("let a = ;\nlet s = \"unterminated")
	if got := diag.As(err); len(got) != 2 || got[0].Code() != 1109 || got[1].Code() != 1002 {
		t.Fatalf("got %v", err)
	}
}

// A speculative parse still fails over to its other reading: recovery never
// turns its failure into a success.
func TestRecoveryLeavesSpeculationAlone(t *testing.T) {
	for _, src := range []string{
		"const f = (): (number[]) => []",
		"const lt = a < b > (c)",
		"let x = f<number>(1)",
	} {
		if _, err := parser.Parse(src); err != nil {
			t.Errorf("%s: %v", src, err)
		}
	}
}

func TestDiagnosticSpans(t *testing.T) {
	src := "let v: { [k: boolean]: number } = {}\nlet w = ;"
	_, err := parser.Parse(src)
	ds := diag.As(err)
	if len(ds) != 2 {
		t.Fatalf("got %v", err)
	}
	if d := ds[0]; d.Code() != 1268 || src[d.Start:d.End] != "boolean" {
		t.Errorf("index key: code %d span %q", d.Code(), src[d.Start:d.End])
	}
	if d := ds[1]; src[d.Start:d.End] != ";" || d.Message.Kind != diag.SyntaxError || d.Message.Phase != diag.PhaseParse {
		t.Errorf("expression: %+v span %q", d, src[d.Start:d.End])
	}
}
